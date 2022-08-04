package httputils

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ForcedCacheTransport forces the cache headers of http responses
// so subsequent caching libraries can cache the responses regardless of their original contents
type ForcedCacheTransport struct {
	Transport     http.RoundTripper
	CacheDuration time.Duration
}

func (t *ForcedCacheTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	resp, err = nextRoundTripper(t.Transport).RoundTrip(req)
	if err != nil {
		return nil, err
	}
	duration := 1 * time.Minute
	if t.CacheDuration > 0 {
		duration = t.CacheDuration
	}
	if resp != nil {
		if resp.Header == nil {
			resp.Header = http.Header{}
		}
		resp.Header.Set("Cache-Control", fmt.Sprintf("max-age=%s", strconv.Itoa(int(duration.Seconds()))))
	}
	return
}
