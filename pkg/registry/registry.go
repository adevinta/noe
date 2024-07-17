package registry

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adevinta/noe/pkg/httputils"
	"github.com/adevinta/noe/pkg/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type Registry interface {
	ListArchs(ctx context.Context, imagePullSecret, image string) ([]Platform, error)
}

var DefaultRegistry = NewPlainRegistry()

type RegistryMetrics struct {
	Requests           *prometheus.CounterVec
	Errors             *prometheus.CounterVec
	Responses          *prometheus.CounterVec
	RateLimitRemaining *prometheus.GaugeVec
	RateLimitLimit     *prometheus.GaugeVec
	RateLimitWindow    *prometheus.GaugeVec
}

func (m RegistryMetrics) MustRegister(reg metrics.RegistererGatherer) {
	reg.MustRegister(
		m.Requests,
		m.Responses,
		m.Errors,
		m.RateLimitRemaining,
		m.RateLimitLimit,
		m.RateLimitWindow,
	)
}

func NewRegistryMetrics(prefix string) *RegistryMetrics {
	m := &RegistryMetrics{
		Requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: "registry",
			Name:      "requests_total",
			Help:      "Number of requests to the container image registry",
		}, []string{}),
		Responses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: "registry",
			Name:      "responses_total",
			Help:      "Number of request responses from the container image registry",
		}, []string{"kind", "http_status"}),
		Errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: "registry",
			Name:      "errors_total",
			Help:      "Number of errors requesting the container image registry",
		}, []string{"errors"}),
		RateLimitRemaining: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: prefix,
			Subsystem: "registry",
			Name:      "rate_limit_remaining",
			Help:      "Number of requests remaining",
		}, []string{"registry"}),
		RateLimitLimit: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: prefix,
			Subsystem: "registry",
			Name:      "rate_limit_limit",
			Help:      "Total number of requests allowed",
		}, []string{"registry"}),
		RateLimitWindow: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: prefix,
			Subsystem: "registry",
			Name:      "rate_limit_window_seconds",
			Help:      "The window in seconds for the rate limit",
		}, []string{"registry"}),
	}
	return m
}

func NewPlainRegistry(builders ...func(*PlainRegistry)) *PlainRegistry {
	r := PlainRegistry{
		Scheme:        "https",
		Authenticator: NewAuthenticator("", ""),
		Proxies:       []RegistryProxy{},
		cacheMetrics:  NewCacheMetrics("noe", "registry_authentication"),
		Metrics:       NewRegistryMetrics("noe"),
	}
	for _, builder := range builders {
		builder(&r)
	}
	return &r
}

type registryAuthResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
	IssuedAt  string `json:"issued_at"`
}

type registryManifestListResponse struct {
	MediaType string `json:"mediaType"`
	// for application/vnd.docker.distribution.manifest.v2+json
	// https://docs.docker.com/registry/spec/manifest-v2-2/
	Architecture string `json:"architecture"`
	// for application/vnd.docker.distribution.manifest.list.v2+json
	Manifests []registryManifestRef `json:"manifests"`
}

type registryManifestRef struct {
	Platform Platform `json:"platform"`
	Digest   string   `json:"digest"`
}

type Platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant"`
}

func WithTransport(transport http.RoundTripper) func(*PlainRegistry) {
	return func(r *PlainRegistry) {
		r.Transport = transport
	}
}

func WithRegistryMetricRegistry(reg metrics.RegistererGatherer) func(*PlainRegistry) {
	return func(r *PlainRegistry) {
		if r.Metrics != nil {
			r.Metrics.MustRegister(reg)
		}
		r.cacheMetrics.MustRegister(reg)
	}
}

func ParseRegistryProxies(proxies string) []RegistryProxy {
	r := []RegistryProxy{}
	for _, proxy := range strings.Split(proxies, ",") {
		proxy = strings.TrimSpace(proxy)
		if proxy == "" {
			continue
		}
		split := strings.SplitN(proxy, "=", 2)
		if len(split) == 2 {
			r = append(r, RegistryProxy{Registry: split[0], Proxy: split[1]})
		} else {
			log.DefaultLogger.WithField("registryProxy", proxy).Warn("invalid registry proxy syntax, ignoring")
		}
	}
	return r
}

func WithDockerProxies(proxies []RegistryProxy) func(*PlainRegistry) {
	return func(r *PlainRegistry) {
		r.Proxies = append(r.Proxies, proxies...)
	}
}

func WithAuthenticator(authenticator Authenticator) func(*PlainRegistry) {
	return func(r *PlainRegistry) {
		r.Authenticator = authenticator
	}
}

func WithSchedulableArchitectures(archs []string) func(*PlainRegistry) {
	return func(r *PlainRegistry) {
		r.SchedulableArchitectures = archs
	}
}

func (r *PlainRegistry) parseImage(image string) (string, string, string, bool) {
	registry := ""
	tag := ""
	hasRef := false
	split := strings.SplitN(image, "/", 2)
	if (strings.Contains(split[0], ".") || strings.Contains(split[0], ":")) && len(split) > 1 {
		registry = split[0]
		image = split[1]
	}
	split = strings.SplitN(image, "@", 2)
	if len(split) == 1 {
	} else {
		image = split[0]
		hasRef = true
	}
	split = strings.SplitN(image, ":", 2)
	if len(split) == 1 {
		tag = "latest"
	} else {
		image = split[0]
		tag = split[1]
	}
	if registry == "" {
		registry = "docker.io"
	}
	if registry == "docker.io" && !strings.Contains(image, "/") {
		image = "library/" + image
	}
	for _, proxy := range r.Proxies {
		if ok, err := filepath.Match(proxy.Registry, registry); err == nil && ok {
			log.DefaultLogger.WithField("registry", registry).WithField("proxy", proxy.Proxy).Debug("using docker registry proxy")
			registry = proxy.Proxy
		}
	}
	return registry, image, tag, hasRef
}

type RegistryProxy struct {
	Registry string
	Proxy    string
}

type PlainRegistry struct {
	Scheme                   string
	Transport                http.RoundTripper
	Authenticator            Authenticator
	Proxies                  []RegistryProxy
	authenticateCache        Cache[string]
	cacheMetrics             *CacheMetrics
	Metrics                  *RegistryMetrics
	SchedulableArchitectures []string
}

type WWWAuthenticateTransport struct {
	cache        *Cache[string]
	cacheMetrics *CacheMetrics
	Transport    http.RoundTripper
}

func (t *WWWAuthenticateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	transport := http.DefaultTransport
	if t.Transport != nil {
		transport = t.Transport
	}
	headReq, err := http.NewRequest("HEAD", req.URL.String(), nil)
	if err != nil {
		return nil, err
	}
	imageParts := strings.Split(req.URL.Path, "/")
	image := strings.Join(imageParts[2:len(imageParts)-2], "/")

	sum := sha1.New()
	sum.Write([]byte(image))
	sum.Write([]byte(req.Header.Get("Authorization")))
	cacheKey := hex.EncodeToString(sum.Sum(nil))
	ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"cacheKey": cacheKey, "image": image})

	cachedAuth := false
	cachedAuthHeader := new(string)
	headReq.Header = req.Header.Clone()
	if t.cache != nil {
		if t.cacheMetrics != nil {
			t.cacheMetrics.Requests.WithLabelValues().Inc()
		}
		// Trigger a cleanup of the cache, but don't wait for it to finish
		// Waiting for the cleanup to finish would block the request and
		// slow down the response time.
		defer func() { go t.cache.CleanUp(time.Now()) }()
		cachedAuthHeader, cachedAuth = t.cache.Load(cacheKey)
		if cachedAuth && cachedAuthHeader != nil {
			log.DefaultLogger.WithContext(ctx).Debug("Using cached authentication")
			headReq.Header.Set("Authorization", *cachedAuthHeader)
		} else {
			log.DefaultLogger.WithContext(ctx).Debug("No cached authentication found")
		}
	}
	resp, err := transport.RoundTrip(headReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {

		kind, authRequest, err := NewWWWAuthenticateRequest(resp.Header.Get("www-Authenticate"))
		if err != nil {
			return resp, err
		}

		query := authRequest.URL.Query()
		query.Set("scope", fmt.Sprintf("repository:%s:pull", image))
		authRequest.URL.RawQuery = query.Encode()

		authResp, authErr := transport.RoundTrip(authRequest)
		if authErr != nil {
			return resp, err
		}
		defer authResp.Body.Close()
		if authResp.StatusCode != http.StatusOK {
			return resp, err
		}
		authResponse := registryAuthResponse{}
		authErr = json.NewDecoder(authResp.Body).Decode(&authResponse)
		if authErr != nil {
			return resp, err
		}
		auth := kind + " " + authResponse.Token
		log.DefaultLogger.WithContext(ctx).Debug("Using fresh authentication")
		req.Header.Set("Authorization", auth)
		if t.cache != nil {
			if t.cacheMetrics != nil {
				t.cacheMetrics.Responses.WithLabelValues("miss").Inc()
			}
			if authResponse.ExpiresIn > 0 {
				log.DefaultLogger.WithContext(ctx).WithField("expires_in", authResponse.ExpiresIn).Debug("Caching authentication token")
				t.cache.Store(cacheKey, &auth, t.cache.WithExpiry(time.Now().Add(time.Duration(authResponse.ExpiresIn)*time.Second)))
			}
		}
	} else if cachedAuth && cachedAuthHeader != nil {
		if t.cacheMetrics != nil {
			t.cacheMetrics.Responses.WithLabelValues("hit").Inc()
		}
		log.DefaultLogger.WithContext(ctx).Debug("Using cached authentication")
		req.Header.Set("Authorization", *cachedAuthHeader)
	}
	resp, err = transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func newGetManifestRequest(ctx context.Context, scheme, registry, image, tag string, auth AuthenticationToken) (*http.Request, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, registry, image, tag), nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	if auth.Token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("%s %s", auth.Kind, auth.Token))
	}
	return req, nil
}

func RegistryLabeller(req *http.Request, resp *http.Response) prometheus.Labels {
	labels := httputils.StandardRoundTripLabeller(req, resp)
	labels["method"] = ""
	labels["status"] = ""
	labels["content_type"] = ""
	labels["has_authorization"] = "false"
	if req != nil {
		labels["method"] = req.Method
		if req.Header.Get("Authorization") != "" {
			labels["has_authorization"] = "true"
		}
	}
	if resp != nil {
		labels["status"] = strconv.Itoa(resp.StatusCode)
		labels["content_type"] = resp.Header.Get("Content-Type")
	}
	return labels
}
func manifestKindFromMediaType(mediaType string) string {
	switch mediaType {
	case "application/vnd.docker.distribution.manifest.list.v2+json", "application/vnd.oci.image.index.v1+json":
		return "manifest_list"
	case "application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json":
		return "manifest"
	}
	return "unknown"
}

func (r *PlainRegistry) getImageManifest(ctx context.Context, client http.Client, auth AuthenticationToken, scheme, registry, image, tag string, acceptHeaders ...string) (*http.Response, error) {
	r.Metrics.Requests.WithLabelValues().Inc()

	req, err := newGetManifestRequest(ctx, r.Scheme, registry, image, tag, auth)
	if err != nil {
		r.Metrics.Errors.WithLabelValues("new_request_failed").Inc()
		return nil, err
	}
	// https://docs.docker.com/registry/spec/manifest-v2-2/
	for _, accept := range acceptHeaders {
		req.Header.Add("Accept", accept)
	}
	resp, err := client.Do(req)
	if err != nil {
		r.Metrics.Errors.WithLabelValues("request_failed").Inc()
		return nil, err
	}
	r.Metrics.Responses.WithLabelValues(manifestKindFromMediaType(resp.Header.Get("Content-Type")), strconv.Itoa(resp.StatusCode)).Inc()
	return resp, nil
}

func getRateLimitHeaders(resp *http.Response) (string, string, string) {
	// docker hub format
	// see https://www.docker.com/blog/checking-your-current-docker-pull-rate-limits-and-status/
	// Ratelimit-Limit:100;w=21600 Ratelimit-Remaining:83;w=21600
	remaining := resp.Header.Get("RateLimit-Remaining")
	limit := resp.Header.Get("RateLimit-Limit")
	remainingS := strings.Split(remaining, ";")
	limitS := strings.Split(limit, ";")
	remaining = remainingS[0]
	limit = limitS[0]

	window := ""
	if len(remainingS) > 1 {
		windowL := strings.Split(remainingS[1], "=")
		if len(windowL) > 1 {
			window = windowL[1]
		}
	}
	if window == "" && len(limitS) > 1 {
		windowL := strings.Split(limitS[1], "=")
		if len(windowL) > 1 {
			window = windowL[1]
		}
	}

	return remaining, limit, window
}

func (r *PlainRegistry) updateRemaingRateLimits(ctx context.Context, registry string, resp *http.Response) {
	remainingS, limitS, windowS := getRateLimitHeaders(resp)

	if remainingS != "" {
		remaining, err := strconv.ParseFloat(remainingS, 64)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).WithError(err).Warn("failed to parse rate-limit remaining")
		} else {
			r.Metrics.RateLimitRemaining.WithLabelValues(registry).Set(remaining)
		}
	}
	if limitS != "" {
		limit, err := strconv.ParseFloat(limitS, 64)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).WithError(err).Warn("failed to parse rate-limit limit")
		} else {
			r.Metrics.RateLimitLimit.WithLabelValues(registry).Set(limit)
		}
	}

	if windowS != "" {
		window, err := strconv.ParseFloat(windowS, 64)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).WithError(err).Warn("failed to parse rate-limit window")
		} else {
			r.Metrics.RateLimitWindow.WithLabelValues(registry).Set(window)
		}
	}
}

type archListResult struct {
	Platform Platform
	err      error
}

func (r *PlainRegistry) listArchsWithAuth(ctx context.Context, client http.Client, auth AuthenticationToken, registry, image, tag string) ([]Platform, error) {
	if registry == "docker.io" {
		registry = "registry-1." + registry
	}
	resp, err := r.getImageManifest(ctx, client, auth, r.Scheme, registry, image, tag,
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get manifest list for image %s in registry %s using the provider %s. Unexpected status code %d. Expecting %d", image, registry, auth.Ref.Provider, resp.StatusCode, http.StatusOK)
	}
	r.updateRemaingRateLimits(ctx, registry, resp)

	response := registryManifestListResponse{}
	b := bytes.Buffer{}
	io.Copy(&b, resp.Body)
	// keep buffer for easier debug until it stabilizes
	// fmt.Println(b.String())
	err = json.NewDecoder(&b).Decode(&response)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	platforms := []Platform{}
	switch resp.Header.Get("Content-Type") {
	case "application/vnd.docker.distribution.manifest.list.v2+json", "application/vnd.oci.image.index.v1+json":
		outChan := make(chan archListResult)
		wg := sync.WaitGroup{}
		for _, manifest := range response.Manifests {
			if manifest.Platform.Architecture == "unknown" {
				log.DefaultLogger.WithContext(ctx).Printf("skipping %s %s:%s since it contains an unknown supported platform.\n", manifest.Platform.Architecture, registry, image)
				continue
			}
			if len(r.SchedulableArchitectures) > 0 {
				found := false
				for _, arch := range r.SchedulableArchitectures {
					if arch == manifest.Platform.Architecture {
						found = true
						break
					}
				}
				if !found {
					log.DefaultLogger.WithContext(ctx).Printf("skipping %s %s:%s since it is not in the list of supported architectures.\n", manifest.Platform.Architecture, registry, image)
					continue
				}
			}
			wg.Add(1)
			go func(manifest registryManifestRef, out chan archListResult) {
				defer wg.Done()
				// Ensure that the pointed image is available
				resp, err := r.getImageManifest(ctx, client, auth, r.Scheme, registry, image, manifest.Digest,
					"application/vnd.oci.image.manifest.v1+json",
					"application/vnd.docker.distribution.manifest.v2+json",
				)
				if err != nil {
					log.DefaultLogger.WithContext(ctx).Printf("failed to get pointed manifest for arch %s of %s/%s: %v. Skipping\n", manifest.Platform.Architecture, registry, image, err)
					out <- archListResult{
						err: err,
					}
					return
				}
				r.updateRemaingRateLimits(ctx, registry, resp)
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					// We are filtering out unknown values for the architecture, in order to avoid issues with node assignments.
					// In any case, these will be managed as "defaults"

					out <- archListResult{
						Platform: manifest.Platform,
					}
					return
				} else if resp.StatusCode == http.StatusNotFound {
					log.DefaultLogger.WithContext(ctx).Printf("skipping %s %s:%s since it can't be fetched. StatusCode: %d\n", manifest.Platform.Architecture, registry, image, resp.StatusCode)
					return
				} else {
					log.DefaultLogger.WithContext(ctx).Printf("failed to get pointed manifest for arch %s of %s/%s: statusCode: %d. Skipping\n", manifest.Platform.Architecture, registry, image, resp.StatusCode)
					out <- struct {
						Platform Platform
						err      error
					}{
						err: fmt.Errorf("failed to get pointed manifest for %s/%s: statusCode: %d", registry, image, resp.StatusCode),
					}
					return
				}
			}(manifest, outChan)
		}
		go func() {
			wg.Wait()
			close(outChan)
		}()
		for out := range outChan {
			if out.err != nil {
				return nil, err
			}
			platforms = append(platforms, out.Platform)
		}

	default:
		if response.Architecture != "" {
			platforms = append(platforms, Platform{Architecture: response.Architecture})
		}
	}
	return platforms, nil
}

func (r *PlainRegistry) ListArchs(ctx context.Context, imagePullSecret, image string) ([]Platform, error) {
	ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"image": image})
	transport := http.DefaultTransport
	if r.Transport != nil {
		transport = r.Transport
	}
	registry, image, tag, _ := r.parseImage(image)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	client := http.Client{
		Transport: &WWWAuthenticateTransport{
			Transport:    transport,
			cache:        &r.authenticateCache,
			cacheMetrics: r.cacheMetrics,
		},
	}
	var platforms []Platform
	var err error
	candidates := make(chan AuthenticationToken)
	go func() {
		r.Authenticator.Authenticate(ctx, imagePullSecret, registry, image, tag, candidates)
		close(candidates)
	}()
	for auth := range candidates {
		platforms, err = r.listArchsWithAuth(ctx, client, auth, registry, image, tag)
		if err != nil {
			continue
		}
		if len(platforms) == 0 {
			platforms = append(platforms, Platform{Architecture: "amd64", OS: "linux"})
		}
		return platforms, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, errors.New("Unable to find image architecture")
}
