package broker

import (
	"crypto/sha256"
	"crypto/subtle"
	"testing"
	"time"
)

func TestMemorySessionStoreCreate(t *testing.T) {
	store := NewMemorySessionStore()
	req := CreateSessionRequest{
		AgentName:            "claude",
		Repo:                 "owner/repo",
		HostRepoPath:         "/workspace/repo",
		RequestedPermissions: map[string]string{"contents": "write"},
	}

	session, secret, err := store.Create(req, 1000)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if session.ID == "" {
		t.Error("session ID should not be empty")
	}
	if session.AgentName != "claude" {
		t.Errorf("AgentName = %q, want claude", session.AgentName)
	}
	if session.Repo != "owner/repo" {
		t.Errorf("Repo = %q, want owner/repo", session.Repo)
	}
	if len(secret) != bindSecretLen {
		t.Errorf("secret length = %d, want %d", len(secret), bindSecretLen)
	}

	// Verify hash matches secret.
	hash := sha256.Sum256(secret)
	if subtle.ConstantTimeCompare(hash[:], session.BindSecretHash) != 1 {
		t.Error("stored hash does not match secret")
	}

	if !session.IsActive() {
		t.Error("new session should be active")
	}
}

func TestMemorySessionStoreGet(t *testing.T) {
	store := NewMemorySessionStore()
	req := CreateSessionRequest{AgentName: "test", Repo: "o/r", HostRepoPath: "/w/r"}
	session, _, _ := store.Create(req, 1000)

	got, err := store.Get(session.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != session.ID {
		t.Errorf("Get returned wrong session")
	}

	_, err = store.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestMemorySessionStoreValidateBinding(t *testing.T) {
	store := NewMemorySessionStore()
	req := CreateSessionRequest{AgentName: "test", Repo: "o/r", HostRepoPath: "/w/r"}
	session, secret, _ := store.Create(req, 1000)

	if err := store.ValidateBinding(session.ID, secret); err != nil {
		t.Fatalf("valid binding failed: %v", err)
	}

	wrongSecret := make([]byte, 32)
	if err := store.ValidateBinding(session.ID, wrongSecret); err == nil {
		t.Error("expected error for wrong secret")
	}

	if err := store.ValidateBinding("nonexistent", secret); err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestMemorySessionStoreRecordActivity(t *testing.T) {
	store := NewMemorySessionStore()
	req := CreateSessionRequest{AgentName: "test", Repo: "o/r", HostRepoPath: "/w/r"}
	session, _, _ := store.Create(req, 1000)

	before := session.LastActivity
	time.Sleep(time.Millisecond)

	if err := store.RecordActivity(session.ID); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}

	got, _ := store.Get(session.ID)
	if !got.LastActivity.After(before) {
		t.Error("LastActivity was not advanced")
	}
	if got.TokenMintCount != 1 {
		t.Errorf("TokenMintCount = %d, want 1", got.TokenMintCount)
	}
}

func TestMemorySessionStoreRevoke(t *testing.T) {
	store := NewMemorySessionStore()
	req := CreateSessionRequest{AgentName: "test", Repo: "o/r", HostRepoPath: "/w/r"}
	session, _, _ := store.Create(req, 1000)

	if err := store.Revoke(session.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	got, _ := store.Get(session.ID)
	if got.IsActive() {
		t.Error("revoked session should not be active")
	}
}

func TestMemorySessionStoreCleanup(t *testing.T) {
	store := NewMemorySessionStore()
	store.SessionTTL = 10 * time.Millisecond

	req := CreateSessionRequest{AgentName: "test", Repo: "o/r", HostRepoPath: "/w/r"}
	session, _, _ := store.Create(req, 1000)

	time.Sleep(20 * time.Millisecond)
	store.Cleanup()

	if _, err := store.Get(session.ID); err == nil {
		t.Error("expired session should have been cleaned up")
	}
}
