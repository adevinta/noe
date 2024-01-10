package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
)

func publicKey(priv interface{}) interface{} {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	default:
		return nil
	}
}

func pemBlockForKey(priv interface{}) *pem.Block {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}
	case *ecdsa.PrivateKey:
		b, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to marshal ECDSA private key: %v", err)
			os.Exit(2)
		}
		return &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}
	default:
		return nil
	}
}

func generateCert(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0755))
	priv, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	require.NoError(t, err)
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"noe-integration-test"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour * 24 * 180),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, publicKey(priv), priv)
	require.NoError(t, err)
	certFD, err := os.Create(filepath.Join(path, "tls.crt"))
	require.NoError(t, err)
	defer certFD.Close()
	require.NoError(t, pem.Encode(certFD, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}))

	keyFD, err := os.Create(filepath.Join(path, "tls.key"))
	require.NoError(t, err)
	defer keyFD.Close()
	require.NoError(t, pem.Encode(keyFD, pemBlockForKey(priv)))
}

func TestNoeMain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if os.Getenv("RUN_INTEGRATION_TESTS") != "true" {
		t.Skip("RUN_INTEGRATION_TESTS environment variable is not set, skipping integration test")
		return
	}

	certPath := "./integration-tests/"
	generateCert(t, certPath)

	env := envconf.New()
	ctx, err := envfuncs.CreateKindCluster("noe-integration-tests")(ctx, env)
	defer envfuncs.DestroyKindCluster("noe-integration-tests")(ctx, env)
	require.NoError(t, err)
	os.Setenv("KUBECONFIG", env.KubeconfigFile())

	go Main(ctx, "./integration-tests/", "amd64", "", "linux", ":8181", "", "")

	client := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	assert.Eventually(t, func() bool {
		_, err := client.Get("https://localhost:8443/api/v1/cluster")
		return err == nil
	}, 30*time.Second, 1*time.Second, "Webhook never became available")

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test",
		},
	}
	podData, err := json.Marshal(pod)
	require.NoError(t, err)
	ar := admissionv1.AdmissionReview{
		Request: &admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Kind: metav1.GroupVersionKind{
				Group:   "",
				Version: "v1",
				Kind:    "Pod",
			},
			Object: runtime.RawExtension{
				Raw: podData,
			},
		},
	}
	ar.SetGroupVersionKind(appsv1.SchemeGroupVersion.WithKind("AdmissionReview"))

	data, err := json.Marshal(ar)
	require.NoError(t, err)

	req, err := http.NewRequest("POST", "https://localhost:8443/mutate", bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
