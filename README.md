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

### Data Connect Hub capability

The same Data module can also install `dc-controller` and create its namespaced
`DataConnectService` application CR. DCH is a sibling capability of Feature Store
and Data Registry; it does not introduce another Platform module or module CR.

```yaml
spec:
  capabilities:
    featureStore:
      managementState: Removed
    dataRegistry:
      managementState: Removed
    dataConnectHub:
      managementState: Managed
      gateway:
        name: odh-gateway
        namespace: opendatahub
      tokenReviewAudiences:
        - https://kubernetes.default.svc
```

The platform projects the real Gateway and service-account audiences; the values
above are illustrative. Omitting `dataConnectHub` leaves DCH disabled, including
for legacy module CRs. Existing unconditional Feast operator installation remains
unchanged when DCH is the only managed capability.

An administrator must provision PostgreSQL and `dch-database-config` (containing
`secret-config.toml`) in the applications namespace. This module does not create
the database or its credentials. Missing prerequisites keep DCH not ready.

The module forwards `platformVersion` from `odh-data-config` into the owned
`opendatahub-dataconnecthub-config` application adapter. Gateway and audience
settings are projected into `DataConnectService.spec`; application sizing,
tracing, and connector overrides are not managed by the module. Existing unowned
applications, ConfigMaps, and controller resources cause an ownership conflict
rather than being silently adopted.

On DCH removal, the application is deleted first and its finalizers must complete
before the controller/RBAC can be removed. Application CRDs and the shared
namespace are retained. DCH readiness and its platform-version acknowledgment
contribute to the single `FeastOperator` module status.

`make get-manifests` fetches both controller bundles at pinned revisions. Set
`DCH_SOURCE_DIR` to a local DCH checkout to test local controller manifests.
The platform must supply controller, REST, Flight, and proxy `RELATED_IMAGE_*`
values; enabled DCH fails closed if any is absent rather than using an upstream
image accidentally in a downstream/disconnected installation.

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
