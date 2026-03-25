package cli

import (
	"bufio"
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
	upBuild     bool
	upLangfuse  bool
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Bootstrap the full local dev environment in one command",
	Long: `Ensures the broker is running, validates host readiness, builds (if needed)
and launches the devcontainer, then opens an interactive shell inside it.

This is the single supported entrypoint for the ai-agent local dev environment.

Examples:
  ai-agent up
  ai-agent up --workspace ~/github/my-project
  ai-agent up --build
  ai-agent up --langfuse`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runUp,
}

func init() {
	upCmd.Flags().StringVar(&upWorkspace, "workspace", ".", "path to the workspace directory to mount")
	upCmd.Flags().BoolVar(&upBuild, "build", false, "force rebuild of the devcontainer image")
	upCmd.Flags().BoolVar(&upLangfuse, "langfuse", false, "start Langfuse observability stack as a sidecar")
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
	// 1. Resolve workspace (the directory containing user repos).
	workspace, err := filepath.Abs(upWorkspace)
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
	report := buildDoctorReport(doctorModeUp, socketPath, "")
	if !report.Ready {
		if tryAutoFix(cmd, report) {
			// Re-run doctor after fixes.
			report = buildDoctorReport(doctorModeUp, socketPath, "")
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

	// 6. Devcontainer up.
	// Find the project root containing .devcontainer/. Search from the
	// executable's directory first (works after `make install` if the
	// binary is still co-located with the repo), then fall back to CWD.
	repoRoot, err := findDevcontainerRoot()
	if err != nil {
		return fmt.Errorf("find devcontainer root: %w", err)
	}

	upArgs := []string{"up", "--workspace-folder", repoRoot}
	if upBuild {
		upArgs = append(upArgs, "--build-no-cache")
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "launching devcontainer in %s\n", repoRoot)
	dcUpCmd := exec.Command(devcontainerBin, upArgs...)
	dcUpCmd.Stdout = cmd.OutOrStdout()
	dcUpCmd.Stderr = cmd.OutOrStderr()
	if err := dcUpCmd.Run(); err != nil {
		return fmt.Errorf("devcontainer up: %w", err)
	}
	writeDevcontainerAccessInfo(cmd.OutOrStdout(), repoRoot)

	// 7. Devcontainer exec — interactive shell.
	execArgs := []string{"exec", "--workspace-folder", repoRoot, "bash"}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "opening shell in devcontainer")
	shellCmd := exec.Command(devcontainerBin, execArgs...)
	shellCmd.Stdin = os.Stdin
	shellCmd.Stdout = cmd.OutOrStdout()
	shellCmd.Stderr = cmd.OutOrStderr()
	if err := shellCmd.Run(); err != nil {
		return fmt.Errorf("open shell in devcontainer: %w (re-enter with: %s)", err, devcontainerExecCommand(repoRoot))
	}
	return nil
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

func writeDevcontainerAccessInfo(w io.Writer, repoRoot string) {
	_, _ = fmt.Fprintf(w, "devcontainer is ready; your host workspace %s is mounted at /workspace\n", os.Getenv("AI_AGENT_WORKSPACE"))
	_, _ = fmt.Fprintf(w, "re-enter later with: %s\n", devcontainerExecCommand(repoRoot))
	_, _ = fmt.Fprintf(w, "find the backing container with: docker ps --filter %q\n", devcontainerLabelFilter(repoRoot))
	_, _ = fmt.Fprintf(w, "or: podman ps --filter %q\n", devcontainerLabelFilter(repoRoot))
}

func devcontainerExecCommand(repoRoot string) string {
	return fmt.Sprintf("devcontainer exec --workspace-folder %s bash", repoRoot)
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
// container tooling interactively. Returns true if any fix was applied.
func tryAutoFix(cmd *cobra.Command, report doctorReport) bool {
	for _, check := range report.Checks {
		if check.Name == "container-runtime" && check.Status == doctorStatusFail {
			return upInstallFn(cmd)
		}
	}
	return false
}

// installMissing checks for each container-runtime prerequisite individually,
// prompts the user for approval, and installs it.
func installMissing(cmd *cobra.Command) bool {
	fixed := false

	if _, err := upLookPath("podman"); err != nil {
		if promptYN(cmd.OutOrStdout(), "Podman is not installed. Install it now?") {
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

	return fixed
}

func promptYN(w io.Writer, question string) bool {
	_, _ = fmt.Fprintf(w, "%s [y/N] ", question)
	scanner := bufio.NewScanner(upStdin)
	if !scanner.Scan() {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(scanner.Text()), "y")
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
