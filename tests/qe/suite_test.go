package qe_test

import (
	"context"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes"
	ctrlcfg "sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/opendatahub-io/opendatahub-operator/v2/internal/controller/services/gateway"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/testf"
)

var (
	testCtx   *testf.TestContext
	k8sClient *kubernetes.Clientset

	gatewayHostname    string
	appsNamespace      string
	operatorNamespace  string
	notebooksNamespace string
	clusterAuth        string
	oidcIssuer        string
	testUser          string
	testPassword      string
)

func TestQE(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "QE Suite")
}

var _ = BeforeSuite(func() {
	var err error


	appsNamespace = getEnvOrDefault("APPS_NAMESPACE", "redhat-ods-applications")
	operatorNamespace = getEnvOrDefault("OPERATOR_NAMESPACE", "redhat-ods-operator")
	notebooksNamespace = getEnvOrDefault("NOTEBOOKS_NAMESPACE", "rhods-notebooks")
	clusterAuth = getEnvOrDefault("CLUSTER_AUTH", "")
	oidcIssuer = getEnvOrDefault("OIDC_ISSUER", "")
	testUser = getEnvOrDefault("TEST_USER", "")
	testPassword = getEnvOrDefault("TEST_PASSWORD", "")

	testCtx, err = testf.NewTestContext()
	Expect(err).NotTo(HaveOccurred(), "failed to create test context")

	cfg, err := ctrlcfg.GetConfig()
	Expect(err).NotTo(HaveOccurred(), "failed to get REST config")

	k8sClient, err = kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred(), "failed to create kubernetes clientset")

	clusterDomain, err := cluster.GetDomain(context.Background(), testCtx.Client())
	Expect(err).NotTo(HaveOccurred(), "failed to get cluster domain")

	gatewayHostname = gateway.DefaultGatewaySubdomain + "." + clusterDomain

	// initalize cluster info
	cluster.Init(testCtx.Context(), testCtx.Client())

	GinkgoWriter.Printf("Gateway hostname: %s\n", gatewayHostname)
	GinkgoWriter.Printf("Apps namespace: %s\n", appsNamespace)
	GinkgoWriter.Printf("Operator namespace: %s\n", operatorNamespace)
	GinkgoWriter.Printf("Notebooks namespace: %s\n", notebooksNamespace)

})


func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
