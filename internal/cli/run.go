package cli

import (
	"errors"
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/app/managedrun"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/spf13/cobra"
)

var exitProcess = os.Exit

type exitCoder interface {
	ExitCode() int
}

type runOptions struct {
	agent       string
	taskRef     string
	repo        string
	socketPath  string
	credHelper  string
	ghWrapper   string
	verifyCmd   string
	maxRetries  int
	tokenWarnAt int64
	tokenStopAt int64
	isolateHome bool
}

func newRunCommand() *cobra.Command {
	options := runOptions{repo: ".", maxRetries: 2, isolateHome: true}
	command := &cobra.Command{
		Use:   "run [flags] -- <agent-command> [args...]",
		Short: "Launch an agent session with brokered auth",
		Long: `Creates a broker session for the specified agent and repository,
sets up fail-closed credential helpers, and execs the agent CLI.

For containerized workflows, start the devcontainer first, shell into it,
and then run "ai-agent run" inside the container. The broker must be
running (or socket-activated) before running this command.
Use "ai-agent doctor" to verify your setup.

Examples:
  ai-agent run --agent claude --repo . -- claude
  ai-agent run --agent codex --repo /path/to/repo -- codex --model o3`,
		DisableFlagParsing: false,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun(cmd, options, args)
		},
	}
	command.Flags().StringVar(&options.agent, "agent", options.agent, "agent identity name (required)")
	command.Flags().StringVar(&options.taskRef, "task-ref", options.taskRef, "optional external task reference, for example github:owner/repo#43")
	command.Flags().StringVar(&options.repo, "repo", options.repo, "path to the git repository")
	command.Flags().StringVar(&options.socketPath, "broker-sock", options.socketPath, "broker socket path (default: auto)")
	command.Flags().StringVar(&options.credHelper, "credential-helper", options.credHelper, "path to credential helper binary (default: auto-detect)")
	command.Flags().StringVar(&options.ghWrapper, "gh-wrapper", options.ghWrapper, "path to ai-agent-gh binary (default: auto-detect)")
	command.Flags().StringVar(&options.verifyCmd, "verify-cmd", options.verifyCmd, "shell command to run after the agent; passing output is hidden and failure output is bounded")
	command.Flags().IntVar(&options.maxRetries, "max-retries", options.maxRetries, "max retries when --verify-cmd fails")
	command.Flags().Int64Var(&options.tokenWarnAt, "token-warn-at", options.tokenWarnAt, "warn once when native agent telemetry reports at least this many run tokens")
	command.Flags().Int64Var(&options.tokenStopAt, "token-stop-at", options.tokenStopAt, "stop the agent when native agent telemetry reports at least this many run tokens")
	command.Flags().BoolVar(&options.isolateHome, "isolate-home", options.isolateHome, "run the agent with an ephemeral HOME that projects only agent login state; personal gh, git, and SSH state stay unreachable")
	_ = command.MarkFlagRequired("agent")
	return command
}

func runRun(cmd *cobra.Command, options runOptions, args []string) error {
	return finishRun(managedrun.Run(cmd.ErrOrStderr(), managedrun.Request{
		AgentName:                options.agent,
		TaskRef:                  options.taskRef,
		RepoPath:                 options.repo,
		BrokerSocketPathOverride: options.socketPath,
		CredentialHelperPath:     options.credHelper,
		GhWrapperPath:            options.ghWrapper,
		VerifyCommand:            options.verifyCmd,
		MaxRetries:               options.maxRetries,
		TokenWarnAt:              options.tokenWarnAt,
		TokenStopAt:              options.tokenStopAt,
		IsolateHome:              options.isolateHome,
		AgentCommand:             args,
		ObservabilityResource:    os.Getenv(paths.EnvObservabilityResource),
		AIAgentVersion:           Version,
	}))
}

func finishRun(err error) error {
	if err == nil {
		return nil
	}

	var exitErr exitCoder
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code == 0 {
			return nil
		}
		exitProcess(code)
		return nil
	}

	return err
}
