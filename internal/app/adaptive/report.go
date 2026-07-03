package adaptive

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
)

const (
	SchemaVersion               = "1"
	DefaultLookback             = 30 * 24 * time.Hour
	DefaultHighTokenThreshold   = int64(100000)
	DefaultRepeatedFailureRuns  = 2
	DefaultWeakVerificationRuns = 2
	DefaultMaxFindings          = 20
	maxEvidenceRunIDs           = 5
	maxCostAmountLength         = 32
)

type Options struct {
	Now                  time.Time
	Lookback             time.Duration
	HighTokenThreshold   int64
	RepeatedFailureRuns  int
	WeakVerificationRuns int
	MaxFindings          int
}

type Window struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
}

type Policy struct {
	HighTokenThreshold   int64 `json:"high_token_threshold"`
	RepeatedFailureRuns  int   `json:"repeated_failure_runs"`
	WeakVerificationRuns int   `json:"weak_verification_runs"`
	MaxFindings          int   `json:"max_findings"`
	MaxEvidenceRunIDs    int   `json:"max_evidence_run_ids"`
}

type Summary struct {
	Runs                int   `json:"runs"`
	Projects            int   `json:"projects"`
	FailedRuns          int   `json:"failed_runs"`
	UsageRuns           int   `json:"usage_runs"`
	CostRuns            int   `json:"cost_runs"`
	InvalidCostRuns     int   `json:"invalid_cost_runs"`
	TotalTokens         int64 `json:"total_tokens"`
	TokenTotalSaturated bool  `json:"token_total_saturated"`
}

type Coverage struct {
	Agent     string `json:"agent"`
	Provider  string `json:"provider,omitempty"`
	Runs      int    `json:"runs"`
	UsageRuns int    `json:"usage_runs"`
	CostRuns  int    `json:"cost_runs"`
}

type CostTotal struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

type Evidence struct {
	MatchedRuns         int      `json:"matched_runs,omitempty"`
	RunIDs              []string `json:"run_ids,omitempty"`
	Outcome             string   `json:"outcome,omitempty"`
	TerminalPhase       string   `json:"terminal_phase,omitempty"`
	FailureType         string   `json:"failure_type,omitempty"`
	TotalTokens         *int64   `json:"total_tokens,omitempty"`
	Agent               string   `json:"agent,omitempty"`
	Provider            string   `json:"provider,omitempty"`
	Model               string   `json:"model,omitempty"`
	ExtraAgentAttempts  int      `json:"extra_agent_attempts,omitempty"`
	ExtraVerifyAttempts int      `json:"extra_verify_attempts,omitempty"`
	UnverifiedRuns      int      `json:"unverified_runs,omitempty"`
	MissingUsageRuns    int      `json:"missing_usage_runs,omitempty"`
}

type Finding struct {
	Kind           string   `json:"kind"`
	Repository     string   `json:"repository"`
	Title          string   `json:"title"`
	Recommendation string   `json:"recommendation"`
	Evidence       Evidence `json:"evidence"`
	rank           int
	weight         int64
}

type Report struct {
	SchemaVersion     string      `json:"schema_version"`
	GeneratedAt       time.Time   `json:"generated_at"`
	Window            Window      `json:"window"`
	Policy            Policy      `json:"policy"`
	Summary           Summary     `json:"summary"`
	Coverage          []Coverage  `json:"coverage"`
	Costs             []CostTotal `json:"costs,omitempty"`
	Findings          []Finding   `json:"findings"`
	TruncatedFindings int         `json:"truncated_findings"`
}

type projectRuns struct {
	runs       int
	unverified int
}

type failureKey struct {
	repository string
	outcome    string
	phase      string
	failure    string
}

type runGroup struct {
	matched int
	runIDs  []string
}

type retryGroup struct {
	matched     int
	runIDs      []string
	extraAgent  int
	extraVerify int
}

type coverageKey struct {
	agent    string
	provider string
}

func DefaultOptions(now time.Time) Options {
	return Options{
		Now:                  now,
		Lookback:             DefaultLookback,
		HighTokenThreshold:   DefaultHighTokenThreshold,
		RepeatedFailureRuns:  DefaultRepeatedFailureRuns,
		WeakVerificationRuns: DefaultWeakVerificationRuns,
		MaxFindings:          DefaultMaxFindings,
	}
}

func Analyze(runs []telemetry.RunSummary, options Options) (Report, error) {
	if err := validateOptions(options); err != nil {
		return Report{}, err
	}
	now := options.Now.UTC()
	selected := selectRuns(runs, now.Add(-options.Lookback), now)
	report := Report{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   now,
		Window:        Window{Since: now.Add(-options.Lookback), Until: now},
		Policy: Policy{
			HighTokenThreshold:   options.HighTokenThreshold,
			RepeatedFailureRuns:  options.RepeatedFailureRuns,
			WeakVerificationRuns: options.WeakVerificationRuns,
			MaxFindings:          options.MaxFindings,
			MaxEvidenceRunIDs:    maxEvidenceRunIDs,
		},
	}

	projects := make(map[string]projectRuns)
	failures := make(map[failureKey]*runGroup)
	retries := make(map[string]*retryGroup)
	coverage := make(map[coverageKey]*Coverage)
	missingUsage := make(map[coverageKey]*runGroup)
	costs := make(map[string]*big.Rat)
	findings := make([]Finding, 0)

	for _, run := range selected {
		repository := normalizedRepository(run.Repository.Slug)
		project := projects[repository]
		project.runs++
		if !run.Execution.VerifyEnabled {
			project.unverified++
		}
		projects[repository] = project

		agent := normalizedAgent(run.Agent.Type)
		key := coverageKey{agent: agent, provider: run.Model.Provider}
		entry := coverage[key]
		if entry == nil {
			entry = &Coverage{Agent: agent, Provider: run.Model.Provider}
			coverage[key] = entry
		}
		entry.Runs++

		report.Summary.Runs++
		if isFailed(run.Outcome) {
			report.Summary.FailedRuns++
			failure := firstNonEmpty(run.Diagnostics.ErrorType, run.Outcome)
			failureGroup := failures[failureKey{repository: repository, outcome: run.Outcome, phase: run.TerminalPhase, failure: failure}]
			if failureGroup == nil {
				failureGroup = &runGroup{}
				failures[failureKey{repository: repository, outcome: run.Outcome, phase: run.TerminalPhase, failure: failure}] = failureGroup
			}
			addRun(failureGroup, run.RunID)
		}

		if isOptimizationUsage(run.Usage) {
			entry.UsageRuns++
			report.Summary.UsageRuns++
			report.Summary.TotalTokens, report.Summary.TokenTotalSaturated = addTokens(report.Summary.TotalTokens, *run.Usage.TotalTokens, report.Summary.TokenTotalSaturated)
			if *run.Usage.TotalTokens >= options.HighTokenThreshold {
				tokens := *run.Usage.TotalTokens
				findings = append(findings, Finding{
					Kind:           "high_token_run",
					Repository:     repository,
					Title:          fmt.Sprintf("run used %d tokens", tokens),
					Recommendation: "Split the task or reduce loaded context, then compare the next run against the emitted token threshold.",
					Evidence:       Evidence{MatchedRuns: 1, RunIDs: []string{run.RunID}, TotalTokens: &tokens, Agent: agent, Model: displayModel(run)},
					rank:           2,
					weight:         tokens,
				})
			}
		} else if run.Outcome == telemetry.OutcomePassed {
			group := missingUsage[key]
			if group == nil {
				group = &runGroup{}
				missingUsage[key] = group
			}
			addRun(group, run.RunID)
		}

		if isOptimizationUsage(run.Usage) && run.Usage.CostAmount != nil && run.Usage.CostCurrency != "" {
			amount, ok := parseCost(*run.Usage.CostAmount)
			if ok {
				entry.CostRuns++
				report.Summary.CostRuns++
				currency := strings.ToUpper(run.Usage.CostCurrency)
				if costs[currency] == nil {
					costs[currency] = new(big.Rat)
				}
				costs[currency].Add(costs[currency], amount)
			} else {
				report.Summary.InvalidCostRuns++
			}
		}

		extraAgent := max(run.Execution.AgentAttempts-1, 0)
		extraVerify := max(run.Execution.VerifyAttempts-1, 0)
		if extraAgent+extraVerify > 0 {
			group := retries[repository]
			if group == nil {
				group = &retryGroup{}
				retries[repository] = group
			}
			group.matched++
			group.extraAgent += extraAgent
			group.extraVerify += extraVerify
			group.runIDs = appendEvidenceRun(group.runIDs, run.RunID)
		}
	}

	report.Summary.Projects = len(projects)
	report.Coverage = sortedCoverage(coverage)
	report.Costs = sortedCosts(costs)
	findings = append(findings, failureFindings(failures, options.RepeatedFailureRuns)...)
	findings = append(findings, retryFindings(retries)...)
	findings = append(findings, verificationFindings(projects, options.WeakVerificationRuns)...)
	findings = append(findings, usageFindings(missingUsage)...)
	sortFindings(findings)
	if len(findings) > options.MaxFindings {
		report.TruncatedFindings = len(findings) - options.MaxFindings
		findings = findings[:options.MaxFindings]
	}
	report.Findings = findings
	return report, nil
}

func validateOptions(options Options) error {
	if options.Now.IsZero() {
		return errors.New("analysis time must be set")
	}
	if options.Lookback <= 0 {
		return errors.New("lookback must be positive")
	}
	if options.HighTokenThreshold <= 0 {
		return errors.New("high-token threshold must be positive")
	}
	if options.RepeatedFailureRuns < 2 {
		return errors.New("repeated-failure minimum must be at least 2")
	}
	if options.WeakVerificationRuns < 1 {
		return errors.New("weak-verification minimum must be positive")
	}
	if options.MaxFindings < 1 {
		return errors.New("finding limit must be positive")
	}
	return nil
}

func selectRuns(runs []telemetry.RunSummary, since, until time.Time) []telemetry.RunSummary {
	selected := make([]telemetry.RunSummary, 0, len(runs))
	for _, run := range runs {
		if run.StartedAt.Before(since) || run.StartedAt.After(until) {
			continue
		}
		selected = append(selected, run)
	}
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].StartedAt.Equal(selected[j].StartedAt) {
			return selected[i].RunID < selected[j].RunID
		}
		return selected[i].StartedAt.Before(selected[j].StartedAt)
	})
	return selected
}

func failureFindings(groups map[failureKey]*runGroup, minimum int) []Finding {
	findings := make([]Finding, 0)
	for key, group := range groups {
		if group.matched < minimum {
			continue
		}
		findings = append(findings, Finding{
			Kind:           "repeated_failure",
			Repository:     key.repository,
			Title:          fmt.Sprintf("%s repeated %d times in %s", key.failure, group.matched, firstNonEmpty(key.phase, "unknown phase")),
			Recommendation: "Add or tighten a deterministic check for this failure and fix the recurring cause before rerunning.",
			Evidence:       Evidence{MatchedRuns: group.matched, RunIDs: group.runIDs, Outcome: key.outcome, TerminalPhase: key.phase, FailureType: key.failure},
			rank:           0,
			weight:         int64(group.matched),
		})
	}
	return findings
}

func retryFindings(groups map[string]*retryGroup) []Finding {
	findings := make([]Finding, 0, len(groups))
	for repository, group := range groups {
		findings = append(findings, Finding{
			Kind:           "retry_waste",
			Repository:     repository,
			Title:          fmt.Sprintf("%d runs consumed %d extra attempts", group.matched, group.extraAgent+group.extraVerify),
			Recommendation: "Review the retry causes and lower the retry budget until failures have a classified, actionable retry policy.",
			Evidence:       Evidence{MatchedRuns: group.matched, RunIDs: group.runIDs, ExtraAgentAttempts: group.extraAgent, ExtraVerifyAttempts: group.extraVerify},
			rank:           1,
			weight:         int64(group.extraAgent + group.extraVerify),
		})
	}
	return findings
}

func verificationFindings(projects map[string]projectRuns, minimum int) []Finding {
	findings := make([]Finding, 0)
	for repository, project := range projects {
		if project.runs < minimum || project.unverified != project.runs {
			continue
		}
		findings = append(findings, Finding{
			Kind:           "weak_verification",
			Repository:     repository,
			Title:          fmt.Sprintf("none of %d runs enabled verification", project.runs),
			Recommendation: "Add a deterministic --verify-cmd for this project so managed runs produce executable quality evidence.",
			Evidence:       Evidence{MatchedRuns: project.runs, UnverifiedRuns: project.unverified},
			rank:           3,
			weight:         int64(project.runs),
		})
	}
	return findings
}

func usageFindings(groups map[coverageKey]*runGroup) []Finding {
	findings := make([]Finding, 0, len(groups))
	for key, group := range groups {
		findings = append(findings, Finding{
			Kind:           "usage_coverage_gap",
			Repository:     "all projects",
			Title:          fmt.Sprintf("%d successful %s runs lacked provider usage", group.matched, key.agent),
			Recommendation: "Verify the pinned agent telemetry event contract before using token totals for optimization decisions.",
			Evidence:       Evidence{MatchedRuns: group.matched, RunIDs: group.runIDs, Agent: key.agent, Provider: key.provider, MissingUsageRuns: group.matched},
			rank:           0,
			weight:         int64(group.matched),
		})
	}
	return findings
}

func sortedCoverage(entries map[coverageKey]*Coverage) []Coverage {
	result := make([]Coverage, 0, len(entries))
	for _, entry := range entries {
		result = append(result, *entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Agent == result[j].Agent {
			return result[i].Provider < result[j].Provider
		}
		return result[i].Agent < result[j].Agent
	})
	return result
}

func sortedCosts(costs map[string]*big.Rat) []CostTotal {
	result := make([]CostTotal, 0, len(costs))
	for currency, amount := range costs {
		result = append(result, CostTotal{Currency: currency, Amount: formatCost(amount)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Currency < result[j].Currency })
	return result
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].rank != findings[j].rank {
			return findings[i].rank < findings[j].rank
		}
		if findings[i].weight != findings[j].weight {
			return findings[i].weight > findings[j].weight
		}
		if findings[i].Repository != findings[j].Repository {
			return findings[i].Repository < findings[j].Repository
		}
		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind < findings[j].Kind
		}
		return firstRunID(findings[i]) < firstRunID(findings[j])
	})
}

func addRun(group *runGroup, runID string) {
	group.matched++
	group.runIDs = appendEvidenceRun(group.runIDs, runID)
}

func appendEvidenceRun(runIDs []string, runID string) []string {
	if len(runIDs) >= maxEvidenceRunIDs {
		return runIDs
	}
	return append(runIDs, runID)
}

func addTokens(total, value int64, saturated bool) (int64, bool) {
	if value <= 0 {
		return total, saturated
	}
	if total > math.MaxInt64-value {
		return math.MaxInt64, true
	}
	return total + value, saturated
}

func parseCost(value string) (*big.Rat, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxCostAmountLength || strings.HasPrefix(value, "-") {
		return nil, false
	}
	digits := 0
	dots := 0
	for _, character := range value {
		switch {
		case character >= '0' && character <= '9':
			digits++
		case character == '.':
			dots++
		default:
			return nil, false
		}
	}
	if digits == 0 || dots > 1 {
		return nil, false
	}
	amount, ok := new(big.Rat).SetString(value)
	return amount, ok
}

func formatCost(value *big.Rat) string {
	formatted := value.FloatString(6)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	if formatted == "" {
		return "0"
	}
	return formatted
}

func displayModel(run telemetry.RunSummary) string {
	return firstNonEmpty(run.Model.Observed, run.Model.Requested, run.Model.Family, run.Model.Provider)
}

func normalizedRepository(value string) string {
	return firstNonEmpty(strings.TrimSpace(value), "unresolved")
}

func normalizedAgent(value string) string {
	return firstNonEmpty(strings.TrimSpace(value), "other")
}

func isFailed(outcome string) bool {
	return outcome != "" && outcome != telemetry.OutcomePassed
}

func isOptimizationUsage(usage *telemetry.Usage) bool {
	return usage != nil && usage.TotalTokens != nil && usage.Scope == "run" && usage.Precision == "request" && usage.Confidence == "provider_reported"
}

func firstRunID(finding Finding) string {
	if len(finding.Evidence.RunIDs) == 0 {
		return ""
	}
	return finding.Evidence.RunIDs[0]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
