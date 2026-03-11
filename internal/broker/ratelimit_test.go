package broker

import (
	"testing"
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
