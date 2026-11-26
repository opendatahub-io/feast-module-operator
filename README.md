# feast-module-operator

Module operator for the FeastOperator component, deployed and managed by the
[ODH Operator](https://github.com/opendatahub-io/opendatahub-operator) via
the modular handler framework.

## Overview

This operator watches `FeastOperator` Custom Resources (API version
`components.platform.opendatahub.io/v1alpha1`) and deploys the upstream
[feast-operator](https://github.com/opendatahub-io/feast) using bundled
kustomize manifests.

The ODH Operator:
1. Deploys this module operator via a Helm chart
2. Creates a `FeastOperator` CR with platform configuration (e.g. OIDC settings)
3. This operator reconciles the CR and deploys the feast-operator workload

## Development
## Tesing CI pipeline
### Prerequisites

- Go 1.26+
- Access to a Kubernetes cluster (for integration/e2e tests)
- `podman` or `docker` for container builds

### Build

```bash
make build
```

### Run Unit Tests

```bash
make test
```

### Run Integration Tests (requires cluster)

```bash
make test-integration
```

### Run E2E Tests (dedicated Kind cluster)

```bash
make test-e2e ODH_PLATFORM_TYPE=odh   # ODH variant
make test-e2e ODH_PLATFORM_TYPE=rhoai # RHOAI variant (reuses the cluster)
```

Each invocation runs the full ordered workflow against the dedicated Kind
cluster `feast-module-operator-e2e`:

1. Preflight required tools and the container engine, then create the
   cluster (Kind `v0.33.0`, pinned default node image, isolated kubeconfig
   under a private scratch directory) or safely reuse it. The global
   kubeconfig and current context are never modified.
2. Install the OpenShift compatibility CRDs (`ServiceMonitor`, `APIServer`,
   `Route` — API storage/validation only), create or verify the test-owned
   namespace `opendatahub-feast-system`, and install the static
   service-certificate fixtures (`controller-manager-metrics-tls` Secret and
   `openshift-service-ca.crt` CA ConfigMap). No cert-manager and no
   unrelated APIs.
3. Build a uniquely tagged local controller image for the node
   architecture through `make container-build` (bundled manifests and the
   published chart defaults stay unchanged).
4. Load that exact tag into every Kind node from a private image archive
   (`kind load image-archive`) and verify the identity locally and inside
   the node. The image is never pushed to a registry.
5. Install/upgrade the generated chart through the pinned Helm (`v4.3.0`)
   using the exact local image with `image.pullPolicy=Never`, the selected
   platform type, and a digest reference for the Feast operand image
   (resolved from the bundled overlay tag, textually distinct from it) so
   static image substitution is observable.
6. Wait for the module rollout, verify the running image identity, and run
   the e2e suite (`test-e2e-run`) with the same kubeconfig, namespace, and
   repository ldflags, with all Kind-only assertions enabled.

Notes:

- Provider behavior: an explicitly supplied `KIND_EXPERIMENTAL_PROVIDER` is
  preserved; otherwise an existing cluster keeps the engine that owns it
  (never silently switched), and a new cluster prefers Podman over Docker.
  If the cluster exists in more than one available engine and no provider
  was supplied, the run fails and asks for an explicit provider.
- The namespace, Helm release, and fixtures are marked with the
  `e2e.feast.dev/owner` annotation. Existing installations without
  matching ownership block the run instead of being adopted.
- Reruns are sequential and safe: each run builds a new unique image tag
  (refreshing the module) and reuses the existing TLS Secret/CA pair after
  validation, never replacing it.
- A failed run preserves the suite-owned `FeastOperator` singleton, the
  operand resources, and the marked `odh-data-config` state for
  inspection, after collecting diagnostics (events, pod status, image
  identity, manager logs). The next authorized run recovers the recorded
  fixture state through the ownership markers and still executes teardown
  coverage. Unknown or conflicting ownership fails closed.
- The suite verifies the module Deployment/Pod carry no `copy-manifests`
  init container or `manifests` emptyDir/mount, keep a read-only manager
  root filesystem with `ODH_MODULE_OPERATOR_MANIFESTS_PATH=/manifests`,
  project the substituted Feast image into the operand, observe OIDC
  issuer set/update/removal, and retain image replacements and live OIDC
  values across a module restart.

Standalone suite (operator already deployed through any means):

```bash
make test-e2e-run OPERATOR_NAMESPACE=opendatahub-feast-system
```

Standalone runs keep `E2E_KIND_CHECKS=false` and the existing core suite;
the Kind-only assertions are explicitly reported as skipped. Opt in with
`E2E_KIND_CHECKS=true E2E_KIND_CLUSTER=<cluster> E2E_EXPECTED_FEAST_IMAGE=<digest-ref>`
(and `KUBECONFIG` pointing at the cluster); missing inputs fail the run
instead of skipping.

Cluster lifecycle:

```bash
make test-e2e-kind-setup  # create (or reuse) and configure the cluster only
make test-e2e-kind-delete # delete only the dedicated cluster
```

Limitations:

- Kind e2e is not an OpenShift certification test: the compatibility CRDs
  and static TLS fixtures provide API storage/validation only; routing,
  service-ca rotation, and Prometheus reconciliation are not implemented.
- Known metrics limitation: framework namespace overrides happen after
  Kustomize replacements, so RHOAI ServiceMonitor DNS fields may retain the
  overlay namespace. Without Prometheus reconciliation this suite does not
  certify functional metrics TLS discovery.
- Known RHOAI limitation: the committed RHOAI overlay
  (`config/manifests/feastoperator/overlays/rhoai/manager_patch.yaml`) passes
  `--metrics-cert-path` to the operand manager, a flag the pinned upstream
  `feastdev/feast-operator` image does not define, so the operand crash-loops
  and `make test-e2e ODH_PLATFORM_TYPE=rhoai` fails at readiness. This is a
  pre-existing overlay bug unrelated to the Kind workflow; the ODH variant is
  the supported e2e path until the overlay is fixed (the operand's own
  `--metrics-bind-address=:8443` secure metrics needs no cert mount).


### Operator Chaos Validation

This repository uses [operator-chaos](https://github.com/opendatahub-io/operator-chaos)
for shift-left upgrade validation. On every pull request that modifies `api/`,
`internal/controller/`, `config/`, or `knowledge`, automated checks validate:

- Breaking changes in the knowledge model (`chaos/knowledge/feast.yaml`)
- Breaking changes in the `FeastOperator` CRD schema
- Upgrade simulation (dry-run mode)

The validation runs automatically via GitHub Actions. See `.github/workflows/operator-chaos.yml`
for implementation details.

### Build Container Image

```bash
make container-build IMG=quay.io/opendatahub/feast-module-operator:dev
```

### Generate Helm Chart

```bash
make helm
```

`config/chart` is committed because `opendatahub-operator` consumes it through
`manifests-config.yaml`. It is rendered and applied by the platform; it is not
installed with `helm install`.

If you install it with Helm, prefer a dedicated release namespace. The chart
uses the same object identities as `kustomize build config/default`, so a
namespace populated by Kustomize needs a complete ownership migration rather
than partial deletion. The chart manages these overlapping resources:

```text
Deployment/opendatahub-feast-operator
ConfigMap/odh-data-config
ServiceAccount/opendatahub-feast-operator
Service/opendatahub-feast-metrics-service
Role/opendatahub-feast-leader-election-role
RoleBinding/opendatahub-feast-leader-election-rolebinding
NetworkPolicy/allow-egress
NetworkPolicy/allow-metrics-traffic
ClusterRole/opendatahub-feast-components-feastoperator-admin-role
ClusterRole/opendatahub-feast-components-feastoperator-editor-role
ClusterRole/opendatahub-feast-components-feastoperator-viewer-role
ClusterRole/opendatahub-feast-manager-role
ClusterRole/opendatahub-feast-metrics-auth-role
ClusterRole/opendatahub-feast-metrics-reader
ClusterRoleBinding/opendatahub-feast-manager-rolebinding
ClusterRoleBinding/opendatahub-feast-metrics-auth-rolebinding
```

Helm 4.2 or newer can adopt the existing objects in place with
`--take-ownership`, which keeps `allow-metrics-traffic` active throughout the
migration:

```bash
release=opendatahub-feast-operator
namespace=opendatahub-feast-system
helm upgrade --install "$release" config/chart \
  --namespace "$namespace" \
  --create-namespace \
  --take-ownership \
  --set-string namespace="$namespace"
```

Before adopting, verify every listed object is the Feast operator installation
in that namespace and is not independently managed or shared with another
controller. `--take-ownership` adds Helm ownership metadata to all overlapping
objects, and a later `helm uninstall` deletes them. The namespace and CRD are
not adopted this way: Helm can install into the existing namespace, and an
existing CRD from `config/crd/bases` is reused because the chart declares it
under `crds/`. Do not delete any listed objects first.

## Architecture

### Reconciliation Flow

```
FeastOperator CR (v1) created by ODH Operator
  │
  ├── initialize: resolve manifest path from ODH_MODULE_OPERATOR_MANIFESTS_PATH
  ├── setKustomizedParams: write OIDC_ISSUER_URL into params.env
  ├── migrateDeploymentSelector: handle stale selector migration
  ├── upgradeIfNeeded: handle version transitions
  ├── deploy: render kustomize manifests and apply to cluster
  └── reportStatus: update CR conditions (Ready/Degraded)
```

### Related Images

| Environment Variable | Purpose |
|---------------------|---------|
| `RELATED_IMAGE_FEAST_OPERATOR` | feast-operator controller-manager image |
| `RELATED_IMAGE_FEATURE_SERVER` | feast feature-server sidecar image |
| `RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE` | ODH/RHOAI kube-rbac-proxy image for feast-operator workload sidecars |

## License

Apache License 2.0 — see [LICENSE](LICENSE).
