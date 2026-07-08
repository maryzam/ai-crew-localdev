package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolatedHomeHidesPersonalCredentials(t *testing.T) {
	realHome := t.TempDir()
	for _, planted := range []string{
		filepath.Join(realHome, ".config", "gh", "hosts.yml"),
		filepath.Join(realHome, ".ssh", "id_rsa"),
		filepath.Join(realHome, ".gitconfig"),
	} {
		if err := os.MkdirAll(filepath.Dir(planted), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(planted, []byte("personal-credential"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(realHome, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realHome, ".claude", "login-state"), []byte("agent-login"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir, cleanup, err := prepareIsolatedHome(realHome)
	if err != nil {
		t.Fatalf("prepareIsolatedHome: %v", err)
	}
	defer cleanup()

	for _, hidden := range []string{".config/gh/hosts.yml", ".ssh/id_rsa", ".gitconfig"} {
		if _, err := os.Lstat(filepath.Join(dir, hidden)); err == nil {
			t.Errorf("personal credential %s is reachable in the isolated home", hidden)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, ".claude", "login-state"))
	if err != nil || string(data) != "agent-login" {
		t.Fatalf("agent login state unreachable through isolated home: %q, %v", data, err)
	}

	if err := os.WriteFile(filepath.Join(dir, ".claude", "written-in-run"), []byte("persists"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(realHome, ".claude", "written-in-run")); err != nil || string(data) != "persists" {
		t.Fatalf("agent state written during the run must persist in the real home: %q, %v", data, err)
	}
}

func TestIsolatedHomeSupportsFirstLoginThroughDanglingLinks(t *testing.T) {
	realHome := t.TempDir()

	dir, cleanup, err := prepareIsolatedHome(realHome)
	if err != nil {
		t.Fatalf("prepareIsolatedHome: %v", err)
	}
	defer cleanup()

	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte("first-login"), 0o600); err != nil {
		t.Fatalf("first login write through dangling link: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(realHome, ".claude.json")); err != nil || string(data) != "first-login" {
		t.Fatalf("first login state must land in the real home: %q, %v", data, err)
	}
}

func TestIsolatedHomeCleanupKeepsRealState(t *testing.T) {
	realHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realHome, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realHome, ".codex", "auth.json"), []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir, cleanup, err := prepareIsolatedHome(realHome)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("isolated home %s not removed (err %v)", dir, err)
	}
	if data, err := os.ReadFile(filepath.Join(realHome, ".codex", "auth.json")); err != nil || string(data) != "token" {
		t.Fatalf("cleanup must not touch real agent state: %q, %v", data, err)
	}
}

func TestApplyIsolatedHomeRedirectsHomeAndXDG(t *testing.T) {
	env := []string{
		"HOME=/home/real",
		"XDG_CONFIG_HOME=/home/real/.config",
		"XDG_DATA_HOME=/home/real/.local/share",
		"XDG_STATE_HOME=/home/real/.local/state",
		"XDG_CACHE_HOME=/home/real/.cache",
		"PATH=/usr/bin",
	}

	result := applyIsolatedHome(env, "/tmp/run-home")

	joined := strings.Join(result, "\n")
	if strings.Contains(joined, "/home/real") {
		t.Fatalf("real home still reachable through env:\n%s", joined)
	}
	if envValue(result, "HOME") != "/tmp/run-home" {
		t.Fatalf("HOME = %q, want /tmp/run-home", envValue(result, "HOME"))
	}
	if envValue(result, "PATH") != "/usr/bin" {
		t.Fatalf("PATH = %q, unrelated env must survive", envValue(result, "PATH"))
	}
}
