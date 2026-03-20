package qe_test

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	testSAName    = "test-svc-token-auth"
	dashboardPath = "/"
)

func newInsecureHTTPClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar: jar,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				// #nosec G402 -- QE test environment requires skipping TLS verification for self-signed certificates
				InsecureSkipVerify: true,
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func callGatewayWithToken(token, path string) (*http.Response, error) {
	client := newInsecureHTTPClient()
	reqURL := fmt.Sprintf("https://%s%s", gatewayHostname, path)

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)

	return client.Do(req)
}

func callGatewayWithoutToken(path string) (*http.Response, error) {
	client := newInsecureHTTPClient()
	reqURL := fmt.Sprintf("https://%s%s", gatewayHostname, path)

	return client.Get(reqURL)
}


func getOpaqueToken() string {
	out, err := exec.Command("oc", "whoami", "--show-token").Output() //nolint:gosec
	Expect(err).NotTo(HaveOccurred(), "failed to get opaque token via 'oc whoami --show-token'")

	token := strings.TrimSpace(string(out))
	Expect(token).NotTo(BeEmpty(), "opaque token should not be empty")

	return token
}

func getCurrentUserName() string {
	out, err := exec.Command("oc", "whoami").Output()
	Expect(err).NotTo(HaveOccurred(), "failed to get current user via 'oc whoami'")

	return strings.TrimSpace(string(out))
}

func getOIDCToken() string {
	Expect(oidcIssuer).NotTo(BeEmpty(), "OIDC_ISSUER must be set")
	Expect(testUser).NotTo(BeEmpty(), "TEST_USER must be set")
	Expect(testPassword).NotTo(BeEmpty(), "TEST_PASSWORD must be set")

	tokenURL := fmt.Sprintf("%s/protocol/openid-connect/token", strings.TrimRight(oidcIssuer, "/"))

	data := url.Values{
		"grant_type": {"password"},
		"client_id":  {"admin-cli"},
		"username":   {testUser},
		"password":   {testPassword},
	}

	httpClient := newInsecureHTTPClient()
	resp, err := httpClient.PostForm(tokenURL, data)
	Expect(err).NotTo(HaveOccurred(), "failed to request OIDC token")
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred(), "failed to read OIDC token response body")
	Expect(resp.StatusCode).To(Equal(http.StatusOK),
		"OIDC token request failed with status %d: %s", resp.StatusCode, string(body))

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	Expect(json.Unmarshal(body, &tokenResp)).To(Succeed(), "failed to parse OIDC token response")
	Expect(tokenResp.AccessToken).NotTo(BeEmpty(), "OIDC access token should not be empty")

	return tokenResp.AccessToken
}

func isOIDCCluster() bool {
	return clusterAuth == "oidc"
}

func skipIfOIDC(reason string) {
	if isOIDCCluster() {
		Skip(reason)
	}
}

func skipIfNotOIDC(reason string) {
	if !isOIDCCluster() {
		Skip(reason)
	}
}

func getAPIStatusUserName(token string) string {
	resp, err := callGatewayWithToken(token, "/api/status")
	Expect(err).NotTo(HaveOccurred(), "failed to call /api/status")
	defer resp.Body.Close()

	Expect(resp.StatusCode).To(Equal(http.StatusOK), "/api/status should return 200")

	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred(), "failed to read /api/status response body")

	var status struct {
		Kube struct {
			UserName string `json:"userName"`
		} `json:"kube"`
	}
	Expect(json.Unmarshal(body, &status)).To(Succeed(), "failed to parse /api/status response")

	return status.Kube.UserName
}

// ensureTestSAExists creates the test SA if it doesn't already exist.
func ensureTestSAExists() {
	_, err := k8sClient.CoreV1().ServiceAccounts(appsNamespace).Get(
		testCtx.Context(), testSAName, metav1.GetOptions{},
	)
	if err != nil {
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testSAName,
				Namespace: appsNamespace,
			},
		}
		_, err = k8sClient.CoreV1().ServiceAccounts(appsNamespace).Create(
			testCtx.Context(), sa, metav1.CreateOptions{},
		)
		Expect(err).NotTo(HaveOccurred(), "failed to create test ServiceAccount")
	}
}
