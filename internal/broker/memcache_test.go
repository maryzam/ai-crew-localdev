package broker

import (
	"encoding/json"
	"fmt"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testCacheKey(resource, paramsHash string) CacheKey {
	return CacheKey{
		CredentialType: githubcontract.CredentialType,
		Resource:       resource,
		ParamsHash:     paramsHash,
	}
}

func payload(token string) json.RawMessage {
	out, _ := json.Marshal(githubcontract.Credential{Token: token})
	return out
}

func extractToken(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var c githubcontract.Credential
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal cached payload: %v", err)
	}
	return c.Token
}

func TestMemoryTokenCacheGetPut(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)

	key := testCacheKey("github:repo:o/r", "h1")
	cache.Put(key, CachedCredential{
		Payload:   payload("tok-1"),
		ExpiresAt: time.Now().Add(time.Hour),
		CachedAt:  time.Now(),
	})

	got, ok := cache.Get(key)
	if !ok {
		t.Fatal("expected cache hit after put")
	}
	if extractToken(t, got.Payload) != "tok-1" {
		t.Errorf("Token = %q", extractToken(t, got.Payload))
	}
}

func TestMemoryTokenCacheTTLExpiry(t *testing.T) {
	cache := NewMemoryTokenCache(10 * time.Millisecond)
	key := testCacheKey("github:repo:o/r", "")
	cache.Put(key, CachedCredential{Payload: payload("tok"), CachedAt: time.Now()})

	time.Sleep(20 * time.Millisecond)
	if _, ok := cache.Get(key); ok {
		t.Error("expected cache miss after TTL expiry")
	}
}

func TestMemoryTokenCacheInvalidate(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)
	key := testCacheKey("github:repo:o/r", "")
	cache.Put(key, CachedCredential{Payload: payload("tok"), CachedAt: time.Now()})

	cache.Invalidate(key)
	if _, ok := cache.Get(key); ok {
		t.Error("expected miss after invalidate")
	}
}

func TestMemoryTokenCacheClear(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)
	for i := 0; i < 5; i++ {
		key := testCacheKey(fmt.Sprintf("github:repo:o/r%d", i), "")
		cache.Put(key, CachedCredential{Payload: payload(fmt.Sprintf("tok-%d", i)), CachedAt: time.Now()})
	}
	cache.Clear()
	for i := 0; i < 5; i++ {
		key := testCacheKey(fmt.Sprintf("github:repo:o/r%d", i), "")
		if _, ok := cache.Get(key); ok {
			t.Errorf("expected miss for key %d after clear", i)
		}
	}
}

func TestMemoryTokenCacheSingleflight(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)
	key := testCacheKey("github:repo:o/r", "h1")

	var fetchCount atomic.Int32
	fetch := func() (*CachedCredential, error) {
		fetchCount.Add(1)
		time.Sleep(50 * time.Millisecond)
		return &CachedCredential{
			Payload:   payload("coalesced-tok"),
			ExpiresAt: time.Now().Add(time.Hour),
			CachedAt:  time.Now(),
		}, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry, _, err := cache.GetOrFetch(key, fetch)
			if err != nil {
				t.Errorf("GetOrFetch: %v", err)
				return
			}
			if extractToken(t, entry.Payload) != "coalesced-tok" {
				t.Errorf("Token = %q", extractToken(t, entry.Payload))
			}
		}()
	}
	wg.Wait()

	if count := fetchCount.Load(); count != 1 {
		t.Errorf("fetch called %d times, want 1 (singleflight)", count)
	}
}

func TestMemoryTokenCacheGetOrFetchCacheHit(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)
	key := testCacheKey("github:repo:o/r", "")
	cache.Put(key, CachedCredential{Payload: payload("cached"), CachedAt: time.Now()})

	entry, cacheHit, err := cache.GetOrFetch(key, func() (*CachedCredential, error) {
		t.Error("fetch should not be called on cache hit")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("GetOrFetch: %v", err)
	}
	if !cacheHit {
		t.Error("expected cache hit")
	}
	if extractToken(t, entry.Payload) != "cached" {
		t.Errorf("Token = %q", extractToken(t, entry.Payload))
	}
}

func TestMemoryTokenCacheDifferentKeysNotCoalesced(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)
	var fetchCount atomic.Int32
	fetch := func() (*CachedCredential, error) {
		fetchCount.Add(1)
		return &CachedCredential{Payload: payload("t"), CachedAt: time.Now()}, nil
	}

	key1 := testCacheKey("github:repo:o/r1", "h1")
	key2 := testCacheKey("github:repo:o/r2", "h1")
	_, _, _ = cache.GetOrFetch(key1, fetch)
	_, _, _ = cache.GetOrFetch(key2, fetch)

	if count := fetchCount.Load(); count != 2 {
		t.Errorf("fetch called %d times, want 2", count)
	}
}
