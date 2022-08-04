package httputils

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForcedCache(t *testing.T) {
	t.Run("existing headers are kept", func(t *testing.T) {
		transport := ForcedCacheTransport{
			Transport: RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				header := http.Header{}
				header.Set("Host", "my-host")
				return &http.Response{
					Header: header,
				}, nil
			}),
		}
		req := httptest.NewRequest("GET", "http://localhost:8080", nil)
		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		assert.Contains(t, resp.Header, "Cache-Control")
		assert.Equal(t, "my-host", resp.Header.Get("Host"))
	})
	t.Run("Cache-Control header is overridden", func(t *testing.T) {
		transport := ForcedCacheTransport{
			Transport: RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				header := http.Header{}
				header.Set("Cache-Control", "previous")
				return &http.Response{
					Header: header,
				}, nil
			}),
		}
		req := httptest.NewRequest("GET", "http://localhost:8080", nil)
		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, "max-age=60", resp.Header.Get("Cache-Control"))
	})
	t.Run("with no specification, default max age is used", func(t *testing.T) {
		transport := ForcedCacheTransport{
			Transport: RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{}, nil
			}),
		}
		req := httptest.NewRequest("GET", "http://localhost:8080", nil)
		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, "max-age=60", resp.Header.Get("Cache-Control"))
	})
	t.Run("when specified, configured max-age is used", func(t *testing.T) {
		transport := ForcedCacheTransport{
			Transport: RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{}, nil
			}),
			CacheDuration: 10 * time.Minute,
		}
		req := httptest.NewRequest("GET", "http://localhost:8080", nil)
		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, "max-age=600", resp.Header.Get("Cache-Control"))
	})
	t.Run("when transport is not specified, default transport is used", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusFound)
		}))
		transport := ForcedCacheTransport{}
		resp, err := transport.RoundTrip(httptest.NewRequest("GET", s.URL, nil))
		require.NoError(t, err)
		assert.Equal(t, http.StatusFound, resp.StatusCode)
	})
	t.Run("when the transport returns an error, the error is returned", func(t *testing.T) {
		transport := ForcedCacheTransport{
			Transport: RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return nil, assert.AnError
			}),
		}
		resp, err := transport.RoundTrip(httptest.NewRequest("GET", "http://localhost:8080", nil))
		assert.Error(t, err)
		assert.Nil(t, resp)
	})
}
