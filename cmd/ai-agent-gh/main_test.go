package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestExtractRepoFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "short flag with space", args: []string{"pr", "list", "-R", "owner/repo"}, want: "owner/repo"},
		{name: "short flag no space", args: []string{"pr", "list", "-Rowner/repo"}, want: "owner/repo"},
		{name: "long flag with space", args: []string{"pr", "list", "--repo", "owner/repo"}, want: "owner/repo"},
		{name: "long flag with equals", args: []string{"pr", "list", "--repo=owner/repo"}, want: "owner/repo"},
		{name: "no repo flag", args: []string{"pr", "list"}, want: ""},
		{name: "empty args", args: nil, want: ""},
		{name: "-R at end without value", args: []string{"pr", "list", "-R"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractRepoFlag(tt.args); got != tt.want {
				t.Fatalf("extractRepoFlag(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestScrubGhEnv(t *testing.T) {
	env := []string{
		"HOME=/home/user",
		"GH_TOKEN=secret",
		"GITHUB_TOKEN=secret2",
		"GH_ENTERPRISE_TOKEN=enterprise-secret",
		"GITHUB_ENTERPRISE_TOKEN=enterprise-secret2",
		"GH_HOST=enterprise.example.com",
		"PATH=/usr/bin",
	}

	result := scrubGhEnv(env)
	got := make(map[string]bool, len(result))
	for _, e := range result {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			got[e[:idx]] = true
		}
	}

	if got["GH_TOKEN"] || got["GITHUB_TOKEN"] || got["GH_ENTERPRISE_TOKEN"] || got["GITHUB_ENTERPRISE_TOKEN"] || got["GH_HOST"] {
		t.Fatalf("scrubGhEnv did not remove all ambient gh auth vars: %v", got)
	}
	if !got["HOME"] || !got["PATH"] {
		t.Fatalf("scrubGhEnv removed safe vars: %v", got)
	}
}

func TestRejectPersistentAuthCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "blocks auth login", args: []string{"auth", "login"}, wantErr: true},
		{name: "blocks auth setup git", args: []string{"auth", "setup-git"}, wantErr: true},
		{name: "blocks auth refresh", args: []string{"auth", "refresh"}, wantErr: true},
		{name: "allows auth status", args: []string{"auth", "status"}, wantErr: false},
		{name: "allows auth logout", args: []string{"auth", "logout"}, wantErr: false},
		{name: "allows auth switch", args: []string{"auth", "switch"}, wantErr: false},
		{name: "allows auth words in flag values", args: []string{"issue", "create", "--title", "auth", "login"}, wantErr: false},
		{name: "allows pr list", args: []string{"pr", "list"}, wantErr: false},
		{name: "allows empty args", args: nil, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectPersistentAuthCommand(tt.args)
			if tt.wantErr && err == nil {
				t.Fatal("expected persistent auth command to be rejected")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("rejectPersistentAuthCommand returned unexpected error: %v", err)
			}
			if err != nil && !strings.Contains(err.Error(), "GitHub repo access is brokered") {
				t.Fatalf("error %q does not explain brokered auth", err)
			}
		})
	}
}

func TestFindRealGh_ExplicitOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write gh: %v", err)
	}
	t.Setenv("AI_AGENT_REAL_GH", path)
	got, err := findRealGh()
	if err != nil {
		t.Fatalf("findRealGh: %v", err)
	}
	if got != path {
		t.Fatalf("findRealGh = %q, want %q", got, path)
	}
}

func TestFindRealGh_ExplicitOverrideMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-gh")
	t.Setenv("AI_AGENT_REAL_GH", path)

	if _, err := findRealGh(); err == nil {
		t.Fatal("expected error for missing AI_AGENT_REAL_GH")
	}
}

func TestFindRealGh_ExplicitOverrideNotExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write gh: %v", err)
	}
	t.Setenv("AI_AGENT_REAL_GH", path)

	if _, err := findRealGh(); err == nil {
		t.Fatal("expected error for non-executable AI_AGENT_REAL_GH")
	}
}

func TestFindRealGh_ExplicitOverrideRequiresExecutableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write gh: %v", err)
	}

	t.Setenv("AI_AGENT_REAL_GH", path)

	_, err := findRealGh()
	if err == nil {
		t.Fatal("expected error for non-executable AI_AGENT_REAL_GH")
	}
}

func TestFindRealGh_NotFound(t *testing.T) {
	t.Setenv("AI_AGENT_REAL_GH", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := findRealGh(); err == nil {
		t.Fatal("expected error when gh not found")
	}
}

func TestFindRealGh_SkipsShimSymlinkToSelf(t *testing.T) {
	t.Setenv("AI_AGENT_REAL_GH", "")

	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	shimDir := filepath.Join(dir, "shim")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir real dir: %v", err)
	}
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatalf("mkdir shim dir: %v", err)
	}

	realGh := filepath.Join(realDir, "gh")
	if err := os.WriteFile(realGh, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write real gh: %v", err)
	}

	self, err := os.Readlink("/proc/self/exe")
	if err != nil {
		t.Fatalf("read /proc/self/exe: %v", err)
	}
	if err := os.Symlink(self, filepath.Join(shimDir, "gh")); err != nil {
		t.Fatalf("create shim symlink: %v", err)
	}

	t.Setenv("PATH", shimDir+":"+realDir)

	got, err := findRealGh()
	if err != nil {
		t.Fatalf("findRealGh: %v", err)
	}
	if got != realGh {
		t.Fatalf("findRealGh = %q, want %q", got, realGh)
	}
}

func TestLoadManagedSession_FileBackedFD(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	fd := createUnlinkedBindFile(t, secret)

	env := map[string]string{
		"AI_AGENT_AUTH_SOCK":       "/run/ai-agent/broker.sock",
		"AI_AGENT_SESSION_ID":      "sess-123",
		"AI_AGENT_SESSION_BIND_FD": strconv.Itoa(fd),
	}

	getenv := func(key string) string { return env[key] }

	first, err := loadManagedSession(getenv)
	if err != nil {
		t.Fatalf("loadManagedSession(first): %v", err)
	}
	second, err := loadManagedSession(getenv)
	if err != nil {
		t.Fatalf("loadManagedSession(second): %v", err)
	}

	if first.SocketPath != env["AI_AGENT_AUTH_SOCK"] {
		t.Fatalf("SocketPath = %q, want %q", first.SocketPath, env["AI_AGENT_AUTH_SOCK"])
	}
	if first.SessionID != env["AI_AGENT_SESSION_ID"] {
		t.Fatalf("SessionID = %q, want %q", first.SessionID, env["AI_AGENT_SESSION_ID"])
	}
	if !bytes.Equal(first.BindSecret, secret) {
		t.Fatalf("first BindSecret = %q, want %q", first.BindSecret, secret)
	}
	if !bytes.Equal(second.BindSecret, secret) {
		t.Fatalf("second BindSecret = %q, want %q", second.BindSecret, secret)
	}
}

func createUnlinkedBindFile(t *testing.T, secret []byte) int {
	t.Helper()

	f, err := os.CreateTemp(t.TempDir(), "bind-secret-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	if _, err := f.Write(secret); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if err := os.Remove(f.Name()); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	return int(f.Fd())
}
