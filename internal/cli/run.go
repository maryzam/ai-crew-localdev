package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/launcher"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [flags] -- <agent-command> [args...]",
	Short: "Launch an agent session with brokered auth",
	Long: `Creates a broker session for the specified agent and repository,
sets up fail-closed credential helpers, and execs the agent CLI.

The broker must be running (or socket-activated) before running this command.
Use "ai-agent doctor" to verify your setup.

Examples:
  ai-agent run --agent claude --repo . -- claude
  ai-agent run --agent codex --repo /path/to/repo -- codex --model o3`,
	DisableFlagParsing: false,
	SilenceUsage:       true,
	RunE:               runRun,
}

var (
	runAgent      string
	runRepo       string
	runSocketPath string
	runCredHelper string
)

func init() {
	runCmd.Flags().StringVar(&runAgent, "agent", "", "agent identity name (required)")
	runCmd.Flags().StringVar(&runRepo, "repo", ".", "path to the git repository")
	runCmd.Flags().StringVar(&runSocketPath, "broker-sock", "", "broker socket path (default: auto)")
	runCmd.Flags().StringVar(&runCredHelper, "credential-helper", "", "path to credential helper binary (default: auto-detect)")
	runCmd.MarkFlagRequired("agent")
}

func runRun(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no agent command specified; use -- to separate agent command from flags")
	}

	// Resolve socket path.
	socketPath := runSocketPath
	if socketPath == "" {
		socketPath = config.DefaultSocketPath()
	}

	// Resolve credential helper path.
	credHelper := runCredHelper
	if credHelper == "" {
		// Look for ai-agent-credential-helper next to ai-agent binary,
		// then in PATH.
		if p, err := exec.LookPath("ai-agent-credential-helper"); err == nil {
			credHelper = p
		} else {
			return fmt.Errorf("ai-agent-credential-helper not found in PATH; install it or use --credential-helper")
		}
	}

	// Verify credential helper exists.
	if _, err := os.Stat(credHelper); err != nil {
		return fmt.Errorf("credential helper not found at %s: %w", credHelper, err)
	}

	return launcher.Launch(launcher.Options{
		AgentName:    runAgent,
		RepoPath:     runRepo,
		SocketPath:   socketPath,
		CredHelper:   credHelper,
		AgentCommand: args,
	})
}
