#!/usr/bin/env bash
set -euo pipefail

# classify-e2e-failure.sh <platform> <evidence-file>
#
# The only failure this repository tolerates temporarily is the documented
# upstream RHOAI operand failure: quay.io/feastdev/feast-operator:0.68.0 exits
# because it does not define --metrics-cert-path. The caller keeps the E2E step
# and job visibly failed; this script only decides whether the aggregate may
# treat that failure as the known, non-blocking one.

platform="${1:-}"
evidence="${2:-}"

if [ "$platform" != "rhoai" ]; then
  echo "unexpected-platform"
  exit 0
fi

if [ -z "$evidence" ] || [ ! -s "$evidence" ]; then
  echo "missing-evidence"
  exit 0
fi

allowed_failed_leaf="TestFeastOperator/foundation/should become ready"
failed_leaf_count="$(grep -Fc 'failed leaf: ' "$evidence" || true)"
allowed_leaf_count="$(grep -Fxc "failed leaf: ${allowed_failed_leaf}" "$evidence" || true)"

if grep -Fq 'operand deployment feast-operator-controller-manager' "$evidence" &&
   grep -Eq 'flag provided but not defined: -metrics-cert-path([[:space:]]|$)' "$evidence" &&
   [ "$failed_leaf_count" -eq 1 ] &&
   [ "$allowed_leaf_count" -eq 1 ]; then
  echo "expected-upstream-metrics-cert-path"
  exit 0
fi

echo "unexpected"
