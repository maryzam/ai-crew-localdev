package readiness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
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
	StatusSkip Status = "skip"
)

type Check struct {
	Name        string `json:"name"`
	Status      Status `json:"status"`
	Details     string `json:"details"`
	Remediation string `json:"remediation,omitempty"`
	Blocking    bool   `json:"blocking"`
}

type Report struct {
	Mode       Mode    `json:"mode"`
	Ready      bool    `json:"ready"`
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

type Configuration struct {
	Identities      *identity.IdentitiesFile
	Policy          *policy.PolicyFile
	IdentitiesError error
	PolicyError     error
}

type HostProbe interface {
	Stat(string) (os.FileInfo, error)
	Lstat(string) (os.FileInfo, error)
	CanOpen(string) error
	WorkingDir() (string, error)
	Executable() (string, error)
	ExpandPath(string) string
}

type BinaryResolver interface {
	Find(string) (string, error)
}

type BrokerHealth interface {
	Check(string) error
}

type RepoResolver interface {
	Resolve(string) (string, string, bool, error)
}

type GovernanceInspector interface {
	Inspect(string, string) (Configuration, error)
}

type PolicyValidator interface {
	Validate(*policy.PolicyFile, *identity.IdentitiesFile) error
}

type Ports struct {
	Host       HostProbe
	Binaries   BinaryResolver
	Broker     BrokerHealth
	Repository RepoResolver
	Governance GovernanceInspector
	Policy     PolicyValidator
}

type Service struct {
	ports Ports
}

func New(ports Ports) (Service, error) {
	if ports.Host == nil {
		return Service{}, fmt.Errorf("readiness host probe is required")
	}
	if ports.Binaries == nil {
		return Service{}, fmt.Errorf("readiness binary resolver is required")
	}
	if ports.Broker == nil {
		return Service{}, fmt.Errorf("readiness broker health is required")
	}
	if ports.Repository == nil {
		return Service{}, fmt.Errorf("readiness repository resolver is required")
	}
	if ports.Governance == nil {
		return Service{}, fmt.Errorf("readiness governance inspector is required")
	}
	if ports.Policy == nil {
		return Service{}, fmt.Errorf("readiness policy validator is required")
	}
	return Service{ports: ports}, nil
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
	report.Ready = !HasBlockingFailure(report.Checks)
	return report
}

func (s Service) RuntimeDir(path, source string) Check {
	info, err := s.ports.Host.Stat(path)
	if err != nil {
		return Check{Name: "runtime-dir", Status: StatusFail, Details: fmt.Sprintf("runtime directory %s is not accessible: %v", path, err), Remediation: "Set XDG_RUNTIME_DIR to your user runtime directory and ensure it exists before launching ai-agent.", Blocking: true}
	}
	if !info.IsDir() {
		return Check{Name: "runtime-dir", Status: StatusFail, Details: fmt.Sprintf("runtime path %s exists but is not a directory", path), Remediation: "Point XDG_RUNTIME_DIR at a directory such as /run/user/<uid>.", Blocking: true}
	}
	return Check{Name: "runtime-dir", Status: StatusPass, Details: fmt.Sprintf("using %s from %s", path, source), Blocking: true}
}

func (s Service) BrokerSocket(path string) []Check {
	info, err := s.ports.Host.Lstat(path)
	if err != nil {
		return []Check{{Name: "broker-socket", Status: StatusFail, Details: fmt.Sprintf("expected broker socket at %s: %v", path, err), Remediation: fmt.Sprintf("Start the broker socket with `systemctl --user start ai-agent-broker.socket` or pass --broker-sock with the correct path. Expected path: %s", path), Blocking: true}, {Name: "broker-reachability", Status: StatusSkip, Details: "skipped because the broker socket is missing", Remediation: fmt.Sprintf("Create the socket at %s before retrying.", path), Blocking: true}}
	}
	if info.Mode()&os.ModeSocket == 0 {
		return []Check{{Name: "broker-socket", Status: StatusFail, Details: fmt.Sprintf("%s exists but is not a Unix domain socket", path), Remediation: fmt.Sprintf("Remove the unexpected file at %s and restart the broker socket unit.", path), Blocking: true}, {Name: "broker-reachability", Status: StatusSkip, Details: "skipped because the broker socket path is the wrong file type", Remediation: fmt.Sprintf("Fix %s so it is a live Unix socket before retrying.", path), Blocking: true}}
	}
	socket := Check{Name: "broker-socket", Status: StatusPass, Details: fmt.Sprintf("found live socket path at %s", path), Blocking: true}
	reachability := Check{Name: "broker-reachability", Status: StatusPass, Details: fmt.Sprintf("broker responded on %s", path), Blocking: true}
	if err := s.ports.Broker.Check(path); err != nil {
		reachability.Status = StatusFail
		reachability.Details = fmt.Sprintf("broker health check failed for %s: %v", path, err)
		reachability.Remediation = "Ensure ai-agent-broker is running and that your user can connect to the broker socket."
	}
	return []Check{socket, reachability}
}

func (s Service) Repository(repoPath string) Check {
	candidate := repoPath
	if candidate == "" {
		cwd, err := s.ports.Host.WorkingDir()
		if err != nil {
			return Check{Name: "repo-remote", Status: StatusSkip, Details: fmt.Sprintf("could not determine working directory: %v", err), Remediation: "Run from a repository checkout or pass --repo to validate a specific repository.", Blocking: false}
		}
		candidate = cwd
	}
	absPath, slug, isSSH, err := s.ports.Repository.Resolve(candidate)
	if err != nil {
		if repoPath != "" {
			return Check{Name: "repo-remote", Status: StatusFail, Details: err.Error(), Remediation: "Use --repo with a git checkout that has an HTTPS origin remote.", Blocking: true}
		}
		return Check{Name: "repo-remote", Status: StatusSkip, Details: "not currently in a git repository; skipping repo-specific HTTPS validation", Remediation: "Run `ai-agent doctor --repo /path/to/repo` to validate a specific checkout.", Blocking: false}
	}
	if isSSH {
		return Check{Name: "repo-remote", Status: StatusFail, Details: fmt.Sprintf("repository %s resolves to %s via SSH", absPath, slug), Remediation: fmt.Sprintf("Switch the origin remote to HTTPS: `git -C %s remote set-url origin https://github.com/%s.git`", absPath, slug), Blocking: true}
	}
	return Check{Name: "repo-remote", Status: StatusPass, Details: fmt.Sprintf("validated HTTPS origin for %s (%s)", absPath, slug), Blocking: true}
}

func (s Service) Binaries(up bool) []Check {
	checks := []Check{s.currentExecutable(), s.resolvedBinary("ai-agent-credential-helper"), s.resolvedBinary("ai-agent-gh"), s.pathBinary("git")}
	if !up {
		checks = append(checks, s.pathBinary("gh"))
	}
	return checks
}

func (s Service) Configuration(identitiesPath, policyPath string) []Check {
	snapshot, err := s.ports.Governance.Inspect(identitiesPath, policyPath)
	if err != nil {
		return []Check{{Name: "broker-configuration-recovery", Status: StatusFail, Details: err.Error(), Remediation: "Restore owner-only access to the configuration directory and rerun doctor.", Blocking: true}}
	}
	identities, identityCheck := Identities(identitiesPath, snapshot.Identities, snapshot.IdentitiesError)
	policyFile, policyCheck := Policy(policyPath, snapshot.Policy, snapshot.PolicyError)
	checks := []Check{identityCheck, policyCheck}
	if identities != nil {
		checks = append(checks, s.IdentityKeys(*identities)...)
	}
	if identities != nil && policyFile != nil {
		checks = append(checks, s.PolicyProviders(identities, policyFile, policyPath), InstallationIDs(*identities, *policyFile, policyPath))
	}
	return checks
}

func Identities(path string, identities *identity.IdentitiesFile, err error) (*identity.IdentitiesFile, Check) {
	if err != nil {
		return nil, Check{Name: "broker-identities", Status: StatusFail, Details: fmt.Sprintf("failed to load identities file %s: %v", path, err), Remediation: fmt.Sprintf("Create or fix %s before starting brokered sessions.", path), Blocking: true}
	}
	if errors := identity.Validate(identities); errors.HasErrors() {
		return nil, Check{Name: "broker-identities", Status: StatusFail, Details: fmt.Sprintf("identities file %s is invalid: %s", path, errors.Error()), Remediation: fmt.Sprintf("Fix the identities file at %s so every agent has an app_id, git name, and git email.", path), Blocking: true}
	}
	return identities, Check{Name: "broker-identities", Status: StatusPass, Details: fmt.Sprintf("validated identities file %s for %d agent(s)", path, len(identities.Agents)), Blocking: true}
}

func Policy(path string, policyFile *policy.PolicyFile, err error) (*policy.PolicyFile, Check) {
	if err != nil {
		return nil, Check{Name: "broker-policy", Status: StatusFail, Details: fmt.Sprintf("failed to load policy file %s: %v", path, err), Remediation: fmt.Sprintf("Restore an owner-only regular policy file at %s before retrying.", path), Blocking: true}
	}
	result := policy.Validate(policyFile)
	if result.Errors.HasErrors() {
		return nil, Check{Name: "broker-policy", Status: StatusFail, Details: fmt.Sprintf("policy file %s is invalid: %s", path, result.Errors.Error()), Remediation: fmt.Sprintf("Run `ai-agent policy validate --policy %s` and fix the reported errors.", path), Blocking: true}
	}
	details := fmt.Sprintf("validated policy file %s for %d agent(s)", path, len(policyFile.Agents))
	if len(result.Warnings) > 0 {
		details = fmt.Sprintf("%s with %d warning(s)", details, len(result.Warnings))
	}
	return policyFile, Check{Name: "broker-policy", Status: StatusPass, Details: details, Blocking: true}
}

func (s Service) IdentityKeys(identities identity.IdentitiesFile) []Check {
	missing := make([]string, 0)
	unreadable := make([]string, 0)
	for _, name := range sortedNames(identities.Agents) {
		keyPath := s.ports.Host.ExpandPath(identities.Agents[name].AppKey)
		if keyPath == "" {
			missing = append(missing, name)
			continue
		}
		info, err := s.ports.Host.Stat(keyPath)
		if err != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s=%s (%v)", name, keyPath, err))
			continue
		}
		if info.IsDir() {
			unreadable = append(unreadable, fmt.Sprintf("%s=%s (is a directory)", name, keyPath))
			continue
		}
		if err := s.ports.Host.CanOpen(keyPath); err != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s=%s (%v)", name, keyPath, err))
		}
	}
	if len(missing) > 0 || len(unreadable) > 0 {
		details := ""
		if len(missing) > 0 {
			details = "missing app_key for " + join(missing)
		}
		if len(unreadable) > 0 {
			if details != "" {
				details += "; "
			}
			details += "unreadable PEM paths: " + join(unreadable)
		}
		return []Check{{Name: "broker-pem-files", Status: StatusFail, Details: details, Remediation: "Set each agent app_key to a readable PEM file on the host before starting the broker.", Blocking: true}}
	}
	return []Check{{Name: "broker-pem-files", Status: StatusPass, Details: fmt.Sprintf("validated readable PEM paths for %d agent(s)", len(identities.Agents)), Blocking: true}}
}

func (s Service) PolicyProviders(identities *identity.IdentitiesFile, policyFile *policy.PolicyFile, policyPath string) Check {
	if err := s.ports.Policy.Validate(policyFile, identities); err != nil {
		return Check{Name: "broker-policy-providers", Status: StatusFail, Details: fmt.Sprintf("policy file %s failed provider validation: %v", policyPath, err), Remediation: fmt.Sprintf("Run `ai-agent policy validate --policy %s` and fix the reported errors.", policyPath), Blocking: true}
	}
	return Check{Name: "broker-policy-providers", Status: StatusPass, Details: fmt.Sprintf("provider configs in %s parse for all registered providers", policyPath), Blocking: true}
}

func InstallationIDs(identities identity.IdentitiesFile, policyFile policy.PolicyFile, policyPath string) Check {
	missing := make([]string, 0)
	for _, name := range sortedNames(identities.Agents) {
		agentPolicy, ok := policyFile.Agents[name]
		if !ok || !hasInstallationID(agentPolicy) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return Check{Name: "broker-installation-ids", Status: StatusFail, Details: "missing installation_id for " + join(missing), Remediation: fmt.Sprintf("Set installation_id for each configured agent in %s before starting brokered sessions.", policyPath), Blocking: true}
	}
	return Check{Name: "broker-installation-ids", Status: StatusPass, Details: fmt.Sprintf("validated installation IDs for %d agent(s)", len(identities.Agents)), Blocking: true}
}

func (s Service) Workspace(path string) Check {
	if path == "" {
		return Check{Name: "container-workspace", Status: StatusFail, Details: "AI_AGENT_WORKSPACE is not set", Remediation: "Export AI_AGENT_WORKSPACE to the host directory that should be mounted into /workspace before launching the devcontainer.", Blocking: true}
	}
	info, err := s.ports.Host.Stat(path)
	if err != nil {
		return Check{Name: "container-workspace", Status: StatusFail, Details: fmt.Sprintf("AI_AGENT_WORKSPACE=%s is not accessible: %v", path, err), Remediation: "Point AI_AGENT_WORKSPACE at an existing host directory before launching the devcontainer.", Blocking: true}
	}
	if !info.IsDir() {
		return Check{Name: "container-workspace", Status: StatusFail, Details: fmt.Sprintf("AI_AGENT_WORKSPACE=%s is not a directory", path), Remediation: "Point AI_AGENT_WORKSPACE at a directory that can be bind-mounted into the container.", Blocking: true}
	}
	return Check{Name: "container-workspace", Status: StatusPass, Details: fmt.Sprintf("workspace source %s is ready to mount", path), Blocking: true}
}

func (s Service) ContainerRuntime(runtime string) Check {
	found := make([]string, 0)
	missing := make([]string, 0)
	if path, err := s.ports.Binaries.Find(runtime); err == nil {
		found = append(found, fmt.Sprintf("%s=%s", runtime, path))
	} else {
		missing = append(missing, runtime)
	}
	alternate := alternateRuntime(runtime)
	if alternate != "" {
		if path, err := s.ports.Binaries.Find(alternate); err == nil {
			found = append(found, fmt.Sprintf("%s=%s", alternate, path))
		}
	}
	if path, err := s.ports.Binaries.Find("devcontainer"); err == nil {
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
		return Check{Name: "container-runtime", Status: StatusFail, Details: fmt.Sprintf("selected runtime %s is not ready; found: %s; missing: %s", runtime, join(found), join(missing)), Remediation: remediation, Blocking: true}
	}
	return Check{Name: "container-runtime", Status: StatusPass, Details: fmt.Sprintf("selected runtime %s is ready: %s", runtime, join(found)), Blocking: true}
}

func (s Service) currentExecutable() Check {
	path, err := s.ports.Host.Executable()
	if err != nil {
		return Check{Name: "binary-ai-agent", Status: StatusFail, Details: fmt.Sprintf("could not resolve ai-agent executable: %v", err), Remediation: "Build or install ai-agent before running the doctor command.", Blocking: true}
	}
	if _, err := s.ports.Host.Stat(path); err != nil {
		return Check{Name: "binary-ai-agent", Status: StatusFail, Details: fmt.Sprintf("ai-agent executable %s is not accessible: %v", path, err), Remediation: "Reinstall ai-agent or fix the binary path.", Blocking: true}
	}
	return Check{Name: "binary-ai-agent", Status: StatusPass, Details: fmt.Sprintf("found ai-agent at %s", path), Blocking: true}
}

func (s Service) resolvedBinary(name string) Check {
	path, err := s.resolveBinary(name)
	if err != nil {
		return Check{Name: "binary-" + name, Status: StatusFail, Details: err.Error(), Remediation: fmt.Sprintf("Build or install %s next to ai-agent or add it to PATH.", name), Blocking: true}
	}
	return Check{Name: "binary-" + name, Status: StatusPass, Details: fmt.Sprintf("found %s at %s", name, path), Blocking: true}
}

func (s Service) resolveBinary(name string) (string, error) {
	if executable, err := s.ports.Host.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), name)
		if info, statErr := s.ports.Host.Stat(candidate); statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	if path, err := s.ports.Binaries.Find(name); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("%s not found", name)
}

func (s Service) pathBinary(name string) Check {
	path, err := s.ports.Binaries.Find(name)
	if err != nil {
		return Check{Name: "binary-" + name, Status: StatusFail, Details: fmt.Sprintf("%s not found in PATH", name), Remediation: fmt.Sprintf("Install %s or add it to PATH before launching managed sessions.", name), Blocking: true}
	}
	return Check{Name: "binary-" + name, Status: StatusPass, Details: fmt.Sprintf("found %s at %s", name, path), Blocking: true}
}

func HasBlockingFailure(checks []Check) bool {
	for _, check := range checks {
		if check.Blocking && check.Status == StatusFail {
			return true
		}
	}
	return false
}

func hasInstallationID(agent policy.AgentPolicy) bool {
	section, ok := agent.Providers["github"]
	if !ok || len(section) == 0 || string(section) == "null" {
		return false
	}
	var value struct {
		InstallationID int64 `json:"installation_id"`
	}
	if err := json.Unmarshal(section, &value); err != nil {
		return false
	}
	return value.InstallationID > 0
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

func join(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		result := parts[0]
		for _, part := range parts[1:] {
			result += ", " + part
		}
		return result
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
