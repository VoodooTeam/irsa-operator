package controllers_test

import (
	"log"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	irsav1alpha1 "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
	irsaCtrl "github.com/VoodooTeam/irsa-operator/controllers"
	// +kubebuilder:scaffold:imports
)

var k8sClient client.Client
var testEnv *envtest.Environment
var st *awsFake

func CustomFail(message string, callerSkip ...int) {
	log.Println(message)
	panic(GINKGO_PANIC)
}

func TestAPIs(t *testing.T) {
	RegisterFailHandler(CustomFail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

type fakeWriter struct{}

func (fakeWriter) Write(b []byte) (int, error) {
	return 0, nil
}

var _ = BeforeSuite(func() {
	// we disable the standard logging
	logf.SetLogger(zap.New(zap.WriteTo(fakeWriter{})))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	// start the envtest cluster
	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// import the irsa scheme
	err = irsav1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	// reconcilers
	// irsa reconcilier
	iR := irsaCtrl.NewIrsaReconciler(
		k8sManager.GetClient(),
		scheme.Scheme,
		ctrl.Log.WithName("controllers").WithName("irsa"),
	)
	err = iR.SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	// policy reconcilier
	clusterName := "clustername"
	st = newAwsFake()
	pR := irsaCtrl.NewPolicyReconciler(
		k8sManager.GetClient(),
		scheme.Scheme,
		st,
		ctrl.Log.WithName("controllers").WithName("policy"),
		clusterName,
	)

	err = pR.SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	// start role reconcilier
	rR := irsaCtrl.NewRoleReconciler(
		k8sManager.GetClient(),
		scheme.Scheme,
		st,
		ctrl.Log.WithName("controllers").WithName("role"),
		clusterName,
		"",
	)
	err = rR.SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

	k8sClient = k8sManager.GetClient()
	Expect(k8sClient).ToNot(BeNil())

}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
