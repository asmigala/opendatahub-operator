package qe_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster/gvk"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/resources"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/utils/test/matchers/jq"
)

// getAuthGroups fetches the Auth CR and extracts the group list from the given field
// (e.g. "adminGroups" or "allowedGroups").
func getAuthGroups(field string) []string {
	authObj := resources.GvkToUnstructured(gvk.Auth)

	err := testCtx.Client().Get(context.Background(), types.NamespacedName{Name: "auth"}, authObj)
	Expect(err).NotTo(HaveOccurred(), "failed to get Auth CR")

	groups, found, err := unstructured.NestedStringSlice(authObj.Object, "spec", field)
	Expect(err).NotTo(HaveOccurred(), "failed to extract .spec.%s from Auth CR", field)
	Expect(found).To(BeTrue(), ".spec.%s not found in Auth CR", field)
	Expect(groups).NotTo(BeEmpty(), ".spec.%s should not be empty", field)

	return groups
}

// verifyBindingHasGroupSubjects checks that a RoleBinding or ClusterRoleBinding has subjects
// for each of the given groups.
func verifyBindingHasGroupSubjects(bindingGVK schema.GroupVersionKind, bindingName string, groups []string) {
	g := testCtx.NewGinkgoWithT(GinkgoT())

	nn := types.NamespacedName{Name: bindingName}
	if bindingGVK == gvk.RoleBinding {
		nn.Namespace = appsNamespace
	}

	for _, group := range groups {
		g.Get(bindingGVK, nn).Eventually().Should(
			jq.Match(`.subjects | any(.name == "%s")`, group),
			fmt.Sprintf("%s %s should have subject %s", bindingGVK.Kind, bindingName, group),
		)
	}
}

var _ = Describe("Auth CRD", Label("AuthCRD", "Smoke", "RHOAIENG-18846"), func() {
	It("should have adminGroups subjects in the admin RoleBinding", func() {
		groups := getAuthGroups("adminGroups")
		verifyBindingHasGroupSubjects(gvk.RoleBinding, "data-science-admingroup-rolebinding", groups)
	})

	It("should have adminGroups subjects in the admin ClusterRoleBinding", func() {
		groups := getAuthGroups("adminGroups")
		verifyBindingHasGroupSubjects(gvk.ClusterRoleBinding, "data-science-admingroupcluster-rolebinding", groups)
	})

	It("should have allowedGroups subjects in the allowed ClusterRoleBinding", func() {
		groups := getAuthGroups("allowedGroups")
		verifyBindingHasGroupSubjects(gvk.ClusterRoleBinding, "data-science-allowedgroupcluster-rolebinding", groups)
	})
})
