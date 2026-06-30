package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/configstore"
	"github.com/maryzam/ai-crew-localdev/internal/securefile"
	"github.com/spf13/cobra"
)

var (
	upWorkspace string
	upProject   string
	upBuild     bool
	upLangfuse  bool
	upRuntime   string
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Bootstrap the full local dev environment in one command",
	Long: `Ensures the broker is running, validates host readiness, builds (if needed)
and launches the devcontainer, then opens an interactive shell inside it.

This is the single supported entrypoint for the ai-agent local dev environment.
In the generic devcontainer, agent CLI login state persists in the ai-agent-home
volume mounted at /home/dev, while GitHub repo credentials remain brokered
through ai-agent run.

Examples:
  ai-agent up
  ai-agent up --workspace ~/github
  ai-agent up --project ~/github/my-rails-app
  ai-agent up --build
  ai-agent up --langfuse`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runUp,
}

func init() {
	upCmd.Flags().StringVar(&upWorkspace, "workspace", ".", "path to the workspace directory to mount")
	upCmd.Flags().StringVar(&upProject, "project", "", "path to a single project whose own .devcontainer should be honored, with the broker overlay injected")
	upCmd.Flags().BoolVar(&upBuild, "build", false, "force rebuild of the devcontainer image")
	upCmd.Flags().BoolVar(&upLangfuse, "langfuse", false, "start Langfuse observability stack as a sidecar")
	upCmd.Flags().StringVar(&upRuntime, "runtime", string(containerRuntimePodman), "container runtime to use: podman or docker")
}

// upLookPath is a test seam for exec.LookPath.
var upLookPath = exec.LookPath

// Test seams for the auto-fix flow.
var (
	upStdin     io.Reader = os.Stdin
	upRunCmd              = func(c *exec.Cmd) error { return c.Run() }
	upInstallFn           = installMissing // replaceable in tests
	upSetupFn             = func(cmd *cobra.Command, args []string, scanner *bufio.Scanner) error {
		return runSetupWithNext(cmd, args, scanner, "continuing: starting broker and devcontainer")
	}
)

func runUp(cmd *cobra.Command, args []string) error {
	runtime, err := parseContainerRuntime(upRuntime)
	if err != nil {
		return err
	}
	promptScanner := bufio.NewScanner(upStdin)

	// 1. Resolve workspace (the directory containing user repos).
	workspace, err := resolveWorkspaceDir()
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	_ = os.Setenv("AI_AGENT_WORKSPACE", workspace)

	// Only set XDG_RUNTIME_DIR if not already set by the host session.
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		_ = os.Setenv("XDG_RUNTIME_DIR", config.RuntimeBaseDir())
	}

	// Ensure runtime subdirectory exists.
	aiAgentRuntime := config.RuntimeDir()
	if err := os.MkdirAll(aiAgentRuntime, 0o700); err != nil {
		return fmt.Errorf("create runtime dir %s: %w", aiAgentRuntime, err)
	}

	runtime, err = ensureUpHostReadiness(cmd, runtime, promptScanner)
	if err != nil {
		return err
	}

	if err := ensureFirstUseConfig(cmd, promptScanner); err != nil {
		return err
	}

	if upLangfuse {
		if err := startLangfuse(cmd); err != nil {
			return fmt.Errorf("langfuse startup: %w", err)
		}
	}

	socketPath := config.DefaultSocketPath()
	if err := ensureBroker(socketPath); err != nil {
		return fmt.Errorf("broker startup: %w", err)
	}

	report := buildDoctorReport(doctorModeUp, socketPath, "", runtime)
	if !report.Ready {
		var fixed bool
		runtime, fixed = tryAutoFix(cmd, report, runtime, promptScanner)
		if fixed {
			// Re-run doctor after fixes.
			report = buildDoctorReport(doctorModeUp, socketPath, "", runtime)
		}
		if !report.Ready {
			writeDoctorText(cmd.OutOrStdout(), report)
			return fmt.Errorf("readiness checks failed; fix the issues above before running 'ai-agent up'")
		}
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "doctor: all checks passed")

	devcontainerBin, err := upLookPath("devcontainer")
	if err != nil {
		return fmt.Errorf("devcontainer CLI not found in PATH: %w", err)
	}

	if upProject != "" {
		return launchProjectDevcontainer(cmd, devcontainerBin, runtime, workspace)
	}

	// Find the project root containing .devcontainer/. Search from the
	// executable's directory first (works after `make install` if the
	// binary is still co-located with the repo), then fall back to CWD.
	repoRoot, err := findDevcontainerRoot()
	if err != nil {
		return fmt.Errorf("find devcontainer root: %w", err)
	}

	upArgs := append([]string{"up"}, devcontainerRuntimeArgs(runtime)...)
	upArgs = append(upArgs, "--workspace-folder", repoRoot)
	if upBuild {
		upArgs = append(upArgs, "--build-no-cache")
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "launching devcontainer in %s with %s\n", repoRoot, runtime)
	dcUpCmd := exec.Command(devcontainerBin, upArgs...)
	dcUpCmd.Stdout = cmd.OutOrStdout()
	dcUpCmd.Stderr = cmd.OutOrStderr()
	if err := upRunCmd(dcUpCmd); err != nil {
		return fmt.Errorf("devcontainer up: %w", err)
	}
	writeDevcontainerAccessInfo(cmd.OutOrStdout(), repoRoot, runtime)
	writeAgentLoginStateInfo(cmd.OutOrStdout())

	execArgs := append([]string{"exec"}, devcontainerRuntimeArgs(runtime)...)
	execArgs = append(execArgs, "--workspace-folder", repoRoot, "bash")
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "opening shell in devcontainer")
	shellCmd := exec.Command(devcontainerBin, execArgs...)
	shellCmd.Stdin = os.Stdin
	shellCmd.Stdout = cmd.OutOrStdout()
	shellCmd.Stderr = cmd.OutOrStderr()
	if err := upRunCmd(shellCmd); err != nil {
		return fmt.Errorf("open shell in devcontainer: %w (re-enter with: %s)", err, devcontainerExecCommand(repoRoot, runtime))
	}
	return nil
}

func ensureUpHostReadiness(cmd *cobra.Command, runtime containerRuntime, scanner *bufio.Scanner) (containerRuntime, error) {
	report := buildUpHostReadinessReport(runtime)
	if report.Ready {
		return runtime, nil
	}

	var fixed bool
	runtime, fixed = tryAutoFix(cmd, report, runtime, scanner)
	if fixed {
		report = buildUpHostReadinessReport(runtime)
	}
	if !report.Ready {
		writeDoctorText(cmd.OutOrStdout(), report)
		return runtime, fmt.Errorf("host readiness checks failed; fix the issues above before running guided setup")
	}
	return runtime, nil
}

func buildUpHostReadinessReport(runtime containerRuntime) doctorReport {
	runtimeDir := config.RuntimeBaseDir()
	checks := []doctorCheck{checkRuntimeDir(runtimeDir)}
	checks = append(checks, checkBinaryReadinessForUp()...)
	checks = append(checks, checkContainerWorkspace())
	checks = append(checks, checkContainerRuntime(runtime))
	return doctorReport{
		Mode:       doctorModeUp,
		Ready:      !hasBlockingFailure(checks),
		RuntimeDir: runtimeDir,
		SocketPath: config.DefaultSocketPath(),
		Checks:     checks,
	}
}

func ensureFirstUseConfig(cmd *cobra.Command, scanner *bufio.Scanner) error {
	issues, err := firstUseConfigIssues()
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		return nil
	}

	w := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(w, "first-time configuration needs attention: %s\n", strings.Join(issues, "; "))
	if !promptYNWithScanner(w, scanner, "Run guided setup now?") {
		return fmt.Errorf("first-time configuration is required before 'ai-agent up'; run 'ai-agent setup' or rerun 'ai-agent up' and accept guided setup")
	}

	if err := upSetupFn(cmd, nil, scanner); err != nil {
		return fmt.Errorf("guided setup: %w", err)
	}
	return nil
}

func firstUseConfigIssues() ([]string, error) {
	var issues []string

	identitiesPath := config.ExpandHome(config.DefaultIdentitiesPath())
	policyPath := configuredPolicyPath()
	snapshot, err := configstore.Inspect(identitiesPath, policyPath)
	if err != nil {
		return nil, fmt.Errorf("recover governance configuration: %w", err)
	}
	idents, identityCheck := loadedIdentitiesCheck(identitiesPath, snapshot.Identities, snapshot.IdentitiesError)
	if identityCheck.Status == doctorStatusFail {
		issues = append(issues, identityCheck.Details)
	}

	pol, policyCheck := loadedPolicyCheck(policyPath, snapshot.Policy, snapshot.PolicyError)
	if policyCheck.Status == doctorStatusFail {
		issues = append(issues, policyCheck.Details)
	}

	if idents != nil {
		for _, check := range checkIdentityKeys(*idents) {
			if check.Status == doctorStatusFail {
				issues = append(issues, check.Details)
			}
		}
	}

	if idents != nil && pol != nil {
		for _, check := range []doctorCheck{
			checkPolicyProviderConfig(idents, pol, policyPath),
			checkInstallationIDs(*idents, *pol, policyPath),
		} {
			if check.Status == doctorStatusFail {
				issues = append(issues, check.Details)
			}
		}
	}

	return issues, nil
}

// resolveWorkspaceDir returns the absolute host directory exported as
// AI_AGENT_WORKSPACE. In --project mode the project itself is the workspace, so
// a project devcontainer that reads ${localEnv:AI_AGENT_WORKSPACE} mounts the
// project rather than an unrelated --workspace default.
func resolveWorkspaceDir() (string, error) {
	if upProject != "" {
		return filepath.Abs(upProject)
	}
	return filepath.Abs(upWorkspace)
}

func launchProjectDevcontainer(cmd *cobra.Command, devcontainerBin string, runtime containerRuntime, project string) error {
	if !projectHasDevcontainer(project) {
		return fmt.Errorf("project %s has no .devcontainer; run 'ai-agent up --workspace %s' to use the generic image instead", project, project)
	}

	overlay, err := brokerOverlayArgs(project)
	if err != nil {
		return err
	}

	upArgs := projectUpArgs(runtime, project, overlay, upBuild)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "launching project devcontainer in %s with %s\n", project, runtime)
	dcUpCmd := exec.Command(devcontainerBin, upArgs...)
	dcUpCmd.Stdout = cmd.OutOrStdout()
	dcUpCmd.Stderr = cmd.OutOrStderr()
	if err := upRunCmd(dcUpCmd); err != nil {
		return fmt.Errorf("devcontainer up: %w", err)
	}
	if err := bootstrapProjectDevcontainer(cmd, devcontainerBin, runtime, project, overlay); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: optional agent defaults were not installed: %v\n", err)
	}

	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "project devcontainer ready; broker and ai-agent toolchain injected")
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "re-enter later with: %s\n", devcontainerExecShellCommand(project, runtime, overlay))

	execArgs := projectExecArgs(runtime, project, overlay, "sh", "-c", fallbackShell)
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "opening shell in devcontainer")
	shellCmd := exec.Command(devcontainerBin, execArgs...)
	shellCmd.Stdin = os.Stdin
	shellCmd.Stdout = cmd.OutOrStdout()
	shellCmd.Stderr = cmd.OutOrStderr()
	if err := upRunCmd(shellCmd); err != nil {
		return fmt.Errorf("open shell in devcontainer: %w (re-enter with: %s)", err, devcontainerExecShellCommand(project, runtime, overlay))
	}
	return nil
}

func bootstrapProjectDevcontainer(cmd *cobra.Command, devcontainerBin string, runtime containerRuntime, project string, overlay []string) error {
	args := projectExecArgs(runtime, project, overlay, path.Join(containerBinDir, "ai-agent"), "bootstrap", "--quiet")
	bootstrap := exec.Command(devcontainerBin, args...)
	bootstrap.Stdout = cmd.OutOrStdout()
	bootstrap.Stderr = cmd.ErrOrStderr()
	if err := upRunCmd(bootstrap); err != nil {
		return fmt.Errorf("bootstrap project devcontainer: %w", err)
	}
	return nil
}

func projectUpArgs(runtime containerRuntime, project string, overlay []string, build bool) []string {
	args := append([]string{"up"}, devcontainerRuntimeArgs(runtime)...)
	args = append(args, "--workspace-folder", project)
	args = append(args, overlay...)
	if build {
		args = append(args, "--build-no-cache")
	}
	return args
}

func projectExecArgs(runtime containerRuntime, project string, overlay []string, command ...string) []string {
	args := append([]string{"exec"}, devcontainerRuntimeArgs(runtime)...)
	args = append(args, "--workspace-folder", project)
	args = append(args, overlay...)
	args = append(args, command...)
	return args
}

// ensureBroker checks if the broker socket is responsive. If not, it tries
// systemd socket activation, then falls back to starting the broker directly.
func ensureBroker(socketPath string) error {
	// Already running?
	if brokerResponds(socketPath) {
		return nil
	}

	// Try systemd socket activation.
	if systemctlBin, err := exec.LookPath("systemctl"); err == nil {
		cmd := exec.Command(systemctlBin, "--user", "start", "ai-agent-broker.socket")
		_ = cmd.Run()
		if waitForBroker(socketPath, 5*time.Second) {
			return nil
		}
	}

	// Fallback: start broker directly in background.
	brokerBin, err := resolveOptionalBinary("ai-agent-broker")
	if err != nil {
		return fmt.Errorf("broker not running and ai-agent-broker not found: %w", err)
	}

	brokerCmd := exec.Command(brokerBin)
	brokerCmd.Stdout = os.Stderr // broker logs to stderr
	brokerCmd.Stderr = os.Stderr
	if err := brokerCmd.Start(); err != nil {
		return fmt.Errorf("start broker: %w", err)
	}

	// Reap the child process to avoid zombies.
	go func() { _ = brokerCmd.Wait() }()

	// Arrange cleanup on exit signals so the broker doesn't outlive us.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		_ = brokerCmd.Process.Signal(syscall.SIGTERM)
	}()

	if !waitForBroker(socketPath, 5*time.Second) {
		_ = brokerCmd.Process.Signal(syscall.SIGTERM)
		return fmt.Errorf("broker did not become ready within 5s at %s", socketPath)
	}
	return nil
}

func brokerResponds(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForBroker(socketPath string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if brokerResponds(socketPath) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func writeDevcontainerAccessInfo(w io.Writer, repoRoot string, runtime containerRuntime) {
	_, _ = fmt.Fprintf(w, "devcontainer is ready; your host workspace %s is mounted at /workspace\n", os.Getenv("AI_AGENT_WORKSPACE"))
	_, _ = fmt.Fprintf(w, "runtime: %s\n", runtime)
	_, _ = fmt.Fprintf(w, "re-enter later with: %s\n", devcontainerExecCommand(repoRoot, runtime))
	_, _ = fmt.Fprintf(w, "find the backing container with: %s ps --filter %q\n", runtime.binaryName(), devcontainerLabelFilter(repoRoot))
}

func writeAgentLoginStateInfo(w io.Writer) {
	_, _ = fmt.Fprintln(w, "agent CLI login state: Claude and Codex store personal sign-in/config under /home/dev")
	_, _ = fmt.Fprintln(w, "persistence: /home/dev is the ai-agent-home volume and survives container re-entry/restart")
	_, _ = fmt.Fprintln(w, "security: run git and gh through 'ai-agent run'; do not run 'gh auth login' in this container")
}

func devcontainerLabelFilter(repoRoot string) string {
	return "label=devcontainer.local_folder=" + repoRoot
}

// langfuseComposePath is a test seam for locating the Langfuse compose file.
var langfuseComposePath = findLangfuseCompose

// startLangfuse launches the Langfuse observability stack using docker compose.
// It looks for contrib/langfuse/docker-compose.yml relative to the project root,
// ensures the .env file exists (copying .env.example if needed), then runs
// docker compose up -d and waits for services to become healthy.
func startLangfuse(cmd *cobra.Command) error {
	composePath, err := langfuseComposePath()
	if err != nil {
		return err
	}

	composeDir := filepath.Dir(composePath)
	envPath := filepath.Join(composeDir, ".env")
	examplePath := filepath.Join(composeDir, ".env.example")

	// Bootstrap .env from .env.example if it doesn't exist.
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		if _, err := os.Stat(examplePath); err != nil {
			return fmt.Errorf("langfuse .env.example not found at %s", examplePath)
		}
		data, err := os.ReadFile(examplePath)
		if err != nil {
			return fmt.Errorf("read .env.example: %w", err)
		}
		if err := securefile.WriteOwnerOnly(envPath, data); err != nil {
			return fmt.Errorf("write .env: %w", err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "langfuse: created .env from .env.example (review and change secrets before production use)")
	}
	langfuseConfig, err := loadLangfuseClientEnvironment(envPath)
	if err != nil {
		return err
	}
	if err := configureLangfusePolicy(envPath, langfuseConfig); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "langfuse: starting observability stack")
	composeCmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d", "--wait")
	composeCmd.Stdout = cmd.OutOrStdout()
	composeCmd.Stderr = cmd.OutOrStderr()
	if err := composeCmd.Run(); err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "langfuse: stack ready at http://localhost:3000")
	return nil
}

type langfuseClientConfig struct {
	Project  string
	Endpoint string
}

func loadLangfuseClientEnvironment(path string) (langfuseClientConfig, error) {
	data, err := securefile.ReadOwnerOnly(path, 64*1024)
	if err != nil {
		return langfuseClientConfig{}, fmt.Errorf("open langfuse environment: %w", err)
	}

	values := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	if err := scanner.Err(); err != nil {
		return langfuseClientConfig{}, fmt.Errorf("read langfuse environment: %w", err)
	}
	publicKey := values["LANGFUSE_INIT_PROJECT_PUBLIC_KEY"]
	secretKey := values["LANGFUSE_INIT_PROJECT_SECRET_KEY"]
	if publicKey == "" || secretKey == "" {
		return langfuseClientConfig{}, fmt.Errorf("langfuse .env must define LANGFUSE_INIT_PROJECT_PUBLIC_KEY and LANGFUSE_INIT_PROJECT_SECRET_KEY")
	}
	project := strings.TrimSpace(values["LANGFUSE_INIT_PROJECT_ID"])
	if project == "" {
		return langfuseClientConfig{}, fmt.Errorf("langfuse .env must define LANGFUSE_INIT_PROJECT_ID")
	}
	endpoint := strings.TrimSpace(values["AI_AGENT_LANGFUSE_OTLP_ENDPOINT"])
	if endpoint == "" {
		endpoint = "http://host.containers.internal:3000/api/public/otel"
	}
	resource := "langfuse:project:" + project
	if err := os.Setenv("AI_AGENT_OBSERVABILITY_RESOURCE", resource); err != nil {
		return langfuseClientConfig{}, err
	}
	return langfuseClientConfig{Project: project, Endpoint: endpoint}, nil
}

func configureLangfusePolicy(credentialsFile string, configValue langfuseClientConfig) error {
	info, err := os.Lstat(credentialsFile)
	if err != nil {
		return fmt.Errorf("inspect langfuse credentials: %w", err)
	}
	stat, ownerOK := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 || !ownerOK || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("langfuse credentials file %s must be an owner-only regular file", credentialsFile)
	}
	policyPath := configuredPolicyPath()
	identitiesPath := config.DefaultIdentitiesPath()
	idents, pol, err := configstore.Load(identitiesPath, policyPath)
	if err != nil {
		return fmt.Errorf("load governance configuration: %w", err)
	}
	section, err := json.Marshal(map[string]string{
		"credentials_file": credentialsFile,
		"endpoint":         configValue.Endpoint,
		"project":          configValue.Project,
	})
	if err != nil {
		return fmt.Errorf("encode langfuse policy: %w", err)
	}
	resource := "langfuse:project:" + configValue.Project
	for name, agent := range pol.Agents {
		if !containsString(agent.Resources, resource) {
			agent.Resources = append(agent.Resources, resource)
		}
		if agent.Providers == nil {
			agent.Providers = make(map[string]json.RawMessage)
		}
		agent.Providers["langfuse"] = section
		pol.Agents[name] = agent
	}
	if err := validateConfiguredPolicy(pol, idents); err != nil {
		return fmt.Errorf("validate langfuse policy: %w", err)
	}
	if err := configstore.Publish(identitiesPath, idents, policyPath, pol); err != nil {
		return fmt.Errorf("publish langfuse policy: %w", err)
	}
	reloadRunningBroker()
	return nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func reloadRunningBroker() {
	data, err := os.ReadFile(filepath.Join(config.RuntimeDir(), "broker.pid"))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err == nil && pid > 1 {
		_ = syscall.Kill(pid, syscall.SIGHUP)
		time.Sleep(200 * time.Millisecond)
	}
}

// findLangfuseCompose locates the Langfuse docker-compose.yml by searching
// upward from the executable's directory and CWD for contrib/langfuse/.
func findLangfuseCompose() (string, error) {
	candidates := []string{}
	if self, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(self))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	return searchLangfuseCompose(candidates)
}

// searchLangfuseCompose walks upward from each start directory looking for
// contrib/langfuse/docker-compose.yml. Extracted for testability.
func searchLangfuseCompose(startDirs []string) (string, error) {
	for _, start := range startDirs {
		current := start
		for {
			candidate := filepath.Join(current, "contrib", "langfuse", "docker-compose.yml")
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			current = parent
		}
	}
	return "", fmt.Errorf("contrib/langfuse/docker-compose.yml not found; run from the ai-crew-localdev checkout")
}

// findDevcontainerRoot locates the project root containing .devcontainer/.
// It searches from the executable's directory first, then from CWD.
// Only .devcontainer/ is matched — bare .git/ is not sufficient, since
// the user may be running from any git repo after installing the binary.
func findDevcontainerRoot() (string, error) {
	// Try executable's directory first.
	if self, err := os.Executable(); err == nil {
		if root, found := walkUpForDevcontainer(filepath.Dir(self)); found {
			return root, nil
		}
	}
	// Fall back to CWD.
	if cwd, err := os.Getwd(); err == nil {
		if root, found := walkUpForDevcontainer(cwd); found {
			return root, nil
		}
	}
	return "", fmt.Errorf(".devcontainer/ not found; run from the ai-crew-localdev checkout or ensure the binary is co-located with the project")
}

// walkUpForDevcontainer walks upward from dir looking for a .devcontainer/
// directory. Returns the containing directory and true if found.
func walkUpForDevcontainer(dir string) (string, bool) {
	current := dir
	for {
		if _, err := os.Stat(filepath.Join(current, ".devcontainer")); err == nil {
			return current, true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		current = parent
	}
}

// findRepoRoot walks upward from dir to find the nearest .devcontainer/ directory
// or .git directory, returning the containing directory.
func findRepoRoot(dir string) (string, error) {
	current := dir
	for {
		if _, err := os.Stat(filepath.Join(current, ".devcontainer")); err == nil {
			return current, nil
		}
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return dir, nil // fallback to original dir
		}
		current = parent
	}
}

// tryAutoFix inspects a failed doctor report and offers to install missing
// container tooling interactively. It may also switch the runtime for this run.
func tryAutoFix(cmd *cobra.Command, report doctorReport, runtime containerRuntime, scanner *bufio.Scanner) (containerRuntime, bool) {
	for _, check := range report.Checks {
		if check.Name == "container-runtime" && check.Status == doctorStatusFail {
			return upInstallFn(cmd, runtime, scanner)
		}
	}
	return runtime, false
}

// installMissing checks for each container-runtime prerequisite individually,
// prompts the user for approval, and installs it.
func installMissing(cmd *cobra.Command, runtime containerRuntime, scanner *bufio.Scanner) (containerRuntime, bool) {
	fixed := false
	selectedRuntime := runtime

	// Container runtime: enforce the selected runtime instead of accepting
	// any available engine. Podman is the default; Docker is an explicit opt-out.
	if _, err := upLookPath(runtime.binaryName()); err != nil && runtime == containerRuntimePodman {
		if _, dockerErr := upLookPath(containerRuntimeDocker.binaryName()); dockerErr == nil {
			switch promptPodmanFallbackWithScanner(cmd.OutOrStdout(), scanner) {
			case "install":
				if err := installPodman(cmd); err == nil {
					fixed = true
				}
			case "docker":
				selectedRuntime = containerRuntimeDocker
				fixed = true
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "using docker for this run; pass --runtime docker next time to opt out explicitly")
			}
		} else if promptYNWithScanner(cmd.OutOrStdout(), scanner, "Selected runtime podman is not installed. Install Podman now?") {
			if err := installPodman(cmd); err == nil {
				fixed = true
			}
		}
	}

	if _, err := upLookPath("devcontainer"); err != nil {
		if promptYNWithScanner(cmd.OutOrStdout(), scanner, "devcontainer CLI is not installed. Install it now?") {
			if err := installDevcontainer(cmd); err == nil {
				fixed = true
			}
		}
	}

	return selectedRuntime, fixed
}

func promptYN(w io.Writer, question string) bool {
	return promptYNWithScanner(w, bufio.NewScanner(upStdin), question)
}

func promptYNWithScanner(w io.Writer, scanner *bufio.Scanner, question string) bool {
	_, _ = fmt.Fprintf(w, "%s [y/N] ", question)
	if !scanner.Scan() {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(scanner.Text()), "y")
}

func promptPodmanFallbackWithScanner(w io.Writer, scanner *bufio.Scanner) string {
	_, _ = fmt.Fprint(w, "Selected runtime podman is not installed, but docker is available. Choose: [i] install Podman and continue, [d] use Docker for this run, [N] cancel ")
	if !scanner.Scan() {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
	case "i", "install", "podman":
		return "install"
	case "d", "docker":
		return "docker"
	default:
		return ""
	}
}

func installPodman(cmd *cobra.Command) error {
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "installing podman via apt-get...")
	c := exec.Command("sudo", "apt-get", "install", "-y", "podman")
	c.Stdin = upStdin
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.OutOrStderr()
	if err := upRunCmd(c); err != nil {
		_, _ = fmt.Fprintf(cmd.OutOrStderr(), "failed to install podman: %v\n", err)
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "podman installed successfully")
	return nil
}

func installDevcontainer(cmd *cobra.Command) error {
	npmBin, err := upLookPath("npm")
	if err != nil {
		_, _ = fmt.Fprintln(cmd.OutOrStderr(), "npm not found in PATH; install Node.js first, then run: npm install -g @devcontainers/cli")
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "installing devcontainer CLI via npm...")
	c := exec.Command(npmBin, "install", "-g", "@devcontainers/cli")
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.OutOrStderr()
	if err := upRunCmd(c); err != nil {
		_, _ = fmt.Fprintf(cmd.OutOrStderr(), "failed to install devcontainer CLI: %v\n", err)
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "devcontainer CLI installed successfully")
	return nil
}
