package cli

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/control"
)

func newSSHRepo(t *testing.T, fetch string) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"remote", "add", "origin", fetch},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return repo
}

func remoteURL(t *testing.T, repo string, push bool) string {
	t.Helper()
	args := []string{"-C", repo, "remote", "get-url"}
	if push {
		args = append(args, "--push")
	}
	args = append(args, "origin")
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("get-url: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestOfferHTTPSRepairRewritesFetchAndPushURLs(t *testing.T) {
	repo := newSSHRepo(t, "git@github.com:owner/repo.git")
	setPush := exec.Command("git", "-C", repo, "remote", "set-url", "--push", "origin", "git@github.com:owner/repo.git")
	if output, err := setPush.CombinedOutput(); err != nil {
		t.Fatalf("set push url: %v: %s", err, output)
	}
	sshErr := &control.SSHRemoteError{RootPath: repo, Slug: "owner/repo"}

	var out bytes.Buffer
	repaired, err := offerHTTPSRepair(&out, strings.NewReader("y\n"), sshErr)
	if err != nil || !repaired {
		t.Fatalf("offerHTTPSRepair = (%v, %v), want (true, nil)", repaired, err)
	}
	want := "https://github.com/owner/repo.git"
	if got := remoteURL(t, repo, false); got != want {
		t.Fatalf("fetch url = %q, want %q", got, want)
	}
	if got := remoteURL(t, repo, true); got != want {
		t.Fatalf("push url = %q, want %q - an SSH push url must be rewritten too", got, want)
	}
	if !strings.Contains(out.String(), "switched origin") {
		t.Fatalf("confirmation missing: %q", out.String())
	}
}

func TestSetHTTPSRemoteReplacesMultiplePushURLs(t *testing.T) {
	repo := newSSHRepo(t, "git@github.com:owner/repo.git")
	for _, pushURL := range []string{"git@github.com:owner/repo.git", "ssh://git@github.com/owner/repo.git"} {
		add := exec.Command("git", "-C", repo, "remote", "set-url", "--add", "--push", "origin", pushURL)
		if output, err := add.CombinedOutput(); err != nil {
			t.Fatalf("add push url: %v: %s", err, output)
		}
	}

	httpsURL := "https://github.com/owner/repo.git"
	if err := setHTTPSRemote(repo, httpsURL); err != nil {
		t.Fatalf("setHTTPSRemote: %v", err)
	}
	if got := remoteURL(t, repo, false); got != httpsURL {
		t.Fatalf("fetch url = %q, want %q", got, httpsURL)
	}
	pushURLs := allPushURLs(t, repo)
	if len(pushURLs) != 1 || pushURLs[0] != httpsURL {
		t.Fatalf("push urls = %v, want exactly [%s]", pushURLs, httpsURL)
	}
	if resolution, err := control.ResolveRepository(repo); err != nil || resolution.SSH {
		t.Fatalf("repaired repo should resolve as non-SSH, got SSH=%v err=%v", resolution.SSH, err)
	}
}

func allPushURLs(t *testing.T, repo string) []string {
	t.Helper()
	output, err := exec.Command("git", "-C", repo, "remote", "get-url", "--push", "--all", "origin").CombinedOutput()
	if err != nil {
		t.Fatalf("get-url --push --all: %v: %s", err, output)
	}
	var urls []string
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	return urls
}

func TestOfferHTTPSRepairDeclinedLeavesRemote(t *testing.T) {
	repo := newSSHRepo(t, "git@github.com:owner/repo.git")
	sshErr := &control.SSHRemoteError{RootPath: repo, Slug: "owner/repo"}
	var out bytes.Buffer
	repaired, err := offerHTTPSRepair(&out, strings.NewReader("n\n"), sshErr)
	if err != nil || repaired {
		t.Fatalf("offerHTTPSRepair = (%v, %v), want (false, nil)", repaired, err)
	}
	if got := remoteURL(t, repo, false); got != "git@github.com:owner/repo.git" {
		t.Fatalf("declined repair must not change the remote, got %q", got)
	}
}

func TestIsTerminalReaderFalseForNonFile(t *testing.T) {
	if isTerminalReader(strings.NewReader("x")) {
		t.Fatal("a non-file reader must not be reported as a terminal")
	}
}

func TestIsTerminalWriterFalseForNonFile(t *testing.T) {
	if isTerminalWriter(&bytes.Buffer{}) {
		t.Fatal("a non-file writer must not be reported as a terminal")
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
