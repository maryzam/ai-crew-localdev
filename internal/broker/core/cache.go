package core

import (
	"encoding/json"
	"time"
)

type CacheKey struct {
	Agent          string
	CredentialType string
	Resource       string
	ParamsHash     string
}

type CachedCredential struct {
	Payload   json.RawMessage
	ExpiresAt time.Time
	CachedAt  time.Time
}

const DefaultCacheTTL = 50 * time.Minute

type TokenCache interface {
	Get(key CacheKey) (*CachedCredential, bool)
	Put(key CacheKey, entry CachedCredential)
	Invalidate(key CacheKey)
	Clear()
}
