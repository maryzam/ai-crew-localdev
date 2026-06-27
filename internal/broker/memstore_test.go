package broker

import (
	"crypto/sha256"
	"crypto/subtle"
	"testing"
	"time"
)

func sampleCreateReq() CreateSessionRequest {
	return CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
		RunID:        "run_memstore",
		TaskRef:      "github:owner/repo#43",
	}
}

func TestMemorySessionStoreCreate(t *testing.T) {
	store := NewMemorySessionStore()
	session, secret, err := store.Create(sampleCreateReq(), 1000)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if session.ID == "" {
		t.Error("session ID should not be empty")
	}
	if session.AgentName != "claude" {
		t.Errorf("AgentName = %q, want claude", session.AgentName)
	}
	if len(session.Resources) != 1 || session.Resources[0].String() != "github:repo:owner/repo" {
		t.Errorf("Resources = %v, want [github:repo:owner/repo]", session.Resources)
	}
	if session.RunID != "run_memstore" {
		t.Errorf("RunID = %q, want run_memstore", session.RunID)
	}
	if session.TaskRef != "github:owner/repo#43" {
		t.Errorf("TaskRef = %q, want github:owner/repo#43", session.TaskRef)
	}
	if len(secret) != bindSecretLen {
		t.Errorf("secret length = %d, want %d", len(secret), bindSecretLen)
	}

	hash := sha256.Sum256(secret)
	if subtle.ConstantTimeCompare(hash[:], session.BindSecretHash) != 1 {
		t.Error("stored hash does not match secret")
	}

	if !session.IsActive() {
		t.Error("new session should be active")
	}
}

func TestMemorySessionStoreCreateRejectsEmptyResources(t *testing.T) {
	store := NewMemorySessionStore()
	_, _, err := store.Create(CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/w/r",
	}, 1000)
	if err == nil {
		t.Fatal("expected error for empty Resources")
	}
}

func TestMemorySessionStoreCreateRejectsBadURI(t *testing.T) {
	store := NewMemorySessionStore()
	_, _, err := store.Create(CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/w/r",
		Resources:    []string{"not-a-uri"},
	}, 1000)
	if err == nil {
		t.Fatal("expected error for malformed Resource URI")
	}
}

func TestMemorySessionStoreGet(t *testing.T) {
	store := NewMemorySessionStore()
	session, _, _ := store.Create(sampleCreateReq(), 1000)

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
	session, secret, _ := store.Create(sampleCreateReq(), 1000)

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
	session, _, _ := store.Create(sampleCreateReq(), 1000)

	before := session.LastActivity
	time.Sleep(time.Millisecond)

	if err := store.RecordActivity(session.ID); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}

	got, _ := store.Get(session.ID)
	if !got.LastActivity.After(before) {
		t.Error("LastActivity was not advanced")
	}
	if got.MintCount != 1 {
		t.Errorf("MintCount = %d, want 1", got.MintCount)
	}
}

func TestMemorySessionStoreRevoke(t *testing.T) {
	store := NewMemorySessionStore()
	session, _, _ := store.Create(sampleCreateReq(), 1000)

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

	session, _, _ := store.Create(sampleCreateReq(), 1000)

	time.Sleep(20 * time.Millisecond)
	store.Cleanup()

	if _, err := store.Get(session.ID); err == nil {
		t.Error("expired session should have been cleaned up")
	}
}

func TestMemorySessionStoreGetReturnsSnapshot(t *testing.T) {
	store := NewMemorySessionStore()
	session, _, _ := store.Create(sampleCreateReq(), 1000)

	snapshot, err := store.Get(session.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if err := store.Revoke(session.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	fresh, err := store.Get(session.ID)
	if err != nil {
		t.Fatalf("Get after revoke: %v", err)
	}

	if snapshot.Revoked {
		t.Error("snapshot should not reflect later revocation")
	}
	if !fresh.Revoked {
		t.Error("fresh snapshot should reflect revocation")
	}
}
