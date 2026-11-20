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
