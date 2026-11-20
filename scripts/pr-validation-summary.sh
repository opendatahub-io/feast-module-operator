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
e2e_rhoai_outcome="${E2E_RHOAI_OUTCOME:-not-run}"

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
case "$e2e_rhoai_outcome" in
  failure)
    e2e_rhoai="failure (expected; non-blocking)"
    ;;
  success)
    e2e_rhoai="success (unexpected)"
    failed=true
    ;;
  skipped | cancelled | not-run | "")
    e2e_rhoai="${e2e_rhoai_outcome:-not-run} (expected failure required)"
    failed=true
    ;;
  *)
    e2e_rhoai="unknown outcome"
    failed=true
    ;;
esac

echo "| Check | Result |"
echo "|-------|--------|"
echo "| Static validation | ${static:-missing} |"
echo "| Hermetic preflight | ${hermetic:-missing} |"
echo "| Manifests | ${manifests:-missing} |"
echo "| Helm chart | ${chart:-missing} |"
echo "| Container image | ${image:-missing} |"
echo "| Operator e2e (odh) | ${e2e_odh:-missing} |"
echo "| Operator e2e (rhoai) | ${e2e_rhoai} |"
echo ""

for result in "$static" "$hermetic" "$manifests" "$chart" "$image" "$e2e_odh"; do
  if [ "$result" != "success" ]; then
    failed=true
  fi
done

if [ "$failed" = "true" ]; then
  echo "❌ Blocking validations failed, or the expected-failure leg did not fail. Check the individual job logs above."
  if [ "$e2e_rhoai_outcome" = "success" ]; then
    echo "❌ Operator e2e (rhoai) unexpectedly passed; remove the expected-failure handling before merging."
  fi
  exit 1
fi

echo "✅ All blocking validations passed."
echo "⚠️ Operator e2e (rhoai) failed as expected (non-blocking until opendatahub-io/feast#312 and the vendored manifest pin move)."
