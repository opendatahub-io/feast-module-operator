#!/usr/bin/env bash
set -euo pipefail

check_skip="${CHECK_SKIP:-}"
skip="${SKIP:-}"
reason="${REASON:-}"
static="${STATIC:-}"
hermetic="${HERMETIC:-}"
manifests="${MANIFESTS:-}"
chart="${CHART:-}"
image="${IMAGE:-}"
e2e_odh="${E2E_ODH:-}"
e2e_rhoai="${E2E_RHOAI:-}"

echo "## Konflux Build Simulation Results"
echo ""

if [ "$check_skip" != "success" ]; then
  echo "❌ Skip evaluation failed (${check_skip:-no result}); refusing to report success."
  exit 1
fi

if [ "$skip" = "true" ]; then
  echo "✅ Validation not required: ${reason:-explicit skip}"
  exit 0
fi

failed=false

echo "| Check | Result |"
echo "|-------|--------|"
echo "| Static validation | ${static:-missing} |"
echo "| Hermetic preflight | ${hermetic:-missing} |"
echo "| Manifests | ${manifests:-missing} |"
echo "| Helm chart | ${chart:-missing} |"
echo "| Container image | ${image:-missing} |"
echo "| Operator e2e (odh) | ${e2e_odh:-missing} |"
echo "| Operator e2e (rhoai) | ${e2e_rhoai:-missing} |"
echo ""

for result in "$static" "$hermetic" "$manifests" "$chart" "$image" "$e2e_odh" "$e2e_rhoai"; do
  if [ "$result" != "success" ]; then
    failed=true
  fi
done

if [ "$failed" = "true" ]; then
  echo "❌ Blocking validations failed. Check the individual job logs above."
  exit 1
fi

echo "✅ All blocking validations passed."
