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

package integration_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hwpWebhook "github.com/opendatahub-io/workbenches-operator/internal/webhook/hardwareprofile"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	scheme    *runtime.Scheme
	ctx       context.Context
	cancel    context.CancelFunc
)

const testNamespace = "test-hwp-integration"

func TestHardwareProfileWebhookIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HardwareProfile Webhook Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.Background()) //nolint:fatcontext // standard kubebuilder test pattern

	By("bootstrapping test environment with webhook server")

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			"testdata",
			filepath.Join("..", "..", "..", "..", "opt", "manifests", "workbenches", "odh-notebook-controller", "crd", "external"),
		},
		ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("testdata", "webhook-config.yaml")},
		},
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme = runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())

	webhookOpts := testEnv.WebhookInstallOptions

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    webhookOpts.LocalServingHost,
			Port:    webhookOpts.LocalServingPort,
			CertDir: webhookOpts.LocalServingCertDir,
		}),
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	Expect(err).NotTo(HaveOccurred())

	err = (&hwpWebhook.Injector{
		Client:      mgr.GetAPIReader(),
		EventWriter: mgr.GetClient(),
		Decoder:     admission.NewDecoder(mgr.GetScheme()),
		Name:        "hwp-integration-test",
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		startErr := mgr.Start(ctx)
		Expect(startErr).NotTo(HaveOccurred())
	}()

	// Wait for the webhook server to be ready (with proper CA verification)
	certPool := x509.NewCertPool()
	Expect(certPool.AppendCertsFromPEM(webhookOpts.LocalServingCAData)).To(BeTrue())

	dialer := &net.Dialer{Timeout: time.Second}
	addrPort := net.JoinHostPort(webhookOpts.LocalServingHost, strconv.Itoa(webhookOpts.LocalServingPort))

	Eventually(func() error {
		conn, connErr := tls.DialWithDialer(dialer, "tcp", addrPort, &tls.Config{
			RootCAs:    certPool,
			ServerName: webhookOpts.LocalServingHost,
			MinVersion: tls.VersionTLS12,
		})
		if connErr != nil {
			return connErr
		}

		return conn.Close()
	}, "30s", "200ms").Should(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Create test namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
	}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
})

var _ = AfterSuite(func() {
	cancel()

	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
