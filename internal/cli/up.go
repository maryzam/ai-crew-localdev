package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/spf13/cobra"
)

var (
	upWorkspace string
	upBuild     bool
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
  ai-agent up --build`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runUp,
}

func init() {
	upCmd.Flags().StringVar(&upWorkspace, "workspace", ".", "path to the workspace directory to mount")
	upCmd.Flags().BoolVar(&upBuild, "build", false, "force rebuild of the devcontainer image")
}

// upLookPath is a test seam for exec.LookPath.
var upLookPath = exec.LookPath

func runUp(cmd *cobra.Command, args []string) error {
	// 1. Resolve workspace.
	workspace, err := filepath.Abs(upWorkspace)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	os.Setenv("AI_AGENT_WORKSPACE", workspace)

	runtimeDir := config.RuntimeBaseDir()
	os.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	// Ensure runtime subdirectory exists.
	aiAgentRuntime := filepath.Join(runtimeDir, "ai-agent")
	if err := os.MkdirAll(aiAgentRuntime, 0o700); err != nil {
		return fmt.Errorf("create runtime dir %s: %w", aiAgentRuntime, err)
	}

	// 2. Ensure broker is running.
	socketPath := config.DefaultSocketPath()
	if err := ensureBroker(socketPath); err != nil {
		return fmt.Errorf("broker startup: %w", err)
	}

	// 3. Run doctor checks.
	report := buildDoctorReport(doctorModeContainer, socketPath, workspace)
	if !report.Ready {
		writeDoctorText(cmd.OutOrStdout(), report)
		return fmt.Errorf("readiness checks failed; fix the issues above before running 'ai-agent up'")
	}
	fmt.Fprintln(cmd.OutOrStdout(), "doctor: all checks passed")

	// 4. Find devcontainer CLI.
	devcontainerBin, err := upLookPath("devcontainer")
	if err != nil {
		return fmt.Errorf("devcontainer CLI not found in PATH: %w", err)
	}

	// 5. Devcontainer up.
	repoRoot, err := findRepoRoot(workspace)
	if err != nil {
		return fmt.Errorf("find repo root: %w", err)
	}

	upArgs := []string{"up", "--workspace-folder", repoRoot}
	if upBuild {
		upArgs = append(upArgs, "--build-no-cache")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "launching devcontainer in %s\n", repoRoot)
	upCmd := exec.Command(devcontainerBin, upArgs...)
	upCmd.Stdout = cmd.OutOrStdout()
	upCmd.Stderr = cmd.OutOrStderr()
	if err := upCmd.Run(); err != nil {
		return fmt.Errorf("devcontainer up: %w", err)
	}

	// 6. Devcontainer exec — interactive shell.
	execArgs := []string{"exec", "--workspace-folder", repoRoot, "bash"}
	fmt.Fprintln(cmd.OutOrStdout(), "opening shell in devcontainer")
	shellCmd := exec.Command(devcontainerBin, execArgs...)
	shellCmd.Stdin = os.Stdin
	shellCmd.Stdout = cmd.OutOrStdout()
	shellCmd.Stderr = cmd.OutOrStderr()
	return shellCmd.Run()
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

	cmd := exec.Command(brokerBin)
	cmd.Stdout = os.Stderr // broker logs to stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start broker: %w", err)
	}

	if !waitForBroker(socketPath, 5*time.Second) {
		return fmt.Errorf("broker did not become ready within 5s at %s", socketPath)
	}
	return nil
}

func brokerResponds(socketPath string) bool {
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
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
