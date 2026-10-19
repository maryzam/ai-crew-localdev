package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
	"github.com/maryzam/ai-crew-localdev/internal/phoenix"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/spf13/cobra"
)

const phoenixRebuildBudget = 30 * time.Second

func newPhoenixCommand() *cobra.Command {
	command := &cobra.Command{Use: "phoenix", Short: "Manage the rebuildable local telemetry view"}
	command.AddCommand(&cobra.Command{
		Use:           "rebuild",
		Short:         "Replay authoritative span evidence into Phoenix",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(command *cobra.Command, _ []string) error {
			root, err := evidence.OpenRoot(paths.EvidenceDir())
			if err != nil {
				return err
			}
			projector, err := phoenix.NewProjector(phoenix.DefaultCollectorEndpoint)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(commandContext(command), phoenixRebuildBudget)
			defer cancel()
			result, err := projector.Rebuild(ctx, root)
			_, _ = fmt.Fprintf(command.OutOrStdout(), "Phoenix rebuild: projected %d, failed %d\n", result.Projected, result.Failed)
			if err != nil {
				return fmt.Errorf("phoenix rebuild incomplete with a %s budget: %w", phoenixRebuildBudget, err)
			}
			return nil
		},
	})
	return command
}
