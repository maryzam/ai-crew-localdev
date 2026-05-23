package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

func loadV2(t *testing.T, name string) *PolicyFileV2 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	f, err := ParsePolicyV2(data)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return f
}

func TestValidateV2_ValidFromFixture(t *testing.T) {
	f := loadV2(t, "valid_policy_v2.json")
	r := ValidateV2(f)
	if r.Errors.HasErrors() {
		t.Fatalf("expected no errors, got %v", r.Errors)
	}
}

func TestValidateV2_MissingSchemaVersion(t *testing.T) {
	f := &PolicyFileV2{
		DefaultSessionTTL:  "1h",
		DefaultIdleTimeout: "15m",
		Agents: map[string]AgentPolicyV2{
			"claude": {Resources: []string{"github:repo:owner/r"}},
		},
	}
	r := ValidateV2(f)
	if !r.Errors.HasErrors() {
		t.Fatal("expected error for missing schema_version")
	}
	found := false
	for _, e := range r.Errors {
		if e.Field == "schema_version" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected schema_version error, got %v", r.Errors)
	}
}

func TestValidateV2_RejectsV1WithMigrationHint(t *testing.T) {
	f := &PolicyFileV2{SchemaVersion: schema.PolicySchemaV1}
	r := ValidateV2(f)
	if !r.Errors.HasErrors() {
		t.Fatal("expected error for v1 schema")
	}
	if len(r.Errors) != 1 {
		t.Errorf("expected exactly one error, got %d: %v", len(r.Errors), r.Errors)
	}
	msg := r.Errors[0].Message
	if !strings.Contains(msg, "migrate") || !strings.Contains(msg, "resources") {
		t.Errorf("expected migration hint mentioning 'migrate' and 'resources', got %q", msg)
	}
}

func TestValidateV2_BadResourceURI(t *testing.T) {
	f := &PolicyFileV2{
		SchemaVersion:      schema.PolicySchemaV2,
		DefaultSessionTTL:  "1h",
		DefaultIdleTimeout: "15m",
		Agents: map[string]AgentPolicyV2{
			"claude": {Resources: []string{"github-repo-owner/r"}},
		},
	}
	r := ValidateV2(f)
	if !r.Errors.HasErrors() {
		t.Fatal("expected error for malformed URI")
	}
	found := false
	for _, e := range r.Errors {
		if strings.Contains(e.Message, "github-repo-owner/r") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error citing bad URI, got %v", r.Errors)
	}
}

func TestValidateV2_UnknownProvider(t *testing.T) {
	f := &PolicyFileV2{
		SchemaVersion:      schema.PolicySchemaV2,
		DefaultSessionTTL:  "1h",
		DefaultIdleTimeout: "15m",
		Agents: map[string]AgentPolicyV2{
			"claude": {Resources: []string{"aws:role:arn:aws:iam::123:role/x"}},
		},
	}
	r := ValidateV2(f)
	if !r.Errors.HasErrors() {
		t.Fatal("expected error for unknown provider")
	}
	found := false
	for _, e := range r.Errors {
		if strings.Contains(e.Message, "unknown provider") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown-provider error, got %v", r.Errors)
	}
}

func TestValidateV2_AgentWithNoResources(t *testing.T) {
	f := &PolicyFileV2{
		SchemaVersion:      schema.PolicySchemaV2,
		DefaultSessionTTL:  "1h",
		DefaultIdleTimeout: "15m",
		Agents: map[string]AgentPolicyV2{
			"claude": {Resources: []string{}},
		},
	}
	r := ValidateV2(f)
	if !r.Errors.HasErrors() {
		t.Fatal("expected error for agent with no resources")
	}
	found := false
	for _, e := range r.Errors {
		if e.Field == "agents.claude.resources" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected resources error, got %v", r.Errors)
	}
}

func TestValidateV2_MultipleAgentsValid(t *testing.T) {
	f := &PolicyFileV2{
		SchemaVersion:      schema.PolicySchemaV2,
		DefaultSessionTTL:  "1h",
		DefaultIdleTimeout: "15m",
		Agents: map[string]AgentPolicyV2{
			"claude": {
				Resources: []string{"github:repo:owner/a"},
				GitHub: &GitHubAgentConfig{
					InstallationID:     42,
					DefaultPermissions: map[string]string{"contents": "write"},
				},
			},
			"codex": {
				Resources: []string{"github:repo:owner/b"},
				GitHub: &GitHubAgentConfig{
					InstallationID:     99,
					DefaultPermissions: map[string]string{"contents": "read"},
				},
			},
		},
	}
	r := ValidateV2(f)
	if r.Errors.HasErrors() {
		t.Fatalf("expected no errors, got %v", r.Errors)
	}
}

func TestValidateV2_DefaultPermissionsSchema(t *testing.T) {
	f := &PolicyFileV2{
		SchemaVersion:      schema.PolicySchemaV2,
		DefaultSessionTTL:  "1h",
		DefaultIdleTimeout: "15m",
		Agents: map[string]AgentPolicyV2{
			"claude": {
				Resources: []string{"github:repo:owner/a"},
				GitHub: &GitHubAgentConfig{
					InstallationID:     1,
					DefaultPermissions: map[string]string{"contents": "superuser"},
				},
			},
		},
	}
	r := ValidateV2(f)
	if !r.Errors.HasErrors() {
		t.Fatal("expected error for bad permission level")
	}
	found := false
	for _, e := range r.Errors {
		if e.Field == "agents.claude.github.default_permissions.contents" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on permissions.contents, got %v", r.Errors)
	}
}
