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
	entries map[CacheKey]*CachedCredential
	ttl     time.Duration
	group   singleflight.Group
}

// NewMemoryTokenCache creates an in-memory cache. A zero ttl falls back to
// DefaultCacheTTL.
func NewMemoryTokenCache(ttl time.Duration) *MemoryTokenCache {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &MemoryTokenCache{
		entries: make(map[CacheKey]*CachedCredential),
		ttl:     ttl,
	}
}

func (c *MemoryTokenCache) Get(key CacheKey) (*CachedCredential, bool) {
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

func (c *MemoryTokenCache) Put(key CacheKey, entry CachedCredential) {
	c.mu.Lock()
	c.entries[key] = &entry
	c.mu.Unlock()
}

func (c *MemoryTokenCache) Invalidate(key CacheKey) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

func (c *MemoryTokenCache) Clear() {
	c.mu.Lock()
	c.entries = make(map[CacheKey]*CachedCredential)
	c.mu.Unlock()
}

// GetOrFetch returns a cached credential or calls fetch exactly once for
// concurrent requests sharing a key. The second return value reports a hit.
func (c *MemoryTokenCache) GetOrFetch(key CacheKey, fetch func() (*CachedCredential, error)) (*CachedCredential, bool, error) {
	if entry, ok := c.Get(key); ok {
		return entry, true, nil
	}

	sfKey := fmt.Sprintf("%s|%s|%s|%s", key.Agent, key.CredentialType, key.Resource, key.ParamsHash)
	result, err, _ := c.group.Do(sfKey, func() (interface{}, error) {
		if entry, ok := c.Get(key); ok {
			return entry, nil
		}
		entry, err := fetch()
		if err != nil {
			return nil, err
		}
		c.Put(key, *entry)
		return entry, nil
	})
	if err != nil {
		return nil, false, err
	}
	return result.(*CachedCredential), false, nil
}
