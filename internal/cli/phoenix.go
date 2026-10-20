package cli

import (
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/phoenix"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtelemetry"
	"github.com/spf13/cobra"
)

func newPhoenixCommand() *cobra.Command {
	command := &cobra.Command{Use: "phoenix", Short: "Manage the rebuildable local telemetry view"}
	var run string
	rebuild := &cobra.Command{
		Use:           "rebuild",
		Short:         "Replay authoritative span evidence into Phoenix",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(command *cobra.Command, _ []string) error {
			root, err := evidence.LoadRoot(paths.EvidenceDir())
			if err != nil {
				return err
			}
			projector, err := phoenix.NewProjector(phoenix.DefaultCollectorEndpoint)
			if err != nil {
				return err
			}
			result, err := runtelemetry.Replay(commandContext(command), root, projector, run)
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Phoenix rebuild: runs %d, envelopes %d, batches %d, failed %d\n", result.Runs, result.Envelopes, result.Batches, result.Failed)
			if err != nil {
				return fmt.Errorf("phoenix rebuild incomplete: %w", err)
			}
			return nil
		},
	}
	rebuild.Flags().StringVar(&run, "run", "", "replay one run instead of all retained evidence")
	command.AddCommand(rebuild)
	return command
}
