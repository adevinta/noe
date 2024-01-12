package arch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/adevinta/noe/pkg/log"
	"github.com/adevinta/noe/pkg/registry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const archKey = "kubernetes.io/arch"

type HandlerMetrics struct {
	ImagePullSecretFailed             *prometheus.CounterVec
	RegistryErrors                    *prometheus.CounterVec
	UpdateSkept                       *prometheus.CounterVec
	ArchSelectorInjected              *prometheus.CounterVec
	PreferredArchitectureNotAvailable *prometheus.CounterVec
	NodeMatchSelector                 *prometheus.CounterVec
}

func (m HandlerMetrics) MustRegister(reg metrics.RegistererGatherer) {
	reg.MustRegister(
		m.ImagePullSecretFailed,
		m.UpdateSkept,
		m.RegistryErrors,
		m.ArchSelectorInjected,
		m.PreferredArchitectureNotAvailable,
		m.NodeMatchSelector,
	)
}

func NewHandlerMetrics(prefix string) *HandlerMetrics {
	m := &HandlerMetrics{
		ImagePullSecretFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: "hook",
			Name:      "image_pull_secret_failed_total",
			Help:      "Number of times the image pull secret could not be retrieved",
		}, []string{"namespace"}),
		RegistryErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: "hook",
			Name:      "registry_errors_total",
			Help:      "Number of times the registry returned an error",
		}, []string{"image"}),
		UpdateSkept: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: "hook",
			Name:      "update_skip_total",
			Help:      "Number of times the update was skipped",
		}, []string{"reason"}),
		ArchSelectorInjected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: "hook",
			Name:      "arch_selector_injected_total",
			Help:      "Number of times the arch selector was injected",
		}, []string{"namespace", "selector"}),
		PreferredArchitectureNotAvailable: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: "hook",
			Name:      "preferred_architecture_not_available_total",
			Help:      "Number of times the preferred architecture was not available",
		}, []string{"namespace"}),
		NodeMatchSelector: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Subsystem: "hook",
			Name:      "node_match_injections_total",
			Help:      "Number of times the node selection to match pod labels was injected",
		}, []string{"namespace", "label"}),
	}
	return m
}

type Registry interface {
	ListArchs(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error)
}

type RegistryFunc func(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error)

func (f RegistryFunc) ListArchs(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error) {
	return f(ctx, imagePullSecret, image)
}

type warning struct {
	msg string
}

func (w warning) Error() string {
	return w.msg
}

type HandlerOption func(*Handler)
type Handler struct {
	Client                   client.Client
	Registry                 Registry
	matchNodeLabels          []string
	metrics                  HandlerMetrics
	decoder                  *admission.Decoder
	preferredArchitecture    string
	schedulableArchitectures []string
	systemOS                 string
}

func NewHandler(client client.Client, registry Registry, opts ...HandlerOption) *Handler {
	h := &Handler{Client: client, Registry: registry, metrics: *NewHandlerMetrics(
		"noe",
	)}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func WithMetricsRegistry(reg metrics.RegistererGatherer) HandlerOption {
	return func(h *Handler) {
		h.metrics.MustRegister(reg)
	}
}

func WithArchitecture(arch string) HandlerOption {
	return func(h *Handler) {
		h.preferredArchitecture = arch
	}
}

func WithSchedulableArchitectures(archs []string) HandlerOption {
	return func(h *Handler) {
		h.schedulableArchitectures = archs
	}
}

func WithOS(os string) HandlerOption {
	return func(h *Handler) {
		h.systemOS = os
	}
}

func WithDecoder(decoder *admission.Decoder) HandlerOption {
	return func(h *Handler) {
		h.decoder = decoder
	}
}

func WithMatchNodeLabels(labels []string) HandlerOption {
	return func(h *Handler) {
		h.matchNodeLabels = labels
	}
}

func ParseMatchNodeLabels(labels string) []string {
	return strings.Split(labels, ",")
}

func GetImagePullSecretFromPodSpec(ctx context.Context, k8sClient client.Client, namespace string, podSpec *v1.PodSpec) (string, error) {
	dockerCfg := registry.DockerConfig{
		Auths: registry.DockerAuths{},
	}
	var lastErr error
	for _, secretName := range podSpec.ImagePullSecrets {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      secretName.Name,
			},
		}
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), secret)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).WithField("secret", secretName.Name).WithError(err).Printf("failed to read image pull secret")
			lastErr = errors.New("failed to read image pull secret")
			continue
		}
		cfg := registry.DockerConfig{}
		err = json.Unmarshal(secret.Data[".dockerconfigjson"], &cfg)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).WithField("secret", secretName.Name).WithError(err).Printf("failed to decode image pull secret")
			lastErr = errors.New("failed to read image pull secret")
			continue
		}
		for k, v := range cfg.Auths {
			dockerCfg.Auths[k] = v
		}
	}
	imagePullSecret := ""
	if len(dockerCfg.Auths) > 0 {
		data, err := json.Marshal(dockerCfg)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).WithError(err).Printf("failed to encode image pull secret")
		} else {
			imagePullSecret = string(data)
		}
	}
	return imagePullSecret, lastErr
}

func GetContainerImages(containerLists ...[]v1.Container) []string {
	images := []string{}
	for _, containerList := range containerLists {
		for _, container := range containerList {
			if container.Image != "" {
				images = append(images, container.Image)
			}
		}
	}
	return images
}

func PodSpecHasNodeArchitectureSelection(ctx context.Context, podSpec *v1.PodSpec) (string, bool) {
	for _, key := range []string{"beta." + archKey, archKey} {
		if _, ok := podSpec.NodeSelector[key]; ok {
			log.DefaultLogger.WithContext(ctx).Println("pod affinity was already set")

			return "node-selector found", true
		}
	}
	if podSpec.Affinity != nil && podSpec.Affinity.NodeAffinity != nil && podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil && podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms != nil {
		for _, affinity := range podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms {
			if affinity.MatchExpressions != nil {
				for _, exp := range affinity.MatchExpressions {
					if exp.Key == archKey || exp.Key == "beta."+archKey {
						log.DefaultLogger.WithContext(ctx).Println("pod affinity was already set")
						return "node affinity label selector found", true
					}
				}
			}
			if affinity.MatchFields != nil {
				for _, exp := range affinity.MatchFields {
					if exp.Key == "metadata.name" {
						log.DefaultLogger.WithContext(ctx).WithField("matchFields", exp).Println("pod affinity was already set")
						return "node affinity field selector found", true
					}
				}
			}
		}
	}
	return "", false
}

func (h *Handler) addPodNodeMatchingLabels(namespace string, podLabels map[string]string, podSpec *v1.PodSpec) {
	for _, key := range h.matchNodeLabels {
		if val, ok := podLabels[key]; ok {
			h.metrics.NodeMatchSelector.WithLabelValues(namespace, key).Inc()
			if podSpec.NodeSelector == nil {
				if podSpec.Affinity == nil {
					podSpec.Affinity = &v1.Affinity{}
				}
				if podSpec.Affinity.NodeAffinity == nil {
					podSpec.Affinity.NodeAffinity = &v1.NodeAffinity{}
				}
				if podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
					podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &v1.NodeSelector{}
				}
				newAffinity := v1.NodeSelectorTerm{
					MatchExpressions: []v1.NodeSelectorRequirement{
						{
							Key:      key,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{val},
						},
					},
				}
				podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = append(
					podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms,
					newAffinity,
				)
			} else {
				podSpec.NodeSelector[key] = val
			}
		}
	}
}

func (h *Handler) updatePodSpec(ctx context.Context, namespace string, podLabels map[string]string, podSpec *v1.PodSpec) error {
	if podSpec.NodeName != "" {
		log.DefaultLogger.WithContext(ctx).WithField("nodeName", podSpec.NodeName).Printf("pod is already scheduled")
		return nil
	}
	reason, found := PodSpecHasNodeArchitectureSelection(ctx, podSpec)
	if found {
		h.metrics.UpdateSkept.WithLabelValues(reason).Inc()
		h.addPodNodeMatchingLabels(namespace, podLabels, podSpec)
		return nil
	}

	var preferredArchIsDefault bool
	preferredArch, preferredArchDefined := podLabels["arch.noe.adevinta.com/preferred"]
	if preferredArch != "" && !h.isArchSupported(preferredArch) {
		log.DefaultLogger.WithContext(ctx).WithField("preferredArch", preferredArch).Println("ignoring unsupported user preferred architecture")
		preferredArch = ""
	}
	if preferredArch == "" && h.preferredArchitecture != "" {
		preferredArch = h.preferredArchitecture
		preferredArchDefined = true
		preferredArchIsDefault = true
		ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"preferredArch": preferredArch})
		log.DefaultLogger.WithContext(ctx).Println("selecting default preferred architecture")
	}

	commonArchitectures := map[string]struct{}{}
	imagePullSecret, err := GetImagePullSecretFromPodSpec(ctx, h.Client, namespace, podSpec)
	if err != nil {
		h.metrics.ImagePullSecretFailed.WithLabelValues(namespace).Inc()
	}
	firstImage := true
	for _, image := range GetContainerImages(podSpec.Containers, podSpec.InitContainers) {
		ctx := log.AddLogFieldsToContext(ctx, logrus.Fields{"image": image})

		platforms, err := h.Registry.ListArchs(ctx, imagePullSecret, image)
		if err != nil {
			h.metrics.RegistryErrors.WithLabelValues(image).Inc()
			log.DefaultLogger.WithContext(ctx).WithError(err).Printf("unable to list image archs")
			return nil
		}

		imageArchitectures := map[string]struct{}{}
		for _, platform := range platforms {
			if platform.OS != "" && platform.OS != h.systemOS {
				log.DefaultLogger.WithContext(ctx).WithField("os", platform.OS).Info("Skipped OS does not match system's")
				continue
			}
			if !h.isArchSupported(platform.Architecture) {
				log.DefaultLogger.WithContext(ctx).WithField("arch", platform.OS).Info("Skipped arch does not match system's")
				continue
			}
			imageArchitectures[platform.Architecture] = struct{}{}
		}

		if firstImage {
			commonArchitectures = imageArchitectures
			firstImage = false
		} else {
			for k := range commonArchitectures {
				if _, ok := imageArchitectures[k]; !ok {
					delete(commonArchitectures, k)
				}
			}
		}
	}
	if firstImage {
		log.DefaultLogger.WithContext(ctx).Println("no image found")
		h.addPodNodeMatchingLabels(namespace, podLabels, podSpec)
		return nil
	}
	ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"compatibleImages": commonArchitectures})
	if len(commonArchitectures) == 0 {
		log.DefaultLogger.WithContext(ctx).Println("no common architecture")
		h.addPodNodeMatchingLabels(namespace, podLabels, podSpec)
		return errors.New("could not find a common image architecture across all containers")
	}

	if _, ok := commonArchitectures[preferredArch]; ok && preferredArchDefined {
		if podSpec.NodeSelector == nil {
			podSpec.NodeSelector = make(map[string]string)
		}
		podSpec.NodeSelector[archKey] = preferredArch
		log.DefaultLogger.WithContext(ctx).Info("updating nodeSelector to match preferred architecture")
		h.metrics.ArchSelectorInjected.WithLabelValues(namespace, "preferred").Inc()
	} else {
		if podSpec.Affinity == nil {
			podSpec.Affinity = &v1.Affinity{}
		}
		if podSpec.Affinity.NodeAffinity == nil {
			podSpec.Affinity.NodeAffinity = &v1.NodeAffinity{}
		}
		if podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
			podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = &v1.NodeSelector{}
		}

		newAffinity := v1.NodeSelectorTerm{
			MatchExpressions: []v1.NodeSelectorRequirement{
				{
					Key:      archKey,
					Operator: v1.NodeSelectorOpIn,
					Values:   keys(commonArchitectures),
				},
			},
		}
		log.DefaultLogger.WithContext(ctx).WithField("affinity", newAffinity).Infof("updated pod affinity")

		podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = append(
			podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms,
			newAffinity,
		)
		h.metrics.ArchSelectorInjected.WithLabelValues(namespace, "affinity").Inc()
		if preferredArchDefined {
			log.DefaultLogger.WithContext(ctx).Info("preferred architecture is not supported by all images")
			if !preferredArchIsDefault {
				h.addPodNodeMatchingLabels(namespace, podLabels, podSpec)
				return warning{msg: fmt.Sprintf("could not select preferred arch: %s", preferredArch)}
			}
		}
	}

	h.addPodNodeMatchingLabels(namespace, podLabels, podSpec)
	return nil
}

func (h *Handler) Handle(ctx context.Context, req admission.Request) admission.Response {
	var warningMessage string

	if h.decoder == nil {
		log.DefaultLogger.WithContext(ctx).Println("failed to decode object, no decoder provided")
		return admission.Allowed("Unable to decode object, skipping update")
	}

	resp := admission.Response{}
	ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"request": map[string]interface{}{"operation": req.Operation, "namespace": req.Namespace, "name": req.Name, "kind": req.Kind}})
	switch req.Kind.Kind {
	case "Pod":

		if req.Operation != admissionv1.Create {
			log.DefaultLogger.WithContext(ctx).Printf("skipping adding node selector to pod updates")
			return admission.Allowed("skipping adding node selector to pod updates")
		}

		pod := &v1.Pod{}

		err := h.decoder.Decode(req, pod)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).Println("failed to decode pod")
			h.generatePodInjectionFailedEvent(ctx, pod, fmt.Errorf("failed to decode pod: %w", err))
			return admission.Errored(http.StatusBadRequest, err)
		}
		updated := pod.DeepCopy()

		err = h.updatePodSpec(ctx, pod.Namespace, pod.Labels, &updated.Spec)
		if err != nil {
			var warningErr warning
			if errors.As(err, &warningErr) {
				warningMessage = err.Error()
			} else {
				h.generatePodInjectionFailedEvent(ctx, pod, err)
				return admission.Denied(err.Error())
			}
		}
		updatedRaw, err := toJson(updated)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).Println("failed to generate patch:", err)
			h.generatePodInjectionFailedEvent(ctx, pod, fmt.Errorf("failed to generate patch: %w", err))
			admission.Errored(http.StatusBadRequest, err)
		}
		resp = admission.PatchResponseFromRaw(req.Object.Raw, updatedRaw)
		if warningMessage != "" {
			resp = resp.WithWarnings(warningMessage)
		}
		err = resp.Complete(req)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).Println("failed to patch response:", err)
		} else {
			if len(resp.Patches) > 0 {
				h.generatePodInjectionSuccessEvent(ctx, pod)
			}
		}
	case "DaemonSet":
		ds := &appsv1.DaemonSet{}
		err := h.decoder.Decode(req, ds)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).Println("failed to decode pod")
			h.generateInjectionFailedEvent(ctx, ds, fmt.Errorf("failed to decode pod: %w", err))
			return admission.Errored(http.StatusBadRequest, err)
		}
		updated := ds.DeepCopy()
		err = h.updatePodSpec(ctx, ds.Namespace, ds.Spec.Template.Labels, &updated.Spec.Template.Spec)
		if err != nil {
			var warningErr warning
			if errors.As(err, &warningErr) {
				warningMessage = err.Error()
			} else {
				h.generateInjectionFailedEvent(ctx, ds, err)
				return admission.Denied(err.Error())
			}
		}
		updatedRaw, err := toJson(updated)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).Println("failed to generate patch:", err)
			admission.Errored(http.StatusBadRequest, err)
			h.generateInjectionFailedEvent(ctx, ds, fmt.Errorf("failed to generate patch: %w", err))
		}
		resp = admission.PatchResponseFromRaw(req.Object.Raw, updatedRaw)
		if warningMessage != "" {
			resp = resp.WithWarnings(warningMessage)
		}
		err = resp.Complete(req)
		if err != nil {
			log.DefaultLogger.WithContext(ctx).Println("failed to patch response:", err)
		} else {
			if len(resp.Patches) > 0 {
				h.generateInjectionSuccessEvent(ctx, ds)
			}
		}
	default:
		log.DefaultLogger.WithContext(ctx).Printf("nothing to do for type %v", req.Kind.Kind)
		resp = admission.Allowed(fmt.Sprintf("nothing to do for type %v", req.Kind.Kind))
	}
	return resp
}

func (h *Handler) isArchSupported(arch string) bool {
	if len(h.schedulableArchitectures) == 0 {
		return true
	}
	return slices.Contains(h.schedulableArchitectures, arch)
}

// Handler implements admission.DecoderInjector.
// A decoder will be automatically injected.

// InjectDecoder injects the decoder.
func (a *Handler) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}

func (h *Handler) generatePodInjectionSuccessEvent(ctx context.Context, pod *v1.Pod) {
	f := func(string) string {
		return fmt.Sprintf("Injected node selector to pod %v", pod.Name)
	}
	upsertNodeSelectorInjectionEvent(
		log.AddLogFieldsToContext(ctx, logrus.Fields{"object": "Pod", "objectName": pod.Name}),
		h.Client,
		pod,
		"Normal",
		"InjectedNodeSelector",
		"injection-succeeded",
		f,
	)
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			u := &unstructured.Unstructured{}
			u.SetAPIVersion(ref.APIVersion)
			u.SetKind(ref.Kind)
			u.SetName(ref.Name)
			u.SetNamespace(pod.Namespace)
			u.SetUID(ref.UID)
			upsertNodeSelectorInjectionEvent(
				log.AddLogFieldsToContext(ctx, logrus.Fields{"object": ref.Kind, "objectName": ref.Name}),
				h.Client,
				u,
				"Normal",
				"InjectedNodeSelector",
				"injection-succeeded",
				f,
			)
		}
	}
}

func (h *Handler) generatePodInjectionFailedEvent(ctx context.Context, pod *v1.Pod, err error) {
	f := func(string) string {
		return fmt.Sprintf("Failed to inject node selector to pod %v: %v", pod.Name, err)
	}
	upsertNodeSelectorInjectionEvent(
		log.AddLogFieldsToContext(ctx, logrus.Fields{"object": "Pod", "objectName": pod.Name}),
		h.Client,
		pod,
		"Warning",
		"FailedToInjectNodeSelector",
		"injection-failed",
		f,
	)
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			u := &unstructured.Unstructured{}
			u.SetAPIVersion(ref.APIVersion)
			u.SetKind(ref.Kind)
			u.SetName(ref.Name)
			u.SetNamespace(pod.Namespace)
			u.SetUID(ref.UID)
			upsertNodeSelectorInjectionEvent(
				log.AddLogFieldsToContext(ctx, logrus.Fields{"object": ref.Kind, "objectName": ref.Name}),
				h.Client,
				u,
				"Warning",
				"FailedToInjectNodeSelector",
				"injection-failed",
				f,
			)
		}
	}
}

func (h *Handler) generateInjectionFailedEvent(ctx context.Context, obj client.Object, err error) {
	f := func(string) string {
		return fmt.Sprintf("Failed to inject node selector to %v %v: %v", obj.GetObjectKind(), obj.GetName(), err)
	}
	upsertNodeSelectorInjectionEvent(
		ctx,
		h.Client,
		obj,
		"Warning",
		"FailedToInjectNodeSelector",
		"injection-failed",
		f,
	)
}

func (h *Handler) generateInjectionSuccessEvent(ctx context.Context, obj client.Object) {
	f := func(string) string {
		return fmt.Sprintf("Injected node selector to %v %v", obj.GetObjectKind(), obj.GetName())
	}
	upsertNodeSelectorInjectionEvent(
		ctx,
		h.Client,
		obj,
		"Normal",
		"InjectedNodeSelector",
		"injection-succeeded",
		f,
	)
}

func keys(set map[string]struct{}) []string {
	r := []string{}
	for k := range set {
		r = append(r, k)
	}
	slices.Sort(r)
	return r
}

func toJson(obj runtime.Object) ([]byte, error) {
	return json.Marshal(obj)
}

func upsertNodeSelectorInjectionEvent(ctx context.Context, k8sClient client.Client, owner client.Object, podName, eventType, nameSuffix string, messageFunc func(string) string) {
	evt := v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: owner.GetNamespace(),
			Name:      owner.GetName() + "-" + nameSuffix,
		},
	}
	_, err := ctrl.CreateOrUpdate(ctx, k8sClient, &evt, func() error {
		evt.Message = messageFunc(evt.Message)
		evt.Message += "\n" + podName
		evt.Type = eventType
		if evt.Series == nil {
			evt.Series = &v1.EventSeries{}
		}
		evt.Count++
		evt.Series.Count++
		evt.Series.LastObservedTime = metav1.NewMicroTime(time.Now())
		evt.Reason = "AutomaticNodeSelectorInjection"
		evt.Source.Component = "noe"
		evt.LastTimestamp = metav1.NewTime(time.Now())
		if evt.FirstTimestamp.IsZero() {
			evt.FirstTimestamp = evt.LastTimestamp
		}
		evt.InvolvedObject = v1.ObjectReference{
			APIVersion: owner.GetObjectKind().GroupVersionKind().GroupVersion().String(),
			Kind:       owner.GetObjectKind().GroupVersionKind().Kind,
			Name:       owner.GetName(),
			Namespace:  owner.GetNamespace(),
			UID:        owner.GetUID(),
		}
		return nil
	})
	if err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("Failed to create pod node selector injection event")
	} else {
		log.DefaultLogger.WithContext(ctx).Trace("Created pod node selector injection event")
	}
}
