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
		{name: "no app_id and resolver returns empty", agent: "unknown-agent", section: `{"installation_id": 1, "default_permissions": {"x": "read"}}`, wantErr: "app_id not set"},
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
