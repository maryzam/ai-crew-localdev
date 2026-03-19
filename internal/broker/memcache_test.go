package broker

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryTokenCacheGetPut(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)

	key := CacheKey{InstallationID: 1, Repo: "o/r", Permissions: "contents=write"}
	token := CachedToken{Token: "tok-1", ExpiresAt: time.Now().Add(time.Hour), CachedAt: time.Now()}

	if _, ok := cache.Get(key); ok {
		t.Error("expected cache miss before put")
	}

	cache.Put(key, token)

	got, ok := cache.Get(key)
	if !ok {
		t.Fatal("expected cache hit after put")
	}
	if got.Token != "tok-1" {
		t.Errorf("Token = %q, want tok-1", got.Token)
	}
}

func TestMemoryTokenCacheTTLExpiry(t *testing.T) {
	cache := NewMemoryTokenCache(10 * time.Millisecond)

	key := CacheKey{InstallationID: 1, Repo: "o/r", Permissions: ""}
	token := CachedToken{Token: "tok", ExpiresAt: time.Now().Add(time.Hour), CachedAt: time.Now()}
	cache.Put(key, token)

	time.Sleep(20 * time.Millisecond)
	if _, ok := cache.Get(key); ok {
		t.Error("expected cache miss after TTL expiry")
	}
}

func TestMemoryTokenCacheInvalidate(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)
	key := CacheKey{InstallationID: 1, Repo: "o/r", Permissions: ""}
	cache.Put(key, CachedToken{Token: "tok", CachedAt: time.Now()})

	cache.Invalidate(key)
	if _, ok := cache.Get(key); ok {
		t.Error("expected miss after invalidate")
	}
}

func TestMemoryTokenCacheClear(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)
	for i := 0; i < 5; i++ {
		key := CacheKey{InstallationID: int64(i), Repo: "o/r"}
		cache.Put(key, CachedToken{Token: fmt.Sprintf("tok-%d", i), CachedAt: time.Now()})
	}

	cache.Clear()

	for i := 0; i < 5; i++ {
		key := CacheKey{InstallationID: int64(i), Repo: "o/r"}
		if _, ok := cache.Get(key); ok {
			t.Errorf("expected miss for key %d after clear", i)
		}
	}
}

func TestMemoryTokenCacheSingleflight(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)
	key := CacheKey{InstallationID: 1, Repo: "o/r", Permissions: "contents=write"}

	var fetchCount atomic.Int32

	fetch := func() (*CachedToken, error) {
		fetchCount.Add(1)
		time.Sleep(50 * time.Millisecond) // Simulate slow fetch.
		return &CachedToken{
			Token:     "coalesced-tok",
			ExpiresAt: time.Now().Add(time.Hour),
			CachedAt:  time.Now(),
		}, nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, _, err := cache.GetOrFetch(key, fetch)
			if err != nil {
				t.Errorf("GetOrFetch: %v", err)
				return
			}
			if tok.Token != "coalesced-tok" {
				t.Errorf("Token = %q, want coalesced-tok", tok.Token)
			}
		}()
	}
	wg.Wait()

	if count := fetchCount.Load(); count != 1 {
		t.Errorf("fetch called %d times, want exactly 1 (singleflight)", count)
	}
}

func TestMemoryTokenCacheGetOrFetchCacheHit(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)
	key := CacheKey{InstallationID: 1, Repo: "o/r", Permissions: ""}

	cache.Put(key, CachedToken{Token: "cached", CachedAt: time.Now()})

	tok, cacheHit, err := cache.GetOrFetch(key, func() (*CachedToken, error) {
		t.Error("fetch should not be called on cache hit")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("GetOrFetch: %v", err)
	}
	if !cacheHit {
		t.Error("expected cache hit")
	}
	if tok.Token != "cached" {
		t.Errorf("Token = %q, want cached", tok.Token)
	}
}

func TestMemoryTokenCacheDifferentKeysNotCoalesced(t *testing.T) {
	cache := NewMemoryTokenCache(DefaultCacheTTL)

	var fetchCount atomic.Int32

	fetch := func() (*CachedToken, error) {
		fetchCount.Add(1)
		return &CachedToken{Token: "t", CachedAt: time.Now()}, nil
	}

	key1 := CacheKey{InstallationID: 1, Repo: "o/r1", Permissions: "contents=write"}
	key2 := CacheKey{InstallationID: 1, Repo: "o/r2", Permissions: "contents=write"}

	_, _, _ = cache.GetOrFetch(key1, fetch)
	_, _, _ = cache.GetOrFetch(key2, fetch)

	if count := fetchCount.Load(); count != 2 {
		t.Errorf("fetch called %d times, want 2 for different keys", count)
	}
}
