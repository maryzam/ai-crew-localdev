package adaptive

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
)

func TestAnalyzeProducesCoverageAndActionableFindings(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	runs := []telemetry.RunSummary{
		testRun("run_failure_1", now.Add(-6*time.Hour), "owner/a", "claude_code", "anthropic", telemetry.OutcomeVerifyFailed, telemetry.PhaseVerify, true, 2, 2, 400, "0.10", "verify_failed"),
		testRun("run_failure_2", now.Add(-5*time.Hour), "owner/a", "claude_code", "anthropic", telemetry.OutcomeVerifyFailed, telemetry.PhaseVerify, true, 2, 2, 500, "0.20", "verify_failed"),
		testRun("run_high", now.Add(-4*time.Hour), "owner/b", "codex", "openai", telemetry.OutcomePassed, telemetry.PhaseAgent, true, 1, 0, 2000, "", ""),
		testRun("run_unverified_1", now.Add(-3*time.Hour), "owner/c", "codex", "openai", telemetry.OutcomePassed, telemetry.PhaseAgent, false, 1, 0, 100, "", ""),
		testRun("run_unverified_2", now.Add(-2*time.Hour), "owner/c", "codex", "openai", telemetry.OutcomePassed, telemetry.PhaseAgent, false, 1, 0, 100, "", ""),
		testRun("run_missing", now.Add(-time.Hour), "owner/d", "codex", "openai", telemetry.OutcomePassed, telemetry.PhaseAgent, true, 1, 0, 0, "", ""),
	}
	options := DefaultOptions(now)
	options.HighTokenThreshold = 1000
	report, err := Analyze(runs, options)
	if err != nil {
		t.Fatal(err)
	}

	if report.Summary.Runs != 6 || report.Summary.Projects != 4 || report.Summary.FailedRuns != 2 || report.Summary.UsageRuns != 5 || report.Summary.CostRuns != 2 || report.Summary.TotalTokens != 3100 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if len(report.Costs) != 1 || report.Costs[0].Currency != "USD" || report.Costs[0].Amount != "0.3" {
		t.Fatalf("costs = %#v", report.Costs)
	}
	if len(report.Coverage) != 2 {
		t.Fatalf("coverage = %#v", report.Coverage)
	}
	kinds := make([]string, 0, len(report.Findings))
	for _, finding := range report.Findings {
		kinds = append(kinds, finding.Kind)
	}
	for _, expected := range []string{"repeated_failure", "retry_waste", "high_token_run", "weak_verification", "usage_coverage_gap"} {
		if !slices.Contains(kinds, expected) {
			t.Errorf("findings missing %q: %#v", expected, report.Findings)
		}
	}
	if report.Findings[0].Kind != "repeated_failure" || report.Findings[0].Evidence.MatchedRuns != 2 {
		t.Fatalf("first finding = %#v", report.Findings[0])
	}
}

func TestAnalyzeAppliesWindowAndFindingBudgets(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	runs := []telemetry.RunSummary{
		testRun("run_old", now.Add(-31*24*time.Hour), "owner/old", "codex", "openai", telemetry.OutcomePassed, telemetry.PhaseAgent, true, 1, 0, 2000, "", ""),
		testRun("run_current_1", now.Add(-2*time.Hour), "owner/current", "codex", "openai", telemetry.OutcomePassed, telemetry.PhaseAgent, false, 1, 0, 2000, "", ""),
		testRun("run_current_2", now.Add(-time.Hour), "owner/current", "codex", "openai", telemetry.OutcomePassed, telemetry.PhaseAgent, false, 1, 0, 3000, "", ""),
		testRun("run_future", now.Add(time.Hour), "owner/future", "codex", "openai", telemetry.OutcomePassed, telemetry.PhaseAgent, true, 1, 0, 4000, "", ""),
	}
	options := DefaultOptions(now)
	options.HighTokenThreshold = 1000
	options.MaxFindings = 1
	report, err := Analyze(runs, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Runs != 2 || report.Summary.Projects != 1 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if len(report.Findings) != 1 || report.TruncatedFindings != 2 {
		t.Fatalf("finding budget not enforced: %#v", report)
	}
	if report.Findings[0].Kind != "high_token_run" || report.Findings[0].Evidence.TotalTokens == nil || *report.Findings[0].Evidence.TotalTokens != 3000 {
		t.Fatalf("finding order = %#v", report.Findings)
	}
}

func TestAnalyzeRejectsInvalidPolicy(t *testing.T) {
	options := DefaultOptions(time.Now())
	options.MaxFindings = 0
	if _, err := Analyze(nil, options); err == nil {
		t.Fatal("expected invalid finding limit to fail")
	}
}

func TestAnalyzeBoundsInvalidCostAndTokenOverflow(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	runs := []telemetry.RunSummary{
		testRun("run_max", now.Add(-2*time.Hour), "owner/a", "claude_code", "anthropic", telemetry.OutcomePassed, telemetry.PhaseAgent, true, 1, 0, math.MaxInt64, "1/2", ""),
		testRun("run_overflow", now.Add(-time.Hour), "owner/a", "claude_code", "anthropic", telemetry.OutcomePassed, telemetry.PhaseAgent, true, 1, 0, 10, "", ""),
		testRun("run_estimated", now.Add(-30*time.Minute), "owner/a", "claude_code", "anthropic", telemetry.OutcomePassed, telemetry.PhaseAgent, true, 1, 0, 200, "0.5", ""),
	}
	runs[2].Usage.Confidence = "estimated"
	options := DefaultOptions(now)
	options.HighTokenThreshold = math.MaxInt64
	report, err := Analyze(runs, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.TotalTokens != math.MaxInt64 || !report.Summary.TokenTotalSaturated || report.Summary.UsageRuns != 2 || report.Summary.InvalidCostRuns != 1 || report.Summary.CostRuns != 0 {
		t.Fatalf("summary = %#v", report.Summary)
	}
}

func testRun(runID string, started time.Time, repository, agent, provider, outcome, phase string, verify bool, agentAttempts, verifyAttempts int, tokens int64, cost, failure string) telemetry.RunSummary {
	run := telemetry.RunSummary{
		RunID:         runID,
		StartedAt:     started,
		Outcome:       outcome,
		TerminalPhase: phase,
		Repository:    telemetry.RepositoryMetadata{Slug: repository},
		Agent:         telemetry.AgentMetadata{Type: agent},
		Model:         telemetry.ModelAttribution{Provider: provider, Observed: "test-model"},
		Execution:     telemetry.ExecutionSummary{AgentAttempts: agentAttempts, VerifyEnabled: verify, VerifyAttempts: verifyAttempts},
		Diagnostics:   telemetry.DiagnosticMetadata{ErrorType: failure},
	}
	if tokens > 0 {
		run.Usage = &telemetry.Usage{Status: "observed", TotalTokens: &tokens, Source: "native_otel", Scope: "run", Precision: "request", Confidence: "provider_reported"}
		if cost != "" {
			run.Usage.CostAmount = &cost
			run.Usage.CostCurrency = "USD"
		}
	}
	return run
}
