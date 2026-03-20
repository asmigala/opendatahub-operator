package qe_test

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/resources"
)

const (
	testCPULimit    = "1001m"
	testMemoryLimit = "4001Mi"
	testImage       = "registry.invalid/test:latest"
	testReplicas    = 0

	cpuJQPath        = ".spec.template.spec.containers[0].resources.limits.cpu"
	memoryJQPath     = ".spec.template.spec.containers[0].resources.limits.memory"
	imageJQPath      = ".spec.template.spec.containers[0].image"
	replicasJQPath   = ".spec.replicas"
	dashboardTmpName = "dashboard-TMP"
)

var controllers = []string{
	"data-science-pipelines-operator-controller-manager",
	"kuberay-operator",
	"notebook-controller-deployment",
	"odh-model-controller",
	"odh-notebook-controller-manager",
	"trustyai-service-operator-controller-manager",
	// "kserve-controller-manager",  // RHOAIENG-27943
	// "kubeflow-training-operator", // RHOAIENG-27944
	dashboardTmpName,
}

// patchAllTestFields patches cpu, memory, image, and replicas on a controller deployment.
func patchAllTestFields(controller string) {
	quotedCPU, _ := json.Marshal(testCPULimit)
	quotedMem, _ := json.Marshal(testMemoryLimit)
	quotedImg, _ := json.Marshal(testImage)

	patchDeployment(controller, "/spec/template/spec/containers/0/resources/limits/cpu", string(quotedCPU))
	patchDeployment(controller, "/spec/template/spec/containers/0/resources/limits/memory", string(quotedMem))
	patchDeployment(controller, "/spec/template/spec/containers/0/image", string(quotedImg))
	patchDeployment(controller, "/spec/replicas", fmt.Sprintf("%d", testReplicas))
}

var _ = Describe("Controller Resources Configuration", Label("ControllerResources", "Tier1", "Integration", "ODS-2664", "RHOAIENG-12811"), Ordered, ContinueOnFailure, func() {

	BeforeAll(func() {
		GinkgoWriter.Printf("Testing controller resource configuration for: %v\n", controllers)
	})

	AfterAll(func() {
		for _, controller := range controllers {
			err := k8sClient.AppsV1().Deployments(appsNamespace).Delete(
				context.Background(), controller, metav1.DeleteOptions{},
			)
			if err != nil {
				GinkgoWriter.Printf("Warning: failed to delete deployment %s during teardown: %v\n", controller, err)
			}
		}
	})

	for _, controller := range controllers {
		controller := controller

		Context(controller, func() {
			BeforeAll(func() {
				if controller == dashboardTmpName {
					controller = resolveDashboardName()
				}
			})
			It("Should not already have managed annotation", func() {
				deployment := resources.GvkToUnstructured(gvk.Deployment)
				err := testCtx.Client().Get(context.Background(), k8stypes.NamespacedName{Name: controller, Namespace: appsNamespace}, deployment)
				Expect(err).NotTo(HaveOccurred())
				Expect(deployment.GetAnnotations()).NotTo(HaveKey("opendatahub.io/managed"))
			})
			It("should preserve allowlisted fields and revert non-allowlisted fields", func() {
				patchAllTestFields(controller)

				By("waiting for operator to reconcile")
				time.Sleep(45 * time.Second) //nolint:mnd

				By("verifying allowlisted fields were preserved")
				verifyDeploymentField(controller, cpuJQPath, true, testCPULimit)
				verifyDeploymentField(controller, memoryJQPath, true, testMemoryLimit)
				verifyDeploymentFieldInt(controller, replicasJQPath, true, testReplicas)

				By("verifying non-allowlisted image was reverted")
				verifyDeploymentField(controller, imageJQPath, false, testImage)
			})

			It("should revert all fields when opendatahub.io/managed=true annotation is set", func() {
				patchAllTestFields(controller)

				annotateDeployment(controller, "opendatahub.io/managed", "true")

				verifyDeploymentField(controller, cpuJQPath, false, testCPULimit)
				verifyDeploymentField(controller, memoryJQPath, false, testMemoryLimit)
				verifyDeploymentFieldInt(controller, replicasJQPath, false, testReplicas)
			})

			It("should revert all fields immediately when managed annotation is already in place", func() {
				patchAllTestFields(controller)

				verifyDeploymentField(controller, cpuJQPath, false, testCPULimit)
				verifyDeploymentField(controller, memoryJQPath, false, testMemoryLimit)
				verifyDeploymentField(controller, imageJQPath, false, testImage)
				verifyDeploymentFieldInt(controller, replicasJQPath, false, testReplicas)
			})
		})
	}
})
