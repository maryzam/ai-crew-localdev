package broker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

func testPolicy() *policy.PolicyFile {
	instID := int64(42)
	return &policy.PolicyFile{
		SchemaVersion:      "ai-agent-policy/v1",
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				AllowedRepos:       []string{"owner/repo-a", "owner/repo-b"},
				InstallationID:     &instID,
				DefaultPermissions: map[string]string{"contents": "write", "metadata": "read"},
			},
		},
	}
}

func TestPolicyEnforcerAuthorize(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy())

	tests := []struct {
		name    string
		agent   string
		repo    string
		perms   map[string]string
		wantErr bool
	}{
		{"allowed repo", "claude", "owner/repo-a", nil, false},
		{"another allowed repo", "claude", "owner/repo-b", nil, false},
		{"disallowed repo", "claude", "owner/repo-c", nil, true},
		{"unknown agent", "codex", "owner/repo-a", nil, true},
		{"valid permissions", "claude", "owner/repo-a",
			map[string]string{"metadata": "read"}, false},
		{"permission escalation", "claude", "owner/repo-a",
			map[string]string{"metadata": "admin"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := e.Authorize(tt.agent, tt.repo, tt.perms)
			if (err != nil) != tt.wantErr {
				t.Errorf("Authorize error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPolicyEnforcerInstallationID(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy())

	id, err := e.InstallationID("claude")
	if err != nil {
		t.Fatalf("InstallationID: %v", err)
	}
	if id != 42 {
		t.Errorf("InstallationID = %d, want 42", id)
	}

	_, err = e.InstallationID("unknown")
	if err == nil {
		t.Error("expected error for unknown agent")
	}
}

func TestPolicyEnforcerDefaultPermissions(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy())

	perms, err := e.DefaultPermissions("claude")
	if err != nil {
		t.Fatalf("DefaultPermissions: %v", err)
	}
	if perms["contents"] != "write" {
		t.Errorf("contents = %q, want write", perms["contents"])
	}
}

func TestPolicyEnforcerReload(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy())

	// Initially, repo-c is not allowed.
	if err := e.Authorize("claude", "owner/repo-c", nil); err == nil {
		t.Fatal("repo-c should not be allowed initially")
	}

	// Write a new policy that allows repo-c.
	dir := t.TempDir()
	newPolicyPath := filepath.Join(dir, "policy.json")

	instID := int64(42)
	newPolicy := policy.PolicyFile{
		SchemaVersion:      "ai-agent-policy/v1",
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents: map[string]policy.AgentPolicy{
			"claude": {
				AllowedRepos:       []string{"owner/repo-a", "owner/repo-c"},
				InstallationID:     &instID,
				DefaultPermissions: map[string]string{"contents": "write", "metadata": "read"},
			},
		},
	}

	data, _ := json.MarshalIndent(newPolicy, "", "  ")
	os.WriteFile(newPolicyPath, data, 0600)

	if err := e.Reload(newPolicyPath); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// After reload, repo-c should be allowed.
	if err := e.Authorize("claude", "owner/repo-c", nil); err != nil {
		t.Errorf("after reload, repo-c should be allowed: %v", err)
	}

	// repo-b should no longer be allowed.
	if err := e.Authorize("claude", "owner/repo-b", nil); err == nil {
		t.Error("after reload, repo-b should not be allowed")
	}
}

func TestPolicyEnforcerReloadInvalid(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy())

	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.json")
	os.WriteFile(badPath, []byte(`{"schema_version":"wrong"}`), 0600)

	err := e.Reload(badPath)
	if err == nil {
		t.Fatal("expected error for invalid policy")
	}

	// Original policy should still be in effect.
	if err := e.Authorize("claude", "owner/repo-a", nil); err != nil {
		t.Error("original policy should still work after failed reload")
	}
}
