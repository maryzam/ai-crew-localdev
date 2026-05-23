package broker

import (
	"encoding/json"
	"time"
)

// CacheKey uniquely identifies a cached credential. Providers contribute a
// stable ParamsHash so different parameter sets for the same
// (credential_type, resource) produce distinct cache entries.
type CacheKey struct {
	CredentialType string
	Resource       string
	ParamsHash     string
}

// CachedCredential holds the raw provider-specific credential payload and its
// expiry metadata. The broker forwards Payload verbatim on cache hits.
type CachedCredential struct {
	Payload   json.RawMessage
	ExpiresAt time.Time
	CachedAt  time.Time
}

// DefaultCacheTTL leaves a 10-minute safety margin under GitHub's 60-minute
// installation token lifetime.
const DefaultCacheTTL = 50 * time.Minute

// TokenCache is the contract for in-memory credential caching with
// singleflight coalescing. Implementations must be safe for concurrent use.
type TokenCache interface {
	Get(key CacheKey) (*CachedCredential, bool)
	Put(key CacheKey, entry CachedCredential)
	Invalidate(key CacheKey)
	Clear()
}
