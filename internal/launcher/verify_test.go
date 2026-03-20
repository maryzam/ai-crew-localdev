package launcher

import (
	"os/exec"
	"testing"
)

func TestLaunchWithVerify_PassesOnFirstAttempt(t *testing.T) {
	var agentCalls, verifyCalls int

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		// Distinguish agent calls from verify calls by the command.
		if name == "/fake/agent" {
			agentCalls++
			return exec.Command("/bin/true")
		}
		// verify: sh -c "true"
		verifyCalls++
		return exec.Command("/bin/true")
	}

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "true",
		MaxRetries:   2,
		RepoPath:     t.TempDir(),
	}, []string{}, func() {})

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
	var agentCalls, verifyCalls int

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "/fake/agent" {
			agentCalls++
			return exec.Command("/bin/true")
		}
		verifyCalls++
		// Fail first two verify attempts, pass on third.
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
	}, []string{}, func() {})

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
	var agentCalls int
	revoked := false

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "/fake/agent" {
			agentCalls++
			return exec.Command("/bin/true")
		}
		return exec.Command("/bin/false") // verify always fails
	}

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "make test",
		MaxRetries:   1,
		RepoPath:     t.TempDir(),
	}, []string{}, func() { revoked = true })

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
	revoked := false

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/bin/false") // agent fails
	}

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "make test",
		MaxRetries:   5,
		RepoPath:     t.TempDir(),
	}, []string{}, func() { revoked = true })

	if err == nil {
		t.Fatal("expected error when agent fails")
	}
	if !revoked {
		t.Error("session should be revoked when agent fails")
	}
}

func TestLaunchWithVerify_ZeroRetries(t *testing.T) {
	var agentCalls int

	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "/fake/agent" {
			agentCalls++
			return exec.Command("/bin/true")
		}
		return exec.Command("/bin/false") // verify fails
	}

	err := launchWithVerify("/fake/agent", Options{
		AgentCommand: []string{"/fake/agent"},
		VerifyCmd:    "make test",
		MaxRetries:   0,
		RepoPath:     t.TempDir(),
	}, []string{}, func() {})

	if err == nil {
		t.Fatal("expected error with 0 retries and failing verify")
	}
	if agentCalls != 1 {
		t.Errorf("agent should run exactly once with 0 retries, ran %d times", agentCalls)
	}
}
