package broker

import (
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// MemoryTokenCache is an in-memory TokenCache with singleflight coalescing.
type MemoryTokenCache struct {
	mu      sync.RWMutex
	entries map[CacheKey]*CachedToken
	ttl     time.Duration
	group   singleflight.Group
}

// NewMemoryTokenCache creates a new in-memory token cache.
// If ttl is zero, DefaultCacheTTL is used.
func NewMemoryTokenCache(ttl time.Duration) *MemoryTokenCache {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &MemoryTokenCache{
		entries: make(map[CacheKey]*CachedToken),
		ttl:     ttl,
	}
}

// Get retrieves a cached token if present and not expired.
func (c *MemoryTokenCache) Get(key CacheKey) (*CachedToken, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		return nil, false
	}
	if time.Since(entry.CachedAt) > c.ttl {
		c.Invalidate(key)
		return nil, false
	}
	return entry, true
}

// Put inserts or replaces a token in the cache.
func (c *MemoryTokenCache) Put(key CacheKey, token CachedToken) {
	c.mu.Lock()
	c.entries[key] = &token
	c.mu.Unlock()
}

// Invalidate removes a single entry from the cache.
func (c *MemoryTokenCache) Invalidate(key CacheKey) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// Clear removes all entries from the cache.
func (c *MemoryTokenCache) Clear() {
	c.mu.Lock()
	c.entries = make(map[CacheKey]*CachedToken)
	c.mu.Unlock()
}

// GetOrFetch returns a cached token or calls fetch exactly once for
// concurrent requests with the same key (singleflight coalescing).
func (c *MemoryTokenCache) GetOrFetch(key CacheKey, fetch func() (*CachedToken, error)) (*CachedToken, bool, error) {
	if entry, ok := c.Get(key); ok {
		return entry, true, nil
	}

	sfKey := fmt.Sprintf("%s|%s|%s", key.CredentialType, key.Resource, key.ParamsHash)

	result, err, _ := c.group.Do(sfKey, func() (interface{}, error) {
		// Double-check cache after acquiring the singleflight slot.
		if entry, ok := c.Get(key); ok {
			return entry, nil
		}
		token, err := fetch()
		if err != nil {
			return nil, err
		}
		c.Put(key, *token)
		return token, nil
	})

	if err != nil {
		return nil, false, err
	}
	return result.(*CachedToken), false, nil
}
