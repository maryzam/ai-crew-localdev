package policycheck

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
)

func TestValidateAcceptsProviderPolicy(t *testing.T) {
	if err := Validate(validPolicy(t), validIdentities()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsProviderConfig(t *testing.T) {
	policyFile := validPolicy(t)
	policyFile.Agents["codex"].Providers["github"] = rawSection(t, map[string]any{
		"installation_id":     0,
		"default_permissions": map[string]string{"contents": "read"},
	})

	err := Validate(policyFile, validIdentities())
	if err == nil {
		t.Fatal("expected provider config error")
	}
	if !strings.Contains(err.Error(), "installation_id must be > 0") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsProviderResourceGrammar(t *testing.T) {
	policyFile := validPolicy(t)
	policyFile.Agents["codex"] = policy.AgentPolicy{
		Resources: []string{"github:issue:example-org/example-repo"},
		Providers: map[string]json.RawMessage{
			"github": rawSection(t, map[string]any{
				"installation_id":     42,
				"default_permissions": map[string]string{"contents": "read"},
			}),
		},
	}

	err := Validate(policyFile, validIdentities())
	if err == nil {
		t.Fatal("expected resource grammar error")
	}
	if !strings.Contains(err.Error(), `resource kind "issue" is not supported`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func validPolicy(t *testing.T) *policy.PolicyFile {
	t.Helper()
	return &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"codex": {
				Resources: []string{"github:repo:example-org/example-repo"},
				Providers: map[string]json.RawMessage{
					"github": rawSection(t, map[string]any{
						"installation_id":     42,
						"default_permissions": map[string]string{"contents": "read"},
					}),
				},
			},
		},
	}
}

func validIdentities() *identity.IdentitiesFile {
	return &identity.IdentitiesFile{
		SchemaVersion: schema.IdentitiesSchemaV2,
		Agents: map[string]identity.AgentIdentity{
			"codex": {
				GitName:    "Codex Bot",
				GitEmail:   "codex@example.invalid",
				GithubHost: "github.com",
				AppID:      "fake-app-id",
				AppKey:     "fake-key",
				Tool:       "codex",
				Model:      "gpt-5-codex",
			},
		},
	}
}

func rawSection(t *testing.T, body map[string]any) json.RawMessage {
	t.Helper()
	out, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal provider section: %v", err)
	}
	return out
}
