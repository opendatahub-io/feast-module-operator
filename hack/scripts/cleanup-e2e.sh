#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

NAMESPACE="${1:-opendatahub-feast-system}"
HELM_RELEASE="${2:-opendatahub-feast-operator}"
CR_RESOURCE="feastoperators.components.platform.opendatahub.io"

echo "Cleaning up e2e test resources..."

# Delete component CRs first and wait for them to disappear before Helm or CRD removal.
kubectl delete "${CR_RESOURCE}" --all --ignore-not-found 2>/dev/null || true
kubectl wait --for=delete "${CR_RESOURCE}" --all --timeout=60s 2>/dev/null || true

# Uninstall Helm release (removes operator Deployment, RBAC, CRD, etc.)
go run helm.sh/helm/v4/cmd/helm@v4.2.0 uninstall "${HELM_RELEASE}" \
    --namespace "${NAMESPACE}" \
    --ignore-not-found \
    --wait 2>/dev/null || true

# Delete namespace
kubectl delete namespace "${NAMESPACE}" --ignore-not-found 2>/dev/null || true

# Delete any leftover cluster-scoped resources
kubectl delete clusterroles -l platform.opendatahub.io/part-of=feastoperator --ignore-not-found 2>/dev/null || true
kubectl delete clusterrolebindings -l platform.opendatahub.io/part-of=feastoperator --ignore-not-found 2>/dev/null || true

# Delete CRD if still present (Helm should have removed it)
kubectl delete crd "${CR_RESOURCE}" --ignore-not-found 2>/dev/null || true

echo "E2E test cleanup complete."
