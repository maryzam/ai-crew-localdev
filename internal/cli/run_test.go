package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestResolveVerificationLoadsManifestContracts(t *testing.T) {
	repo := writeRunTestManifest(t, `{"schema_version":"ai-agent-manifest/v1","contracts":[{"name":"tests","command":"make test"},{"name":"lint","command":"make lint","retry":"never"}]}`)

	var notes strings.Builder
	contracts, err := resolveVerification(&notes, repo, "")
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
	contracts, err := resolveVerification(&notes, repo, "go test ./...")
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

	if _, err := resolveVerification(&strings.Builder{}, repo, ""); err == nil || !strings.Contains(err.Error(), "invalid project manifest") {
		t.Fatalf("err = %v, want fail-closed manifest validation", err)
	}
}

func TestResolveVerificationNoManifestMeansNoContracts(t *testing.T) {
	contracts, err := resolveVerification(&strings.Builder{}, t.TempDir(), "")
	if err != nil || contracts != nil {
		t.Fatalf("contracts = %+v, err = %v; want none", contracts, err)
	}
}
