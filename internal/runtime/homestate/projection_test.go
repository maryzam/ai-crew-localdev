package homestate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectionHidesPersonalCredentials(t *testing.T) {
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

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer func() { _ = projection.Cleanup() }()

	for _, hidden := range []string{".config/gh/hosts.yml", ".ssh/id_rsa", ".gitconfig"} {
		if _, err := os.Lstat(filepath.Join(projection.RunHome(), hidden)); err == nil {
			t.Fatalf("personal credential %s is reachable in the isolated home", hidden)
		}
	}
	for _, escaped := range []string{
		filepath.Join(projection.RunHome(), ".codex", "..", ".ssh", "id_rsa"),
		filepath.Join(projection.RunHome(), ".claude", "..", ".config", "gh", "hosts.yml"),
	} {
		if _, err := os.Stat(escaped); err == nil {
			t.Fatalf("personal credential is reachable through traversal path %s", escaped)
		}
	}
}

func TestProjectionPersistsExistingDirectoryState(t *testing.T) {
	realHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realHome, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realHome, ".claude", "login-state"), []byte("agent-login"), 0o600); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer func() { _ = projection.Cleanup() }()

	data, err := os.ReadFile(filepath.Join(projection.RunHome(), ".claude", "login-state"))
	if err != nil || string(data) != "agent-login" {
		t.Fatalf("agent login state unreachable through isolated home: %q, %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(projection.RunHome(), ".claude", "written-in-run"), []byte("persists"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(realHome, ".claude", "written-in-run")); !os.IsNotExist(err) {
		t.Fatalf("run state reached real home before commit (err %v)", err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(realHome, ".claude", "written-in-run")); err != nil || string(data) != "persists" {
		t.Fatalf("agent state written during the run must persist in the real home: %q, %v", data, err)
	}
}

func TestProjectionSupportsDirectoryFirstLogin(t *testing.T) {
	realHome := t.TempDir()

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer func() { _ = projection.Cleanup() }()

	nested := filepath.Join(projection.RunHome(), ".codex", "sessions")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("CLI-style MkdirAll during first login: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "auth.json"), []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(realHome, ".codex", "sessions", "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("first-login state reached real home before commit (err %v)", err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(realHome, ".codex", "sessions", "auth.json")); err != nil || string(data) != "token" {
		t.Fatalf("mkdir-based first login must land in the real home: %q, %v", data, err)
	}
}

func TestProjectionCopiesExistingFileStateIntoRunHome(t *testing.T) {
	realHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(realHome, ".claude.json"), []byte("old-state"), 0o600); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer func() { _ = projection.Cleanup() }()

	data, err := os.ReadFile(filepath.Join(projection.RunHome(), ".claude.json"))
	if err != nil || string(data) != "old-state" {
		t.Fatalf("copied file state = %q, %v", data, err)
	}
}

func TestProjectionCommitsAtomicFileReplacement(t *testing.T) {
	realHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(realHome, ".claude.json"), []byte("old-state"), 0o600); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	tmp := filepath.Join(projection.RunHome(), ".claude.json.tmp")
	if err := os.WriteFile(tmp, []byte("new-state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(projection.RunHome(), ".claude.json")); err != nil {
		t.Fatalf("CLI-style atomic save: %v", err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := projection.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if data, err := os.ReadFile(filepath.Join(realHome, ".claude.json")); err != nil || string(data) != "new-state" {
		t.Fatalf("atomic save must persist to the real home: %q, %v", data, err)
	}
	if _, err := os.Stat(projection.RunHome()); !os.IsNotExist(err) {
		t.Fatalf("isolated home not removed after cleanup (err %v)", err)
	}
}

func TestProjectionCommitsFileStateFromFailedRun(t *testing.T) {
	realHome := t.TempDir()

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projection.RunHome(), ".claude.json"), []byte("rotated-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := projection.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if data, err := os.ReadFile(filepath.Join(realHome, ".claude.json")); err != nil || string(data) != "rotated-token" {
		t.Fatalf("file state from failed run must persist: %q, %v", data, err)
	}
}

func TestProjectionCommitsDirectoryStateFromFailedRun(t *testing.T) {
	realHome := t.TempDir()

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projection.RunHome(), ".codex", "auth.json"), []byte("rotated-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := projection.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if data, err := os.ReadFile(filepath.Join(realHome, ".codex", "auth.json")); err != nil || string(data) != "rotated-token" {
		t.Fatalf("directory state from failed run must persist: %q, %v", data, err)
	}
}

func TestProjectionPreservesCodexStandaloneInstallWhenStateChanges(t *testing.T) {
	realHome := t.TempDir()
	release := filepath.Join(realHome, ".codex", "packages", "standalone", "releases", "0.143.0-x86_64-unknown-linux-musl")
	binary := filepath.Join(release, "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("codex"), 0o711); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(realHome, ".codex", "packages", "standalone", "current")
	if err := os.Symlink(release, current); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projection.RunHome(), ".codex", "packages")); !os.IsNotExist(err) {
		t.Fatalf("codex package cache must not be projected into the run home (err %v)", err)
	}
	if err := os.WriteFile(filepath.Join(projection.RunHome(), ".codex", "auth.json"), []byte("rotated-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if info, err := os.Stat(binary); err != nil {
		t.Fatalf("codex binary missing after commit: %v", err)
	} else if got := info.Mode().Perm(); got != 0o711 {
		t.Fatalf("codex binary mode = %o, want 711", got)
	}
	if target, err := os.Readlink(current); err != nil || target != release {
		t.Fatalf("codex current symlink = %q, %v", target, err)
	}
	if data, err := os.ReadFile(filepath.Join(realHome, ".codex", "auth.json")); err != nil || string(data) != "rotated-token" {
		t.Fatalf("codex auth state must persist: %q, %v", data, err)
	}
}

func TestProjectionPreservesOwnerExecuteOnClaudeStateScripts(t *testing.T) {
	realHome := t.TempDir()
	hook := filepath.Join(realHome, ".claude", "plugins", "example", "hooks", "session-start.sh")
	if err := os.MkdirAll(filepath.Dir(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if info, err := os.Stat(filepath.Join(projection.RunHome(), ".claude", "plugins", "example", "hooks", "session-start.sh")); err != nil {
		t.Fatalf("projected hook missing: %v", err)
	} else if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("projected hook mode = %o, want 700", got)
	}
	if err := os.WriteFile(filepath.Join(projection.RunHome(), ".claude", "login-state"), []byte("rotated-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if info, err := os.Stat(hook); err != nil {
		t.Fatalf("real hook missing after commit: %v", err)
	} else if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("real hook mode = %o, want 700", got)
	}
}

func TestProjectionDoesNotRewriteUnchangedFile(t *testing.T) {
	realHome := t.TempDir()
	realFile := filepath.Join(realHome, ".claude.json")
	if err := os.WriteFile(realFile, []byte("old-state"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(realFile)
	if err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	after, err := os.Stat(realFile)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("unchanged file was rewritten: before %s after %s", before.ModTime(), after.ModTime())
	}
}

func TestProjectionCommitsFileDeletion(t *testing.T) {
	realHome := t.TempDir()
	realFile := filepath.Join(realHome, ".claude.json")
	if err := os.WriteFile(realFile, []byte("old-state"), 0o600); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.Remove(filepath.Join(projection.RunHome(), ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if _, err := os.Stat(realFile); !os.IsNotExist(err) {
		t.Fatalf("deleted file state must be removed from real home (err %v)", err)
	}
}

func TestProjectionCommitsDirectoryDeletion(t *testing.T) {
	realHome := t.TempDir()
	realDir := filepath.Join(realHome, ".codex")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "auth.json"), []byte("old-state"), 0o600); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(projection.RunHome(), ".codex")); err != nil {
		t.Fatal(err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if _, err := os.Stat(realDir); !os.IsNotExist(err) {
		t.Fatalf("deleted directory state must be removed from real home (err %v)", err)
	}
}

func TestProjectionDoesNotExposeSourceSymlinkInDirectoryState(t *testing.T) {
	realHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realHome, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(realHome, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realHome, ".ssh", "id_rsa"), []byte("personal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(realHome, ".ssh", "id_rsa"), filepath.Join(realHome, ".codex", "ssh-link")); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := os.Stat(filepath.Join(projection.RunHome(), ".codex", "ssh-link")); !os.IsNotExist(err) {
		t.Fatalf("source symlink must not be projected into run home (err %v)", err)
	}
	if err := os.WriteFile(filepath.Join(projection.RunHome(), ".codex", "auth.json"), []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(realHome, ".codex", "ssh-link")); err != nil || target != filepath.Join(realHome, ".ssh", "id_rsa") {
		t.Fatalf("source symlink should remain in real home without becoming reachable through run home: %q, %v", target, err)
	}
}

func TestProjectionRejectsRunSymlinkInDirectoryState(t *testing.T) {
	realHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realHome, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(realHome, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realHome, ".ssh", "id_rsa"), []byte("personal"), 0o600); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.Symlink(filepath.Join(realHome, ".ssh", "id_rsa"), filepath.Join(projection.RunHome(), ".codex", "leak")); err != nil {
		t.Fatal(err)
	}

	if err := projection.Commit(); err == nil {
		t.Fatal("expected run symlink in projected directory state to fail closed")
	}
	if _, err := os.Stat(filepath.Join(realHome, ".codex", "leak")); !os.IsNotExist(err) {
		t.Fatalf("symlink state must not be committed to real home (err %v)", err)
	}
}

func TestProjectionRejectsDirectoryReplacementWithFile(t *testing.T) {
	realHome := t.TempDir()
	realDir := filepath.Join(realHome, ".codex")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "auth.json"), []byte("old-state"), 0o600); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(projection.RunHome(), ".codex")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projection.RunHome(), ".codex"), []byte("not-a-dir"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := projection.Commit(); err == nil {
		t.Fatal("expected directory replacement with file to fail closed")
	}
	if data, err := os.ReadFile(filepath.Join(realDir, "auth.json")); err != nil || string(data) != "old-state" {
		t.Fatalf("real directory state must remain unchanged after rejected replacement: %q, %v", data, err)
	}
}

func TestProjectionWarnsWhenRealFileDrifts(t *testing.T) {
	realHome := t.TempDir()
	realFile := filepath.Join(realHome, ".claude.json")
	if err := os.WriteFile(realFile, []byte("old-state"), 0o600); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := os.WriteFile(realFile, []byte("other-run"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projection.RunHome(), ".claude.json"), []byte("this-run"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := projection.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if len(projection.Warnings()) != 1 || !strings.Contains(projection.Warnings()[0], "changed in the real home") {
		t.Fatalf("warnings = %v", projection.Warnings())
	}
	if data, err := os.ReadFile(realFile); err != nil || string(data) != "this-run" {
		t.Fatalf("last writer should win after drift warning: %q, %v", data, err)
	}
}

func TestProjectionCleanupKeepsRealState(t *testing.T) {
	realHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realHome, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realHome, ".codex", "auth.json"), []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}

	projection, err := Prepare(realHome)
	if err != nil {
		t.Fatal(err)
	}
	if err := projection.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	if _, err := os.Stat(projection.RunHome()); !os.IsNotExist(err) {
		t.Fatalf("isolated home %s not removed (err %v)", projection.RunHome(), err)
	}
	if data, err := os.ReadFile(filepath.Join(realHome, ".codex", "auth.json")); err != nil || string(data) != "token" {
		t.Fatalf("cleanup must not touch real agent state: %q, %v", data, err)
	}
}

func TestApplyEnvRedirectsHomeAndXDG(t *testing.T) {
	env := []string{
		"HOME=/home/real",
		"XDG_CONFIG_HOME=/home/real/.config",
		"XDG_DATA_HOME=/home/real/.local/share",
		"XDG_STATE_HOME=/home/real/.local/state",
		"XDG_CACHE_HOME=/home/real/.cache",
		"PATH=/usr/bin",
	}

	result := ApplyEnv(env, "/tmp/run-home")

	joined := strings.Join(result, "\n")
	if strings.Contains(joined, "/home/real") {
		t.Fatalf("real home still reachable through env:\n%s", joined)
	}
	if EnvValue(result, "HOME") != "/tmp/run-home" {
		t.Fatalf("HOME = %q, want /tmp/run-home", EnvValue(result, "HOME"))
	}
	if EnvValue(result, "PATH") != "/usr/bin" {
		t.Fatalf("PATH = %q, unrelated env must survive", EnvValue(result, "PATH"))
	}
}
