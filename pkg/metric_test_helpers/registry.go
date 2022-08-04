package metric_test_helpers

import (
	"errors"
	"reflect"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type RegistererFuncs struct {
	metrics.RegistererGatherer
	RegisterCount     int
	RegisterFunc      func(registry prometheus.Collector) error
	MustRegisterCount int
	MustRegisterFunc  func(registry ...prometheus.Collector)
}

func (r *RegistererFuncs) Register(registry prometheus.Collector) error {
	r.RegisterCount++
	if r.RegisterFunc == nil {
		return errors.New("RegisterFunc not set")
	}
	return r.RegisterFunc(registry)
}
func (r *RegistererFuncs) MustRegister(registry ...prometheus.Collector) {
	r.MustRegisterCount++
	if r.MustRegisterFunc == nil {
		panic("MustRegisterFunc not set")
	}
	r.MustRegisterFunc(registry...)
}

var _ metrics.RegistererGatherer = &RegistererFuncs{}

type MetricsRegisterer interface {
	MustRegister(reg metrics.RegistererGatherer)
}

func AssertAllMetricsHaveBeenRegistered(t *testing.T, metrics MetricsRegisterer) {
	t.Helper()
	registry := RegistererFuncs{
		MustRegisterFunc: func(collector ...prometheus.Collector) {
			metricDesc := make(chan *prometheus.Desc)
			go func() {
				for _, c := range collector {
					require.NotNil(t, c)
					c.Describe(metricDesc)
				}
				close(metricDesc)
			}()
			allMetrics := []string{}
			for desc := range metricDesc {

				allMetrics = append(allMetrics, desc.String())
			}
			allFields := []string{}
			tpe := reflect.ValueOf(metrics).Elem().Type()
			for i := 0; i < tpe.NumField(); i++ {
				allFields = append(allFields, tpe.Field(i).Name)
			}
			assert.Lenf(t, allMetrics, len(allFields), "some metrics fields %v are not registered to the prometheus registry", allFields)
		},
	}
	metrics.MustRegister(&registry)
	assert.Equal(t, 1, registry.MustRegisterCount)
}
