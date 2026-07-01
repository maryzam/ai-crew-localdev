package broker

import (
	"encoding/json"
	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

func testGithubSection() json.RawMessage {
	out, _ := json.Marshal(map[string]any{
		"installation_id":     42,
		"default_permissions": map[string]string{"contents": "write", "metadata": "read"},
	})
	return out
}

func testPolicy() *policy.PolicyFile {
	return &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				Resources: []string{"github:repo:owner/repo-a", "github:repo:owner/repo-b"},
				Providers: map[string]json.RawMessage{"github": testGithubSection()},
			},
		},
	}
}

func TestPolicyEnforcerProviderSection(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy(), "github")

	section, ok := e.ProviderSection("claude", "github")
	if !ok {
		t.Fatal("expected providers.github section to be present")
	}
	if !json.Valid(section) {
		t.Errorf("section is not valid JSON: %s", section)
	}

	if _, ok := e.ProviderSection("unknown", "github"); ok {
		t.Error("unknown agent should return ok=false")
	}
	if _, ok := e.ProviderSection("claude", "aws"); ok {
		t.Error("unknown provider should return ok=false")
	}
}

func TestPolicyEnforcerSetPolicy(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy(), "github")

	if err := e.AuthorizeResource("claude", brokerapi.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/repo-c"}); err == nil {
		t.Fatal("repo-c should not be allowed initially")
	}

	updated := &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				Resources: []string{"github:repo:owner/repo-a", "github:repo:owner/repo-c"},
				Providers: map[string]json.RawMessage{"github": testGithubSection()},
			},
		},
	}
	e.SetPolicy(updated)

	if err := e.AuthorizeResource("claude", brokerapi.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/repo-c"}); err != nil {
		t.Errorf("after SetPolicy, repo-c should be allowed: %v", err)
	}
	if err := e.AuthorizeResource("claude", brokerapi.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/repo-b"}); err == nil {
		t.Error("after SetPolicy, repo-b should not be allowed")
	}
}
