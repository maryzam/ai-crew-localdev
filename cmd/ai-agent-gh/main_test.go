package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractRepoFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "short flag with space",
			args: []string{"pr", "list", "-R", "owner/repo"},
			want: "owner/repo",
		},
		{
			name: "short flag no space",
			args: []string{"pr", "list", "-Rowner/repo"},
			want: "owner/repo",
		},
		{
			name: "long flag with space",
			args: []string{"pr", "list", "--repo", "owner/repo"},
			want: "owner/repo",
		},
		{
			name: "long flag with equals",
			args: []string{"pr", "list", "--repo=owner/repo"},
			want: "owner/repo",
		},
		{
			name: "no repo flag",
			args: []string{"pr", "list"},
			want: "",
		},
		{
			name: "empty args",
			args: nil,
			want: "",
		},
		{
			name: "-R at end without value",
			args: []string{"pr", "list", "-R"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRepoFlag(tt.args)
			if got != tt.want {
				t.Errorf("extractRepoFlag(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestScrubGhEnv(t *testing.T) {
	env := []string{
		"HOME=/home/user",
		"GH_TOKEN=secret",
		"GITHUB_TOKEN=secret2",
		"GH_HOST=example.com",
		"PATH=/usr/bin",
	}

	result := scrubGhEnv(env)

	got := make(map[string]bool)
	for _, e := range result {
		idx := 0
		for i := range e {
			if e[i] == '=' {
				idx = i
				break
			}
		}
		got[e[:idx]] = true
	}

	if got["GH_TOKEN"] {
		t.Error("GH_TOKEN should be scrubbed")
	}
	if got["GITHUB_TOKEN"] {
		t.Error("GITHUB_TOKEN should be scrubbed")
	}
	if got["GH_HOST"] {
		t.Error("GH_HOST should be scrubbed")
	}
	if !got["HOME"] {
		t.Error("HOME should be preserved")
	}
	if !got["PATH"] {
		t.Error("PATH should be preserved")
	}
}

func TestFindRealGh_ExplicitOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("stub"), 0755); err != nil {
		t.Fatalf("write gh: %v", err)
	}
	t.Setenv("AI_AGENT_REAL_GH", path)
	got, err := findRealGh()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != path {
		t.Errorf("got %q, want %q", got, path)
	}
}

func TestFindRealGh_ExplicitOverrideMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-gh")
	t.Setenv("AI_AGENT_REAL_GH", path)

	_, err := findRealGh()
	if err == nil {
		t.Fatal("expected error for missing AI_AGENT_REAL_GH")
	}
}

func TestFindRealGh_ExplicitOverrideNotExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(path, []byte("stub"), 0644); err != nil {
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
	t.Setenv("PATH", t.TempDir()) // empty dir, no gh binary
	_, err := findRealGh()
	if err == nil {
		t.Fatal("expected error when gh not found")
	}
}

func TestFindRealGh_SkipsShimSymlinkToSelf(t *testing.T) {
	t.Setenv("AI_AGENT_REAL_GH", "")

	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	shimDir := filepath.Join(dir, "shim")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("mkdir real dir: %v", err)
	}
	if err := os.MkdirAll(shimDir, 0755); err != nil {
		t.Fatalf("mkdir shim dir: %v", err)
	}

	realGh := filepath.Join(realDir, "gh")
	if err := os.WriteFile(realGh, []byte("stub"), 0755); err != nil {
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
