#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# shellcheck disable=SC1091
source "$repo_root/scripts/validate-build.sh"

actual="$(printf '%s\n' "config/chart/values.yaml" | format_tolerated_drift origin/main)"
expected='   tolerated (already stale on origin/main): config/chart/values.yaml'
if [[ "$actual" != "$expected" ]]; then
  printf 'unexpected tolerated-drift output:\n%s\n' "$actual" >&2
  exit 1
fi

actual="$(printf '%s\n' "config/chart/a&b.yaml" | format_tolerated_drift "refs/heads/release/1.0")"
expected='   tolerated (already stale on refs/heads/release/1.0): config/chart/a&b.yaml'
if [[ "$actual" != "$expected" ]]; then
  printf 'special characters were not preserved:\n%s\n' "$actual" >&2
  exit 1
fi

image_repo='localhost:5001/konflux-sim/feast-module-operator'
image_ref="${image_repo}:e678dbb3dcd8"
is_resolved_pr_image "$image_ref" "$image_repo" "$image_ref"
is_resolved_pr_image "$image_ref" "$image_repo" "$image_repo@sha256:a4d595f8d97e"
if is_resolved_pr_image "$image_ref" "$image_repo" 'quay.io/opendatahub/odh-feast-module-operator:odh-stable'; then
  echo 'default operator image was accepted as the PR image' >&2
  exit 1
fi

printf 'validate-build helper tests passed\n'
