package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/launcher"
	"github.com/spf13/cobra"
)

func TestSanitizeTaskName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "basic", input: "Add billing webhook", want: "add-billing-webhook"},
		{name: "symbols collapse", input: "Fix flaky / test!!!", want: "fix-flaky-test"},
		{name: "empty after sanitize", input: "!!!", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeTaskName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("sanitizeTaskName returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("sanitizeTaskName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrepareTaskWorktreeCreatesBranchAndWorktree(t *testing.T) {
	repoDir := initTaskTestRepo(t)

	got, err := prepareTaskWorktree(repoDir, "Add billing webhook", "main", "", "")
	if err != nil {
		t.Fatalf("prepareTaskWorktree: %v", err)
	}

	if got.BranchName != "task/add-billing-webhook" {
		t.Fatalf("BranchName = %q, want %q", got.BranchName, "task/add-billing-webhook")
	}
	if got.RepoRoot != repoDir {
		t.Fatalf("RepoRoot = %q, want %q", got.RepoRoot, repoDir)
	}
	if !strings.HasPrefix(got.Path, filepath.Join(filepath.Dir(repoDir), ".ai-agent-worktrees", filepath.Base(repoDir))) {
		t.Fatalf("Path = %q, want managed worktree root under repo parent", got.Path)
	}

	head := strings.TrimSpace(string(mustTaskGitOutput(t, got.Path, "rev-parse", "--abbrev-ref", "HEAD")))
	if head != got.BranchName {
		t.Fatalf("worktree HEAD = %q, want %q", head, got.BranchName)
	}
}

func TestPrepareTaskWorktreeUsesCustomRootAndBranch(t *testing.T) {
	repoDir := initTaskTestRepo(t)
	customRoot := filepath.Join(t.TempDir(), "managed")

	got, err := prepareTaskWorktree(repoDir, "Fix login", "main", "feature/fix-login", customRoot)
	if err != nil {
		t.Fatalf("prepareTaskWorktree: %v", err)
	}

	if got.BranchName != "feature/fix-login" {
		t.Fatalf("BranchName = %q, want %q", got.BranchName, "feature/fix-login")
	}
	if got.Path != filepath.Join(customRoot, filepath.Base(repoDir), "fix-login") {
		t.Fatalf("Path = %q, want %q", got.Path, filepath.Join(customRoot, filepath.Base(repoDir), "fix-login"))
	}
}

func TestRunTaskStartPassesManagedWorktreeToLauncher(t *testing.T) {
	repoDir := initTaskTestRepo(t)
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	origLaunch := taskLaunch
	t.Cleanup(func() { taskLaunch = origLaunch })

	var got launcher.Options
	taskLaunch = func(opts launcher.Options) error {
		got = opts
		return nil
	}

	taskStartAgent = "codex"
	taskStartTaskName = "Add task command"
	taskStartRepo = repoDir
	taskStartBase = "main"
	taskStartBranchName = ""
	taskStartWorktreeRoot = ""
	taskStartSocketPath = ""
	taskStartCredHelper = "/bin/true"
	taskStartGhWrapper = ""
	taskStartVerifyCmd = ""
	taskStartMaxRetries = 2

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	if err := runTaskStart(cmd, []string{"/bin/echo", "hello"}); err != nil {
		t.Fatalf("runTaskStart: %v", err)
	}

	if got.AgentName != "codex" {
		t.Fatalf("AgentName = %q, want %q", got.AgentName, "codex")
	}
	if got.RepoPath == repoDir {
		t.Fatalf("RepoPath = %q, expected managed worktree path", got.RepoPath)
	}
	if !strings.Contains(out.String(), "task worktree ready:") {
		t.Fatalf("expected command output to mention task worktree, got %q", out.String())
	}

	head := strings.TrimSpace(string(mustTaskGitOutput(t, got.RepoPath, "rev-parse", "--abbrev-ref", "HEAD")))
	if head != "task/add-task-command" {
		t.Fatalf("worktree HEAD = %q, want %q", head, "task/add-task-command")
	}
}

func initTaskTestRepo(t *testing.T) string {
	t.Helper()

	repoDir := t.TempDir()
	runTaskGit(t, repoDir, "init", "-b", "main")
	runTaskGit(t, repoDir, "config", "user.name", "Task Test")
	runTaskGit(t, repoDir, "config", "user.email", "task@example.com")
	runTaskGit(t, repoDir, "remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# task test\n"), 0o644); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	runTaskGit(t, repoDir, "add", "README.md")
	runTaskGit(t, repoDir, "commit", "-m", "init")
	return repoDir
}

func runTaskGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func mustTaskGitOutput(t *testing.T, dir string, args ...string) []byte {
	t.Helper()

	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return out
}
