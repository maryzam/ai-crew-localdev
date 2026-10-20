package cli

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtelemetry"
	"github.com/spf13/cobra"
)

func newLogsCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "logs [run-id]",
		Short:         "Inspect authoritative local run evidence",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(command *cobra.Command, args []string) error {
			root, err := evidence.LoadRoot(paths.EvidenceDir())
			if err != nil {
				return err
			}
			run := ""
			if len(args) == 1 {
				run = args[0]
			} else {
				run, err = root.LatestRun()
				if err != nil {
					return err
				}
			}
			summary, err := runtelemetry.Summarize(root, run)
			if err != nil {
				return err
			}
			writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintf(writer, "Run:\t%s\nTelemetry bytes:\t%d\n", summary.Run, summary.TelemetryBytes)
			signals := make([]string, 0, len(summary.Records))
			for signal := range summary.Records {
				signals = append(signals, signal)
			}
			sort.Strings(signals)
			for _, signal := range signals {
				_, _ = fmt.Fprintf(writer, "%s records:\t%d\n", signal, summary.Records[signal])
			}
			if summary.Receiver != nil {
				_, _ = fmt.Fprintf(writer, "Stored requests:\t%d\nRejected requests:\t%d\nStorage failures:\t%d\nProjected requests:\t%d\nProjection failures:\t%d\n", summary.Receiver.Stored, summary.Receiver.Rejected, summary.Receiver.Failed, summary.Receiver.Projected, summary.Receiver.ProjectionFailed)
			}
			_, _ = fmt.Fprintf(writer, "Pruned telemetry files:\t%d\n", len(summary.Pruned))
			return writer.Flush()
		},
	}
}
