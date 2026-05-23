package broker

import (
	"testing"
)

func TestMemorySessionStoreCreateResourcesNewStyle(t *testing.T) {
	store := NewMemorySessionStore()

	req := CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/tmp/repo",
		Resources:    []string{"github:repo:owner/repo-a", "github:repo:owner/repo-b"},
	}

	sess, _, err := store.Create(req, 1000)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if sess.Repo != "" {
		t.Errorf("Repo = %q, want empty (new-style request)", sess.Repo)
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

func TestMemorySessionStoreCreateResourcesLegacyRepo(t *testing.T) {
	store := NewMemorySessionStore()

	req := CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/tmp/repo",
		Repo:         "owner/repo-a",
	}

	sess, _, err := store.Create(req, 1000)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if sess.Repo != "owner/repo-a" {
		t.Errorf("Repo = %q, want owner/repo-a", sess.Repo)
	}
	if len(sess.Resources) != 1 {
		t.Fatalf("Resources len = %d, want 1", len(sess.Resources))
	}
	got := sess.Resources[0]
	if got.Provider != "github" || got.Kind != "repo" || got.Identifier != "owner/repo-a" {
		t.Errorf("synthesized resource = %+v, want github:repo:owner/repo-a", got)
	}
}

func TestMemorySessionStoreCreateResourcesMalformed(t *testing.T) {
	store := NewMemorySessionStore()

	req := CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/tmp/repo",
		Resources:    []string{"not-a-valid-uri"},
	}

	_, _, err := store.Create(req, 1000)
	if err == nil {
		t.Fatal("expected error for malformed resource URI")
	}
}

func TestMemorySessionStoreCreateResourcesNewWinsOverLegacy(t *testing.T) {
	store := NewMemorySessionStore()

	req := CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/tmp/repo",
		Repo:         "owner/legacy",
		Resources:    []string{"github:repo:owner/new"},
	}

	sess, _, err := store.Create(req, 1000)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if sess.Repo != "" {
		t.Errorf("Repo = %q, want empty when Resources is set", sess.Repo)
	}
	if len(sess.Resources) != 1 || sess.Resources[0].Identifier != "owner/new" {
		t.Errorf("Resources = %+v, want only owner/new", sess.Resources)
	}
}
