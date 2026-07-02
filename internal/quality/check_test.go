package quality

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRunCheckSuppressesPassingOutput(t *testing.T) {
	original := execCommand
	execCommand = func(string, ...string) *exec.Cmd { return exec.Command("sh", "-c", "printf noisy") }
	t.Cleanup(func() { execCommand = original })

	result, err := RunCheck(CheckOptions{Command: []string{"ignored"}, EvidenceDir: t.TempDir(), TailLines: 60})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("expected pass, got %+v", result)
	}
	if result.LogPath != "" {
		t.Fatalf("passing run without keep-success-log should not retain a log, got %q", result.LogPath)
	}
}

func TestRunCheckReportsFailureEvidence(t *testing.T) {
	original := execCommand
	execCommand = func(string, ...string) *exec.Cmd { return exec.Command("sh", "-c", "printf boom >&2; exit 3") }
	t.Cleanup(func() { execCommand = original })

	dir := t.TempDir()
	result, err := RunCheck(CheckOptions{Command: []string{"ignored"}, EvidenceDir: dir, TailLines: 60})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || result.ExitCode != 3 {
		t.Fatalf("expected failure exit=3, got %+v", result)
	}
	if result.LogPath == "" {
		t.Fatal("failure should retain an evidence log")
	}
	if _, statErr := os.Stat(result.LogPath); statErr != nil {
		t.Fatalf("evidence log missing: %v", statErr)
	}
}

func TestPruneEvidenceCapsDirectory(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxEvidenceFiles+5; i++ {
		path := filepath.Join(dir, fmt.Sprintf("check-%02d.log", i))
		if err := os.WriteFile(path, []byte("evidence"), 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(int64(i+1), 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneEvidence(dir, ""); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != maxEvidenceFiles {
		t.Fatalf("files = %d, want %d", len(entries), maxEvidenceFiles)
	}
}
