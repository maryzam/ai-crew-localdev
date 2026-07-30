package uphost

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func makeRepoDir(t *testing.T, parent, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(parent, name, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
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
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeRepoDir(t, workspace, "submodule")
	if name, ok := SoleRepository(workspace); ok {
		t.Fatalf("a repo workspace must keep the /workspace landing, got child %q", name)
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
