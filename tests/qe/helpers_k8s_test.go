package qe_test

// Generic Kubernetes helpers: patching, annotating, label parsing, deployment waits.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"
)

// parseLabelSelector parses a "key=value[,key2=value2]" label selector string into a map.
func parseLabelSelector(selector string) map[string]string {
	labels := map[string]string{}
	for _, part := range strings.Split(selector, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			labels[kv[0]] = kv[1]
		}
	}
	return labels
}

// patchDeployment applies a JSON patch to a deployment in the apps namespace.
func patchDeployment(name, patchPath, patchValue string) {
	patch := fmt.Sprintf(`[{"op":"replace","path":"%s","value":%s}]`, patchPath, patchValue)
	_, err := k8sClient.AppsV1().Deployments(appsNamespace).Patch(
		context.Background(),
		name,
		types.JSONPatchType,
		[]byte(patch),
		metav1.PatchOptions{},
	)
	Expect(err).NotTo(HaveOccurred(), "failed to patch deployment %s at %s", name, patchPath)
}

// annotateDeployment sets an annotation on a deployment in the apps namespace.
func annotateDeployment(name, key, value string) {
	patch := fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, key, value)
	_, err := k8sClient.AppsV1().Deployments(appsNamespace).Patch(
		context.Background(),
		name,
		types.MergePatchType,
		[]byte(patch),
		metav1.PatchOptions{},
	)
	Expect(err).NotTo(HaveOccurred(), "failed to annotate deployment %s", name)
}

// annotateNamespace sets an annotation on a namespace.
func annotateNamespace(namespace, key, value string) {
	patch := fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, key, value)
	_, err := k8sClient.CoreV1().Namespaces().Patch(
		context.Background(),
		namespace,
		types.MergePatchType,
		[]byte(patch),
		metav1.PatchOptions{},
	)
	Expect(err).NotTo(HaveOccurred(), "failed to annotate namespace %s", namespace)
}

// patchConfigMapData patches a ConfigMap's data field via merge patch.
func patchConfigMapData(name, namespace, key, value string) {
	patch := fmt.Sprintf(`{"data":{"%s":"%s"}}`, key, value)
	_, err := k8sClient.CoreV1().ConfigMaps(namespace).Patch(
		context.Background(),
		name,
		types.MergePatchType,
		[]byte(patch),
		metav1.PatchOptions{},
	)
	Expect(err).NotTo(HaveOccurred(), "failed to patch ConfigMap %s/%s", namespace, name)
}

// waitForDeploymentAvailable waits until a deployment exists and has ready pods.
func waitForDeploymentAvailable(deploymentName, labelSelector string) {
	g := testCtx.NewGinkgoWithT(GinkgoT())

	g.Get(gvk.Deployment, types.NamespacedName{
		Name:      deploymentName,
		Namespace: appsNamespace,
	}).Eventually(10 * time.Minute).Should(Not(BeNil()),
		"deployment %s should exist", deploymentName,
	)

	g.List(gvk.Pod,
		client.InNamespace(appsNamespace),
		client.MatchingLabels(parseLabelSelector(labelSelector)),
	).Eventually(10 * time.Minute).Should(And(
		Not(BeEmpty()),
		HaveEach(jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`)),
	), "pods with label %s should be ready", labelSelector)
}

// waitForDeploymentRemoved waits until a deployment no longer exists and pods are gone.
func waitForDeploymentRemoved(deploymentName, labelSelector string) {
	g := testCtx.NewGinkgoWithT(GinkgoT())

	g.Get(gvk.Deployment, types.NamespacedName{
		Name:      deploymentName,
		Namespace: appsNamespace,
	}).Eventually(10 * time.Minute).Should(BeNil(),
		"deployment %s should be removed", deploymentName,
	)

	g.List(gvk.Pod,
		client.InNamespace(appsNamespace),
		client.MatchingLabels(parseLabelSelector(labelSelector)),
	).Eventually(10 * time.Minute).Should(BeEmpty(),
		"pods with label %s should be removed", labelSelector,
	)
}

// verifyDeploymentField checks a deployment field matches (or doesn't match) an expected string value.
func verifyDeploymentField(controller, jqExpr string, shouldMatch bool, expected string) {
	g := testCtx.NewGinkgoWithT(GinkgoT())
	nn := types.NamespacedName{Name: controller, Namespace: appsNamespace}

	if shouldMatch {
		g.Get(gvk.Deployment, nn).Eventually(3 * time.Minute).Should(
			jq.Match(jqExpr+` == "%s"`, expected),
			"%s: %s should still be %s", controller, jqExpr, expected,
		)
	} else {
		g.Get(gvk.Deployment, nn).Eventually(3 * time.Minute).Should(
			jq.Match(jqExpr+` != "%s"`, expected),
			"%s: %s should have been reverted from %s", controller, jqExpr, expected,
		)
	}
}

// verifyDeploymentFieldInt checks a deployment numeric field matches (or doesn't match) an expected value.
func verifyDeploymentFieldInt(controller, jqExpr string, shouldMatch bool, expected int) {
	g := testCtx.NewGinkgoWithT(GinkgoT())
	nn := types.NamespacedName{Name: controller, Namespace: appsNamespace}

	if shouldMatch {
		g.Get(gvk.Deployment, nn).Eventually(3 * time.Minute).Should(
			jq.Match(jqExpr+` == %d`, expected),
			"%s: %s should still be %d", controller, jqExpr, expected,
		)
	} else {
		g.Get(gvk.Deployment, nn).Eventually(3 * time.Minute).Should(
			jq.Match(jqExpr+` != %d`, expected),
			"%s: %s should have been reverted from %d", controller, jqExpr, expected,
		)
	}
}

// getResourceOrNil fetches an unstructured resource and returns nil if not found.
func getResourceOrNil(resGVK schema.GroupVersionKind, name, namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(resGVK)
	err := testCtx.Client().Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, obj)
	if err != nil {
		return nil
	}
	return obj
}
