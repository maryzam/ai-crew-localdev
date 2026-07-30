package uphost

import (
	"os"
	"path/filepath"
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
