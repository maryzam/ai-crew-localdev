package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestRunCheckSuppressesPassingOutput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	originalCommand := checkExecCommand
	checkExecCommand = func(string, ...string) *exec.Cmd { return exec.Command("sh", "-c", "printf noisy") }
	t.Cleanup(func() { checkExecCommand = originalCommand })
	checkDir, checkKeepSuccessLog, checkTailLines = "", false, 60

	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := runCheck(cmd, []string{"ignored"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "noisy") || !strings.Contains(output.String(), "check: passed") {
		t.Fatalf("output = %q", output.String())
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
