package qe_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"
)

// imageTestEntry defines a test case for checking a component's image on CSV and deployment.
type imageTestEntry struct {
	component      string
	deploymentName string
	labelSelector  string
	imageName      string
}

// FIXME: some of these were wrong in the robot test suite, also the whole test was always passing. Double check
var imageTestEntries = []imageTestEntry{
	//{component: "modelregistry", deploymentName: "model-registry-operator-controller-manager", labelSelector: "control-plane=model-registry-operator", imageName: "odh_mlmd_grpc_server_image"},
	{component: "modelregistry", deploymentName: "model-registry-operator-controller-manager", labelSelector: "control-plane=model-registry-operator", imageName: "odh_model_registry_operator_image"},
	{component: "trustyai", deploymentName: "trustyai-service-operator-controller-manager", labelSelector: "app.kubernetes.io/part-of=trustyai", imageName: "odh_trustyai_service_operator_image"},
	{component: "ray", deploymentName: "kuberay-operator", labelSelector: "app.kubernetes.io/name=kuberay", imageName: "odh_kuberay_operator_controller_image"},
	{component: "kueue", deploymentName: "kueue-controller-manager", labelSelector: "app.kubernetes.io/name=kueue", imageName: "odh_kueue_controller_image"},
	{component: "workbenches", deploymentName: "odh-notebook-controller-manager", labelSelector: "app.kubernetes.io/part-of=workbenches", imageName: "odh_notebook_controller_image"},
	{component: "workbenches", deploymentName: "notebook-controller-deployment", labelSelector: "app.kubernetes.io/part-of=workbenches", imageName: "odh_kf_notebook_controller_image"},
	{component: "dashboard", imageName: "odh_dashboard_image"},
	{component: "dashboard", imageName: "odh_kube_auth_proxy_image"},
	{component: "dashboard", imageName: "odh_mod_arch_model_registry_image"},
	{component: "dashboard", imageName: "odh_mod_arch_gen_ai_image"},
	{component: "dashboard", imageName: "odh_mod_arch_maas_image"},
	{component: "datasciencepipelines", deploymentName: "data-science-pipelines-operator-controller-manager", labelSelector: "app.kubernetes.io/name=data-science-pipelines-operator", imageName: "odh_data_science_pipelines_operator_controller_image"},
	{component: "trainingoperator", deploymentName: "kubeflow-training-operator", labelSelector: "app.kubernetes.io/name=training-operator", imageName: "odh_training_operator_image"},
}

var _ = Describe("Component Images", Label("ComponentImages", "Smoke", "RHOAIENG-12576"), Ordered, func() {
	for _, entry := range imageTestEntries {
		entry := entry
		Context(entry.component, func() {
			deployName := entry.deploymentName
			labelSel := entry.labelSelector

			BeforeAll(func() {
				if entry.component == "dashboard" {
					deployName = resolveDashboardName()
					labelSel = resolveDashboardLabelSelector()
				}
			})

			It(fmt.Sprintf("should have correct %s image on %s deployment", entry.imageName, deployName), func() {
				state := getDSCComponentState(entry.component)
				if state != "Managed" {
					Skip(fmt.Sprintf("component %s is %s, not Managed", entry.component, state))
				}

				csvImage := getCSVRelatedImage(entry.imageName)
				GinkgoWriter.Printf("CSV image for %s: %s\n", entry.imageName, csvImage)

				g := testCtx.NewGinkgoWithT(GinkgoT())
				g.Get(gvk.Deployment, k8stypes.NamespacedName{
					Name:      deployName,
					Namespace: appsNamespace,
				}).Eventually(10 * time.Minute).Should(Not(BeNil()), "deployment %s should exist", deployName)

				g.List(gvk.Pod,
					client.InNamespace(appsNamespace),
					client.MatchingLabels(parseLabelSelector(labelSel)),
				).Eventually(10 * time.Minute).Should(And(
					Not(BeEmpty()),
					HaveEach(jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`)),
				), "pods with label %s should be ready", labelSel)

				g.Get(gvk.Deployment, k8stypes.NamespacedName{
					Name:      deployName,
					Namespace: appsNamespace,
				}).Eventually().Should(
					jq.Match(`.spec.template.spec.containers | any(.image == "%s")`, csvImage),
					"deployment %s should have container with image %s", deployName, csvImage,
				)
			})
		})
	}
})
