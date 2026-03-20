package qe_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"
)

const (
	testCABundleNS      = "test-trustedcabundle"
	trustedCABundleCM   = "odh-trusted-ca-bundle"
	customCABundleValue = "test-example-custom-ca-bundle"
)

// restoreDSCITrustedCABundle restores the DSCI trusted CA bundle settings.
func restoreDSCITrustedCABundle(savedCustomCA string) {
	setDSCITrustedCABundleState("Managed")
	setDSCICustomCABundle(savedCustomCA)
}

var _ = Describe("Trusted CA Bundles", Label("TrustedCABundle", "ODS-2638"), Ordered, func() {
	var savedCustomCABundle string

	BeforeAll(func() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: testCABundleNS},
		}
		_, err := k8sClient.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
		if err != nil {
			GinkgoWriter.Printf("Namespace %s may already exist: %v\n", testCABundleNS, err)
		}

		savedCustomCABundle = getDSCICustomCABundle()
		GinkgoWriter.Printf("Saved customCABundle: %s\n", savedCustomCABundle)
	})

	AfterAll(func() {
		err := k8sClient.CoreV1().Namespaces().Delete(context.Background(), testCABundleNS, metav1.DeleteOptions{})
		if err != nil {
			GinkgoWriter.Printf("Warning: failed to delete namespace %s: %v\n", testCABundleNS, err)
		}
	})

	Context("Managed state", Label("Smoke", "TrustedCABundle-Managed"), func() {
		AfterEach(func() {
			restoreDSCITrustedCABundle(savedCustomCABundle)
		})

		It("should create odh-trusted-ca-bundle ConfigMap in test namespace", func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			g.Get(gvk.ConfigMap, k8stypes.NamespacedName{
				Name:      trustedCABundleCM,
				Namespace: testCABundleNS,
			}).Eventually(10 * time.Minute).Should(Not(BeNil()),
				"ConfigMap %s should exist in %s", trustedCABundleCM, testCABundleNS,
			)
		})

		It("should contain ca-bundle.crt key", func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			g.Get(gvk.ConfigMap, k8stypes.NamespacedName{
				Name:      trustedCABundleCM,
				Namespace: testCABundleNS,
			}).Eventually(10 * time.Minute).Should(
				jq.Match(`.data | has("ca-bundle.crt")`),
				"ConfigMap should contain ca-bundle.crt key",
			)
		})

		It("should propagate custom CA bundle from DSCI to ConfigMap", func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			setDSCICustomCABundle(customCABundleValue)

			g.Get(gvk.ConfigMap, k8stypes.NamespacedName{
				Name:      trustedCABundleCM,
				Namespace: testCABundleNS,
			}).Eventually(5 * time.Minute).Should(
				jq.Match(`.data["odh-ca-bundle.crt"] | contains("%s")`, customCABundleValue),
				"ConfigMap should contain custom CA bundle value",
			)
		})
	})

	Context("Unmanaged state", Label("Smoke", "TrustedCABundle-Unmanaged"), func() {
		AfterEach(func() {
			restoreDSCITrustedCABundle(savedCustomCABundle)
		})

		It("should not overwrite manually modified ConfigMap", func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			setDSCITrustedCABundleState("Unmanaged")

			patchConfigMapData(trustedCABundleCM, testCABundleNS, "odh-ca-bundle.crt", "random-ca-bundle-value")

			time.Sleep(5 * time.Second)

			g.Get(gvk.ConfigMap, k8stypes.NamespacedName{
				Name:      trustedCABundleCM,
				Namespace: testCABundleNS,
			}).Eventually(5 * time.Minute).Should(
				jq.Match(`.data["odh-ca-bundle.crt"] | contains("random-ca-bundle-value")`),
				"ConfigMap should retain manually set value when Unmanaged",
			)
		})
	})

	Context("Removed state", Label("Tier1", "TrustedCABundle-Removed"), func() {
		AfterEach(func() {
			restoreDSCITrustedCABundle(savedCustomCABundle)
		})

		It("should remove odh-trusted-ca-bundle ConfigMap", func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			setDSCITrustedCABundleState("Removed")

			g.Get(gvk.ConfigMap, k8stypes.NamespacedName{
				Name:      trustedCABundleCM,
				Namespace: testCABundleNS,
			}).Eventually(10 * time.Minute).Should(BeNil(),
				"ConfigMap %s should be removed from %s", trustedCABundleCM, testCABundleNS,
			)
		})
	})

	Context("Namespace exclusion", Label("Tier1", "TrustedCABundle-Exclude-Namespace"), func() {
		AfterEach(func() {
			annotateNamespace(testCABundleNS, "security.opendatahub.io/inject-trusted-ca-bundle", "True")
			restoreDSCITrustedCABundle(savedCustomCABundle)
		})

		It("should remove ConfigMap when namespace is excluded and recreate when re-included", func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			setDSCITrustedCABundleState("Managed")

			g.Get(gvk.ConfigMap, k8stypes.NamespacedName{
				Name:      trustedCABundleCM,
				Namespace: testCABundleNS,
			}).Eventually(10 * time.Minute).Should(Not(BeNil()),
				"ConfigMap should exist before exclusion",
			)

			annotateNamespace(testCABundleNS, "security.opendatahub.io/inject-trusted-ca-bundle", "False")

			g.Get(gvk.ConfigMap, k8stypes.NamespacedName{
				Name:      trustedCABundleCM,
				Namespace: testCABundleNS,
			}).Eventually(10 * time.Minute).Should(BeNil(),
				"ConfigMap should be removed after namespace exclusion",
			)

			annotateNamespace(testCABundleNS, "security.opendatahub.io/inject-trusted-ca-bundle", "True")

			g.Get(gvk.ConfigMap, k8stypes.NamespacedName{
				Name:      trustedCABundleCM,
				Namespace: testCABundleNS,
			}).Eventually(10 * time.Minute).Should(Not(BeNil()),
				"ConfigMap should be recreated after namespace re-inclusion",
			)
		})
	})
})
