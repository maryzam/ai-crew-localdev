package configstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/maryzam/ai-crew-localdev/internal/schema"
	"github.com/maryzam/ai-crew-localdev/internal/securefile"
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
	loadedIdentities, err := identity.Load(identitiesPath)
	if err != nil {
		t.Fatal(err)
	}
	if loadedIdentities.Agents["new"].AppID != "new" {
		t.Fatalf("identities = %#v", loadedIdentities)
	}
	data, err := securefile.ReadOwnerOnly(policyPath, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := policy.ParsePolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Agents["new"].Resources[0] != "github:repo:new/repo" {
		t.Fatalf("policy = %#v", parsed)
	}
}

func TestRecoverCompletesCommittedGenerationAfterWriteFailure(t *testing.T) {
	dir := t.TempDir()
	identitiesPath := filepath.Join(dir, "identities.json")
	policyPath := filepath.Join(dir, "policy.json")
	if err := securefile.WriteOwnerOnly(identitiesPath, mustMarshal(t, testIdentities("old"))); err != nil {
		t.Fatal(err)
	}
	if err := securefile.WriteOwnerOnly(policyPath, mustMarshal(t, testPolicy("old/repo"))); err != nil {
		t.Fatal(err)
	}
	writes := 0
	p := publisher{
		write: func(path string, data []byte) error {
			writes++
			if writes == 3 {
				return errors.New("injected write failure")
			}
			return securefile.WriteOwnerOnly(path, data)
		},
		remove: securefile.Remove,
	}
	entries := []journalEntry{{Path: identitiesPath, Data: mustMarshal(t, testIdentities("new"))}, {Path: policyPath, Data: mustMarshal(t, testPolicy("new/repo"))}}
	if err := p.publish(dir, entries); err == nil {
		t.Fatal("injected failure was ignored")
	}
	if err := Recover(identitiesPath); err != nil {
		t.Fatal(err)
	}
	loaded, err := identity.Load(identitiesPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Agents["new"]; !ok {
		t.Fatalf("identities = %#v", loaded)
	}
	data, err := securefile.ReadOwnerOnly(policyPath, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := policy.ParsePolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed.Agents["new"]; !ok {
		t.Fatalf("policy = %#v", parsed)
	}
	if _, err := os.Stat(filepath.Join(dir, journalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains: %v", err)
	}
}

func TestInspectNeverReturnsMixedGeneration(t *testing.T) {
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
		snapshot, err := Inspect(identitiesPath, policyPath)
		if err != nil {
			t.Fatal(err)
		}
		_, oldIdentity := snapshot.Identities.Agents["old"]
		_, oldPolicy := snapshot.Policy.Agents["old"]
		_, newIdentity := snapshot.Identities.Agents["new"]
		_, newPolicy := snapshot.Policy.Agents["new"]
		if oldIdentity != oldPolicy || newIdentity != newPolicy || oldIdentity == newIdentity {
			t.Fatalf("mixed snapshot: %#v %#v", snapshot.Identities, snapshot.Policy)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPublishRejectsUnrecoverableJournal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := securefile.WriteOwnerOnly(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	p := publisher{write: securefile.WriteOwnerOnly, remove: securefile.Remove}
	tests := []struct {
		name    string
		entries []journalEntry
	}{
		{name: "duplicate", entries: []journalEntry{{Path: path, Data: []byte("one")}, {Path: path, Data: []byte("two")}}},
		{name: "oversized", entries: []journalEntry{{Path: path, Data: make([]byte, (1<<20)+1)}, {Path: filepath.Join(dir, "other"), Data: []byte("two")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := p.publish(dir, test.entries); err == nil {
				t.Fatal("invalid journal accepted")
			}
			data, err := securefile.ReadOwnerOnly(path, 16)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "old" {
				t.Fatalf("data = %q", data)
			}
			if _, err := os.Stat(filepath.Join(dir, journalName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("journal exists: %v", err)
			}
		})
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
