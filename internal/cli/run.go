package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/launcher"
	"github.com/spf13/cobra"
)

var (
	execLookPath = exec.LookPath
	osExecutable = os.Executable
	exitProcess  = os.Exit
)

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
	runAgent      string
	runTaskRef    string
	runRepo       string
	runSocketPath string
	runCredHelper string
	runGhWrapper  string
	runVerifyCmd  string
	runMaxRetries int
)

func init() {
	runCmd.Flags().StringVar(&runAgent, "agent", "", "agent identity name (required)")
	runCmd.Flags().StringVar(&runTaskRef, "task-ref", "", "optional external task reference, for example github:owner/repo#43")
	runCmd.Flags().StringVar(&runRepo, "repo", ".", "path to the git repository")
	runCmd.Flags().StringVar(&runSocketPath, "broker-sock", "", "broker socket path (default: auto)")
	runCmd.Flags().StringVar(&runCredHelper, "credential-helper", "", "path to credential helper binary (default: auto-detect)")
	runCmd.Flags().StringVar(&runGhWrapper, "gh-wrapper", "", "path to ai-agent-gh binary (default: auto-detect)")
	runCmd.Flags().StringVar(&runVerifyCmd, "verify-cmd", "", "shell command to run after agent exits (e.g. \"make verify\"); enables verify-and-retry loop")
	runCmd.Flags().IntVar(&runMaxRetries, "max-retries", 2, "max retries when --verify-cmd fails")
	_ = runCmd.MarkFlagRequired("agent")
}

func runRun(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no agent command specified; use -- to separate agent command from flags")
	}

	socketPath, err := resolveBrokerSocketPath(runSocketPath)
	if err != nil {
		return err
	}

	credHelper := runCredHelper
	if credHelper == "" {
		credHelper, err = resolveOptionalBinary("ai-agent-credential-helper")
		if err != nil || credHelper == "" {
			return fmt.Errorf("ai-agent-credential-helper not found next to ai-agent or in PATH; install it or use --credential-helper")
		}
	}

	if _, err := os.Stat(credHelper); err != nil {
		return fmt.Errorf("credential helper not found at %s: %w", credHelper, err)
	}

	ghWrapper := runGhWrapper
	if ghWrapper == "" {
		ghWrapper, _ = resolveOptionalBinary("ai-agent-gh")
	}

	realGhPath := ""
	if ghWrapper != "" {
		realGhPath = resolveRealGhPath(ghWrapper)
	}

	return finishRun(launcher.Launch(launcher.Options{
		AgentName:       runAgent,
		ConfiguredModel: configuredIdentityModel(runAgent),
		TaskRef:         runTaskRef,
		RepoPath:        runRepo,
		SocketPath:      socketPath,
		CredHelper:      credHelper,
		GhWrapper:       ghWrapper,
		RealGhPath:      realGhPath,
		AgentCommand:    args,
		AIAgentVersion:  Version,
		VerifyCmd:       runVerifyCmd,
		MaxRetries:      runMaxRetries,
	}))
}

func configuredIdentityModel(agentName string) string {
	identities, err := identity.Load(config.DefaultIdentitiesPath())
	if err != nil {
		return ""
	}
	agent, ok := identities.Agents[agentName]
	if !ok {
		return ""
	}
	return strings.TrimSpace(agent.Model)
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

func resolveOptionalBinary(name string) (string, error) {
	if p, err := resolveSiblingBinary(name); err == nil {
		return p, nil
	}
	if p, err := execLookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s not found", name)
}

func resolveSiblingBinary(name string) (string, error) {
	self, err := osExecutable()
	if err != nil {
		return "", err
	}

	candidate := filepath.Join(filepath.Dir(self), name)
	info, err := os.Stat(candidate)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", candidate)
	}
	if info.Mode()&0111 == 0 {
		return "", fmt.Errorf("%s is not executable", candidate)
	}
	return candidate, nil
}

func resolveExecutableFromPath(name string, skipPath string) (string, error) {
	var skipInfo os.FileInfo
	if skipPath != "" {
		if info, err := os.Stat(skipPath); err == nil && !info.IsDir() {
			skipInfo = info
		}
	}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if skipInfo != nil && os.SameFile(info, skipInfo) {
			continue
		}
		if info.Mode()&0111 == 0 {
			continue
		}
		return candidate, nil
	}

	return "", fmt.Errorf("%s not found in PATH", name)
}

func resolveRealGhPath(ghWrapper string) string {
	if p := os.Getenv("AI_AGENT_REAL_GH"); isExecutableFile(p) {
		return p
	}

	p, _ := resolveExecutableFromPath("gh", ghWrapper)
	return p
}

func isExecutableFile(path string) bool {
	if path == "" {
		return false
	}

	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}

	return info.Mode()&0111 != 0
}
