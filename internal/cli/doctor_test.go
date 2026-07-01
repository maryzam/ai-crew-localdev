package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/app/readiness"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func TestDoctorCommandRendersReadyReport(t *testing.T) {
	directory := t.TempDir()
	runtimeDir := filepath.Join(directory, "runtime")
	socketPath := filepath.Join(runtimeDir, "broker.sock")
	executable := filepath.Join(directory, "ai-agent")
	mustMkdirAll(t, runtimeDir)
	mustWriteExecutable(t, directory, "ai-agent")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	service := doctorTestService(t, executable, nil, true)
	command := newDoctorCommand(service)
	command.SetArgs([]string{"--broker-sock", socketPath})
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Execute(); err != nil {
		t.Fatalf("doctor command: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "ai-agent doctor (host)\n") || !strings.HasSuffix(output.String(), "ready: all checks passed\n") {
		t.Fatalf("unexpected output:\n%s", output.String())
	}
}

func TestDoctorCommandRendersJSONFailure(t *testing.T) {
	directory := t.TempDir()
	runtimeDir := filepath.Join(directory, "missing")
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	service := doctorTestService(t, filepath.Join(directory, "ai-agent"), nil, false)
	command := newDoctorCommand(service)
	command.SetArgs([]string{"--broker-sock", filepath.Join(directory, "missing.sock"), "--json"})
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Execute(); err == nil || err.Error() != "readiness checks failed" {
		t.Fatalf("error = %v", err)
	}
	var report readiness.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output.String())
	}
	if report.Ready || report.Mode != readiness.ModeHost {
		t.Fatalf("report = %#v", report)
	}
}

func TestDoctorCommandRejectsInvalidSocketBeforeOutput(t *testing.T) {
	command := newDoctorCommand(doctorTestService(t, "/unused", nil, false))
	command.SetArgs([]string{"--broker-sock", "relative.sock"})
	var output bytes.Buffer
	command.SetOut(&output)
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q", output.String())
	}
}

func TestWriteDoctorTextContract(t *testing.T) {
	report := readiness.Report{Mode: readiness.ModeHost, Ready: false, Checks: []readiness.Check{{Name: "broker-socket", Status: readiness.StatusFail, Details: "missing", Remediation: "start broker"}, {Name: "repo-remote", Status: readiness.StatusSkip, Details: "not in repo"}}}
	var output bytes.Buffer
	writeDoctorText(&output, report)
	want := "ai-agent doctor (host)\n[fail] broker-socket: missing\n  fix: start broker\n[skip] repo-remote: not in repo\nnot ready: fix the failing checks above\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestWriteDoctorJSONContract(t *testing.T) {
	report := readiness.Report{Mode: readiness.ModeHost, Ready: false, RuntimeDir: "/run/user/1", SocketPath: "/run/user/1/ai-agent/broker.sock", Checks: []readiness.Check{{Name: "broker-socket", Status: readiness.StatusFail, Details: "missing", Remediation: "start broker"}}}
	var output bytes.Buffer
	if err := writeDoctorJSON(&output, report); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"mode\": \"host\",\n  \"ready\": false,\n  \"runtime_dir\": \"/run/user/1\",\n  \"socket_path\": \"/run/user/1/ai-agent/broker.sock\",\n  \"checks\": [\n    {\n      \"name\": \"broker-socket\",\n      \"status\": \"fail\",\n      \"details\": \"missing\",\n      \"remediation\": \"start broker\"\n    }\n  ]\n}\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func doctorTestService(t *testing.T, executable string, healthError error, socketExists bool) readiness.Service {
	t.Helper()
	pemPath := filepath.Join(t.TempDir(), "agent.pem")
	if err := os.WriteFile(pemPath, []byte("pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	identities := &identity.IdentitiesFile{SchemaVersion: schema.IdentitiesSchemaV2, Agents: map[string]identity.AgentIdentity{"agent": {AppID: "1", AppKey: pemPath, GitName: "agent", GitEmail: "agent@example.test"}}}
	section := json.RawMessage(`{"installation_id":1}`)
	policyFile := &policy.PolicyFile{SchemaVersion: schema.PolicySchemaCurrent, DefaultSessionTTL: "8h", DefaultIdleTimeout: "1h", Agents: map[string]policy.AgentPolicy{"agent": {Resources: []string{"github:repo:owner/repo"}, Providers: map[string]json.RawMessage{"github": section}}}}
	ports := &doctorTestPorts{executable: executable, healthError: healthError, identities: identities, policyFile: policyFile, socketExists: socketExists}
	return readiness.New(readiness.Dependencies{Stat: ports.Stat, Lstat: ports.Lstat, CanOpen: ports.CanOpen, WorkingDir: ports.WorkingDir, Executable: ports.Executable, ExpandPath: ports.ExpandPath, FindBinary: ports.Find, CheckBroker: ports.Check, ResolveRepo: ports.Resolve, LoadConfiguration: ports.LoadConfiguration, ValidatePolicy: ports.Validate})
}

type doctorTestPorts struct {
	executable   string
	healthError  error
	identities   *identity.IdentitiesFile
	policyFile   *policy.PolicyFile
	socketExists bool
}

func (*doctorTestPorts) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }
func (p *doctorTestPorts) Lstat(path string) (os.FileInfo, error) {
	if p.socketExists {
		return doctorSocketInfo{name: filepath.Base(path)}, nil
	}
	return os.Lstat(path)
}
func (*doctorTestPorts) CanOpen(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	return file.Close()
}
func (*doctorTestPorts) WorkingDir() (string, error)   { return "/repo", nil }
func (p *doctorTestPorts) Executable() (string, error) { return p.executable, nil }
func (*doctorTestPorts) ExpandPath(path string) string { return path }
func (*doctorTestPorts) Find(name string) (string, error) {
	return "/usr/bin/" + name, nil
}
func (p *doctorTestPorts) Check(string) error { return p.healthError }
func (*doctorTestPorts) Resolve(string) (string, string, bool, error) {
	return "/repo", "owner/repo", false, nil
}
func (p *doctorTestPorts) LoadConfiguration(string, string) (readiness.Configuration, error) {
	return readiness.Configuration{Identities: p.identities, Policy: p.policyFile}, nil
}
func (*doctorTestPorts) Validate(*policy.PolicyFile, *identity.IdentitiesFile) error { return nil }

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func mustWriteDoctorConfig(t *testing.T, dir string, withInstallationID bool) string {
	t.Helper()
	configDir := filepath.Join(dir, "config")
	mustMkdirAll(t, configDir)
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	pemPath := filepath.Join(dir, "claude.pem")
	if err := os.WriteFile(pemPath, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	identitiesJSON := fmt.Sprintf(`{"schema_version":"ai-agent-identities/v2","agents":{"claude":{"git_name":"claude[bot]","git_email":"claude@example.com","github_host":"github.com","app_id":"12345","app_key":%q}}}`, pemPath)
	if err := os.WriteFile(paths.DefaultIdentitiesPath(), []byte(identitiesJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	installationID := 0
	if withInstallationID {
		installationID = 42
	}
	policyJSON := fmt.Sprintf(`{"schema_version":"2","default_session_ttl":"8h","default_idle_timeout":"1h","agents":{"claude":{"resources":["github:repo:owner/repo"],"providers":{"github":{"installation_id":%d,"default_permissions":{"contents":"write","metadata":"read"}}}}}}`, installationID)
	if err := os.WriteFile(paths.DefaultPolicyPath(), []byte(policyJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	return pemPath
}

type doctorSocketInfo struct {
	name string
}

func (info doctorSocketInfo) Name() string  { return info.name }
func (doctorSocketInfo) Size() int64        { return 0 }
func (doctorSocketInfo) Mode() os.FileMode  { return os.ModeSocket | 0o600 }
func (doctorSocketInfo) ModTime() time.Time { return time.Time{} }
func (doctorSocketInfo) IsDir() bool        { return false }
func (doctorSocketInfo) Sys() any           { return nil }
