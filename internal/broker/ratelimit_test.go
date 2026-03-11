package broker

import (
	"testing"
	"time"
)

func TestRateLimiterAllows(t *testing.T) {
	rl := NewRateLimiter(5, 10)

	for i := 0; i < 5; i++ {
		if !rl.Allow("sess-1", "o/r") {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	// 6th request should be denied (session limit = 5).
	if rl.Allow("sess-1", "o/r") {
		t.Error("request 6 should be denied by session limit")
	}
}

func TestRateLimiterRepoLimit(t *testing.T) {
	rl := NewRateLimiter(100, 3) // High session limit, low repo limit.

	for i := 0; i < 3; i++ {
		if !rl.Allow("sess-"+string(rune('a'+i)), "o/r") {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	if rl.Allow("sess-d", "o/r") {
		t.Error("should be denied by repo limit")
	}
}

func TestRateLimiterDifferentSessions(t *testing.T) {
	rl := NewRateLimiter(2, 100)

	rl.Allow("sess-1", "o/r")
	rl.Allow("sess-1", "o/r")

	// sess-1 exhausted, but sess-2 should still be allowed.
	if !rl.Allow("sess-2", "o/r") {
		t.Error("different session should be allowed")
	}
}

func TestRateLimiterDefaults(t *testing.T) {
	rl := NewRateLimiter(0, 0)
	if rl.sessionLimit != DefaultSessionRateLimit {
		t.Errorf("session limit = %d, want %d", rl.sessionLimit, DefaultSessionRateLimit)
	}
	if rl.repoLimit != DefaultRepoRateLimit {
		t.Errorf("repo limit = %d, want %d", rl.repoLimit, DefaultRepoRateLimit)
	}
}

func TestRateLimiterCleanup(t *testing.T) {
	rl := NewRateLimiter(5, 5)

	// Override window to 10ms for fast testing.
	rl.window = 10 * time.Millisecond

	// Record requests.
	rl.Allow("sess-active", "repo-active")
	rl.Allow("sess-expired", "repo-expired")

	// Wait for window to pass.
	time.Sleep(20 * time.Millisecond)

	// Record another request to keep one active.
	rl.Allow("sess-active", "repo-active")

	// Run cleanup.
	rl.Cleanup()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// sess-expired and repo-expired should be removed.
	if _, ok := rl.sessions["sess-expired"]; ok {
		t.Error("sess-expired bucket should have been cleaned up")
	}
	if _, ok := rl.repos["repo-expired"]; ok {
		t.Error("repo-expired bucket should have been cleaned up")
	}

	// sess-active and repo-active should remain.
	if _, ok := rl.sessions["sess-active"]; !ok {
		t.Error("sess-active bucket should not have been cleaned up")
	}
	if _, ok := rl.repos["repo-active"]; !ok {
		t.Error("repo-active bucket should not have been cleaned up")
	}

	// Internal timestamps for active buckets should be compacted (expired ones removed).
	if len(rl.sessions["sess-active"].timestamps) != 1 {
		t.Errorf("expected 1 active timestamp in sess-active, got %d", len(rl.sessions["sess-active"].timestamps))
	}
}
