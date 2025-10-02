package main

import (
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
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var mainContext = signals.SetupSignalHandler()

func main() {
	var preferredArch, schedulableArchs, systemOS string
	var metricsAddr, healthProbeAddr string
	var registryProxies, matchNodeLabels string
	var certDir string
	var kubeletImageCredentialProviderBinBir, kubeletImageCredentialProviderConfig string
	var privateregistriesPatterns string
	var enableLeaderElection bool
	const leaderElectionID string = "noe-controller-leader"

	flag.StringVar(&preferredArch, "preferred-arch", "amd64", "Preferred architecture when placing pods")
	flag.StringVar(&schedulableArchs, "cluster-schedulable-archs", "", "Comma separated list of architectures schedulable in the cluster")
	flag.StringVar(&systemOS, "system-os", "linux", "Sole OS supported by the system")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&healthProbeAddr, "health-probe-addr", ":8081", "The address the health probe endpoint binds to.")
	flag.StringVar(&certDir, "cert-dir", "./", "The directory where the TLS certificates are stored")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&registryProxies, "registry-proxies", "", "Proxies to substitute in the registry URL in the form of docker.io=docker-proxy.company.corp,quay.io=quay-proxy.company.corp")
	flag.StringVar(&matchNodeLabels, "match-node-labels", "", "A set of pod label keys to match against node labels in the form of key1,key2")
	flag.StringVar(&kubeletImageCredentialProviderBinBir, "image-credential-provider-bin-dir", "", "The path to the directory where credential provider plugin binaries are located.")
	flag.StringVar(&kubeletImageCredentialProviderConfig, "image-credential-provider-config", "", "The path to the credential provider plugin config file.")
	flag.StringVar(&privateregistriesPatterns, "private-registries", "", "Comma separated list to match private registries. Any image matching those patterns will be considered as private and anonymous pull will be disabled. The patterns are matched using kubelet matching rules. (see https://kubernetes.io/docs/tasks/administer-cluster/kubelet-credential-provider/#configure-image-matching)")

	flag.Parse()

	var schedulableArchSlice []string

	if schedulableArchs != "" {
		schedulableArchSlice = strings.Split(schedulableArchs, ",")
	}

	if preferredArch != "" && schedulableArchs != "" && !slices.Contains(schedulableArchSlice, preferredArch) {
		err := fmt.Errorf("preferred architecture is not schedulable in the cluster")
		log.DefaultLogger.WithError(err).Error("refusing to continue")
		os.Exit(1)
	}
	ctrllog.SetLogger(log.NewLogr(log.DefaultLogger))
	// Setup a Manager
	log.DefaultLogger.WithContext(mainContext).Println("setting up manager")
	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    8443,
			CertDir: certDir,
		}),
		HealthProbeBindAddress: healthProbeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       leaderElectionID,
		Logger:                 log.NewLogr(log.DefaultLogger),
	})
	if err != nil {
		log.DefaultLogger.WithContext(mainContext).WithError(err).Error("unable to set up overall controller manager")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.DefaultLogger.WithContext(mainContext).WithError(err).Error("unable to set up healthz check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.DefaultLogger.WithContext(mainContext).WithError(err).Error("unable to set up readyz check")
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
		registry.WithRegistryMetricRegistry(metrics.Registry),
		registry.WithSchedulableArchitectures(schedulableArchSlice),
		registry.WithAuthenticator(registry.NewAuthenticator(kubeletImageCredentialProviderConfig, kubeletImageCredentialProviderBinBir, strings.Split(privateregistriesPatterns, ","))),
	)
	containerRegistry = registry.NewCachedRegistry(containerRegistry, 1*time.Hour, registry.WithCacheMetricsRegistry(metrics.Registry))

	if err = controllers.NewPodReconciler(
		"noe",
		controllers.WithClient(mgr.GetClient()),
		controllers.WithRegistry(containerRegistry),
		controllers.WithMetricsRegistry(metrics.Registry),
	).SetupWithManager(mgr); err != nil {
		log.DefaultLogger.WithContext(mainContext).WithError(err).Error("unable to create pod controller")
		os.Exit(1)
	}

	// Setup webhooks
	log.DefaultLogger.WithContext(mainContext).Println("setting up webhook server")
	hookServer := mgr.GetWebhookServer()

	decoder := admission.NewDecoder(mgr.GetScheme())

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

	log.DefaultLogger.WithContext(mainContext).Println("registering webhooks to the webhook server")
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

	log.DefaultLogger.WithContext(mainContext).Println("starting manager")
	if err := mgr.Start(mainContext); err != nil {
		log.DefaultLogger.WithContext(mainContext).WithError(err).Error("unable to run manager")
		os.Exit(1)
	}
}
