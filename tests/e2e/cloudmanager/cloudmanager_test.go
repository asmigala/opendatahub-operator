package cloudmanager_test

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/annotations"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/labels"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"

	. "github.com/onsi/gomega"
)

// TestCloudManager_DeploymentsAvailable verifies that creating a CR with all
// dependencies Managed causes the dependency deployments to actually reach
// Available status — not just exist.
func TestCloudManager_DeploymentsAvailable(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForReady(wt)
	waitForDeploymentsAvailable(wt)
}

// TestCloudManager_DeploymentSelfHealing verifies that if a managed deployment
// is deleted, the controller recreates it. This tests the reconcile loop against
// a real cluster with real UIDs and resource versions.
func TestCloudManager_DeploymentSelfHealing(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForDeploymentsAvailable(wt)

	for _, dep := range managedDependencyDeployments {
		t.Run(dep.Name, func(t *testing.T) {
			wt := tc.NewWithT(t)
			nn := types.NamespacedName{Name: dep.Name, Namespace: dep.Namespace}

			wt.Delete(gvk.Deployment, nn).Eventually().Should(Succeed())
			wt.Get(gvk.Deployment, nn).Eventually().Should(BeNil())

			// The controller should recreate it.
			wt.Get(gvk.Deployment, nn).Eventually().Should(
				jq.Match(`.status.conditions[] | select(.type == "Available") | .status == "True"`),
			)
		})
	}
}

// TestCloudManager_GarbageCollectionOnDelete verifies that deleting the CR
// causes Kubernetes garbage collection to clean up all owned deployments.
func TestCloudManager_GarbageCollectionOnDelete(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForReady(wt)

	// Verify deployments have owner references pointing to the CR.
	for _, dep := range managedDependencyDeployments {
		wt.Get(gvk.Deployment, types.NamespacedName{
			Name: dep.Name, Namespace: dep.Namespace,
		}).Eventually().Should(
			jq.Match(`.metadata.ownerReferences | length > 0`),
		)
	}

	// Verify namespaces have owner references pointing to the CR.
	for _, ns := range dependencyNamespaces {
		wt.Get(gvk.Namespace, types.NamespacedName{Name: ns}).
			Eventually().Should(
			jq.Match(`.metadata.ownerReferences | length > 0`),
		)
	}

	// Delete the CR.
	wt.Delete(provider.GVK, k8sEngineCrNn()).Eventually().Should(Succeed())
	wt.Get(provider.GVK, k8sEngineCrNn()).Eventually().Should(BeNil())

	// All owned deployments should be garbage-collected.
	for _, dep := range managedDependencyDeployments {
		wt.Get(gvk.Deployment, types.NamespacedName{
			Name: dep.Name, Namespace: dep.Namespace,
		}).Eventually().Should(BeNil())
	}

	// All owned namespaces should be garbage-collected.
	for _, ns := range dependencyNamespaces {
		wt.Get(gvk.Namespace, types.NamespacedName{Name: ns}).
			Eventually().Should(BeNil())
	}
}

// TestCloudManager_UnmanagedNotReconciled verifies that switching a dependency
// from Managed to Unmanaged causes the controller to stop reconciling it.
// If the deployment is then deleted, the controller should not recreate it.
func TestCloudManager_UnmanagedNotReconciled(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForDeploymentsAvailable(wt)

	// Capture the generation before patching.
	cr := wt.Get(provider.GVK, k8sEngineCrNn()).Eventually().Should(Not(BeNil()))
	gen, _, _ := unstructured.NestedInt64(cr.Object, "metadata", "generation")

	// Patch cert-manager to Unmanaged.
	wt.Patch(provider.GVK, k8sEngineCrNn(), func(obj *unstructured.Unstructured) error {
		return unstructured.SetNestedField(
			obj.Object, "Unmanaged",
			"spec", "dependencies", "certManager", "managementPolicy",
		)
	}).Eventually().Should(Not(BeNil()))

	// Wait for the controller to fully reconcile the spec change —
	// observedGeneration must catch up to the new generation.
	wt.Get(provider.GVK, k8sEngineCrNn()).Eventually().Should(And(
		jq.Match(`.metadata.generation > %d`, gen),
		jq.Match(`.status.observedGeneration == .metadata.generation`),
		jq.Match(`.status.phase == "Ready"`),
	))

	// Delete the cert-manager deployment.
	target := managedDependencyDeployments[0]
	wt.Expect(target.Name).To(ContainSubstring("cert-manager"), "expected first managed deployment to be cert-manager")
	nn := types.NamespacedName{Name: target.Name, Namespace: target.Namespace}
	wt.Delete(gvk.Deployment, nn).Eventually().Should(Succeed())
	wt.Get(gvk.Deployment, nn).Eventually().Should(BeNil())

	// It should NOT come back — the controller is no longer managing it.
	consistentlyGone(wt, nn)

	// The other deployments should still be running.
	for _, dep := range managedDependencyDeployments[1:] {
		wt.Get(gvk.Deployment, types.NamespacedName{
			Name: dep.Name, Namespace: dep.Namespace,
		}).Eventually().Should(Not(BeNil()))
	}
}

// TestCloudManager_InvalidNameRejected verifies that the CEL validation rule
// on the CRD rejects CRs with names other than the expected singleton name.
func TestCloudManager_InvalidNameRejected(t *testing.T) {
	wt := tc.NewWithT(t)

	cr := &unstructured.Unstructured{}
	cr.SetGroupVersionKind(provider.GVK)
	cr.SetName("wrong-name")
	cr.Object["spec"] = map[string]any{
		"dependencies": allManaged(),
	}

	err := wt.Client().Create(wt.Context(), cr)
	wt.Expect(err).To(HaveOccurred())
}

// ---------------------------------------------------------------------------
// Reconciliation lifecycle tests
// ---------------------------------------------------------------------------

// TestCloudManager_StatusConditions verifies that the CR reports proper status
// conditions with all expected fields after successful reconciliation:
// - Ready=True (top-level happy condition)
// - ProvisioningSucceeded=True (dependent condition)
// - Each condition has type, status, reason, lastTransitionTime, observedGeneration
// - Status phase is "Ready" and observedGeneration matches metadata.generation.
func TestCloudManager_StatusConditions(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForReady(wt)

	t.Run("Ready condition", func(t *testing.T) {
		wt := tc.NewWithT(t)
		wt.Get(provider.GVK, k8sEngineCrNn()).Eventually().Should(And(
			jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
			jq.Match(`.status.conditions[] | select(.type == "Ready") | has("lastTransitionTime")`),
		))
	})

	t.Run("ProvisioningSucceeded condition", func(t *testing.T) {
		wt := tc.NewWithT(t)
		wt.Get(provider.GVK, k8sEngineCrNn()).Eventually().Should(And(
			jq.Match(`.status.conditions[] | select(.type == "ProvisioningSucceeded") | .status == "True"`),
			jq.Match(`.status.conditions[] | select(.type == "ProvisioningSucceeded") | has("lastTransitionTime")`),
			jq.Match(`.status.conditions[] | select(.type == "ProvisioningSucceeded") | .observedGeneration > 0`),
		))
	})

	t.Run("phase and observedGeneration", func(t *testing.T) {
		wt := tc.NewWithT(t)
		wt.Get(provider.GVK, k8sEngineCrNn()).Eventually().Should(And(
			jq.Match(`.status.phase == "Ready"`),
			jq.Match(`.status.observedGeneration == .metadata.generation`),
		))
	})
}

// TestCloudManager_HelmRenderedResources verifies that the Helm chart rendering
// pipeline produces resources with the expected operator metadata. The deploy
// action stamps every owned resource with infrastructure labels and annotations.
func TestCloudManager_HelmRenderedResources(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForReady(wt)

	partOfValue := strings.ToLower(provider.GVK.Kind)

	for _, dep := range managedDependencyDeployments {
		t.Run(dep.Name, func(t *testing.T) {
			wt := tc.NewWithT(t)
			nn := types.NamespacedName{Name: dep.Name, Namespace: dep.Namespace}

			wt.Get(gvk.Deployment, nn).Eventually().Should(And(
				jq.Match(`.metadata.labels."%s" == "%s"`, labels.InfrastructurePartOf, partOfValue),
				jq.Match(`.metadata.annotations."%s" == "%s"`, annotations.InstanceName, provider.InstanceName),
				jq.Match(`.metadata.annotations | has("%s")`, annotations.InstanceGeneration),
				jq.Match(`.metadata.annotations | has("%s")`, annotations.InstanceUID),
				jq.Match(`.metadata.annotations | has("%s")`, annotations.PlatformVersion),
				jq.Match(`.metadata.annotations | has("%s")`, annotations.PlatformType),
			))
		})
	}
}

// TestCloudManager_NamespacesCreated verifies that the target namespaces
// for each managed dependency are created.
func TestCloudManager_NamespacesCreated(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForReady(wt)

	for _, ns := range dependencyNamespaces {
		t.Run(ns, func(t *testing.T) {
			wt := tc.NewWithT(t)
			wt.Get(gvk.Namespace, types.NamespacedName{Name: ns}).
				Eventually().
				Should(Not(BeNil()))
		})
	}
}

// TestCloudManager_ServiceAccountsCreated verifies that the Helm charts create
// labeled ServiceAccounts in each dependency namespace.
func TestCloudManager_ServiceAccountsCreated(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForReady(wt)

	for _, dep := range managedDependencyDeployments {
		t.Run(dep.Name+"/serviceaccounts", func(t *testing.T) {
			wt := tc.NewWithT(t)
			wt.List(gvk.ServiceAccount,
				client.InNamespace(dep.Namespace),
				client.MatchingLabels{labels.InfrastructurePartOf: strings.ToLower(provider.GVK.Kind)},
			).Eventually().Should(Not(BeEmpty()))
		})
	}
}

// TestCloudManager_StatusAfterSpecChange verifies that updating the CR spec
// triggers re-reconciliation and the status reflects the new generation.
// Unlike UnmanagedNotReconciled (which tests behavioral consequences of
// switching to Unmanaged), this test focuses on the status tracking contract:
// observedGeneration must catch up after each spec mutation.
func TestCloudManager_StatusAfterSpecChange(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForReady(wt)

	// Capture the current generation.
	cr := wt.Get(provider.GVK, k8sEngineCrNn()).Eventually().Should(Not(BeNil()))
	gen1, _, _ := unstructured.NestedInt64(cr.Object, "metadata", "generation")

	// First mutation: switch sailOperator to Unmanaged.
	wt.Patch(provider.GVK, k8sEngineCrNn(), func(obj *unstructured.Unstructured) error {
		return unstructured.SetNestedField(
			obj.Object, "Unmanaged",
			"spec", "dependencies", "sailOperator", "managementPolicy",
		)
	}).Eventually().Should(Not(BeNil()))

	wt.Get(provider.GVK, k8sEngineCrNn()).Eventually().Should(And(
		jq.Match(`.metadata.generation > %d`, gen1),
		jq.Match(`.status.observedGeneration == .metadata.generation`),
		jq.Match(`.status.phase == "Ready"`),
	))

	// Second mutation: switch it back to Managed.
	cr = wt.Get(provider.GVK, k8sEngineCrNn()).Eventually().Should(Not(BeNil()))
	gen2, _, _ := unstructured.NestedInt64(cr.Object, "metadata", "generation")

	wt.Patch(provider.GVK, k8sEngineCrNn(), func(obj *unstructured.Unstructured) error {
		return unstructured.SetNestedField(
			obj.Object, "Managed",
			"spec", "dependencies", "sailOperator", "managementPolicy",
		)
	}).Eventually().Should(Not(BeNil()))

	wt.Get(provider.GVK, k8sEngineCrNn()).Eventually().Should(And(
		jq.Match(`.metadata.generation > %d`, gen2),
		jq.Match(`.status.observedGeneration == .metadata.generation`),
		jq.Match(`.status.phase == "Ready"`),
	))
}

// ---------------------------------------------------------------------------
// Workload validation tests
// ---------------------------------------------------------------------------

// TestCloudManager_CertManagerIssuesCertificates verifies that the cert-manager
// operator is functional by checking that the bootstrap PKI trust chain works:
// the root CA Certificate becomes Ready and cert-manager creates its Secret.
func TestCloudManager_CertManagerIssuesCertificates(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForReady(wt)

	t.Run("selfsigned ClusterIssuer is ready", func(t *testing.T) {
		wt := tc.NewWithT(t)
		wt.Get(gvk.CertManagerClusterIssuer, types.NamespacedName{
			Name: "opendatahub-selfsigned-issuer",
		}).Eventually().Should(
			jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		)
	})

	t.Run("root CA Certificate is issued", func(t *testing.T) {
		wt := tc.NewWithT(t)
		wt.Get(gvk.CertManagerCertificate, types.NamespacedName{
			Name: "opendatahub-ca", Namespace: "cert-manager",
		}).Eventually().Should(
			jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		)
	})

	t.Run("CA-backed ClusterIssuer is ready", func(t *testing.T) {
		wt := tc.NewWithT(t)
		wt.Get(gvk.CertManagerClusterIssuer, types.NamespacedName{
			Name: "opendatahub-ca-issuer",
		}).Eventually().Should(
			jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		)
	})

	t.Run("CA Secret is created", func(t *testing.T) {
		wt := tc.NewWithT(t)
		wt.Get(gvk.Secret, types.NamespacedName{
			Name: "opendatahub-ca", Namespace: "cert-manager",
		}).Eventually().Should(Not(BeNil()))
	})
}

// TestCloudManager_LWSOperatorFunctional verifies that the LeaderWorkerSet
// operator is running and has registered the LeaderWorkerSet CRD.
func TestCloudManager_LWSOperatorFunctional(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForReady(wt)

	t.Run("LeaderWorkerSetOperator CR exists", func(t *testing.T) {
		wt := tc.NewWithT(t)
		wt.Get(gvk.LeaderWorkerSetOperatorV1, types.NamespacedName{
			Name: "cluster",
		}).Eventually().Should(Not(BeNil()))
	})

	t.Run("LeaderWorkerSet CRD is installed", func(t *testing.T) {
		wt := tc.NewWithT(t)
		wt.Get(gvk.CustomResourceDefinition, types.NamespacedName{
			Name: "leaderworkersets.leaderworkerset.x-k8s.io",
		}).Eventually().Should(Not(BeNil()))
	})
}

// TestCloudManager_GatewayAPICRDsInstalled verifies that the gateway-api
// Helm chart installs the standard Gateway API CRDs on the cluster.
func TestCloudManager_GatewayAPICRDsInstalled(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForReady(wt)

	gatewayAPICRDs := []string{
		"backendtlspolicies.gateway.networking.k8s.io",
		"gatewayclasses.gateway.networking.k8s.io",
		"gateways.gateway.networking.k8s.io",
		"grpcroutes.gateway.networking.k8s.io",
		"httproutes.gateway.networking.k8s.io",
		"referencegrants.gateway.networking.k8s.io",
	}

	for _, crdName := range gatewayAPICRDs {
		t.Run(crdName, func(t *testing.T) {
			wt := tc.NewWithT(t)
			wt.Get(gvk.CustomResourceDefinition, types.NamespacedName{
				Name: crdName,
			}).Eventually().Should(Not(BeNil()))
		})
	}
}

// TestCloudManager_SailOperatorFunctional verifies that the sail-operator
// is running and the Istio CR it creates reaches a healthy state.
func TestCloudManager_SailOperatorFunctional(t *testing.T) {
	wt := tc.NewWithT(t)
	createCR(t, wt, allManaged())
	waitForReady(wt)

	t.Run("Istio CR is healthy", func(t *testing.T) {
		wt := tc.NewWithT(t)
		wt.Get(gvk.Istio, types.NamespacedName{
			Name: "default", Namespace: "istio-system",
		}).Eventually().Should(
			jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		)
	})
}
