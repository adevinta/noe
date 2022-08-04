package httputils

import (
	"net/http"
	"sync"
)

type serialisedRequest struct {
	request  *http.Request
	response *http.Response
	err      error
	wg       sync.WaitGroup
}

// RateLimitedRoundTripper ensures similar requests are serialised instead of run in parallel
// A tipical use is to use this in front of a caching layer to reduce the request rate to the upstream services
type RateLimitedRoundTripper struct {
	// Transport allows to customize the underneath RoundTripper
	// If not provided, http.DefaultTransport will be used
	Transport http.RoundTripper
	// KeyFunc allows to group how requests will be grouped for rate limiting
	// the default behaviour returns the request method with full URL
	// ensuring that all calls to a specific endpoint will be serialized
	KeyFunc              func(req *http.Request) string
	ConcurrentCallsLimit int

	access        sync.Mutex
	requestsChans map[string]chan *serialisedRequest
	inFlights     map[string]int
}

func (t *RateLimitedRoundTripper) handleRequests(serialisedRequests chan *serialisedRequest) {
	for serialisedRequest := range serialisedRequests {
		serialisedRequest.response, serialisedRequest.err = nextRoundTripper(t.Transport).RoundTrip(serialisedRequest.request)
		serialisedRequest.wg.Done()
	}
}

func (t *RateLimitedRoundTripper) getRequestChan(key string) chan *serialisedRequest {
	t.access.Lock()
	if t.requestsChans == nil {
		t.requestsChans = map[string]chan *serialisedRequest{}
		t.inFlights = map[string]int{}
	}
	requestsChan, ok := t.requestsChans[key]
	if !ok {
		requestsChan = make(chan *serialisedRequest)
		t.requestsChans[key] = requestsChan
		limit := t.ConcurrentCallsLimit
		if limit < 1 {
			limit = 1
		}
		for i := 0; i < limit; i++ {
			go func(requestsChan chan *serialisedRequest) {
				t.handleRequests(requestsChan)
			}(requestsChan)
		}
	}

	t.inFlights[key]++
	t.access.Unlock()
	return requestsChan
}

func (t *RateLimitedRoundTripper) done(key string) {
	t.access.Lock()
	defer t.access.Unlock()
	requestsChan, ok := t.requestsChans[key]
	if ok {
		t.inFlights[key]--
	}
	if t.inFlights[key] == 0 {
		if ok {
			close(requestsChan)
		}
		delete(t.requestsChans, key)
		delete(t.inFlights, key)
	}
}

func (t *RateLimitedRoundTripper) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	keyFunc := func(req *http.Request) string {
		return req.Method + " " + req.URL.String()
	}
	if t.KeyFunc != nil {
		keyFunc = t.KeyFunc
	}
	serialized := serialisedRequest{
		request: req,
		wg:      sync.WaitGroup{},
	}
	serialized.wg.Add(1)
	key := keyFunc(req)
	t.getRequestChan(key) <- &serialized
	defer t.done(key)
	serialized.wg.Wait()
	resp, err = serialized.response, serialized.err
	return
}
