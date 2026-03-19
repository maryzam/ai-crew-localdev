package cli

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestEnsureBrokerAlreadyRunning(t *testing.T) {
	// Start a listener to simulate a running broker.
	socketPath := filepath.Join(t.TempDir(), "broker.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// ensureBroker should return immediately without error.
	if err := ensureBroker(socketPath); err != nil {
		t.Fatalf("ensureBroker with running broker: %v", err)
	}
}

func TestBrokerResponds(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "broker.sock")

	// No listener — should return false.
	if brokerResponds(socketPath) {
		t.Error("brokerResponds should return false for missing socket")
	}

	// Start listener — should return true.
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	if !brokerResponds(socketPath) {
		t.Error("brokerResponds should return true for live socket")
	}
}

func TestWaitForBrokerTimeout(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "no-broker.sock")

	start := time.Now()
	result := waitForBroker(socketPath, 500*time.Millisecond)
	elapsed := time.Since(start)

	if result {
		t.Error("waitForBroker should return false when no broker is listening")
	}
	if elapsed < 400*time.Millisecond {
		t.Errorf("waitForBroker returned too quickly: %v", elapsed)
	}
}

func TestXDGRuntimeDirPreserved(t *testing.T) {
	// Verify that RuntimeBaseDir returns existing XDG_RUNTIME_DIR value.
	original := os.Getenv("XDG_RUNTIME_DIR")
	t.Setenv("XDG_RUNTIME_DIR", "/custom/runtime")

	got := os.Getenv("XDG_RUNTIME_DIR")
	if got != "/custom/runtime" {
		t.Errorf("XDG_RUNTIME_DIR should be preserved, got %s", got)
	}

	// Restore.
	if original != "" {
		t.Setenv("XDG_RUNTIME_DIR", original)
	}
}
