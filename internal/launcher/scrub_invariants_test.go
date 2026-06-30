package launcher

import (
	"strings"
	"testing"
)

func TestScrubEnvRemovesEveryAmbientCredential(t *testing.T) {
	env := make([]string, 0, len(scrubbedEnvVars)+2)
	env = append(env, "HOME=/home/test", "PATH=/usr/bin")
	for _, v := range scrubbedEnvVars {
		env = append(env, v+"=ambient-value")
	}

	result := ScrubEnv(env, "/helper", "/sock", "sess-1", 3, "owner/repo", "", "")

	remaining := make(map[string]string)
	for _, e := range result {
		k, v, _ := strings.Cut(e, "=")
		remaining[k] = v
	}

	for _, v := range scrubbedEnvVars {
		if val, ok := remaining[v]; ok && val == "ambient-value" {
			t.Errorf("ScrubbedEnvVar %q was not removed from environment", v)
		}
	}
	for key, want := range map[string]string{"HOME": "/home/test", "PATH": "/usr/bin", "AI_AGENT_AUTH_SOCK": "/sock", "AI_AGENT_SESSION_ID": "sess-1", "AI_AGENT_SESSION_BIND_FD": "3", "AI_AGENT_SESSION_REPO": "owner/repo"} {
		if remaining[key] != want {
			t.Errorf("%s = %q, want %q", key, remaining[key], want)
		}
	}
}

func TestScrubEnvDisablesInteractiveGitCredentials(t *testing.T) {
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

func TestScrubEnvUsesOnlyBrokerCredentialHelper(t *testing.T) {
	env := []string{"HOME=/home/test", "PATH=/usr/bin"}
	result := ScrubEnv(env, "/usr/local/bin/ai-agent-credential-helper", "/sock", "sess", 3, "o/r", "", "")

	m := envMap(result)

	if m["GIT_CONFIG_KEY_0"] != "credential.helper" {
		t.Fatalf("GIT_CONFIG_KEY_0 = %q, want credential.helper", m["GIT_CONFIG_KEY_0"])
	}
	if m["GIT_CONFIG_VALUE_0"] != "" {
		t.Fatalf("GIT_CONFIG_VALUE_0 = %q, want empty (reset)", m["GIT_CONFIG_VALUE_0"])
	}

	if m["GIT_CONFIG_KEY_1"] != "credential.helper" {
		t.Fatalf("GIT_CONFIG_KEY_1 = %q, want credential.helper", m["GIT_CONFIG_KEY_1"])
	}
	if m["GIT_CONFIG_VALUE_1"] != "/usr/local/bin/ai-agent-credential-helper" {
		t.Fatalf("GIT_CONFIG_VALUE_1 = %q, want /usr/local/bin/ai-agent-credential-helper", m["GIT_CONFIG_VALUE_1"])
	}
	expected := map[string]string{
		"GIT_CONFIG_COUNT":   "7",
		"GIT_CONFIG_KEY_2":   "credential.https://github.com.useHttpPath",
		"GIT_CONFIG_VALUE_2": "true",
		"GIT_CONFIG_KEY_3":   "http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_3": "",
		"GIT_CONFIG_KEY_4":   "http.https://github.com/o/r.extraheader",
		"GIT_CONFIG_VALUE_4": "",
		"GIT_CONFIG_KEY_5":   "http.https://github.com/o/r.git.extraheader",
		"GIT_CONFIG_VALUE_5": "",
		"GIT_CONFIG_KEY_6":   "http.extraheader",
		"GIT_CONFIG_VALUE_6": "",
	}
	for key, want := range expected {
		if m[key] != want {
			t.Errorf("%s = %q, want %q", key, m[key], want)
		}
	}
}

func TestScrubEnvReplacesParentGitConfiguration(t *testing.T) {
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

	for _, e := range result {
		if strings.Contains(e, "=store") && strings.HasPrefix(e, "GIT_CONFIG_VALUE_") {
			t.Errorf("parent git config value leaked: %s", e)
		}
		if strings.Contains(e, "Authorization: token leaked") {
			t.Errorf("parent extraheader leaked: %s", e)
		}
	}

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
