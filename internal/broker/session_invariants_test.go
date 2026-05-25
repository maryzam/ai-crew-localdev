package broker

import (
	"strings"
	"testing"
	"time"
)

// Invariant: a session whose absolute TTL has elapsed must not be usable.
// This ensures time-bounded session guarantees hold regardless of activity.
func TestInvariant_ExpiredSessionCannotMint(t *testing.T) {
	store := NewMemorySessionStore()
	store.SessionTTL = 10 * time.Millisecond
	store.IdleTimeout = time.Hour // disable idle expiry for this test

	req := CreateSessionRequest{
		AgentName:    "test-agent",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
	}

	session, secret, err := store.Create(req, 1000)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Session should be active immediately.
	if !session.IsActive() {
		t.Fatal("new session should be active")
	}

	// Wait for TTL to elapse.
	time.Sleep(20 * time.Millisecond)

	// Re-fetch and verify expired.
	got, err := store.Get(session.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.IsActive() {
		t.Fatal("expired session must not be active")
	}
	if !got.IsExpired() {
		t.Fatal("session past TTL must report IsExpired() == true")
	}

	// Binding validation should still work (it checks hash, not lifecycle),
	// but the session is not usable for minting.
	if err := store.ValidateBinding(session.ID, secret); err != nil {
		t.Fatalf("ValidateBinding should succeed even on expired session: %v", err)
	}
}

// Invariant: a revoked session must not be usable, even if its TTL has
// not elapsed. Revocation is immediate and permanent.
func TestInvariant_RevokedSessionCannotMint(t *testing.T) {
	store := NewMemorySessionStore()

	req := CreateSessionRequest{
		AgentName:    "test-agent",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
	}

	session, _, err := store.Create(req, 1000)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !session.IsActive() {
		t.Fatal("new session should be active")
	}

	if err := store.Revoke(session.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	got, err := store.Get(session.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.IsActive() {
		t.Fatal("revoked session must not be active")
	}
	if !got.Revoked {
		t.Fatal("revoked session must have Revoked == true")
	}
}

// Invariant: presenting the wrong bind secret must be rejected with a
// binding_mismatch error. This prevents a process that knows only the
// session ID from minting tokens.
func TestInvariant_BindSecretMismatchDenied(t *testing.T) {
	store := NewMemorySessionStore()

	req := CreateSessionRequest{
		AgentName:    "test-agent",
		HostRepoPath: "/workspace/repo",
		Resources:    []string{"github:repo:owner/repo"},
	}

	session, _, err := store.Create(req, 1000)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	wrongSecret := make([]byte, 32) // all zeros
	err = store.ValidateBinding(session.ID, wrongSecret)
	if err == nil {
		t.Fatal("ValidateBinding with wrong secret must return an error")
	}
	if !strings.Contains(err.Error(), "binding mismatch") {
		t.Fatalf("expected binding_mismatch error, got: %v", err)
	}
}
