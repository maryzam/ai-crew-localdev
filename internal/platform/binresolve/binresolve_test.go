package binresolve

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveOptionalPrefersSiblingBinary(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "ai-agent")
	helper := filepath.Join(dir, "ai-agent-credential-helper")
	writeExecutable(t, self)
	writeExecutable(t, helper)

	resolver := DefaultResolver()
	resolver.Executable = func() (string, error) { return self, nil }
	resolver.LookPath = func(string) (string, error) { return "/usr/bin/ai-agent-credential-helper", nil }

	got, err := resolver.ResolveOptional("ai-agent-credential-helper")
	if err != nil {
		t.Fatalf("ResolveOptional: %v", err)
	}
	if got != helper {
		t.Fatalf("ResolveOptional = %q, want %q", got, helper)
	}
}

func TestResolveOptionalFallsBackToPath(t *testing.T) {
	resolver := DefaultResolver()
	resolver.Executable = func() (string, error) { return "", os.ErrNotExist }
	resolver.LookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }

	got, err := resolver.ResolveOptional("ai-agent-gh")
	if err != nil {
		t.Fatalf("ResolveOptional: %v", err)
	}
	if got != "/usr/bin/ai-agent-gh" {
		t.Fatalf("ResolveOptional = %q", got)
	}
}

func TestResolveExecutableFromPathSkipsWrapper(t *testing.T) {
	dir := t.TempDir()
	wrapperBin := filepath.Join(dir, "ai-agent-gh")
	realDir := filepath.Join(dir, "real")
	shimDir := filepath.Join(dir, "shim")
	realGh := filepath.Join(realDir, "gh")
	shimGh := filepath.Join(shimDir, "gh")

	for _, path := range []string{realDir, shimDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable(t, wrapperBin)
	if err := os.Symlink(wrapperBin, shimGh); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, realGh)

	resolver := DefaultResolver()
	resolver.PathList = func() []string { return []string{shimDir, realDir} }

	got, err := resolver.ResolveExecutableFromPath("gh", wrapperBin)
	if err != nil {
		t.Fatalf("ResolveExecutableFromPath: %v", err)
	}
	if got != realGh {
		t.Fatalf("ResolveExecutableFromPath = %q, want %q", got, realGh)
	}
}

func TestResolveOptionalReportsMissingBinary(t *testing.T) {
	resolver := DefaultResolver()
	resolver.Executable = func() (string, error) { return "", os.ErrNotExist }
	resolver.LookPath = func(string) (string, error) { return "", errors.New("missing") }

	if _, err := resolver.ResolveOptional("ai-agent-gh"); err == nil {
		t.Fatal("ResolveOptional should fail for missing binary")
	}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
