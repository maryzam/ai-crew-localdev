package policy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

func loadTestPolicy(t *testing.T, name string) *PolicyFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("failed to read testdata/%s: %v", name, err)
	}
	f, err := ParsePolicy(data)
	if err != nil {
		t.Fatalf("failed to parse testdata/%s: %v", name, err)
	}
	return f
}

func TestValidate_ValidPolicy(t *testing.T) {
	f := loadTestPolicy(t, "valid_policy.json")
	result := Validate(f)
	if result.Errors.HasErrors() {
		t.Errorf("expected no errors for valid policy, got: %v", result.Errors)
	}
}

func TestValidate_MissingAgents(t *testing.T) {
	f := loadTestPolicy(t, "invalid_missing_agents.json")
	result := Validate(f)
	if !result.Errors.HasErrors() {
		t.Error("expected errors for missing agents")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "agents" {
			found = true
		}
	}
	if !found {
		t.Error("expected an error on 'agents' field")
	}
}

func TestValidate_BadPermission(t *testing.T) {
	f := loadTestPolicy(t, "invalid_bad_permission.json")
	result := Validate(f)
	if !result.Errors.HasErrors() {
		t.Error("expected errors for bad permission value")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "agents.codex.default_permissions.contents" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on contents permission, got: %v", result.Errors)
	}
}

func TestValidate_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		policy     PolicyFile
		wantErrors bool
		wantField  string
	}{
		{
			name: "valid policy",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaV1,
				DefaultSessionTTL:  "8h",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						AllowedRepos:       []string{"owner/repo"},
						DefaultPermissions: map[string]string{"contents": "write"},
					},
				},
			},
			wantErrors: false,
		},
		{
			name: "missing agents",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaV1,
				DefaultSessionTTL:  "8h",
				DefaultIdleTimeout: "1h",
				Agents:             map[string]AgentPolicy{},
			},
			wantErrors: true,
			wantField:  "agents",
		},
		{
			name: "bad permission value",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaV1,
				DefaultSessionTTL:  "8h",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						AllowedRepos:       []string{"owner/repo"},
						DefaultPermissions: map[string]string{"contents": "superuser"},
					},
				},
			},
			wantErrors: true,
			wantField:  "agents.codex.default_permissions.contents",
		},
		{
			name: "invalid repo slug",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaV1,
				DefaultSessionTTL:  "8h",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						AllowedRepos:       []string{"not a valid slug!"},
						DefaultPermissions: map[string]string{"contents": "write"},
					},
				},
			},
			wantErrors: true,
			wantField:  "agents.codex.allowed_repos",
		},
		{
			name: "missing session TTL",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaV1,
				DefaultSessionTTL:  "",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						AllowedRepos:       []string{},
						DefaultPermissions: map[string]string{"contents": "write"},
					},
				},
			},
			wantErrors: true,
			wantField:  "default_session_ttl",
		},
		{
			name: "unparseable duration",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaV1,
				DefaultSessionTTL:  "forever",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						AllowedRepos:       []string{},
						DefaultPermissions: map[string]string{"contents": "write"},
					},
				},
			},
			wantErrors: true,
			wantField:  "default_session_ttl",
		},
		{
			name: "wrong schema version",
			policy: PolicyFile{
				SchemaVersion:      "wrong/v99",
				DefaultSessionTTL:  "8h",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						AllowedRepos:       []string{},
						DefaultPermissions: map[string]string{"contents": "write"},
					},
				},
			},
			wantErrors: true,
			wantField:  "schema_version",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Validate(&tc.policy)
			if tc.wantErrors && !result.Errors.HasErrors() {
				t.Error("expected validation errors but got none")
			}
			if !tc.wantErrors && result.Errors.HasErrors() {
				t.Errorf("expected no errors, got: %v", result.Errors)
			}
			if tc.wantField != "" {
				found := false
				for _, e := range result.Errors {
					if e.Field == tc.wantField {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error on field %q, got errors: %v", tc.wantField, result.Errors)
				}
			}
		})
	}
}
