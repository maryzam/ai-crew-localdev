package broker

import "time"

// CacheKey uniquely identifies a cached token.
//
// Cache keys are scoped to (installation_id, repo, permissions) so that
// different permission sets for the same repo produce separate cache entries.
type CacheKey struct {
	// InstallationID is the GitHub App installation that issued the token.
	InstallationID int64

	// Repo is the owner/repo the token is scoped to.
	Repo string

	// Permissions is a deterministic string serialization of the permission
	// map (sorted by key). This ensures that identical permission sets
	// always produce the same cache key.
	Permissions string
}

// CachedToken holds a token and its expiry metadata.
//
// The cache TTL should be shorter than the GitHub token lifetime to avoid
// serving expired tokens. For GitHub's default 60-minute installation
// tokens, a 50-minute cache TTL is recommended. This 10-minute margin
// ensures that tokens returned from the cache always have meaningful
// remaining validity.
type CachedToken struct {
	// Token is the GitHub installation access token.
	Token string

	// ExpiresAt is when the upstream token expires (per GitHub's response).
	ExpiresAt time.Time

	// CachedAt is when the token was inserted into the cache.
	CachedAt time.Time
}

// DefaultCacheTTL is the recommended cache TTL for GitHub installation
// tokens. GitHub tokens expire after 60 minutes; caching for 50 minutes
// leaves a 10-minute safety margin.
const DefaultCacheTTL = 50 * time.Minute

// TokenCache defines the interface for an in-memory token cache.
//
// Implementations should use singleflight coalescing to avoid duplicate
// upstream requests for the same cache key. Implementations must be safe
// for concurrent use.
type TokenCache interface {
	// Get retrieves a cached token. The second return value is false if the
	// key is not present or the cached entry has expired.
	Get(key CacheKey) (*CachedToken, bool)

	// Put inserts or replaces a token in the cache.
	Put(key CacheKey, token CachedToken)

	// Invalidate removes a single entry from the cache.
	Invalidate(key CacheKey)

	// Clear removes all entries from the cache.
	Clear()
}
