package cli

import (
	"errors"
	"os"
	"testing"
)

func TestCleanupRevokedSessionIgnoresMissingFile(t *testing.T) {
	orig := removeSessionInfo
	removeSessionInfo = func(sessionID string) error { return os.ErrNotExist }
	t.Cleanup(func() { removeSessionInfo = orig })

	if err := cleanupRevokedSession("sess-123"); err != nil {
		t.Fatalf("cleanupRevokedSession: %v", err)
	}
}

func TestCleanupRevokedSessionReturnsRemovalFailure(t *testing.T) {
	orig := removeSessionInfo
	removeSessionInfo = func(sessionID string) error { return errors.New("permission denied") }
	t.Cleanup(func() { removeSessionInfo = orig })

	err := cleanupRevokedSession("sess-123")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
