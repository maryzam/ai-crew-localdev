package uphost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func makeRepoDir(t *testing.T, parent, name string) {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q")
}

func TestSoleRepositoryReturnsSingleChildRepo(t *testing.T) {
	workspace := t.TempDir()
	makeRepoDir(t, workspace, "only-repo")
	if err := os.MkdirAll(filepath.Join(workspace, "plain-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	name, ok := SoleRepository(workspace)
	if !ok || name != "only-repo" {
		t.Fatalf("SoleRepository = (%q, %v), want (only-repo, true)", name, ok)
	}
}

func TestSoleRepositoryFalseForMultipleRepos(t *testing.T) {
	workspace := t.TempDir()
	makeRepoDir(t, workspace, "alpha")
	makeRepoDir(t, workspace, "beta")
	if name, ok := SoleRepository(workspace); ok {
		t.Fatalf("SoleRepository = (%q, true), want false when more than one repo exists", name)
	}
}

func TestSoleRepositoryFalseWhenNoRepo(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := SoleRepository(workspace); ok {
		t.Fatal("SoleRepository should be false when no child is a git repository")
	}
}

func TestSoleRepositoryFalseWhenWorkspaceIsItselfARepo(t *testing.T) {
	workspace := t.TempDir()
	runGit(t, workspace, "init", "-q")
	makeRepoDir(t, workspace, "submodule")
	if name, ok := SoleRepository(workspace); ok {
		t.Fatalf("a repo workspace must keep the /workspace landing, got child %q", name)
	}
}

func TestSoleRepositoryIgnoresCorruptGitMarker(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "broken", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := SoleRepository(workspace); ok {
		t.Fatal("a directory with an empty or corrupt .git must not count as a repository")
	}
}

func TestSoleRepositoryRejectsControlCharacterRepoName(t *testing.T) {
	workspace := t.TempDir()
	makeRepoDir(t, workspace, "re\x1bpo")
	if name, ok := SoleRepository(workspace); ok {
		t.Fatalf("a repo directory name with control characters must not be selected, got %q", name)
	}
}

func TestSoleRepositoryDetectsWorktree(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init", "-q")
	runGit(t, source, "-c", "user.email=t@example.test", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "init")
	workspace := t.TempDir()
	runGit(t, source, "worktree", "add", "-q", filepath.Join(workspace, "wt"))

	name, ok := SoleRepository(workspace)
	if !ok || name != "wt" {
		t.Fatalf("SoleRepository = (%q, %v), want (wt, true) for a git worktree", name, ok)
	}
}

func TestSoleRepositoryFallsBackWhenWorkspaceExceedsScanLimit(t *testing.T) {
	workspace := t.TempDir()
	makeRepoDir(t, workspace, "only-repo")
	for i := 0; i <= soleRepositoryScanLimit; i++ {
		if err := os.Mkdir(filepath.Join(workspace, "pad-"+strconv.Itoa(i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := SoleRepository(workspace); ok {
		t.Fatal("an oversized workspace must fall back to the /workspace landing")
	}
}
