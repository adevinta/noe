package registry

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

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

func NewCacheMetrics(prefix string) *CacheMetrics {
	m := &CacheMetrics{
		Requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: "registry_cache",
			Name:      "requests_total",
			Help:      "Number of requests to the registry cache",
		}, []string{}),
		Responses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: "registry_cache",
			Name:      "responses_total",
			Help:      "Number of request responses from the registry cache",
		}, []string{"cache"}),
	}
	return m
}

type cacheEntry struct {
	platforms []Platform
	expiry    time.Time
}

type CacheOption func(*CachedRegistry)
type CachedRegistry struct {
	registry      Registry
	cache         sync.Map
	serialize     sync.Map
	cacheDuration time.Duration
	cleanupAccess sync.Mutex
	lastCleanup   time.Time
	metrics       *CacheMetrics
}

func NewCachedRegistry(registry Registry, cacheDuration time.Duration, opts ...CacheOption) *CachedRegistry {
	r := &CachedRegistry{
		registry:      registry,
		cacheDuration: cacheDuration,
		metrics:       NewCacheMetrics("noe"),
	}
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
			cr.metrics.Responses.WithLabelValues("hit").Inc()
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
		cr.metrics.Responses.WithLabelValues("miss").Inc()
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
