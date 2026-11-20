#!/usr/bin/env bash
set -euo pipefail

# Konflux PR build simulator for opendatahub-io/feast-module-operator.
#
# One Konflux pull-request build:
#   .tekton/odh-feast-module-operator-pull-request.yaml
#   Containerfile, path-context ".", output quay.io/opendatahub/odh-feast-module-operator
#
# A `go build` does not prove what this repository actually ships. The image
# carries the operand manifests under a fixed path, and the Helm chart in
# config/chart is generated from config/default by `make helm`. Both are
# contracts between files that no compiler checks.
#
# Usage:
#   scripts/validate-build.sh <static|hermetic|manifests|chart|image|e2e|all>
#   scripts/validate-build.sh builder-go   # the Go version the builder image pins
#   scripts/validate-build.sh target       # dockerfile/context from the PipelineRun
#
# The dockerfile and build context are read from the Konflux PipelineRun, not
# hardcoded here, so the simulation cannot validate a different target from the
# one Konflux builds.
#
# Environment:
#   IMAGE                Local tag for the operator image
#   CONTAINER_TOOL       docker or podman (default docker)
#   PLATFORM             Docker platform to build (default linux/amd64)
#   HELM_VERSION         Helm used for rendering (default v4.2.0)
#   KUSTOMIZE_VERSION    kustomize used for rendering (default v5.8.1)
#   HELM_BIN             Prebuilt helm binary (skips the `go run` fallback)
#   KUSTOMIZE_BIN        Prebuilt kustomize binary (skips the `go run` fallback)
#   KUBECONFORM_BIN      kubeconform binary (default kubeconform)
#   KUBERNETES_VERSION   Kubernetes schema version for kubeconform
#   CHART_DRIFT_BASE_REF Base ref used to tell pre-existing drift from drift a
#                        PR introduces (for example origin/main). Without it any
#                        drift fails, which is the safe default.
#   SKIP_CHART_DRIFT=1   Skip the regenerate-and-compare step
#   SKIP_IMAGE=1         Skip the image build
#   E2E_PLATFORMS        Platform types the e2e mode deploys (default "odh rhoai")
#   E2E_EXPECTED_IMAGE   Image reference the live operator Deployment must run
#   E2E_CLUSTER_NAME     Kind cluster name (default konflux-sim)
#   E2E_REGISTRY_NAME    Local registry container name (default konflux-sim-registry)
#   E2E_REGISTRY_PORT    Local registry port on 127.0.0.1 (default 5001)

MODE="${1:-all}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Set by resolve_konflux_target from .tekton/*pull-request.yaml.
CONTAINERFILE=""
BUILD_CONTEXT=""
KONFLUX_DOCKERFILE=""
KONFLUX_PATH_CONTEXT=""
KONFLUX_TARGET=""
CHART_DIR="${CHART_DIR:-config/chart}"
IMAGE="${IMAGE:-quay.io/opendatahub/odh-feast-module-operator:pr-validation}"
CONTAINER_TOOL="${CONTAINER_TOOL:-docker}"
PLATFORM="${PLATFORM:-linux/amd64}"
HELM_VERSION="${HELM_VERSION:-v4.2.0}"
KUSTOMIZE_VERSION="${KUSTOMIZE_VERSION:-v5.8.1}"
HELM_BIN="${HELM_BIN:-}"
KUSTOMIZE_BIN="${KUSTOMIZE_BIN:-}"
KUBECONFORM_BIN="${KUBECONFORM_BIN:-kubeconform}"
KUBERNETES_VERSION="${KUBERNETES_VERSION:-1.31.0}"
MANIFEST_CONTRACT="${MANIFEST_CONTRACT:-scripts/manifest_contract.py}"

info() { printf 'ℹ️  %s\n' "$*"; }
pass() { printf '✅ %s\n' "$*"; }
warn() { printf '⚠️  %s\n' "$*"; }
err()  { printf '❌ %s\n' "$*" >&2; }

format_tolerated_drift() {
  local base_ref="$1"
  awk -v base="$base_ref" '{ print "   tolerated (already stale on " base "): " $0 }'
}

is_resolved_pr_image() {
  local image_ref="$1" image_repo="$2" resolved="$3"
  case "$resolved" in
    "$image_ref" | "$image_repo"@sha256:*) return 0 ;;
    *) return 1 ;;
  esac
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "required command not found: $1"
    exit 1
  fi
}

# verify_sha256 <expected> <file>
#
# macOS ships a /sbin/sha256sum that has no --check, so compare the digest
# directly instead of depending on GNU coreutils.
verify_sha256() {
  local expected="$1" file="$2" actual

  if command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$file" | awk '{print $1}')"
  elif command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$file" | awk '{print $1}')"
  else
    err "no SHA-256 tool available (need shasum or sha256sum)"
    return 1
  fi

  if [ "$actual" != "$expected" ]; then
    err "SHA-256 mismatch for $(basename "$file"): expected $expected, got $actual"
    return 1
  fi
}

run_kustomize() {
  local dir="$1"
  if [ -n "$KUSTOMIZE_BIN" ]; then
    "$KUSTOMIZE_BIN" build "$dir"
  elif command -v kustomize >/dev/null 2>&1; then
    kustomize build "$dir"
  elif command -v go >/dev/null 2>&1; then
    # `go run` needs a writable GOCACHE and a reachable module proxy. Air-gapped
    # runs should set KUSTOMIZE_BIN instead of relying on this fallback.
    go run "sigs.k8s.io/kustomize/kustomize/v5@${KUSTOMIZE_VERSION}" build "$dir"
  else
    kubectl kustomize "$dir"
  fi
}

run_helm() {
  if [ -n "$HELM_BIN" ]; then
    "$HELM_BIN" "$@"
  elif command -v helm >/dev/null 2>&1; then
    helm "$@"
  else
    # Same caveat as run_kustomize: this fallback downloads modules.
    go run "helm.sh/helm/v4/cmd/helm@${HELM_VERSION}" "$@"
  fi
}

resolve_konflux_target() {
  # Cache: downstream helpers call this repeatedly.
  [ -n "$CONTAINERFILE" ] && return 0

  local -a files=()
  local file
  for file in .tekton/*pull-request.yaml; do
    [ -e "$file" ] && files+=("$file")
  done

  if [ "${#files[@]}" -eq 0 ]; then
    err "no .tekton/*pull-request.yaml found"
    exit 1
  fi
  if [ "${#files[@]}" -gt 1 ]; then
    err "found ${#files[@]} Konflux pull-request PipelineRuns; this simulator models exactly one"
    printf '   %s\n' "${files[@]}" >&2
    exit 1
  fi
  KONFLUX_TARGET="${files[0]}"

  read -r KONFLUX_DOCKERFILE KONFLUX_PATH_CONTEXT < <(python3 - "$KONFLUX_TARGET" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
params = {p.get("name"): p.get("value") for p in (doc.get("spec") or {}).get("params") or []}
print(params.get("dockerfile", "-"), params.get("path-context", "-"))
PY
)

  if [ "$KONFLUX_DOCKERFILE" = "-" ] || [ "$KONFLUX_PATH_CONTEXT" = "-" ]; then
    err "$KONFLUX_TARGET is missing a dockerfile or path-context param"
    exit 1
  fi

  BUILD_CONTEXT="${KONFLUX_PATH_CONTEXT%/}"
  [ -n "$BUILD_CONTEXT" ] || BUILD_CONTEXT="."
  CONTAINERFILE="${BUILD_CONTEXT%/}/${KONFLUX_DOCKERFILE}"
  CONTAINERFILE="${CONTAINERFILE#./}"
}

check_konflux_targets() {
  info "Checking Konflux PipelineRun targets..."
  resolve_konflux_target

  if [ ! -f "$CONTAINERFILE" ]; then
    err "$KONFLUX_TARGET points at $CONTAINERFILE, which does not exist"
    exit 1
  fi
  pass "$KONFLUX_TARGET -> $CONTAINERFILE (context $BUILD_CONTEXT)"

  local has_platforms
  has_platforms="$(python3 - "$KONFLUX_TARGET" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
params = {p.get("name") for p in (doc.get("spec") or {}).get("params") or []}
print("yes" if "build-platforms" in params else "no")
PY
)"
  if [ "$has_platforms" = "no" ]; then
    warn "$KONFLUX_TARGET does not set build-platforms; the shared pipeline falls back to linux/x86_64 only."
    warn "  A single-arch manifest list surfaces as ImagePullBackOff on ppc64le/arm64 (RHOAIENG-79767)."
  fi
}

builder_go_version() {
  resolve_konflux_target
  local version
  version="$(awk '/^FROM[[:space:]]+golang:/ {
      sub(/^FROM[[:space:]]+golang:/, "")
      sub(/[[:space:]].*$/, "")
      print
      exit
    }' "$CONTAINERFILE")"
  if [ -z "$version" ]; then
    err "$CONTAINERFILE has no golang builder image; cannot read the pinned Go version"
    exit 1
  fi
  printf '%s\n' "$version"
}

check_go_inputs() {
  info "Checking Go module inputs..."

  if [ ! -s go.sum ]; then
    err "go.sum is missing or empty; a hermetic build cannot verify the module set"
    exit 1
  fi
  pass "go.sum present"

  if grep -q '^replace ' go.mod; then
    warn "go.mod has replace directives; a prefetch has to resolve them, and go.sum alone does not describe them"
  fi
}

check_containerfile() {
  info "Checking the Containerfile..."
  local file="$CONTAINERFILE"

  if [ ! -f "$file" ]; then
    err "$file does not exist"
    exit 1
  fi

  local src
  while IFS= read -r src; do
    [ -n "$src" ] || continue
    [ "$src" = "." ] && continue
    # COPY sources are relative to the PipelineRun's path-context, not to the
    # repository root.
    if [ ! -e "${BUILD_CONTEXT%/}/${src}" ]; then
      err "$file copies $src, which is not in the build context ($BUILD_CONTEXT)"
      exit 1
    fi
  done < <(awk '
    /^COPY / {
      # COPY --from=<stage> reads from an earlier build stage, not the context.
      if ($0 ~ /--from=/) next
      n = 0
      for (i = 2; i <= NF; i++) {
        if ($i ~ /^--/) continue
        args[++n] = $i
      }
      # The final argument is the destination; everything before it is a source.
      for (i = 1; i < n; i++) print args[i]
    }' "$file")

  pass "every COPY source exists in the build context"

  if ! grep -qE '^COPY\s+(--from=\S+\s+)?\S*config/manifests/?\s+/' "$file"; then
    err "$file no longer copies config/manifests/ into the image; the operator would have no operands"
    exit 1
  fi
  pass "the image ships config/manifests/"

  local base
  while IFS= read -r base; do
    case "$base" in
      *:latest|*:latest@*)
        warn "base image $base is a floating tag; an unpinned base makes the build non-reproducible"
        ;;
    esac
  done < <(awk '/^FROM /{print $2}' "$file")

  if ! grep -q -- '-tags strictfipsruntime' "$file" && ! grep -q -- 'strictfipsruntime' "$file"; then
    warn "$file builds without -tags strictfipsruntime; RHOAI FIPS builds require it"
  fi
}

check_manifest_path_contract() {
  info "Checking the image/chart manifest path contract..."
  local rendered
  rendered="$(mktemp)"
  trap 'rm -f "${rendered:-}"' RETURN

  if ! run_helm template feast-contract "$CHART_DIR" --namespace validation > "$rendered"; then
    err "helm template failed for $CHART_DIR"
    exit 1
  fi

  if ! python3 "$MANIFEST_CONTRACT" manifest-path-contract "$CONTAINERFILE" "$rendered"; then
    err "the image and the chart disagree about where manifests live (see RHOAIENG-99568)"
    exit 1
  fi

  if ! python3 "$MANIFEST_CONTRACT" duplicate-env "$rendered"; then
    err "duplicate env var names in the rendered chart — the API server keeps only the last value"
    exit 1
  fi
  pass "manifest path contract holds"
}

# regenerate_chart <dest> <ref> <label> <tmp>
#
# Exports <ref> into <dest>, keeps a copy of the committed chart, and rebuilds
# it with `make helm`. Returns non-zero when the ref cannot be generated.
regenerate_chart() {
  local dest="$1" ref="$2" label="$3" tmp="$4"

  mkdir -p "$dest"

  if ! git archive "$ref" | tar -x -C "$dest"; then
    err "could not export $ref"
    return 1
  fi

  if [ -d "$dest/$CHART_DIR" ]; then
    cp -R "$dest/$CHART_DIR" "$tmp/$label-committed"
  else
    mkdir -p "$tmp/$label-committed"
  fi

  # Reuse a prebuilt kustomize when one was supplied; otherwise `make helm`
  # falls back to `go run`, which needs network on a cold module cache.
  local -a make_args=()
  if [ -n "$KUSTOMIZE_BIN" ]; then
    make_args+=("KUSTOMIZE=$KUSTOMIZE_BIN")
  fi

  # A generation failure is not a warning: it means the shipped chart cannot be
  # verified at all, and a green job would be claiming coverage it never had.
  if ! ( cd "$dest" && make helm ${make_args[@]+"${make_args[@]}"} > "$tmp/make-$label.log" 2>&1 ); then
    err "'make helm' failed on $ref; the chart cannot be verified"
    tail -30 "$tmp/make-$label.log" | sed 's/^/   /'
    return 1
  fi
}

# chart_drift_files <committed> <generated> <report>
#
# Writes a content diff to <report> and echoes the sorted list of chart-relative
# files that differ. The list is derived from checksum manifests rather than the
# diff text, so it can be compared between two runs without parsing YAML.
chart_drift_files() {
  local committed="${1%/}" generated="${2%/}" report="$3"
  local rel

  diff -r "$committed" "$generated" > "$report" 2>&1 || true

  # Compare content directly. A checksum manifest made the result depend on
  # sha256sum being installed (xargs exits 127 without it, both manifests come
  # out empty, nothing differs and real drift passes) and on how the tool
  # behaves on an empty directory.
  {
    ( cd "$committed" && find . -type f )
    ( cd "$generated" && find . -type f )
  } | LC_ALL=C sort -u | while IFS= read -r rel; do
    # A file present on one side only is drift, which cmp reports.
    cmp -s "$committed/$rel" "$generated/$rel" || printf '%s\n' "${rel#./}"
  done
}

check_chart_drift() {
  local tmp
  tmp="$(mktemp -d)"

  # Cleanup is explicit: a RETURN trap would fire when the helper functions
  # below return and remove the working directory underneath them.
  local rc=0
  run_chart_drift "$tmp" || rc=$?
  rm -rf "$tmp"

  return "$rc"
}

run_chart_drift() {
  local tmp="$1"

  info "Checking whether config/chart matches 'make helm' output..."

  if ! regenerate_chart "$tmp/head" HEAD head "$tmp"; then
    return 1
  fi

  local head_drift
  head_drift="$(chart_drift_files "$CHART_DIR" "$tmp/head/$CHART_DIR" "$tmp/head.diff")"

  if [ -z "$head_drift" ]; then
    pass "config/chart matches the generated output"
    return 0
  fi

  # Drift on HEAD is not automatically this PR's fault: the repository can be
  # behind on regeneration. Regenerate the base ref as well and compare the two
  # sets of drifted files. Only files this PR starts to drift are its fault.
  #
  # The comparison is a set difference, not string equality: a PR that
  # regenerates one of several stale files reduces the drift set, and that is
  # an improvement, not a regression.
  #
  # Residual gap: a file that is already stale on the base ref stays tolerated
  # even if this PR makes its drift worse. That is bounded by the base ref
  # being cleaned up (see the follow-up), and it is still strictly better than
  # blocking every PR on drift the repository already shipped.
  local base_ref="${CHART_DRIFT_BASE_REF:-}"
  if [ -z "$base_ref" ]; then
    err "CHART_DRIFT_BASE_REF is not set, so pre-existing drift cannot be told apart from drift this PR adds"
    head -30 "$tmp/head.diff" | sed 's/^/   /'
    err "  Set CHART_DRIFT_BASE_REF=<base ref> or run 'make helm' and commit the result."
    return 1
  fi

  if ! regenerate_chart "$tmp/base" "$base_ref" base "$tmp"; then
    err "could not regenerate $base_ref, so drift on HEAD cannot be attributed to this PR"
    return 1
  fi

  local base_drift
  base_drift="$(chart_drift_files "$tmp/base-committed" "$tmp/base/$CHART_DIR" "$tmp/base.diff")"

  local added_drift
  added_drift="$(LC_ALL=C comm -23 <(printf '%s\n' "$head_drift") <(printf '%s\n' "$base_drift"))"

  if [ -z "$added_drift" ]; then
    warn "config/chart already does not match 'make helm' output on $base_ref; this PR adds no drift:"
    printf '%s\n' "$head_drift" | format_tolerated_drift "$base_ref"
    warn "  config/chart is what manifests-config.yaml consumes (sourcePath: config/chart)."
    warn "  Tracked separately; this check fails as soon as $base_ref is clean again."
    return 0
  fi

  err "config/chart does not match 'make helm' output:"
  printf '%s\n' "$added_drift" | sed 's/^/   new drift: /'
  head -30 "$tmp/head.diff" | sed 's/^/   /'
  err "  config/chart is what manifests-config.yaml consumes (sourcePath: config/chart)."
  err "  Nothing else regenerates it; run 'make helm' and commit the result."
  return 1
}

validate_static() {
  check_konflux_targets
  check_go_inputs
  check_containerfile
  check_manifest_path_contract
  pass "Static validation passed"
}

validate_hermetic() {
  info "Proving the Go inputs build without network..."
  require_cmd go

  # Honour a caller-supplied toolchain. CI sets GOTOOLCHAIN=local after
  # installing the go.mod version, so a mismatch fails here instead of being
  # papered over by a mid-build toolchain download.
  local toolchain="${GOTOOLCHAIN:-auto}"

  local modcache
  modcache="$(mktemp -d)"
  trap 'chmod -R u+w "${modcache:-}" 2>/dev/null || true; rm -rf "${modcache:-}"' RETURN

  GOTOOLCHAIN="$toolchain" GOMODCACHE="$modcache" GOFLAGS="-mod=readonly" go mod download
  pass "module cache populated"

  GOTOOLCHAIN="$toolchain" GOMODCACHE="$modcache" GOPROXY=off GOFLAGS="-mod=readonly" \
    CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -o "$modcache/manager" ./cmd/main.go
  pass "cmd/main.go builds with GOPROXY=off"

  GOTOOLCHAIN="$toolchain" GOMODCACHE="$modcache" GOPROXY=off GOFLAGS="-mod=readonly" \
    CGO_ENABLED=0 GOOS=linux go build -buildvcs=false ./...
  pass "every package builds with GOPROXY=off"

  GOMODCACHE="$modcache" go mod verify
  pass "go mod verify"
}

validate_manifests() {
  info "Validating kustomize entry points..."
  require_cmd python3

  local out_dir
  out_dir="$(mktemp -d)"
  trap 'rm -rf "${out_dir:-}"' RETURN

  local dirs=(
    config/default
    config/manifests/feastoperator/default
    config/manifests/feastoperator/overlays/odh
    config/manifests/feastoperator/overlays/rhoai
    config/manifests/feastoperator/overlays/openshift
    config/rbac
    config/network-policy
    config/samples
  )

  local dir name
  for dir in "${dirs[@]}"; do
    name="$(printf '%s' "$dir" | tr '/' '_')"
    if run_kustomize "$dir" > "${out_dir}/${name}.yaml"; then
      pass "kustomize build ${dir}"
    else
      err "kustomize build failed for ${dir}"
      exit 1
    fi
  done

  info "Checking rendered manifests for duplicate env var names..."
  if ! python3 "$MANIFEST_CONTRACT" duplicate-env "${out_dir}"/*.yaml; then
    err "duplicate env var names in rendered manifests"
    exit 1
  fi
  pass "no duplicate env var names"

  info "Checking that RBAC bindings name a ServiceAccount that exists..."
  if ! python3 "$MANIFEST_CONTRACT" rbac-subjects "${out_dir}"/*.yaml; then
    err "an RBAC binding points at a ServiceAccount the manifests never create (RHOAIENG-100514)"
    exit 1
  fi
  pass "RBAC subjects resolve"

  if ! command -v "$KUBECONFORM_BIN" >/dev/null 2>&1; then
    warn "kubeconform not installed; skipping schema validation"
    return
  fi

  local file status
  for file in "${out_dir}"/*.yaml; do
    # -verbose records skipped resources in the JSON; without it a built-in
    # kind with no schema disappears from the report instead of being caught.
    # A schema violation (statusInvalid) exits nonzero: that must fail the
    # build, not be swallowed by `|| true`.
    status=0
    "$KUBECONFORM_BIN" -strict -verbose -ignore-missing-schemas \
      -kubernetes-version "$KUBERNETES_VERSION" -output json "$file" \
      > "${file}.json" 2> "${file}.err" || status=$?

    if [ "$status" -ne 0 ]; then
      err "kubeconform rejected $(basename "$file") (exit $status):"
      if [ -s "${file}.err" ]; then
        sed 's/^/   /' "${file}.err" >&2
      fi
      if [ -s "${file}.json" ]; then
        sed 's/^/   /' "${file}.json" >&2
      fi
      exit 1
    fi

    if [ ! -s "${file}.json" ]; then
      err "kubeconform produced no JSON report for $(basename "$file")"
      exit 1
    fi

    local bad
    bad="$(python3 - "${file}.json" <<'PY'
import json, sys
# The standalone schema bundle ships no schema for these built-in kinds, so a
# skip is expected and is not an apiVersion/kind typo.
EXPECTED_SKIPS = {("apiextensions.k8s.io", "CustomResourceDefinition")}
bad = []
for r in json.load(open(sys.argv[1])).get("resources") or []:
    if r.get("status") != "statusSkipped":
        continue
    version = r.get("version", "")
    group = version.split("/")[0] if "/" in version else ""
    if (group, r.get("kind")) in EXPECTED_SKIPS:
        continue
    if group == "" or group.endswith(".k8s.io") or group in {"apps", "batch", "policy", "autoscaling", "extensions"}:
        bad.append(f"  {r.get('kind')} ({r.get('version')})")
print("\n".join(bad))
PY
)"
    if [ -n "$bad" ]; then
      err "built-in kind(s) skipped in $(basename "$file") — likely an apiVersion/kind typo:"
      printf '%s\n' "$bad"
      exit 1
    fi
  done
  pass "kubeconform schema validation passed"
}

validate_chart() {
  info "Rendering the Helm chart..."
  require_cmd python3

  local rendered
  rendered="$(mktemp)"
  trap 'rm -f "${rendered:-}"' RETURN

  if ! run_helm template feast-validation "$CHART_DIR" --namespace validation --include-crds > "$rendered"; then
    err "helm template failed for $CHART_DIR"
    exit 1
  fi
  pass "helm template rendered $CHART_DIR"

  local kind
  for kind in Deployment ClusterRole ClusterRoleBinding ServiceAccount NetworkPolicy; do
    if ! grep -q "^kind: ${kind}$" "$rendered"; then
      err "the chart no longer renders a ${kind}; the platform expects it"
      exit 1
    fi
  done
  pass "expected resources are present"

  if ! grep -q '^kind: CustomResourceDefinition$' "$rendered"; then
    warn "the chart renders no CRD even with --include-crds; check $CHART_DIR/crds"
  fi

  if ! python3 "$MANIFEST_CONTRACT" rbac-subjects "$rendered"; then
    err "the chart binds RBAC to a ServiceAccount it does not create; the operator will crash-loop on startup (RHOAIENG-100514)"
    exit 1
  fi
  pass "chart RBAC subjects resolve"

  if [ "${SKIP_CHART_DRIFT:-0}" != "1" ]; then
    check_chart_drift
  fi
}

validate_image() {
  resolve_konflux_target
  info "Building the Konflux container image with ${CONTAINER_TOOL}..."
  require_cmd "$CONTAINER_TOOL"

  "$CONTAINER_TOOL" build --platform "$PLATFORM" -f "$CONTAINERFILE" -t "$IMAGE" "$BUILD_CONTEXT"

  "$CONTAINER_TOOL" run --rm --platform "$PLATFORM" "$IMAGE" --help >/dev/null
  pass "the manager binary starts and prints usage"

  "$CONTAINER_TOOL" run --rm --platform "$PLATFORM" "$IMAGE" operator --help >/dev/null
  pass "the operator subcommand is wired"

  local container="feast-module-operator-sim-$$"
  "$CONTAINER_TOOL" create --platform "$PLATFORM" --name "$container" "$IMAGE" >/dev/null
  trap '"$CONTAINER_TOOL" rm -f "${container:-}" >/dev/null 2>&1 || true' RETURN

  local listing
  listing="$("$CONTAINER_TOOL" export "$container" | tar -tf - 2>/dev/null || true)"
  "$CONTAINER_TOOL" rm -f "$container" >/dev/null 2>&1 || true

  if ! printf '%s\n' "$listing" | grep -q '^manifests/feastoperator/'; then
    err "the image does not contain manifests/feastoperator/; the operator would start with no overlays"
    exit 1
  fi
  pass "the image ships manifests/feastoperator/"
}

# ---------------------------------------------------------------------------
# e2e: run the repository's own operator e2e against the image built here.
#
# `make test-e2e` takes the operator image through IMG and the platform through
# ODH_PLATFORM_TYPE, so this exercises the PR image and the overlay it selects
# at runtime. Neither is visible to `go build` or to `helm template`.
#
# The chart pins `imagePullPolicy: Always`, so a locally built image cannot be
# loaded into the node's containerd: it has to be pullable. A throwaway
# registry that the Kind node reaches through a containerd mirror keeps that
# honest without publishing anything.
# ---------------------------------------------------------------------------

E2E_CLUSTER_NAME="${E2E_CLUSTER_NAME:-konflux-sim}"
E2E_REGISTRY_NAME="${E2E_REGISTRY_NAME:-konflux-sim-registry}"
E2E_REGISTRY_PORT="${E2E_REGISTRY_PORT:-5001}"
# Pinned by digest: this container runs with container-engine access on the
# runner, and it is the one unpinned artifact left in the e2e path next to a
# CRD bundle that is already checksum-pinned. Bump it by reviewed change.
E2E_REGISTRY_IMAGE="${E2E_REGISTRY_IMAGE:-registry:3@sha256:82f6ff4a7f982960c8aa51617fea3aabc485df6e55cce80d22deb1de36f1bc3c}"
E2E_PLATFORMS="${E2E_PLATFORMS:-odh rhoai}"
E2E_NAMESPACE="${E2E_NAMESPACE:-opendatahub-feast-system}"
E2E_SERVICEMONITOR_CRDS="${E2E_SERVICEMONITOR_CRDS:-https://github.com/prometheus-operator/prometheus-operator/releases/download/v0.80.1/stripped-down-crds.yaml}"
E2E_SERVICEMONITOR_CRDS_SHA256="${E2E_SERVICEMONITOR_CRDS_SHA256:-2948db44666044f0f5639649dc21b13bd403545562797c3a4d9bd76efab9e8dd}"

e2e_teardown() {
  if [ "${E2E_KEEP_CLUSTER:-0}" = "1" ]; then
    warn "E2E_KEEP_CLUSTER=1: leaving cluster ${E2E_CLUSTER_NAME} and registry in place"
    return 0
  fi
  kind delete cluster --name "$E2E_CLUSTER_NAME" >/dev/null 2>&1 || true
  "$CONTAINER_TOOL" rm -f "$E2E_REGISTRY_NAME" >/dev/null 2>&1 || true
}

e2e_dump_diagnostics() {
  local platform="$1"
  warn "diagnostics for ODH_PLATFORM_TYPE=${platform} (best effort):"
  # `set -e` and pipefail are active in this shell. Without relaxing them the
  # first kubectl that fails (missing CRD, no previous container to log) exits
  # the group and skips every later command, which is the opposite of best
  # effort.
  (
    set +e +o pipefail
    kubectl get feastoperator -A -o yaml 2>&1 | head -60
    kubectl get deploy,svc,sa,cm -n "$E2E_NAMESPACE" -o wide 2>&1
    kubectl get events -n "$E2E_NAMESPACE" --sort-by=.lastTimestamp 2>&1 | tail -30
    kubectl logs -n "$E2E_NAMESPACE" -l control-plane=controller-manager --tail=80 2>&1
    kubectl logs -n "$E2E_NAMESPACE" -l control-plane=controller-manager --tail=80 --previous 2>&1
    kubectl -n "$E2E_NAMESPACE" describe pod -l control-plane=controller-manager 2>&1 | tail -40
  ) | sed 's/^/   /' || true
}

validate_e2e() {
  resolve_konflux_target
  require_cmd "$CONTAINER_TOOL"
  require_cmd kind
  require_cmd kubectl
  require_cmd make
  require_cmd curl

  # Never touch the caller's kubeconfig. A failed `kind create` must not leave
  # `make deploy-helm` pointed at whatever cluster the shell already knew about.
  local kubeconfig
  kubeconfig="$(mktemp)"
  export KUBECONFIG="$kubeconfig"

  trap 'e2e_teardown; rm -f "${kubeconfig:-}"' EXIT
  trap 'exit 130' INT TERM

  info "Building the Konflux container image with ${CONTAINER_TOOL}..."
  "$CONTAINER_TOOL" build --platform "$PLATFORM" -f "$CONTAINERFILE" -t "$IMAGE" "$BUILD_CONTEXT"

  info "Starting the local registry on 127.0.0.1:${E2E_REGISTRY_PORT}..."
  "$CONTAINER_TOOL" rm -f "$E2E_REGISTRY_NAME" >/dev/null 2>&1 || true
  "$CONTAINER_TOOL" run -d --name "$E2E_REGISTRY_NAME" \
    -p "127.0.0.1:${E2E_REGISTRY_PORT}:5000" "$E2E_REGISTRY_IMAGE" >/dev/null

  local kind_config
  kind_config="$(mktemp)"
  # Enable containerd's certs.d directory, which is how a node is told about a
  # registry that is not on the public internet. This is kind's documented
  # local-registry setup; the older registry.mirrors key does not apply to
  # containerd 2.x.
  cat > "$kind_config" <<'YAML'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
  - |-
    [plugins."io.containerd.grpc.v1.cri".registry]
      config_path = "/etc/containerd/certs.d"
YAML

  info "Creating Kind cluster ${E2E_CLUSTER_NAME}..."
  kind create cluster --name "$E2E_CLUSTER_NAME" --wait 180s --config "$kind_config"
  rm -f "$kind_config"

  # `localhost` inside a node is not the host's localhost, so alias it to the
  # registry container, which the node reaches by name over the kind network.
  "$CONTAINER_TOOL" network connect kind "$E2E_REGISTRY_NAME" >/dev/null 2>&1 || true
  local node registry_dir
  registry_dir="/etc/containerd/certs.d/localhost:${E2E_REGISTRY_PORT}"
  for node in $(kind get nodes --name "$E2E_CLUSTER_NAME"); do
    "$CONTAINER_TOOL" exec "$node" mkdir -p "$registry_dir"
    printf '[host."http://%s:5000"]\n' "$E2E_REGISTRY_NAME" \
      | "$CONTAINER_TOOL" exec -i "$node" cp /dev/stdin "${registry_dir}/hosts.toml"
  done

  # The controller registers an owner watch on monitoring.coreos.com
  # ServiceMonitors unconditionally, so the manager cannot start without that
  # CRD. Platforms ship it; Kind does not.
  info "Installing the prometheus-operator CRDs (the operator owns a ServiceMonitor)..."
  local crd_bundle
  crd_bundle="$(mktemp)"
  curl -fsSL -o "$crd_bundle" "$E2E_SERVICEMONITOR_CRDS"
  verify_sha256 "$E2E_SERVICEMONITOR_CRDS_SHA256" "$crd_bundle"
  # Server-side apply: these CRDs exceed the 256 KiB client-side apply limit.
  kubectl apply --server-side -f "$crd_bundle" >/dev/null
  rm -f "$crd_bundle"

  # `kubectl wait --for=condition` reads .status.conditions directly and dies
  # with "accessor error: <nil> is of the type <nil>" when the API server has
  # not written status yet, which a freshly applied CRD often has not. Poll the
  # condition instead of depending on that timing.
  local established
  established=""
  for _ in $(seq 1 60); do
    established="$(kubectl get crd servicemonitors.monitoring.coreos.com \
      -o jsonpath='{.status.conditions[?(@.type=="Established")].status}' 2>/dev/null || true)"
    if [ "$established" = "True" ]; then
      break
    fi
    sleep 2
  done
  if [ "$established" != "True" ]; then
    err "the ServiceMonitor CRD did not become Established"
    return 1
  fi
  pass "ServiceMonitor CRD is established"

  local image_repo image_ref
  image_repo="localhost:${E2E_REGISTRY_PORT}/konflux-sim/feast-module-operator"
  image_ref="${image_repo}:$(git rev-parse --short=12 HEAD)"
  "$CONTAINER_TOOL" tag "$IMAGE" "$image_ref"
  "$CONTAINER_TOOL" push "$image_ref" >/dev/null
  pass "the PR image is pullable at ${image_ref}"

  # Helm resolves tags to digests when the container tool can supply one. Use
  # the same resolver here so the e2e compares against the exact reference Helm
  # deploys, not just the mutable tag that was pushed.
  local expected_image
  expected_image="$(CONTAINER_TOOL="$CONTAINER_TOOL" bash ./hack/scripts/resolve-image-ref.sh "$image_ref")"
  if ! is_resolved_pr_image "$image_ref" "$image_repo" "$expected_image"; then
    err "resolved image ${expected_image:-<empty>} is not the PR image ${image_ref}"
    return 1
  fi
  pass "the e2e will require the operator Deployment to run ${expected_image}"

  local platform
  for platform in $E2E_PLATFORMS; do
    info "Running the operator e2e with ODH_PLATFORM_TYPE=${platform}..."
    local -a make_vars=(
      # Pin the exact reference used for the assertion. Passing the tag here
      # would let deploy-helm resolve it again, potentially to a different
      # digest, and make the image identity check racy.
      "IMG=$expected_image"
      "RESOLVE_IMAGE_REF=0"
      "E2E_EXPECTED_IMAGE=$expected_image"
      "ODH_PLATFORM_TYPE=$platform"
      "HELM_NAMESPACE=$E2E_NAMESPACE"
      "OPERATOR_NAMESPACE=$E2E_NAMESPACE"
    )
    # Reuse prebuilt binaries when CI installed them; the Makefile otherwise
    # shells out to `go run`, which re-resolves the module on every call.
    if [ -n "$HELM_BIN" ]; then
      make_vars+=("HELM=$HELM_BIN")
    fi
    if [ -n "$KUSTOMIZE_BIN" ]; then
      make_vars+=("KUSTOMIZE=$KUSTOMIZE_BIN")
    fi

    if ! CONTAINER_TOOL="$CONTAINER_TOOL" make test-e2e ${make_vars[@]+"${make_vars[@]}"}; then
      err "operator e2e failed for ODH_PLATFORM_TYPE=${platform}"
      e2e_dump_diagnostics "$platform"
      return 1
    fi
    pass "operator e2e passed for ODH_PLATFORM_TYPE=${platform}"
  done
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  case "$MODE" in
    static)    validate_static ;;
    hermetic)  validate_hermetic ;;
    manifests) validate_manifests ;;
    chart)     validate_chart ;;
    image)     validate_image ;;
    e2e)       validate_e2e ;;
    builder-go) builder_go_version ;;
    target)
      resolve_konflux_target
      printf 'dockerfile=%s\ncontext=%s\nbuilder_go=%s\n' \
        "$CONTAINERFILE" "$BUILD_CONTEXT" "$(builder_go_version)"
      ;;
    all)       validate_static; validate_hermetic; validate_manifests; validate_chart; validate_image ;;
    *)
      err "Unknown mode: $MODE"
      echo "Usage: $0 <static|hermetic|manifests|chart|image|e2e|builder-go|target|all>" >&2
      exit 2
      ;;
  esac
fi
