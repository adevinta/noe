package httputils

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertPrometheusLabels(t *testing.T, metric *dto.Metric, labels prometheus.Labels) {
	t.Helper()
	for _, labelPair := range metric.Label {
		require.NotNil(t, labelPair.Name)
		require.NotNil(t, labelPair.Value)
		assert.Equal(t, labels[labelPair.GetName()], labelPair.GetValue())
	}
	assert.Len(t, metric.Label, len(labels))
}

func TestMonitoredRoundTripperSupportsNilAnswers(t *testing.T) {
	registry := prometheus.NewRegistry()
	rt := NewMonitoredRoundTripper(
		registry,
		prometheus.Opts{Name: "test"},
		StandardRoundTripLabeller,
	)
	rt.Transport = RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, nil
	})
	resp, err := rt.RoundTrip(nil)
	assert.Nil(t, resp)
	assert.NoError(t, err)
}

func TestStandardLabeller(t *testing.T) {
	t.Run("with nil handlers", func(t *testing.T) {
		assert.Equal(
			t,
			prometheus.Labels{
				"host":   "",
				"cached": "false",
			},
			StandardRoundTripLabeller(nil, nil),
		)
	})
	t.Run("uncached responses", func(t *testing.T) {
		header := http.Header{}
		header.Set("X-From-Cache", "1")
		assert.Equal(
			t,
			prometheus.Labels{
				"host":   "",
				"cached": "true",
			},
			StandardRoundTripLabeller(nil, &http.Response{Header: header}),
		)
	})
	t.Run("with no header in response", func(t *testing.T) {
		header := http.Header{}
		header.Set("X-From-Cache", "1")
		assert.Equal(
			t,
			prometheus.Labels{
				"host":   "",
				"cached": "false",
			},
			StandardRoundTripLabeller(nil, &http.Response{}),
		)
	})
	t.Run("with host", func(t *testing.T) {
		header := http.Header{}
		header.Set("X-From-Cache", "1")
		assert.Equal(
			t,
			prometheus.Labels{
				"host":   "my.domain.tld",
				"cached": "false",
			},
			StandardRoundTripLabeller(httptest.NewRequest("GET", "https://my.domain.tld", nil), nil),
		)
	})
}

func TestMonitoredRoundTripperHandlesSuccessfulRequests(t *testing.T) {
	registry := prometheus.NewRegistry()
	rt := NewMonitoredRoundTripper(
		registry,
		prometheus.Opts{Name: "test"},
		StandardRoundTripLabeller,
	)
	rt.Transport = RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		assert.Equal(t, "GET", req.Method)
		assert.Equal(t, "https://www.adevinta.com", req.URL.String())
		return &http.Response{StatusCode: http.StatusAccepted}, nil
	})
	resp, err := rt.RoundTrip(httptest.NewRequest("GET", "https://www.adevinta.com", nil))
	assert.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
}

func TestMonitoredRoundTripperHandlesFailingRequests(t *testing.T) {
	registry := prometheus.NewRegistry()
	rt := NewMonitoredRoundTripper(
		registry,
		prometheus.Opts{Name: "test"},
		StandardRoundTripLabeller,
	)
	rt.Transport = RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("test-error")
	})
	resp, err := rt.RoundTrip(httptest.NewRequest("GET", "https://www.adevinta.com", nil))
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestMonitoredRoundTripper(t *testing.T) {
	registry := prometheus.NewRegistry()
	rt := NewMonitoredRoundTripper(
		registry,
		prometheus.Opts{Name: "test"},
		StandardRoundTripLabeller,
	)
	now = func() time.Time {
		return time.Date(2022, 01, 01, 10, 10, 0, 0, time.UTC)
	}
	rt.Transport = RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		now = func() time.Time {
			return time.Date(2022, 01, 01, 10, 10, 0, 100000000, time.UTC)
		}
		return nil, nil
	})
	_, err := rt.RoundTrip(httptest.NewRequest("GET", "https://www.adevinta.com", nil))
	require.NoError(t, err)
	families, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		switch family.GetName() {
		case "test_count":
			// Do some checks
			for _, metric := range family.Metric {
				assertPrometheusLabels(t, metric, prometheus.Labels{
					"cached": "false",
					"code":   "0",
					"host":   "www.adevinta.com",
				})
				assert.EqualValues(t, 1.0, *metric.Counter.Value)
			}
		case "test_duration_seconds":
			// Do some checks
			for _, metric := range family.Metric {
				assertPrometheusLabels(t, metric, prometheus.Labels{
					"cached": "false",
					"host":   "www.adevinta.com",
				})
				assert.EqualValues(t, 1.0, metric.Histogram.GetSampleCount())
				assert.EqualValues(t, 0.1, metric.Histogram.GetSampleSum())
			}
			// Do some other check
		default:
			t.Errorf("unexpected metric %s", family.GetName())
		}
	}
}

func TestInstrumentHandler(t *testing.T) {
	registry := prometheus.NewRegistry()

	resp := httptest.NewRecorder()
	InstrumentHandler(
		registry,
		prometheus.Opts{Name: "test"},
		StandardHandlerLabeller,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}),
	).ServeHTTP(resp, httptest.NewRequest("GET", "https://www.adevinta.com", nil))
	families, err := registry.Gather()
	require.NoError(t, err)
	foundFamilies := 0
	for _, family := range families {
		foundFamilies++
		switch family.GetName() {
		case "test_count":
			// Do some checks
			for _, metric := range family.Metric {
				assertPrometheusLabels(t, metric, prometheus.Labels{
					"code": strconv.Itoa(http.StatusAccepted),
					"host": "www.adevinta.com",
				})
				assert.EqualValues(t, 1.0, *metric.Counter.Value)
			}
		case "test_duration_seconds":
			// Do some checks
			for _, metric := range family.Metric {
				assertPrometheusLabels(t, metric, prometheus.Labels{
					"code": strconv.Itoa(http.StatusAccepted),
					"host": "www.adevinta.com",
				})
				assert.EqualValues(t, 1.0, metric.Histogram.GetSampleCount())
			}
			// Do some other check
		default:
			t.Errorf("unexpected metric %s", family.GetName())
		}
	}
	assert.Equal(t, 2, foundFamilies)
}
