package qe_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"
)

// componentSpec defines a DSC component and its expected deployment for state transition tests.
type componentSpec struct {
	component      string
	deploymentName string
	labelSelector  string
	jiraTag        string
	excludeOnODH   bool
}

// nestedComponentSpec defines a nested DSC component (e.g. kserve.modelsAsService).
type nestedComponentSpec struct {
	componentSpec
	parentComponent string
	nestedComponent string
}

// needs to be array of pointers because we are going to modify the dashboard entry and iteration would capture it by value
var dscComponents = []*componentSpec{
	// FIXME: claude decided to remove kueue from here to get around the unsupported Managed state while still being able to generate the tests on the fly
	// maybe we can find a better solution
	// kueue only supports Removed/Unmanaged (not Managed), tested separately below
	{component: "ray", deploymentName: "kuberay-operator", labelSelector: "app.kubernetes.io/name=kuberay", jiraTag: "RHOAIENG-5435"},
	{component: "trainingoperator", deploymentName: "kubeflow-training-operator", labelSelector: "app.kubernetes.io/name=training-operator", jiraTag: "RHOAIENG-6627"},
	{component: "trainer", deploymentName: "kubeflow-trainer-controller-manager", labelSelector: "app.kubernetes.io/name=trainer", excludeOnODH: true},
	//{component: "dashboard", jiraTag: "RHOAIENG-7298"}, // deploymentName and labelSelector resolved in BeforeAll
	{component: "aipipelines", deploymentName: "data-science-pipelines-operator-controller-manager", labelSelector: "app.kubernetes.io/name=data-science-pipelines-operator", jiraTag: "RHOAIENG-7298"},
	{component: "trustyai", deploymentName: "trustyai-service-operator-controller-manager", labelSelector: "app.kubernetes.io/part-of=trustyai", jiraTag: "RHOAIENG-14018"},
	{component: "modelregistry", deploymentName: "model-registry-operator-controller-manager", labelSelector: "control-plane=model-registry-operator", jiraTag: "RHOAIENG-10404"},
	{component: "kserve", deploymentName: "kserve-controller-manager", labelSelector: "control-plane=kserve-controller-manager", jiraTag: "RHOAIENG-7217"},
	{component: "workbenches", deploymentName: "notebook-controller-deployment", labelSelector: "component.opendatahub.io/name=odh-notebook-controller"},
	{component: "feastoperator", deploymentName: "feast-operator-controller-manager", labelSelector: "app.kubernetes.io/part-of=feastoperator"},
	{component: "llamastackoperator", deploymentName: "llama-stack-k8s-operator-controller-manager", labelSelector: "app.kubernetes.io/part-of=llamastackoperator"},
	{component: "mlflowoperator", deploymentName: "mlflow-operator-controller-manager", labelSelector: "app.kubernetes.io/name=mlflow-operator"},
	{component: "sparkoperator", deploymentName: "spark-operator-controller", labelSelector: "app.kubernetes.io/name=spark-operator"},
}

var dscNestedComponents = []nestedComponentSpec{
	{
		parentComponent: "kserve",
		nestedComponent: "modelsAsService",
		componentSpec: componentSpec{
			deploymentName: "maas-api",
			labelSelector:  "app.kubernetes.io/part-of=models-as-a-service",
		},
	},
}

// restoreComponentState restores a component to its saved state.
func restoreComponentState(component, deploymentName, labelSelector, savedState string) {
	currentState := getDSCComponentState(component)
	if currentState == savedState {
		return
	}

	targetState := savedState
	if targetState == "" {
		targetState = "Removed"
	}

	setDSCComponentState(component, targetState)

	switch targetState {
	case "Managed":
		waitForDeploymentAvailable(deploymentName, labelSelector)
	case "Removed":
		waitForDeploymentRemoved(deploymentName, labelSelector)
	}
}

// restoreNestedComponentState restores a nested component and its parent to saved states.
func restoreNestedComponentState(parent, nested, deploymentName, labelSelector, savedNestedState, savedParentState string) {
	currentState := getDSCNestedComponentState(parent, nested)
	targetState := savedNestedState
	if targetState == "" {
		targetState = "Removed"
	}

	if currentState != targetState {
		if targetState == "Managed" {
			parentState := getDSCComponentState(parent)
			if parentState != "Managed" {
				setDSCComponentState(parent, "Managed")
			}
		}
		setDSCNestedComponentState(parent, nested, targetState)
		switch targetState {
		case "Managed":
			waitForDeploymentAvailable(deploymentName, labelSelector)
		case "Removed":
			waitForDeploymentRemoved(deploymentName, labelSelector)
		}
	}

	parentCurrent := getDSCComponentState(parent)
	parentTarget := savedParentState
	if parentTarget == "" {
		parentTarget = "Removed"
	}
	if parentCurrent != parentTarget {
		setDSCComponentState(parent, parentTarget)
	}
}

// resolveDashboardComponent fills in the platform-specific deployment name and
// label selector for the dashboard entry in dscComponents.
func resolveDashboardComponent() {
	deployName := resolveDashboardName()
	labelSel := resolveDashboardLabelSelector()

	for i := range dscComponents {
		if dscComponents[i].component == "dashboard" {
			dscComponents[i].deploymentName = deployName
			dscComponents[i].labelSelector = labelSel
			return
		}
	}
}

var _ = Describe("DSC Components", Label("DSCComponents", "Integration"), Ordered, ContinueOnFailure, func() {
	var savedStates map[string]string
	var savedNestedStates map[string]string

	BeforeAll(func() {
		resolveDashboardComponent()
		waitForDSCReady()

		savedStates = make(map[string]string)
		savedStates["kueue"] = getDSCComponentState("kueue")
		GinkgoWriter.Printf("Saved kueue state: %s\n", savedStates["kueue"])
		for _, c := range dscComponents {
			savedStates[c.component] = getDSCComponentState(c.component)
			GinkgoWriter.Printf("Saved %s state: %s\n", c.component, savedStates[c.component])
		}

		savedNestedStates = make(map[string]string)
		for _, nc := range dscNestedComponents {
			key := nc.parentComponent + "." + nc.nestedComponent
			savedNestedStates[key] = getDSCNestedComponentState(nc.parentComponent, nc.nestedComponent)
			GinkgoWriter.Printf("Saved %s state: %s\n", key, savedNestedStates[key])
		}
	})

	for _, comp := range dscComponents {
		comp := comp

		managedLabels := Labels{"Smoke", comp.component + "-managed"}
		removedLabels := Labels{"Tier1", comp.component + "-removed"}
		if comp.jiraTag != "" {
			managedLabels = append(managedLabels, comp.jiraTag)
			removedLabels = append(removedLabels, comp.jiraTag)
		}

		Context(fmt.Sprintf("%s state transitions", comp.component), func() {
			It(fmt.Sprintf("should deploy when %s is set to Managed", comp.component), managedLabels, func() {
				if comp.excludeOnODH {
					skipIfODH(fmt.Sprintf("%s is not supported on ODH", comp.component))
				}

				currentState := getDSCComponentState(comp.component)
				if currentState != "Managed" {
					setDSCComponentState(comp.component, "Managed")
				}

				waitForDeploymentAvailable(comp.deploymentName, comp.labelSelector)
				
				// FIXME: robot tests also verified the image registry here
			})

			It(fmt.Sprintf("should remove resources when %s is set to Removed", comp.component), removedLabels, func() {
				if comp.excludeOnODH {
					skipIfODH(fmt.Sprintf("%s is not supported on ODH", comp.component))
				}

				currentState := getDSCComponentState(comp.component)
				if currentState != "Removed" {
					setDSCComponentState(comp.component, "Removed")
				}

				waitForDeploymentRemoved(comp.deploymentName, comp.labelSelector)
			})

			AfterEach(func() {
				restoreComponentState(comp.component, comp.deploymentName, comp.labelSelector, savedStates[comp.component])
			})
		})
	}

	Context("kueue unmanaged state transitions", func() {
		It("should handle Removed to Unmanaged transition", Label("Smoke", "kueue-unmanaged-from-removed"), func() {
			setDSCComponentState("kueue", "Removed")
			waitForDeploymentRemoved("kueue-controller-manager", "app.kubernetes.io/name=kueue")

			setDSCComponentState("kueue", "Unmanaged")
		})

		It("should handle Unmanaged to Removed transition", Label("Tier1", "kueue-removed-from-unmanaged"), func() {
			setDSCComponentState("kueue", "Unmanaged")

			setDSCComponentState("kueue", "Removed")
			waitForDeploymentRemoved("kueue-controller-manager", "app.kubernetes.io/name=kueue")
		})

		AfterEach(func() {
			restoreComponentState("kueue", "kueue-controller-manager", "app.kubernetes.io/name=kueue", savedStates["kueue"])
		})
	})

	// FIXME: I think this is obsolote - there's no knativeServingGVK in the cluster at all, even when kserve is enabled,
	// the robot tests just interpreted the missing CRD as non-present CR
	//Context("kserve removed state side effects", func() {
		//It("should remove KnativeServing CR when KServe is Removed", Label("Tier1", "kserve-side-effects", "RHOAIENG-7217"), func() {
			//setDSCComponentState("kserve", "Removed")
			//waitForDeploymentRemoved("kserve-controller-manager", "control-plane=kserve-controller-manager")

			//g := testCtx.NewGinkgoWithT(GinkgoT())
			//g.List(knativeServingGVK,
				//client.InNamespace("knative-serving"),
			//).Eventually(5 * time.Minute).Should(BeEmpty(),
				//"KnativeServing should be removed when KServe is Removed",
			//)
		//})

		//AfterEach(func() {
			//restoreComponentState("kserve", "kserve-controller-manager", "control-plane=kserve-controller-manager", savedStates["kserve"])
		//})
	//})

	for _, nc := range dscNestedComponents {
		nc := nc
		key := nc.parentComponent + "." + nc.nestedComponent

		// FIXME: need to review this logic, seems a bit fishy
		Context(fmt.Sprintf("%s state transitions", key), func() {
			It(fmt.Sprintf("should deploy when %s is set to Managed", key), Label("Smoke", nc.nestedComponent+"-managed"), func() {
				parentState := getDSCComponentState(nc.parentComponent)
				if parentState != "Managed" {
					setDSCComponentState(nc.parentComponent, "Managed")
				}

				currentState := getDSCNestedComponentState(nc.parentComponent, nc.nestedComponent)
				if currentState != "Managed" {
					setDSCNestedComponentState(nc.parentComponent, nc.nestedComponent, "Managed")
				}

				waitForDeploymentAvailable(nc.deploymentName, nc.labelSelector)
			})

			It(fmt.Sprintf("should remove resources when %s is set to Removed", key), Label("Tier1", nc.nestedComponent+"-removed"), func() {
				currentState := getDSCNestedComponentState(nc.parentComponent, nc.nestedComponent)
				if currentState != "Removed" {
					setDSCNestedComponentState(nc.parentComponent, nc.nestedComponent, "Removed")
				}

				waitForDeploymentRemoved(nc.deploymentName, nc.labelSelector)
			})

			AfterEach(func() {
				restoreNestedComponentState(
					nc.parentComponent, nc.nestedComponent,
					nc.deploymentName, nc.labelSelector,
					savedNestedStates[key],
					savedStates[nc.parentComponent],
				)
			})
		})
	}

	Context("model registry namespace", func() {
		It("should have the correct registriesNamespace configured", Label("Smoke", "modelregistry-namespace", "RHOAIENG-10404"), func() {
			g := testCtx.NewGinkgoWithT(GinkgoT())

			expectedNS := resolveModelRegistryNamespace()

			g.Get(gvk.DataScienceCluster, types.NamespacedName{Name: dscName}).
				Eventually().Should(
				jq.Match(`.spec.components.modelregistry.registriesNamespace == "%s"`, expectedNS),
			)

			_, err := k8sClient.CoreV1().Namespaces().Get(context.Background(), expectedNS, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "model registry namespace %s should exist", expectedNS)
		})
	})
})
