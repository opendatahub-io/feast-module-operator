#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

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

CHART_DIR=config/chart
chart_drift_supported
CHART_DIR=config/chart/
chart_drift_supported
CHART_DIR=config/alternate-chart
if chart_drift_supported; then
  echo 'alternate chart directory was accepted for drift regeneration' >&2
  exit 1
fi
CHART_DIR=config/chart

if listing_has_manifest_file <<< 'manifests/feastoperator/'; then
  echo 'directory-only manifest entry was accepted' >&2
  exit 1
fi
if ! listing_has_manifest_file <<< 'manifests/feastoperator/default/kustomization.yaml'; then
  echo 'manifest file under manifests/feastoperator/ was rejected' >&2
  exit 1
fi

mkdir -p "$tmp/bin"
call_log="$tmp/e2e-calls.log"
cat > "$tmp/bin/kind" <<'EOF'
#!/usr/bin/env bash
printf 'provider=%s args=%s\n' "${KIND_EXPERIMENTAL_PROVIDER:-}" "$*" >> "$CALL_LOG"
EOF
cat > "$tmp/bin/podman" <<'EOF'
#!/usr/bin/env bash
printf 'container args=%s\n' "$*" >> "$CALL_LOG"
EOF
chmod +x "$tmp/bin/kind" "$tmp/bin/podman"
PATH="$tmp/bin:$PATH"
export CALL_LOG="$call_log"
CONTAINER_TOOL=podman
E2E_CLUSTER_NAME=owned-cluster
E2E_REGISTRY_NAME=owned-registry
E2E_KEEP_CLUSTER=0

: > "$call_log"
kind_cmd get clusters >/dev/null
grep -Fq 'provider=podman args=get clusters' "$call_log"

: > "$call_log"
E2E_CLUSTER_CREATED=0
E2E_REGISTRY_CREATED=0
e2e_teardown >/dev/null
if [ -s "$call_log" ]; then
  echo 'teardown touched resources it did not create' >&2
  exit 1
fi

: > "$call_log"
E2E_CLUSTER_CREATED=1
E2E_REGISTRY_CREATED=1
E2E_REGISTRY_CONTAINER_ID=owned-container-id
e2e_teardown >/dev/null
grep -Fq 'provider=podman args=delete cluster --name owned-cluster' "$call_log"
grep -Fq 'container args=rm -f owned-container-id' "$call_log"
E2E_REGISTRY_CONTAINER_ID=

: > "$call_log"
E2E_KEEP_CLUSTER=1
e2e_teardown >/dev/null
if [ -s "$call_log" ]; then
  echo 'E2E_KEEP_CLUSTER still deleted owned resources' >&2
  exit 1
fi
E2E_KEEP_CLUSTER=0

mkdir -p "$tmp/konflux/.tekton"
cat > "$tmp/konflux/.tekton/test-pull-request.yaml" <<'EOF'
apiVersion: tekton.dev/v1
kind: PipelineRun
spec:
  params:
    - name: dockerfile
      value: "Containerfile operator"
    - name: path-context
      value: "."
EOF
target_output="$(
  cd "$tmp/konflux"
  CONTAINERFILE=
  BUILD_CONTEXT=
  KONFLUX_DOCKERFILE=
  KONFLUX_PATH_CONTEXT=
  KONFLUX_TARGET=
  resolve_konflux_target
  printf '%s|%s|%s\n' "$KONFLUX_DOCKERFILE" "$KONFLUX_PATH_CONTEXT" "$CONTAINERFILE"
)"
if [[ "$target_output" != 'Containerfile operator|.|Containerfile operator' ]]; then
  printf 'spaced PipelineRun parameters were not preserved: %s\n' "$target_output" >&2
  exit 1
fi

image_repo='localhost:5001/konflux-sim/feast-module-operator'
image_ref="${image_repo}:e678dbb3dcd8"
image_digest="$(printf 'a4d595f8%.0s' {1..8})"
image_digest_upper="$(printf '%s' "$image_digest" | tr '[:lower:]' '[:upper:]')"
is_resolved_pr_image "$image_ref" "$image_repo" "$image_ref"
is_resolved_pr_image "$image_ref" "$image_repo" "$image_repo@sha256:${image_digest}"
if is_resolved_pr_image "$image_ref" "$image_repo" "$image_repo@sha256:${image_digest:0:12}"; then
  echo 'truncated digest was accepted as the PR image' >&2
  exit 1
fi
if is_resolved_pr_image "$image_ref" "$image_repo" "$image_repo@sha256:${image_digest_upper}"; then
  echo 'uppercase digest was accepted as the PR image' >&2
  exit 1
fi
if is_resolved_pr_image "$image_ref" "$image_repo" 'quay.io/opendatahub/odh-feast-module-operator:odh-stable'; then
  echo 'default operator image was accepted as the PR image' >&2
  exit 1
fi

printf 'validate-build helper tests passed\n'
