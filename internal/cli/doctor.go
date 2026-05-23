package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"

	"github.com/maryzam/ai-crew-localdev/internal/brokerclient"
	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/launcher"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/spf13/cobra"
)

type doctorMode string

const (
	doctorModeHost      doctorMode = "host"
	doctorModeContainer doctorMode = "container"
	doctorModeUp        doctorMode = "up"
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
	doctorRuntime    string
	doctorJSON       bool
)

var (
	doctorLookPath     = exec.LookPath
	doctorExecutable   = os.Executable
	doctorGetwd        = os.Getwd
	doctorLstat        = os.Lstat
	doctorStat         = os.Stat
	doctorOpen         = os.Open
	doctorBrokerHealth = brokerHealthCheck
	doctorResolveRepo  = launcher.ResolveRepo
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
	doctorCmd.Flags().StringVar(&doctorRuntime, "runtime", string(containerRuntimePodman), "container runtime to validate in container mode: podman or docker")
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "emit machine-readable JSON output")
}

func runDoctor(cmd *cobra.Command, args []string) error {
	mode := doctorMode(doctorModeFlag)
	if mode != doctorModeHost && mode != doctorModeContainer {
		return fmt.Errorf("invalid --mode %q: expected host or container", doctorModeFlag)
	}
	runtime, err := parseContainerRuntime(doctorRuntime)
	if err != nil {
		return err
	}

	socketPath := doctorBrokerSock
	if socketPath == "" {
		socketPath = config.DefaultSocketPath()
	}

	report := buildDoctorReport(mode, socketPath, doctorRepoPath, runtime)

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

// buildDoctorReport runs all readiness checks for the given mode and returns
// the report. Extracted so that other commands (e.g. "up") can call it
// programmatically without going through the Cobra flag layer.
func buildDoctorReport(mode doctorMode, socketPath, repoPath string, runtime containerRuntime) doctorReport {
	runtimeDir := config.RuntimeBaseDir()

	report := doctorReport{
		Mode:       mode,
		RuntimeDir: runtimeDir,
		SocketPath: socketPath,
	}

	report.Checks = append(report.Checks, checkRuntimeDir(runtimeDir))
	report.Checks = append(report.Checks, checkBrokerSocket(socketPath)...)
	if mode != doctorModeUp {
		// The "up" mode skips repo-remote validation because the workspace
		// is a repos directory, not necessarily a git checkout.
		report.Checks = append(report.Checks, checkRepoReadiness(repoPath))
	}
	report.Checks = append(report.Checks, checkBrokerConfigReadiness()...)
	if mode == doctorModeUp {
		// The "up" mode only needs host-side binaries that are required
		// before the container starts. gh lives inside the container.
		report.Checks = append(report.Checks, checkBinaryReadinessForUp()...)
	} else {
		report.Checks = append(report.Checks, checkBinaryReadiness()...)
	}
	if mode == doctorModeContainer || mode == doctorModeUp {
		report.Checks = append(report.Checks, checkContainerWorkspace())
		report.Checks = append(report.Checks, checkContainerRuntime(runtime))
	}

	report.Ready = !hasBlockingFailure(report.Checks)
	return report
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

// checkBinaryReadinessForUp checks only binaries needed on the host before
// the devcontainer starts. gh is provided inside the container image, so
// it is not required on the host for the "up" command.
func checkBinaryReadinessForUp() []doctorCheck {
	checks := []doctorCheck{checkCurrentExecutable()}
	checks = append(checks, checkResolvedBinary("ai-agent-credential-helper"))
	checks = append(checks, checkResolvedBinary("ai-agent-gh"))
	checks = append(checks, checkPathBinary("git"))
	return checks
}

func checkBrokerConfigReadiness() []doctorCheck {
	identitiesPath := config.ExpandHome(config.DefaultIdentitiesPath())
	idents, identityCheck := loadIdentitiesCheck(identitiesPath)

	policyPath := os.Getenv("AI_AGENT_POLICY_PATH")
	if policyPath == "" {
		policyPath = config.DefaultPolicyPath()
	}
	policyPath = config.ExpandHome(policyPath)
	pol, policyCheck := loadPolicyCheck(policyPath)

	checks := []doctorCheck{identityCheck, policyCheck}
	if idents != nil {
		checks = append(checks, checkIdentityKeys(*idents)...)
	}
	if idents != nil && pol != nil {
		checks = append(checks, checkInstallationIDs(*idents, *pol, policyPath))
	}
	return checks
}

func loadIdentitiesCheck(path string) (*identity.IdentitiesFile, doctorCheck) {
	idents, err := identity.Load(path)
	if err != nil {
		return nil, doctorCheck{
			Name:        "broker-identities",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("failed to load identities file %s: %v", path, err),
			Remediation: fmt.Sprintf("Create or fix %s before starting brokered sessions.", path),
			Blocking:    true,
		}
	}

	if errs := identity.Validate(idents); errs.HasErrors() {
		return nil, doctorCheck{
			Name:        "broker-identities",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("identities file %s is invalid: %s", path, errs.Error()),
			Remediation: fmt.Sprintf("Fix the identities file at %s so every agent has an app_id, git name, and git email.", path),
			Blocking:    true,
		}
	}

	return idents, doctorCheck{
		Name:     "broker-identities",
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("validated identities file %s for %d agent(s)", path, len(idents.Agents)),
		Blocking: true,
	}
}

func loadPolicyCheck(path string) (*policy.PolicyFile, doctorCheck) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, doctorCheck{
			Name:        "broker-policy",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("failed to read policy file %s: %v", path, err),
			Remediation: fmt.Sprintf("Create or fix %s before starting brokered sessions.", path),
			Blocking:    true,
		}
	}

	pol, err := policy.ParsePolicy(data)
	if err != nil {
		return nil, doctorCheck{
			Name:        "broker-policy",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("failed to parse policy file %s: %v", path, err),
			Remediation: fmt.Sprintf("Fix the JSON syntax in %s before retrying.", path),
			Blocking:    true,
		}
	}

	result := policy.Validate(pol)
	if result.Errors.HasErrors() {
		return nil, doctorCheck{
			Name:        "broker-policy",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("policy file %s is invalid: %s", path, result.Errors.Error()),
			Remediation: fmt.Sprintf("Run `ai-agent policy validate --policy %s` and fix the reported errors.", path),
			Blocking:    true,
		}
	}

	details := fmt.Sprintf("validated policy file %s for %d agent(s)", path, len(pol.Agents))
	if len(result.Warnings) > 0 {
		details = fmt.Sprintf("%s with %d warning(s)", details, len(result.Warnings))
	}

	return pol, doctorCheck{
		Name:     "broker-policy",
		Status:   doctorStatusPass,
		Details:  details,
		Blocking: true,
	}
}

func checkIdentityKeys(idents identity.IdentitiesFile) []doctorCheck {
	agents := sortedAgentNames(idents.Agents)
	missing := make([]string, 0)
	unreadable := make([]string, 0)

	for _, name := range agents {
		keyPath := config.ExpandHome(idents.Agents[name].AppKey)
		if keyPath == "" {
			missing = append(missing, name)
			continue
		}

		info, err := doctorStat(keyPath)
		if err != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s=%s (%v)", name, keyPath, err))
			continue
		}
		if info.IsDir() {
			unreadable = append(unreadable, fmt.Sprintf("%s=%s (is a directory)", name, keyPath))
			continue
		}

		file, err := doctorOpen(keyPath)
		if err != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s=%s (%v)", name, keyPath, err))
			continue
		}
		_ = file.Close()
	}

	if len(missing) > 0 || len(unreadable) > 0 {
		details := ""
		if len(missing) > 0 {
			details = "missing app_key for " + joinWithComma(missing)
		}
		if len(unreadable) > 0 {
			if details != "" {
				details += "; "
			}
			details += "unreadable PEM paths: " + joinWithComma(unreadable)
		}

		return []doctorCheck{{
			Name:        "broker-pem-files",
			Status:      doctorStatusFail,
			Details:     details,
			Remediation: "Set each agent app_key to a readable PEM file on the host before starting the broker.",
			Blocking:    true,
		}}
	}

	return []doctorCheck{{
		Name:     "broker-pem-files",
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("validated readable PEM paths for %d agent(s)", len(agents)),
		Blocking: true,
	}}
}

func checkInstallationIDs(idents identity.IdentitiesFile, pol policy.PolicyFile, policyPath string) doctorCheck {
	missing := make([]string, 0)
	for _, name := range sortedAgentNames(idents.Agents) {
		agentPolicy, ok := pol.Agents[name]
		if !ok || !hasInstallationID(agentPolicy) {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		return doctorCheck{
			Name:        "broker-installation-ids",
			Status:      doctorStatusFail,
			Details:     "missing installation_id for " + joinWithComma(missing),
			Remediation: fmt.Sprintf("Set installation_id for each configured agent in %s before starting brokered sessions.", policyPath),
			Blocking:    true,
		}
	}

	return doctorCheck{
		Name:     "broker-installation-ids",
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("validated installation IDs for %d agent(s)", len(idents.Agents)),
		Blocking: true,
	}
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

func checkContainerRuntime(runtime containerRuntime) doctorCheck {
	var found []string
	var missing []string

	if path, err := doctorLookPath(runtime.binaryName()); err == nil {
		found = append(found, fmt.Sprintf("%s=%s", runtime.binaryName(), path))
	} else {
		missing = append(missing, runtime.binaryName())
	}

	if alternate := runtime.alternate(); alternate != "" {
		if path, err := doctorLookPath(alternate.binaryName()); err == nil {
			found = append(found, fmt.Sprintf("%s=%s", alternate.binaryName(), path))
		}
	}

	// devcontainer CLI is always required.
	if path, err := doctorLookPath("devcontainer"); err == nil {
		found = append(found, fmt.Sprintf("devcontainer=%s", path))
	} else {
		missing = append(missing, "devcontainer")
	}

	if len(missing) > 0 {
		remediation := fmt.Sprintf("Install %s and the devcontainer CLI before using the container readiness flow.", runtime.binaryName())
		if len(missing) == 1 && missing[0] == runtime.binaryName() {
			remediation = fmt.Sprintf("Install %s before using the container readiness flow.", runtime.binaryName())
		}
		if len(missing) == 1 && missing[0] == "devcontainer" {
			remediation = "Install the devcontainer CLI before using the container readiness flow."
		}
		if alternate := runtime.alternate(); alternate != "" {
			remediation += fmt.Sprintf(" To opt out explicitly, rerun with --runtime %s.", alternate.binaryName())
		}
		return doctorCheck{
			Name:        "container-runtime",
			Status:      doctorStatusFail,
			Details:     fmt.Sprintf("selected runtime %s is not ready; found: %s; missing: %s", runtime.binaryName(), joinWithComma(found), joinWithComma(missing)),
			Remediation: remediation,
			Blocking:    true,
		}
	}
	return doctorCheck{
		Name:     "container-runtime",
		Status:   doctorStatusPass,
		Details:  fmt.Sprintf("selected runtime %s is ready: %s", runtime.binaryName(), joinWithComma(found)),
		Blocking: true,
	}
}

func hasInstallationID(ap policy.AgentPolicy) bool {
	section, ok := ap.Providers["github"]
	if !ok || len(section) == 0 || string(section) == "null" {
		return false
	}
	var s struct {
		InstallationID int64 `json:"installation_id"`
	}
	if err := json.Unmarshal(section, &s); err != nil {
		return false
	}
	return s.InstallationID > 0
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
	_, _ = fmt.Fprintf(w, "ai-agent doctor (%s)\n", report.Mode)
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(w, "[%s] %s: %s\n", string(check.Status), check.Name, check.Details)
		if check.Remediation != "" && check.Status != doctorStatusPass {
			_, _ = fmt.Fprintf(w, "  fix: %s\n", check.Remediation)
		}
	}
	if report.Ready {
		_, _ = fmt.Fprintln(w, "ready: all blocking checks passed")
		return
	}
	_, _ = fmt.Fprintln(w, "not ready: fix the failing checks above")
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

func sortedAgentNames[T any](agents map[string]T) []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
