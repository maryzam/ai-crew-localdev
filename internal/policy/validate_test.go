package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

func loadTestPolicy(t *testing.T, name string) *PolicyFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	f, err := ParsePolicy(data)
	if err != nil {
		t.Fatalf("parse testdata/%s: %v", name, err)
	}
	return f
}

func githubSection(t *testing.T, body map[string]any) json.RawMessage {
	t.Helper()
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal github section: %v", err)
	}
	return out
}

func TestValidateAcceptsValidPolicy(t *testing.T) {
	f := loadTestPolicy(t, "valid_policy.json")
	r := Validate(f)
	if r.Errors.HasErrors() {
		t.Fatalf("unexpected errors: %v", r.Errors)
	}
}

func TestValidateRejectsMissingAgents(t *testing.T) {
	f := loadTestPolicy(t, "invalid_missing_agents.json")
	r := Validate(f)
	if !r.Errors.HasErrors() {
		t.Fatal("expected error for missing agents")
	}
	if !hasError(r.Errors, "agents") {
		t.Errorf("expected error on agents, got: %v", r.Errors)
	}
}

func TestValidateRejectsGithubResourceWithoutProviderSection(t *testing.T) {
	f := &PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]AgentPolicy{
			"codex": {
				Resources: []string{"github:repo:owner/repo"},
			},
		},
	}
	r := Validate(f)
	if !hasError(r.Errors, "agents.codex.providers.github") {
		t.Errorf("expected error on agents.codex.providers.github, got: %v", r.Errors)
	}
}

func TestValidateTable(t *testing.T) {
	gh := githubSection(t, map[string]any{
		"installation_id":     1,
		"default_permissions": map[string]string{"contents": "write"},
	})

	cases := []struct {
		name       string
		policy     PolicyFile
		wantErrors bool
		wantField  string
	}{
		{
			name: "valid",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaCurrent,
				DefaultSessionTTL:  "8h",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						Resources: []string{"github:repo:owner/repo"},
						Providers: map[string]json.RawMessage{"github": gh},
					},
				},
			},
		},
		{
			name: "missing agents",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaCurrent,
				DefaultSessionTTL:  "8h",
				DefaultIdleTimeout: "1h",
				Agents:             map[string]AgentPolicy{},
			},
			wantErrors: true,
			wantField:  "agents",
		},
		{
			name: "no resources",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaCurrent,
				DefaultSessionTTL:  "8h",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						Providers: map[string]json.RawMessage{"github": gh},
					},
				},
			},
			wantErrors: true,
			wantField:  "agents.codex.resources",
		},
		{
			name: "invalid resource uri",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaCurrent,
				DefaultSessionTTL:  "8h",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						Resources: []string{"github:repo"},
						Providers: map[string]json.RawMessage{"github": gh},
					},
				},
			},
			wantErrors: true,
			wantField:  "agents.codex.resources[0]",
		},
		{
			name: "missing session TTL",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaCurrent,
				DefaultSessionTTL:  "",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						Resources: []string{"github:repo:owner/repo"},
						Providers: map[string]json.RawMessage{"github": gh},
					},
				},
			},
			wantErrors: true,
			wantField:  "default_session_ttl",
		},
		{
			name: "unparseable duration",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaCurrent,
				DefaultSessionTTL:  "forever",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						Resources: []string{"github:repo:owner/repo"},
						Providers: map[string]json.RawMessage{"github": gh},
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
						Resources: []string{"github:repo:owner/repo"},
						Providers: map[string]json.RawMessage{"github": gh},
					},
				},
			},
			wantErrors: true,
			wantField:  "schema_version",
		},
		{
			name: "github resource without providers.github",
			policy: PolicyFile{
				SchemaVersion:      schema.PolicySchemaCurrent,
				DefaultSessionTTL:  "8h",
				DefaultIdleTimeout: "1h",
				Agents: map[string]AgentPolicy{
					"codex": {
						Resources: []string{"github:repo:owner/repo"},
					},
				},
			},
			wantErrors: true,
			wantField:  "agents.codex.providers.github",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Validate(&tc.policy)
			if tc.wantErrors && !r.Errors.HasErrors() {
				t.Fatal("expected errors but got none")
			}
			if !tc.wantErrors && r.Errors.HasErrors() {
				t.Fatalf("unexpected errors: %v", r.Errors)
			}
			if tc.wantField != "" && !hasError(r.Errors, tc.wantField) {
				t.Errorf("expected error on %q, got: %v", tc.wantField, r.Errors)
			}
		})
	}
}

func hasError(errs schema.ValidationErrors, field string) bool {
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}
