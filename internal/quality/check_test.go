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

func TestRunCheckClassifiesFailures(t *testing.T) {
	tests := []struct {
		name       string
		command    []string
		wantClass  string
		wantSignal string
	}{
		{"nonzero exit", []string{"sh", "-c", "exit 3"}, FailureClassExit, ""},
		{"terminated by signal", []string{"sh", "-c", "kill -TERM $$"}, FailureClassSignal, "terminated"},
		{"start failure", []string{"/nonexistent/definitely-missing-binary"}, FailureClassStartFailed, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RunCheck(CheckOptions{
				Command:     tt.command,
				EvidenceDir: t.TempDir(),
				TailLines:   10,
			})
			if err != nil {
				t.Fatalf("RunCheck: %v", err)
			}
			if result.Passed {
				t.Fatal("expected failure")
			}
			if result.FailureClass != tt.wantClass {
				t.Fatalf("FailureClass = %q, want %q", result.FailureClass, tt.wantClass)
			}
			if result.Signal != tt.wantSignal {
				t.Fatalf("Signal = %q, want %q", result.Signal, tt.wantSignal)
			}
		})
	}
}

func TestRunCheckPassesEnvAndRunner(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	runnerCalled := false
	result, err := RunCheck(CheckOptions{
		Command:     []string{"sh", "-c", `printf %s "$CHECK_ENV_PROBE" > "$MARKER"`},
		Env:         []string{"PATH=/bin:/usr/bin", "CHECK_ENV_PROBE=isolated", "MARKER=" + marker},
		EvidenceDir: t.TempDir(),
		TailLines:   10,
		Run: func(cmd *exec.Cmd) error {
			runnerCalled = true
			return cmd.Run()
		},
	})
	if err != nil {
		t.Fatalf("RunCheck: %v", err)
	}
	if !result.Passed {
		t.Fatal("expected pass")
	}
	if !runnerCalled {
		t.Fatal("injected runner was not used")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "isolated" {
		t.Fatalf("check env not applied: marker = %q (err %v)", data, err)
	}
}
