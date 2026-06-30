package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
	langfusecontract "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse/contract"
	"github.com/maryzam/ai-crew-localdev/internal/telemetry"
)

func TestPrepareGhWrapper_Empty(t *testing.T) {
	dir, cleanup, err := prepareGhWrapper("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	if dir != "" {
		t.Fatalf("expected empty dir, got %q", dir)
	}
}

func TestPrepareGhWrapper_CreatesSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	wrapper := filepath.Join(tmpDir, "ai-agent-gh")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	dir, cleanup, err := prepareGhWrapper(wrapper)
	if err != nil {
		t.Fatalf("prepareGhWrapper: %v", err)
	}
	defer cleanup()

	target, err := os.Readlink(filepath.Join(dir, "gh"))
	if err != nil {
		t.Fatalf("read gh symlink: %v", err)
	}

	absWrapper, _ := filepath.Abs(wrapper)
	if target != absWrapper {
		t.Fatalf("symlink target = %q, want %q", target, absWrapper)
	}
}

func TestLaunchRevokesSessionOnPostCreateFailure(t *testing.T) {
	repoDir := t.TempDir()
	runtimeDir := t.TempDir()

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())

	runGit(t, repoDir, "init")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/owner/repo.git")

	origNewBrokerClient := newBrokerClient
	t.Cleanup(func() { newBrokerClient = origNewBrokerClient })

	client := &stubBrokerClient{
		createResp: &brokerapi.CreateSessionResponse{
			SessionID:  "sess-123",
			BindSecret: []byte("bind-secret"),
			ExpiresAt:  time.Now().Add(time.Hour),
		},
	}
	newBrokerClient = func(string) brokerClient { return client }

	err := Launch(Options{
		AgentName:    "claude",
		RepoPath:     repoDir,
		SocketPath:   "/unused.sock",
		CredHelper:   "/bin/true",
		AgentCommand: []string{"definitely-not-a-real-binary"},
	})
	if err == nil {
		t.Fatal("expected launch to fail")
	}

	if len(client.calls) != 2 {
		t.Fatalf("broker calls = %v, want [create_session revoke_session]", client.calls)
	}
	if client.calls[0] != brokerapi.MethodCreateSession || client.calls[1] != brokerapi.MethodRevokeSession {
		t.Fatalf("broker calls = %v, want [create_session revoke_session]", client.calls)
	}

	if len(client.createReqs) != 1 {
		t.Fatalf("create requests = %d, want 1", len(client.createReqs))
	}
	got := client.createReqs[0]
	if len(got.Resources) != 1 || got.Resources[0] != "github:repo:owner/repo" {
		t.Errorf("CreateSessionRequest.Resources = %v, want [github:repo:owner/repo]", got.Resources)
	}
	if got.RunID == "" {
		t.Error("CreateSessionRequest.RunID should be set")
	}
}

func TestLaunchRequestsBrokeredObservabilityCredential(t *testing.T) {
	repoDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/owner/repo.git")

	originalClient := newBrokerClient
	t.Cleanup(func() { newBrokerClient = originalClient })
	client := &stubBrokerClient{
		createResp: &brokerapi.CreateSessionResponse{SessionID: "sess-123", BindSecret: []byte("bind-secret"), ExpiresAt: time.Now().Add(time.Hour)},
		mintErr:    errors.New("observability unavailable"),
	}
	newBrokerClient = func(string) brokerClient { return client }

	_ = Launch(Options{
		AgentName:             "codex",
		RepoPath:              repoDir,
		SocketPath:            "/unused.sock",
		CredHelper:            "/bin/true",
		AgentCommand:          []string{"definitely-not-a-real-binary"},
		ObservabilityResource: "langfuse:project:managed-runs",
	})

	resources := client.createReqs[0].Resources
	if len(resources) != 2 || resources[1] != "langfuse:project:managed-runs" {
		t.Fatalf("resources = %v", resources)
	}
	if len(client.mintReqs) != 1 || client.mintReqs[0].CredentialType != langfusecontract.CredentialType {
		t.Fatalf("mint requests = %#v", client.mintReqs)
	}
}

func TestLaunchRevokesSessionWhenAgentFails(t *testing.T) {
	client := launchAgentForTest(t, "false")

	if len(client.calls) != 2 ||
		client.calls[0] != brokerapi.MethodCreateSession ||
		client.calls[1] != brokerapi.MethodRevokeSession {
		t.Fatalf("broker calls = %v, want [create_session revoke_session]", client.calls)
	}
}

// A session must not outlive its agent even on a clean exit, so revocation
// runs whether the agent succeeds or fails.
func TestLaunchRevokesSessionWhenAgentSucceeds(t *testing.T) {
	client := launchAgentForTest(t, "true")

	if len(client.calls) != 2 ||
		client.calls[0] != brokerapi.MethodCreateSession ||
		client.calls[1] != brokerapi.MethodRevokeSession {
		t.Fatalf("broker calls = %v, want [create_session revoke_session]", client.calls)
	}
}

func TestLaunchPassesBindFDToAgent(t *testing.T) {
	repoDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())

	runGit(t, repoDir, "init")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/owner/repo.git")

	agentPath := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(agentPath, []byte(`#!/bin/sh
set -eu
test "$(cat "/proc/self/fd/$AI_AGENT_SESSION_BIND_FD")" = "bind-secret"
`), 0o755); err != nil {
		t.Fatalf("write agent script: %v", err)
	}

	origNewBrokerClient := newBrokerClient
	t.Cleanup(func() { newBrokerClient = origNewBrokerClient })

	client := &stubBrokerClient{
		createResp: &brokerapi.CreateSessionResponse{
			SessionID:  "sess-123",
			BindSecret: []byte("bind-secret"),
			ExpiresAt:  time.Now().Add(time.Hour),
		},
	}
	newBrokerClient = func(string) brokerClient { return client }

	err := Launch(Options{
		AgentName:    "claude",
		RepoPath:     repoDir,
		SocketPath:   "/unused.sock",
		CredHelper:   "/bin/true",
		AgentCommand: []string{agentPath},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
}

func TestSuperviseAgentReturnsAgentExitCode(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	err := superviseAgent("/bin/sh", Options{
		AgentCommand: []string{"/bin/sh", "-c", "exit 7"},
	}, nil, nil, "sess-exit", func() {}, disabledRecorderForTest(t))

	var exitErr *AgentExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want AgentExitError", err, err)
	}
	if exitErr.ExitCode() != 7 {
		t.Fatalf("ExitCode = %d, want 7", exitErr.ExitCode())
	}
}

func disabledRecorderForTest(t *testing.T) *telemetry.Recorder {
	t.Helper()
	t.Setenv("AI_AGENT_TELEMETRY", "disabled")
	recorder, err := telemetry.StartRun(telemetry.RunContext{RunID: "run_disabled"})
	if err != nil {
		t.Fatal(err)
	}
	return recorder
}

func launchAgentForTest(t *testing.T, agentCmd string) *stubBrokerClient {
	t.Helper()

	repoDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())

	runGit(t, repoDir, "init")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/owner/repo.git")

	origNewBrokerClient := newBrokerClient
	t.Cleanup(func() { newBrokerClient = origNewBrokerClient })

	client := &stubBrokerClient{
		createResp: &brokerapi.CreateSessionResponse{
			SessionID:  "sess-123",
			BindSecret: []byte("bind-secret"),
			ExpiresAt:  time.Now().Add(time.Hour),
		},
	}
	newBrokerClient = func(string) brokerClient { return client }

	_ = Launch(Options{
		AgentName:    "claude",
		RepoPath:     repoDir,
		SocketPath:   "/unused.sock",
		CredHelper:   "/bin/true",
		AgentCommand: []string{agentCmd},
	})
	return client
}

// With telemetry disabled StartRun returns a nil *Recorder; the launcher must
// drive the whole run through that null object without panicking and without
// writing any local telemetry.
func TestLaunchWithTelemetryDisabledUsesNullRecorder(t *testing.T) {
	repoDir := t.TempDir()
	configDir := t.TempDir()
	logPath := filepath.Join(configDir, "runs.jsonl")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	t.Setenv("AI_AGENT_TELEMETRY", "disabled")

	runGit(t, repoDir, "init")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/owner/repo.git")

	origNewBrokerClient := newBrokerClient
	t.Cleanup(func() { newBrokerClient = origNewBrokerClient })
	client := &stubBrokerClient{createResp: &brokerapi.CreateSessionResponse{
		SessionID: "sess-123", BindSecret: []byte("bind-secret"), ExpiresAt: time.Now().Add(time.Hour),
	}}
	newBrokerClient = func(string) brokerClient { return client }

	if err := Launch(Options{
		AgentName: "claude", RepoPath: repoDir, SocketPath: "/unused.sock",
		CredHelper: "/bin/true", AgentCommand: []string{"true"},
	}); err != nil {
		t.Fatalf("Launch with telemetry disabled: %v", err)
	}

	if len(client.calls) != 2 || client.calls[0] != brokerapi.MethodCreateSession || client.calls[1] != brokerapi.MethodRevokeSession {
		t.Fatalf("broker calls = %v, want [create_session revoke_session]", client.calls)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("disabled telemetry must not write %s (stat err = %v)", logPath, err)
	}
}

func TestPrepareGhWrapper_MissingBinary(t *testing.T) {
	if _, _, err := prepareGhWrapper("/nonexistent/ai-agent-gh"); err == nil {
		t.Fatal("expected error for missing wrapper")
	}
}

type stubBrokerClient struct {
	calls      []string
	createReqs []brokerapi.CreateSessionRequest
	mintReqs   []brokerapi.CredentialRequest
	createResp *brokerapi.CreateSessionResponse
	createErr  error
	mintResp   *brokerapi.CredentialResponse
	mintErr    error
	revokeErr  error
}

func (c *stubBrokerClient) MintCredential(req brokerapi.CredentialRequest) (*brokerapi.CredentialResponse, error) {
	c.calls = append(c.calls, brokerapi.MethodMintCredential)
	c.mintReqs = append(c.mintReqs, req)
	return c.mintResp, c.mintErr
}

func (c *stubBrokerClient) CreateSession(req brokerapi.CreateSessionRequest) (*brokerapi.CreateSessionResponse, error) {
	c.calls = append(c.calls, brokerapi.MethodCreateSession)
	c.createReqs = append(c.createReqs, req)
	return c.createResp, c.createErr
}

func (c *stubBrokerClient) RevokeSession(req brokerapi.RevokeSessionRequest) error {
	c.calls = append(c.calls, brokerapi.MethodRevokeSession)
	return c.revokeErr
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
