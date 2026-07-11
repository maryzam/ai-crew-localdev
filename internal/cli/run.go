package cli

import (
	"errors"
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/control"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/launcher"
	"github.com/spf13/cobra"
)

var exitProcess = os.Exit

type exitCoder interface {
	ExitCode() int
}

var runCmd = &cobra.Command{
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
	RunE:               runRun,
}

var (
	runAgent       string
	runTaskRef     string
	runRepo        string
	runSocketPath  string
	runCredHelper  string
	runGhWrapper   string
	runVerifyCmd   string
	runMaxRetries  int
	runIsolateHome bool
)

func init() {
	runCmd.Flags().StringVar(&runAgent, "agent", "", "agent identity name (required)")
	runCmd.Flags().StringVar(&runTaskRef, "task-ref", "", "optional external task reference, for example github:owner/repo#43")
	runCmd.Flags().StringVar(&runRepo, "repo", ".", "path to the git repository")
	runCmd.Flags().StringVar(&runSocketPath, "broker-sock", "", "broker socket path (default: auto)")
	runCmd.Flags().StringVar(&runCredHelper, "credential-helper", "", "path to credential helper binary (default: auto-detect)")
	runCmd.Flags().StringVar(&runGhWrapper, "gh-wrapper", "", "path to ai-agent-gh binary (default: auto-detect)")
	runCmd.Flags().StringVar(&runVerifyCmd, "verify-cmd", "", "shell command to run after the agent; passing output is hidden and failure output is bounded")
	runCmd.Flags().IntVar(&runMaxRetries, "max-retries", 2, "max retries when --verify-cmd fails")
	runCmd.Flags().BoolVar(&runIsolateHome, "isolate-home", true, "run the agent with an ephemeral HOME that projects only agent login state; personal gh, git, and SSH state stay unreachable")
	_ = runCmd.MarkFlagRequired("agent")
}

func runRun(cmd *cobra.Command, args []string) error {
	planner := control.NewPlanner(cmd.ErrOrStderr())
	planned, err := planner.PlanRun(control.RunRequest{
		AgentName:                runAgent,
		TaskRef:                  runTaskRef,
		RepoPath:                 runRepo,
		BrokerSocketPathOverride: runSocketPath,
		CredentialHelperPath:     runCredHelper,
		GhWrapperPath:            runGhWrapper,
		VerifyCommand:            runVerifyCmd,
		MaxRetries:               runMaxRetries,
		IsolateHome:              runIsolateHome,
		AgentCommand:             args,
		AIAgentVersion:           Version,
		ObservabilityResource:    os.Getenv(paths.EnvObservabilityResource),
	})
	if err != nil {
		return err
	}
	return finishRun(launcher.Launch(launcherOptions(planned.Launcher)))
}

func launcherOptions(options control.LauncherOptions) launcher.Options {
	return launcher.Options{
		RunID:                 options.RunID,
		AgentName:             options.AgentName,
		ConfiguredModel:       options.ConfiguredModel,
		TaskRef:               options.TaskRef,
		RepoPath:              options.RepoPath,
		SocketPath:            options.SocketPath,
		CredHelper:            options.CredHelper,
		GhWrapper:             options.GhWrapper,
		RealGhPath:            options.RealGhPath,
		AgentCommand:          append([]string(nil), options.AgentCommand...),
		AIAgentVersion:        options.AIAgentVersion,
		ObservabilityResource: options.ObservabilityResource,
		VerifyCmd:             options.VerifyCmd,
		Contracts:             launcherContracts(options.Contracts),
		ContractsDir:          options.ContractsDir,
		MaxRetries:            options.MaxRetries,
		DisableHomeIsolation:  options.DisableHomeIsolation,
	}
}

func launcherContracts(contracts []control.VerifyContract) []launcher.VerifyContract {
	result := make([]launcher.VerifyContract, 0, len(contracts))
	for _, contract := range contracts {
		result = append(result, launcher.VerifyContract{
			Name:       contract.Name,
			Command:    contract.Command,
			RetryAgent: contract.RetryAgent,
		})
	}
	return result
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
