package broker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

func testPolicy() *policy.PolicyFile {
	return &policy.PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				Resources: []string{"github:repo:owner/repo-a", "github:repo:owner/repo-b"},
				GitHub: &policy.GitHubAgentConfig{
					InstallationID:     42,
					DefaultPermissions: map[string]string{"contents": "write", "metadata": "read"},
				},
			},
		},
	}
}

func TestPolicyEnforcerGitHubConfig(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy())

	cfg, err := e.GitHubConfig("claude")
	if err != nil {
		t.Fatalf("GitHubConfig: %v", err)
	}
	if cfg.InstallationID != 42 {
		t.Errorf("InstallationID = %d, want 42", cfg.InstallationID)
	}
	if cfg.DefaultPermissions["contents"] != "write" {
		t.Errorf("contents = %q, want write", cfg.DefaultPermissions["contents"])
	}

	if _, err := e.GitHubConfig("unknown"); err == nil {
		t.Error("expected error for unknown agent")
	}
}

func TestPolicyEnforcerReload(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy())

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
				GitHub: &policy.GitHubAgentConfig{
					InstallationID:     42,
					DefaultPermissions: map[string]string{"contents": "write", "metadata": "read"},
				},
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
	e := NewPolicyEnforcer(testPolicy())

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
