package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/adevinta/noe/pkg/httputils"
	"github.com/adevinta/noe/pkg/log"

	"github.com/adevinta/noe/pkg/arch"
	"github.com/adevinta/noe/pkg/controllers"
	"github.com/adevinta/noe/pkg/registry"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func main() {
	var preferredArch, schedulableArchs, systemOS string
	var metricsAddr string
	var registryProxies, matchNodeLabels string

	flag.StringVar(&preferredArch, "preferred-arch", "amd64", "Preferred architecture when placing pods")
	flag.StringVar(&schedulableArchs, "cluster-schedulable-archs", "", "Comma separated list of architectures schedulable in the cluster")
	flag.StringVar(&systemOS, "system-os", "linux", "Sole OS supported by the system")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&metricsAddr, "registry-proxies", "", "Proxies to substitute in the registry URL in the form of docker.io=docker-proxy.company.corp,quay.io=quay-proxy.company.corp")
	flag.StringVar(&matchNodeLabels, "match-node-labels", "", "A set of pod label keys to match against node labels in the form of key1,key2")
	flag.Parse()

	Main(signals.SetupSignalHandler(), "./", preferredArch, schedulableArchs, systemOS, metricsAddr, registryProxies, matchNodeLabels)
}

func Main(ctx context.Context, certDir, preferredArch, schedulableArchs, systemOS, metricsAddr, registryProxies, matchNodeLabels string) {
	var schedulableArchSlice []string

	if schedulableArchs != "" {
		schedulableArchSlice = strings.Split(schedulableArchs, ",")
	}

	if preferredArch != "" && schedulableArchs != "" && !slices.Contains(schedulableArchSlice, preferredArch) {
		err := fmt.Errorf("preferred architecture is not schedulable in the cluster")
		log.DefaultLogger.WithError(err).Error("refusing to continue")
		os.Exit(1)
	}

	// Setup a Manager
	log.DefaultLogger.WithContext(ctx).Println("setting up manager")
	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		MetricsBindAddress: metricsAddr,
		Logger:             log.NewLogr(log.DefaultLogger),
	})
	if err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("unable to set up overall controller manager")
		os.Exit(1)
	}

	var containerRegistry registry.Registry = registry.NewPlainRegistry(
		registry.WithDockerProxies(registry.ParseRegistryProxies(registryProxies)),
		registry.WithTransport(httputils.NewMonitoredRoundTripper(
			metrics.Registry,
			prometheus.Opts{
				Namespace: "noe",
				Subsystem: "http_client",
			},
			registry.RegistryLabeller,
		)),
	)
	containerRegistry = registry.NewCachedRegistry(containerRegistry, 1*time.Hour, registry.WithCacheMetricsRegistry(metrics.Registry))

	if err = controllers.NewPodReconciler(
		"noe",
		controllers.WithClient(mgr.GetClient()),
		controllers.WithRegistry(containerRegistry),
		controllers.WithMetricsRegistry(metrics.Registry),
	).SetupWithManager(mgr); err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("unable to create pod controller")
		os.Exit(1)
	}

	// Setup webhooks
	log.DefaultLogger.WithContext(ctx).Println("setting up webhook server")
	hookServer := mgr.GetWebhookServer()

	hookServer.Port = 8443
	hookServer.CertDir = certDir
	decoder, err := admission.NewDecoder(mgr.GetScheme())
	if err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("unable to create admission decoder")
		os.Exit(1)
	}

	admissionHook := &webhook.Admission{
		Handler: arch.NewHandler(
			mgr.GetClient(),
			containerRegistry,
			arch.WithMetricsRegistry(metrics.Registry),
			arch.WithArchitecture(preferredArch),
			arch.WithSchedulableArchitectures(schedulableArchSlice),
			arch.WithOS(systemOS),
			arch.WithDecoder(decoder),
			arch.WithMatchNodeLabels(arch.ParseMatchNodeLabels(matchNodeLabels)),
		),
	}
	admissionHook.InjectLogger(log.NewLogr(log.DefaultLogger))

	log.DefaultLogger.WithContext(ctx).Println("registering webhooks to the webhook server")
	hookServer.Register(
		"/mutate",
		httputils.InstrumentHandler(
			metrics.Registry,
			prometheus.Opts{
				Namespace: "noe",
				Subsystem: "webhook",
			},
			httputils.StandardHandlerLabeller,
			admissionHook,
		),
	)

	log.DefaultLogger.WithContext(ctx).Println("starting manager")
	if err := mgr.Start(ctx); err != nil {
		log.DefaultLogger.WithContext(ctx).WithError(err).Error("unable to run manager")
		os.Exit(1)
	}
}
