package broker

import "time"

// CacheKey uniquely identifies a cached credential. The key is
// credential-generic: providers contribute a stable ParamsHash so
// different parameter sets for the same (credential_type, resource)
// produce distinct cache entries.
type CacheKey struct {
	// CredentialType is the wire-level credential_type discriminator
	// (e.g. CredentialTypeGitHubAppInstallation).
	CredentialType string

	// Resource is the full ResourceURI string the credential is scoped
	// to (e.g. "github:repo:owner/name").
	Resource string

	// ParamsHash is a provider-computed stable hash of the params blob
	// (e.g. a hex sha256 over the sorted permission map for GitHub).
	// Providers without parameters use the empty string.
	ParamsHash string
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
