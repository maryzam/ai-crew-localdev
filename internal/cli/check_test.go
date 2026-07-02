package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunCheckReportsPassWithoutOutput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	checkDir, checkKeepSuccessLog, checkTailLines = "", false, 60

	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := runCheck(cmd, []string{"sh", "-c", "printf noisy"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "noisy") || !strings.Contains(output.String(), "check: passed") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunCheckRejectsInvalidTailLines(t *testing.T) {
	checkTailLines = 5000
	t.Cleanup(func() { checkTailLines = 60 })
	if err := runCheck(&cobra.Command{}, []string{"true"}); err == nil {
		t.Fatal("expected error for out-of-range --tail-lines")
	}
}
