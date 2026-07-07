package launcher

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
	"github.com/maryzam/ai-crew-localdev/internal/providers/profiles"
)

func TestPrepareCommandWrappers_Empty(t *testing.T) {
	dir, cleanup, err := prepareCommandWrappers("", []string{"gh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	if dir != "" {
		t.Fatalf("expected empty dir, got %q", dir)
	}
}

func TestPrepareCommandWrappers_CreatesProfileCommandSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	wrapper := filepath.Join(tmpDir, "ai-agent-gh")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}

	commands := profiles.Commands()
	if len(commands) == 0 {
		t.Fatal("no interception profile declares commands")
	}

	dir, cleanup, err := prepareCommandWrappers(wrapper, commands)
	if err != nil {
		t.Fatalf("prepareCommandWrappers: %v", err)
	}
	defer cleanup()

	absWrapper, _ := filepath.Abs(wrapper)
	for _, command := range commands {
		target, err := os.Readlink(filepath.Join(dir, command))
		if err != nil {
			t.Fatalf("read %s symlink: %v", command, err)
		}
		if target != absWrapper {
			t.Fatalf("%s symlink target = %q, want %q", command, target, absWrapper)
		}
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
		createResp: &api.CreateSessionResponse{
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
	if client.calls[0] != api.MethodCreateSession || client.calls[1] != api.MethodRevokeSession {
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

func TestLaunchPublishesObservabilityThroughBrokerBeforeRevocation(t *testing.T) {
	repoDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/owner/repo.git")

	originalClient := newBrokerClient
	t.Cleanup(func() { newBrokerClient = originalClient })
	client := &stubBrokerClient{
		createResp: &api.CreateSessionResponse{SessionID: "sess-123", BindSecret: []byte("bind-secret"), ExpiresAt: time.Now().Add(time.Hour)},
	}
	newBrokerClient = func(string) brokerClient { return client }

	_ = Launch(Options{
		AgentName:             "codex",
		RepoPath:              repoDir,
		SocketPath:            "/unused.sock",
		CredHelper:            "/bin/true",
		AgentCommand:          []string{"true"},
		ObservabilityResource: "langfuse:project:managed-runs",
	})

	resources := client.createReqs[0].Resources
	if len(resources) != 2 || resources[1] != "langfuse:project:managed-runs" {
		t.Fatalf("resources = %v", resources)
	}
	if len(client.publishReqs) == 0 {
		t.Fatal("managed run did not publish telemetry through the broker")
	}
	if client.publishReqs[0].Resource != "langfuse:project:managed-runs" {
		t.Fatalf("publish resource = %q", client.publishReqs[0].Resource)
	}
	if client.calls[len(client.calls)-1] != api.MethodRevokeSession {
		t.Fatalf("broker calls = %v, telemetry must publish before revocation", client.calls)
	}
}

func TestLaunchCollectsNativeUsageWithoutRemoteExport(t *testing.T) {
	repoDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())
	t.Setenv("AI_AGENT_RUN_TELEMETRY_LOG", logPath)
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/owner/repo.git")

	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(t.TempDir(), "claude")
	if err := os.Symlink(testBinary, agentPath); err != nil {
		t.Fatal(err)
	}

	originalClient := newBrokerClient
	t.Cleanup(func() { newBrokerClient = originalClient })
	client := &stubBrokerClient{
		createResp: &api.CreateSessionResponse{SessionID: "sess-123", BindSecret: []byte("bind-secret"), ExpiresAt: time.Now().Add(time.Hour)},
	}
	newBrokerClient = func(string) brokerClient { return client }

	if err := Launch(Options{
		AgentName:    "claude",
		RepoPath:     repoDir,
		SocketPath:   "/unused.sock",
		CredHelper:   "/bin/true",
		AgentCommand: []string{agentPath, "-test.run=^TestLauncherNativeTelemetryHelper$"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(client.publishReqs) != 0 {
		t.Fatalf("local usage collection published %d remote payloads", len(client.publishReqs))
	}
	runs, err := telemetry.ReadRunHistory(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Usage == nil || runs[0].Usage.TotalTokens == nil || *runs[0].Usage.TotalTokens != 15 {
		t.Fatalf("runs = %#v", runs)
	}
}

func TestLauncherNativeTelemetryHelper(t *testing.T) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")
	if endpoint == "" {
		return
	}
	header, err := url.QueryUnescape(os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"))
	if err != nil {
		t.Fatal(err)
	}
	authorization, ok := strings.CutPrefix(header, "Authorization=")
	if !ok {
		t.Fatalf("telemetry authorization header = %q", header)
	}
	payload := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"claude_code.api_request"},"attributes":[{"key":"model","value":{"stringValue":"claude-test"}},{"key":"input_tokens","value":{"intValue":"10"}},{"key":"output_tokens","value":{"intValue":"5"}}]}]}]}]}`
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", authorization)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("telemetry status = %d", response.StatusCode)
	}
}

func TestLaunchRevokesSessionWhenAgentFails(t *testing.T) {
	client := launchAgentForTest(t, "false")

	if len(client.calls) != 2 ||
		client.calls[0] != api.MethodCreateSession ||
		client.calls[1] != api.MethodRevokeSession {
		t.Fatalf("broker calls = %v, want [create_session revoke_session]", client.calls)
	}
}

func TestLaunchRevokesSessionWhenAgentSucceeds(t *testing.T) {
	client := launchAgentForTest(t, "true")

	if len(client.calls) != 2 ||
		client.calls[0] != api.MethodCreateSession ||
		client.calls[1] != api.MethodRevokeSession {
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
		createResp: &api.CreateSessionResponse{
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
		createResp: &api.CreateSessionResponse{
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
	client := &stubBrokerClient{createResp: &api.CreateSessionResponse{
		SessionID: "sess-123", BindSecret: []byte("bind-secret"), ExpiresAt: time.Now().Add(time.Hour),
	}}
	newBrokerClient = func(string) brokerClient { return client }

	if err := Launch(Options{
		AgentName: "claude", RepoPath: repoDir, SocketPath: "/unused.sock",
		CredHelper: "/bin/true", AgentCommand: []string{"true"},
	}); err != nil {
		t.Fatalf("Launch with telemetry disabled: %v", err)
	}

	if len(client.calls) != 2 || client.calls[0] != api.MethodCreateSession || client.calls[1] != api.MethodRevokeSession {
		t.Fatalf("broker calls = %v, want [create_session revoke_session]", client.calls)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("disabled telemetry must not write %s (stat err = %v)", logPath, err)
	}
}

func TestPrepareCommandWrappers_MissingBinary(t *testing.T) {
	if _, _, err := prepareCommandWrappers("/nonexistent/ai-agent-gh", []string{"gh"}); err == nil {
		t.Fatal("expected error for missing wrapper")
	}
}

type stubBrokerClient struct {
	calls       []string
	createReqs  []api.CreateSessionRequest
	publishReqs []api.PublishTelemetryRequest
	createResp  *api.CreateSessionResponse
	createErr   error
	publishErr  error
	revokeErr   error
}

func (c *stubBrokerClient) PublishTelemetry(req api.PublishTelemetryRequest) (*api.PublishTelemetryResponse, error) {
	c.calls = append(c.calls, api.MethodPublishTelemetry)
	c.publishReqs = append(c.publishReqs, req)
	if c.publishErr != nil {
		return nil, c.publishErr
	}
	return &api.PublishTelemetryResponse{AcceptedBytes: len(req.Payload)}, nil
}

func (c *stubBrokerClient) CreateSession(req api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	c.calls = append(c.calls, api.MethodCreateSession)
	c.createReqs = append(c.createReqs, req)
	return c.createResp, c.createErr
}

func (c *stubBrokerClient) RevokeSession(req api.RevokeSessionRequest) error {
	c.calls = append(c.calls, api.MethodRevokeSession)
	return c.revokeErr
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
