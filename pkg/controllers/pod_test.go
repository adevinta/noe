package controllers_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/adevinta/noe/pkg/arch"
	"github.com/adevinta/noe/pkg/controllers"
	"github.com/adevinta/noe/pkg/metric_test_helpers"
	"github.com/adevinta/noe/pkg/registry"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestAllMetricsShouldBeRegistered(t *testing.T) {
	metrics := controllers.NewHandlerMetrics("test")
	metric_test_helpers.AssertAllMetricsHaveBeenRegistered(t, metrics)
}

func TestReconcileWhenAPodIsAdded(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithObjects(
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-1",
				Namespace: "ns",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "test-image",
					},
				},
			},
		},
	).Build()

	metricsRegistry := prometheus.NewRegistry()

	reconciler := controllers.NewPodReconciler(
		"test",
		controllers.WithClient(k8sClient),
		controllers.WithMetricsRegistry(metricsRegistry),
		controllers.WithRegistry(arch.RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
			assert.Equal(t, "test-image", image)
			return []registry.Platform{
				{
					OS:           "linux",
					Architecture: "amd64",
				},
				{
					OS:           "linux",
					Architecture: "arm64",
				},
			}, nil
		})))

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pod-1", Namespace: "ns"}})
	assert.NoError(t, err)
	// A pod should be considered only once.
	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pod-1", Namespace: "ns"}})
	assert.NoError(t, err)

	checkMetricRegistry(
		t,
		metricsRegistry,
		"test_images_count",
		func(t *testing.T, family *dto.MetricFamily) {
			assert.Len(t, family.Metric, 2)
			checkMetricValueForLabels(
				t,
				family.Metric,
				prometheus.Labels{
					"arch":    "amd64",
					"os":      "linux",
					"image":   "test-image",
					"variant": "",
				},
				func(t *testing.T, metric *dto.Metric) {
					assert.EqualValues(t, 1.0, *metric.Gauge.Value)
				},
			)
			checkMetricValueForLabels(
				t,
				family.Metric,
				prometheus.Labels{
					"arch":    "arm64",
					"os":      "linux",
					"image":   "test-image",
					"variant": "",
				},
				func(t *testing.T, metric *dto.Metric) {
					assert.EqualValues(t, 1.0, *metric.Gauge.Value)
				},
			)
		},
	)
}

func TestReconcileWhenOtherPodsAreAddedAndImageHasChanged(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithObjects(
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-1",
				Namespace: "ns",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "test-image",
					},
				},
			},
		},
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-2",
				Namespace: "ns",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "test-image",
					},
				},
			},
		},
	).Build()

	metricsRegistry := prometheus.NewRegistry()

	reconciler := controllers.NewPodReconciler(
		"test",
		controllers.WithClient(k8sClient),
		controllers.WithMetricsRegistry(metricsRegistry),
		controllers.WithRegistry(arch.RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
			assert.Equal(t, "test-image", image)
			return []registry.Platform{
				{
					OS:           "linux",
					Architecture: "amd64",
				},
				{
					OS:           "linux",
					Architecture: "arm64",
				},
			}, nil
		})))

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pod-1", Namespace: "ns"}})
	assert.NoError(t, err)

	// image has changed
	controllers.WithRegistry(arch.RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
		assert.Equal(t, "test-image", image)
		return []registry.Platform{
			{
				OS:           "linux",
				Architecture: "amd64",
			},
		}, nil
	}))(reconciler)

	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pod-2", Namespace: "ns"}})
	assert.NoError(t, err)

	checkMetricRegistry(
		t,
		metricsRegistry,
		"test_images_count",
		func(t *testing.T, family *dto.MetricFamily) {
			assert.Len(t, family.Metric, 1)
			checkMetricValueForLabels(
				t,
				family.Metric,
				prometheus.Labels{
					"arch":    "amd64",
					"os":      "linux",
					"image":   "test-image",
					"variant": "",
				},
				func(t *testing.T, metric *dto.Metric) {
					assert.EqualValues(t, 2.0, *metric.Gauge.Value)
				},
			)
		},
	)
}

func TestReconcileWhenAPodIsDeletedButOtherRemains(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithObjects(
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-1",
				Namespace: "ns",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "test-image",
					},
				},
			},
		},
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-2",
				Namespace: "ns",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "test-image",
					},
				},
			},
		},
	).Build()

	metricsRegistry := prometheus.NewRegistry()

	reconciler := controllers.NewPodReconciler(
		"test",
		controllers.WithClient(k8sClient),
		controllers.WithMetricsRegistry(metricsRegistry),
		controllers.WithRegistry(arch.RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
			assert.Equal(t, "test-image", image)
			return []registry.Platform{
				{
					OS:           "linux",
					Architecture: "amd64",
				},
				{
					OS:           "linux",
					Architecture: "arm64",
				},
			}, nil
		})))

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pod-1", Namespace: "ns"}})
	assert.NoError(t, err)
	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pod-2", Namespace: "ns"}})
	assert.NoError(t, err)

	require.NoError(t, k8sClient.Delete(
		context.Background(),
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-1",
				Namespace: "ns",
			},
		},
	))

	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pod-1", Namespace: "ns"}})
	assert.NoError(t, err)

	checkMetricRegistry(
		t,
		metricsRegistry,
		"test_images_count",
		func(t *testing.T, family *dto.MetricFamily) {
			assert.Len(t, family.Metric, 2)
			checkMetricValueForLabels(
				t,
				family.Metric,
				prometheus.Labels{
					"arch":    "amd64",
					"os":      "linux",
					"image":   "test-image",
					"variant": "",
				},
				func(t *testing.T, metric *dto.Metric) {
					assert.EqualValues(t, 1.0, *metric.Gauge.Value)
				},
			)
			checkMetricValueForLabels(
				t,
				family.Metric,
				prometheus.Labels{
					"arch":    "arm64",
					"os":      "linux",
					"image":   "test-image",
					"variant": "",
				},
				func(t *testing.T, metric *dto.Metric) {
					assert.EqualValues(t, 1.0, *metric.Gauge.Value)
				},
			)
		},
	)
}

func TestReconcileWhenTheLastPodIsDeleted(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithObjects(
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-1",
				Namespace: "ns",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "test-image",
					},
				},
			},
		},
	).Build()

	metricsRegistry := prometheus.NewRegistry()

	reconciler := controllers.NewPodReconciler(
		"test",
		controllers.WithClient(k8sClient),
		controllers.WithMetricsRegistry(metricsRegistry),
		controllers.WithRegistry(arch.RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
			assert.Equal(t, "test-image", image)
			return []registry.Platform{
				{
					OS:           "linux",
					Architecture: "amd64",
				},
				{
					OS:           "linux",
					Architecture: "arm64",
				},
			}, nil
		})))

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pod-1", Namespace: "ns"}})
	assert.NoError(t, err)

	require.NoError(t, k8sClient.Delete(
		context.Background(),
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-1",
				Namespace: "ns",
			},
		},
	))

	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pod-1", Namespace: "ns"}})
	assert.NoError(t, err)

	checkNoMetric(
		t,
		metricsRegistry,
		"test_images_count",
	)
}

func TestReconcileShouldDeletePodsOnMismatchingNodes(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithObjects(
		&appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "deployment-1",
				Namespace: "ns",
				UID:       "deployment-1-uid",
			},
		},
		&v1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "deployment-1",
						UID:        "deployment-1-uid",
						Controller: pointer.BoolPtr(true),
					},
				},
				Name:      "test-pod-1",
				Namespace: "ns",
				UID:       "test-pod-1-uid",
			},
			Spec: v1.PodSpec{
				NodeName: "node-1",
				Containers: []v1.Container{
					{
						Image: "test-image",
					},
				},
			},
		},
		&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				Labels: map[string]string{
					"beta.kubernetes.io/arch": "amd64",
					"beta.kubernetes.io/os":   "linux",
				},
			},
		},
	).Build()

	metricsRegistry := prometheus.NewRegistry()

	reconciler := controllers.NewPodReconciler(
		"test",
		controllers.WithClient(k8sClient),
		controllers.WithMetricsRegistry(metricsRegistry),
		controllers.WithRegistry(arch.RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
			assert.Equal(t, "test-image", image)
			return []registry.Platform{
				{
					OS:           "linux",
					Architecture: "arm64",
				},
			}, nil
		})))

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pod-1", Namespace: "ns"}})
	assert.NoError(t, err)

	checkMetricRegistry(
		t,
		metricsRegistry,
		"test_pods_deletion_total",
		func(t *testing.T, family *dto.MetricFamily) {
			checkMetricValueForLabels(
				t,
				family.Metric,
				prometheus.Labels{
					"namespace": "ns",
					"status":    "success",
				},
				func(t *testing.T, metric *dto.Metric) {
					assert.EqualValues(t, 1.0, *metric.Counter.Value)
				},
			)
		},
	)
	err = k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "pod-1"}, &v1.Pod{})
	assert.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err))

	evts := v1.EventList{}
	err = k8sClient.List(context.Background(), &evts)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(evts.Items), 2)
	for _, evt := range evts.Items {
		assert.Regexp(t, ".*-deleted-pod", evt.Name)
		assert.Equal(t, "Normal", evt.Type)
		assert.Equal(t, "PlatformMismatch", evt.Reason)
		assert.Equal(t, "noe", evt.Source.Component)
		assert.Equal(t, "ns", evt.Namespace)
		assert.NotEmpty(t, evt.FirstTimestamp)
		assert.NotEmpty(t, evt.LastTimestamp)
		assert.Equal(t, 1, int(evt.Count))
		assert.Contains(t, evt.Message, "Pod(s) was deleted because it was scheduled on a node with a platform that is not supported by the image:\ntest-pod-1")
		assert.Contains(t, []string{"test-pod-1", "deployment-1"}, evt.InvolvedObject.Name)
		assert.Contains(t, []string{"test-pod-1-uid", "deployment-1-uid"}, string(evt.InvolvedObject.UID))
		assert.NotEmpty(t, evt.InvolvedObject.APIVersion, evt.InvolvedObject.APIVersion)
		assert.Equal(t, "ns", evt.InvolvedObject.Namespace)
	}
}

func TestReconcileShouldReportMetricsAndEventsWhenPodDeletionFails(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithObjects(
		&appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "deployment-1",
				Namespace: "ns",
				UID:       "deployment-1-uid",
			},
		},
		&v1.Pod{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "deployment-1",
						UID:        "deployment-1-uid",
						Controller: pointer.BoolPtr(true),
					},
				},
				Name:      "test-pod-1",
				Namespace: "ns",
				UID:       "test-pod-1-uid",
			},
			Spec: v1.PodSpec{
				NodeName: "node-1",
				Containers: []v1.Container{
					{
						Image: "test-image",
					},
				},
			},
		},
		&v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				Labels: map[string]string{
					"beta.kubernetes.io/arch": "amd64",
					"beta.kubernetes.io/os":   "linux",
				},
			},
		},
	).Build()

	k8sClient = &deleteErrorK8sClient{WithWatch: k8sClient}

	metricsRegistry := prometheus.NewRegistry()

	reconciler := controllers.NewPodReconciler(
		"test",
		controllers.WithClient(k8sClient),
		controllers.WithMetricsRegistry(metricsRegistry),
		controllers.WithRegistry(arch.RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
			assert.Equal(t, "test-image", image)
			return []registry.Platform{
				{
					OS:           "linux",
					Architecture: "arm64",
				},
			}, nil
		})))

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pod-1", Namespace: "ns"}})
	assert.NoError(t, err)

	checkMetricRegistry(
		t,
		metricsRegistry,
		"test_pods_deletion_total",
		func(t *testing.T, family *dto.MetricFamily) {
			checkMetricValueForLabels(
				t,
				family.Metric,
				prometheus.Labels{
					"namespace": "ns",
					"status":    "failed",
				},
				func(t *testing.T, metric *dto.Metric) {
					assert.EqualValues(t, 1.0, *metric.Counter.Value)
				},
			)
		},
	)

	evts := v1.EventList{}
	err = k8sClient.List(context.Background(), &evts)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(evts.Items), 2)
	for _, evt := range evts.Items {
		assert.Regexp(t, ".*-failed-to-delete-pod", evt.Name)
		assert.Equal(t, "Warning", evt.Type)
		assert.Equal(t, "PlatformMismatch", evt.Reason)
		assert.Equal(t, "noe", evt.Source.Component)
		assert.Equal(t, "ns", evt.Namespace)
		assert.NotEmpty(t, evt.FirstTimestamp)
		assert.NotEmpty(t, evt.LastTimestamp)
		assert.Equal(t, 1, int(evt.Count))
		assert.Contains(t, evt.Message, "Failed to delete pod(s) scheduled on a node with a platform that is not supported by the image. Pod(s):\ntest-pod-1")
		assert.Contains(t, []string{"test-pod-1", "deployment-1"}, evt.InvolvedObject.Name)
		assert.Contains(t, []string{"test-pod-1-uid", "deployment-1-uid"}, string(evt.InvolvedObject.UID))
		assert.NotEmpty(t, evt.InvolvedObject.APIVersion, evt.InvolvedObject.APIVersion)
		assert.Equal(t, "ns", evt.InvolvedObject.Namespace)
	}
}

type deleteErrorK8sClient struct {
	client.WithWatch
}

func (c *deleteErrorK8sClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return errors.New("delete error")
}

var _ client.WithWatch = &deleteErrorK8sClient{}

func checkNoMetric(t *testing.T, metricRegistry *prometheus.Registry, metric string) {
	t.Helper()
	families, err := metricRegistry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == metric {
			t.Errorf("metric %s found", metric)
		}
	}
}

func checkMetricRegistry(t *testing.T, metricRegistry *prometheus.Registry, metric string, check func(*testing.T, *dto.MetricFamily)) {
	t.Helper()
	families, err := metricRegistry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == metric {
			check(t, family)
			return
		}
	}
	t.Errorf("metric %s not found", metric)
}

func checkMetricValueForLabels(t *testing.T, metrics []*dto.Metric, labels prometheus.Labels, check func(*testing.T, *dto.Metric)) {
	t.Helper()
	availableLabelSet := []map[string]string{}
	for _, metric := range metrics {
		found := true
		labelSet := map[string]string{}
		for _, labelPair := range metric.Label {
			require.NotNil(t, labelPair.Name)
			require.NotNil(t, labelPair.Value)
			labelSet[labelPair.GetName()] = labelPair.GetValue()
			if val, ok := labels[labelPair.GetName()]; !ok || val != labelPair.GetValue() {
				found = false
			}
		}
		if found {
			check(t, metric)
			return
		}
		availableLabelSet = append(availableLabelSet, labelSet)
	}
	t.Errorf("metric not found for labels %v. Available Labels: %v", labels, availableLabelSet)
}

func TestReconcileWhenPodHasBeenPlacedCorrectlyShouldBeSkipped(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithObjects(
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "not-running-pod",
				Namespace: "ns",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "test-image",
					},
				},
			},
			Status: v1.PodStatus{
				Phase: v1.PodPending,
			},
		}, &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "running-not-ready-pod",
				Namespace: "ns",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "test-image",
					},
				},
			},
			Status: v1.PodStatus{
				Phase: v1.PodRunning,
				Conditions: []v1.PodCondition{
					{
						Type:   v1.PodReady,
						Status: v1.ConditionFalse,
					},
				},
			},
		}, &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "running-ready-pod",
				Namespace: "ns",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "test-image",
					},
				},
			},
			Status: v1.PodStatus{
				Phase: v1.PodRunning,
				Conditions: []v1.PodCondition{
					{
						Type:   v1.PodReady,
						Status: v1.ConditionTrue,
					},
				},
			},
		},
	).Build()

	metricsRegistry := prometheus.NewRegistry()

	reconciler := controllers.NewPodReconciler(
		"test",
		controllers.WithClient(k8sClient),
		controllers.WithMetricsRegistry(metricsRegistry),
		controllers.WithRegistry(arch.RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
			if image == "test-image" {
				return []registry.Platform{
					{
						OS:           "linux",
						Architecture: "amd64",
					},
				}, nil
			} else {
				return nil, errors.New("error")
			}
		})))

	_, err := reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "not-running-pod", Namespace: "ns"}})
	assert.NoError(t, err)

	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "running-not-ready-pod", Namespace: "ns"}})
	assert.NoError(t, err)

	_, err = reconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "running-ready-pod", Namespace: "ns"}})
	assert.NoError(t, err)

	checkMetricRegistry(
		t,
		metricsRegistry,
		"test_images_count",
		func(t *testing.T, family *dto.MetricFamily) {
			assert.Len(t, family.Metric, 1)
			fmt.Print(family.Metric)
			checkMetricValueForLabels(
				t,
				family.Metric,
				prometheus.Labels{
					"arch":    "amd64",
					"os":      "linux",
					"image":   "test-image",
					"variant": "",
				},
				func(t *testing.T, metric *dto.Metric) {
					assert.EqualValues(t, 2.0, *metric.Gauge.Value)
				},
			)
		},
	)
}
