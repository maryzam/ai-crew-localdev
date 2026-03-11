package broker

import (
	"sync"
	"time"
)

const (
	// DefaultSessionRateLimit is the default max requests per minute per session.
	DefaultSessionRateLimit = 30

	// DefaultRepoRateLimit is the default max requests per minute per repo.
	DefaultRepoRateLimit = 60
)

// RateLimiter enforces per-session and per-repo token mint rate limits
// using a sliding window counter.
type RateLimiter struct {
	mu           sync.Mutex
	sessionLimit int
	repoLimit    int
	window       time.Duration
	sessions     map[string]*bucket
	repos        map[string]*bucket
}

type bucket struct {
	timestamps []time.Time
}

// NewRateLimiter creates a rate limiter with the given per-session and
// per-repo limits per minute. Zero values use defaults.
func NewRateLimiter(sessionLimit, repoLimit int) *RateLimiter {
	if sessionLimit <= 0 {
		sessionLimit = DefaultSessionRateLimit
	}
	if repoLimit <= 0 {
		repoLimit = DefaultRepoRateLimit
	}
	return &RateLimiter{
		sessionLimit: sessionLimit,
		repoLimit:    repoLimit,
		window:       time.Minute,
		sessions:     make(map[string]*bucket),
		repos:        make(map[string]*bucket),
	}
}

// Allow checks whether a request from the given session for the given repo
// is within rate limits. Returns true and records the request if allowed.
func (r *RateLimiter) Allow(sessionID, repo string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	if !r.allowBucket(r.sessions, sessionID, r.sessionLimit, now) {
		return false
	}
	if !r.allowBucket(r.repos, repo, r.repoLimit, now) {
		return false
	}

	r.recordBucket(r.sessions, sessionID, now)
	r.recordBucket(r.repos, repo, now)
	return true
}

func (r *RateLimiter) allowBucket(buckets map[string]*bucket, key string, limit int, now time.Time) bool {
	b, ok := buckets[key]
	if !ok {
		return true
	}
	cutoff := now.Add(-r.window)
	count := 0
	for _, ts := range b.timestamps {
		if ts.After(cutoff) {
			count++
		}
	}
	return count < limit
}

func (r *RateLimiter) recordBucket(buckets map[string]*bucket, key string, now time.Time) {
	b, ok := buckets[key]
	if !ok {
		b = &bucket{}
		buckets[key] = b
	}

	// Prune old entries while recording.
	cutoff := now.Add(-r.window)
	pruned := b.timestamps[:0]
	for _, ts := range b.timestamps {
		if ts.After(cutoff) {
			pruned = append(pruned, ts)
		}
	}
	b.timestamps = append(pruned, now)
}

// Cleanup removes any rate limit buckets that no longer contain active timestamps.
// This prevents memory leaks from short-lived sessions or repos over time.
func (r *RateLimiter) Cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	r.cleanupBuckets(r.sessions, cutoff)
	r.cleanupBuckets(r.repos, cutoff)
}

func (r *RateLimiter) cleanupBuckets(buckets map[string]*bucket, cutoff time.Time) {
	for key, b := range buckets {
		activeCount := 0
		for _, ts := range b.timestamps {
			if ts.After(cutoff) {
				activeCount++
			}
		}

		if activeCount == 0 {
			delete(buckets, key)
		} else if activeCount < len(b.timestamps) {
			// Compact the slice if some but not all entries are expired
			pruned := b.timestamps[:0]
			for _, ts := range b.timestamps {
				if ts.After(cutoff) {
					pruned = append(pruned, ts)
				}
			}
			b.timestamps = pruned
		}
	}
}
