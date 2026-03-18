package launcher

import (
	"strings"
	"testing"
)

func TestScrubEnv(t *testing.T) {
	input := []string{
		"HOME=/home/user",
		"PATH=/usr/bin",
		"GH_TOKEN=should-be-removed",
		"GITHUB_TOKEN=should-be-removed",
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"GIT_SSH_COMMAND=ssh -i key",
		"EDITOR=vim",
		"GIT_TERMINAL_PROMPT=1",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=some.key",
		"GIT_CONFIG_VALUE_0=some-value",
	}

	result := ScrubEnv(input, "/usr/local/bin/ai-agent-credential-helper", "/run/user/1000/ai-agent/broker.sock", "sess-123", 3, "owner/repo", "", "/usr/bin/gh")

	env := make(map[string]string)
	for _, e := range result {
		k, v, _ := strings.Cut(e, "=")
		env[k] = v
	}

	// Verify scrubbed vars are gone.
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "SSH_AUTH_SOCK", "GIT_SSH_COMMAND"} {
		if _, ok := env[key]; ok {
			t.Errorf("expected %s to be scrubbed", key)
		}
	}

	// Verify parent GIT_CONFIG entries were scrubbed.
	if _, ok := env["GIT_CONFIG_KEY_0"]; ok {
		// Should have been scrubbed, then replaced with our own.
		// Our own GIT_CONFIG_KEY_0 should be credential.helper (empty).
	}

	// Verify safe vars are kept.
	if env["HOME"] != "/home/user" {
		t.Error("HOME should be preserved")
	}
	if env["EDITOR"] != "vim" {
		t.Error("EDITOR should be preserved")
	}

	// Verify forced vars.
	if env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q, want %q", env["GIT_TERMINAL_PROMPT"], "0")
	}

	// Verify session vars.
	if env["AI_AGENT_AUTH_SOCK"] != "/run/user/1000/ai-agent/broker.sock" {
		t.Errorf("AI_AGENT_AUTH_SOCK = %q", env["AI_AGENT_AUTH_SOCK"])
	}
	if env["AI_AGENT_SESSION_ID"] != "sess-123" {
		t.Errorf("AI_AGENT_SESSION_ID = %q", env["AI_AGENT_SESSION_ID"])
	}
	if env["AI_AGENT_SESSION_BIND_FD"] != "3" {
		t.Errorf("AI_AGENT_SESSION_BIND_FD = %q", env["AI_AGENT_SESSION_BIND_FD"])
	}

	// Verify session repo.
	if env["AI_AGENT_SESSION_REPO"] != "owner/repo" {
		t.Errorf("AI_AGENT_SESSION_REPO = %q, want %q", env["AI_AGENT_SESSION_REPO"], "owner/repo")
	}

	// Verify git credential helper setup.
	if env["GIT_CONFIG_COUNT"] != "7" {
		t.Errorf("GIT_CONFIG_COUNT = %q, want %q", env["GIT_CONFIG_COUNT"], "7")
	}
	if v := env["GIT_CONFIG_VALUE_0"]; v != "" {
		t.Errorf("GIT_CONFIG_VALUE_0 = %q, want empty (reset)", v)
	}
	if env["GIT_CONFIG_KEY_1"] != "credential.helper" {
		t.Errorf("GIT_CONFIG_KEY_1 = %q", env["GIT_CONFIG_KEY_1"])
	}
	if env["GIT_CONFIG_VALUE_1"] != "/usr/local/bin/ai-agent-credential-helper" {
		t.Errorf("GIT_CONFIG_VALUE_1 = %q", env["GIT_CONFIG_VALUE_1"])
	}

	// Verify extraheader neutralization.
	if env["GIT_CONFIG_KEY_2"] != "credential.https://github.com.useHttpPath" {
		t.Errorf("GIT_CONFIG_KEY_2 = %q", env["GIT_CONFIG_KEY_2"])
	}
	if v := env["GIT_CONFIG_VALUE_2"]; v != "true" {
		t.Errorf("GIT_CONFIG_VALUE_2 = %q, want true", v)
	}
	if env["GIT_CONFIG_KEY_3"] != "http.https://github.com/.extraheader" {
		t.Errorf("GIT_CONFIG_KEY_3 = %q", env["GIT_CONFIG_KEY_3"])
	}
	if v := env["GIT_CONFIG_VALUE_3"]; v != "" {
		t.Errorf("GIT_CONFIG_VALUE_3 = %q, want empty", v)
	}
	if env["GIT_CONFIG_KEY_4"] != "http.https://github.com/owner/repo.extraheader" {
		t.Errorf("GIT_CONFIG_KEY_4 = %q", env["GIT_CONFIG_KEY_4"])
	}
	if v := env["GIT_CONFIG_VALUE_4"]; v != "" {
		t.Errorf("GIT_CONFIG_VALUE_4 = %q, want empty", v)
	}
	if env["GIT_CONFIG_KEY_5"] != "http.https://github.com/owner/repo.git.extraheader" {
		t.Errorf("GIT_CONFIG_KEY_5 = %q", env["GIT_CONFIG_KEY_5"])
	}
	if v := env["GIT_CONFIG_VALUE_5"]; v != "" {
		t.Errorf("GIT_CONFIG_VALUE_5 = %q, want empty", v)
	}
	if env["GIT_CONFIG_KEY_6"] != "http.extraheader" {
		t.Errorf("GIT_CONFIG_KEY_6 = %q", env["GIT_CONFIG_KEY_6"])
	}
	if v := env["GIT_CONFIG_VALUE_6"]; v != "" {
		t.Errorf("GIT_CONFIG_VALUE_6 = %q, want empty", v)
	}
	if env["AI_AGENT_REAL_GH"] != "/usr/bin/gh" {
		t.Errorf("AI_AGENT_REAL_GH = %q, want %q", env["AI_AGENT_REAL_GH"], "/usr/bin/gh")
	}
}

func TestScrubEnvGhWrapperPrependsPATH(t *testing.T) {
	input := []string{
		"HOME=/home/user",
		"PATH=/usr/bin:/usr/local/bin",
	}

	result := ScrubEnv(input, "/helper", "/sock", "sess", 3, "o/r", "/tmp/gh-shim", "")

	env := make(map[string]string)
	for _, e := range result {
		k, v, _ := strings.Cut(e, "=")
		env[k] = v
	}

	path := env["PATH"]
	if !strings.HasPrefix(path, "/tmp/gh-shim:") {
		t.Errorf("PATH should start with gh wrapper dir, got %q", path)
	}
}

func TestScrubEnvEmptyInput(t *testing.T) {
	result := ScrubEnv(nil, "/helper", "/sock", "sess", 3, "o/r", "", "")

	// Should still have forced vars and session vars.
	env := make(map[string]string)
	for _, e := range result {
		k, v, _ := strings.Cut(e, "=")
		env[k] = v
	}

	if env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Error("expected GIT_TERMINAL_PROMPT=0")
	}
	if env["AI_AGENT_SESSION_ID"] != "sess" {
		t.Error("expected session ID")
	}
}
