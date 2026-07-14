package cli

import (
	"fmt"
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/app/agentdefaults"
	"github.com/spf13/cobra"
)

type bootstrapOptions struct {
	quiet bool
}

func newBootstrapCommand() *cobra.Command {
	options := bootstrapOptions{}
	command := &cobra.Command{
		Use:          "bootstrap",
		Short:        "Install missing agent guidance and skills",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBootstrap(cmd, options)
		},
	}
	command.Flags().BoolVar(&options.quiet, "quiet", options.quiet, "hide installed and preserved paths")
	return command
}

func runBootstrap(cmd *cobra.Command, options bootstrapOptions) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	result, err := agentdefaults.Install(home)
	if err != nil {
		return err
	}
	if options.quiet {
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
