package broker

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestPolicyEnforcerReload(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy(), "github")

	if err := e.AuthorizeResource("claude", ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/repo-c"}); err == nil {
		t.Fatal("repo-c should not be allowed initially")
	}

	dir := t.TempDir()
	newPolicyPath := filepath.Join(dir, "policy.json")

	newPolicy := policy.PolicyFile{
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

	data, _ := json.MarshalIndent(newPolicy, "", "  ")
	if err := os.WriteFile(newPolicyPath, data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := e.Reload(newPolicyPath); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if err := e.AuthorizeResource("claude", ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/repo-c"}); err != nil {
		t.Errorf("after reload, repo-c should be allowed: %v", err)
	}
	if err := e.AuthorizeResource("claude", ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/repo-b"}); err == nil {
		t.Error("after reload, repo-b should not be allowed")
	}
}

func TestPolicyEnforcerReloadInvalid(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy(), "github")

	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badPath, []byte(`{"schema_version":"wrong"}`), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := e.Reload(badPath); err == nil {
		t.Fatal("expected error for invalid policy")
	}

	if err := e.AuthorizeResource("claude", ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/repo-a"}); err != nil {
		t.Error("original policy should still work after failed reload")
	}
}
