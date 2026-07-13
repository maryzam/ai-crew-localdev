package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

func TestPublishWritesOneRecoverableGeneration(t *testing.T) {
	dir := t.TempDir()
	identitiesPath := filepath.Join(dir, "identities.json")
	policyPath := filepath.Join(dir, "policy.json")
	identities := testIdentities("new")
	policyFile := testPolicy("new/repo")
	if err := Publish(identitiesPath, identities, policyPath, policyFile); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Load(identitiesPath, policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.IdentitiesError != nil || snapshot.PolicyError != nil {
		t.Fatalf("load errors = %v, %v", snapshot.IdentitiesError, snapshot.PolicyError)
	}
	loadedIdentities, loadedPolicy := snapshot.Identities, snapshot.Policy
	if loadedIdentities.Agents["new"].AppID != "new" {
		t.Fatalf("identities = %#v", loadedIdentities)
	}
	if loadedPolicy.Agents["new"].Resources[0] != "github:repo:new/repo" {
		t.Fatalf("policy = %#v", loadedPolicy)
	}
}

func TestLoadRecoversCommittedJournal(t *testing.T) {
	dir := t.TempDir()
	identitiesPath := filepath.Join(dir, "identities.json")
	policyPath := filepath.Join(dir, "policy.json")
	paths, err := resolve(identitiesPath, policyPath)
	if err != nil {
		t.Fatal(err)
	}
	pending := transaction{IdentitiesPath: paths.identities, PolicyPath: paths.policy, Identities: mustMarshal(t, testIdentities("new")), Policy: mustMarshal(t, testPolicy("new/repo"))}
	data, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	if err := securefile.WriteOwnerOnly(filepath.Join(dir, journalName), data); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Load(identitiesPath, policyPath)
	if err != nil || snapshot.IdentitiesError != nil || snapshot.PolicyError != nil {
		t.Fatalf("load = %#v, %v", snapshot, err)
	}
	if _, ok := snapshot.Identities.Agents["new"]; !ok {
		t.Fatalf("identities = %#v", snapshot.Identities)
	}
	if _, err := os.Stat(filepath.Join(dir, journalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains: %v", err)
	}
}

func TestLoadNeverReturnsMixedGeneration(t *testing.T) {
	dir := t.TempDir()
	identitiesPath := filepath.Join(dir, "identities.json")
	policyPath := filepath.Join(dir, "policy.json")
	if err := Publish(identitiesPath, testIdentities("old"), policyPath, testPolicy("old/repo")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- Publish(identitiesPath, testIdentities("new"), policyPath, testPolicy("new/repo"))
	}()
	for range 32 {
		snapshot, err := Load(identitiesPath, policyPath)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.IdentitiesError != nil || snapshot.PolicyError != nil {
			t.Fatalf("load errors = %v, %v", snapshot.IdentitiesError, snapshot.PolicyError)
		}
		identities, policyFile := snapshot.Identities, snapshot.Policy
		_, oldIdentity := identities.Agents["old"]
		_, oldPolicy := policyFile.Agents["old"]
		_, newIdentity := identities.Agents["new"]
		_, newPolicy := policyFile.Agents["new"]
		if oldIdentity != oldPolicy || newIdentity != newPolicy || oldIdentity == newIdentity {
			t.Fatalf("mixed snapshot: %#v %#v", identities, policyFile)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPublishPolicyDoesNotRewriteIdentities(t *testing.T) {
	dir := t.TempDir()
	identitiesPath := filepath.Join(dir, "identities.json")
	policyPath := filepath.Join(dir, "policy.json")
	identitiesData := []byte(`{"schema_version":"ai-agent-identities/v2","future_field":"preserve","agents":{"owner":{"app_id":"1","app_key":"/key","git_name":"owner","git_email":"owner@example.test","future_agent_field":"preserve"}}}`)
	if err := securefile.WriteOwnerOnly(identitiesPath, identitiesData); err != nil {
		t.Fatal(err)
	}

	if err := PublishPolicy(identitiesPath, policyPath, testPolicy("owner/repo")); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(identitiesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(identitiesData) {
		t.Fatalf("identities changed:\n got %s\nwant %s", got, identitiesData)
	}
	snapshot, err := Load(identitiesPath, policyPath)
	if err != nil || snapshot.PolicyError != nil {
		t.Fatalf("load = %#v, %v", snapshot, err)
	}
	if snapshot.Policy.Agents["owner"].Resources[0] != "github:repo:owner/repo" {
		t.Fatalf("policy = %#v", snapshot.Policy)
	}
}

func TestLoadRejectsUnboundOrOversizedTransaction(t *testing.T) {
	for _, name := range []string{"wrong target", "oversized"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			identitiesPath := filepath.Join(dir, "identities.json")
			policyPath := filepath.Join(dir, "policy.json")
			paths, err := resolve(identitiesPath, policyPath)
			if err != nil {
				t.Fatal(err)
			}
			pending := transaction{IdentitiesPath: paths.identities, PolicyPath: paths.policy, Identities: []byte("identities"), Policy: []byte("policy")}
			if name == "wrong target" {
				pending.PolicyPath = filepath.Join(dir, "unexpected")
			} else {
				pending.Identities = make([]byte, maxFileSize+1)
			}
			data, err := json.Marshal(pending)
			if err != nil {
				t.Fatal(err)
			}
			if err := securefile.WriteOwnerOnly(filepath.Join(dir, journalName), data); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(identitiesPath, policyPath); err == nil {
				t.Fatal("invalid transaction accepted")
			}
			if _, err := os.Stat(identitiesPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("identities changed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "unexpected")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected target changed: %v", err)
			}
		})
	}
}

func TestLoadRejectsSymlinkLock(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, lockName)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(dir, "identities.json"), filepath.Join(dir, "policy.json")); err == nil {
		t.Fatal("symlink lock accepted")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "unchanged" {
		t.Fatalf("target = %q", data)
	}
}

func testIdentities(agent string) *identity.IdentitiesFile {
	return &identity.IdentitiesFile{SchemaVersion: schema.IdentitiesSchemaV2, Agents: map[string]identity.AgentIdentity{agent: {AppID: agent, AppKey: "/key", GitName: agent, GitEmail: agent + "@example.test"}}}
}

func testPolicy(repo string) *policy.PolicyFile {
	agent := repo[:len(repo)-len("/repo")]
	section, _ := json.Marshal(map[string]any{"installation_id": 1})
	return &policy.PolicyFile{SchemaVersion: schema.PolicySchemaCurrent, DefaultSessionTTL: "8h", DefaultIdleTimeout: "1h", Agents: map[string]policy.AgentPolicy{agent: {Resources: []string{"github:repo:" + repo}, Providers: map[string]json.RawMessage{"github": section}}}}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
