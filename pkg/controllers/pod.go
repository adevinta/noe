/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0/
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/adevinta/noe/pkg/arch"
	"github.com/adevinta/noe/pkg/log"
	"github.com/adevinta/noe/pkg/registry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type ControllerMetrics struct {
	ImageCount      *prometheus.GaugeVec
	PodDeletedTotal *prometheus.CounterVec
}

func (m ControllerMetrics) MustRegister(reg metrics.RegistererGatherer) {
	reg.MustRegister(
		m.ImageCount,
		m.PodDeletedTotal,
	)
}

func NewHandlerMetrics(prefix string) *ControllerMetrics {
	m := &ControllerMetrics{
		ImageCount: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: prefix,
				Subsystem: "images",
				Name:      "count",
				Help:      "Number of images in the cluster at a given point in time.",
			},
			[]string{"image", "os", "arch", "variant"},
		),
		PodDeletedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: prefix,
				Subsystem: "pods",
				Name:      "deletion_total",
				Help:      "Total number of pods deleted because scheduled on mismatching instance architecture.",
			},
			[]string{"namespace", "status"},
		),
	}
	return m
}

type imageUsage struct {
	platforms []registry.Platform
	refcount  int
}

type Registry interface {
	ListArchs(ctx context.Context, imagePullSecret, image string) ([]registry.Platform, error)
}

// PodReconciler reconciles a Cluster object
type PodReconciler struct {
	client.Client
	Registry       Registry
	podImages      map[string][]string
	imagePlatforms map[string]*imageUsage
	metrics        *ControllerMetrics
}

type PodReconcilerOption func(*PodReconciler)

func WithMetricsRegistry(reg metrics.RegistererGatherer) PodReconcilerOption {
	return func(h *PodReconciler) {
		h.metrics.MustRegister(reg)
	}
}
func WithRegistry(reg Registry) PodReconcilerOption {
	return func(h *PodReconciler) {
		h.Registry = reg
	}
}
func WithClient(cl client.Client) PodReconcilerOption {
	return func(h *PodReconciler) {
		h.Client = cl
	}
}

func NewPodReconciler(prefix string, opts ...PodReconcilerOption) *PodReconciler {
	r := &PodReconciler{
		podImages:      map[string][]string{},
		imagePlatforms: map[string]*imageUsage{},
		metrics:        NewHandlerMetrics(prefix),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"controller": fmt.Sprintf("%T", r), "namespace": req.Namespace, "name": req.Name})

	log.DefaultLogger.WithContext(ctx).Debug("Reconciling Pod")

	pod := &v1.Pod{}
	err := r.Client.Get(ctx, req.NamespacedName, pod)
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, err
	}

	if apierrors.IsNotFound(err) {
		r.deleteFromCaches(req.NamespacedName.String())
		return ctrl.Result{}, nil
	}
	if r.isImageCached(req.NamespacedName.String()) || podIsReady(ctx, pod) {
		log.DefaultLogger.WithContext(ctx).Info("pod was already processed")
		return ctrl.Result{}, nil
	}

	podImages := arch.GetContainerImages(pod.Spec.InitContainers, pod.Spec.Containers)

	ctx, podScheduledOnMatchingNode, err := r.podScheduledOnMatchingNode(ctx, req.Namespace, pod, podImages)
	if err != nil {
		return ctrl.Result{Requeue: true, RequeueAfter: 10 * time.Second}, err
	}

	r.addToCache(req.NamespacedName.String(), podImages)

	if podScheduledOnMatchingNode {
		return ctrl.Result{}, nil
	}

	if _, ok := arch.PodSpecHasNodeArchitectureSelection(ctx, &pod.Spec); ok {
		log.DefaultLogger.WithContext(ctx).Info("pod has node architecture selection")
		return ctrl.Result{}, nil
	}
	log.DefaultLogger.WithContext(ctx).Warnf("pod scheduled on node with no matching platform")

	// TODO: add some checks whether this is really a problem (errors, ...)
	r.deletePodAndNotifyUser(ctx, pod)

	return ctrl.Result{}, nil
}

func (r *PodReconciler) podScheduledOnMatchingNode(ctx context.Context, namespace string, pod *v1.Pod, podImages []string) (context.Context, bool, error) {
	imagePullSecret, err := arch.GetImagePullSecretFromPodSpec(ctx, r.Client, namespace, &pod.Spec)
	if err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("Failed to get image pull secret from pod spec")
	}

	nodeOs := ""
	nodeArch := ""
	if pod.Spec.NodeName != "" {
		// the pod was already scheduled
		node := v1.Node{}
		err := r.Client.Get(ctx, client.ObjectKey{Name: pod.Spec.NodeName}, &node)
		if err != nil {
			ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"node": pod.Spec.NodeName, "pod": pod.Name})
			log.DefaultLogger.WithContext(ctx).WithError(err).Error("Failed to get node spec")
			return ctx, false, err
		}
		if value, ok := node.Labels["kubernetes.io/arch"]; ok {
			nodeArch = value
		} else if value, ok := node.Labels["beta.kubernetes.io/arch"]; ok {
			nodeArch = value
		}
		if value, ok := node.Labels["kubernetes.io/os"]; ok {
			nodeOs = value
		} else if value, ok := node.Labels["beta.kubernetes.io/os"]; ok {
			nodeOs = value
		}
		ctx = log.AddLogFieldsToContext(ctx, logrus.Fields{"node": pod.Spec.NodeName, "nodeOs": nodeOs, "nodeArch": nodeArch})
	}

	podScheduledOnMatchingNode := true
	for _, image := range podImages {
		platforms, err := r.Registry.ListArchs(ctx, imagePullSecret, image)
		if err != nil {
			return ctx, false, err
		}
		r.incrementPlatformStatistics(image, platforms)
		if nodeOs != "" && nodeArch != "" {
			hasMatchingPlatform := false
			for _, platform := range platforms {
				if platform.OS == nodeOs && platform.Architecture == nodeArch {
					hasMatchingPlatform = true
				}
			}
			if !hasMatchingPlatform {
				podScheduledOnMatchingNode = false
			}
		}
	}
	return ctx, podScheduledOnMatchingNode, nil
}

func (r *PodReconciler) addToCache(namespacedName string, podImages []string) {
	r.podImages[namespacedName] = podImages
}

func (r *PodReconciler) isImageCached(namespacedName string) bool {
	_, ok := r.podImages[namespacedName]
	return ok
}

func (r *PodReconciler) deleteFromCaches(namespacedName string) {
	images, ok := r.podImages[namespacedName]
	if ok {
		for _, image := range images {
			r.decrementPlatformStatistics(image)
		}

		delete(r.podImages, namespacedName)
	}
}

func (r *PodReconciler) incrementPlatformStatistics(image string, platforms []registry.Platform) {
	if _, ok := r.imagePlatforms[image]; !ok {
		r.imagePlatforms[image] = &imageUsage{platforms: platforms}
	} else {
		for _, oldPlatform := range r.imagePlatforms[image].platforms {
			found := false
			for _, newPlatform := range platforms {
				if oldPlatform.OS == newPlatform.OS && oldPlatform.Architecture == newPlatform.Architecture && oldPlatform.Variant == newPlatform.Variant {
					found = true
					break
				}
			}
			if !found {
				// An image currently in use has been updated and no longer supports the platform.
				r.metrics.ImageCount.DeleteLabelValues(image, oldPlatform.OS, oldPlatform.Architecture, oldPlatform.Variant)
			}
		}
		r.imagePlatforms[image].platforms = platforms
	}
	r.imagePlatforms[image].refcount++
	for _, platform := range platforms {
		r.metrics.ImageCount.WithLabelValues(image, platform.OS, platform.Architecture, platform.Variant).Inc()
	}
}

func (r *PodReconciler) decrementPlatformStatistics(image string) {
	if usage, ok := r.imagePlatforms[image]; ok {
		usage.refcount--
		for _, platform := range usage.platforms {
			r.metrics.ImageCount.WithLabelValues(image, platform.OS, platform.Architecture, platform.Variant).Dec()
			if usage.refcount == 0 {
				r.metrics.ImageCount.DeleteLabelValues(image, platform.OS, platform.Architecture, platform.Variant)
			}
		}
		if usage.refcount == 0 {
			delete(r.imagePlatforms, image)
		}
	}
}

func (r *PodReconciler) deletePodAndNotifyUser(ctx context.Context, pod *v1.Pod) {
	err := r.Client.Delete(ctx, pod)

	eventType := "Normal"
	nameSuffix := "-deleted-pod"
	messagePrefix := "Pod(s) was deleted because it was scheduled on a node with a platform that is not supported by the image:"
	if err != nil {
		eventType = "Warning"
		nameSuffix = "-failed-to-delete-pod"
		messagePrefix = "Failed to delete pod(s) scheduled on a node with a platform that is not supported by the image. Pod(s):"
		r.metrics.PodDeletedTotal.WithLabelValues(pod.Namespace, "failed").Inc()
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("Failed to delete pod scheduled on node with no matching platform")
	} else {
		r.metrics.PodDeletedTotal.WithLabelValues(pod.Namespace, "success").Inc()
		log.DefaultLogger.WithContext(ctx).Info("Deleted pod scheduled on node with no matching platform")
	}
	// give visibility to the user that the pod has been deleted for both the pod and its owner
	upsertPodDeletionEvent(ctx, r.Client, pod, pod.GetName(), eventType, nameSuffix, messagePrefix)
	if len(pod.OwnerReferences) > 0 {
		for _, ref := range pod.OwnerReferences {
			if ref.Controller != nil && *ref.Controller {
				u := &unstructured.Unstructured{}
				u.SetAPIVersion(ref.APIVersion)
				u.SetKind(ref.Kind)
				u.SetName(ref.Name)
				u.SetNamespace(pod.Namespace)
				u.SetUID(ref.UID)
				upsertPodDeletionEvent(
					ctx,
					r.Client,
					u,
					pod.GetName(),
					eventType,
					nameSuffix,
					messagePrefix,
				)
			}
		}
	}
}

// podIsReady checks if a pod condition is ready which means the readiness probe of all containers are OK.
//
// from the documentation: https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/#pod-readiness-gate
// if a Pod's condition is Ready, it means all of its containers are running properly and those with a readinessProbe are passing the probe. which means it has been running in the right node's architecture.
// If any container in the Pod is not running or fails its readinessProbe, the Pod's Ready condition will be False.
// In other words, when a Pod is in a Ready condition, you can be assured that all its containers are running properly.
// If there were issues with any container, the Pod would not be marked as Ready.```
func podIsReady(ctx context.Context, pod *v1.Pod) bool {

	// Running: The pod has been bound to a node, and all of the containers have been created.
	// At least one container is still running, or is in the process of starting or restarting.
	// See: https://github.com/kubernetes/api/blob/b01b44926aa4920c8c8c008003e16316cf59ffda/core/v1/types.go#L4238C1-L4239C93
	if pod.Status.Phase != v1.PodRunning {
		return false
	}

	ready := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == "Ready" && condition.Status == "True" {
			ready = true
			break
		}
	}
	log.DefaultLogger.WithContext(ctx).Debugf("Pod %v is in status, %v", pod.Name, ready)
	return ready
}

func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Pod{}).
		Complete(r)
}

func upsertPodDeletionEvent(ctx context.Context, k8sClient client.Client, owner client.Object, podName, eventType, nameSuffix, messagePrefix string) {
	evt := v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: owner.GetNamespace(),
			Name:      owner.GetName() + "-" + nameSuffix,
		},
	}
	_, err := ctrl.CreateOrUpdate(ctx, k8sClient, &evt, func() error {
		if evt.Message == "" {
			evt.Message = messagePrefix
		}
		evt.Message += "\n" + podName
		evt.Type = eventType
		if evt.Series == nil {
			evt.Series = &v1.EventSeries{}
		}
		evt.Count++
		evt.Series.Count++
		evt.Series.LastObservedTime = metav1.NewMicroTime(time.Now())
		evt.Reason = "PlatformMismatch"
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
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("Failed to create pod deletion event")
	}
}
