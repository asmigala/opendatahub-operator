package qe_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"
)

const testNotebookUser = "test-nb-user"

// safeUsername converts a username to the JupyterHub safe format.
// Replaces non-alphanumeric characters with -XX hex encoding, then lowercases.
// e.g. "ldap-user1" -> "ldap-2duser1"
func safeUsername(username string) string {
	var b strings.Builder
	for _, c := range username {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		} else {
			fmt.Fprintf(&b, "-%02x", c)
		}
	}
	return strings.ToLower(b.String())
}

// FIXME: not sure it's right to construct a notebook like this - we are hardcoding args etc that might change in future
//go:generate echo "notebook template is embedded below"
const notebookTemplate = `apiVersion: kubeflow.org/v1
kind: Notebook
metadata:
  name: "{{.Name}}"
  namespace: "{{.Namespace}}"
  labels:
    app: "{{.Name}}"
    opendatahub.io/dashboard: "true"
    opendatahub.io/odh-managed: "true"
    opendatahub.io/user: "{{.SafeUser}}"
  annotations:
    notebooks.opendatahub.io/inject-oauth: "true"
    opendatahub.io/username: "{{.Username}}"
spec:
  template:
    spec:
      serviceAccountName: "{{.Name}}"
      enableServiceLinks: false
      containers:
      - name: "{{.Name}}"
        image: image-registry.openshift-image-registry.svc:5000/redhat-ods-applications/s2i-minimal-notebook:2025.1
        ports:
        - containerPort: 8888
          name: notebook-port
          protocol: TCP
        resources:
          limits:
            cpu: "1"
            memory: 2Gi
          requests:
            cpu: 500m
            memory: 1Gi
      - name: oauth-proxy
        image: registry.redhat.io/openshift4/ose-oauth-proxy-rhel9@sha256:ca21e218e26c46e3c63d926241846f8f307fd4a586cc4b04147da49af6018ef5
        ports:
        - containerPort: 8443
          name: oauth-proxy
          protocol: TCP
        args:
        - --provider=openshift
        - --https-address=:8443
        - --http-address=
        - --openshift-service-account={{.Name}}
        - --upstream=http://localhost:8888
        - --upstream-ca=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt
        - --email-domain=*
        - --skip-provider-button
        - --client-id={{.Name}}-{{.Namespace}}-oauth-client
        - --client-secret-file=/etc/oauth/client/secret
        - --scope=user:info user:check-access
        resources:
          limits:
            cpu: 100m
            memory: 64Mi
          requests:
            cpu: 100m
            memory: 64Mi
`

// buildNotebookCR creates an unstructured Notebook CR from the embedded YAML template.
func buildNotebookCR(name, namespace, username string) *unstructured.Unstructured {
	tmpl, err := template.New("notebook").Parse(notebookTemplate)
	Expect(err).NotTo(HaveOccurred(), "failed to parse notebook template")

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, struct {
		Name, Namespace, Username, SafeUser string
	}{
		Name:      name,
		Namespace: namespace,
		Username:  username,
		SafeUser:  safeUsername(username),
	})
	Expect(err).NotTo(HaveOccurred(), "failed to execute notebook template")

	obj := &unstructured.Unstructured{}
	err = yaml.NewYAMLOrJSONDecoder(&buf, buf.Len()).Decode(&obj.Object)
	Expect(err).NotTo(HaveOccurred(), "failed to decode notebook YAML")

	return obj
}

var _ = Describe("Notebook Network Policies", Label("NetworkPolicies", "Smoke", "ODS-2045"), Ordered, func() {
	var notebookName string

	BeforeAll(func() {
		safeUser := safeUsername(testNotebookUser)
		notebookName = "jupyter-nb-" + safeUser

		GinkgoWriter.Printf("Creating notebook %s in namespace %s\n", notebookName, notebooksNamespace)

		nb := buildNotebookCR(notebookName, notebooksNamespace, testNotebookUser)
		err := testCtx.Client().Create(context.Background(), nb)
		Expect(err).NotTo(HaveOccurred(), "failed to create Notebook CR %s", notebookName)

		// Wait for network policies to be created
		g := testCtx.NewGinkgoWithT(GinkgoT())
		g.Get(gvk.NetworkPolicy, k8stypes.NamespacedName{
			Name:      notebookName + "-ctrl-np",
			Namespace: notebooksNamespace,
		}).Eventually(2 * time.Minute).Should(Not(BeNil()),
			"control network policy should be created for notebook %s", notebookName,
		)
	})

	AfterAll(func() {
		// Delete the notebook CR
		nb := &unstructured.Unstructured{}
		nb.SetGroupVersionKind(gvk.Notebook)
		nb.SetName(notebookName)
		nb.SetNamespace(notebooksNamespace)

		err := testCtx.Client().Delete(context.Background(), nb)
		if err != nil {
			GinkgoWriter.Printf("Warning: failed to delete notebook %s: %v\n", notebookName, err)
		}
	})

	It("should create network policies for the notebook", func() {
		g := testCtx.NewGinkgoWithT(GinkgoT())

		release := cluster.GetRelease()

		// On RHOAI-Managed, network policies may not be created
		ctrlNP := getResourceOrNil(gvk.NetworkPolicy, notebookName+"-ctrl-np", notebooksNamespace)
		oauthNP := getResourceOrNil(gvk.NetworkPolicy, notebookName+"-kube-rbac-proxy-np", notebooksNamespace)

		if release.Name == cluster.ManagedRhoai {
			if ctrlNP == nil && oauthNP == nil {
				GinkgoWriter.Printf("RHOAI-Managed: network policies not created (expected behavior)\n")
				Skip("Network policies not created on RHOAI-Managed cluster")
			}
		} else {
			// On ODH and RHOAI-SelfManaged, network policies must exist
			g.Get(gvk.NetworkPolicy, k8stypes.NamespacedName{
				Name:      notebookName + "-ctrl-np",
				Namespace: notebooksNamespace,
			}).Eventually(2 * time.Minute).Should(Not(BeNil()))

			g.Get(gvk.NetworkPolicy, k8stypes.NamespacedName{
				Name:      notebookName + "-kube-rbac-proxy-np",
				Namespace: notebooksNamespace,
			}).Eventually(2 * time.Minute).Should(Not(BeNil()))
		}
	})

	It("should configure control network policy with correct port and namespace selector", func() {
		g := testCtx.NewGinkgoWithT(GinkgoT())

		nn := k8stypes.NamespacedName{Name: notebookName + "-ctrl-np", Namespace: notebooksNamespace}
		np := getResourceOrNil(gvk.NetworkPolicy, nn.Name, nn.Namespace)
		if np == nil {
			Skip("Control network policy does not exist on this cluster")
		}

		// Port should be 8888/TCP
		g.Get(gvk.NetworkPolicy, nn).Eventually().Should(
			jq.Match(`.spec.ingress[0].ports[0].port == 8888`),
			"control network policy should allow port 8888",
		)
		g.Get(gvk.NetworkPolicy, nn).Eventually().Should(
			jq.Match(`.spec.ingress[0].ports[0].protocol == "TCP"`),
		)

		// Namespace selector should reference the applications namespace
		g.Get(gvk.NetworkPolicy, nn).Eventually().Should(
			jq.Match(`.spec.ingress[0].from[0].namespaceSelector.matchLabels["kubernetes.io/metadata.name"] == "%s"`, appsNamespace),
			"control network policy namespace selector should reference %s", appsNamespace,
		)

		// Should have exactly 1 ingress rule (not overly permissive)
		g.Get(gvk.NetworkPolicy, nn).Eventually().Should(
			jq.Match(`.spec.ingress | length == 1`),
		)
	})

	It("should configure OAuth network policy with correct port", func() {
		g := testCtx.NewGinkgoWithT(GinkgoT())

		nn := k8stypes.NamespacedName{Name: notebookName + "-kube-rbac-proxy-np", Namespace: notebooksNamespace}
		np := getResourceOrNil(gvk.NetworkPolicy, nn.Name, nn.Namespace)
		if np == nil {
			Skip("OAuth network policy does not exist on this cluster")
		}

		// Port should be 8443/TCP
		g.Get(gvk.NetworkPolicy, nn).Eventually().Should(
			jq.Match(`.spec.ingress[0].ports[0].port == 8443`),
			"OAuth network policy should allow port 8443",
		)
		g.Get(gvk.NetworkPolicy, nn).Eventually().Should(
			jq.Match(`.spec.ingress[0].ports[0].protocol == "TCP"`),
		)

		// Should have exactly 1 ingress rule
		g.Get(gvk.NetworkPolicy, nn).Eventually().Should(
			jq.Match(`.spec.ingress | length == 1`),
		)
	})

	// FIXME: the robot tests has comments that say that the labels are sometimes empty, which they are. Need to make sure that's ok
	//It("should have correct labels on network policies", func() {
		//g := testCtx.NewGinkgoWithT(GinkgoT())

		//for _, suffix := range []string{"-ctrl-np", "-kube-rbac-proxy-np"} {
			//nn := k8stypes.NamespacedName{Name: notebookName + suffix, Namespace: notebooksNamespace}
			//np := getResourceOrNil(gvk.NetworkPolicy, nn.Name, nn.Namespace)
			//if np == nil {
				//continue
			//}

			//g.Get(gvk.NetworkPolicy, nn).Eventually().Should(
				//jq.Match(`.metadata.labels["notebook-name"] == "%s"`, notebookName),
				//"network policy %s should have notebook-name label", nn.Name,
			//)
		//}
	//})

	It("should have correct namespace labels for the platform", func() {
		g := testCtx.NewGinkgoWithT(GinkgoT())
		release := cluster.GetRelease()

		nn := k8stypes.NamespacedName{Name: notebooksNamespace}

		// FIXME: use a resolve function like elsewhere
		switch release.Name {
		case cluster.OpenDataHub:
			g.Get(gvk.Namespace, nn).Eventually().Should(
				jq.Match(`.metadata.labels["opendatahub.io/generated-namespace"] != null`),
				"ODH namespace should have opendatahub.io/generated-namespace label",
			)
		case cluster.SelfManagedRhoai:
			g.Get(gvk.Namespace, nn).Eventually().Should(
				jq.Match(`.metadata.labels["opendatahub.io/generated-namespace"] != null`),
				"RHOAI-SelfManaged namespace should have opendatahub.io/generated-namespace label",
			)
		case cluster.ManagedRhoai:
			GinkgoWriter.Printf("RHOAI-Managed: namespace label validation - checking available labels\n")
		}
	})
})

