package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/manifest"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/store"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/launcher"
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
	if len(args) == 0 {
		return fmt.Errorf("no agent command specified; use -- to separate agent command from flags")
	}
	if err := validateMaxRetries(runMaxRetries); err != nil {
		return err
	}

	info, err := loadProjectManifest(cmd.ErrOrStderr(), runRepo)
	if err != nil {
		return err
	}
	hostIdentity := configuredIdentity(runAgent)
	if err := info.enforceAgent(runAgent, args, hostIdentity); err != nil {
		return err
	}
	contracts, contractsDir := info.contracts(cmd.ErrOrStderr(), runVerifyCmd)
	configuredModel := hostIdentity.model()
	if manifestModel := info.modelDefault(runAgent); manifestModel != "" {
		configuredModel = manifestModel
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "model: run attribution uses project manifest default %q for agent %s\n", manifestModel, runAgent)
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
		AgentName:             runAgent,
		ConfiguredModel:       configuredModel,
		TaskRef:               runTaskRef,
		RepoPath:              runRepo,
		SocketPath:            socketPath,
		CredHelper:            credHelper,
		GhWrapper:             ghWrapper,
		RealGhPath:            realGhPath,
		AgentCommand:          args,
		AIAgentVersion:        Version,
		ObservabilityResource: os.Getenv("AI_AGENT_OBSERVABILITY_RESOURCE"),
		VerifyCmd:             runVerifyCmd,
		Contracts:             contracts,
		ContractsDir:          contractsDir,
		MaxRetries:            runMaxRetries,
		DisableHomeIsolation:  !runIsolateHome,
	}))
}

type projectManifestInfo struct {
	file *manifest.File
	path string
	root string
}

type hostAgentIdentity struct {
	value identity.AgentIdentity
	found bool
	err   error
}

func loadProjectManifest(errOut io.Writer, repoPath string) (*projectManifestInfo, error) {
	root := repoWorktreeRoot(repoPath)
	manifestPath, found, err := manifest.Find(root)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	file, err := manifest.Load(manifestPath)
	if err != nil {
		return nil, err
	}
	result := manifest.Validate(file)
	if result.Errors.HasErrors() {
		return nil, fmt.Errorf("invalid project manifest %s: %s", manifestPath, result.Errors.Error())
	}
	for _, warning := range result.Warnings {
		_, _ = fmt.Fprintf(errOut, "manifest: warning: %s: %s\n", warning.Field, warning.Message)
	}
	return &projectManifestInfo{file: file, path: manifestPath, root: root}, nil
}

func (info *projectManifestInfo) enforceAgent(agentName string, command []string, hostIdentity hostAgentIdentity) error {
	if info == nil || info.file.Agents == nil || len(info.file.Agents.Allowed) == 0 {
		return nil
	}
	if !slices.Contains(info.file.Agents.Allowed, agentName) {
		return fmt.Errorf("agent %q is not allowed by the project manifest %s (allowed: %s)", agentName, info.path, strings.Join(info.file.Agents.Allowed, ", "))
	}
	if hostIdentity.err != nil {
		return fmt.Errorf("agent %q is allowed by the project manifest %s but host identity could not be loaded: %w", agentName, info.path, hostIdentity.err)
	}
	if !hostIdentity.found {
		return fmt.Errorf("agent %q is allowed by the project manifest %s but no host identity is configured", agentName, info.path)
	}
	tool := strings.TrimSpace(hostIdentity.value.Tool)
	if tool == "" {
		return fmt.Errorf("agent %q is allowed by the project manifest %s but host identity has no configured tool", agentName, info.path)
	}
	if len(command) == 0 || !agentCommandMatchesTool(command[0], tool) {
		actual := ""
		if len(command) > 0 {
			actual = filepath.Base(strings.TrimSpace(command[0]))
		}
		return fmt.Errorf("agent %q is allowed by the project manifest %s but command %q does not match configured tool %q", agentName, info.path, actual, tool)
	}
	return nil
}

func (info *projectManifestInfo) modelDefault(agentName string) string {
	if info == nil || info.file.Agents == nil {
		return ""
	}
	return strings.TrimSpace(info.file.Agents.Defaults[agentName].Model)
}

func (info *projectManifestInfo) contracts(errOut io.Writer, verifyCmd string) ([]launcher.VerifyContract, string) {
	if info == nil || len(info.file.Contracts) == 0 {
		return nil, ""
	}
	if verifyCmd != "" {
		_, _ = fmt.Fprintf(errOut, "verify: --verify-cmd overrides %d project contract(s) from %s\n", len(info.file.Contracts), info.path)
		return nil, ""
	}
	contracts := make([]launcher.VerifyContract, 0, len(info.file.Contracts))
	for _, contract := range info.file.Contracts {
		contracts = append(contracts, launcher.VerifyContract{
			Name:       contract.Name,
			Command:    contract.Command,
			RetryAgent: contract.Retry != manifest.RetryNever,
		})
	}
	_, _ = fmt.Fprintf(errOut, "verify: %d project contract(s) declared in %s\n", len(contracts), info.path)
	return contracts, info.root
}

func resolveVerification(errOut io.Writer, repoPath string, verifyCmd string) ([]launcher.VerifyContract, string, error) {
	info, err := loadProjectManifest(errOut, repoPath)
	if err != nil {
		return nil, "", err
	}
	contracts, contractsDir := info.contracts(errOut, verifyCmd)
	return contracts, contractsDir, nil
}

func repoWorktreeRoot(repoPath string) string {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--show-toplevel").Output()
	root := strings.TrimSpace(string(out))
	if err != nil || root == "" {
		return repoPath
	}
	return root
}

func validateMaxRetries(value int) error {
	if value < 0 || value > 10 {
		return fmt.Errorf("--max-retries must be between 0 and 10")
	}
	return nil
}

func agentCommandMatchesTool(commandName string, tool string) bool {
	commandName = filepath.Base(strings.TrimSpace(commandName))
	tool = filepath.Base(strings.TrimSpace(tool))
	switch tool {
	case "claude-code":
		return commandName == "claude" || commandName == "claude-code"
	default:
		return commandName == tool
	}
}

func configuredIdentityModel(agentName string) string {
	return configuredIdentity(agentName).model()
}

func configuredIdentity(agentName string) hostAgentIdentity {
	snapshot, err := store.Load(paths.DefaultIdentitiesPath(), paths.DefaultPolicyPath())
	if err != nil || snapshot.IdentitiesError != nil {
		if err == nil {
			err = snapshot.IdentitiesError
		}
		return hostAgentIdentity{err: err}
	}
	agent, ok := snapshot.Identities.Agents[agentName]
	if !ok {
		return hostAgentIdentity{}
	}
	return hostAgentIdentity{value: agent, found: true}
}

func (host hostAgentIdentity) model() string {
	if !host.found || host.err != nil {
		return ""
	}
	return strings.TrimSpace(host.value.Model)
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
