package launcher

import (
	"os/exec"
	"testing"
)

func TestLaunchWithVerify_PassesOnFirstAttempt(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var agentCalls, verifyCalls int

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "/fake/agent" {
			agentCalls++
			return exec.Command("/bin/true")
		}
		verifyCalls++
		return exec.Command("/bin/true")
	}

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "true",
		MaxRetries:   2,
		RepoPath:     t.TempDir(),
	}, []string{}, "sess-test-pass", func() {})

	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if agentCalls != 1 {
		t.Errorf("agent should run once, ran %d times", agentCalls)
	}
	if verifyCalls != 1 {
		t.Errorf("verify should run once, ran %d times", verifyCalls)
	}
}

func TestLaunchWithVerify_RetriesOnVerifyFailure(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var agentCalls, verifyCalls int

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "/fake/agent" {
			agentCalls++
			return exec.Command("/bin/true")
		}
		verifyCalls++
		if verifyCalls <= 2 {
			return exec.Command("/bin/false")
		}
		return exec.Command("/bin/true")
	}

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "make test",
		MaxRetries:   2,
		RepoPath:     t.TempDir(),
	}, []string{}, "sess-test-retry", func() {})

	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if agentCalls != 3 {
		t.Errorf("agent should run 3 times (1 + 2 retries), ran %d times", agentCalls)
	}
	if verifyCalls != 3 {
		t.Errorf("verify should run 3 times, ran %d times", verifyCalls)
	}
}

func TestLaunchWithVerify_FailsAfterAllRetries(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var agentCalls int
	revoked := false

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "/fake/agent" {
			agentCalls++
			return exec.Command("/bin/true")
		}
		return exec.Command("/bin/false")
	}

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "make test",
		MaxRetries:   1,
		RepoPath:     t.TempDir(),
	}, []string{}, "sess-test-fail", func() { revoked = true })

	if err == nil {
		t.Fatal("expected error after all retries exhausted")
	}
	if agentCalls != 2 {
		t.Errorf("agent should run 2 times (1 + 1 retry), ran %d times", agentCalls)
	}
	if !revoked {
		t.Error("session should be revoked on final failure")
	}
}

func TestLaunchWithVerify_AgentFailureStopsImmediately(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	revoked := false

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/bin/false")
	}

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "make test",
		MaxRetries:   5,
		RepoPath:     t.TempDir(),
	}, []string{}, "sess-test-agent-fail", func() { revoked = true })

	if err == nil {
		t.Fatal("expected error when agent fails")
	}
	if !revoked {
		t.Error("session should be revoked when agent fails")
	}
}

func TestLaunchWithVerify_ZeroRetries(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	var agentCalls int

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "/fake/agent" {
			agentCalls++
			return exec.Command("/bin/true")
		}
		return exec.Command("/bin/false")
	}

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "make test",
		MaxRetries:   0,
		RepoPath:     t.TempDir(),
	}, []string{}, "sess-test-zero", func() {})

	if err == nil {
		t.Fatal("expected error with 0 retries and failing verify")
	}
	if agentCalls != 1 {
		t.Errorf("agent should run exactly once with 0 retries, ran %d times", agentCalls)
	}
}

func TestLaunchWithVerify_CleansUpSessionFile(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	// Save a session file that cleanup should remove.
	sessID := "sess-cleanup-test"
	if err := SaveSessionInfo(SessionInfo{
		SessionID: sessID,
		AgentName: "test",
		Repo:      "o/r",
	}); err != nil {
		t.Fatalf("SaveSessionInfo: %v", err)
	}

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/bin/true")
	}

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "true",
		MaxRetries:   0,
		RepoPath:     t.TempDir(),
	}, []string{}, sessID, func() {})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Session file should have been removed by cleanup.
	if _, err := LoadSessionInfo(sessID); err == nil {
		t.Error("session file should have been removed after cleanup")
	}
}
