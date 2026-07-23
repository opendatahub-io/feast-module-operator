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

## License

Apache License 2.0 — see [LICENSE](LICENSE).
