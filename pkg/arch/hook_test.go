package arch

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/adevinta/noe/pkg/metric_test_helpers"
	"github.com/adevinta/noe/pkg/registry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func runWebhookTest(t testing.TB, webhook *Handler, obj runtime.Object) admission.Response {
	t.Helper()
	decoder, err := admission.NewDecoder(scheme.Scheme)
	require.NoError(t, err)
	webhook.InjectDecoder(decoder)
	raw, err := toJson(obj)
	require.NoError(t, err)

	return webhook.Handle(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Kind:      metav1.GroupVersionKind{Kind: "Pod"},
			Operation: admissionv1.Create,
			Object: runtime.RawExtension{
				Object: obj,
				Raw:    raw,
			},
		},
	})
}

func archNodeSelectorPatchForArchs(archs ...string) jsonpatch.Operation {
	var tmp []interface{}
	for _, a := range archs {
		tmp = append(tmp, interface{}(a))
	}
	return jsonpatch.Operation{
		Operation: "add",
		Path:      "/spec/affinity",
		Value: map[string]interface{}{
			"nodeAffinity": map[string]interface{}{
				"requiredDuringSchedulingIgnoredDuringExecution": map[string]interface{}{
					"nodeSelectorTerms": []interface{}{
						map[string]interface{}{
							"matchExpressions": []interface{}{
								map[string]interface{}{
									"key":      "kubernetes.io/arch",
									"operator": "In",
									"values":   tmp,
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestAllMetricsShouldBeRegistered(t *testing.T) {
	metrics := NewHandlerMetrics("test")
	metric_test_helpers.AssertAllMetricsHaveBeenRegistered(t, metrics)
}

func TestHookAcceptsSingleImageAndAddsSelector(t *testing.T) {
	resp := runWebhookTest(
		t,
		NewHandler(
			fake.NewClientBuilder().Build(),
			RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
				assert.Equal(t, "ubuntu", image)
				return []registry.Platform{
					{OS: "linux", Architecture: "arm64"},
					{OS: "linux", Architecture: "amd64"},
					{OS: "windows", Architecture: "amd64"},
				}, nil
			}),
			WithOS("linux"),
		),
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test",
				Name:      "object",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "ubuntu",
					},
				},
			},
		},
	)
	assert.True(t, resp.Allowed)
	assert.Equal(t, http.StatusOK, int(resp.Result.Code))
	require.Len(t, resp.Patches, 1)
	assert.Equal(
		t,
		archNodeSelectorPatchForArchs("amd64", "arm64"),
		resp.Patches[0],
	)
}

func TestHookAcceptsSingleImageWithoutOSAndAddsSelector(t *testing.T) {
	resp := runWebhookTest(
		t,
		NewHandler(
			fake.NewClientBuilder().Build(),
			RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
				assert.Equal(t, "ubuntu", image)
				return []registry.Platform{
					{Architecture: "amd64"},
				}, nil
			}),
			WithOS("linux"),
		),
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test",
				Name:      "object",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "ubuntu",
					},
				},
			},
		},
	)
	assert.True(t, resp.Allowed)
	assert.Equal(t, http.StatusOK, int(resp.Result.Code))
	require.Len(t, resp.Patches, 1)
	assert.Equal(
		t,
		archNodeSelectorPatchForArchs("amd64"),
		resp.Patches[0],
	)
}

func testPodLabelsMatchesNodeLabelsSelector(t *testing.T, selector string) {
	t.Helper()

	t.Run("When the pod does not have node selection", func(t *testing.T) {
		resp := runWebhookTest(
			t,
			NewHandler(
				fake.NewClientBuilder().Build(),
				RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
					assert.Equal(t, "ubuntu", image)
					return []registry.Platform{
						{OS: "linux", Architecture: "adv-866"},
					}, nil
				}),
				WithArchitecture("adv-866"),
				WithOS("linux"),
				WithMatchNodeLabels(ParseMatchNodeLabels(selector)),
			),
			&v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "object",
					Labels: map[string]string{
						selector: "true",
					},
				},

				Spec: v1.PodSpec{
					NodeSelector: map[string]string{
						"kubernetes.io/arch": "adv-866",
					},
					Containers: []v1.Container{
						{
							Image: "ubuntu",
						},
					},
				},
			},
		)
		assert.True(t, resp.Allowed)
		assert.Equal(t, http.StatusOK, int(resp.Result.Code))
		require.Len(t, resp.Patches, 1)
		assert.Contains(
			t,
			resp.Patches,
			jsonpatch.Operation{
				Operation: "add",
				Path:      "/spec/nodeSelector/" + strings.Replace(selector, "/", "~1", -1),
				Value:     "true",
			},
		)
	})

	t.Run("When the pod already has a node selector", func(t *testing.T) {
		resp := runWebhookTest(
			t,
			NewHandler(
				fake.NewClientBuilder().Build(),
				RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
					return nil, errors.New("registry should not be called")
				}),
				WithOS("linux"),
				WithMatchNodeLabels(ParseMatchNodeLabels(selector)),
			),
			&v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "object",
					Labels: map[string]string{
						selector: "true",
					},
				},

				Spec: v1.PodSpec{
					NodeSelector: map[string]string{
						"kubernetes.io/arch": "adv-866",
					},
					Containers: []v1.Container{
						{
							Image: "ubuntu",
						},
					},
				},
			},
		)
		assert.True(t, resp.Allowed)
		assert.Equal(t, http.StatusOK, int(resp.Result.Code))
		require.Len(t, resp.Patches, 1)
		assert.Contains(
			t,
			resp.Patches,
			jsonpatch.Operation{
				Operation: "add",
				Path:      "/spec/nodeSelector/" + strings.Replace(selector, "/", "~1", -1),
				Value:     "true",
			},
		)
	})
	t.Run("When the pod already has a node affinity", func(t *testing.T) {
		resp := runWebhookTest(
			t,
			NewHandler(
				fake.NewClientBuilder().Build(),
				RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
					return nil, errors.New("registry should not be called")
				}),
				WithOS("linux"),
				WithMatchNodeLabels(ParseMatchNodeLabels(selector)),
			),
			&v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "object",
					Labels: map[string]string{
						selector: "true",
					},
				},

				Spec: v1.PodSpec{
					Affinity: &v1.Affinity{
						NodeAffinity: &v1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
								NodeSelectorTerms: []v1.NodeSelectorTerm{
									{
										MatchExpressions: []v1.NodeSelectorRequirement{
											{
												Key:      "kubernetes.io/arch",
												Operator: v1.NodeSelectorOpIn,
												Values:   []string{"adv-866"},
											},
										},
									},
								},
							},
						},
					},
					Containers: []v1.Container{
						{
							Image: "ubuntu",
						},
					},
				},
			},
		)
		assert.True(t, resp.Allowed)
		assert.Equal(t, http.StatusOK, int(resp.Result.Code))
		require.Len(t, resp.Patches, 1)
		assert.Contains(
			t,
			resp.Patches,
			jsonpatch.Operation{
				Operation: "add",
				Path:      "/spec/affinity/nodeAffinity/requiredDuringSchedulingIgnoredDuringExecution/nodeSelectorTerms/1",
				Value: map[string]interface{}{
					"matchExpressions": []interface{}{
						map[string]interface{}{
							"key":      selector,
							"operator": "In",
							"values": []interface{}{
								"true",
							},
						},
					},
				},
			},
		)
	})
}

func TestHookHandlesPodNodeMatchingLabelSelector(t *testing.T) {
	testPodLabelsMatchesNodeLabelsSelector(t, "accelerator.node.kubernetes.io/gpu")
	testPodLabelsMatchesNodeLabelsSelector(t, "accelerator.node.kubernetes.io/inference")
}

func TestHookSucceedsWhenPreferredArchUnavailable(t *testing.T) {
	resp := runWebhookTest(
		t,
		NewHandler(
			fake.NewClientBuilder().Build(),
			RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
				assert.Equal(t, "ubuntu", image)
				return []registry.Platform{
					{OS: "linux", Architecture: "amd64"},
					{OS: "windows", Architecture: "amd64"},
				}, nil
			}),
			WithOS("linux"),
		),
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test",
				Name:      "object",
				Labels:    map[string]string{"arch.noe.adevinta.com/preferred": "arm64"},
			},

			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "ubuntu",
					},
				},
			},
		},
	)
	assert.True(t, resp.Allowed)
	assert.Equal(t, http.StatusOK, int(resp.Result.Code))
	require.Len(t, resp.Patches, 1)
	assert.Contains(
		t,
		resp.Patches,
		archNodeSelectorPatchForArchs("amd64"),
	)
}

func TestHookHonorsDefaultPreferredArch(t *testing.T) {
	resp := runWebhookTest(
		t,
		NewHandler(
			fake.NewClientBuilder().Build(),
			RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
				assert.Equal(t, "ubuntu", image)
				return []registry.Platform{
					{OS: "linux", Architecture: "arm64"},
					{OS: "linux", Architecture: "amd64"},
					{OS: "windows", Architecture: "amd64"},
				}, nil
			}),
			WithArchitecture("amd64"),
			WithOS("linux"),
		),
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test",
				Name:      "object",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "ubuntu",
					},
				},
			},
		},
	)
	assert.True(t, resp.Allowed)
	assert.Equal(t, http.StatusOK, int(resp.Result.Code))
	require.Len(t, resp.Patches, 1)
	assert.NotContains(
		t,
		resp.Patches,
		archNodeSelectorPatchForArchs("amd64", "arm64"),
	)
	assert.Contains(
		t,
		resp.Patches,
		jsonpatch.Operation{
			Operation: "add",
			Path:      "/spec/nodeSelector",
			Value: map[string]interface{}{
				"kubernetes.io/arch": "amd64",
			},
		},
	)
}

func TestHookAcceptsMultipleImagesAndAddsSelector(t *testing.T) {
	resp := runWebhookTest(
		t,
		NewHandler(
			fake.NewClientBuilder().Build(),
			RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
				switch image {
				case "ubuntu":
					return []registry.Platform{
						{OS: "linux", Architecture: "arm64"},
						{OS: "linux", Architecture: "amd64"},
						{OS: "windows", Architecture: "amd64"},
					}, nil
				default:
					return []registry.Platform{
						{OS: "linux", Architecture: "arm64"},
					}, nil
				}
			}),
			WithOS("linux"),
		),
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test",
				Name:      "object",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "ubuntu",
					},
					{
						Image: "alpine",
					},
				},
			},
		},
	)
	assert.True(t, resp.Allowed)
	assert.Equal(t, http.StatusOK, int(resp.Result.Code))
	require.Len(t, resp.Patches, 1)
	assert.Equal(
		t,
		archNodeSelectorPatchForArchs("arm64"),
		resp.Patches[0],
	)
	assert.Greater(t, len(resp.Patch), 1)
}

func TestHookHandlesInitContainers(t *testing.T) {
	resp := runWebhookTest(
		t,
		NewHandler(
			fake.NewClientBuilder().Build(),
			RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
				switch image {
				case "ubuntu":
					return []registry.Platform{
						{OS: "linux", Architecture: "arm64"},
						{OS: "linux", Architecture: "amd64"},
						{OS: "windows", Architecture: "amd64"},
					}, nil
				case "nginx":
					return []registry.Platform{
						{OS: "linux", Architecture: "amd64"},
					}, nil

				default:
					return []registry.Platform{
						{OS: "linux", Architecture: "amd64"},
					}, nil
				}
			}),
			WithOS("linux"),
		),
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test",
				Name:      "object",
			},
			Spec: v1.PodSpec{
				InitContainers: []v1.Container{
					{
						Image: "nginx",
					},
				},
				Containers: []v1.Container{
					{
						Image: "ubuntu",
					},
					{
						Image: "alpine",
					},
				},
			},
		},
	)
	assert.True(t, resp.Allowed)
	assert.Equal(t, http.StatusOK, int(resp.Result.Code))
	require.Len(t, resp.Patches, 1)
	assert.Equal(
		t,
		archNodeSelectorPatchForArchs("amd64"),
		resp.Patches[0],
	)
	assert.Greater(t, len(resp.Patch), 1)
}

func TestHookRejectsMultipleImagesWithNoCommonArch(t *testing.T) {
	resp := runWebhookTest(
		t,
		NewHandler(
			fake.NewClientBuilder().Build(),
			RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
				switch image {
				case "ubuntu":
					return []registry.Platform{
						{OS: "linux", Architecture: "amd64"},
						{OS: "windows", Architecture: "amd64"},
					}, nil
				default:
					return []registry.Platform{
						{OS: "linux", Architecture: "arm64"},
					}, nil
				}
			}),
			WithOS("linux"),
		),
		&v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test",
				Name:      "object",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Image: "ubuntu",
					},
					{
						Image: "alpine",
					},
				},
			},
		},
	)
	assert.False(t, resp.Allowed)
	assert.Equal(t, http.StatusForbidden, int(resp.Result.Code))
	assert.Len(t, resp.Patches, 0)
	assert.Len(t, resp.Patch, 0)
}

func TestUpdatePodSpecWithEmptyImage(t *testing.T) {
	t.Run("When one container has missing images", func(t *testing.T) {
		resp := runWebhookTest(
			t,
			NewHandler(
				fake.NewClientBuilder().Build(),
				RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
					if image == "ubuntu" {
						return []registry.Platform{
							{OS: "linux", Architecture: "amd64"},
						}, nil
					}
					return nil, errors.New("image not found")
				}),
				WithOS("linux"),
			),
			&v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "object",
				},

				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{},
					},
					InitContainers: []v1.Container{
						{
							Image: "ubuntu",
						},
					},
				},
			},
		)
		assert.True(t, resp.Allowed)
		assert.Equal(t, http.StatusOK, int(resp.Result.Code))
	})
	t.Run("When all containers has missing images", func(t *testing.T) {
		resp := runWebhookTest(
			t,
			NewHandler(
				fake.NewClientBuilder().Build(),
				RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
					if image == "ubuntu" {
						return []registry.Platform{
							{OS: "linux", Architecture: "amd64"},
						}, nil
					}
					return nil, errors.New("image not found")
				}),
				WithOS("linux"),
			),
			&v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "object",
				},

				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{},
					},
					InitContainers: []v1.Container{
						{},
					},
				},
			},
		)
		assert.True(t, resp.Allowed)
		assert.Equal(t, http.StatusOK, int(resp.Result.Code))
	})
}

func TestUpdatePodSpecsDoesNotChangePodsTargettingAGivenNode(t *testing.T) {
	// provide a metric registry to ensure we have no panic because of mismatching labels
	h := NewHandler(fake.NewClientBuilder().Build(), nil, WithOS("linux"), WithMetricsRegistry(prometheus.NewRegistry()))
	t.Run("When the pod is already assigned to the node", func(t *testing.T) {
		podSpec := &v1.PodSpec{
			NodeName: "my-node",
		}
		testPodSpecIsNotModified(t, h, podSpec)
	})
	t.Run("When the pod is targetting a specific node", func(t *testing.T) {
		podSpec := &v1.PodSpec{
			Affinity: &v1.Affinity{
				NodeAffinity: &v1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
						NodeSelectorTerms: []v1.NodeSelectorTerm{
							{
								MatchFields: []v1.NodeSelectorRequirement{
									{
										Key:      "metadata.name",
										Operator: v1.NodeSelectorOpIn,
										Values:   []string{"node-name"},
									},
								},
							},
						},
					},
				},
			},
		}
		testPodSpecIsNotModified(t, h, podSpec)
	})
}

func TestUpdatePodSpecWithPreDefinedSelector(t *testing.T) {
	// provide a metric registry to ensure we have no panic because of mismatching labels
	h := NewHandler(fake.NewClientBuilder().Build(), nil, WithOS("linux"), WithMetricsRegistry(prometheus.NewRegistry()))
	t.Run("When the pod has architecture selector", func(t *testing.T) {
		podSpec := &v1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/arch": "amd64",
			},
		}
		testPodSpecIsNotModified(t, h, podSpec)
	})
	t.Run("When the pod has beta architecture selector", func(t *testing.T) {
		podSpec := &v1.PodSpec{
			NodeSelector: map[string]string{
				"beta.kubernetes.io/arch": "amd64",
			},
		}
		testPodSpecIsNotModified(t, h, podSpec)
	})
	t.Run("When the pod has architecture affinity", func(t *testing.T) {
		podSpec := &v1.PodSpec{
			Affinity: &v1.Affinity{
				NodeAffinity: &v1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
						NodeSelectorTerms: []v1.NodeSelectorTerm{
							{
								MatchExpressions: []v1.NodeSelectorRequirement{
									{
										Key:      "kubernetes.io/arch",
										Operator: v1.NodeSelectorOpIn,
										Values:   []string{"amd64"},
									},
								},
							},
						},
					},
				},
			},
		}
		testPodSpecIsNotModified(t, h, podSpec)
	})
	t.Run("When the pod has beta architecture affinity", func(t *testing.T) {
		podSpec := &v1.PodSpec{
			Affinity: &v1.Affinity{
				NodeAffinity: &v1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &v1.NodeSelector{
						NodeSelectorTerms: []v1.NodeSelectorTerm{
							{
								MatchExpressions: []v1.NodeSelectorRequirement{
									{
										Key:      "kubernetes.io/arch",
										Operator: v1.NodeSelectorOpIn,
										Values:   []string{"amd64"},
									},
								},
							},
						},
					},
				},
			},
		}
		testPodSpecIsNotModified(t, h, podSpec)
	})
}

func TestUpdatePodSpecWithFailingRegistry(t *testing.T) {
	client := fake.NewClientBuilder().Build()
	// provide a metric registry to ensure we have no panic because of mismatching labels
	h := NewHandler(client, RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
		return nil, errors.New("error")
	}), WithOS("linux"), WithMetricsRegistry(prometheus.NewRegistry()))

	podSpec := &v1.PodSpec{
		Containers: []v1.Container{
			{
				Image: "ubuntu",
			},
		},
	}
	testPodSpecIsNotModified(t, h, podSpec)
}

func TestUpdatePodSpecWithImagePullSecret(t *testing.T) {
	t.Run("When the secret does not exist", func(t *testing.T) {
		client := fake.NewClientBuilder().Build()
		// provide a metric registry to ensure we have no panic because of mismatching labels
		h := NewHandler(client, RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
			return []registry.Platform{
				{
					Architecture: "amd64",
					OS:           "linux",
				},
			}, nil
		}), WithOS("linux"), WithMetricsRegistry(prometheus.NewRegistry()))

		podSpec := &v1.PodSpec{
			Containers: []v1.Container{
				{
					Image: "ubuntu",
				},
			},
			ImagePullSecrets: []v1.LocalObjectReference{
				{
					Name: "my-secret",
				},
			},
		}
		assert.NoErrorf(t, h.updatePodSpec(context.TODO(), "my-ns", map[string]string{}, podSpec), "noe should fallback to standard credentials")
	})
	t.Run("When the image pull secret does not have docker config key", func(t *testing.T) {
		client := fake.NewClientBuilder().
			WithObjects(&v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-secret",
					Namespace: "my-ns",
				},
				Data: map[string][]byte{},
			}).
			Build()
		// provide a metric registry to ensure we have no panic because of mismatching labels
		h := NewHandler(client, RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
			return []registry.Platform{
				{
					Architecture: "amd64",
					OS:           "linux",
				},
			}, nil
		}), WithOS("linux"), WithMetricsRegistry(prometheus.NewRegistry()))

		podSpec := &v1.PodSpec{
			Containers: []v1.Container{
				{
					Image: "ubuntu",
				},
			},
			ImagePullSecrets: []v1.LocalObjectReference{
				{
					Name: "my-secret",
				},
			},
		}
		assert.NoErrorf(t, h.updatePodSpec(context.TODO(), "my-ns", map[string]string{}, podSpec), "noe should fallback to standard credentials")
	})
	t.Run("When the image pull secret is invalid", func(t *testing.T) {
		client := fake.NewClientBuilder().
			WithObjects(&v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-secret",
					Namespace: "my-ns",
				},
				Data: map[string][]byte{
					".dockerconfigjson": []byte("this-is-not-a-json"),
				},
			}).
			Build()
		// provide a metric registry to ensure we have no panic because of mismatching labels
		h := NewHandler(client, RegistryFunc(func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
			return []registry.Platform{
				{
					Architecture: "amd64",
					OS:           "linux",
				},
			}, nil
		}), WithOS("linux"), WithMetricsRegistry(prometheus.NewRegistry()))

		podSpec := &v1.PodSpec{
			Containers: []v1.Container{
				{
					Image: "ubuntu",
				},
			},
			ImagePullSecrets: []v1.LocalObjectReference{
				{
					Name: "my-secret",
				},
			},
		}
		assert.NoErrorf(t, h.updatePodSpec(context.TODO(), "my-ns", map[string]string{}, podSpec), "noe should fallback to standard credentials")
	})

}

func testPodSpecIsNotModified(t *testing.T, h *Handler, original *v1.PodSpec) {
	t.Helper()
	result := original.DeepCopy()
	assert.NoError(t, h.updatePodSpec(context.TODO(), "my-ns", map[string]string{}, result))
	assert.Equal(t, original, result)
}
