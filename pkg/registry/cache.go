package registry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adevinta/noe/pkg/log"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type CacheEntry[T any] struct {
	Value  *T
	Expiry time.Time
}

type Cache[T any] struct {
	cache         sync.Map
	serialize     sync.Map
	cleanupAccess sync.Mutex
	lastCleanup   time.Time
	CleanUpPeriod time.Duration
	CacheDuration time.Duration
}

func NewCache[T any](cacheDuration time.Duration) *Cache[T] {
	return &Cache[T]{
		CacheDuration: cacheDuration,
	}
}

func (c *Cache[T]) Load(cacheKey string) (*T, bool) {
	if entry, ok := c.cache.Load(cacheKey); ok {
		cached := entry.(*CacheEntry[T])
		if time.Now().Before(cached.Expiry) {
			return cached.Value, true
		}
	}
	return nil, false
}

func (c *Cache[T]) LoadOrCall(cacheKey string, miss func() (T, error)) (T, bool, error) {
	cond, _ := c.serialize.LoadOrStore(cacheKey, &sync.Mutex{})
	serialize := cond.(*sync.Mutex)

	// Serialize requests for the same key
	serialize.Lock()
	defer serialize.Unlock()

	if entry, ok := c.Load(cacheKey); ok {
		return *entry, true, nil
	}

	value, err := miss()
	if err != nil {
		return value, false, err
	}
	c.Store(cacheKey, &value)
	return value, false, nil
}

func (c *Cache[T]) Store(cacheKey string, value *T, mutations ...func(*CacheEntry[T])) {
	entry := &CacheEntry[T]{
		Value:  value,
		Expiry: time.Now().Add(c.CacheDuration),
	}
	for _, mutation := range mutations {
		mutation(entry)
	}
	c.cache.Store(cacheKey, entry)
}

func (c *Cache[T]) CleanUp(now time.Time) {
	c.cleanupAccess.Lock()
	defer c.cleanupAccess.Unlock()
	if c.CleanUpPeriod == 0 {
		log.DefaultLogger.Debug("setting default cleanup period to 5 minutes")
		c.CleanUpPeriod = 5 * time.Minute
	}
	if now.Before(c.lastCleanup.Add(c.CleanUpPeriod)) {
		log.DefaultLogger.WithField("cleanupPeriod", c.CleanUpPeriod).Debug("Not enough time passed since last cleanup, skipping")
		return
	}
	c.cache.Range(func(key, value interface{}) bool {
		entry := value.(*CacheEntry[T])
		if now.After(entry.Expiry) {
			log.DefaultLogger.WithField("key", key).WithField("expire", entry.Expiry).Debug("cleaning up expired cache entry")
			c.cache.Delete(key)
			c.serialize.Delete(key)
		}
		return true
	})
	c.lastCleanup = now
}

func (c *Cache[T]) WithExpiry(expiry time.Time) func(*CacheEntry[T]) {
	return func(entry *CacheEntry[T]) {
		entry.Expiry = expiry
	}
}

type CacheMetrics struct {
	Requests  *prometheus.CounterVec
	Responses *prometheus.CounterVec
}

func (m CacheMetrics) MustRegister(reg metrics.RegistererGatherer) {
	reg.MustRegister(
		m.Requests,
		m.Responses,
	)
}

func NewCacheMetrics(prefix, system string) *CacheMetrics {
	m := &CacheMetrics{
		Requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: system,
			Name:      "cache_requests_total",
			Help:      fmt.Sprintf("Number of requests to the %s cache", system),
		}, []string{}),
		Responses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: system,
			Name:      "cache_responses_total",
			Help:      fmt.Sprintf("Number of request responses from the %s cache", system),
		}, []string{"cache"}),
	}
	return m
}

type CacheOption func(*CachedRegistry)
type CachedRegistry struct {
	cache    Cache[[]Platform]
	registry Registry
	metrics  *CacheMetrics
}

func NewCachedRegistry(registry Registry, cacheDuration time.Duration, opts ...CacheOption) *CachedRegistry {
	r := &CachedRegistry{
		registry: registry,
		metrics:  NewCacheMetrics("noe", "registry"),
	}
	r.cache.CacheDuration = cacheDuration
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func WithCacheMetricsRegistry(reg metrics.RegistererGatherer) CacheOption {
	return func(h *CachedRegistry) {
		h.metrics.MustRegister(reg)
	}
}

func (cr *CachedRegistry) ListArchs(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
	cr.metrics.Requests.WithLabelValues().Inc()
	cacheKey := imagePullSecret + ":" + image

	// Trigger a cleanup of the cache, but don't wait for it to finish
	// Waiting for the cleanup to finish would block the request and
	// slow down the response time.
	defer func() { go cr.cache.CleanUp(time.Now()) }()

	archs, cached, err := cr.cache.LoadOrCall(cacheKey, func() ([]Platform, error) {
		archs, err := cr.registry.ListArchs(ctx, imagePullSecret, image)
		if err != nil {
			return nil, err
		}
		return archs, nil
	})
	if err != nil {
		return nil, err
	}
	if cached {
		cr.metrics.Responses.WithLabelValues("hit").Inc()
	} else {
		cr.metrics.Responses.WithLabelValues("miss").Inc()
	}
	return archs, nil
}
