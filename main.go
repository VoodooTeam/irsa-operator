package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	irsav1alpha1 "github.com/VoodooTeam/irsa-operator/api/v1alpha1"
	irsaws "github.com/VoodooTeam/irsa-operator/aws"
	"github.com/VoodooTeam/irsa-operator/controllers"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/aws/session"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(irsav1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var clusterName string
	var oidcProviderARN string
	var permissionsBoundariesPolicyARN string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	flag.StringVar(&clusterName, "cluster-name", "", "The cluster name, used to avoid name collisions on aws, set this to the name of the eks cluster")
	flag.StringVar(&oidcProviderARN, "oidc-provider-arn", "", "The ARN of the oidc provider to use.")
	flag.StringVar(&permissionsBoundariesPolicyARN, "permissions-boundaries-policy-arn", "", "The ARN of the policy used as permissions boundaries")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if clusterName == "" {
		setupLog.Error(errors.New("cluster-name not provided"), "unable to start manager")
		os.Exit(1)
	}
	if oidcProviderARN == "" {
		setupLog.Error(errors.New("oidc-provider-url not provided"), "unable to start manager")
		os.Exit(1)
	}
	setupLog.Info(fmt.Sprintf("cluster name is : %s", clusterName))
	setupLog.Info(fmt.Sprintf("oidc provider arn is : %s", oidcProviderARN))
	if permissionsBoundariesPolicyARN == "" {
		setupLog.Info("no permissions boundaries set, you're granting FullAdmin rights to your k8s admins")
	} else {
		setupLog.Info(fmt.Sprintf("permissions boundaries policy arn is : %s", permissionsBoundariesPolicyARN))
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "d8e70b98.voodoo.io",
		Namespace:              "", // empty string means "watch resources on all namespaces"
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = controllers.NewIrsaReconciler(
		mgr.GetClient(),
		mgr.GetScheme(),
		ctrl.Log.WithName("controllers").WithName("IamRoleServiceAccount"),
	).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "IamRoleServiceAccount")
		os.Exit(1)
	}

	awsCfg := getAwsConfig()

	if err = controllers.NewPolicyReconciler(
		mgr.GetClient(),
		mgr.GetScheme(),
		irsaws.NewAwsManager(
			awsCfg,
			ctrl.Log.WithName("aws").WithName("Policy"), clusterName,
			oidcProviderARN,
		),
		ctrl.Log.WithName("controllers").WithName("Policy"),
		clusterName,
	).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Policy")
		os.Exit(1)
	}

	if err = controllers.NewRoleReconciler(
		mgr.GetClient(),
		mgr.GetScheme(),
		irsaws.NewAwsManager(awsCfg, ctrl.Log.WithName("controllers").WithName("Aws"), clusterName, oidcProviderARN),
		ctrl.Log.WithName("controllers").WithName("Role"),
		clusterName,
		permissionsBoundariesPolicyARN,
	).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Role")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getAwsConfig() *session.Session {
	localstackEndpoint := os.Getenv("LOCALSTACK_ENDPOINT")

	if localstackEndpoint != "" {
		setupLog.Info(fmt.Sprintf("using localstack at : %s", localstackEndpoint))
		if _, err := http.Get(localstackEndpoint); err != nil { // we check connectivity
			panic(err)
		}

		return session.Must(session.NewSession(&aws.Config{
			Credentials: credentials.NewStaticCredentials("test", "test", ""),
			DisableSSL:  aws.Bool(true),
			Region:      aws.String(endpoints.UsWest1RegionID),
			Endpoint:    aws.String(localstackEndpoint),
		}))
	}

	return session.Must(session.NewSession())
}
