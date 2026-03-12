package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareGhWrapper_Empty(t *testing.T) {
	dir, cleanup, err := prepareGhWrapper("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	if dir != "" {
		t.Errorf("expected empty dir for empty wrapper path, got %q", dir)
	}
}

func TestPrepareGhWrapper_CreatesSymlink(t *testing.T) {
	// Create a fake ai-agent-gh binary.
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "ai-agent-gh")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	dir, cleanup, err := prepareGhWrapper(fakeBin)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()

	if dir == "" {
		t.Fatal("expected non-empty dir")
	}

	// Verify the gh symlink exists and points to the right target.
	ghLink := filepath.Join(dir, "gh")
	target, err := os.Readlink(ghLink)
	if err != nil {
		t.Fatalf("gh symlink not found: %v", err)
	}

	absTarget, _ := filepath.Abs(fakeBin)
	if target != absTarget {
		t.Errorf("symlink target = %q, want %q", target, absTarget)
	}
}

func TestPrepareGhWrapper_MissingBinary(t *testing.T) {
	_, _, err := prepareGhWrapper("/nonexistent/ai-agent-gh")
	if err == nil {
		t.Fatal("expected error for missing wrapper binary")
	}
}
