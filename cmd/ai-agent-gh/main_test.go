package main

import (
	"os"
	"path/filepath"
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

	if got["GH_TOKEN"] || got["GITHUB_TOKEN"] || got["GH_HOST"] {
		t.Fatalf("scrubGhEnv did not remove all ambient gh auth vars: %v", got)
	}
	if !got["HOME"] || !got["PATH"] {
		t.Fatalf("scrubGhEnv removed safe vars: %v", got)
	}
}

func TestFindRealGh_ExplicitOverride(t *testing.T) {
	t.Setenv("AI_AGENT_REAL_GH", "/usr/bin/gh")
	got, err := findRealGh()
	if err != nil {
		t.Fatalf("findRealGh: %v", err)
	}
	if got != "/usr/bin/gh" {
		t.Fatalf("findRealGh = %q, want /usr/bin/gh", got)
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
