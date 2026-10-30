package volumedeviceexporter

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakecore "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	clocktesting "k8s.io/utils/clock/testing"
)

const observedTLSConfig = `{"targetcsiconfig":{"servingInfo":{"minTLSVersion":"VersionTLS12","cipherSuites":["TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"]}}}`

func TestSyncClientCA(t *testing.T) {
	oldCA := testCAPEM(t, "old")
	newCA := testCAPEM(t, "new")

	t.Run("copies only client CA and rotates bundles", func(t *testing.T) {
		c, clients := newClientCAController(t, operatorapi.Managed, oldCA, nil)
		if err := c.syncClientCA(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertClientCA(t, clients, oldCA)

		setSourceClientCA(t, c, oldCA+newCA)
		if err := c.syncClientCA(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertClientCA(t, clients, oldCA+newCA)

		setSourceClientCA(t, c, newCA)
		if err := c.syncClientCA(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertClientCA(t, clients, newCA)
	})

	t.Run("is idempotent", func(t *testing.T) {
		destination := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: clientCAConfigMapName, Namespace: csoclients.OperatorNamespace},
			Data:       map[string]string{"client-ca.crt": oldCA},
		}
		c, clients := newClientCAController(t, operatorapi.Managed, oldCA, destination)
		if err := c.syncClientCA(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, action := range fakeKubeClient(t, clients).Actions() {
			if action.GetVerb() == "update" || action.GetVerb() == "create" {
				t.Errorf("unexpected %s action for unchanged client CA", action.GetVerb())
			}
		}
	})

	for _, tc := range []struct {
		name      string
		source    *corev1.ConfigMap
		wantError string
	}{
		{name: "missing source", wantError: "reading metrics client CA"},
		{name: "missing key", source: sourceConfigMap(""), wantError: "no client CA certificates"},
		{name: "empty key", source: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "extension-apiserver-authentication", Namespace: metav1.NamespaceSystem}, Data: map[string]string{"client-ca-file": ""}}, wantError: "no client CA certificates"},
		{name: "whitespace key", source: sourceConfigMap(" \n\t"), wantError: "no client CA certificates"},
		{name: "malformed PEM", source: sourceConfigMap("not a certificate"), wantError: "no client CA certificates"},
	} {
		t.Run("preserves last good bundle on "+tc.name, func(t *testing.T) {
			destination := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: clientCAConfigMapName, Namespace: csoclients.OperatorNamespace},
				Data:       map[string]string{"client-ca.crt": oldCA},
			}
			c, clients := newClientCAController(t, operatorapi.Managed, "", destination)
			if tc.source != nil {
				seedSourceConfigMap(t, c, tc.source)
			}
			err := c.syncClientCA(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
			assertClientCA(t, clients, oldCA)
		})
	}

	t.Run("propagates destination create error", func(t *testing.T) {
		c, clients := newClientCAController(t, operatorapi.Managed, oldCA, nil)
		fakeKubeClient(t, clients).PrependReactor("create", "configmaps", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("create failed")
		})
		if err := c.syncClientCA(context.Background()); err == nil || !strings.Contains(err.Error(), "create failed") {
			t.Fatalf("error = %v, want create failure", err)
		}
	})

	t.Run("propagates destination update error", func(t *testing.T) {
		destination := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: clientCAConfigMapName, Namespace: csoclients.OperatorNamespace},
			Data:       map[string]string{"client-ca.crt": oldCA},
		}
		c, clients := newClientCAController(t, operatorapi.Managed, newCA, destination)
		fakeKubeClient(t, clients).PrependReactor("update", "configmaps", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("update failed")
		})
		if err := c.syncClientCA(context.Background()); err == nil || !strings.Contains(err.Error(), "update failed") {
			t.Fatalf("error = %v, want update failure", err)
		}
	})
}

func TestSyncDaemonSetClientCARequirements(t *testing.T) {
	validCA := testCAPEM(t, "valid")
	for _, tc := range []struct {
		name   string
		state  operatorapi.ManagementState
		source string
		wantDS bool
		wantCA bool
	}{
		{name: "managed missing source creates nothing", state: operatorapi.Managed},
		{name: "managed invalid source creates nothing", state: operatorapi.Managed, source: "invalid"},
		{name: "managed valid source creates both", state: operatorapi.Managed, source: validCA, wantDS: true, wantCA: true},
		{name: "unmanaged skips", state: operatorapi.Unmanaged, source: validCA},
		{name: "removed skips", state: operatorapi.Removed, source: validCA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, clients := newClientCAController(t, tc.state, tc.source, nil)
			err := c.syncDaemonSet(context.Background(), nil)
			if tc.state == operatorapi.Managed && !tc.wantDS && err == nil {
				t.Fatal("expected invalid source error")
			}
			if (tc.state != operatorapi.Managed || tc.wantDS) && err != nil {
				t.Fatal(err)
			}
			_, dsErr := clients.KubeClient.AppsV1().DaemonSets(csoclients.OperatorNamespace).Get(context.Background(), "volume-device-exporter", metav1.GetOptions{})
			if got := dsErr == nil; got != tc.wantDS {
				t.Errorf("DaemonSet exists = %t, want %t (error: %v)", got, tc.wantDS, dsErr)
			}
			_, caErr := clients.KubeClient.CoreV1().ConfigMaps(csoclients.OperatorNamespace).Get(context.Background(), clientCAConfigMapName, metav1.GetOptions{})
			if got := caErr == nil; got != tc.wantCA {
				t.Errorf("client CA ConfigMap exists = %t, want %t (error: %v)", got, tc.wantCA, caErr)
			}
		})
	}

	t.Run("recovers after valid source is observed", func(t *testing.T) {
		c, clients := newClientCAController(t, operatorapi.Managed, "invalid", nil)
		if err := c.syncDaemonSet(context.Background(), nil); err == nil {
			t.Fatal("expected invalid source error")
		}
		setSourceClientCA(t, c, validCA)
		if err := c.syncDaemonSet(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		if _, err := clients.KubeClient.AppsV1().DaemonSets(csoclients.OperatorNamespace).Get(context.Background(), "volume-device-exporter", metav1.GetOptions{}); err != nil {
			t.Fatal(err)
		}
		assertClientCA(t, clients, validCA)
	})
}

func newClientCAController(t *testing.T, state operatorapi.ManagementState, clientCA string, destination *corev1.ConfigMap) (*controller, *csoclients.Clients) {
	t.Helper()
	storage := csoclients.GetCR(func(storage *operatorapi.Storage) *operatorapi.Storage {
		storage.Spec.ManagementState = state
		storage.Spec.ObservedConfig.Raw = []byte(observedTLSConfig)
		return storage
	})
	objects := &csoclients.FakeTestObjects{OperatorObjects: []runtime.Object{storage}}
	if destination != nil {
		objects.CoreObjects = []runtime.Object{destination}
	}
	clients := csoclients.NewFakeClients(objects)
	c := &controller{
		operatorClient: clients.OperatorClient,
		clients:        clients,
		recorder:       events.NewInMemoryRecorder("test", clocktesting.NewFakePassiveClock(time.Now())),
	}
	if clientCA != "" {
		seedSourceConfigMap(t, c, sourceConfigMap(clientCA))
	}
	return c, clients
}

func sourceConfigMap(clientCA string) *corev1.ConfigMap {
	data := map[string]string{"requestheader-client-ca-file": "must not be copied"}
	if clientCA != "" {
		data["client-ca-file"] = clientCA
	}
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "extension-apiserver-authentication", Namespace: metav1.NamespaceSystem}, Data: data}
}

func seedSourceConfigMap(t *testing.T, c *controller, source *corev1.ConfigMap) {
	t.Helper()
	store := c.clients.KubeInformers.InformersFor(metav1.NamespaceSystem).Core().V1().ConfigMaps().Informer().GetStore()
	if err := store.Add(source); err != nil {
		t.Fatal(err)
	}
}

func setSourceClientCA(t *testing.T, c *controller, clientCA string) {
	t.Helper()
	store := c.clients.KubeInformers.InformersFor(metav1.NamespaceSystem).Core().V1().ConfigMaps().Informer().GetStore()
	if err := store.Update(sourceConfigMap(clientCA)); err != nil {
		t.Fatal(err)
	}
}

func assertClientCA(t *testing.T, clients *csoclients.Clients, want string) {
	t.Helper()
	actual, err := clients.KubeClient.CoreV1().ConfigMaps(csoclients.OperatorNamespace).Get(context.Background(), clientCAConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Data) != 1 || actual.Data["client-ca.crt"] != want {
		t.Errorf("client CA data = %#v, want only client-ca.crt with expected bundle", actual.Data)
	}
}

func fakeKubeClient(t *testing.T, clients *csoclients.Clients) *fakecore.Clientset {
	t.Helper()
	client, ok := clients.KubeClient.(*fakecore.Clientset)
	if !ok {
		t.Fatalf("KubeClient = %T, want *fake.Clientset", clients.KubeClient)
	}
	return client
}

func testCAPEM(t *testing.T, commonName string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
