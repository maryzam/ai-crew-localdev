package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunCheckReportsPassWithoutOutput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := runCheck(cmd, checkOptions{tailLines: 60}, []string{"sh", "-c", "printf noisy"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "noisy") || !strings.Contains(output.String(), "check: passed") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunCheckRejectsInvalidTailLines(t *testing.T) {
	if err := runCheck(&cobra.Command{}, checkOptions{tailLines: 5000}, []string{"true"}); err == nil {
		t.Fatal("expected error for out-of-range --tail-lines")
	}
}
