#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
summary="$repo_root/scripts/pr-validation-summary.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

base_env=(
  CHECK_SKIP=success
  SKIP=false
  STATIC=success
  HERMETIC=success
  MANIFESTS=success
  CHART=success
  IMAGE=success
  E2E_ODH=success
)

run_summary() {
  local name="$1"
  shift
  if env "${base_env[@]}" "$@" "$summary" > "$tmp/$name.out" 2>&1; then
    echo "ok - $name"
  else
    echo "not ok - $name" >&2
    cat "$tmp/$name.out" >&2
    return 1
  fi
}

expect_failure() {
  local name="$1"
  shift
  if env "${base_env[@]}" "$@" "$summary" > "$tmp/$name.out" 2>&1; then
    echo "not ok - $name unexpectedly passed" >&2
    cat "$tmp/$name.out" >&2
    return 1
  fi
  echo "ok - $name"
}

run_summary expected-rhoai-failure \
  E2E_RHOAI_OUTCOME=failure \
  E2E_RHOAI_CLASSIFICATION=expected-upstream-metrics-cert-path
grep -Fq "All blocking validations passed." "$tmp/expected-rhoai-failure.out"
grep -Fq "Operator e2e (rhoai) | failure (expected upstream --metrics-cert-path signature; non-blocking)" "$tmp/expected-rhoai-failure.out"
grep -Fq "flag provided but not defined: -metrics-cert-path" "$tmp/expected-rhoai-failure.out"
if grep -Fq "All validations passed." "$tmp/expected-rhoai-failure.out"; then
  echo "not ok - summary claimed all validations passed" >&2
  exit 1
fi

expect_failure odh-failure \
  E2E_ODH=failure \
  E2E_RHOAI_OUTCOME=failure \
  E2E_RHOAI_CLASSIFICATION=expected-upstream-metrics-cert-path
expect_failure rhoai-unexpected-success \
  E2E_RHOAI_OUTCOME=success \
  E2E_RHOAI_CLASSIFICATION=expected-upstream-metrics-cert-path
grep -Fq "unexpectedly passed" "$tmp/rhoai-unexpected-success.out"
expect_failure rhoai-not-run E2E_RHOAI_OUTCOME=not-run
expect_failure rhoai-skipped E2E_RHOAI_OUTCOME=skipped
expect_failure rhoai-missing-classification E2E_RHOAI_OUTCOME=failure
grep -Fq "classification: missing-evidence" "$tmp/rhoai-missing-classification.out"
expect_failure rhoai-missing-sa E2E_RHOAI_OUTCOME=failure E2E_RHOAI_CLASSIFICATION=unexpected
grep -Fq "classification: unexpected" "$tmp/rhoai-missing-sa.out"
expect_failure rhoai-unexpected-classification \
  E2E_RHOAI_OUTCOME=failure \
  E2E_RHOAI_CLASSIFICATION=unexpected-platform

echo "pr-validation summary tests passed"
