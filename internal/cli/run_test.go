package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type fakeExitError struct {
	code int
}

func (e fakeExitError) Error() string {
	return "agent exited"
}

func (e fakeExitError) ExitCode() int {
	return e.code
}

func TestFinishRunExitsWithAgentStatus(t *testing.T) {
	var gotCode int
	origExitProcess := exitProcess
	exitProcess = func(code int) { gotCode = code }
	t.Cleanup(func() { exitProcess = origExitProcess })

	err := finishRun(fakeExitError{code: 7})
	if err != nil {
		t.Fatalf("finishRun: %v", err)
	}
	if gotCode != 7 {
		t.Fatalf("exit code = %d, want 7", gotCode)
	}
}

func TestFinishRunReturnsNonAgentError(t *testing.T) {
	want := errors.New("launch failed")
	if got := finishRun(want); !errors.Is(got, want) {
		t.Fatalf("finishRun error = %v, want %v", got, want)
	}
}

func writeRunTestManifest(t *testing.T, content string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".ai-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".ai-agent", "manifest.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func runTestOptions(t *testing.T, agentName string, repo string) runOptions {
	t.Helper()
	return runOptions{
		agent:       agentName,
		repo:        repo,
		socketPath:  filepath.Join(t.TempDir(), "broker.sock"),
		maxRetries:  2,
		isolateHome: true,
	}
}

const agentsManifest = `{"schema_version":"ai-agent-manifest/v2","agents":{"allowed":["claude","codex"],"defaults":{"claude":{"model":"claude-sonnet-5"}}}}`

func TestRunRefusesDisallowedAgentBeforeAnyBrokerActivity(t *testing.T) {
	repo := writeRunTestManifest(t, agentsManifest)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())

	options := runTestOptions(t, "gemini", repo)

	command := &cobra.Command{}
	command.SetErr(new(bytes.Buffer))
	err := runRun(command, options, []string{"gemini"})

	if err == nil || !strings.Contains(err.Error(), `agent "gemini" is not allowed`) {
		t.Fatalf("err = %v; the allowlist refusal must fire before broker socket resolution or session creation", err)
	}
	if strings.Contains(err.Error(), "sock") || strings.Contains(err.Error(), "broker") {
		t.Fatalf("err = %v; refusal must not depend on broker availability", err)
	}
}

func TestRunRefusesAllowedAgentWithWrongToolBeforeAnyBrokerActivity(t *testing.T) {
	repo := writeRunTestManifest(t, agentsManifest)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	options := runTestOptions(t, "claude", repo)

	command := &cobra.Command{}
	command.SetErr(new(bytes.Buffer))
	err := runRun(command, options, []string{"codex"})

	if err == nil || !strings.Contains(err.Error(), `does not match configured tool "claude-code"`) {
		t.Fatalf("err = %v; the configured-tool refusal must fire before broker socket resolution or session creation", err)
	}
	if strings.Contains(err.Error(), "sock") || strings.Contains(err.Error(), "broker") {
		t.Fatalf("err = %v; refusal must not depend on broker availability", err)
	}
}

func TestRunAllowedAgentWithMatchingToolReachesPostGovernanceSetup(t *testing.T) {
	repo := writeRunTestManifest(t, agentsManifest)
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "agent@example.test"}, {"config", "user.name", "Agent"}, {"remote", "add", "origin", "https://github.com/owner/repo.git"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONTAINER", "1")
	t.Setenv("HOME", t.TempDir())
	options := runTestOptions(t, "codex", repo)
	options.credHelper = filepath.Join(t.TempDir(), "missing-helper")

	command := &cobra.Command{}
	command.SetErr(new(bytes.Buffer))
	err := runRun(command, options, []string{"codex"})

	if err == nil || !strings.Contains(err.Error(), "credential helper not found") {
		t.Fatalf("err = %v; matching manifest/tool intent should reach helper setup", err)
	}
}
