package launcher

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
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

	runGit(t, repoDir, "init")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/owner/repo.git")

	origNewBrokerClient := newBrokerClient
	t.Cleanup(func() { newBrokerClient = origNewBrokerClient })

	client := &stubBrokerClient{
		createResp: &broker.CreateSessionResponse{
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
	if client.calls[0] != broker.MethodCreateSession || client.calls[1] != broker.MethodRevokeSession {
		t.Fatalf("broker calls = %v, want [create_session revoke_session]", client.calls)
	}
}

func TestLaunchRevokesSessionWhenExecFails(t *testing.T) {
	repoDir := t.TempDir()
	runtimeDir := t.TempDir()

	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	runGit(t, repoDir, "init")
	runGit(t, repoDir, "remote", "add", "origin", "https://github.com/owner/repo.git")

	origNewBrokerClient := newBrokerClient
	origSyscallExec := syscallExec
	t.Cleanup(func() {
		newBrokerClient = origNewBrokerClient
		syscallExec = origSyscallExec
	})

	client := &stubBrokerClient{
		createResp: &broker.CreateSessionResponse{
			SessionID:  "sess-123",
			BindSecret: []byte("bind-secret"),
			ExpiresAt:  time.Now().Add(time.Hour),
		},
	}
	newBrokerClient = func(string) brokerClient { return client }
	syscallExec = func(string, []string, []string) error {
		return errors.New("exec failed")
	}

	err := Launch(Options{
		AgentName:    "claude",
		RepoPath:     repoDir,
		SocketPath:   "/unused.sock",
		CredHelper:   "/bin/true",
		AgentCommand: []string{"/bin/true"},
	})
	if err == nil {
		t.Fatal("expected launch to fail")
	}

	if len(client.calls) != 2 {
		t.Fatalf("broker calls = %v, want [create_session revoke_session]", client.calls)
	}
	if client.calls[0] != broker.MethodCreateSession || client.calls[1] != broker.MethodRevokeSession {
		t.Fatalf("broker calls = %v, want [create_session revoke_session]", client.calls)
	}
}

func TestPrepareGhWrapper_MissingBinary(t *testing.T) {
	if _, _, err := prepareGhWrapper("/nonexistent/ai-agent-gh"); err == nil {
		t.Fatal("expected error for missing wrapper")
	}
}

type stubBrokerClient struct {
	calls      []string
	createResp *broker.CreateSessionResponse
}

func (c *stubBrokerClient) CreateSession(req broker.CreateSessionRequest) (*broker.CreateSessionResponse, error) {
	c.calls = append(c.calls, broker.MethodCreateSession)
	return c.createResp, nil
}

func (c *stubBrokerClient) RevokeSession(req broker.RevokeSessionRequest) error {
	c.calls = append(c.calls, broker.MethodRevokeSession)
	return nil
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
