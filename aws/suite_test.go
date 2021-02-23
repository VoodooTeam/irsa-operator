package aws_test

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"

	irsaws "github.com/VoodooTeam/irsa-operator/aws"
	"github.com/VoodooTeam/irsa-operator/controllers"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/go-logr/stdr"
	dockertest "github.com/ory/dockertest/v3"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var resource *dockertest.Resource
var pool *dockertest.Pool
var awsmngr controllers.AwsManager
var clusterName string

func TestTypes(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Aws Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func() {
	localStackEndpoint := os.Getenv("LOCALSTACK_ENDPOINT")
	if localStackEndpoint == "" {
		localStackEndpoint = setupLocalStack()
		Expect(pool).NotTo(BeNil())
		Expect(resource).NotTo(BeNil())
	} else if _, err := http.Get(localStackEndpoint); err != nil {
		log.Fatal("can't reach localstack on ", localStackEndpoint)
	}

	clusterName = "clustername"
	awsmngr = irsaws.NewAwsManager(
		session.Must(session.NewSession(&aws.Config{
			Credentials: credentials.NewStaticCredentials("test", "test", ""),
			DisableSSL:  aws.Bool(true),
			Region:      aws.String(endpoints.UsWest1RegionID),
			Endpoint:    &localStackEndpoint,
		})),
		stdr.New(log.New(os.Stderr, "", log.LstdFlags)),
		clusterName,
		"oidcprovider.url",
	)
	Expect(awsmngr).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	if os.Getenv("LOCALSTACK_ENDPOINT") == "" {
		err := pool.Purge(resource)
		Expect(err).NotTo(HaveOccurred())
	}
})

func setupLocalStack() string {
	var err error
	pool, err = dockertest.NewPool("")
	if err != nil {
		log.Fatalf("Could not connect to docker: %s", err)
	}

	resource, err = pool.Run("localstack/localstack", "0.12.4", []string{"SERVICES=iam,s3,sts"})
	if err != nil {
		log.Fatalf("Could not start resource: %s", err)
	}

	localStackEndpoint := fmt.Sprintf("http://localhost:%s", resource.GetPort("4566/tcp"))
	if err = pool.Retry(func() error {
		_, err := http.Get(localStackEndpoint)
		return err
	}); err != nil {
		log.Fatalf("Could not connect to localstack container: %s", err)
	}

	return localStackEndpoint
}
