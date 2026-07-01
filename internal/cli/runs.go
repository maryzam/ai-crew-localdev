package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
	"github.com/spf13/cobra"
)

var runsCmd = &cobra.Command{
	Use:   "runs",
	Short: "Inspect managed-run history",
}

var (
	runsListJSON  bool
	runsListLimit int
	runsShowJSON  bool
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

func init() {
	runsCmd.AddCommand(runsListCmd)
	runsCmd.AddCommand(runsShowCmd)
	runsListCmd.Flags().BoolVar(&runsListJSON, "json", false, "write run history as JSON")
	runsListCmd.Flags().IntVar(&runsListLimit, "limit", 20, "maximum runs to display")
	runsShowCmd.Flags().BoolVar(&runsShowJSON, "json", false, "write the run as JSON")
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
