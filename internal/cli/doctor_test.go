package cli

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/spf13/cobra"
)

func TestRunDoctorHostReady(t *testing.T) {
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "r")
	binDir := filepath.Join(dir, "bin")
	sockPath := filepath.Join(runtimeDir, "ai-agent", "broker.sock")

	mustMkdirAll(t, filepath.Dir(sockPath))
	mustMkdirAll(t, binDir)
	ln := mustListenUnix(t, sockPath)
	defer func() { _ = ln.Close() }()

	agentBin := mustWriteExecutable(t, binDir, "ai-agent")
	mustWriteExecutable(t, binDir, "ai-agent-credential-helper")
	mustWriteExecutable(t, binDir, "ai-agent-gh")
	gitBin := mustWriteExecutable(t, binDir, "git")
	ghBin := mustWriteExecutable(t, binDir, "gh")
	mustWriteDoctorConfig(t, dir, true)

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	setDoctorTestHooks(t, doctorTestHooks{
		executable: func() (string, error) { return agentBin, nil },
		health:     func(path string) error { return nil },
		resolveRepo: func(repoPath string) (string, string, bool, error) {
			return "/workspace/repo", "owner/repo", false, nil
		},
		lookPath: func(name string) (string, error) {
			switch name {
			case "git":
				return gitBin, nil
			case "gh":
				return ghBin, nil
			default:
				return "", fmt.Errorf("unexpected lookup for %s", name)
			}
		},
		execLookPath: func(name string) (string, error) { return "", fmt.Errorf("%s not found", name) },
	})

	doctorModeFlag = string(doctorModeHost)
	doctorBrokerSock = ""
	doctorRepoPath = ""
	doctorRuntime = string(containerRuntimePodman)
	doctorJSON = false

	var out bytes.Buffer
	cmd := newDoctorTestCommand(&out)
	if err := runDoctor(cmd, nil); err != nil {
		t.Fatalf("runDoctor: %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "ready: all blocking checks passed") {
		t.Fatalf("expected ready output, got:\n%s", out.String())
	}
}

func TestRunDoctorFailsWhenBrokerSocketMissing(t *testing.T) {
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "r")
	binDir := filepath.Join(dir, "bin")

	mustMkdirAll(t, filepath.Join(runtimeDir, "ai-agent"))
	mustMkdirAll(t, binDir)
	agentBin := mustWriteExecutable(t, binDir, "ai-agent")
	mustWriteExecutable(t, binDir, "ai-agent-credential-helper")
	mustWriteExecutable(t, binDir, "ai-agent-gh")
	gitBin := mustWriteExecutable(t, binDir, "git")
	ghBin := mustWriteExecutable(t, binDir, "gh")
	mustWriteDoctorConfig(t, dir, true)

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	setDoctorTestHooks(t, doctorTestHooks{
		executable: func() (string, error) { return agentBin, nil },
		health:     func(path string) error { return nil },
		resolveRepo: func(repoPath string) (string, string, bool, error) {
			return "", "", false, fmt.Errorf("not a git repository")
		},
		lookPath: func(name string) (string, error) {
			switch name {
			case "git":
				return gitBin, nil
			case "gh":
				return ghBin, nil
			default:
				return "", fmt.Errorf("unexpected lookup for %s", name)
			}
		},
		execLookPath: func(name string) (string, error) { return "", fmt.Errorf("%s not found", name) },
	})

	doctorModeFlag = string(doctorModeHost)
	doctorBrokerSock = ""
	doctorRepoPath = ""
	doctorRuntime = string(containerRuntimePodman)
	doctorJSON = false

	var out bytes.Buffer
	cmd := newDoctorTestCommand(&out)
	err := runDoctor(cmd, nil)
	if err == nil {
		t.Fatalf("expected readiness failure, got nil\noutput:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[fail] broker-socket") {
		t.Fatalf("expected broker socket failure in output, got:\n%s", out.String())
	}
}

func TestRunDoctorContainerModeRequiresWorkspaceAndRuntimeTooling(t *testing.T) {
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "r")
	binDir := filepath.Join(dir, "bin")
	sockPath := filepath.Join(runtimeDir, "ai-agent", "broker.sock")

	mustMkdirAll(t, filepath.Dir(sockPath))
	mustMkdirAll(t, binDir)
	ln := mustListenUnix(t, sockPath)
	defer func() { _ = ln.Close() }()

	agentBin := mustWriteExecutable(t, binDir, "ai-agent")
	mustWriteExecutable(t, binDir, "ai-agent-credential-helper")
	mustWriteExecutable(t, binDir, "ai-agent-gh")
	gitBin := mustWriteExecutable(t, binDir, "git")
	ghBin := mustWriteExecutable(t, binDir, "gh")
	mustWriteDoctorConfig(t, dir, true)

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("AI_AGENT_WORKSPACE", "")
	setDoctorTestHooks(t, doctorTestHooks{
		executable: func() (string, error) { return agentBin, nil },
		health:     func(path string) error { return nil },
		resolveRepo: func(repoPath string) (string, string, bool, error) {
			return "/workspace/repo", "owner/repo", false, nil
		},
		lookPath: func(name string) (string, error) {
			switch name {
			case "git":
				return gitBin, nil
			case "gh":
				return ghBin, nil
			default:
				return "", fmt.Errorf("%s not found", name)
			}
		},
		execLookPath: func(name string) (string, error) { return "", fmt.Errorf("%s not found", name) },
	})

	doctorModeFlag = string(doctorModeContainer)
	doctorBrokerSock = ""
	doctorRepoPath = ""
	doctorRuntime = string(containerRuntimePodman)
	doctorJSON = false

	var out bytes.Buffer
	cmd := newDoctorTestCommand(&out)
	err := runDoctor(cmd, nil)
	if err == nil {
		t.Fatalf("expected readiness failure, got nil\noutput:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[fail] container-workspace") {
		t.Fatalf("expected workspace failure in output, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[fail] container-runtime") {
		t.Fatalf("expected runtime failure in output, got:\n%s", out.String())
	}
}

func TestBuildDoctorReportUpModeChecksContainerRuntime(t *testing.T) {
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "r")
	binDir := filepath.Join(dir, "bin")
	sockPath := filepath.Join(runtimeDir, "ai-agent", "broker.sock")

	mustMkdirAll(t, filepath.Dir(sockPath))
	mustMkdirAll(t, binDir)
	ln := mustListenUnix(t, sockPath)
	defer func() { _ = ln.Close() }()

	agentBin := mustWriteExecutable(t, binDir, "ai-agent")
	mustWriteExecutable(t, binDir, "ai-agent-credential-helper")
	mustWriteExecutable(t, binDir, "ai-agent-gh")
	gitBin := mustWriteExecutable(t, binDir, "git")
	mustWriteDoctorConfig(t, dir, true)

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	// AI_AGENT_WORKSPACE is set by runUp before calling buildDoctorReport,
	// but we leave it empty here to also trigger that failure.
	t.Setenv("AI_AGENT_WORKSPACE", "")
	setDoctorTestHooks(t, doctorTestHooks{
		executable: func() (string, error) { return agentBin, nil },
		health:     func(path string) error { return nil },
		resolveRepo: func(repoPath string) (string, string, bool, error) {
			return "/workspace/repo", "owner/repo", false, nil
		},
		lookPath: func(name string) (string, error) {
			switch name {
			case "git":
				return gitBin, nil
			default:
				return "", fmt.Errorf("%s not found", name)
			}
		},
		execLookPath: func(name string) (string, error) { return "", fmt.Errorf("%s not found", name) },
	})

	report := buildDoctorReport(doctorModeUp, sockPath, "", containerRuntimePodman)
	if report.Ready {
		t.Fatal("expected report to be not ready when container tools are missing")
	}

	var foundWorkspace, foundRuntime bool
	for _, check := range report.Checks {
		if check.Name == "container-workspace" && check.Status == doctorStatusFail {
			foundWorkspace = true
		}
		if check.Name == "container-runtime" && check.Status == doctorStatusFail {
			foundRuntime = true
		}
	}
	if !foundWorkspace {
		t.Error("doctorModeUp should include a failing container-workspace check")
	}
	if !foundRuntime {
		t.Error("doctorModeUp should include a failing container-runtime check")
	}
}

func TestRunDoctorJSONReportsFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(dir, "missing"))
	mustWriteDoctorConfig(t, dir, true)

	setDoctorTestHooks(t, doctorTestHooks{
		executable: func() (string, error) { return "/tmp/ai-agent", nil },
		health:     func(path string) error { return nil },
		resolveRepo: func(repoPath string) (string, string, bool, error) {
			return "", "", false, fmt.Errorf("not a git repository")
		},
		lookPath:     func(name string) (string, error) { return "/usr/bin/" + name, nil },
		execLookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
	})

	doctorModeFlag = string(doctorModeHost)
	doctorBrokerSock = ""
	doctorRepoPath = ""
	doctorRuntime = string(containerRuntimePodman)
	doctorJSON = true

	var out bytes.Buffer
	cmd := newDoctorTestCommand(&out)
	err := runDoctor(cmd, nil)
	if err == nil {
		t.Fatal("expected readiness failure in JSON mode")
	}
	if !strings.Contains(out.String(), `"ready": false`) {
		t.Fatalf("expected JSON report, got:\n%s", out.String())
	}
}

func TestRunDoctorRejectsInvalidBrokerSocketEnv(t *testing.T) {
	t.Setenv("AI_AGENT_AUTH_SOCK", "relative.sock")

	doctorModeFlag = string(doctorModeHost)
	doctorBrokerSock = ""
	doctorRepoPath = ""
	doctorRuntime = string(containerRuntimePodman)
	doctorJSON = false

	var out bytes.Buffer
	cmd := newDoctorTestCommand(&out)
	err := runDoctor(cmd, nil)
	if err == nil {
		t.Fatal("expected invalid AI_AGENT_AUTH_SOCK error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid AI_AGENT_AUTH_SOCK") {
		t.Fatalf("runDoctor error = %q, want invalid AI_AGENT_AUTH_SOCK", err)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no doctor output, got:\n%s", out.String())
	}
}

func TestRunDoctorFailsWhenInstallationIDMissing(t *testing.T) {
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "r")
	binDir := filepath.Join(dir, "bin")
	sockPath := filepath.Join(runtimeDir, "ai-agent", "broker.sock")

	mustMkdirAll(t, filepath.Dir(sockPath))
	mustMkdirAll(t, binDir)
	ln := mustListenUnix(t, sockPath)
	defer func() { _ = ln.Close() }()

	agentBin := mustWriteExecutable(t, binDir, "ai-agent")
	mustWriteExecutable(t, binDir, "ai-agent-credential-helper")
	mustWriteExecutable(t, binDir, "ai-agent-gh")
	gitBin := mustWriteExecutable(t, binDir, "git")
	ghBin := mustWriteExecutable(t, binDir, "gh")
	mustWriteDoctorConfig(t, dir, false)

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	setDoctorTestHooks(t, doctorTestHooks{
		executable: func() (string, error) { return agentBin, nil },
		health:     func(path string) error { return nil },
		resolveRepo: func(repoPath string) (string, string, bool, error) {
			return "/workspace/repo", "owner/repo", false, nil
		},
		lookPath: func(name string) (string, error) {
			switch name {
			case "git":
				return gitBin, nil
			case "gh":
				return ghBin, nil
			default:
				return "", fmt.Errorf("unexpected lookup for %s", name)
			}
		},
		execLookPath: func(name string) (string, error) { return "", fmt.Errorf("%s not found", name) },
	})

	doctorModeFlag = string(doctorModeHost)
	doctorBrokerSock = ""
	doctorRepoPath = ""
	doctorRuntime = string(containerRuntimePodman)
	doctorJSON = false

	var out bytes.Buffer
	cmd := newDoctorTestCommand(&out)
	err := runDoctor(cmd, nil)
	if err == nil {
		t.Fatalf("expected readiness failure, got nil\noutput:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[fail] broker-installation-ids") {
		t.Fatalf("expected installation ID failure in output, got:\n%s", out.String())
	}
}

func TestRunDoctorFailsWhenPolicyResourceIsMalformed(t *testing.T) {
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "r")
	binDir := filepath.Join(dir, "bin")
	sockPath := filepath.Join(runtimeDir, "ai-agent", "broker.sock")

	mustMkdirAll(t, filepath.Dir(sockPath))
	mustMkdirAll(t, binDir)
	ln := mustListenUnix(t, sockPath)
	defer func() { _ = ln.Close() }()

	agentBin := mustWriteExecutable(t, binDir, "ai-agent")
	mustWriteExecutable(t, binDir, "ai-agent-credential-helper")
	mustWriteExecutable(t, binDir, "ai-agent-gh")
	gitBin := mustWriteExecutable(t, binDir, "git")
	ghBin := mustWriteExecutable(t, binDir, "gh")
	mustWriteDoctorConfigWithResource(t, dir, "github:org:acme")

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	setDoctorTestHooks(t, doctorTestHooks{
		executable: func() (string, error) { return agentBin, nil },
		health:     func(path string) error { return nil },
		resolveRepo: func(repoPath string) (string, string, bool, error) {
			return "/workspace/repo", "owner/repo", false, nil
		},
		lookPath: func(name string) (string, error) {
			switch name {
			case "git":
				return gitBin, nil
			case "gh":
				return ghBin, nil
			default:
				return "", fmt.Errorf("unexpected lookup for %s", name)
			}
		},
		execLookPath: func(name string) (string, error) { return "", fmt.Errorf("%s not found", name) },
	})

	doctorModeFlag = string(doctorModeHost)
	doctorBrokerSock = ""
	doctorRepoPath = ""
	doctorRuntime = string(containerRuntimePodman)
	doctorJSON = false

	var out bytes.Buffer
	cmd := newDoctorTestCommand(&out)
	err := runDoctor(cmd, nil)
	if err == nil {
		t.Fatalf("expected provider-validation failure, got nil\noutput:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[fail] broker-policy-providers") {
		t.Fatalf("expected broker-policy-providers failure, got:\n%s", out.String())
	}
}

func TestRunDoctorFailsWhenPEMUnreadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read unreadable files; skipping PEM readability test")
	}

	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "r")
	binDir := filepath.Join(dir, "bin")
	sockPath := filepath.Join(runtimeDir, "ai-agent", "broker.sock")

	mustMkdirAll(t, filepath.Dir(sockPath))
	mustMkdirAll(t, binDir)
	ln := mustListenUnix(t, sockPath)
	defer func() { _ = ln.Close() }()

	agentBin := mustWriteExecutable(t, binDir, "ai-agent")
	mustWriteExecutable(t, binDir, "ai-agent-credential-helper")
	mustWriteExecutable(t, binDir, "ai-agent-gh")
	gitBin := mustWriteExecutable(t, binDir, "git")
	ghBin := mustWriteExecutable(t, binDir, "gh")
	pemPath := mustWriteDoctorConfig(t, dir, true)
	if err := os.Chmod(pemPath, 0o200); err != nil {
		t.Fatalf("chmod unreadable PEM: %v", err)
	}

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	setDoctorTestHooks(t, doctorTestHooks{
		executable: func() (string, error) { return agentBin, nil },
		health:     func(path string) error { return nil },
		resolveRepo: func(repoPath string) (string, string, bool, error) {
			return "/workspace/repo", "owner/repo", false, nil
		},
		lookPath: func(name string) (string, error) {
			switch name {
			case "git":
				return gitBin, nil
			case "gh":
				return ghBin, nil
			default:
				return "", fmt.Errorf("unexpected lookup for %s", name)
			}
		},
		execLookPath: func(name string) (string, error) { return "", fmt.Errorf("%s not found", name) },
	})

	doctorModeFlag = string(doctorModeHost)
	doctorBrokerSock = ""
	doctorRepoPath = ""
	doctorRuntime = string(containerRuntimePodman)
	doctorJSON = false

	var out bytes.Buffer
	cmd := newDoctorTestCommand(&out)
	err := runDoctor(cmd, nil)
	if err == nil {
		t.Fatalf("expected readiness failure, got nil\noutput:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[fail] broker-pem-files") {
		t.Fatalf("expected PEM readability failure in output, got:\n%s", out.String())
	}
}

type doctorTestHooks struct {
	executable   func() (string, error)
	health       func(string) error
	resolveRepo  func(string) (string, string, bool, error)
	lookPath     func(string) (string, error)
	execLookPath func(string) (string, error)
}

func setDoctorTestHooks(t *testing.T, hooks doctorTestHooks) {
	t.Helper()

	origDoctorExecutable := doctorExecutable
	origDoctorHealth := doctorBrokerHealth
	origDoctorResolveRepo := doctorResolveRepo
	origDoctorLookPath := doctorLookPath
	origExecLookPath := execLookPath
	origOSExecutable := osExecutable

	doctorExecutable = hooks.executable
	doctorBrokerHealth = hooks.health
	doctorResolveRepo = hooks.resolveRepo
	doctorLookPath = hooks.lookPath
	execLookPath = hooks.execLookPath
	osExecutable = hooks.executable

	t.Cleanup(func() {
		doctorExecutable = origDoctorExecutable
		doctorBrokerHealth = origDoctorHealth
		doctorResolveRepo = origDoctorResolveRepo
		doctorLookPath = origDoctorLookPath
		execLookPath = origExecLookPath
		osExecutable = origOSExecutable
	})
}

func newDoctorTestCommand(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetOut(out)
	return cmd
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteExecutable(t *testing.T, dir string, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func mustListenUnix(t *testing.T, socketPath string) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on %s: %v", socketPath, err)
	}
	return ln
}

func mustWriteDoctorConfig(t *testing.T, dir string, withInstallationID bool) string {
	t.Helper()

	configDir := filepath.Join(dir, "config")
	mustMkdirAll(t, configDir)
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)

	pemPath := filepath.Join(dir, "claude.pem")
	if err := os.WriteFile(pemPath, []byte("stub"), 0o600); err != nil {
		t.Fatalf("write pem: %v", err)
	}

	identitiesJSON := fmt.Sprintf(`{
  "schema_version": "ai-agent-identities/v2",
  "agents": {
    "claude": {
      "git_name": "claude[bot]",
      "git_email": "claude@example.com",
      "github_host": "github.com",
      "app_id": "12345",
      "app_key": %q,
      "tool": "claude-code",
      "model": "claude-sonnet-4-6"
    }
  }
}`, pemPath)
	if err := os.WriteFile(config.DefaultIdentitiesPath(), []byte(identitiesJSON), 0o600); err != nil {
		t.Fatalf("write identities: %v", err)
	}

	installationField := `"installation_id": 0,`
	if withInstallationID {
		installationField = `"installation_id": 42,`
	}

	policyJSON := fmt.Sprintf(`{
  "schema_version": "2",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "claude": {
      "resources": ["github:repo:owner/repo"],
      "providers": {
        "github": {
          %s
          "default_permissions": {
            "contents": "write",
            "metadata": "read"
          }
        }
      }
    }
  }
	}`, installationField)
	if err := os.WriteFile(config.DefaultPolicyPath(), []byte(policyJSON), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	return pemPath
}

func mustWriteDoctorConfigWithResource(t *testing.T, dir, resourceURI string) string {
	t.Helper()

	configDir := filepath.Join(dir, "config")
	mustMkdirAll(t, configDir)
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)

	pemPath := filepath.Join(dir, "claude.pem")
	if err := os.WriteFile(pemPath, []byte("stub"), 0o600); err != nil {
		t.Fatalf("write pem: %v", err)
	}

	identitiesJSON := fmt.Sprintf(`{
  "schema_version": "ai-agent-identities/v2",
  "agents": {
    "claude": {
      "git_name": "claude[bot]",
      "git_email": "claude@example.com",
      "github_host": "github.com",
      "app_id": "12345",
      "app_key": %q,
      "tool": "claude-code",
      "model": "claude-sonnet-4-6"
    }
  }
}`, pemPath)
	if err := os.WriteFile(config.DefaultIdentitiesPath(), []byte(identitiesJSON), 0o600); err != nil {
		t.Fatalf("write identities: %v", err)
	}

	policyJSON := fmt.Sprintf(`{
  "schema_version": "2",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "claude": {
      "resources": [%q],
      "providers": {
        "github": {
          "installation_id": 42,
          "default_permissions": {"contents": "write", "metadata": "read"}
        }
      }
    }
  }
}`, resourceURI)
	if err := os.WriteFile(config.DefaultPolicyPath(), []byte(policyJSON), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return pemPath
}
