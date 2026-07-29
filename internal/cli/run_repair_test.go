package cli

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func newSSHRepo(t *testing.T, remote string) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"remote", "add", "origin", remote},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return repo
}

func originURL(t *testing.T, repo string) string {
	t.Helper()
	output, err := exec.Command("git", "-C", repo, "remote", "get-url", "origin").CombinedOutput()
	if err != nil {
		t.Fatalf("get-url: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestEnsureHTTPSRemoteSwitchesWhenConfirmed(t *testing.T) {
	repo := newSSHRepo(t, "git@github.com:maryzam/demo.git")
	var out bytes.Buffer
	if err := ensureHTTPSRemote(&out, strings.NewReader("y\n"), true, repo); err != nil {
		t.Fatalf("ensureHTTPSRemote: %v", err)
	}
	if got := originURL(t, repo); got != "https://github.com/maryzam/demo.git" {
		t.Fatalf("origin = %q, want HTTPS", got)
	}
	if !strings.Contains(out.String(), "https://github.com/maryzam/demo.git") {
		t.Fatalf("output should confirm the switch: %q", out.String())
	}
}

func TestEnsureHTTPSRemoteKeepsSSHWhenDeclined(t *testing.T) {
	repo := newSSHRepo(t, "git@github.com:maryzam/demo.git")
	var out bytes.Buffer
	if err := ensureHTTPSRemote(&out, strings.NewReader("n\n"), true, repo); err != nil {
		t.Fatalf("ensureHTTPSRemote: %v", err)
	}
	if got := originURL(t, repo); got != "git@github.com:maryzam/demo.git" {
		t.Fatalf("origin = %q, want unchanged SSH", got)
	}
}

func TestEnsureHTTPSRemoteFailsClosedWhenNonInteractive(t *testing.T) {
	repo := newSSHRepo(t, "git@github.com:maryzam/demo.git")
	var out bytes.Buffer
	if err := ensureHTTPSRemote(&out, strings.NewReader("y\n"), false, repo); err != nil {
		t.Fatalf("ensureHTTPSRemote: %v", err)
	}
	if got := originURL(t, repo); got != "git@github.com:maryzam/demo.git" {
		t.Fatalf("non-interactive must not modify the remote, got %q", got)
	}
	if out.Len() != 0 {
		t.Fatalf("non-interactive must not prompt: %q", out.String())
	}
}

func TestEnsureHTTPSRemoteNoOpForHTTPSRemote(t *testing.T) {
	repo := newSSHRepo(t, "https://github.com/maryzam/demo.git")
	var out bytes.Buffer
	if err := ensureHTTPSRemote(&out, strings.NewReader("y\n"), true, repo); err != nil {
		t.Fatalf("ensureHTTPSRemote: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("HTTPS remote must not prompt: %q", out.String())
	}
}

func TestReadOneLineLeavesRemainingInput(t *testing.T) {
	reader := strings.NewReader("yes\nrest for the agent")
	line, err := readOneLine(reader)
	if err != nil {
		t.Fatal(err)
	}
	if line != "yes" {
		t.Fatalf("line = %q, want yes", line)
	}
	remaining, _ := readOneLine(reader)
	if remaining != "rest for the agent" {
		t.Fatalf("remaining input consumed incorrectly: %q", remaining)
	}
}
