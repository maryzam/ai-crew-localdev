package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDir_EnvOverride(t *testing.T) {
	t.Setenv("AI_AGENT_CONFIG_DIR", "/tmp/custom-config")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")

	got := ConfigDir()
	if got != "/tmp/custom-config" {
		t.Errorf("ConfigDir() = %q, want %q", got, "/tmp/custom-config")
	}
}

func TestConfigDir_XDGFallback(t *testing.T) {
	t.Setenv("AI_AGENT_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")

	got := ConfigDir()
	want := "/tmp/xdg/ai-agent"
	if got != want {
		t.Errorf("ConfigDir() = %q, want %q", got, want)
	}
}

func TestConfigDir_DefaultFallback(t *testing.T) {
	t.Setenv("AI_AGENT_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	got := ConfigDir()
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "ai-agent")
	if got != want {
		t.Errorf("ConfigDir() = %q, want %q", got, want)
	}
}

func TestRuntimeDir_XDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	got := RuntimeDir()
	want := "/run/user/1000/ai-agent"
	if got != want {
		t.Errorf("RuntimeDir() = %q, want %q", got, want)
	}
}

func TestRuntimeDir_Fallback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")

	got := RuntimeDir()
	if !strings.HasPrefix(got, "/run/user/") || !strings.HasSuffix(got, "/ai-agent") {
		t.Errorf("RuntimeDir() = %q, expected /run/user/<uid>/ai-agent pattern", got)
	}
}

func TestRuntimeBaseDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got := RuntimeBaseDir(); got != "/run/user/1000" {
		t.Fatalf("RuntimeBaseDir() = %q, want %q", got, "/run/user/1000")
	}

	t.Setenv("XDG_RUNTIME_DIR", "")
	got := RuntimeBaseDir()
	if !strings.HasPrefix(got, "/run/user/") {
		t.Fatalf("RuntimeBaseDir() = %q, want /run/user/<uid>", got)
	}
}

func TestDefaultPolicyPath(t *testing.T) {
	t.Setenv("AI_AGENT_CONFIG_DIR", "/tmp/test-config")

	got := DefaultPolicyPath()
	want := "/tmp/test-config/policy.json"
	if got != want {
		t.Errorf("DefaultPolicyPath() = %q, want %q", got, want)
	}
}

func TestDefaultIdentitiesPath(t *testing.T) {
	t.Setenv("AI_AGENT_CONFIG_DIR", "/tmp/test-config")

	got := DefaultIdentitiesPath()
	want := "/tmp/test-config/identities.json"
	if got != want {
		t.Errorf("DefaultIdentitiesPath() = %q, want %q", got, want)
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input string
		want  string
	}{
		{"~/foo/bar", filepath.Join(home, "foo/bar")},
		{"~", home},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~other", "~other"}, // should not expand ~otheruser
	}

	for _, tc := range tests {
		got := ExpandHome(tc.input)
		if got != tc.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
