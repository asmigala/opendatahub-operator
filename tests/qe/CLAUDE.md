# QE Test Migration: Robot Framework -> Go/Ginkgo

This directory contains QE tests migrated from ods-ci Robot Framework tests to Go using Ginkgo/Gomega.
The source Robot tests live in the companion repo at `ods-ci/ods_ci/tests/`.

## Task

When asked to migrate a Robot test, read the `.robot` file and its `.resource` files, then create a
corresponding `*_test.go` file in this directory following the patterns below. All files share the
`qe_test` package (flat structure, no subdirectories). Put test logic (Describe/Context/It blocks)
in the test file. Put reusable helpers in the appropriate helpers file — do NOT scatter helpers
across test files.

## File Structure

### Suite & helpers

| File | Purpose |
|---|---|
| `suite_test.go` | Suite bootstrap (`TestQE`, `BeforeSuite`), package-level vars (`testCtx`, `k8sClient`, env config), `getEnvOrDefault` |
| `helpers_k8s_test.go` | Generic k8s helpers: `parseLabelSelector`, `patchDeployment`, `annotateDeployment`, `annotateNamespace`, `patchConfigMapData`, `waitForDeploymentAvailable`, `waitForDeploymentRemoved`, `verifyDeploymentField`, `verifyDeploymentFieldInt` |
| `helpers_platform_test.go` | Platform-aware helpers: dashboard resolution (`resolveDashboardName`, `resolveDashboardLabelSelector`), skip helpers (`skipIfODH`), operator/CSV lookups (`getExpectedReleaseName`, `getInstalledCSVName`, `getCSVRelatedImage`, `operatorSubscriptionLabels`, `operatorPodLabelSelector`), DSC/DSCI state management (`getDSCComponentState`, `setDSCComponentState`, `getDSCNestedComponentState`, `setDSCNestedComponentState`, `setDSCITrustedCABundleState`, `setDSCICustomCABundle`, `getDSCICustomCABundle`, `waitForDSCReady`) |
| `helpers_gateway_test.go` | Gateway/auth helpers: `newInsecureHTTPClient`, `callGatewayWithToken`, `callGatewayWithoutToken`, `getOpaqueToken`, `getCurrentUserName`, `getOIDCToken`, `skipIfOIDC`, `skipIfNotOIDC`, `getAPIStatusUserName`, `ensureTestSAExists` |

### Test files

Each test file contains only `Describe`/`Context`/`It` blocks, test-specific constants/types,
and helpers that are truly specific to that one test (e.g. `restoreComponentState` in
`dsc_components_test.go`).

**When migrating a new test**, check the helpers files first — the function you need likely
already exists. If you need a new helper, add it to the appropriate helpers file, not the test file.

## Key Packages to Reuse

| Package | What it provides | Example usage |
|---|---|---|
| `pkg/utils/test/testf/` | `TestContext`, `WithT` (wraps gomega with k8s client) | `testCtx.NewGinkgoWithT(GinkgoT())` |
| `pkg/cluster/gvk/` | GVK constants for k8s resources | `gvk.Deployment`, `gvk.ClusterRoleBinding`, `gvk.ServiceAccount` |
| `pkg/utils/test/matchers/jq/` | Gomega matcher using jq expressions | `jq.Match(`.spec.field == "value"`)` |
| `internal/controller/services/gateway/` | Gateway constants | `gateway.KubeAuthProxyName`, `gateway.GatewayNamespace` |
| `pkg/cluster/` | Cluster utilities | `cluster.GetDomain()`, `cluster.GetRelease()` |

## Important: GinkgoT() Compatibility

`GinkgoT()` does not satisfy `testing.TB` (Go's `testing.TB` has a private method preventing
external implementations). Use `testCtx.NewGinkgoWithT(GinkgoT())` instead of `testCtx.NewWithT()`.
The `NewGinkgoWithT` method in `pkg/utils/test/testf/testf.go` accepts `gomegaTypes.GomegaTestingT`
which `GinkgoT()` does satisfy.

## Patterns

### Resource Assertions (checking k8s objects exist and have expected fields)

```go
g := testCtx.NewGinkgoWithT(GinkgoT())

g.Get(gvk.Deployment, types.NamespacedName{
    Name:      gateway.KubeAuthProxyName,
    Namespace: gateway.GatewayNamespace,
}).Eventually().Should(
    jq.Match(`.spec.template.spec.containers[0].args | any(. == "--some-flag=true")`),
)
```

### HTTP Requests to Gateway

Use `callGatewayWithToken(token, path)` and `callGatewayWithoutToken(path)` from
`helpers_gateway_test.go`.

```go
resp, err := callGatewayWithToken(token, "/api/status")
Expect(err).NotTo(HaveOccurred())
defer resp.Body.Close()
Expect(resp.StatusCode).To(Equal(http.StatusOK))
```

### ServiceAccount Tokens

Use the Kubernetes TokenRequest API (not `oc create token`):

```go
ensureTestSAExists()
token := createServiceAccountToken(saName, namespace, 600) // 600 seconds
```

### DSC/DSCI State Management

Use helpers from `helpers_platform_test.go`. These use merge patch to avoid conflicts
with concurrent operator status updates:

```go
// Read state
state := getDSCComponentState("ray")

// Set state (uses merge patch — no resource version conflicts)
setDSCComponentState("ray", "Managed")
setDSCNestedComponentState("kserve", "modelsAsService", "Removed")

// Wait for deployment to appear/disappear (from helpers_k8s_test.go)
waitForDeploymentAvailable("kuberay-operator", "app.kubernetes.io/name=kuberay")
waitForDeploymentRemoved("kuberay-operator", "app.kubernetes.io/name=kuberay")
```

### Platform-Dependent Values

Dashboard deployment name and label selector vary by platform. Use the resolve helpers
(must be called after `cluster.Init()` in `BeforeSuite`):

```go
name := resolveDashboardName()           // "rhods-dashboard" or "odh-dashboard"
label := resolveDashboardLabelSelector()  // "app.kubernetes.io/part-of=rhods-dashboard" etc.
```

### OIDC vs Non-OIDC Clusters

Use `skipIfOIDC(reason)` / `skipIfNotOIDC(reason)` from `helpers_gateway_test.go`.
Auth mode is set via `CLUSTER_AUTH=oidc` env var.

### Ginkgo Labels for Filtering

Migrate Robot `[Tags]` to Ginkgo `Label()`. Include all tags except `Operator` (which is implicit
since all tests here target the operator). This includes:
- **Tier tags**: `Smoke`, `Tier1`, `Tier2`, etc.
- **JIRA references**: `RHOAIENG-XXXXX` — always include these
- **Category tags**: `Security`, `Negative`, `E2E`, etc. — include if present

Apply labels to the `Describe` block for tags shared across all test cases in the Robot file,
and to individual `Context`/`It` blocks for tags specific to certain test cases.

```go
var _ = Describe("My Feature", Label("MyFeature", "RHOAIENG-12345"), func() {
    Context("Basic checks", Label("Tier1"), func() { ... })
    Context("Edge cases", Label("Tier2"), func() { ... })
})
```

Run filtered: `go test ./tests/qe/ -v -count=1 -ginkgo.label-filter="Tier1"`

### Test Cleanup

Use `AfterEach` to clean up resources created during tests (e.g., ServiceAccounts).
This ensures cleanup happens even if a test fails.

### Mapping Robot Concepts to Go

| Robot | Go/Ginkgo |
|---|---|
| `*** Test Cases ***` | `Describe` + `It` blocks |
| `[Tags]` | `Label()` on `Describe`/`Context`/`It` |
| `[Setup]` / `[Teardown]` | `BeforeEach` / `AfterEach` |
| `Suite Setup` / `Suite Teardown` | `BeforeSuite` / `AfterSuite` |
| `Skip If ...` | `skipIfOIDC()` / `skipIfNotOIDC()` / `skipIfODH()` / `Skip()` |
| `Run And Return Rc And Output oc get ...` | `g.Get(gvk.X, ...)` with `jq.Match()` |
| `Run And Return Rc And Output oc patch ...` | `setDSCComponentState()` or `patchDeployment()` |
| `Should Be Equal` | `Expect(x).To(Equal(y))` |
| `Should Contain` | `Expect(x).To(ContainSubstring(y))` |
| Resource keywords (`.resource` files) | Helper functions in `helpers_*_test.go` |

## Shared State (package-level vars in suite_test.go)

- `testCtx` — `*testf.TestContext` with k8s controller-runtime client
- `k8sClient` — `*kubernetes.Clientset` for TokenRequest API and direct k8s calls
- `gatewayHostname` — resolved from cluster domain + gateway subdomain
- `appsNamespace` — from `APPS_NAMESPACE` env var (default: `redhat-ods-applications`)
- `operatorNamespace` — from `OPERATOR_NAMESPACE` env var (default: `redhat-ods-operator`)
- `clusterAuth` — from `CLUSTER_AUTH` env var
- `oidcIssuer`, `testUser`, `testPassword` — from env vars, used for OIDC tests

## Running Tests

```bash
# All QE tests
go test ./tests/qe/ -v -count=1 -timeout 10m

# By feature label
go test ./tests/qe/ -v -count=1 -ginkgo.label-filter="ServiceTokenAuth"

# By tier
go test ./tests/qe/ -v -count=1 -ginkgo.label-filter="Tier1"

# With OIDC cluster config
CLUSTER_AUTH=oidc OIDC_ISSUER=https://... TEST_USER=user TEST_PASSWORD=pass \
  go test ./tests/qe/ -v -count=1
```

## Completed Migrations

- `0104__service_token_auth.robot` -> `service_token_auth_test.go` (11 tests)
- `0103__auth_crd.robot` -> `auth_crd_test.go` (3 tests)
- `0108__operator.robot` -> `operator_test.go` (3 tests)
- `0113__dsc_components.robot` -> `dsc_components_test.go` + `controller_resources_test.go` (30+ tests)
- `0114__component_images.robot` -> `component_images_test.go` (14 tests)
- `0111__trusted_ca_bundles.robot` -> `trusted_ca_bundles_test.go` (6 tests)
