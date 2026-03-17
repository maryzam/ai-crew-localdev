package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/maryzam/ai-crew-localdev/internal/brokerclient"
	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/launcher"
	"github.com/spf13/cobra"
)

type doctorMode string

const (
	doctorModeHost      doctorMode = "host"
	doctorModeContainer doctorMode = "container"
)

type doctorStatus string

const (
	doctorStatusPass doctorStatus = "pass"
	doctorStatusFail doctorStatus = "fail"
	doctorStatusSkip doctorStatus = "skip"
)

type doctorCheck struct {
	Name        string       `json:"name"`
	Status      doctorStatus `json:"status"`
	Details     string       `json:"details"`
	Remediation string       `json:"remediation,omitempty"`
	Blocking    bool         `json:"blocking"`
}

type doctorReport struct {
	Mode       doctorMode    `json:"mode"`
	Ready      bool          `json:"ready"`
	RuntimeDir string        `json:"runtime_dir"`
	SocketPath string        `json:"socket_path"`
	Checks     []doctorCheck `json:"checks"`
}

var (
	doctorBrokerSock string
	doctorModeFlag   string
	doctorRepoPath   string
	doctorJSON       bool
)

var (
	doctorLookPath      = exec.LookPath
	doctorExecutable    = os.Executable
	doctorGetwd         = os.Getwd
	doctorLstat         = os.Lstat
	doctorStat          = os.Stat
	doctorBrokerHealth  = brokerHealthCheck
	doctorResolveRepo   = launcher.ResolveRepo
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Validate host and devcontainer readiness",
	Long: `Validates the local prerequisites required for brokered auth sessions.

Run with --mode host to check host-native sessions, or --mode container to
check the stricter prerequisites needed before launching the devcontainer.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runDoctor,
}

func init() {
	doctorCmd.Flags().StringVar(&doctorModeFlag, "mode", string(doctorModeHost), "readiness mode: host or container")
	doctorCmd.Flags().StringVar(&doctorBrokerSock, "broker-sock", "", "broker socket path (default: auto)")
	doctorCmd.Flags().StringVar(&doctorRepoPath, "repo", "", "path to a git repository to validate (default: current directory when inside a repo)")
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "emit machine-readable JSON output")
}

func runDoctor(cmd *cobra.Command, args []string) error {
	mode := doctorMode(doctorModeFlag)
	if mode != doctorModeHost && mode != doctorModeContainer {
		return fmt.Errorf("invalid --mode %q: expected host or container", doctorModeFlag)
	}

	runtimeDir := config.RuntimeBaseDir()
	socketPath := doctorBrokerSock
	if socketPath == "" {
		socketPath = config.DefaultSocketPath()
	}

	report := doctorReport{
		Mode:       mode,
		RuntimeDir: runtimeDir,
		SocketPath: socketPath,
	}

	report.Checks = append(report.Checks, checkRuntimeDir(runtimeDir))
	report.Checks = append(report.Checks, checkBrokerSocket(socketPath)...)
	report.Checks = append(report.Checks, checkRepoReadiness(doctorRepoPath))
	report.Checks = append(report.Checks, checkBinaryReadiness()...)
	if mode == doctorModeContainer {
		report.Checks = append(report.Checks, checkContainerWorkspace())
		report.Checks = append(report.Checks, checkContainerRuntime())
	}

	report.Ready = !hasBlockingFailure(report.Checks)

	if doctorJSON {
		if err := writeDoctorJSON(cmd.OutOrStdout(), report); err != nil {
			return err
		}
		if report.Ready {
			return nil
		}
		return fmt.Errorf("readiness checks failed")
	}

	writeDoctorText(cmd.OutOrStdout(), report)
	if report.Ready {
		return nil
	}
	return fmt.Errorf("readiness checks failed")
}

func checkRuntimeDir(path string) doctorCheck {
	info, err := doctorStat(path)
	if err != nil {
		return doctorCheck{
			Name:        "runtime-dir",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("runtime directory %s is not accessible: %v", path, err),
			Remediation: "Set XDG_RUNTIME_DIR to your user runtime directory and ensure it exists before launching ai-agent.",
			Blocking:    true,
		}
	}
	if !info.IsDir() {
		return doctorCheck{
			Name:        "runtime-dir",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("runtime path %s exists but is not a directory", path),
			Remediation: "Point XDG_RUNTIME_DIR at a directory such as /run/user/<uid>.",
			Blocking:    true,
		}
	}

	source := "fallback"
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		source = "XDG_RUNTIME_DIR"
	}
	return doctorCheck{
		Name:     "runtime-dir",
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("using %s from %s", path, source),
		Blocking: true,
	}
}

func checkBrokerSocket(path string) []doctorCheck {
	info, err := doctorLstat(path)
	if err != nil {
		return []doctorCheck{
			{
				Name:        "broker-socket",
				Status:      doctorStatusFail,
				Details:     fmt.Sprintf("expected broker socket at %s: %v", path, err),
				Remediation: fmt.Sprintf("Start the broker socket with `systemctl --user start ai-agent-broker.socket` or pass --broker-sock with the correct path. Expected path: %s", path),
				Blocking:    true,
			},
			{
				Name:        "broker-reachability",
				Status:      doctorStatusSkip,
				Details:     "skipped because the broker socket is missing",
				Remediation: fmt.Sprintf("Create the socket at %s before retrying.", path),
				Blocking:    true,
			},
		}
	}
	if info.Mode()&os.ModeSocket == 0 {
		return []doctorCheck{
			{
				Name:        "broker-socket",
				Status:      doctorStatusFail,
				Details:     fmt.Sprintf("%s exists but is not a Unix domain socket", path),
				Remediation: fmt.Sprintf("Remove the unexpected file at %s and restart the broker socket unit.", path),
				Blocking:    true,
			},
			{
				Name:        "broker-reachability",
				Status:      doctorStatusSkip,
				Details:     "skipped because the broker socket path is the wrong file type",
				Remediation: fmt.Sprintf("Fix %s so it is a live Unix socket before retrying.", path),
				Blocking:    true,
			},
		}
	}

	socketCheck := doctorCheck{
		Name:     "broker-socket",
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("found live socket path at %s", path),
		Blocking: true,
	}

	reachability := doctorCheck{
		Name:     "broker-reachability",
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("broker responded on %s", path),
		Blocking: true,
	}
	if err := doctorBrokerHealth(path); err != nil {
		reachability.Status = doctorStatusFail
		reachability.Details = fmt.Sprintf("broker health check failed for %s: %v", path, err)
		reachability.Remediation = "Ensure ai-agent-broker is running and that your user can connect to the broker socket."
	}

	return []doctorCheck{socketCheck, reachability}
}

func checkRepoReadiness(repoPath string) doctorCheck {
	candidate := repoPath
	if candidate == "" {
		cwd, err := doctorGetwd()
		if err != nil {
			return doctorCheck{
				Name:        "repo-remote",
				Status:      doctorStatusSkip,
				Details:     fmt.Sprintf("could not determine working directory: %v", err),
				Remediation: "Run from a repository checkout or pass --repo to validate a specific repository.",
				Blocking:    false,
			}
		}
		candidate = cwd
	}

	absPath, slug, isSSH, err := doctorResolveRepo(candidate)
	if err != nil {
		if repoPath != "" {
			return doctorCheck{
				Name:        "repo-remote",
				Status:      doctorStatusFail,
				Details:     err.Error(),
				Remediation: "Use --repo with a git checkout that has an HTTPS origin remote.",
				Blocking:    true,
			}
		}
		return doctorCheck{
			Name:        "repo-remote",
			Status:      doctorStatusSkip,
			Details:     "not currently in a git repository; skipping repo-specific HTTPS validation",
			Remediation: "Run `ai-agent doctor --repo /path/to/repo` to validate a specific checkout.",
			Blocking:    false,
		}
	}

	if isSSH {
		return doctorCheck{
			Name:        "repo-remote",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("repository %s resolves to %s via SSH", absPath, slug),
			Remediation: fmt.Sprintf("Switch the origin remote to HTTPS: `git -C %s remote set-url origin https://github.com/%s.git`", absPath, slug),
			Blocking:    true,
		}
	}

	return doctorCheck{
		Name:     "repo-remote",
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("validated HTTPS origin for %s (%s)", absPath, slug),
		Blocking: true,
	}
}

func checkBinaryReadiness() []doctorCheck {
	checks := []doctorCheck{checkCurrentExecutable()}
	checks = append(checks, checkResolvedBinary("ai-agent-credential-helper"))
	checks = append(checks, checkResolvedBinary("ai-agent-gh"))
	checks = append(checks, checkPathBinary("git"))
	checks = append(checks, checkPathBinary("gh"))
	return checks
}

func checkCurrentExecutable() doctorCheck {
	path, err := doctorExecutable()
	if err != nil {
		return doctorCheck{
			Name:        "binary-ai-agent",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("could not resolve ai-agent executable: %v", err),
			Remediation: "Build or install ai-agent before running the doctor command.",
			Blocking:    true,
		}
	}
	if _, err := doctorStat(path); err != nil {
		return doctorCheck{
			Name:        "binary-ai-agent",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("ai-agent executable %s is not accessible: %v", path, err),
			Remediation: "Reinstall ai-agent or fix the binary path.",
			Blocking:    true,
		}
	}
	return doctorCheck{
		Name:     "binary-ai-agent",
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("found ai-agent at %s", path),
		Blocking: true,
	}
}

func checkResolvedBinary(name string) doctorCheck {
	path, err := resolveOptionalBinary(name)
	if err != nil {
		return doctorCheck{
			Name:        "binary-" + name,
			Status:      doctorStatusFail,
			Details:     err.Error(),
			Remediation: fmt.Sprintf("Build or install %s next to ai-agent or add it to PATH.", name),
			Blocking:    true,
		}
	}
	return doctorCheck{
		Name:     "binary-" + name,
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("found %s at %s", name, path),
		Blocking: true,
	}
}

func checkPathBinary(name string) doctorCheck {
	path, err := doctorLookPath(name)
	if err != nil {
		return doctorCheck{
			Name:        "binary-" + name,
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("%s not found in PATH", name),
			Remediation: fmt.Sprintf("Install %s or add it to PATH before launching managed sessions.", name),
			Blocking:    true,
		}
	}
	return doctorCheck{
		Name:     "binary-" + name,
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("found %s at %s", name, path),
		Blocking: true,
	}
}

func checkContainerWorkspace() doctorCheck {
	workspace := os.Getenv("AI_AGENT_WORKSPACE")
	if workspace == "" {
		return doctorCheck{
			Name:        "container-workspace",
			Status:      doctorStatusFail,
			Details:     "AI_AGENT_WORKSPACE is not set",
			Remediation: "Export AI_AGENT_WORKSPACE to the host directory that should be mounted into /workspace before launching the devcontainer.",
			Blocking:    true,
		}
	}
	info, err := doctorStat(workspace)
	if err != nil {
		return doctorCheck{
			Name:        "container-workspace",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("AI_AGENT_WORKSPACE=%s is not accessible: %v", workspace, err),
			Remediation: "Point AI_AGENT_WORKSPACE at an existing host directory before launching the devcontainer.",
			Blocking:    true,
		}
	}
	if !info.IsDir() {
		return doctorCheck{
			Name:        "container-workspace",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("AI_AGENT_WORKSPACE=%s is not a directory", workspace),
			Remediation: "Point AI_AGENT_WORKSPACE at a directory that can be bind-mounted into the container.",
			Blocking:    true,
		}
	}
	return doctorCheck{
		Name:     "container-workspace",
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("workspace source %s is ready to mount", workspace),
		Blocking: true,
	}
}

func checkContainerRuntime() doctorCheck {
	var found []string
	var missing []string
	for _, candidate := range []string{"podman", "devcontainer"} {
		if path, err := doctorLookPath(candidate); err == nil {
			found = append(found, fmt.Sprintf("%s=%s", candidate, path))
		} else {
			missing = append(missing, candidate)
		}
	}
	if len(missing) > 0 {
		return doctorCheck{
			Name:        "container-runtime",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("missing container tooling: %s", joinWithComma(missing)),
			Remediation: "Install both Podman and the devcontainer CLI before using the container readiness flow.",
			Blocking:    true,
		}
	}
	return doctorCheck{
		Name:     "container-runtime",
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("available runtime tooling: %s", joinWithComma(found)),
		Blocking: true,
	}
}

func hasBlockingFailure(checks []doctorCheck) bool {
	for _, check := range checks {
		if check.Blocking && check.Status == doctorStatusFail {
			return true
		}
	}
	return false
}

func writeDoctorText(w io.Writer, report doctorReport) {
	fmt.Fprintf(w, "ai-agent doctor (%s)\n", report.Mode)
	for _, check := range report.Checks {
		fmt.Fprintf(w, "[%s] %s: %s\n", string(check.Status), check.Name, check.Details)
		if check.Remediation != "" && check.Status != doctorStatusPass {
			fmt.Fprintf(w, "  fix: %s\n", check.Remediation)
		}
	}
	if report.Ready {
		fmt.Fprintln(w, "ready: all blocking checks passed")
		return
	}
	fmt.Fprintln(w, "not ready: fix the failing checks above")
}

func writeDoctorJSON(w io.Writer, report doctorReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func brokerHealthCheck(socketPath string) error {
	resp, err := (&brokerclient.Client{SocketPath: socketPath}).HealthCheck()
	if err != nil {
		return err
	}
	if !resp.Healthy {
		return fmt.Errorf("broker responded unhealthy")
	}
	return nil
}

func joinWithComma(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		out := parts[0]
		for _, part := range parts[1:] {
			out += ", " + part
		}
		return out
	}
}
