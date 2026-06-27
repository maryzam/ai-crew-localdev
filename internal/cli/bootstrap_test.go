package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunBootstrapInstallsDefaultsWithoutOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	existing := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(existing), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("custom\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	bootstrapQuiet = false
	if err := runBootstrap(cmd, nil); err != nil {
		t.Fatalf("runBootstrap: %v", err)
	}
	if !strings.Contains(output.String(), "preserved "+existing) {
		t.Fatalf("output = %q", output.String())
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "custom\n" {
		t.Fatalf("existing file overwritten: %q", data)
	}
}
