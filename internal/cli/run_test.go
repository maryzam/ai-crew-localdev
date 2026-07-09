package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/spf13/cobra"
)

type fakeExitError struct {
	code int
}

func TestConfiguredIdentityModelUsesNamedAgent(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	data := []byte(`{"schema_version":"ai-agent-identities/v2","agents":{"codex":{"model":"gpt-5.2-codex"}}}`)
	if err := os.WriteFile(filepath.Join(configDir, "identities.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := configuredIdentityModel("codex"); got != "gpt-5.2-codex" {
		t.Fatalf("configured model = %q", got)
	}
	if got := configuredIdentityModel("claude"); got != "" {
		t.Fatalf("missing agent model = %q", got)
	}
}

func (e fakeExitError) Error() string {
	return "agent exited"
}

func (e fakeExitError) ExitCode() int {
	return e.code
}

func TestResolveSiblingBinary(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "ai-agent")
	helper := filepath.Join(dir, "ai-agent-credential-helper")

	if err := os.WriteFile(self, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write self: %v", err)
	}
	if err := os.WriteFile(helper, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	origExecutable := osExecutable
	osExecutable = func() (string, error) { return self, nil }
	t.Cleanup(func() { osExecutable = origExecutable })

	got, err := resolveSiblingBinary("ai-agent-credential-helper")
	if err != nil {
		t.Fatalf("resolveSiblingBinary: %v", err)
	}
	if got != helper {
		t.Fatalf("resolveSiblingBinary = %q, want %q", got, helper)
	}
}

func TestResolveOptionalBinaryFallsBackToPath(t *testing.T) {
	origExecutable := osExecutable
	origLookPath := execLookPath
	osExecutable = func() (string, error) { return "", os.ErrNotExist }
	execLookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	t.Cleanup(func() {
		osExecutable = origExecutable
		execLookPath = origLookPath
	})

	got, err := resolveOptionalBinary("ai-agent-gh")
	if err != nil {
		t.Fatalf("resolveOptionalBinary: %v", err)
	}
	if got != "/usr/bin/ai-agent-gh" {
		t.Fatalf("resolveOptionalBinary = %q, want %q", got, "/usr/bin/ai-agent-gh")
	}
}

func TestResolveExecutableFromPathSkipsWrapper(t *testing.T) {
	dir := t.TempDir()
	wrapperBin := filepath.Join(dir, "ai-agent-gh")
	realDir := filepath.Join(dir, "real")
	shimDir := filepath.Join(dir, "shim")
	realGh := filepath.Join(realDir, "gh")
	shimGh := filepath.Join(shimDir, "gh")

	for _, p := range []string{realDir, shimDir} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	if err := os.WriteFile(wrapperBin, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	if err := os.Symlink(wrapperBin, shimGh); err != nil {
		t.Fatalf("symlink shim gh: %v", err)
	}
	if err := os.WriteFile(realGh, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write real gh: %v", err)
	}

	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+realDir)

	got, err := resolveExecutableFromPath("gh", wrapperBin)
	if err != nil {
		t.Fatalf("resolveExecutableFromPath: %v", err)
	}
	if got != realGh {
		t.Fatalf("resolveExecutableFromPath = %q, want %q", got, realGh)
	}
}

func TestResolveRealGhPathPrefersEnvOverride(t *testing.T) {
	dir := t.TempDir()
	realGh := filepath.Join(dir, "gh")
	if err := os.WriteFile(realGh, []byte("stub"), 0755); err != nil {
		t.Fatalf("write real gh: %v", err)
	}

	t.Setenv("AI_AGENT_REAL_GH", realGh)
	t.Setenv("PATH", t.TempDir())

	got := resolveRealGhPath(filepath.Join(dir, "ai-agent-gh"))
	if got != realGh {
		t.Fatalf("resolveRealGhPath = %q, want %q", got, realGh)
	}
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

func TestValidateMaxRetriesBoundsAutomaticSpend(t *testing.T) {
	for _, value := range []int{0, 2, 10} {
		if err := validateMaxRetries(value); err != nil {
			t.Fatalf("validateMaxRetries(%d): %v", value, err)
		}
	}
	for _, value := range []int{-1, 11} {
		if err := validateMaxRetries(value); err == nil {
			t.Fatalf("validateMaxRetries(%d) should fail", value)
		}
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
}

func configureRunTest(t *testing.T, agentName string, repo string) {
	t.Helper()
	origAgent, origTaskRef, origRepo, origSocket := runAgent, runTaskRef, runRepo, runSocketPath
	origCredHelper, origGhWrapper, origVerifyCmd, origMaxRetries := runCredHelper, runGhWrapper, runVerifyCmd, runMaxRetries
	t.Cleanup(func() {
		runAgent, runTaskRef, runRepo, runSocketPath = origAgent, origTaskRef, origRepo, origSocket
		runCredHelper, runGhWrapper, runVerifyCmd, runMaxRetries = origCredHelper, origGhWrapper, origVerifyCmd, origMaxRetries
	})
	runAgent = agentName
	runTaskRef = ""
	runRepo = repo
	runSocketPath = filepath.Join(t.TempDir(), "broker.sock")
	runCredHelper = ""
	runGhWrapper = ""
	runVerifyCmd = ""
	runMaxRetries = 2
}

func TestResolveVerificationLoadsManifestContracts(t *testing.T) {
	repo := writeRunTestManifest(t, `{"schema_version":"ai-agent-manifest/v1","contracts":[{"name":"tests","command":"make test"},{"name":"lint","command":"make lint","retry":"never"}]}`)

	var notes strings.Builder
	contracts, contractsDir, err := resolveVerification(&notes, repo, "")
	_ = contractsDir
	if err != nil {
		t.Fatalf("resolveVerification: %v", err)
	}
	if len(contracts) != 2 {
		t.Fatalf("contracts = %+v", contracts)
	}
	if contracts[0].Name != "tests" || !contracts[0].RetryAgent {
		t.Fatalf("tests contract = %+v, want retry agent by default", contracts[0])
	}
	if contracts[1].Name != "lint" || contracts[1].RetryAgent {
		t.Fatalf("lint contract = %+v, want retry never honored", contracts[1])
	}
	if !strings.Contains(notes.String(), "2 project contract(s)") {
		t.Fatalf("notes = %q", notes.String())
	}
}

func TestResolveVerificationVerifyCmdOverridesManifest(t *testing.T) {
	repo := writeRunTestManifest(t, `{"schema_version":"ai-agent-manifest/v1","contracts":[{"name":"tests","command":"make test"}]}`)

	var notes strings.Builder
	contracts, contractsDir, err := resolveVerification(&notes, repo, "go test ./...")
	_ = contractsDir
	if err != nil {
		t.Fatalf("resolveVerification: %v", err)
	}
	if contracts != nil {
		t.Fatalf("contracts = %+v, want nil when --verify-cmd overrides", contracts)
	}
	if !strings.Contains(notes.String(), "--verify-cmd overrides 1 project contract(s)") {
		t.Fatalf("notes = %q", notes.String())
	}
}

func TestResolveVerificationFailsClosedOnInvalidManifest(t *testing.T) {
	repo := writeRunTestManifest(t, `{"schema_version":"ai-agent-manifest/v1","contracts":[{"name":"tests","command":"  "}]}`)

	if _, _, err := resolveVerification(&strings.Builder{}, repo, ""); err == nil || !strings.Contains(err.Error(), "invalid project manifest") {
		t.Fatalf("err = %v, want fail-closed manifest validation", err)
	}
}

func TestResolveVerificationNoManifestMeansNoContracts(t *testing.T) {
	contracts, _, err := resolveVerification(&strings.Builder{}, t.TempDir(), "")
	if err != nil || contracts != nil {
		t.Fatalf("contracts = %+v, err = %v; want none", contracts, err)
	}
}

func TestResolveVerificationFindsManifestFromSubdirectory(t *testing.T) {
	repo := writeRunTestManifest(t, `{"schema_version":"ai-agent-manifest/v1","contracts":[{"name":"tests","command":"make test"}]}`)
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	subdir := filepath.Join(repo, "pkg", "deep")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}

	contracts, contractsDir, err := resolveVerification(&strings.Builder{}, subdir, "")
	if err != nil {
		t.Fatalf("resolveVerification: %v", err)
	}
	if len(contracts) != 1 || contracts[0].Name != "tests" {
		t.Fatalf("contracts = %+v, want the root manifest discovered from a subdirectory", contracts)
	}
	resolvedRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	resolvedDir, err := filepath.EvalSymlinks(contractsDir)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedDir != resolvedRepo {
		t.Fatalf("contractsDir = %q, want the worktree root %q", contractsDir, repo)
	}
}

func TestResolveVerificationFailsClosedOnNonRegularManifest(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".ai-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, "elsewhere.json")
	if err := os.WriteFile(target, []byte(`{"schema_version":"ai-agent-manifest/v1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repo, ".ai-agent", "manifest.json")); err != nil {
		t.Fatal(err)
	}

	if _, _, err := resolveVerification(&strings.Builder{}, repo, ""); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("err = %v; a present but non-regular manifest must refuse the run, not silently disable contracts", err)
	}
}

const agentsManifest = `{"schema_version":"ai-agent-manifest/v1","agents":{"allowed":["claude","codex"],"defaults":{"claude":{"model":"claude-sonnet-5"}}}}`

func TestManifestAgentAllowlistRefusesUnlistedAgent(t *testing.T) {
	repo := writeRunTestManifest(t, agentsManifest)
	info, err := loadProjectManifest(&strings.Builder{}, repo)
	if err != nil {
		t.Fatalf("loadProjectManifest: %v", err)
	}

	hostIdentity := hostAgentIdentity{value: identity.AgentIdentity{Tool: "claude-code"}, found: true}
	if err := info.enforceAgent("gemini", []string{"gemini"}, hostAgentIdentity{}); err == nil || !strings.Contains(err.Error(), `agent "gemini" is not allowed`) {
		t.Fatalf("err = %v, want fail-closed allowlist refusal", err)
	}
	if err := info.enforceAgent("claude", []string{"claude"}, hostIdentity); err != nil {
		t.Fatalf("allowed agent refused: %v", err)
	}
}

func TestManifestAgentPolicyIsNoOpWithoutDeclaration(t *testing.T) {
	var nilInfo *projectManifestInfo
	if err := nilInfo.enforceAgent("anyone", []string{"anyone"}, hostAgentIdentity{}); err != nil {
		t.Fatalf("missing manifest must not restrict agents: %v", err)
	}
	if model := nilInfo.modelDefault("anyone"); model != "" {
		t.Fatalf("missing manifest returned model %q", model)
	}

	repo := writeRunTestManifest(t, `{"schema_version":"ai-agent-manifest/v1","contracts":[{"name":"t","command":"true"}]}`)
	info, err := loadProjectManifest(&strings.Builder{}, repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := info.enforceAgent("anyone", []string{"anyone"}, hostAgentIdentity{}); err != nil {
		t.Fatalf("manifest without agents section must not restrict agents: %v", err)
	}
}

func TestManifestAgentAllowlistRequiresConfiguredToolMatch(t *testing.T) {
	repo := writeRunTestManifest(t, agentsManifest)
	info, err := loadProjectManifest(&strings.Builder{}, repo)
	if err != nil {
		t.Fatal(err)
	}
	hostIdentity := hostAgentIdentity{value: identity.AgentIdentity{Tool: "claude-code"}, found: true}

	if err := info.enforceAgent("claude", []string{"claude"}, hostIdentity); err != nil {
		t.Fatalf("claude command should match claude-code identity tool: %v", err)
	}
	if err := info.enforceAgent("claude", []string{"codex"}, hostIdentity); err == nil || !strings.Contains(err.Error(), `does not match configured tool "claude-code"`) {
		t.Fatalf("err = %v, want configured-tool mismatch", err)
	}
	if err := info.enforceAgent("claude", []string{"claude"}, hostAgentIdentity{}); err == nil || !strings.Contains(err.Error(), "no host identity is configured") {
		t.Fatalf("err = %v, want missing identity failure", err)
	}
	if err := info.enforceAgent("claude", []string{"claude"}, hostAgentIdentity{value: identity.AgentIdentity{}, found: true}); err == nil || !strings.Contains(err.Error(), "no configured tool") {
		t.Fatalf("err = %v, want missing tool failure", err)
	}
}

func TestManifestModelDefaultAppliesPerAgent(t *testing.T) {
	repo := writeRunTestManifest(t, agentsManifest)
	info, err := loadProjectManifest(&strings.Builder{}, repo)
	if err != nil {
		t.Fatal(err)
	}
	if model := info.modelDefault("claude"); model != "claude-sonnet-5" {
		t.Fatalf("modelDefault(claude) = %q, want claude-sonnet-5", model)
	}
	if model := info.modelDefault("codex"); model != "" {
		t.Fatalf("modelDefault(codex) = %q, want no default", model)
	}
}

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
	configDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
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
