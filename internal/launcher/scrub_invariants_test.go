package launcher

import (
	"strings"
	"testing"
)

// Invariant: every variable listed in ScrubbedEnvVars must be removed from
// the environment after ScrubEnv. This ensures no ambient credential can
// bypass brokered auth.
func TestInvariant_AllAmbientCredentialsScrubbed(t *testing.T) {
	// Build an environment that sets every scrubbed var to a detectable value.
	env := make([]string, 0, len(ScrubbedEnvVars)+2)
	env = append(env, "HOME=/home/test", "PATH=/usr/bin")
	for _, v := range ScrubbedEnvVars {
		env = append(env, v+"=ambient-value")
	}

	result := ScrubEnv(env, "/helper", "/sock", "sess-1", 3, "owner/repo", "", "")

	remaining := make(map[string]string)
	for _, e := range result {
		k, v, _ := strings.Cut(e, "=")
		remaining[k] = v
	}

	for _, v := range ScrubbedEnvVars {
		if val, ok := remaining[v]; ok && val == "ambient-value" {
			t.Errorf("ScrubbedEnvVar %q was not removed from environment", v)
		}
	}
}

// Invariant: GIT_TERMINAL_PROMPT must always be "0" after ScrubEnv,
// regardless of what it was set to before. This forces git to fail closed
// instead of prompting the user when the broker is unavailable.
func TestInvariant_GitTerminalPromptAlwaysZero(t *testing.T) {
	tests := []struct {
		name string
		env  []string
	}{
		{"not set", []string{"HOME=/home/test"}},
		{"set to 1", []string{"HOME=/home/test", "GIT_TERMINAL_PROMPT=1"}},
		{"set to 0", []string{"HOME=/home/test", "GIT_TERMINAL_PROMPT=0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ScrubEnv(tt.env, "/helper", "/sock", "sess", 3, "o/r", "", "")
			env := envMap(result)
			if env["GIT_TERMINAL_PROMPT"] != "0" {
				t.Errorf("GIT_TERMINAL_PROMPT = %q, want %q", env["GIT_TERMINAL_PROMPT"], "0")
			}
		})
	}
}

// Invariant: ScrubEnv must clear any pre-existing credential.helper
// configuration via an empty value (GIT_CONFIG_VALUE_0) before setting
// the broker's credential helper. This ensures no cached or default
// helpers can supply credentials that bypass the broker.
func TestInvariant_CredentialHelperIsExclusive(t *testing.T) {
	env := []string{"HOME=/home/test", "PATH=/usr/bin"}
	result := ScrubEnv(env, "/usr/local/bin/ai-agent-credential-helper", "/sock", "sess", 3, "o/r", "", "")

	m := envMap(result)

	// First credential.helper entry must be empty (clears defaults).
	if m["GIT_CONFIG_KEY_0"] != "credential.helper" {
		t.Fatalf("GIT_CONFIG_KEY_0 = %q, want credential.helper", m["GIT_CONFIG_KEY_0"])
	}
	if m["GIT_CONFIG_VALUE_0"] != "" {
		t.Fatalf("GIT_CONFIG_VALUE_0 = %q, want empty (reset)", m["GIT_CONFIG_VALUE_0"])
	}

	// Second credential.helper entry must be our helper.
	if m["GIT_CONFIG_KEY_1"] != "credential.helper" {
		t.Fatalf("GIT_CONFIG_KEY_1 = %q, want credential.helper", m["GIT_CONFIG_KEY_1"])
	}
	if m["GIT_CONFIG_VALUE_1"] != "/usr/local/bin/ai-agent-credential-helper" {
		t.Fatalf("GIT_CONFIG_VALUE_1 = %q, want /usr/local/bin/ai-agent-credential-helper", m["GIT_CONFIG_VALUE_1"])
	}
}

// Invariant: any GIT_CONFIG_COUNT/KEY/VALUE chain inherited from the parent
// environment must be replaced, not merged. Leaking parent git config entries
// could re-enable credential helpers or extraheaders that bypass the broker.
func TestInvariant_NoParentGitConfigLeaks(t *testing.T) {
	env := []string{
		"HOME=/home/test",
		"PATH=/usr/bin",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=credential.helper",
		"GIT_CONFIG_VALUE_0=store",
		"GIT_CONFIG_KEY_1=http.extraheader",
		"GIT_CONFIG_VALUE_1=Authorization: token leaked",
	}

	result := ScrubEnv(env, "/helper", "/sock", "sess", 3, "o/r", "", "")
	m := envMap(result)

	// Parent's credential.helper=store must not survive.
	for _, e := range result {
		if strings.Contains(e, "=store") && strings.HasPrefix(e, "GIT_CONFIG_VALUE_") {
			t.Errorf("parent git config value leaked: %s", e)
		}
		if strings.Contains(e, "Authorization: token leaked") {
			t.Errorf("parent extraheader leaked: %s", e)
		}
	}

	// Our GIT_CONFIG_COUNT must be set (to "7" per current impl).
	if m["GIT_CONFIG_COUNT"] == "" {
		t.Fatal("GIT_CONFIG_COUNT not set after scrub")
	}
	if m["GIT_CONFIG_COUNT"] == "2" {
		t.Fatal("GIT_CONFIG_COUNT still has parent value")
	}
}

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}
