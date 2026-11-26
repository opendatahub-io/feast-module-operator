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
  E2E_RHOAI=success
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

run_summary all-pass
grep -Fq "All blocking validations passed." "$tmp/all-pass.out"

expect_failure odh-failure E2E_ODH=failure
grep -Fq "Operator e2e (odh) | failure" "$tmp/odh-failure.out"

expect_failure rhoai-failure E2E_RHOAI=failure
grep -Fq "Operator e2e (rhoai) | failure" "$tmp/rhoai-failure.out"

expect_failure static-failure STATIC=failure

run_summary skipped SKIP=true REASON="documentation only"
grep -Fq "Validation not required: documentation only" "$tmp/skipped.out"

expect_failure skip-check-failure CHECK_SKIP=failure

echo "pr-validation summary tests passed"
