package cli

import (
	"fmt"
	"os"
	"os/exec"

	usagecapture "github.com/maryzam/ai-crew-localdev/internal/usage"
	"github.com/spf13/cobra"
)

var (
	usageLookPath = exec.LookPath
	usageRunCmd   = func(command *exec.Cmd) error { return command.Run() }
)

var usageCmd = &cobra.Command{
	Use:                "usage [ccusage arguments...]",
	Short:              "Inspect aggregate local agent usage",
	DisableFlagParsing: true,
	SilenceUsage:       true,
	RunE:               runUsage,
}

func runUsage(cmd *cobra.Command, args []string) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return cmd.Help()
	}
	path, err := usageLookPath("ccusage")
	if err != nil {
		return fmt.Errorf("usage adapter is not installed; managed runs still work but omit usage")
	}
	if len(args) == 0 {
		args = []string{"monthly", "--offline", "--compact"}
	}

	child := exec.Command(path, args...)
	child.Env = usagecapture.SafeEnv()
	child.Stdin = os.Stdin
	child.Stdout = cmd.OutOrStdout()
	child.Stderr = cmd.ErrOrStderr()
	if err := usageRunCmd(child); err != nil {
		return fmt.Errorf("ccusage: %w", err)
	}
	return nil
}
