package governance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func TestDefaultPathsHonorEnvironmentContract(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(paths.EnvConfigDir, filepath.Join(dir, "config"))
	t.Setenv(paths.EnvPolicyPath, filepath.Join(dir, "policy", "custom.json"))

	got := DefaultPaths()
	if got.Identities != filepath.Join(dir, "config", "identities.json") {
		t.Fatalf("identities path = %q", got.Identities)
	}
	if got.Policy != filepath.Join(dir, "policy", "custom.json") {
		t.Fatalf("policy path = %q", got.Policy)
	}
}

func TestFileStoreRoundTripUsesGovernancePaths(t *testing.T) {
	dir := t.TempDir()
	governancePaths := Paths{Identities: filepath.Join(dir, "identities.json"), Policy: filepath.Join(dir, "policy.json")}
	installationID := int64(12345)
	identities := &identity.IdentitiesFile{SchemaVersion: schema.IdentitiesSchemaV2, Agents: map[string]identity.AgentIdentity{
		"codex": {AppID: "100", AppKey: filepath.Join(dir, "app.pem"), GitName: "Codex Bot", GitEmail: "codex@example.test", GithubHost: "github.com", Tool: "codex", InstallationID: &installationID},
	}}
	policyFile := &policy.PolicyFile{SchemaVersion: schema.PolicySchemaCurrent, DefaultSessionTTL: "8h", DefaultIdleTimeout: "1h", Agents: map[string]policy.AgentPolicy{
		"codex": {Resources: []string{"github:repo:owner/repo"}},
	}}
	store := FileStore{}

	if err := store.Publish(governancePaths, identities, policyFile); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	snapshot, err := store.Load(governancePaths)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snapshot.IdentitiesError != nil || snapshot.PolicyError != nil {
		t.Fatalf("snapshot errors: identities=%v policy=%v", snapshot.IdentitiesError, snapshot.PolicyError)
	}
	if snapshot.Identities.Agents["codex"].GitEmail != "codex@example.test" {
		t.Fatalf("identity email = %q", snapshot.Identities.Agents["codex"].GitEmail)
	}
	if got := snapshot.Policy.Agents["codex"].Resources; len(got) != 1 || got[0] != "github:repo:owner/repo" {
		t.Fatalf("resources = %v", got)
	}
	if _, err := os.Stat(governancePaths.Identities); err != nil {
		t.Fatalf("identities file: %v", err)
	}
	if _, err := os.Stat(governancePaths.Policy); err != nil {
		t.Fatalf("policy file: %v", err)
	}
}
