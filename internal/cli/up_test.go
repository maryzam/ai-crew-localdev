package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRepoRoot(t *testing.T) {
	root := t.TempDir()
	devcontainerDir := filepath.Join(root, ".devcontainer")
	if err := os.Mkdir(devcontainerDir, 0o755); err != nil {
		t.Fatalf("mkdir .devcontainer: %v", err)
	}

	subDir := filepath.Join(root, "sub", "deep")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir sub/deep: %v", err)
	}

	got, err := findRepoRoot(subDir)
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	if got != root {
		t.Errorf("findRepoRoot(%s) = %s, want %s", subDir, got, root)
	}
}

func TestFindRepoRootGitFallback(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	got, err := findRepoRoot(root)
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	if got != root {
		t.Errorf("findRepoRoot(%s) = %s, want %s", root, got, root)
	}
}

func TestFindRepoRootNoMarker(t *testing.T) {
	dir := t.TempDir()
	got, err := findRepoRoot(dir)
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	// Should fall back to the original dir.
	if got != dir {
		t.Errorf("findRepoRoot(%s) = %s, want fallback to same dir", dir, got)
	}
}
