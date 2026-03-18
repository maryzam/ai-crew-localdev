package cli

import (
	"os"
	"path/filepath"
	"testing"
)

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
