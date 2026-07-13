package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

func writeRunTestIdentity(t *testing.T, configDir string, agentName string, tool string, model string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(fmt.Sprintf(`{"schema_version":"ai-agent-identities/v2","agents":{%q:{"git_name":"%s[bot]","git_email":"%s@example.test","github_host":"github.com","app_id":"123","app_key":"/tmp/key.pem","tool":%q,"model":%q}}}`, agentName, agentName, agentName, tool, model))
	if err := os.WriteFile(filepath.Join(configDir, "identities.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	policyData, err := json.Marshal(map[string]any{
		"schema_version":       "2",
		"default_session_ttl":  "8h",
		"default_idle_timeout": "1h",
		"agents": map[string]any{agentName: map[string]any{
			"resources": []string{"github:repo:example-org/example-repo"},
			"providers": map[string]any{"github": map[string]any{
				"installation_id":     42,
				"default_permissions": map[string]string{"contents": "read"},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "policy.json"), policyData, 0o600); err != nil {
		t.Fatal(err)
	}
}

func configureRunTest(t *testing.T, agentName string, repo string) {
	t.Helper()
	origAgent, origTaskRef, origRepo, origSocket := runAgent, runTaskRef, runRepo, runSocketPath
	origCredHelper, origGhWrapper, origVerifyCmd, origMaxRetries := runCredHelper, runGhWrapper, runVerifyCmd, runMaxRetries
	origTokenWarnAt, origTokenStopAt := runTokenWarnAt, runTokenStopAt
	t.Cleanup(func() {
		runAgent, runTaskRef, runRepo, runSocketPath = origAgent, origTaskRef, origRepo, origSocket
		runCredHelper, runGhWrapper, runVerifyCmd, runMaxRetries = origCredHelper, origGhWrapper, origVerifyCmd, origMaxRetries
		runTokenWarnAt, runTokenStopAt = origTokenWarnAt, origTokenStopAt
	})
	runAgent = agentName
	runTaskRef = ""
	runRepo = repo
	runSocketPath = filepath.Join(t.TempDir(), "broker.sock")
	runCredHelper = ""
	runGhWrapper = ""
	runVerifyCmd = ""
	runMaxRetries = 2
	runTokenWarnAt = 0
	runTokenStopAt = 0
}

const agentsManifest = `{"schema_version":"ai-agent-manifest/v1","agents":{"allowed":["claude","codex"],"defaults":{"claude":{"model":"claude-sonnet-5"}}}}`

func TestRunRefusesDisallowedAgentBeforeAnyBrokerActivity(t *testing.T) {
	repo := writeRunTestManifest(t, agentsManifest)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())

	configureRunTest(t, "gemini", repo)

	command := &cobra.Command{}
	command.SetErr(new(bytes.Buffer))
	err := runRun(command, []string{"gemini"})

	if err == nil || !strings.Contains(err.Error(), `agent "gemini" is not allowed`) {
		t.Fatalf("err = %v; the allowlist refusal must fire before broker socket resolution or session creation", err)
	}
	if strings.Contains(err.Error(), "sock") || strings.Contains(err.Error(), "broker") {
		t.Fatalf("err = %v; refusal must not depend on broker availability", err)
	}
}

func TestRunRefusesAllowedAgentWithWrongToolBeforeAnyBrokerActivity(t *testing.T) {
	repo := writeRunTestManifest(t, agentsManifest)
	configDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	writeRunTestIdentity(t, configDir, "claude", "claude-code", "claude-sonnet-5")
	configureRunTest(t, "claude", repo)

	command := &cobra.Command{}
	command.SetErr(new(bytes.Buffer))
	err := runRun(command, []string{"codex"})

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
	configDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	t.Setenv("AI_AGENT_CONTAINER", "1")
	t.Setenv("HOME", t.TempDir())
	writeRunTestIdentity(t, configDir, "codex", "codex", "gpt-5.2-codex")
	configureRunTest(t, "codex", repo)
	runCredHelper = filepath.Join(t.TempDir(), "missing-helper")

	command := &cobra.Command{}
	command.SetErr(new(bytes.Buffer))
	err := runRun(command, []string{"codex"})

	if err == nil || !strings.Contains(err.Error(), "credential helper not found") {
		t.Fatalf("err = %v; matching identity/tool should pass manifest governance and reach helper setup", err)
	}
}
