package core

import (
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
)

func TestMemorySessionStoreCreateResources(t *testing.T) {
	store := NewMemorySessionStore()

	req := api.CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/tmp/repo",
		Resources:    []string{"github:repo:owner/repo-a", "github:repo:owner/repo-b"},
	}

	sess, _, err := store.Create(req, 1000)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(sess.Resources) != 2 {
		t.Fatalf("Resources len = %d, want 2", len(sess.Resources))
	}
	if got := sess.Resources[0]; got.Provider != "github" || got.Kind != "repo" || got.Identifier != "owner/repo-a" {
		t.Errorf("Resources[0] = %+v, want github:repo:owner/repo-a", got)
	}
	if got := sess.Resources[1]; got.Identifier != "owner/repo-b" {
		t.Errorf("Resources[1].Identifier = %q, want owner/repo-b", got.Identifier)
	}
}

func TestMemorySessionStoreCreateResourcesMalformed(t *testing.T) {
	store := NewMemorySessionStore()

	req := api.CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/tmp/repo",
		Resources:    []string{"not-a-valid-uri"},
	}

	_, _, err := store.Create(req, 1000)
	if err == nil {
		t.Fatal("expected error for malformed resource URI")
	}
}
