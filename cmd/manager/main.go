package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-storage-operator/pkg/apis"
	"github.com/openshift/cluster-storage-operator/pkg/controller"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"github.com/operator-framework/operator-sdk/pkg/leader"
	sdkVersion "github.com/operator-framework/operator-sdk/version"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
)

var log = logf.Log.WithName("cmd")

func printVersion() {
	log.Info(fmt.Sprintf("Go Version: %s", runtime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH))
	log.Info(fmt.Sprintf("Version of operator-sdk: %v", sdkVersion.Version))
}

// tryDeleteIncompatibleLock tries to identify a ConfigMap created by CSO 4.6 and delete it.
// On a downgrade from 4.6 to 4.5 scenario, the leader election ConfigMap is created by CSO 4.6
// (library-go) and it doesn't have any ownerReference set. However, CSO 4.5 uses the leader-for-life
// election model, and it expects that the ConfigMap either doesn't exist or exists but has an
// ownerReference set (in order to know whether the lock belongs to it or not). Without this code,
// CSO 4.5 will perpetually fail to acquire the lock.
// More information at https://bugzilla.redhat.com/show_bug.cgi?id=1877316
func tryDeleteIncompatibleLock(config *rest.Config, lockName string) error {
	ns, err := k8sutil.GetOperatorNamespace()
	if err != nil {
		return err
	}

	cl, err := client.New(config, client.Options{})
	if err != nil {
		return err
	}

	cm := &corev1.ConfigMap{}
	key := client.ObjectKey{Namespace: ns, Name: lockName}
	err = cl.Get(context.TODO(), key, cm)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	// If the ConfigMap has metadata.ownerReferences, then it was
	// likely created by CSO 4.5. In this case, we don't want to
	// delete the lock, otherwise we could end up with multiple
	// operators running at the same time.
	if len(cm.GetOwnerReferences()) > 0 {
		return nil
	}

	log.Info("Found ConfigMap lock without metadata.ownerReferences, deleting")
	err = cl.Delete(context.TODO(), cm)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func main() {
	flag.Parse()

	// The logger instantiated here can be changed to any logger
	// implementing the logr.Logger interface. This logger will
	// be propagated through the whole operator, generating
	// uniform and structured logs.
	logf.SetLogger(logf.ZapLogger(false))

	printVersion()

	namespace := "kube-system"

	// Get a config to talk to the apiserver
	cfg, err := config.GetConfig()
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Delete incompatible ConfigMap lock if it exists
	lockName := "cluster-storage-operator-lock"
	errWait := wait.PollImmediate(time.Second*10, time.Minute*5, func() (bool, error) {
		err := tryDeleteIncompatibleLock(cfg, lockName)
		if err != nil {
			log.Error(err, "")
			return false, err
		}
		return true, nil
	})
	if errWait != nil {
		log.Error(errWait, "trying to delete incompatible ConfigMap lock")
		os.Exit(1)
	}

	// Become the leader before proceeding
	err = leader.Become(context.TODO(), lockName)
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Create a new Cmd to provide shared dependencies and start components
	mgr, err := manager.New(cfg, manager.Options{Namespace: namespace})
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("Registering Components.")

	// Setup Scheme for all resources
	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	if err := configv1.AddToScheme(mgr.GetScheme()); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	// Setup all Controllers
	if err := controller.AddToManager(mgr); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("Starting the Cmd.")

	// Start the Cmd
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		log.Error(err, "Manager exited non-zero")
		os.Exit(1)
	}
}
