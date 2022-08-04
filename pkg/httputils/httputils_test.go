package httputils

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRoundTripperFunc(t *testing.T) {
	t.Run("with a nil function, an error is returned", func(t *testing.T) {
		resp, err := RoundTripperFunc(nil).RoundTrip(&http.Request{})
		assert.Error(t, err)
		assert.Nil(t, resp)
	})
	t.Run("with a non-nil function, the function is called", func(t *testing.T) {
		req := &http.Request{
			Method: "GET",
			URL:    &url.URL{Path: "/"},
			Body:   io.NopCloser(strings.NewReader("hello")),
		}
		resp := &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("world")),
		}
		transport := RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, req, r)
			return resp, nil
		})
		actualResp, err := transport.RoundTrip(req)
		assert.NoError(t, err)
		assert.Equal(t, resp, actualResp)
	})
}
