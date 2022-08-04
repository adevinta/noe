package httputils

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultRateLimitedRoundTripper(t *testing.T) {
	transport := &RateLimitedRoundTripper{}
	allDone := sync.WaitGroup{}
	allInFlight := sync.WaitGroup{}
	concurrentCalls := int32(0)
	remaining := int32(10)
	transport.Transport = RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&concurrentCalls, 1)
		defer func() {
			atomic.AddInt32(&concurrentCalls, -1)
			atomic.AddInt32(&remaining, -1)
		}()
		assert.LessOrEqual(t, concurrentCalls, int32(1))
		// wait for all the requests to have been sent
		allInFlight.Wait()
		time.Sleep(20 * time.Millisecond)
		assert.Equal(t, int(remaining), transport.inFlights["GET http://localhost:8080/some"])
		return &http.Response{
			Header: r.Header,
		}, nil
	})
	allInFlight.Add(1)
	for i := 0; i < 10; i++ {
		allDone.Add(1)
		go func(i int) {
			req := httptest.NewRequest("GET", "http://localhost:8080/some", nil)
			req.Header.Set("X-Test-Request-Number", strconv.Itoa(i))
			resp, err := transport.RoundTrip(req)
			require.NoError(t, err)
			assert.Equal(t, strconv.Itoa(i), resp.Header.Get("X-Test-Request-Number"))
			allDone.Done()
		}(i)
	}
	allInFlight.Done()
	allDone.Wait()
}

func TestRateLimitedRoundTripperDoesNotSerializeDifferentRequests(t *testing.T) {
	transport := &RateLimitedRoundTripper{}
	allDone := sync.WaitGroup{}
	allInFlight := sync.WaitGroup{}
	concurrentCalls := int32(0)
	transport.Transport = RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&concurrentCalls, 1)
		defer func() {
			atomic.AddInt32(&concurrentCalls, -1)
		}()
		assert.LessOrEqual(t, concurrentCalls, int32(10))
		// wait for all the requests to have been sent
		allInFlight.Wait()

		transport.access.Lock()
		assert.Equal(t, 1, transport.inFlights["GET "+r.URL.String()])
		transport.access.Unlock()
		time.Sleep(20 * time.Millisecond)
		return &http.Response{
			Header: r.Header,
		}, nil
	})
	allInFlight.Add(1)
	for i := 0; i < 10; i++ {
		allDone.Add(1)
		go func(i int) {
			req := httptest.NewRequest("GET", "http://localhost:8080/some/"+strconv.Itoa(i), nil)
			req.Header.Set("X-Test-Request-Number", strconv.Itoa(i))
			resp, err := transport.RoundTrip(req)
			require.NoError(t, err)
			assert.Equal(t, strconv.Itoa(i), resp.Header.Get("X-Test-Request-Number"))
			allDone.Done()
		}(i)
	}
	allInFlight.Done()
	allDone.Wait()
}

func TestCustomizedRateLimitedRoundTripper(t *testing.T) {
	transport := &RateLimitedRoundTripper{
		KeyFunc: func(r *http.Request) string {
			return r.Host
		},
		ConcurrentCallsLimit: 3,
	}
	allDone := sync.WaitGroup{}
	allInFlight := sync.WaitGroup{}
	concurrentCalls := int32(0)
	transport.Transport = RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&concurrentCalls, 1)
		defer func() {
			atomic.AddInt32(&concurrentCalls, -1)
		}()
		assert.LessOrEqual(t, concurrentCalls, int32(3))
		// wait for all the requests to have been sent
		allInFlight.Wait()
		time.Sleep(20 * time.Millisecond)
		return &http.Response{
			Header: r.Header,
		}, nil
	})
	allInFlight.Add(1)
	for i := 0; i < 10; i++ {
		allDone.Add(1)
		go func(i int) {
			req := httptest.NewRequest("GET", "http://localhost:8080/some/"+strconv.Itoa(i), nil)
			req.Header.Set("X-Test-Request-Number", strconv.Itoa(i))
			resp, err := transport.RoundTrip(req)
			require.NoError(t, err)
			assert.Equal(t, strconv.Itoa(i), resp.Header.Get("X-Test-Request-Number"))
			allDone.Done()
		}(i)
	}
	allInFlight.Done()
	allDone.Wait()
}
