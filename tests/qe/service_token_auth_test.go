package qe_test

import (
	"context"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/opendatahub-operator/v2/internal/controller/services/gateway"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"
)

func createServiceAccountToken(saName, namespace string, expirationSeconds int64) string {
	tokenRequest := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: &expirationSeconds,
		},
	}

	result, err := k8sClient.CoreV1().ServiceAccounts(namespace).CreateToken(
		context.Background(), saName, tokenRequest, metav1.CreateOptions{},
	)
	Expect(err).NotTo(HaveOccurred(), "failed to create token for SA %s/%s", namespace, saName)

	return result.Status.Token
}

var _ = Describe("Service Token Auth", Label("ServiceTokenAuth", "RHOAIENG-47496"), func() {
	AfterEach(func(ctx SpecContext) {
		if k8sClient != nil {
			// Clean up the test service account if it was created
			_ = k8sClient.CoreV1().ServiceAccounts(appsNamespace).Delete(
				context.Background(), testSAName, metav1.DeleteOptions{},
			)
		}

	})
	Context("Gateway Configuration", Label("Tier1"), func() {
		It("should have token auth enabled on kube-auth-proxy", func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			g.Get(gvk.Deployment, types.NamespacedName{
				Name:      gateway.KubeAuthProxyName,
				Namespace: gateway.GatewayNamespace,
			}).Eventually().Should(
				jq.Match(`.spec.template.spec.containers[0].args | any(. == "--enable-k8s-token-validation=true")`),
			)
		})

		It("should use dedicated ServiceAccount", func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			g.Get(gvk.Deployment, types.NamespacedName{
				Name:      gateway.KubeAuthProxyName,
				Namespace: gateway.GatewayNamespace,
			}).Eventually().Should(
				jq.Match(`.spec.template.spec.serviceAccountName == "%s"`, gateway.KubeAuthProxyName),
			)
		})

		It("should have ClusterRoleBinding for TokenReview", func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			crbName := gateway.KubeAuthProxyName + "-tokenreview"

			g.Get(gvk.ClusterRoleBinding, types.NamespacedName{Name: crbName}).Eventually().Should(And(
				jq.Match(`.subjects[0].name == "%s"`, gateway.KubeAuthProxyName),
				jq.Match(`.subjects[0].namespace == "%s"`, gateway.GatewayNamespace),
				jq.Match(`.roleRef.name == "system:auth-delegator"`),
			))
		})
	})

	Context("Authenticated Access", Label("Tier1"), func() {
		It("should authenticate SA via token", func() {
			ensureTestSAExists()

			token := createServiceAccountToken(testSAName, appsNamespace, 600)

			resp, err := callGatewayWithToken(token, dashboardPath)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("should authenticate via opaque token", func() {
			skipIfOIDC("Not supported with BYOIDC")

			token := getOpaqueToken()

			resp, err := callGatewayWithToken(token, dashboardPath)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("should authenticate via OIDC JWT", func() {
			skipIfNotOIDC("Only applicable with BYOIDC")

			token := getOIDCToken()

			resp, err := callGatewayWithToken(token, dashboardPath)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})
	})

	Context("API Status Identity", Label("Tier1"), func() {
		It("should return SA identity in /api/status", func() {
			ensureTestSAExists()

			token := createServiceAccountToken(testSAName, appsNamespace, 600)
			expectedUser := fmt.Sprintf("system:serviceaccount:%s:%s", appsNamespace, testSAName)

			userName := getAPIStatusUserName(token)
			Expect(userName).To(Equal(expectedUser))
		})

		It("should return user identity with opaque token", func() {
			skipIfOIDC("Not supported with BYOIDC")

			token := getOpaqueToken()
			expectedUser := getCurrentUserName()

			userName := getAPIStatusUserName(token)
			Expect(userName).To(Equal(expectedUser))
		})

		It("should return user identity with OIDC JWT", func() {
			skipIfNotOIDC("Only applicable with BYOIDC")

			token := getOIDCToken()

			userName := getAPIStatusUserName(token)
			Expect(userName).To(Equal(testUser))
		})
	})

	Context("Negative Cases", Label("Tier2"), func() {
		It("should reject invalid token", func() {
			invalidToken := "eyJhbGciOiJSUzI1NiIsImtpZCI6ImlOdmFsaWQifQ.invalid.token"

			resp, err := callGatewayWithToken(invalidToken, dashboardPath)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(SatisfyAny(
				Equal(http.StatusForbidden),
				Equal(http.StatusFound),
			))
		})

		It("should challenge request with no token", func() {
			resp, err := callGatewayWithoutToken(dashboardPath)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(SatisfyAny(
				Equal(http.StatusForbidden),
				Equal(http.StatusFound),
			))
		})
	})
})
