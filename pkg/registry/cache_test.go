package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adevinta/noe/pkg/metric_test_helpers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllMetricsShouldBeRegistered(t *testing.T) {
	metrics := NewCacheMetrics("test", "registry")
	metric_test_helpers.AssertAllMetricsHaveBeenRegistered(t, metrics)
}

func TestCachedRegistry(t *testing.T) {
	platforms := []Platform{
		{Architecture: "amd64", OS: "linux"},
	}

	registryFunc := RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
		return platforms, nil
	})

	cacheDuration := 1 * time.Hour

	ctx := context.Background()

	t.Run("cache miss", func(t *testing.T) {

		cr := NewCachedRegistry(registryFunc, cacheDuration)
		cr.registry = RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
			assert.Equal(t, "secret", imagePullSecret)
			assert.Equal(t, "image", image)
			return []Platform{
				{Architecture: "amd64", OS: "linux"},
			}, nil
		})
		archs, err := cr.ListArchs(ctx, "secret", "image")
		assert.NoError(t, err)
		assert.Equal(t, platforms, archs)
	})

	t.Run("cache hit", func(t *testing.T) {

		cr := NewCachedRegistry(registryFunc, cacheDuration)
		// warm up cache
		cr.registry = RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
			assert.Equal(t, "secret", imagePullSecret)
			assert.Equal(t, "image", image)
			return []Platform{
				{Architecture: "amd64", OS: "linux"},
			}, nil
		})
		_, err := cr.ListArchs(ctx, "secret", "image")
		require.NoError(t, err)

		cr.registry = RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
			assert.Fail(t, "registry should not be called")
			return nil, errors.New("Registry should not be called")
		})
		archs, err := cr.ListArchs(ctx, "secret", "image")
		assert.NoError(t, err)
		assert.Equal(t, platforms, archs)
	})

	t.Run("concurrent calls with the same arguments benefits from cache", func(t *testing.T) {

		cr := NewCachedRegistry(registryFunc, cacheDuration)
		inFlight := sync.WaitGroup{}
		done := sync.WaitGroup{}
		calls := int32(0)

		cr.registry = RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
			inFlight.Wait()
			fmt.Println("all requests are in flight")
			atomic.AddInt32(&calls, 1)
			assert.Equal(t, "secret", imagePullSecret)
			assert.Equal(t, "image", image)
			return []Platform{
				{Architecture: "amd64", OS: "linux"},
			}, nil
		})
		for i := 0; i < 10; i++ {
			inFlight.Add(1)
			done.Add(1)
			go func(i int) {
				fmt.Println("sending ListArchs request ", i)
				inFlight.Done()
				defer done.Done()
				archs, err := cr.ListArchs(ctx, "secret", "image")
				assert.NoError(t, err)
				assert.Equal(t, platforms, archs)
				fmt.Println("ListArch request ", i, " is done")
			}(i)
		}
		done.Wait()
		fmt.Println("all ListArchs requests are done")
		assert.Equal(t, 1, int(calls))
	})

	t.Run("concurrent calls with the different arguments are made in parallel", func(t *testing.T) {
		cr := NewCachedRegistry(registryFunc, cacheDuration)
		done := sync.WaitGroup{}
		calls := int32(0)

		inFlight := int32(1)

		cr.registry = RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
			fmt.Println("request for ", imagePullSecret, image, " is in flight")
			defer fmt.Println("request for ", imagePullSecret, image, " is done")
			atomic.AddInt32(&inFlight, 1)
			atomic.AddInt32(&calls, 1)
			assert.Eventuallyf(
				t,
				func() bool {
					return atomic.LoadInt32(&inFlight) > 5
				}, 1*time.Second, 10*time.Millisecond,
				"multiple requests should be in flight at the same time",
			)
			return []Platform{
				{Architecture: "amd64", OS: "linux"},
			}, nil
		})
		for i := 0; i < 10; i++ {
			done.Add(1)
			go func(i int) {
				defer done.Done()
				archs, err := cr.ListArchs(ctx, "secret", "image-"+fmt.Sprint(i))
				assert.NoError(t, err)
				assert.Equal(t, platforms, archs)
				archs, err = cr.ListArchs(ctx, "secret-"+fmt.Sprint(i), "image")
				assert.NoError(t, err)
				assert.Equal(t, platforms, archs)
				fmt.Println("ListArch request ", i, " is done")
			}(i)
		}
		done.Wait()
		fmt.Println("all ListArchs requests are done")
		assert.Equal(t, 20, int(calls))
	})

	t.Run("cache expiry", func(t *testing.T) {

		cr := NewCachedRegistry(registryFunc, cacheDuration)
		cr.registry = RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
			assert.Equal(t, "secret", imagePullSecret)
			assert.Equal(t, "image", image)
			return []Platform{
				{Architecture: "amd64", OS: "linux"},
			}, nil
		})
		_, err := cr.ListArchs(ctx, "secret", "image")
		require.NoError(t, err)

		cr.cache.CacheDuration = 1 * time.Millisecond

		var lastCleanUp time.Time
		assert.Eventuallyf(t, func() bool {
			lastCleanUp = cr.cache.lastCleanup
			return cr.cache.lastCleanup.Unix() > 0
		}, 1*time.Second, 10*time.Millisecond, "cleanup should be called after call to ListArchs")

		cr.cache.CleanUp(lastCleanUp)
		_, ok := cr.cache.Load("secret:image")
		assert.True(t, ok, "cache entry not be removed when cleaning up too early")

		cr.cache.CleanUp(lastCleanUp.Add(2 * time.Hour))
		_, ok = cr.cache.Load("secret:image")
		assert.False(t, ok, "cache entry should be removed after cleanUpCache")
	})

	t.Run("error case", func(t *testing.T) {

		cr := NewCachedRegistry(registryFunc, cacheDuration)
		calls := 0
		errorRegistryFunc := RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
			calls++
			return nil, errors.New("error")
		})
		cr.registry = errorRegistryFunc
		archs, err := cr.ListArchs(ctx, "secret", "image-in-error")
		assert.Error(t, err)
		assert.Nil(t, archs)
		archs, err = cr.ListArchs(ctx, "secret", "image-in-error")
		assert.Error(t, err)
		assert.Nil(t, archs)
		assert.Equal(t, 2, calls, "Errors should not be cached, registry should be called twice")
	})
}

func TestCacheCleanup(t *testing.T) {
	cache := Cache[string]{}
	now := time.Now()
	value := func(s string) *string {
		return &s

	}
	cache.Store("expiredKey1", value("expiredValue1"), cache.WithExpiry(now.Add(-1*time.Second)))
	cache.Store("expiredKey2", value("expiredValue2"), cache.WithExpiry(now.Add(-10*time.Second)))
	cache.Store("expiredKey3", value("expiredValue3"), cache.WithExpiry(now.Add(-10*time.Hour)))
	cache.Store("notExpiredKey1", value("notExpiredValue1"), cache.WithExpiry(now.Add(1*time.Second)))
	cache.Store("notExpiredKey2", value("notExpiredValue2"), cache.WithExpiry(now.Add(10*time.Second)))
	cache.Store("notExpiredKey3", value("notExpiredValue3"), cache.WithExpiry(now.Add(10*time.Hour)))

	t.Run("When the cache has no cleanup period, the default one is considered", func(t *testing.T) {
		cache.lastCleanup = now
		cache.CleanUp(now)
		assert.Equal(t, 5*time.Minute, cache.CleanUpPeriod)
	})

	t.Run("When the cache was cleaned up recently, it should not be cleaned up again", func(t *testing.T) {
		cache.lastCleanup = now
		cache.CleanUpPeriod = 1 * time.Hour
		cache.CleanUp(now)
		_, ok := cache.cache.Load("expiredKey1")
		assert.True(t, ok)
		_, ok = cache.cache.Load("expiredKey2")
		assert.True(t, ok)
		_, ok = cache.cache.Load("expiredKey3")
		assert.True(t, ok)
	})

	t.Run("When the cache was cleaned up recently, it should not be cleaned up again", func(t *testing.T) {
		cache.lastCleanup = now.Add(-2 * time.Hour)
		cache.CleanUpPeriod = 1 * time.Hour
		cache.CleanUp(now)
		_, ok := cache.cache.Load("expiredKey1")
		assert.False(t, ok)
		_, ok = cache.cache.Load("expiredKey2")
		assert.False(t, ok)
		_, ok = cache.cache.Load("expiredKey3")
		assert.False(t, ok)
	})
	t.Run("The lastCleanupField is updated after the cache is cleaned up", func(t *testing.T) {
		cache.lastCleanup = time.Time{}
		cache.CleanUp(now.Add(2 * time.Hour))
		assert.Equal(t, now.Add(2*time.Hour), cache.lastCleanup)
	})
}
