package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/app/adaptive"
	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
	"github.com/spf13/cobra"
)

var runsCmd = &cobra.Command{
	Use:   "runs",
	Short: "Inspect managed-run history",
}

var (
	runsListJSON                 bool
	runsListLimit                int
	runsShowJSON                 bool
	runsAnalyzeJSON              bool
	runsAnalyzeSince             time.Duration
	runsAnalyzeHighTokens        int64
	runsAnalyzeRepeatedFailures  int
	runsAnalyzeUnverifiedRuns    int
	runsAnalyzeUnverifiedPercent int
	runsAnalyzeMaxFindings       int
)

var runsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent managed runs",
	Args:  cobra.NoArgs,
	RunE:  runRunsList,
}

var runsShowCmd = &cobra.Command{
	Use:   "show <run-id>",
	Short: "Show one managed run",
	Args:  cobra.ExactArgs(1),
	RunE:  runRunsShow,
}

var runsAnalyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Produce advisory optimization recommendations",
	Args:  cobra.NoArgs,
	RunE:  runRunsAnalyze,
}

func init() {
	runsCmd.AddCommand(runsListCmd)
	runsCmd.AddCommand(runsShowCmd)
	runsCmd.AddCommand(runsAnalyzeCmd)
	runsListCmd.Flags().BoolVar(&runsListJSON, "json", false, "write run history as JSON")
	runsListCmd.Flags().IntVar(&runsListLimit, "limit", 20, "maximum runs to display")
	runsShowCmd.Flags().BoolVar(&runsShowJSON, "json", false, "write the run as JSON")
	runsAnalyzeCmd.Flags().BoolVar(&runsAnalyzeJSON, "json", false, "write the advisory report as JSON")
	runsAnalyzeCmd.Flags().DurationVar(&runsAnalyzeSince, "since", adaptive.DefaultLookback, "history window to analyze")
	runsAnalyzeCmd.Flags().Int64Var(&runsAnalyzeHighTokens, "high-tokens", adaptive.DefaultHighTokenThreshold, "token threshold for a high-token run")
	runsAnalyzeCmd.Flags().IntVar(&runsAnalyzeRepeatedFailures, "min-repeated-failures", adaptive.DefaultRepeatedFailureRuns, "matching failures required for a recurring-failure finding")
	runsAnalyzeCmd.Flags().IntVar(&runsAnalyzeUnverifiedRuns, "min-unverified-runs", adaptive.DefaultWeakVerificationRuns, "unverified project runs required for a weak-verification finding")
	runsAnalyzeCmd.Flags().IntVar(&runsAnalyzeUnverifiedPercent, "min-unverified-percent", adaptive.DefaultWeakVerificationPercent, "minimum unverified percentage for a weak-verification finding")
	runsAnalyzeCmd.Flags().IntVar(&runsAnalyzeMaxFindings, "max-findings", adaptive.DefaultMaxFindings, "maximum recommendations to emit")
}

func runRunsList(cmd *cobra.Command, _ []string) error {
	runs, err := telemetry.ReadRunHistory(telemetry.LocalTelemetryPath())
	if err != nil {
		return err
	}
	if runsListLimit < 0 {
		return fmt.Errorf("limit must be non-negative")
	}
	if runsListLimit > 0 && len(runs) > runsListLimit {
		runs = runs[:runsListLimit]
	}
	if runsListJSON {
		return writeCommandJSON(cmd, runs)
	}
	if len(runs) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no managed runs")
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "RUN ID\tSTARTED\tOUTCOME\tAGENT\tMODEL\tTOKENS\tREPOSITORY")
	for _, run := range runs {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			telemetry.ShortRunID(run.RunID),
			run.StartedAt.Local().Format("2006-01-02 15:04:05"),
			valueOr(run.Outcome, "incomplete"),
			run.Agent.Type,
			displayModel(run),
			displayTokens(run.Usage),
			run.Repository.Slug,
		)
	}
	return w.Flush()
}

func runRunsShow(cmd *cobra.Command, args []string) error {
	runs, err := telemetry.ReadRunHistory(telemetry.LocalTelemetryPath())
	if err != nil {
		return err
	}
	run, err := telemetry.FindRun(runs, args[0])
	if err != nil {
		return err
	}
	if runsShowJSON {
		return writeCommandJSON(cmd, run)
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "Run ID:\t%s\n", run.RunID)
	_, _ = fmt.Fprintf(w, "Trace ID:\t%s\n", run.TraceID)
	_, _ = fmt.Fprintf(w, "Started:\t%s\n", run.StartedAt.Local().Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "Outcome:\t%s\n", valueOr(run.Outcome, "incomplete"))
	_, _ = fmt.Fprintf(w, "Terminal phase:\t%s\n", valueOr(run.TerminalPhase, "unknown"))
	_, _ = fmt.Fprintf(w, "Duration:\t%s\n", time.Duration(run.DurationMS)*time.Millisecond)
	_, _ = fmt.Fprintf(w, "Repository:\t%s\n", run.Repository.Slug)
	_, _ = fmt.Fprintf(w, "Commit:\t%s\n", valueOr(run.Repository.CommitSHA, "unresolved"))
	_, _ = fmt.Fprintf(w, "Branch:\t%s\n", valueOr(run.Repository.Branch, "unresolved"))
	_, _ = fmt.Fprintf(w, "Dirty:\t%t\n", run.Repository.Dirty)
	_, _ = fmt.Fprintf(w, "Agent:\t%s (%s)\n", run.Agent.Identity, run.Agent.Type)
	_, _ = fmt.Fprintf(w, "Model:\t%s\n", displayModel(run))
	_, _ = fmt.Fprintf(w, "Model source:\t%s (%s)\n", valueOr(run.Model.Resolution.PrimarySource, "none"), run.Model.Resolution.Confidence)
	if run.Model.Requested != "" && run.Model.Observed != "" && run.Model.Requested != run.Model.Observed {
		_, _ = fmt.Fprintf(w, "Requested model:\t%s\n", run.Model.Requested)
	}
	_, _ = fmt.Fprintf(w, "Agent attempts:\t%d\n", run.Execution.AgentAttempts)
	_, _ = fmt.Fprintf(w, "Verification:\t%s (%d attempts)\n", run.Verification.Outcome, run.Execution.VerifyAttempts)
	for _, contract := range run.Verification.Contracts {
		detail := fmt.Sprintf("%s (%d attempts", contract.Outcome, contract.Attempts)
		if contract.FailureClass != "" {
			detail += ", " + contract.FailureClass
		}
		detail += ")"
		_, _ = fmt.Fprintf(w, "Contract %s:\t%s\n", contract.Name, detail)
	}
	if run.Usage != nil {
		_, _ = fmt.Fprintf(w, "Tokens:\t%s (%s, %s)\n", displayTokens(run.Usage), run.Usage.Status, run.Usage.Source)
		if run.Usage.CostAmount != nil {
			_, _ = fmt.Fprintf(w, "Estimated cost:\t%s %s\n", *run.Usage.CostAmount, run.Usage.CostCurrency)
		}
	}
	_, _ = fmt.Fprintf(w, "Broker session:\t%s\n", valueOr(run.Broker.SessionID, "not created"))
	if run.Task.Ref != "" {
		_, _ = fmt.Fprintf(w, "Task:\t%s\n", run.Task.Ref)
	}
	return w.Flush()
}

func runRunsAnalyze(cmd *cobra.Command, _ []string) error {
	runs, err := telemetry.ReadRunHistory(telemetry.LocalTelemetryPath())
	if err != nil {
		return err
	}
	options := adaptive.Options{
		Now:                     time.Now().UTC(),
		Lookback:                runsAnalyzeSince,
		HighTokenThreshold:      runsAnalyzeHighTokens,
		RepeatedFailureRuns:     runsAnalyzeRepeatedFailures,
		WeakVerificationRuns:    runsAnalyzeUnverifiedRuns,
		WeakVerificationPercent: runsAnalyzeUnverifiedPercent,
		MaxFindings:             runsAnalyzeMaxFindings,
	}
	report, err := adaptive.Analyze(runs, options)
	if err != nil {
		return err
	}
	if runsAnalyzeJSON {
		return writeCommandJSON(cmd, report)
	}
	return writeAdaptiveReport(cmd, report)
}

func writeAdaptiveReport(cmd *cobra.Command, report adaptive.Report) error {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, "Adaptive optimization report")
	_, _ = fmt.Fprintf(out, "Window: %s to %s\n", report.Window.Since.Format(time.RFC3339), report.Window.Until.Format(time.RFC3339))
	_, _ = fmt.Fprintf(out, "Runs: %d across %d projects; failures: %d; tokens: %d\n", report.Summary.Runs, report.Summary.Projects, report.Summary.FailedRuns, report.Summary.TotalTokens)
	_, _ = fmt.Fprintf(out, "Policy: high tokens >= %d; repeated failures >= %d; unverified runs >= %d and %d%%; findings <= %d\n", report.Policy.HighTokenThreshold, report.Policy.RepeatedFailureRuns, report.Policy.WeakVerificationRuns, report.Policy.WeakVerificationPercent, report.Policy.MaxFindings)
	if len(report.Coverage) > 0 {
		_, _ = fmt.Fprintln(out, "\nUsage coverage:")
		writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, "AGENT\tPROVIDER\tRUNS\tTRUSTED\tOTHER\tMISSING\tCOST")
		for _, coverage := range report.Coverage {
			_, _ = fmt.Fprintf(writer, "%s\t%s\t%d\t%d\t%d\t%d\t%d\n", coverage.Agent, valueOr(coverage.Provider, "unresolved"), coverage.Runs, coverage.UsageRuns, coverage.OtherUsageRuns, coverage.MissingUsageRuns, coverage.CostRuns)
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
	if len(report.Costs) > 0 {
		values := make([]string, 0, len(report.Costs))
		for _, cost := range report.Costs {
			values = append(values, cost.Amount+" "+cost.Currency)
		}
		_, _ = fmt.Fprintf(out, "Cost totals: %s\n", strings.Join(values, ", "))
	}
	_, _ = fmt.Fprintln(out, "\nRecommendations:")
	if len(report.Findings) == 0 {
		_, _ = fmt.Fprintln(out, "- none for the selected window and policy")
	} else {
		for _, finding := range report.Findings {
			_, _ = fmt.Fprintf(out, "- [%s] %s: %s\n", finding.Kind, finding.Repository, finding.Title)
			_, _ = fmt.Fprintf(out, "  Evidence: %s\n", formatAdaptiveEvidence(finding.Evidence))
			_, _ = fmt.Fprintf(out, "  Action: %s\n", finding.Recommendation)
		}
	}
	if report.TruncatedFindings > 0 {
		_, _ = fmt.Fprintf(out, "%d additional findings omitted by the configured limit.\n", report.TruncatedFindings)
	}
	_, _ = fmt.Fprintln(out, "Advisory only: no project files or policy were changed.")
	return nil
}

func formatAdaptiveEvidence(evidence adaptive.Evidence) string {
	values := make([]string, 0, 16)
	if evidence.MatchedRuns > 0 {
		values = append(values, fmt.Sprintf("matched_runs=%d", evidence.MatchedRuns))
	}
	if len(evidence.RunIDs) > 0 {
		runIDs := make([]string, 0, len(evidence.RunIDs))
		for _, runID := range evidence.RunIDs {
			runIDs = append(runIDs, telemetry.ShortRunID(runID))
		}
		values = append(values, "run_ids="+strings.Join(runIDs, ","))
	}
	if evidence.TotalTokens != nil {
		values = append(values, fmt.Sprintf("tokens=%d", *evidence.TotalTokens))
	}
	if evidence.TokenTotalSaturated {
		values = append(values, "tokens_saturated=true")
	}
	if evidence.PeakTokens != nil {
		values = append(values, fmt.Sprintf("peak_tokens=%d", *evidence.PeakTokens))
	}
	if evidence.Outcome != "" {
		values = append(values, "outcome="+evidence.Outcome)
	}
	if evidence.Agent != "" {
		values = append(values, "agent="+evidence.Agent)
	}
	if evidence.Provider != "" {
		values = append(values, "provider="+evidence.Provider)
	}
	if evidence.Source != "" {
		values = append(values, "source="+evidence.Source)
	}
	if evidence.Scope != "" {
		values = append(values, "scope="+evidence.Scope)
	}
	if evidence.Precision != "" {
		values = append(values, "precision="+evidence.Precision)
	}
	if evidence.Confidence != "" {
		values = append(values, "confidence="+evidence.Confidence)
	}
	if evidence.TerminalPhase != "" {
		values = append(values, "phase="+evidence.TerminalPhase)
	}
	if evidence.ExtraAgentAttempts > 0 {
		values = append(values, fmt.Sprintf("extra_agent_attempts=%d", evidence.ExtraAgentAttempts))
	}
	if evidence.ExtraVerifyAttempts > 0 {
		values = append(values, fmt.Sprintf("extra_verify_attempts=%d", evidence.ExtraVerifyAttempts))
	}
	if evidence.UnverifiedRuns > 0 {
		values = append(values, fmt.Sprintf("unverified_runs=%d", evidence.UnverifiedRuns))
	}
	if evidence.VerificationPercent > 0 {
		values = append(values, fmt.Sprintf("unverified_percent=%d", evidence.VerificationPercent))
	}
	if evidence.MissingUsageRuns > 0 {
		values = append(values, fmt.Sprintf("missing_usage_runs=%d", evidence.MissingUsageRuns))
	}
	if evidence.OtherUsageRuns > 0 {
		values = append(values, fmt.Sprintf("other_usage_runs=%d", evidence.OtherUsageRuns))
	}
	return strings.Join(values, " ")
}

func writeCommandJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func displayModel(run telemetry.RunSummary) string {
	return valueOr(run.Model.Observed, run.Model.Requested, run.Model.Family, run.Model.Provider, "unresolved")
}

func displayTokens(usage *telemetry.Usage) string {
	if usage == nil || usage.TotalTokens == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%d", *usage.TotalTokens)
}

func valueOr(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
