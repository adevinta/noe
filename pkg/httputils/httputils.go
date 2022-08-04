package httputils

import (
	"errors"
	"net/http"
)

// RoundTripperFunc implements the http.RoundTripper interface for a given function
type RoundTripperFunc func(r *http.Request) (*http.Response, error)

func (t RoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	if t == nil {
		return nil, errors.New("nil function provided")
	}
	return t(r)
}

// RoundTripperFunc must implement the http.RoundTripper interface
var _ http.RoundTripper = RoundTripperFunc(http.DefaultTransport.RoundTrip)

func nextRoundTripper(transport http.RoundTripper) http.RoundTripper {
	if transport != nil {
		return transport
	}
	return http.DefaultTransport
}
