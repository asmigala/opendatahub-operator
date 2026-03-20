package qe_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sunstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/resources"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"
)

const (
	dscName  = "default-dsc"
	dsciName = "default-dsci"
)

var _ = Describe("Operator Installation", Label("Installation"), func() {
	Context("Release Attributes", Label("Smoke"), func() {
		It("should have matching release.name in DSC and DSCI", Label("RHOAIENG-9760"), func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			expectedName := getExpectedReleaseName()
			Expect(expectedName).NotTo(BeEmpty(), "expected release name should not be empty")

			g.Get(gvk.DataScienceCluster, types.NamespacedName{Name: dscName}).Eventually().Should(
				jq.Match(`.status.release.name == "%s"`, expectedName),
			)

			g.Get(gvk.DSCInitialization, types.NamespacedName{Name: dsciName}).Eventually().Should(
				jq.Match(`.status.release.name == "%s"`, expectedName),
			)
		})

		It("should have release.version matching the CSV version", Label("RHOAIENG-8082"), func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			csvName := getInstalledCSVName()

			csvObj := resources.GvkToUnstructured(gvk.ClusterServiceVersion)
			err := testCtx.Client().Get(context.Background(), types.NamespacedName{
				Name:      csvName,
				Namespace: operatorNamespace,
			}, csvObj)
			Expect(err).NotTo(HaveOccurred(), "failed to get CSV %s", csvName)

			csvVersion, found, err := k8sunstructured.NestedString(csvObj.Object, "spec", "version")
			Expect(err).NotTo(HaveOccurred(), "failed to extract .spec.version from CSV")
			Expect(found).To(BeTrue(), ".spec.version not found in CSV %s", csvName)

			g.Get(gvk.DataScienceCluster, types.NamespacedName{Name: dscName}).Eventually().Should(
				jq.Match(`.status.release.version == "%s"`, csvVersion),
			)

			g.Get(gvk.DSCInitialization, types.NamespacedName{Name: dsciName}).Eventually().Should(
				jq.Match(`.status.release.version == "%s"`, csvVersion),
			)
		})
	})

	Context("Pod Health", Label("Sanity"), func() {
		It("should not have operator pods stuck in CrashLoopBackOff", Label("ODS-818"), func() {
			pods, err := k8sClient.CoreV1().Pods(operatorNamespace).List(
				context.Background(),
				metav1.ListOptions{LabelSelector: operatorPodLabelSelector()},
			)
			Expect(err).NotTo(HaveOccurred(), "failed to list operator pods")

			for _, pod := range pods.Items {
				for _, cs := range pod.Status.ContainerStatuses {
					if cs.State.Waiting != nil {
						var reason string
						var exitCode int32
						if cs.LastTerminationState.Terminated != nil {
							reason = cs.LastTerminationState.Terminated.Reason
							exitCode = cs.LastTerminationState.Terminated.ExitCode
						}
						Expect(cs.State.Waiting.Reason).NotTo(Equal("CrashLoopBackOff"),
							"operator pod %s container %s is in CrashLoopBackOff (last terminated reason: %s, exit code: %d)",
							pod.Name, cs.Name, reason, exitCode,
						)
					}
				}
			}
		})
	})
})
