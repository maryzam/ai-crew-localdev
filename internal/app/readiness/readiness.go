package readiness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/governance"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
	"github.com/maryzam/ai-crew-localdev/internal/providers/capabilities"
)

type Mode string

const (
	ModeHost      Mode = "host"
	ModeContainer Mode = "container"
	ModeUp        Mode = "up"
)

type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusWarn Status = "warn"
	StatusSkip Status = "skip"
)

const pemRotationReminderAge = 180 * 24 * time.Hour

const maxBrokerPEMBytes = 1 << 20

type Check struct {
	Name        string            `json:"name"`
	Status      Status            `json:"status"`
	Severity    Severity          `json:"severity"`
	Owner       Owner             `json:"owner"`
	Details     string            `json:"details"`
	Remediation string            `json:"remediation,omitempty"`
	Evidence    map[string]string `json:"evidence,omitempty"`
}

type Report struct {
	Mode       Mode    `json:"mode"`
	Ready      bool    `json:"ready"`
	Outcome    Status  `json:"outcome"`
	RuntimeDir string  `json:"runtime_dir"`
	SocketPath string  `json:"socket_path"`
	Checks     []Check `json:"checks"`
}

type Input struct {
	Mode             Mode
	RuntimeDir       string
	RuntimeSource    string
	SocketPath       string
	RepoPath         string
	Workspace        string
	IdentitiesPath   string
	PolicyPath       string
	ContainerRuntime string
}

type Dependencies struct {
	Stat              func(string) (os.FileInfo, error)
	Lstat             func(string) (os.FileInfo, error)
	WorkingDir        func() (string, error)
	Executable        func() (string, error)
	ExpandPath        func(string) string
	FindBinary        func(string) (string, error)
	CheckBroker       func(string) error
	ResolveRepo       func(string) (string, string, bool, error)
	LoadConfiguration func(string, string) (governance.Snapshot, error)
	ValidatePolicy    func(*policy.PolicyFile, *identity.IdentitiesFile) error
	Now               func() time.Time
}

type Service struct {
	deps Dependencies
}

func New(deps Dependencies) Service {
	return Service{deps: deps}
}

func (s Service) Run(input Input) Report {
	report := Report{Mode: input.Mode, RuntimeDir: input.RuntimeDir, SocketPath: input.SocketPath}
	report.Checks = append(report.Checks, s.RuntimeDir(input.RuntimeDir, input.RuntimeSource))
	report.Checks = append(report.Checks, s.BrokerSocket(input.SocketPath)...)
	if input.Mode != ModeUp {
		report.Checks = append(report.Checks, s.Repository(input.RepoPath))
	}
	report.Checks = append(report.Checks, s.Configuration(input.IdentitiesPath, input.PolicyPath)...)
	report.Checks = append(report.Checks, s.Binaries(input.Mode == ModeUp)...)
	if input.Mode == ModeContainer || input.Mode == ModeUp {
		report.Checks = append(report.Checks, s.Workspace(input.Workspace))
		report.Checks = append(report.Checks, s.ContainerRuntime(input.ContainerRuntime))
	}
	Classify(report.Checks)
	report.Ready = !HasFailure(report.Checks)
	report.Outcome = Outcome(report.Checks)
	return report
}

func (s Service) RuntimeDir(path, source string) Check {
	info, err := s.deps.Stat(path)
	if err != nil {
		return Check{Name: "runtime-dir", Status: StatusFail, Details: fmt.Sprintf("runtime directory %s is not accessible: %v", path, err), Remediation: "Set XDG_RUNTIME_DIR to your user runtime directory and ensure it exists before launching ai-agent."}
	}
	if !info.IsDir() {
		return Check{Name: "runtime-dir", Status: StatusFail, Details: fmt.Sprintf("runtime path %s exists but is not a directory", path), Remediation: "Point XDG_RUNTIME_DIR at a directory such as /run/user/<uid>."}
	}
	return Check{Name: "runtime-dir", Status: StatusPass, Details: fmt.Sprintf("using %s from %s", path, source)}
}

func (s Service) BrokerSocket(path string) []Check {
	info, err := s.deps.Lstat(path)
	if err != nil {
		return []Check{{Name: "broker-socket", Status: StatusFail, Details: fmt.Sprintf("expected broker socket at %s: %v", path, err), Remediation: fmt.Sprintf("Start the broker socket with `systemctl --user start ai-agent-broker.socket` or pass --broker-sock with the correct path. Expected path: %s", path)}, {Name: "broker-reachability", Status: StatusSkip, Details: "skipped because the broker socket is missing", Remediation: fmt.Sprintf("Create the socket at %s before retrying.", path)}}
	}
	if info.Mode()&os.ModeSocket == 0 {
		return []Check{{Name: "broker-socket", Status: StatusFail, Details: fmt.Sprintf("%s exists but is not a Unix domain socket", path), Remediation: fmt.Sprintf("Remove the unexpected file at %s and restart the broker socket unit.", path)}, {Name: "broker-reachability", Status: StatusSkip, Details: "skipped because the broker socket path is the wrong file type", Remediation: fmt.Sprintf("Fix %s so it is a live Unix socket before retrying.", path)}}
	}
	socket := Check{Name: "broker-socket", Status: StatusPass, Details: fmt.Sprintf("found live socket path at %s", path)}
	reachability := Check{Name: "broker-reachability", Status: StatusPass, Details: fmt.Sprintf("broker responded on %s", path)}
	if err := s.deps.CheckBroker(path); err != nil {
		reachability.Status = StatusFail
		reachability.Details = fmt.Sprintf("broker health check failed for %s: %v", path, err)
		reachability.Remediation = "Ensure ai-agent-broker is running and that your user can connect to the broker socket."
	}
	return []Check{socket, reachability}
}

func (s Service) Repository(repoPath string) Check {
	candidate := repoPath
	if candidate == "" {
		cwd, err := s.deps.WorkingDir()
		if err != nil {
			return Check{Name: "repo-remote", Status: StatusSkip, Details: fmt.Sprintf("could not determine working directory: %v", err), Remediation: "Run from a repository checkout or pass --repo to validate a specific repository."}
		}
		candidate = cwd
	}
	absPath, slug, isSSH, err := s.deps.ResolveRepo(candidate)
	if err != nil {
		if repoPath != "" {
			return Check{Name: "repo-remote", Status: StatusFail, Details: err.Error(), Remediation: "Use --repo with a git checkout that has an HTTPS origin remote."}
		}
		return Check{Name: "repo-remote", Status: StatusSkip, Details: "not currently in a git repository; skipping repo-specific HTTPS validation", Remediation: "Run `ai-agent doctor --repo /path/to/repo` to validate a specific checkout."}
	}
	if isSSH {
		return Check{Name: "repo-remote", Status: StatusFail, Details: fmt.Sprintf("repository %s resolves to %s via SSH", absPath, slug), Remediation: fmt.Sprintf("Switch the origin remote to HTTPS: `git -C %s remote set-url origin https://github.com/%s.git`", absPath, slug)}
	}
	return Check{Name: "repo-remote", Status: StatusPass, Details: fmt.Sprintf("validated HTTPS origin for %s (%s)", absPath, slug)}
}

func (s Service) Binaries(up bool) []Check {
	checks := []Check{s.currentExecutable(), s.resolvedBinary("ai-agent-credential-helper"), s.resolvedBinary("ai-agent-gh"), s.pathBinary("git")}
	if !up {
		checks = append(checks, s.pathBinary("gh"))
	}
	return checks
}

func (s Service) Configuration(identitiesPath, policyPath string) []Check {
	configuration, err := s.deps.LoadConfiguration(identitiesPath, policyPath)
	if err != nil {
		return []Check{{Name: "broker-configuration-recovery", Status: StatusFail, Details: err.Error(), Remediation: "Restore owner-only access to the configuration directory and rerun doctor."}}
	}
	identities, identityCheck := validateIdentities(identitiesPath, configuration.Identities, configuration.IdentitiesError)
	policyFile, policyCheck := validatePolicy(policyPath, configuration.Policy, configuration.PolicyError)
	checks := []Check{identityCheck, policyCheck}
	if identities != nil {
		checks = append(checks, s.IdentityKeys(*identities)...)
	}
	if identities != nil && policyFile != nil {
		checks = append(checks, s.PolicyProviders(identities, policyFile, policyPath), checkProviderReadinessFields(*identities, *policyFile, policyPath))
	}
	return checks
}

func validateIdentities(path string, identities *identity.IdentitiesFile, err error) (*identity.IdentitiesFile, Check) {
	if err != nil {
		return nil, Check{Name: "broker-identities", Status: StatusFail, Details: fmt.Sprintf("failed to load identities file %s: %v", path, err), Remediation: fmt.Sprintf("Create or fix %s before starting brokered sessions.", path)}
	}
	if errors := identity.Validate(identities); errors.HasErrors() {
		return nil, Check{Name: "broker-identities", Status: StatusFail, Details: fmt.Sprintf("identities file %s is invalid: %s", path, errors.Error()), Remediation: fmt.Sprintf("Fix the identities file at %s so every agent has an app_id, git name, and git email.", path)}
	}
	return identities, Check{Name: "broker-identities", Status: StatusPass, Details: fmt.Sprintf("validated identities file %s for %d agent(s)", path, len(identities.Agents))}
}

func validatePolicy(path string, policyFile *policy.PolicyFile, err error) (*policy.PolicyFile, Check) {
	if err != nil {
		return nil, Check{Name: "broker-policy", Status: StatusFail, Details: fmt.Sprintf("failed to load policy file %s: %v", path, err), Remediation: fmt.Sprintf("Restore an owner-only regular policy file at %s before retrying.", path)}
	}
	result := policy.Validate(policyFile)
	if result.Errors.HasErrors() {
		return nil, Check{Name: "broker-policy", Status: StatusFail, Details: fmt.Sprintf("policy file %s is invalid: %s", path, result.Errors.Error()), Remediation: fmt.Sprintf("Run `ai-agent policy validate --policy %s` and fix the reported errors.", path)}
	}
	details := fmt.Sprintf("validated policy file %s for %d agent(s)", path, len(policyFile.Agents))
	if len(result.Warnings) > 0 {
		details = fmt.Sprintf("%s with %d warning(s)", details, len(result.Warnings))
	}
	return policyFile, Check{Name: "broker-policy", Status: StatusPass, Details: details}
}

func (s Service) IdentityKeys(identities identity.IdentitiesFile) []Check {
	missing := make([]string, 0)
	rejected := make([]string, 0)
	stale := make([]string, 0)
	for _, name := range sortedNames(identities.Agents) {
		keyPath := s.deps.ExpandPath(identities.Agents[name].AppKey)
		if keyPath == "" {
			missing = append(missing, name)
			continue
		}
		info, err := securefile.ValidateOwnerOnly(keyPath, maxBrokerPEMBytes)
		if err != nil {
			rejected = append(rejected, fmt.Sprintf("%s=%s (%v)", name, keyPath, err))
			continue
		}
		if age := s.now().Sub(info.ModTime()); age >= pemRotationReminderAge {
			stale = append(stale, fmt.Sprintf("%s=%s (%d days)", name, keyPath, int(age.Hours()/24)))
		}
	}
	total := len(identities.Agents)
	return []Check{pemFilesCheck(total, missing, rejected), pemRotationCheck(total, stale)}
}

func pemFilesCheck(total int, missing, rejected []string) Check {
	if len(missing) == 0 && len(rejected) == 0 {
		return Check{Name: "broker-pem-files", Status: StatusPass, Details: fmt.Sprintf("validated broker-loadable PEM keys for %d agent(s)", total)}
	}
	details := ""
	if len(missing) > 0 {
		details = "missing app_key for " + strings.Join(missing, ", ")
	}
	if len(rejected) > 0 {
		if details != "" {
			details += "; "
		}
		details += "keys the broker will reject: " + strings.Join(rejected, ", ")
	}
	evidence := map[string]string{}
	if len(missing) > 0 {
		evidence["missing"] = strings.Join(missing, ",")
	}
	if len(rejected) > 0 {
		evidence["rejected"] = strings.Join(rejected, ",")
	}
	return Check{Name: "broker-pem-files", Status: StatusFail, Details: details, Remediation: "Point each agent app_key at an owner-only (chmod 600), non-symlink PEM file you own; the broker loads keys with these exact rules and refuses anything else.", Evidence: evidence}
}

func pemRotationCheck(total int, stale []string) Check {
	days := int(pemRotationReminderAge.Hours() / 24)
	if len(stale) == 0 {
		return Check{Name: "broker-pem-rotation", Status: StatusPass, Details: fmt.Sprintf("PEM keys are within the %d-day rotation reminder for %d agent(s)", days, total)}
	}
	return Check{Name: "broker-pem-rotation", Status: StatusWarn, Details: "PEM keys past the rotation reminder age: " + strings.Join(stale, ", "), Remediation: fmt.Sprintf("Rotate GitHub App private keys older than %d days and update app_key.", days), Evidence: map[string]string{"reminder_age_days": fmt.Sprintf("%d", days), "stale": strings.Join(stale, ",")}}
}

func (s Service) now() time.Time {
	if s.deps.Now != nil {
		return s.deps.Now()
	}
	return time.Now()
}

func (s Service) PolicyProviders(identities *identity.IdentitiesFile, policyFile *policy.PolicyFile, policyPath string) Check {
	if err := s.deps.ValidatePolicy(policyFile, identities); err != nil {
		return Check{Name: "broker-policy-providers", Status: StatusFail, Details: fmt.Sprintf("policy file %s failed provider validation: %v", policyPath, err), Remediation: fmt.Sprintf("Run `ai-agent policy validate --policy %s` and fix the reported errors.", policyPath)}
	}
	return Check{Name: "broker-policy-providers", Status: StatusPass, Details: fmt.Sprintf("provider configs in %s parse for all registered providers", policyPath)}
}

func checkProviderReadinessFields(identities identity.IdentitiesFile, policyFile policy.PolicyFile, policyPath string) Check {
	requirements := capabilities.ReadinessFieldRequirements()
	missing := make([]string, 0)
	for _, name := range sortedNames(identities.Agents) {
		agentPolicy, ok := policyFile.Agents[name]
		if !ok {
			for provider, fields := range requirements {
				for _, field := range fields {
					missing = append(missing, name+" providers."+provider+"."+field)
				}
			}
			continue
		}
		for provider, fields := range requirements {
			for _, field := range fields {
				if !hasProviderField(agentPolicy, provider, field) {
					missing = append(missing, name+" providers."+provider+"."+field)
				}
			}
		}
	}
	if len(missing) > 0 {
		return Check{Name: "broker-provider-fields", Status: StatusFail, Details: "missing provider readiness fields: " + strings.Join(missing, ", "), Remediation: fmt.Sprintf("Set required provider fields in %s before starting brokered sessions.", policyPath)}
	}
	return Check{Name: "broker-provider-fields", Status: StatusPass, Details: fmt.Sprintf("validated provider readiness fields for %d agent(s)", len(identities.Agents))}
}

func (s Service) Workspace(path string) Check {
	if path == "" {
		return Check{Name: "container-workspace", Status: StatusFail, Details: paths.EnvWorkspace + " is not set", Remediation: "Export " + paths.EnvWorkspace + " to the host directory that should be mounted into /workspace before launching the devcontainer."}
	}
	info, err := s.deps.Stat(path)
	if err != nil {
		return Check{Name: "container-workspace", Status: StatusFail, Details: fmt.Sprintf("%s=%s is not accessible: %v", paths.EnvWorkspace, path, err), Remediation: "Point " + paths.EnvWorkspace + " at an existing host directory before launching the devcontainer."}
	}
	if !info.IsDir() {
		return Check{Name: "container-workspace", Status: StatusFail, Details: fmt.Sprintf("%s=%s is not a directory", paths.EnvWorkspace, path), Remediation: "Point " + paths.EnvWorkspace + " at a directory that can be bind-mounted into the container."}
	}
	return Check{Name: "container-workspace", Status: StatusPass, Details: fmt.Sprintf("workspace source %s is ready to mount", path)}
}

func (s Service) ContainerRuntime(runtime string) Check {
	found := make([]string, 0)
	missing := make([]string, 0)
	if path, err := s.deps.FindBinary(runtime); err == nil {
		found = append(found, fmt.Sprintf("%s=%s", runtime, path))
	} else {
		missing = append(missing, runtime)
	}
	alternate := alternateRuntime(runtime)
	if alternate != "" {
		if path, err := s.deps.FindBinary(alternate); err == nil {
			found = append(found, fmt.Sprintf("%s=%s", alternate, path))
		}
	}
	if path, err := s.deps.FindBinary("devcontainer"); err == nil {
		found = append(found, fmt.Sprintf("devcontainer=%s", path))
	} else {
		missing = append(missing, "devcontainer")
	}
	if len(missing) > 0 {
		remediation := fmt.Sprintf("Install %s and the devcontainer CLI before using the container readiness flow.", runtime)
		if len(missing) == 1 && missing[0] == runtime {
			remediation = fmt.Sprintf("Install %s before using the container readiness flow.", runtime)
		}
		if len(missing) == 1 && missing[0] == "devcontainer" {
			remediation = "Install the devcontainer CLI before using the container readiness flow."
		}
		if alternate != "" {
			remediation += fmt.Sprintf(" To opt out explicitly, rerun with --runtime %s.", alternate)
		}
		return Check{Name: "container-runtime", Status: StatusFail, Details: fmt.Sprintf("selected runtime %s is not ready; found: %s; missing: %s", runtime, strings.Join(found, ", "), strings.Join(missing, ", ")), Remediation: remediation}
	}
	return Check{Name: "container-runtime", Status: StatusPass, Details: fmt.Sprintf("selected runtime %s is ready: %s", runtime, strings.Join(found, ", "))}
}

func (s Service) currentExecutable() Check {
	path, err := s.deps.Executable()
	if err != nil {
		return Check{Name: "binary-ai-agent", Status: StatusFail, Details: fmt.Sprintf("could not resolve ai-agent executable: %v", err), Remediation: "Build or install ai-agent before running the doctor command."}
	}
	if _, err := s.deps.Stat(path); err != nil {
		return Check{Name: "binary-ai-agent", Status: StatusFail, Details: fmt.Sprintf("ai-agent executable %s is not accessible: %v", path, err), Remediation: "Reinstall ai-agent or fix the binary path."}
	}
	return Check{Name: "binary-ai-agent", Status: StatusPass, Details: fmt.Sprintf("found ai-agent at %s", path)}
}

func (s Service) resolvedBinary(name string) Check {
	path, err := s.resolveBinary(name)
	if err != nil {
		return Check{Name: "binary-" + name, Status: StatusFail, Details: err.Error(), Remediation: fmt.Sprintf("Build or install %s next to ai-agent or add it to PATH.", name)}
	}
	return Check{Name: "binary-" + name, Status: StatusPass, Details: fmt.Sprintf("found %s at %s", name, path)}
}

func (s Service) resolveBinary(name string) (string, error) {
	if executable, err := s.deps.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), name)
		if info, statErr := s.deps.Stat(candidate); statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	if path, err := s.deps.FindBinary(name); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("%s not found", name)
}

func (s Service) pathBinary(name string) Check {
	path, err := s.deps.FindBinary(name)
	if err != nil {
		return Check{Name: "binary-" + name, Status: StatusFail, Details: fmt.Sprintf("%s not found in PATH", name), Remediation: fmt.Sprintf("Install %s or add it to PATH before launching managed sessions.", name)}
	}
	return Check{Name: "binary-" + name, Status: StatusPass, Details: fmt.Sprintf("found %s at %s", name, path)}
}

func HasFailure(checks []Check) bool {
	for _, check := range checks {
		if check.Status == StatusFail {
			return true
		}
	}
	return false
}

func HasWarning(checks []Check) bool {
	for _, check := range checks {
		if check.Status == StatusWarn {
			return true
		}
	}
	return false
}

func Outcome(checks []Check) Status {
	outcome := StatusPass
	for _, check := range checks {
		switch check.Status {
		case StatusFail:
			return StatusFail
		case StatusWarn:
			outcome = StatusWarn
		}
	}
	return outcome
}

func hasProviderField(agent policy.AgentPolicy, provider string, field string) bool {
	section, ok := agent.Providers[provider]
	if !ok || len(section) == 0 || string(section) == "null" {
		return false
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(section, &values); err != nil {
		return false
	}
	raw, ok := values[field]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var number int64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number > 0
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text) != ""
	}
	return true
}

func alternateRuntime(runtime string) string {
	switch runtime {
	case "podman":
		return "docker"
	case "docker":
		return "podman"
	default:
		return ""
	}
}

func sortedNames[T any](values map[string]T) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
