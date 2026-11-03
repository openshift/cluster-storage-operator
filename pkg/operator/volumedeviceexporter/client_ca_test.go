package volumedeviceexporter

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	operatorapi "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-storage-operator/pkg/csoclients"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clocktesting "k8s.io/utils/clock/testing"
)

const observedTLSConfig = `{"targetcsiconfig":{"servingInfo":{"minTLSVersion":"VersionTLS12","cipherSuites":["TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"]}}}`

func TestClientCASync(t *testing.T) {
	oldCA := testCAPEM(t, "old")
	newCA := testCAPEM(t, "new")
	c, clients := newClientCAController(t, operatorapi.Managed, oldCA, nil)
	syncer := newClientCASyncer(t, clients, c.recorder)

	syncClientCA(t, syncer, c.recorder)
	assertClientCA(t, clients, oldCA)
	cacheDestination(t, c, clients)

	setSourceClientCA(t, c, oldCA+newCA)
	syncClientCA(t, syncer, c.recorder)
	assertClientCA(t, clients, oldCA+newCA)
	cacheDestination(t, c, clients)

	setSourceClientCA(t, c, newCA)
	syncClientCA(t, syncer, c.recorder)
	assertClientCA(t, clients, newCA)
}

func TestClientCASyncExcludesUnrelatedKeys(t *testing.T) {
	ca := testCAPEM(t, "client")
	c, clients := newClientCAController(t, operatorapi.Managed, ca, nil)
	syncClientCA(t, newClientCASyncer(t, clients, c.recorder), c.recorder)
	assertClientCA(t, clients, ca)
}

func TestClientCASyncSkipsUnmanaged(t *testing.T) {
	ca := testCAPEM(t, "client")
	c, clients := newClientCAController(t, operatorapi.Unmanaged, ca, nil)
	syncClientCA(t, newClientCASyncer(t, clients, c.recorder), c.recorder)
	if _, err := clients.KubeClient.CoreV1().ConfigMaps(csoclients.OperatorNamespace).Get(context.Background(), clientCAConfigMapName, metav1.GetOptions{}); err == nil {
		t.Fatal("client CA ConfigMap was created while unmanaged")
	}
}

func TestClientCAInvalidSourcePreventsDaemonSet(t *testing.T) {
	c, clients := newClientCAController(t, operatorapi.Managed, "not a certificate", nil)
	syncer := newClientCASyncer(t, clients, c.recorder)
	syncClientCA(t, syncer, c.recorder)
	cacheDestination(t, c, clients)

	err := c.syncDaemonSet(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "no client CA certificates") {
		t.Fatalf("error = %v, want invalid client CA error", err)
	}
	if _, err := clients.KubeClient.AppsV1().DaemonSets(csoclients.OperatorNamespace).Get(context.Background(), "volume-device-exporter", metav1.GetOptions{}); err == nil {
		t.Fatal("DaemonSet was created with invalid client trust")
	}

	validCA := testCAPEM(t, "recovered")
	setSourceClientCA(t, c, validCA)
	syncClientCA(t, syncer, c.recorder)
	cacheDestination(t, c, clients)
	if err := c.syncDaemonSet(context.Background(), nil); err != nil {
		t.Fatalf("DaemonSet reconciliation did not recover after valid CA sync: %v", err)
	}
	if _, err := clients.KubeClient.AppsV1().DaemonSets(csoclients.OperatorNamespace).Get(context.Background(), "volume-device-exporter", metav1.GetOptions{}); err != nil {
		t.Fatalf("DaemonSet was not created after valid CA sync: %v", err)
	}
}

func TestDaemonSetWaitsForValidSynchronizedClientCA(t *testing.T) {
	validCA := testCAPEM(t, "valid")
	for _, tc := range []struct {
		name string
		ca   string
		want string
	}{
		{name: "missing", want: "reading synchronized metrics client CA"},
		{name: "invalid", ca: "invalid", want: "no client CA certificates"},
		{name: "valid", ca: validCA},
	} {
		t.Run(tc.name, func(t *testing.T) {
			destination := (*corev1.ConfigMap)(nil)
			if tc.ca != "" {
				destination = destinationConfigMap(tc.ca)
			}
			c, _ := newClientCAController(t, operatorapi.Managed, "", destination)
			if destination != nil {
				seedDestinationConfigMap(t, c, destination)
			}
			err := c.syncDaemonSet(context.Background(), nil)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error = %v, want %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func newClientCASyncer(t *testing.T, clients *csoclients.Clients, recorder events.Recorder) *resourcesynccontroller.ResourceSyncController {
	t.Helper()
	return newClientCASyncController(clients, recorder)
}

func syncClientCA(t *testing.T, syncer *resourcesynccontroller.ResourceSyncController, recorder events.Recorder) {
	t.Helper()
	if err := syncer.Sync(context.Background(), factory.NewSyncContext("test", recorder)); err != nil {
		t.Fatal(err)
	}
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
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "extension-apiserver-authentication", Namespace: metav1.NamespaceSystem},
		Data: map[string]string{
			clientCAConfigMapKey:           clientCA,
			"requestheader-client-ca-file": "must not be copied",
		},
	}
}

func destinationConfigMap(clientCA string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: clientCAConfigMapName, Namespace: csoclients.OperatorNamespace},
		Data:       map[string]string{clientCAConfigMapKey: clientCA},
	}
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

func seedDestinationConfigMap(t *testing.T, c *controller, destination *corev1.ConfigMap) {
	t.Helper()
	store := c.clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Core().V1().ConfigMaps().Informer().GetStore()
	if err := store.Add(destination); err != nil {
		t.Fatal(err)
	}
}

func cacheDestination(t *testing.T, c *controller, clients *csoclients.Clients) {
	t.Helper()
	destination, err := clients.KubeClient.CoreV1().ConfigMaps(csoclients.OperatorNamespace).Get(context.Background(), clientCAConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store := c.clients.KubeInformers.InformersFor(csoclients.OperatorNamespace).Core().V1().ConfigMaps().Informer().GetStore()
	if err := store.Add(destination); err != nil {
		if err := store.Update(destination); err != nil {
			t.Fatal(err)
		}
	}
}

func assertClientCA(t *testing.T, clients *csoclients.Clients, want string) {
	t.Helper()
	actual, err := clients.KubeClient.CoreV1().ConfigMaps(csoclients.OperatorNamespace).Get(context.Background(), clientCAConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Data) != 1 || actual.Data[clientCAConfigMapKey] != want {
		t.Errorf("client CA data = %#v, want only %s with expected bundle", actual.Data, clientCAConfigMapKey)
	}
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
