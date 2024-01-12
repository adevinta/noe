package registry

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adevinta/noe/pkg/httputils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const WellKnownMultiArchImage = "alpine:3.17.2"

func testParseImage(t testing.TB, fullImage, expectedRegistry, expectedImage, expectedTag string, expectedHasRef bool) {
	t.Helper()
	reg := PlainRegistry{}
	registry, image, tag, hasRef := reg.parseImage(fullImage)
	assert.Equal(t, expectedRegistry, registry)
	assert.Equal(t, expectedImage, image)
	assert.Equal(t, expectedTag, tag)
	assert.Equal(t, expectedHasRef, hasRef)
}

func TestParseImage(t *testing.T) {
	testParseImage(t, "some.image", "docker.io", "library/some.image", "latest", false)
	testParseImage(t, "ubuntu", "docker.io", "library/ubuntu", "latest", false)
	testParseImage(t, "library/ubuntu:tagged", "docker.io", "library/ubuntu", "tagged", false)
	testParseImage(t, "company.io/my/image/path:tagged@1234", "company.io", "my/image/path", "tagged", true)
	testParseImage(t, "company.io/my/image/path@1234", "company.io", "my/image/path", "latest", true)
	testParseImage(t, "localhost:5000/my/image@1234", "localhost:5000", "my/image", "latest", true)
}

func assertKeysMatches(t *testing.T, expected []string, actual map[string]string) {
	t.Helper()
	for _, key := range expected {
		_, ok := actual[key]
		assert.True(t, ok, "key %s not found", key)
	}
	for key := range actual {
		assert.Contains(t, expected, key, "key %s not expected", key)
	}
}

func TestRegistryLabeller(t *testing.T) {
	requiredLabels := []string{"cached", "method", "host", "status", "content_type", "has_authorization"}

	t.Run("When request and response are nil", func(t *testing.T) {
		labels := RegistryLabeller(nil, nil)
		assertKeysMatches(t, requiredLabels, labels)
	})
	t.Run("When only response is nil", func(t *testing.T) {
		t.Run("without authorization", func(t *testing.T) {
			labels := RegistryLabeller(&http.Request{
				Method: "METHOD",
			}, nil)
			assertKeysMatches(t, requiredLabels, labels)
			assert.Equal(t, "METHOD", labels["method"])
			assert.Equal(t, "false", labels["has_authorization"])
		})
		t.Run("without authorization", func(t *testing.T) {
			labels := RegistryLabeller(&http.Request{
				Method: "METHOD",
				Header: http.Header{
					"Authorization": []string{"Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))},
				},
			}, nil)
			assertKeysMatches(t, requiredLabels, labels)
			assert.Equal(t, "METHOD", labels["method"])
			assert.Equal(t, "true", labels["has_authorization"])
		})
	})
	t.Run("When only request is nil", func(t *testing.T) {
		labels := RegistryLabeller(nil, &http.Response{
			StatusCode: 200,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
			},
		})
		assertKeysMatches(t, requiredLabels, labels)
		assert.Equal(t, "200", labels["status"])
		assert.Equal(t, "application/json", labels["content_type"])
	})
	t.Run("When request and response are provided", func(t *testing.T) {
		labels := RegistryLabeller(
			&http.Request{
				Method: "GET",
				Header: http.Header{
					"Authorization": []string{"Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))},
				},
			},
			&http.Response{
				StatusCode: 200,
				Header: http.Header{
					"Content-Type": []string{"application/json"},
				},
			},
		)
		assertKeysMatches(t, requiredLabels, labels)
		assert.Equal(t, "GET", labels["method"])
		assert.Equal(t, "true", labels["has_authorization"])
		assert.Equal(t, "200", labels["status"])
		assert.Equal(t, "application/json", labels["content_type"])
	})

}

func TestParseImageSubstituteRegistries(t *testing.T) {
	reg := PlainRegistry{
		Proxies: []RegistryProxy{
			{
				Registry: "docker.io",
				Proxy:    "other.docker.proxy.tld",
			},
			{
				Registry: "other.docker.proxy.tld",
				Proxy:    "docker.proxy.tld",
			},
		},
	}
	registry, _, _, _ := reg.parseImage("ubuntu")
	assert.Equal(t, "docker.proxy.tld", registry)
}

func TestListDockerHubLibraryArch(t *testing.T) {
	platforms, err := DefaultRegistry.ListArchs(context.Background(), "", WellKnownMultiArchImage)
	require.NoError(t, err)
	require.NotNil(t, platforms)
	assert.Contains(t, platforms, Platform{Architecture: "amd64", OS: "linux"})
	assert.Contains(t, platforms, Platform{Architecture: "arm64", OS: "linux", Variant: "v8"})
}
func TestListGoogleUSArch(t *testing.T) {
	platforms, err := DefaultRegistry.ListArchs(context.Background(), "", "us.gcr.io/k8s-artifacts-prod/autoscaling/vpa-admission-controller:0.8.0")
	require.NoError(t, err)
	require.NotNil(t, platforms)
	assert.Contains(t, platforms, Platform{Architecture: "amd64", OS: "linux"})
}
func TestListGoogleArch(t *testing.T) {
	platforms, err := DefaultRegistry.ListArchs(context.Background(), "", "gcr.io/kubebuilder/kube-rbac-proxy:v0.4.1")
	require.NoError(t, err)
	require.NotNil(t, platforms)
	assert.Contains(t, platforms, Platform{Architecture: "amd64", OS: "linux"})
}

func TestListGithubArch(t *testing.T) {
	platforms, err := DefaultRegistry.ListArchs(context.Background(), "", "ghcr.io/open-telemetry/opentelemetry-operator/opentelemetry-operator:0.56.0")
	require.NoError(t, err)
	require.NotNil(t, platforms)
	assert.Contains(t, platforms, Platform{Architecture: "amd64", OS: "linux"})
	assert.Contains(t, platforms, Platform{Architecture: "arm64", OS: "linux"})
}

func TestListArchsWithAuthenticationAndManifestListV2(t *testing.T) {
	registry := NewPlainRegistry(WithTransport(httputils.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "HEAD":
			// simulate a registry requesting authentication
			// TODO: fix wwwauthenticate authenticating each sub request
			//assert.Equal(t, "https://registry.company.corp/v2/my/image/manifests/latest", req.URL.String())
			headers := http.Header{}
			headers.Set("Www-Authenticate", "Bearer realm=\"https://auth.comnpany.corp/token\",service=\"registry.company.corp\"")
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     headers,
			}, nil
		case "GET":
			switch req.URL.Host {
			case "auth.comnpany.corp":
				assert.Equal(t, "/token", req.URL.Path)
				assert.Equal(t, "registry.company.corp", req.URL.Query().Get("service"))
				assert.Equal(t, "repository:my/image:pull", req.URL.Query().Get("scope"))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"token":"my-token"}`)),
				}, nil
			case "registry.company.corp":
				switch req.URL.Path {
				case "/v2/my/image/manifests/latest":
					headers := http.Header{}
					headers.Set("Content-Type", "application/vnd.docker.distribution.manifest.list.v2+json")
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     headers,
						Body: io.NopCloser(strings.NewReader(`{
							"manifests":[
								{
									"platform":{
										"architecture":"amd64",
										"os":"linux"
									},
									"digest":"amd-digest"
								},
								{
									"platform":{
										"architecture":"arm64",
										"os":"linux",
										"variant":"v8"
									},
									"digest":"arm-digest"
								}
							]
						}`)),
					}, nil
				case "/v2/my/image/manifests/arm-digest", "/v2/my/image/manifests/amd-digest":
					return &http.Response{
						StatusCode: http.StatusOK,
					}, nil
				default:
					t.Errorf("unexpected %v to %v", req.Method, req.URL)
				}
			default:
				t.Errorf("unexpected %v to %v", req.Method, req.URL)
			}
		default:
			t.Errorf("unexpected %v to %v", req.Method, req.URL)
		}
		assert.Equal(t, "Bearer some-token", req.Header.Get("Authorization"))
		t.Fail()
		return nil, nil
	})))
	platforms, err := registry.ListArchs(context.Background(), "", "registry.company.corp/my/image")
	assert.NoError(t, err)
	assert.Len(t, platforms, 2)
	assert.Contains(t, platforms, Platform{Architecture: "amd64", OS: "linux"})
	assert.Contains(t, platforms, Platform{Architecture: "arm64", OS: "linux", Variant: "v8"})
}

func TestListArchsWithAuthenticationAndManifestIndexV1(t *testing.T) {
	registry := NewPlainRegistry(WithTransport(httputils.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "HEAD":
			// simulate a registry requesting authentication
			// TODO: fix wwwauthenticate authenticating each sub request
			//assert.Equal(t, "https://registry.company.corp/v2/my/image/manifests/latest", req.URL.String())
			headers := http.Header{}
			headers.Set("Www-Authenticate", "Bearer realm=\"https://auth.comnpany.corp/token\",service=\"registry.company.corp\"")
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     headers,
			}, nil
		case "GET":
			switch req.URL.Host {
			case "auth.comnpany.corp":
				assert.Equal(t, "/token", req.URL.Path)
				assert.Equal(t, "registry.company.corp", req.URL.Query().Get("service"))
				assert.Equal(t, "repository:my/image:pull", req.URL.Query().Get("scope"))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"token":"my-token"}`)),
				}, nil
			case "registry.company.corp":
				switch req.URL.Path {
				case "/v2/my/image/manifests/latest":
					headers := http.Header{}
					headers.Set("Content-Type", "application/vnd.oci.image.index.v1+json")
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     headers,
						Body: io.NopCloser(strings.NewReader(`{
							"manifests":[
								{
									"platform":{
										"architecture":"unknown",
										"os":"unknown"
									},
									"digest":"amd-digest"
								},
								{
									"platform":{
										"architecture":"arm64",
										"os":"linux",
										"variant":"v8"
									},
									"digest":"arm-digest"
								}
							]
						}`)),
					}, nil
				case "/v2/my/image/manifests/arm-digest", "/v2/my/image/manifests/amd-digest":
					return &http.Response{
						StatusCode: http.StatusOK,
					}, nil
				default:
					t.Errorf("unexpected %v to %v", req.Method, req.URL)
				}
			default:
				t.Errorf("unexpected %v to %v", req.Method, req.URL)
			}
		default:
			t.Errorf("unexpected %v to %v", req.Method, req.URL)
		}
		assert.Equal(t, "Bearer some-token", req.Header.Get("Authorization"))
		t.Fail()
		return nil, nil
	})))
	platforms, err := registry.ListArchs(context.Background(), "", "registry.company.corp/my/image")
	assert.NoError(t, err)
	assert.Len(t, platforms, 1)
	assert.NotContains(t, platforms, Platform{Architecture: "unknown", OS: "unknown"})
	assert.Contains(t, platforms, Platform{Architecture: "arm64", OS: "linux", Variant: "v8"})
}

func TestListArchWithEmptyImage(t *testing.T) {
	registry := NewPlainRegistry()
	_, err := registry.ListArchs(context.Background(), "", "")
	assert.Error(t, err)
}

func TestListArchsWithAuthenticationAndPlainManifest(t *testing.T) {
	registry := NewPlainRegistry(WithTransport(httputils.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case "HEAD":
			// simulate a registry requesting authentication
			//assert.Equal(t, "https://registry.company.corp/v2/my/image/manifests/latest", req.URL.String())
			headers := http.Header{}
			headers.Set("Www-Authenticate", "Bearer realm=\"https://auth.comnpany.corp/token\",service=\"registry.company.corp\"")
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     headers,
			}, nil
		case "GET":
			switch req.URL.Host {
			case "auth.comnpany.corp":
				assert.Equal(t, "/token", req.URL.Path)
				assert.Equal(t, "registry.company.corp", req.URL.Query().Get("service"))
				assert.Equal(t, "repository:my/image:pull", req.URL.Query().Get("scope"))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"token":"my-token"}`)),
				}, nil
			case "registry.company.corp":
				switch req.URL.Path {
				case "/v2/my/image/manifests/latest":
					headers := http.Header{}
					headers.Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     headers,
						Body: io.NopCloser(strings.NewReader(`{
							"architecture": "amd64"
						}`)),
					}, nil
				default:
					t.Errorf("unexpected %v to %v", req.Method, req.URL)
				}
			default:
				t.Errorf("unexpected %v to %v", req.Method, req.URL)
			}
		default:
			t.Errorf("unexpected %v to %v", req.Method, req.URL)
		}
		assert.Equal(t, "Bearer some-token", req.Header.Get("Authorization"))
		t.Fail()
		return nil, nil
	})))
	platforms, err := registry.ListArchs(context.Background(), "", "registry.company.corp/my/image")
	assert.NoError(t, err)
	assert.Len(t, platforms, 1)
	assert.Contains(t, platforms, Platform{Architecture: "amd64"})
}

func TestParseRegistryProxies(t *testing.T) {
	assert.Equal(
		t,
		[]RegistryProxy{},
		ParseRegistryProxies(""),
	)
	assert.Equal(
		t,
		[]RegistryProxy{{Registry: "docker.io", Proxy: "docker-proxy.company.corp"}},
		ParseRegistryProxies("docker.io=docker-proxy.company.corp"),
	)
	assert.Equal(
		t,
		[]RegistryProxy{{Registry: "docker.io", Proxy: "docker-proxy.company.corp"}, {Registry: "quay.io", Proxy: "quay-proxy.company.corp"}},
		ParseRegistryProxies("docker.io=docker-proxy.company.corp,quay.io=quay-proxy.company.corp"),
	)
}
