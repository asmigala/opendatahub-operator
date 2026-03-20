package qe_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"
)

// collectODSNamespaces returns all namespaces relevant to ODS (operator + labeled namespaces).
func collectODSNamespaces() []string {
	namespaces := []string{operatorNamespace}

	nsList, err := k8sClient.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{
		LabelSelector: "opendatahub.io/generated-namespace",
	})
	Expect(err).NotTo(HaveOccurred())
	for _, ns := range nsList.Items {
		namespaces = append(namespaces, ns.Name)
	}

	dashboardNsList, err := k8sClient.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{
		LabelSelector: "opendatahub.io/dashboard",
	})
	Expect(err).NotTo(HaveOccurred())
	for _, ns := range dashboardNsList.Items {
		namespaces = append(namespaces, ns.Name)
	}

	return namespaces
}

var _ = Describe("Post Install Verification", Label("PostInstall"), func() {
	It("should use image digests instead of tags for all pods in ODS namespaces", Label("Smoke", "ODS-2406", "ExcludeOnODH"), func() {
		skipIfODH("Image digest verification only applies to RHOAI")

		namespaces := collectODSNamespaces()
		digestRegexp := regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)
		var violations []string

		for _, ns := range namespaces {
			GinkgoWriter.Printf("checking pods in namespace %s\n", ns)
			pods, err := k8sClient.CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to list pods in namespace %s", ns)

			for _, pod := range pods.Items {
				for _, container := range pod.Spec.Containers {
					if !digestRegexp.MatchString(container.Image) {
						violations = append(violations,
							fmt.Sprintf("  %s/%s container %s: %s", ns, pod.Name, container.Name, container.Image))
					}
				}
				for _, container := range pod.Spec.InitContainers {
					if !digestRegexp.MatchString(container.Image) {
						violations = append(violations,
							fmt.Sprintf("  %s/%s init-container %s: %s", ns, pod.Name, container.Name, container.Image))
					}
				}
			}
		}

		Expect(violations).To(BeEmpty(),
			"found %d images using tags instead of digests:\n%s",
			len(violations), strings.Join(violations, "\n"))
	})

	It("should not have pods running with anyuid SCC or as root", Label("Smoke", "RHOAIENG-15892"), func() {
		pods, err := k8sClient.CoreV1().Pods(appsNamespace).List(context.Background(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to list pods in %s", appsNamespace)

		for _, pod := range pods.Items {
			scc := pod.Annotations["openshift.io/scc"]
			Expect(scc).NotTo(Equal("anyuid"),
				"pod %s/%s is running with anyuid SCC", appsNamespace, pod.Name,
			)

			if pod.Spec.SecurityContext != nil && pod.Spec.SecurityContext.RunAsUser != nil {
				Expect(*pod.Spec.SecurityContext.RunAsUser).NotTo(BeZero(),
					"pod %s/%s has pod-level runAsUser=0", appsNamespace, pod.Name,
				)
			}

			for _, container := range pod.Spec.Containers {
				if container.SecurityContext != nil && container.SecurityContext.RunAsUser != nil {
					Expect(*container.SecurityContext.RunAsUser).NotTo(BeZero(),
						"pod %s/%s container %s has runAsUser=0", appsNamespace, pod.Name, container.Name,
					)
				}
			}
		}
	})

	It("should have consistent CSV display name and version", Label("Smoke", "ODS-1862"), func() {
		g := testCtx.NewGinkgoWithT(GinkgoT())

		csvName := getInstalledCSVName()

		g.Get(gvk.ClusterServiceVersion, k8stypes.NamespacedName{
			Name:      csvName,
			Namespace: operatorNamespace,
		}).Eventually().Should(
			jq.Match(`(.metadata.name | split(".")[1:] | join(".") | ltrimstr("v")) == .spec.version`),
			"CSV name version suffix should match spec.version",
		)
	})

	// FIXME: "Verify DSC Contains Correct Component Versions" requires cloning the
	// rhods-operator git repo and comparing component_metadata.yaml files against DSC status.
	// Tags: Smoke, Operator, RHOAIENG-12693, ExcludeOnODH
	//
	// FIXME: "Verify Notebooks Network Policies For All Platforms" requires notebook creation
	// infrastructure. Tags: Smoke, Tier1, JupyterHub, ODS-2045, Operator
	//
	// FIXME: "Verify No Alerts Are Firing After Installation Except For DeadManSnitch" requires
	// Prometheus/alerting infrastructure. Tags: Smoke, ODS-540, RHOAIENG-13079, Operator
})
