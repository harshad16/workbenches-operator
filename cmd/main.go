/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package main is the entry point for the workbenches-operator.
package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	componentsv1alpha1 "github.com/opendatahub-io/workbenches-operator/api/v1alpha1"
	"github.com/opendatahub-io/workbenches-operator/internal/controller"
	"github.com/opendatahub-io/workbenches-operator/internal/platform"
	"github.com/opendatahub-io/workbenches-operator/internal/tlsconfig"
	"github.com/opendatahub-io/workbenches-operator/internal/webhook"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(componentsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(configv1.Install(scheme))
}

func main() {
	var (
		metricsAddr          string
		enableLeaderElection bool
		probeAddr            string
		secureMetrics        bool
		enableHTTP2          bool
		enableWebhooks       bool
		manifestsBasePath    string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS with the default certificate or :8080 for HTTP.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers.")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", true,
		"Enable webhook server for connection injection and hardware profile mutation.")
	flag.StringVar(&manifestsBasePath, "manifests-base-path", "/opt/manifests",
		"Base path for component manifests.")

	opts := zap.Options{
		Development: false,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	info, err := os.Stat(manifestsBasePath)
	if err != nil {
		setupLog.Error(err, "invalid manifests-base-path", "path", manifestsBasePath)
		os.Exit(1)
	}

	if !info.IsDir() {
		setupLog.Error(errors.New("path exists but is not a directory"), "invalid manifests-base-path", "path", manifestsBasePath)
		os.Exit(1)
	}

	// Bootstrap TLS configuration from the cluster's APIServer TLS profile (OpenShift only).
	restCfg := ctrl.GetConfigOrDie()
	bootstrapClient, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "unable to create bootstrap client for TLS profile")
		os.Exit(1)
	}

	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 10*time.Second)
	tlsResult, err := tlsconfig.Bootstrap(bootstrapCtx, bootstrapClient, enableHTTP2, tlsconfig.DefaultFetcher())
	bootstrapCancel()
	if err != nil {
		setupLog.Error(err, "unable to read APIServer TLS profile, refusing to start with unknown TLS posture")
		os.Exit(1)
	}

	if !tlsResult.HasOpenShiftConfigAPI {
		setupLog.Info("TLS profile not available, using hardened defaults (non-OpenShift cluster)")
	} else if len(tlsResult.UnsupportedCiphers) > 0 {
		setupLog.Info("some ciphers from TLS profile are not supported by Go", "unsupported", tlsResult.UnsupportedCiphers)
	}

	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsResult.TLSOpts,
	}

	if secureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	webhookServer := ctrlwebhook.NewServer(ctrlwebhook.Options{
		Port:    9443,
		TLSOpts: tlsResult.TLSOpts,
	})

	providedApplicationsNamespace := strings.TrimSpace(os.Getenv("APPLICATIONS_NAMESPACE"))
	applicationsNamespace := providedApplicationsNamespace
	if applicationsNamespace == "" || len(validation.IsDNS1123Label(applicationsNamespace)) > 0 {
		applicationsNamespace = platform.DefaultNotebooksNamespaceODH
		if providedApplicationsNamespace == "" {
			setupLog.Info("APPLICATIONS_NAMESPACE not set; using default",
				"default", applicationsNamespace)
		} else {
			setupLog.Info("APPLICATIONS_NAMESPACE invalid; using default",
				"provided", providedApplicationsNamespace,
				"default", applicationsNamespace)
		}
	}

	mgrOptions := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "workbenches-operator.platform.opendatahub.io",
	}
	// Partially addresses https://github.com/opendatahub-io/workbenches-operator/issues/43:
	// scope ConfigMap informer cache to APPLICATIONS_NAMESPACE. Deployment watches remain
	// cluster-scoped because workbench Deployments live in the workbench namespace, which
	// can differ from APPLICATIONS_NAMESPACE.
	mgrOptions.Cache = cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&corev1.ConfigMap{}: {
				Namespaces: map[string]cache.Config{
					applicationsNamespace: {},
				},
			},
		},
	}

	mgr, err := ctrl.NewManager(restCfg, mgrOptions)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.WorkbenchesReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		ManifestsBasePath:     manifestsBasePath,
		ApplicationsNamespace: applicationsNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Workbenches")
		os.Exit(1)
	}

	if enableWebhooks {
		if err := webhook.RegisterAllWebhooks(mgr); err != nil {
			setupLog.Error(err, "unable to register webhooks")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// Register SecurityProfileWatcher on OpenShift: cancel context on TLS profile change so pod restarts.
	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())

	if tlsResult.HasOpenShiftConfigAPI {
		watcher := &tlspkg.SecurityProfileWatcher{
			Client:                mgr.GetClient(),
			InitialTLSProfileSpec: tlsResult.Profile,
			OnProfileChange: func(_ context.Context, _, _ configv1.TLSProfileSpec) {
				setupLog.Info("TLS profile changed, initiating graceful shutdown to reload")
				cancel()
			},
		}
		if tlsResult.TLSAdherenceFetched {
			watcher.InitialTLSAdherencePolicy = tlsResult.TLSAdherence
			watcher.OnAdherencePolicyChange = func(_ context.Context, _, _ configv1.TLSAdherencePolicy) {
				setupLog.Info("TLS adherence policy changed, initiating shutdown to reload")
				cancel()
			}
		}
		if err := watcher.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to register TLS security profile watcher")
			cancel()
			os.Exit(1)
		}
	}

	setupLog.Info("starting manager")

	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		cancel()
		os.Exit(1)
	}
	cancel()
}
