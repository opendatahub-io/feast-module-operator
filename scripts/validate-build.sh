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
#   scripts/validate-build.sh <static|hermetic|manifests|chart|image|all>
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
#   SKIP_CHART_DRIFT=1   Skip the regenerate-and-compare step
#   SKIP_IMAGE=1         Skip the image build

MODE="${1:-all}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

CONTAINERFILE="${CONTAINERFILE:-Containerfile}"
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

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "required command not found: $1"
    exit 1
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

check_konflux_targets() {
  info "Checking Konflux PipelineRun targets..."
  local file found=0

  for file in .tekton/*pull-request.yaml; do
    [ -e "$file" ] || continue
    found=$((found + 1))

    local dockerfile path_context has_platforms
    read -r dockerfile path_context < <(python3 - "$file" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
params = {p.get("name"): p.get("value") for p in (doc.get("spec") or {}).get("params") or []}
print(params.get("dockerfile", "-"), params.get("path-context", "-"))
PY
)

    if [ "$dockerfile" = "-" ] || [ "$path_context" = "-" ]; then
      err "$file is missing a dockerfile or path-context param"
      exit 1
    fi

    if [ ! -f "${path_context%/}/${dockerfile}" ]; then
      err "$file points at ${path_context%/}/${dockerfile}, which does not exist"
      exit 1
    fi
    pass "$file -> ${path_context%/}/${dockerfile}"

    has_platforms="$(python3 - "$file" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
params = {p.get("name") for p in (doc.get("spec") or {}).get("params") or []}
print("yes" if "build-platforms" in params else "no")
PY
)"
    if [ "$has_platforms" = "no" ]; then
      warn "$file does not set build-platforms; the shared pipeline falls back to linux/x86_64 only."
      warn "  A single-arch manifest list surfaces as ImagePullBackOff on ppc64le/arm64 (RHOAIENG-79767)."
    fi
  done

  if [ "$found" -eq 0 ]; then
    err "no .tekton/*pull-request.yaml found"
    exit 1
  fi
}

check_go_inputs() {
  info "Checking Go module inputs..."
  require_cmd go

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
    if [ ! -e "$src" ]; then
      err "$file copies $src, which is not in the build context"
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

check_chart_drift() {
  info "Checking whether config/chart matches 'make helm' output..."

  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp:-}"' RETURN
  mkdir -p "$tmp/repo"

  if ! git archive HEAD | tar -x -C "$tmp/repo"; then
    warn "could not export HEAD; skipping chart drift check"
    return
  fi

  # Reuse a prebuilt kustomize when one was supplied; otherwise `make helm`
  # falls back to `go run`, which needs network on a cold module cache.
  local -a make_args=()
  if [ -n "$KUSTOMIZE_BIN" ]; then
    make_args+=("KUSTOMIZE=$KUSTOMIZE_BIN")
  fi

  if ! ( cd "$tmp/repo" && make helm ${make_args[@]+"${make_args[@]}"} >/dev/null 2>&1 ); then
    warn "make helm failed in a clean export; skipping chart drift check"
    return
  fi

  if diff -r "$CHART_DIR" "$tmp/repo/$CHART_DIR" > "$tmp/diff.txt" 2>&1; then
    pass "config/chart matches the generated output"
  else
    warn "config/chart does not match 'make helm' output on HEAD:"
    head -20 "$tmp/diff.txt" | sed 's/^/   /'
    warn "  config/chart is what manifests-config.yaml consumes (sourcePath: config/chart)."
    warn "  Nothing else in CI regenerates it — 'Verify Generated Files' runs manifests+generate only (RHOAIENG-96436)."
    warn "  Not fatal here on purpose: this drift already exists on main, so a hard failure would block every PR."
  fi
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

  local file
  for file in "${out_dir}"/*.yaml; do
    "$KUBECONFORM_BIN" -strict -ignore-missing-schemas \
      -kubernetes-version "$KUBERNETES_VERSION" -output json "$file" > "${file}.json" || true

    local bad
    bad="$(python3 - "${file}.json" <<'PY'
import json, sys
bad = []
for r in json.load(open(sys.argv[1])).get("resources") or []:
    if r.get("status") != "statusSkipped":
        continue
    group = r.get("version", "").split("/")[0] if "/" in r.get("version", "") else ""
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
  info "Building the Konflux container image with ${CONTAINER_TOOL}..."
  require_cmd "$CONTAINER_TOOL"

  "$CONTAINER_TOOL" build --platform "$PLATFORM" -f "$CONTAINERFILE" -t "$IMAGE" .

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

case "$MODE" in
  static)    validate_static ;;
  hermetic)  validate_hermetic ;;
  manifests) validate_manifests ;;
  chart)     validate_chart ;;
  image)     validate_image ;;
  all)       validate_static; validate_hermetic; validate_manifests; validate_chart; validate_image ;;
  *)
    err "Unknown mode: $MODE"
    echo "Usage: $0 <static|hermetic|manifests|chart|image|all>" >&2
    exit 2
    ;;
esac
