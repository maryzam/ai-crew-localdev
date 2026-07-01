package cli

import (
	"fmt"
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/app/agentdefaults"
	"github.com/spf13/cobra"
)

var bootstrapQuiet bool

var bootstrapCmd = &cobra.Command{
	Use:          "bootstrap",
	Short:        "Install missing agent guidance and skills",
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runBootstrap,
}

func init() {
	bootstrapCmd.Flags().BoolVar(&bootstrapQuiet, "quiet", false, "hide installed and preserved paths")
}

func runBootstrap(cmd *cobra.Command, _ []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	result, err := agentdefaults.Install(home)
	if err != nil {
		return err
	}
	if bootstrapQuiet {
		return nil
	}
	for _, path := range result.Installed {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "installed %s\n", path)
	}
	for _, path := range result.Skipped {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "preserved %s\n", path)
	}
	return nil
}
