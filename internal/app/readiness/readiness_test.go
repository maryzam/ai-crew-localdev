package readiness

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/governance"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
)

func TestRunSelectsChecksByMode(t *testing.T) {
	directory := t.TempDir()
	runtimeDir := filepath.Join(directory, "runtime")
	workspace := filepath.Join(directory, "workspace")
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(runtimeDir, "broker.sock")
	ports := readyPorts(t, directory)
	ports.sockets[socketPath] = true
	service := mustService(t, ports)
	report := service.Run(Input{Mode: ModeUp, RuntimeDir: runtimeDir, RuntimeSource: "XDG_RUNTIME_DIR", SocketPath: socketPath, Workspace: workspace, IdentitiesPath: "/identities", PolicyPath: "/policy", ContainerRuntime: "podman"})
	if !report.Ready {
		t.Fatalf("report = %#v", report)
	}
	if hasCheck(report.Checks, "repo-remote") || hasCheck(report.Checks, "binary-gh") {
		t.Fatalf("up report contains host-only checks: %#v", report.Checks)
	}
	if !hasCheck(report.Checks, "container-workspace") || !hasCheck(report.Checks, "container-runtime") {
		t.Fatalf("up report omits container checks: %#v", report.Checks)
	}
}

func TestBrokerSocketReportsMissingAndUnhealthy(t *testing.T) {
	directory := t.TempDir()
	ports := readyPorts(t, directory)
	service := mustService(t, ports)
	missing := service.BrokerSocket(filepath.Join(directory, "missing.sock"))
	if missing[0].Status != StatusFail || missing[1].Status != StatusSkip {
		t.Fatalf("missing checks = %#v", missing)
	}
	socketPath := filepath.Join(directory, "broker.sock")
	ports.sockets[socketPath] = true
	ports.healthError = errors.New("audit unavailable")
	unhealthy := mustService(t, ports).BrokerSocket(socketPath)
	if unhealthy[1].Status != StatusFail || unhealthy[1].Details != "broker health check failed for "+socketPath+": audit unavailable" {
		t.Fatalf("unhealthy checks = %#v", unhealthy)
	}
}

func TestConfigurationRejectsProviderAndInstallationFailures(t *testing.T) {
	directory := t.TempDir()
	ports := readyPorts(t, directory)
	ports.policyError = errors.New("malformed resource")
	ports.identities, ports.policyFile = validConfiguration(filepath.Join(directory, "agent.pem"), 0)
	checks := mustService(t, ports).Configuration("/identities", "/policy")
	if checkByName(t, checks, "broker-policy-providers").Status != StatusFail || checkByName(t, checks, "broker-provider-fields").Status != StatusFail {
		t.Fatalf("checks = %#v", checks)
	}
}

func TestIdentityKeysReportsUnreadablePEM(t *testing.T) {
	directory := t.TempDir()
	pemPath := filepath.Join(directory, "agent.pem")
	if err := os.WriteFile(pemPath, []byte("pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	ports := readyPorts(t, directory)
	ports.openError = errors.New("permission denied")
	identities, _ := validConfiguration(pemPath, 1)
	check := mustService(t, ports).IdentityKeys(*identities)[0]
	if check.Status != StatusFail || check.Name != "broker-pem-files" {
		t.Fatalf("check = %#v", check)
	}
}

type testPorts struct {
	directory   string
	executable  string
	identities  *identity.IdentitiesFile
	policyFile  *policy.PolicyFile
	healthError error
	policyError error
	openError   error
	sockets     map[string]bool
}

func (p *testPorts) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (p *testPorts) Lstat(path string) (os.FileInfo, error) {
	if p.sockets[path] {
		return socketInfo{name: filepath.Base(path)}, nil
	}
	return os.Lstat(path)
}
func (p *testPorts) CanOpen(string) error             { return p.openError }
func (p *testPorts) WorkingDir() (string, error)      { return p.directory, nil }
func (p *testPorts) Executable() (string, error)      { return p.executable, nil }
func (p *testPorts) ExpandPath(path string) string    { return path }
func (p *testPorts) Find(name string) (string, error) { return "/usr/bin/" + name, nil }
func (p *testPorts) Check(string) error               { return p.healthError }
func (p *testPorts) Resolve(string) (string, string, bool, error) {
	return p.directory, "owner/repo", false, nil
}
func (p *testPorts) LoadConfiguration(string, string) (governance.Snapshot, error) {
	return governance.Snapshot{Identities: p.identities, Policy: p.policyFile}, nil
}
func (p *testPorts) Validate(*policy.PolicyFile, *identity.IdentitiesFile) error {
	return p.policyError
}

func readyPorts(t *testing.T, directory string) *testPorts {
	t.Helper()
	executable := filepath.Join(directory, "ai-agent")
	for _, name := range []string{"ai-agent", "ai-agent-credential-helper", "ai-agent-gh"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("binary"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pemPath := filepath.Join(directory, "agent.pem")
	if err := os.WriteFile(pemPath, []byte("pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	identities, policyFile := validConfiguration(pemPath, 1)
	return &testPorts{directory: directory, executable: executable, identities: identities, policyFile: policyFile, sockets: make(map[string]bool)}
}

func mustService(t *testing.T, ports *testPorts) Service {
	t.Helper()
	return New(Dependencies{Stat: ports.Stat, Lstat: ports.Lstat, CanOpen: ports.CanOpen, WorkingDir: ports.WorkingDir, Executable: ports.Executable, ExpandPath: ports.ExpandPath, FindBinary: ports.Find, CheckBroker: ports.Check, ResolveRepo: ports.Resolve, LoadConfiguration: ports.LoadConfiguration, ValidatePolicy: ports.Validate})
}

func validConfiguration(pemPath string, installationID int64) (*identity.IdentitiesFile, *policy.PolicyFile) {
	identities := &identity.IdentitiesFile{SchemaVersion: schema.IdentitiesSchemaV2, Agents: map[string]identity.AgentIdentity{"agent": {AppID: "1", AppKey: pemPath, GitName: "agent", GitEmail: "agent@example.test"}}}
	section, _ := json.Marshal(map[string]any{"installation_id": installationID})
	policyFile := &policy.PolicyFile{SchemaVersion: schema.PolicySchemaCurrent, DefaultSessionTTL: "8h", DefaultIdleTimeout: "1h", Agents: map[string]policy.AgentPolicy{"agent": {Resources: []string{"github:repo:owner/repo"}, Providers: map[string]json.RawMessage{"github": section}}}}
	return identities, policyFile
}

func hasCheck(checks []Check, name string) bool {
	for _, check := range checks {
		if check.Name == name {
			return true
		}
	}
	return false
}

func checkByName(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("missing check %s", name)
	return Check{}
}

type socketInfo struct {
	name string
}

func (info socketInfo) Name() string  { return info.name }
func (socketInfo) Size() int64        { return 0 }
func (socketInfo) Mode() os.FileMode  { return os.ModeSocket | 0o600 }
func (socketInfo) ModTime() time.Time { return time.Time{} }
func (socketInfo) IsDir() bool        { return false }
func (socketInfo) Sys() any           { return nil }
