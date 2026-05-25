package github

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	resolver := func(agent string) string {
		if agent == "claude" {
			return "id-from-identity"
		}
		return ""
	}

	cases := []struct {
		name    string
		agent   string
		section string
		wantErr string
		assert  func(t *testing.T, c Config)
	}{
		{
			name:    "valid with explicit app_id",
			agent:   "claude",
			section: `{"installation_id": 42, "app_id": "explicit", "default_permissions": {"contents": "write"}}`,
			assert: func(t *testing.T, c Config) {
				if c.InstallationID != 42 || c.AppID != "explicit" {
					t.Errorf("got %+v", c)
				}
			},
		},
		{
			name:    "valid falls back to identity resolver",
			agent:   "claude",
			section: `{"installation_id": 7, "default_permissions": {"metadata": "read"}}`,
			assert: func(t *testing.T, c Config) {
				if c.AppID != "id-from-identity" {
					t.Errorf("AppID = %q, want id-from-identity", c.AppID)
				}
			},
		},
		{name: "missing section", agent: "claude", section: "", wantErr: "missing providers.github"},
		{name: "null section", agent: "claude", section: "null", wantErr: "missing providers.github"},
		{name: "malformed json", agent: "claude", section: "{not-json", wantErr: "providers.github"},
		{name: "zero installation_id", agent: "claude", section: `{"installation_id": 0, "default_permissions": {"x": "read"}}`, wantErr: "installation_id must be > 0"},
		{name: "negative installation_id", agent: "claude", section: `{"installation_id": -1, "default_permissions": {"x": "read"}}`, wantErr: "installation_id must be > 0"},
		{name: "empty permissions", agent: "claude", section: `{"installation_id": 1, "default_permissions": {}}`, wantErr: "default_permissions must not be empty"},
		{name: "invalid permission level", agent: "claude", section: `{"installation_id": 1, "default_permissions": {"contents": "owner"}}`, wantErr: "invalid level"},
		{name: "unknown permission key typo", agent: "claude", section: `{"installation_id": 1, "default_permissions": {"pull_request": "write"}}`, wantErr: "unknown permission key"},
		{name: "unknown permission key arbitrary", agent: "claude", section: `{"installation_id": 1, "default_permissions": {"fictional_perm": "read"}}`, wantErr: "unknown permission key"},
		{name: "no app_id and resolver returns empty", agent: "unknown-agent", section: `{"installation_id": 1, "default_permissions": {"contents": "read"}}`, wantErr: "app_id not set"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseConfig(tc.agent, json.RawMessage(tc.section), resolver)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.assert != nil {
				tc.assert(t, cfg)
			}
		})
	}
}

func TestParseConfigAcceptsDocumentedInstallationTokenPermissionKeys(t *testing.T) {
	resolver := func(string) string { return "id-from-identity" }
	documentedKeys := []string{
		"actions",
		"administration",
		"checks",
		"code_quality",
		"codespaces",
		"contents",
		"dependabot_secrets",
		"deployments",
		"discussions",
		"environments",
		"issues",
		"merge_queues",
		"metadata",
		"packages",
		"pages",
		"pull_requests",
		"repository_custom_properties",
		"repository_hooks",
		"repository_projects",
		"secret_scanning_alerts",
		"secrets",
		"security_events",
		"single_file",
		"statuses",
		"vulnerability_alerts",
		"workflows",
	}

	for _, key := range documentedKeys {
		t.Run(key, func(t *testing.T) {
			section, err := json.Marshal(map[string]any{
				"installation_id": 42,
				"default_permissions": map[string]string{
					key: "write",
				},
			})
			if err != nil {
				t.Fatalf("marshal section: %v", err)
			}

			if _, err := parseConfig("claude", section, resolver); err != nil {
				t.Fatalf("parseConfig rejected documented permission key %q: %v", key, err)
			}
		})
	}
}
