package qe_test

// Platform-aware helpers: dashboard resolution, operator/CSV lookups,
// DSC/DSCI state management, skip helpers, and DSC readiness.

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/resources"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"
)

// --- Dashboard resolution ---

// resolveDashboardName returns the platform-specific dashboard deployment name.
// Must be called after cluster.Init().
func resolveDashboardName() string {
	release := cluster.GetRelease()
	switch release.Name {
	case cluster.ManagedRhoai, cluster.SelfManagedRhoai:
		return "rhods-dashboard"
	default:
		return "odh-dashboard"
	}
}

// resolveDashboardLabelSelector returns the platform-specific dashboard label selector.
func resolveDashboardLabelSelector() string {
	return "app.kubernetes.io/part-of=" + resolveDashboardName()
}


// resolveDashboardName returns the platform-specific model registry namespace
func resolveModelRegistryNamespace() string {
	release := cluster.GetRelease()
	switch release.Name {
	case cluster.ManagedRhoai, cluster.SelfManagedRhoai:
		return "rhoai-model-registries"
	default:
		return "odh-model-registries"
	}
}

// --- Skip helpers ---

// skipIfODH skips the test if running on an ODH cluster.
func skipIfODH(reason string) {
	release := cluster.GetRelease()
	if release.Name == cluster.OpenDataHub {
		Skip(reason)
	}
}

// --- Operator / CSV helpers ---

// getExpectedReleaseName returns the expected release name based on the platform type.
func getExpectedReleaseName() string {
	// FIXME: this is probably a circular check - we get the release name from the cluster
	// in cluster.Init() and then check that it's the same name. We should copy what ods-ci
	// is doing and decide the expected name from the PRODUCT var
	release := cluster.GetRelease()
	return string(release.Name)
}

// getInstalledCSVName finds the operator Subscription in the operator namespace and
// returns the installedCSV name from its status.
func getInstalledCSVName() string {
	subList := &unstructured.UnstructuredList{}
	subList.SetGroupVersionKind(gvk.Subscription)

	err := testCtx.Client().List(context.Background(), subList,
		client.InNamespace(operatorNamespace),
		client.MatchingLabels(operatorSubscriptionLabels()),
	)
	Expect(err).NotTo(HaveOccurred(), "failed to list Subscriptions in %s", operatorNamespace)
	Expect(subList.Items).NotTo(BeEmpty(), "no operator Subscription found in %s", operatorNamespace)

	csvName, found, err := unstructured.NestedString(subList.Items[0].Object, "status", "installedCSV")
	Expect(err).NotTo(HaveOccurred(), "failed to extract .status.installedCSV")
	Expect(found).To(BeTrue(), ".status.installedCSV not found in Subscription")
	Expect(csvName).NotTo(BeEmpty(), ".status.installedCSV should not be empty")

	return csvName
}

// getCSVRelatedImage looks up a relatedImages entry by name from the operator CSV.
func getCSVRelatedImage(imageName string) string {
	csvName := getInstalledCSVName()

	csvObj := resources.GvkToUnstructured(gvk.ClusterServiceVersion)
	err := testCtx.Client().Get(context.Background(), types.NamespacedName{
		Name:      csvName,
		Namespace: operatorNamespace,
	}, csvObj)
	Expect(err).NotTo(HaveOccurred(), "failed to get CSV %s", csvName)

	relatedImages, found, err := unstructured.NestedSlice(csvObj.Object, "spec", "relatedImages")
	Expect(err).NotTo(HaveOccurred(), "failed to extract .spec.relatedImages from CSV")
	Expect(found).To(BeTrue(), ".spec.relatedImages not found in CSV %s", csvName)

	for _, ri := range relatedImages {
		riMap, ok := ri.(map[string]any)
		if !ok {
			continue
		}
		if riMap["name"] == imageName {
			image, _ := riMap["image"].(string)
			Expect(image).NotTo(BeEmpty(), "relatedImages entry %s has empty image", imageName)
			return image
		}
	}

	Fail(fmt.Sprintf("relatedImages entry %s not found in CSV %s", imageName, csvName))
	return ""
}

// operatorSubscriptionLabels returns the label selector for the operator Subscription.
func operatorSubscriptionLabels() map[string]string {
	release := cluster.GetRelease()

	var operatorName string
	switch release.Name {
	case cluster.ManagedRhoai, cluster.SelfManagedRhoai:
		operatorName = "rhods-operator"
	default:
		operatorName = "opendatahub-operator"
	}

	return map[string]string{
		fmt.Sprintf("operators.coreos.com/%s.%s", operatorName, operatorNamespace): "",
	}
}

// operatorPodLabelSelector returns the label selector for operator pods.
func operatorPodLabelSelector() string {
	release := cluster.GetRelease()
	switch release.Name {
	case cluster.ManagedRhoai, cluster.SelfManagedRhoai:
		return "name=rhods-operator"
	default:
		return "control-plane=controller-manager"
	}
}

// --- DSC / DSCI state management ---

// getDSCComponentState reads the management state of a DSC component.
func getDSCComponentState(component string) string {
	obj := resources.GvkToUnstructured(gvk.DataScienceCluster)
	err := testCtx.Client().Get(context.Background(), types.NamespacedName{Name: dscName}, obj)
	Expect(err).NotTo(HaveOccurred(), "failed to get DSC")

	state, found, err := unstructured.NestedString(obj.Object, "spec", "components", component, "managementState")
	Expect(err).NotTo(HaveOccurred(), "failed to extract state for %s", component)
	if !found {
		return ""
	}
	return state
}

// getDSCNestedComponentState reads the management state of a nested DSC component.
func getDSCNestedComponentState(parent, nested string) string {
	obj := resources.GvkToUnstructured(gvk.DataScienceCluster)
	err := testCtx.Client().Get(context.Background(), types.NamespacedName{Name: dscName}, obj)
	Expect(err).NotTo(HaveOccurred(), "failed to get DSC")

	state, found, err := unstructured.NestedString(obj.Object, "spec", "components", parent, nested, "managementState")
	Expect(err).NotTo(HaveOccurred(), "failed to extract state for %s.%s", parent, nested)
	if !found {
		return ""
	}
	return state
}

// setDSCComponentState patches the DSC to set a component's managementState.
// Uses merge patch to avoid conflicts with concurrent status updates.
func setDSCComponentState(component, state string) {
	patch := fmt.Sprintf(`{"spec":{"components":{"%s":{"managementState":"%s"}}}}`, component, state)
	obj := resources.GvkToUnstructured(gvk.DataScienceCluster)
	obj.SetName(dscName)

	err := testCtx.Client().Patch(context.Background(), obj,
		client.RawPatch(types.MergePatchType, []byte(patch)))
	Expect(err).NotTo(HaveOccurred(), "failed to patch DSC component %s to %s", component, state)

	GinkgoWriter.Printf("Set component %s state to %s\n", component, state)
}

// setDSCNestedComponentState patches the DSC to set a nested component's managementState.
func setDSCNestedComponentState(parent, nested, state string) {
	patch := fmt.Sprintf(`{"spec":{"components":{"%s":{"%s":{"managementState":"%s"}}}}}`, parent, nested, state)
	obj := resources.GvkToUnstructured(gvk.DataScienceCluster)
	obj.SetName(dscName)

	err := testCtx.Client().Patch(context.Background(), obj,
		client.RawPatch(types.MergePatchType, []byte(patch)))
	Expect(err).NotTo(HaveOccurred(), "failed to patch DSC nested component %s.%s to %s", parent, nested, state)

	GinkgoWriter.Printf("Set nested component %s.%s state to %s\n", parent, nested, state)
}

// setDSCITrustedCABundleState patches the DSCI trustedCABundle managementState.
func setDSCITrustedCABundleState(state string) {
	patch := fmt.Sprintf(`{"spec":{"trustedCABundle":{"managementState":"%s"}}}`, state)
	obj := resources.GvkToUnstructured(gvk.DSCInitialization)
	obj.SetName(dsciName)

	err := testCtx.Client().Patch(context.Background(), obj,
		client.RawPatch(types.MergePatchType, []byte(patch)))
	Expect(err).NotTo(HaveOccurred(), "failed to patch DSCI trustedCABundle managementState to %s", state)

	GinkgoWriter.Printf("Set trustedCABundle managementState to %s\n", state)
}

// setDSCICustomCABundle patches the DSCI trustedCABundle customCABundle value.
func setDSCICustomCABundle(value string) {
	patch := fmt.Sprintf(`{"spec":{"trustedCABundle":{"customCABundle":"%s"}}}`, value)
	obj := resources.GvkToUnstructured(gvk.DSCInitialization)
	obj.SetName(dsciName)

	err := testCtx.Client().Patch(context.Background(), obj,
		client.RawPatch(types.MergePatchType, []byte(patch)))
	Expect(err).NotTo(HaveOccurred(), "failed to patch DSCI customCABundle")
}

// getDSCICustomCABundle reads the current customCABundle value from the DSCI.
func getDSCICustomCABundle() string {
	obj := resources.GvkToUnstructured(gvk.DSCInitialization)
	err := testCtx.Client().Get(context.Background(), types.NamespacedName{Name: dsciName}, obj)
	Expect(err).NotTo(HaveOccurred(), "failed to get DSCI")

	value, _, _ := unstructured.NestedString(obj.Object, "spec", "trustedCABundle", "customCABundle")
	return value
}

// waitForDSCReady waits for the DSC to report ready state.
func waitForDSCReady() {
	g := testCtx.NewGinkgoWithT(GinkgoT())

	g.Get(gvk.DataScienceCluster, types.NamespacedName{Name: dscName}).
		Eventually(10 * time.Minute).Should(
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		"DSC should be in Ready state",
	)
}
