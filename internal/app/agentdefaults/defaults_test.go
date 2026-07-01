package agentdefaults

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallCreatesDefaultsAndPreservesExistingFiles(t *testing.T) {
	home := t.TempDir()
	existing := filepath.Join(home, ".codex", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(existing), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("custom\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Install(home)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(result.Installed) != 3 || len(result.Skipped) != 1 {
		t.Fatalf("result = %#v", result)
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "custom\n" {
		t.Fatalf("existing guidance overwritten: %q", data)
	}

	for _, path := range result.Installed {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %s = %o", path, info.Mode().Perm())
		}
	}

	second, err := Install(home)
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if len(second.Installed) != 0 || len(second.Skipped) != 4 {
		t.Fatalf("second result = %#v", second)
	}
}

func TestInstallRejectsRelativeHome(t *testing.T) {
	if _, err := Install("relative"); err == nil {
		t.Fatal("expected relative home to fail")
	}
}
