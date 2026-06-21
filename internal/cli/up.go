package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/config"
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
)

func runUp(cmd *cobra.Command, args []string) error {
	runtime, err := parseContainerRuntime(upRuntime)
	if err != nil {
		return err
	}

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

	// 2. Ensure broker is running.
	socketPath := config.DefaultSocketPath()
	if err := ensureBroker(socketPath); err != nil {
		return fmt.Errorf("broker startup: %w", err)
	}

	// 3. Optionally start Langfuse observability stack.
	if upLangfuse {
		if err := startLangfuse(cmd); err != nil {
			return fmt.Errorf("langfuse startup: %w", err)
		}
	}

	// 4. Run doctor checks — use "up" mode which skips repo-remote and
	// host gh checks that are irrelevant for the bootstrap command.
	report := buildDoctorReport(doctorModeUp, socketPath, "", runtime)
	if !report.Ready {
		var fixed bool
		runtime, fixed = tryAutoFix(cmd, report, runtime)
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

	// 5. Find devcontainer CLI.
	devcontainerBin, err := upLookPath("devcontainer")
	if err != nil {
		return fmt.Errorf("devcontainer CLI not found in PATH: %w", err)
	}

	if upProject != "" {
		return launchProjectDevcontainer(cmd, devcontainerBin, runtime, workspace)
	}

	// 6. Devcontainer up.
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

	// 7. Devcontainer exec — interactive shell.
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

// aiAgentBinaries are injected into a project devcontainer so brokered auth
// works without the project's own image having to bake them in.
var aiAgentBinaries = []string{"ai-agent", "ai-agent-gh", "ai-agent-credential-helper"}

// launchProjectDevcontainer brings up the target project's own devcontainer —
// its runtimes, services, ports, and postCreate — and injects the broker
// overlay so agents inside it authenticate through the host broker.
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

	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "project devcontainer ready; broker socket and ai-agent toolchain injected")
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

func projectHasDevcontainer(project string) bool {
	_, ok := projectDevcontainerConfigPath(project)
	return ok
}

func projectDevcontainerConfigPath(project string) (string, bool) {
	for _, p := range []string{
		filepath.Join(project, ".devcontainer", "devcontainer.json"),
		filepath.Join(project, ".devcontainer.json"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
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

// brokerOverlayArgs builds the devcontainer flags that bind-mount the host
// broker socket and ai-agent toolchain into a project container and expose
// them via PATH and AI_AGENT_AUTH_SOCK. Both mounts are read-only: the
// container connects to the broker socket (a connect() succeeds on a read-only
// mount) and executes the injected binaries, but cannot mutate the host runtime
// dir or replace the host toolchain.
func brokerOverlayArgs(project string) ([]string, error) {
	binDir, err := aiAgentBinDir()
	if err != nil {
		return nil, err
	}

	socketDir := config.RuntimeDir()
	socketName := filepath.Base(config.DefaultSocketPath())
	overlayPath, err := writeBrokerOverlayConfig(project, socketDir, socketName, binDir)
	if err != nil {
		return nil, err
	}
	args := []string{"--override-config", overlayPath}
	args = append(args,
		"--remote-env", "AI_AGENT_AUTH_SOCK=/run/ai-agent/"+socketName,
		"--remote-env", "PATH=/usr/local/ai-agent/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	)
	return args, nil
}

func writeBrokerOverlayConfig(project string, socketDir string, socketName string, binDir string) (string, error) {
	configPath, ok := projectDevcontainerConfigPath(project)
	if !ok {
		return "", fmt.Errorf("project %s has no devcontainer config", project)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read project devcontainer config: %w", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(stripJSONTrailingCommas(stripJSONComments(data)), &merged); err != nil {
		return "", fmt.Errorf("parse project devcontainer config %s: %w", configPath, err)
	}

	mounts := []string{
		fmt.Sprintf("source=%s,target=/run/ai-agent,type=bind,readonly", socketDir),
	}
	for _, b := range aiAgentBinaries {
		mounts = append(mounts,
			fmt.Sprintf("source=%s,target=/usr/local/ai-agent/bin/%s,type=bind,readonly", filepath.Join(binDir, b), b))
	}

	if _, ok := merged["dockerComposeFile"]; ok {
		composeOverlayPath, err := writeBrokerComposeOverlayConfig(merged, socketDir, binDir)
		if err != nil {
			return "", err
		}
		merged["dockerComposeFile"] = appendDockerComposeFile(merged["dockerComposeFile"], composeOverlayPath)
	} else {
		existingMounts, _ := merged["mounts"].([]any)
		for _, mount := range mounts {
			existingMounts = append(existingMounts, mount)
		}
		merged["mounts"] = existingMounts
	}

	data, err = json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode broker overlay config: %w", err)
	}
	data = append(data, '\n')

	runtimeDir := config.RuntimeDir()
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("create runtime dir for broker overlay config: %w", err)
	}
	path := filepath.Join(runtimeDir, "devcontainer-broker-overlay-"+socketName+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write broker overlay config: %w", err)
	}
	return path, nil
}

func writeBrokerComposeOverlayConfig(projectConfig map[string]any, socketDir string, binDir string) (string, error) {
	service, ok := projectConfig["service"].(string)
	if !ok || service == "" {
		return "", fmt.Errorf("project devcontainer uses dockerComposeFile but has no service")
	}

	lines := []string{
		"services:",
		fmt.Sprintf("  %s:", quoteYAMLString(service)),
		"    volumes:",
		fmt.Sprintf("      - %s", quoteYAMLString(socketDir+":/run/ai-agent:ro")),
	}
	for _, b := range aiAgentBinaries {
		lines = append(lines,
			fmt.Sprintf("      - %s", quoteYAMLString(filepath.Join(binDir, b)+":/usr/local/ai-agent/bin/"+b+":ro")))
	}
	data := []byte(strings.Join(lines, "\n") + "\n")

	runtimeDir := config.RuntimeDir()
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("create runtime dir for broker compose overlay: %w", err)
	}
	path := filepath.Join(runtimeDir, "devcontainer-broker-compose-overlay.yml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write broker compose overlay: %w", err)
	}
	return path, nil
}

func appendDockerComposeFile(current any, overlayPath string) any {
	switch v := current.(type) {
	case []any:
		return append(v, overlayPath)
	case []string:
		out := make([]string, 0, len(v)+1)
		out = append(out, v...)
		return append(out, overlayPath)
	case string:
		if v == "" {
			return overlayPath
		}
		return []any{v, overlayPath}
	default:
		return overlayPath
	}
}

func quoteYAMLString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func stripJSONComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(data) {
			switch data[i+1] {
			case '/':
				for i < len(data) && data[i] != '\n' {
					i++
				}
				if i < len(data) {
					out = append(out, data[i])
				}
				continue
			case '*':
				i += 2
				for i+1 < len(data) && (data[i] != '*' || data[i+1] != '/') {
					if data[i] == '\n' {
						out = append(out, '\n')
					}
					i++
				}
				i++
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func stripJSONTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\r' || data[j] == '\n') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func aiAgentBinDir() (string, error) {
	self, err := osExecutable()
	if err != nil {
		return "", fmt.Errorf("locate ai-agent binary: %w", err)
	}
	dir := filepath.Dir(self)
	for _, b := range aiAgentBinaries {
		if _, err := os.Stat(filepath.Join(dir, b)); err != nil {
			return "", fmt.Errorf("ai-agent toolchain incomplete in %s (missing %s); run 'make install'", dir, b)
		}
	}
	return dir, nil
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
		if err := os.WriteFile(envPath, data, 0o600); err != nil {
			return fmt.Errorf("write .env: %w", err)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "langfuse: created .env from .env.example (review and change secrets before production use)")
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
func tryAutoFix(cmd *cobra.Command, report doctorReport, runtime containerRuntime) (containerRuntime, bool) {
	for _, check := range report.Checks {
		if check.Name == "container-runtime" && check.Status == doctorStatusFail {
			return upInstallFn(cmd, runtime)
		}
	}
	return runtime, false
}

// installMissing checks for each container-runtime prerequisite individually,
// prompts the user for approval, and installs it.
func installMissing(cmd *cobra.Command, runtime containerRuntime) (containerRuntime, bool) {
	fixed := false
	selectedRuntime := runtime

	// Container runtime: enforce the selected runtime instead of accepting
	// any available engine. Podman is the default; Docker is an explicit opt-out.
	if _, err := upLookPath(runtime.binaryName()); err != nil && runtime == containerRuntimePodman {
		if _, dockerErr := upLookPath(containerRuntimeDocker.binaryName()); dockerErr == nil {
			switch promptPodmanFallback(cmd.OutOrStdout()) {
			case "install":
				if err := installPodman(cmd); err == nil {
					fixed = true
				}
			case "docker":
				selectedRuntime = containerRuntimeDocker
				fixed = true
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "using docker for this run; pass --runtime docker next time to opt out explicitly")
			}
		} else if promptYN(cmd.OutOrStdout(), "Selected runtime podman is not installed. Install Podman now?") {
			if err := installPodman(cmd); err == nil {
				fixed = true
			}
		}
	}

	if _, err := upLookPath("devcontainer"); err != nil {
		if promptYN(cmd.OutOrStdout(), "devcontainer CLI is not installed. Install it now?") {
			if err := installDevcontainer(cmd); err == nil {
				fixed = true
			}
		}
	}

	return selectedRuntime, fixed
}

func promptYN(w io.Writer, question string) bool {
	_, _ = fmt.Fprintf(w, "%s [y/N] ", question)
	scanner := bufio.NewScanner(upStdin)
	if !scanner.Scan() {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(scanner.Text()), "y")
}

func promptPodmanFallback(w io.Writer) string {
	_, _ = fmt.Fprint(w, "Selected runtime podman is not installed, but docker is available. Choose: [i] install Podman and continue, [d] use Docker for this run, [N] cancel ")
	scanner := bufio.NewScanner(upStdin)
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
