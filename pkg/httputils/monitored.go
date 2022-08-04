package httputils

import (
	"net/http"
	"strconv"
	"time"

	"github.com/adevinta/noe/pkg/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

var (
	now = time.Now
)

type MonitoredRoundTripper struct {
	// Transport allows to customize the underneath RoundTripper
	// If not provided, http.DefaultTransport will be used
	Transport    http.RoundTripper
	CallsCounter *prometheus.CounterVec
	Timings      prometheus.HistogramVec
	Labeller     func(req *http.Request, resp *http.Response) prometheus.Labels
}

func NewMonitoredRoundTripper(reg prometheus.Registerer, metricsOpts prometheus.Opts, labeller func(req *http.Request, resp *http.Response) prometheus.Labels) *MonitoredRoundTripper {
	labelNames := []string{}
	for key := range labeller(nil, nil) {
		labelNames = append(labelNames, key)
	}
	monitor := &MonitoredRoundTripper{
		Labeller: labeller,
		CallsCounter: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace:   metricsOpts.Namespace,
			Subsystem:   metricsOpts.Subsystem,
			Name:        metricsOpts.Name + "_count",
			Help:        metricsOpts.Help,
			ConstLabels: metricsOpts.ConstLabels,
		}, append(labelNames, "code")),
		Timings: *prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace:   metricsOpts.Namespace,
			Subsystem:   metricsOpts.Subsystem,
			Name:        metricsOpts.Name + "_duration_seconds",
			Help:        metricsOpts.Help,
			ConstLabels: metricsOpts.ConstLabels,
		}, labelNames),
	}
	reg.MustRegister(
		monitor.CallsCounter,
		monitor.Timings,
	)
	return monitor
}

func (t *MonitoredRoundTripper) RoundTrip(req *http.Request) (resp *http.Response, err error) {

	start := now()
	defer func() {
		labels := t.Labeller(req, resp)
		t.Timings.With(labels).Observe(now().Sub(start).Seconds())
		statusCode := "0"
		if resp != nil {
			statusCode = strconv.Itoa(resp.StatusCode)
		}
		labels["code"] = statusCode
		t.CallsCounter.With(labels).Inc()
	}()
	resp, err = nextRoundTripper(t.Transport).RoundTrip(req)
	return
}

type trackCodeResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (t *trackCodeResponseWriter) WriteHeader(statusCode int) {
	t.ResponseWriter.WriteHeader(statusCode)
	if t.statusCode == 0 {
		t.statusCode = statusCode
	}
}

func InstrumentHandler(reg prometheus.Registerer, metricsOpts prometheus.Opts, labeller func(req *http.Request, resp *http.Response) prometheus.Labels, h http.Handler) http.Handler {
	labelNames := []string{}
	for key := range labeller(nil, nil) {
		labelNames = append(labelNames, key)
	}
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace:   metricsOpts.Namespace,
		Subsystem:   metricsOpts.Subsystem,
		Name:        metricsOpts.Name + "_count",
		Help:        metricsOpts.Help,
		ConstLabels: metricsOpts.ConstLabels,
	}, append(labelNames, "code"))
	timer := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace:   metricsOpts.Namespace,
		Subsystem:   metricsOpts.Subsystem,
		Name:        metricsOpts.Name + "_duration_seconds",
		Help:        metricsOpts.Help,
		ConstLabels: metricsOpts.ConstLabels,
	}, append(labelNames, "code"))
	reg.MustRegister(counter, timer)

	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"method": req.Method, "path": req.URL.Path})
		tracked := &trackCodeResponseWriter{
			ResponseWriter: resp,
			statusCode:     0,
		}
		req = req.WithContext(ctx)
		start := time.Now()
		defer func() {
			elapsed := time.Since(start)
			labels := labeller(req, nil) // TODO: add response
			labels["code"] = strconv.Itoa(tracked.statusCode)
			counter.With(labels).Inc()
			timer.With(labels).Observe(elapsed.Seconds())
			log.DefaultLogger.WithContext(req.Context()).WithField("duration", elapsed).WithField("status", tracked.statusCode).Trace("request processed")
		}()
		h.ServeHTTP(tracked, req)
	})
}

func StandardRoundTripLabeller(req *http.Request, resp *http.Response) prometheus.Labels {
	if req == nil {
		req = &http.Request{}
	}
	if resp == nil {
		resp = &http.Response{}
	}
	cached := "false"
	if resp.Header != nil {
		if _, ok := resp.Header["X-From-Cache"]; ok {
			cached = "true"
		}
	}
	return prometheus.Labels{
		"host":   req.Host,
		"cached": cached,
	}
}

func StandardHandlerLabeller(req *http.Request, resp *http.Response) prometheus.Labels {
	if req == nil {
		req = &http.Request{}
	}
	if resp == nil {
		resp = &http.Response{}
	}
	return prometheus.Labels{
		"host": req.Host,
	}
}
