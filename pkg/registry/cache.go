package registry

import (
	"context"
	"sync"
	"time"
)

type cacheEntry struct {
	platforms []Platform
	expiry    time.Time
}

type CachedRegistry struct {
	registry      Registry
	cache         sync.Map
	serialize     sync.Map
	cacheDuration time.Duration
	cleanupAccess sync.Mutex
	lastCleanup   time.Time
}

func NewCachedRegistry(registry Registry, cacheDuration time.Duration) *CachedRegistry {
	return &CachedRegistry{
		registry:      registry,
		cacheDuration: cacheDuration,
	}
}

func (cr *CachedRegistry) ListArchs(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
	cacheKey := imagePullSecret + ":" + image

	// Trigger cleanup asynchronously
	defer func() { go cr.cleanUpCache(time.Now()) }()
	cond, _ := cr.serialize.LoadOrStore(cacheKey, &sync.Mutex{})
	serialize := cond.(*sync.Mutex)

	// Serialize requests for the same key
	serialize.Lock()
	defer serialize.Unlock()

	// Check cache
	if entry, ok := cr.cache.Load(cacheKey); ok {
		cached := entry.(*cacheEntry)
		if time.Now().Before(cached.expiry) {
			return cached.platforms, nil
		}
	}

	// Fetch from registry and store in cache
	archs, err := cr.registry.ListArchs(ctx, imagePullSecret, image)
	if err == nil {
		entry := &cacheEntry{
			platforms: archs,
			expiry:    time.Now().Add(cr.cacheDuration),
		}
		cr.cache.Store(cacheKey, entry)

	}

	return archs, err
}

func (cr *CachedRegistry) cleanUpCache(now time.Time) {
	cr.cleanupAccess.Lock()
	defer cr.cleanupAccess.Unlock()
	if now.Before(cr.lastCleanup.Add(5 * time.Minute)) {
		return
	}
	cr.cache.Range(func(key, value interface{}) bool {
		entry := value.(*cacheEntry)
		if now.After(entry.expiry) {
			cr.cache.Delete(key)
			cr.serialize.Delete(key)
		}
		return true
	})
	cr.lastCleanup = now
}
