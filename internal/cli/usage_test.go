package cli

import (
	"bytes"
	"errors"
	"os/exec"
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunUsageDefaultsToOfflineAggregateAndScrubsEnvironment(t *testing.T) {
	originalLookPath, originalRunCmd := usageLookPath, usageRunCmd
	t.Cleanup(func() { usageLookPath, usageRunCmd = originalLookPath, originalRunCmd })
	usageLookPath = func(string) (string, error) { return "/tool/ccusage", nil }
	t.Setenv("GITHUB_TOKEN", "secret")
	usageRunCmd = func(command *exec.Cmd) error {
		want := []string{"/tool/ccusage", "monthly", "--offline", "--compact"}
		if !reflect.DeepEqual(command.Args, want) {
			t.Fatalf("args = %#v", command.Args)
		}
		for _, item := range command.Env {
			if item == "GITHUB_TOKEN=secret" {
				t.Fatal("secret environment passed to ccusage")
			}
		}
		return nil
	}
	if err := runUsage(&cobra.Command{}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRunUsageExplainsMissingAdapter(t *testing.T) {
	originalLookPath := usageLookPath
	t.Cleanup(func() { usageLookPath = originalLookPath })
	usageLookPath = func(string) (string, error) { return "", errors.New("missing") }
	if err := runUsage(&cobra.Command{}, nil); err == nil {
		t.Fatal("expected missing adapter error")
	}
}

func TestRunUsageHandlesHelp(t *testing.T) {
	cmd := &cobra.Command{Use: "usage"}
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := runUsage(cmd, []string{"--help"}); err != nil {
		t.Fatal(err)
	}
}
