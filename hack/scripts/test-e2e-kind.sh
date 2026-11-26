#!/usr/bin/env bash
# Orchestrate the dedicated Kind e2e workflow for one platform variant.
#
# Ordered workflow (single platform per invocation):
#   1. Preflight tools/engine; create or safely reuse the dedicated Kind
#      cluster (exact pin, isolated kubeconfig, node readiness).
#   2. Install the OpenShift compatibility CRDs, create or verify the
#      test-owned namespace, and install the static service-certificate
#      fixtures (no cert-manager, no unrelated APIs).
#   3. Build a uniquely tagged local controller image for the node
#      architecture through the existing container-build path.
#   4. Load that exact tag into every Kind node from a private image
#      archive; verify the identity locally and inside the node.
#   5. Install/upgrade the generated chart through pinned Helm using the
#      exact local image and image.pullPolicy=Never (no registry push,
#      no digest resolution).
#   6. Wait for the module rollout, verify the running image identity,
#      and invoke `make test-e2e-run` with the same kubeconfig, namespace,
#      and repository ldflags.
#
# Modes:
#   test-e2e-kind.sh <platform> <platform-value>   full workflow (via make)
#   test-e2e-kind.sh --setup-only                  steps 1-2 only
#   test-e2e-kind.sh --delete-cluster              delete only the dedicated cluster
#
# Safety policy:
#   - Only the dedicated named Kind cluster is used or deleted; the global
#     kubeconfig and current context are never modified.
#   - No registry push, no automatic cluster deletion, no legacy cleanup
#     script, no ambient-cluster fallback.
#   - Existing namespaces/releases without matching fixture ownership
#     block the run instead of being adopted.
#
# The cluster name and pinned versions must stay identical to the baked
# settings in hack/scripts/install-openshift-crds.sh and
# hack/scripts/setup-service-certs.sh; mismatches are rejected.
set -Eeuo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# ---------------------------------------------------------------------------
# Settings (must agree with the committed compatibility helpers)
# ---------------------------------------------------------------------------

# Dedicated Kind cluster identity. Matches the baked settings of
# install-openshift-crds.sh and setup-service-certs.sh; overrides are
# rejected so orchestration and helpers can never diverge.
KIND_CLUSTER_NAME="feast-module-operator-e2e"

# Exact pins (same runners as the Makefile).
KIND_VERSION="v0.33.0"
HELM_VERSION="v4.3.0"

# Test installation identity. Matches the helpers and the e2e suite
# defaults (test/support.DefaultOperatorNamespace via OPERATOR_NAMESPACE).
NAMESPACE="opendatahub-feast-system"
HELM_RELEASE="opendatahub-feast-operator"
CHART_NAME="opendatahub-feast-operator"

# Fixture ownership annotation shared with the e2e suite.
FIXTURE_OWNER_ANNOTATION="e2e.feast.dev/owner"

# Bounded waits.
NODE_READY_TIMEOUT="300s"
ROLLOUT_TIMEOUT="300s"
# Bounded wait for a live module pod of this run's image: rollout status
# can return while a replaced pod of a previous run is still terminating.
POD_READY_TIMEOUT_SECS=120

# Supported platform variants for this workflow.
PLATFORM_ODH="odh"
PLATFORM_RHOAI="rhoai"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

info() {
    echo "test-e2e-kind: $*"
}

die() {
    echo "test-e2e-kind: error: $*" >&2
    exit 1
}

cleanup_scratch() {
    if [ -d "${SCRATCH:-}" ]; then
        rm -rf -- "${SCRATCH}"
    fi
}

usage() {
    cat >&2 <<'EOF'
Usage:
  test-e2e-kind.sh <platform> <platform-value>   # full e2e workflow
  test-e2e-kind.sh --setup-only                  # create/configure cluster only
  test-e2e-kind.sh --delete-cluster              # delete the dedicated cluster
EOF
}

# Every kubectl call goes through the private kubeconfig exported from the
# dedicated cluster; the ambient context is never consulted.
kctl() {
    kubectl --kubeconfig "${KUBECONFIG_FILE}" "$@"
}

# Every kind call uses the exact pin and the resolved provider. The
# provider is either the explicitly supplied KIND_EXPERIMENTAL_PROVIDER or
# the engine this script selected; it is never changed for an existing
# cluster.
kind_run() {
    KIND_EXPERIMENTAL_PROVIDER="${PROVIDER}" \
        go run sigs.k8s.io/kind@"${KIND_VERSION}" "$@"
}

helm_run() {
    go run helm.sh/helm/v4/cmd/helm@"${HELM_VERSION}" "$@"
}

engine_usable() {
    command -v "$1" >/dev/null 2>&1 && "$1" info >/dev/null 2>&1
}

# The control-plane container name identifies the cluster inside an engine.
cluster_in_engine() {
    "$1" ps -a --format '{{.Names}}' 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}-control-plane"
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

MODE="run"
PLATFORM=""
PLATFORM_VALUE=""

case "${1:-}" in
    --setup-only)
        MODE="setup-only"
        ;;
    --delete-cluster)
        MODE="delete-cluster"
        ;;
    "")
        usage
        die "missing platform argument"
        ;;
    -*)
        usage
        die "unknown option: $1"
        ;;
    *)
        PLATFORM="$1"
        PLATFORM_VALUE="${2:-}"
        ;;
esac

if [ "${MODE}" = "run" ]; then
    [ -n "${PLATFORM_VALUE}" ] || die "platform value argument is required (pass it from the Makefile)"

    case "${PLATFORM}:${PLATFORM_VALUE}" in
        "${PLATFORM_ODH}:Open Data Hub") ;;
        "${PLATFORM_RHOAI}:OpenShift AI Self-Managed") ;;
        *)
            die "unsupported platform pair '${PLATFORM}:${PLATFORM_VALUE}'; expected ${PLATFORM_ODH} or ${PLATFORM_RHOAI} with the Makefile platform value"
            ;;
    esac
fi

# ---------------------------------------------------------------------------
# Preflight
# ---------------------------------------------------------------------------

for command_name in go kubectl jq skopeo make git; do
    command -v "${command_name}" >/dev/null 2>&1 ||
        die "${command_name} is required"
done

# Resolve the container engine/provider. An explicitly supplied
# KIND_EXPERIMENTAL_PROVIDER is preserved as-is; otherwise an existing
# cluster keeps the engine that owns it (never silently switched), and a
# new cluster prefers Podman over Docker.
EXPLICIT_PROVIDER="${KIND_EXPERIMENTAL_PROVIDER:-}"
if [ -n "${EXPLICIT_PROVIDER}" ]; then
    PROVIDER="${EXPLICIT_PROVIDER}"
    engine_usable "${PROVIDER}" ||
        die "KIND_EXPERIMENTAL_PROVIDER=${PROVIDER} is not a usable container engine"
else
    PROVIDER=""
    existing=""
    for candidate in docker podman; do
        if engine_usable "${candidate}" && cluster_in_engine "${candidate}"; then
            existing="${existing} ${candidate}"
        fi
    done

    case "${existing}" in
        " docker podman")
            die "cluster '${KIND_CLUSTER_NAME}' exists in both docker and podman and no KIND_EXPERIMENTAL_PROVIDER was supplied; set it explicitly"
            ;;
        " docker")
            PROVIDER="docker"
            ;;
        " podman")
            PROVIDER="podman"
            ;;
        "")
            if engine_usable podman; then
                PROVIDER="podman"
            elif engine_usable docker; then
                PROVIDER="docker"
            else
                die "no usable container engine found (tried podman, docker)"
            fi
            ;;
    esac
fi

info "container engine/provider: ${PROVIDER}"

SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/feast-e2e-kind.XXXXXX")"
trap cleanup_scratch EXIT INT TERM
KUBECONFIG_FILE="${SCRATCH}/kubeconfig"

# ---------------------------------------------------------------------------
# Delete-cluster mode
# ---------------------------------------------------------------------------

if [ "${MODE}" = "delete-cluster" ]; then
    if cluster_in_engine "${PROVIDER}"; then
        info "deleting dedicated Kind cluster '${KIND_CLUSTER_NAME}' (provider ${PROVIDER})"
        kind_run delete cluster --name "${KIND_CLUSTER_NAME}"
        info "cluster '${KIND_CLUSTER_NAME}' deleted"
    else
        info "cluster '${KIND_CLUSTER_NAME}' not found in ${PROVIDER}; nothing to delete"
    fi
    exit 0
fi

# ---------------------------------------------------------------------------
# Step 1: dedicated Kind cluster
# ---------------------------------------------------------------------------

cluster_list="$(kind_run get clusters)" ||
    die "kind ${KIND_VERSION} could not list clusters"

if printf '%s\n' "${cluster_list}" | grep -qx "${KIND_CLUSTER_NAME}"; then
    info "reusing dedicated Kind cluster '${KIND_CLUSTER_NAME}'"
    kind_run get kubeconfig --name "${KIND_CLUSTER_NAME}" >"${KUBECONFIG_FILE}" ||
        die "could not export the kubeconfig of cluster '${KIND_CLUSTER_NAME}'"
else
    info "creating dedicated Kind cluster '${KIND_CLUSTER_NAME}' (kind ${KIND_VERSION}, pinned default node image)"
    kind_run create cluster --name "${KIND_CLUSTER_NAME}" --kubeconfig "${KUBECONFIG_FILE}" ||
        die "could not create cluster '${KIND_CLUSTER_NAME}'"
fi
chmod 600 "${KUBECONFIG_FILE}"

info "waiting for node readiness"
kctl wait --for=condition=Ready nodes --all --timeout="${NODE_READY_TIMEOUT}" >/dev/null ||
    die "nodes of cluster '${KIND_CLUSTER_NAME}' did not become ready in time"

NODE_ARCH="$(kctl get nodes -o jsonpath='{.items[0].status.nodeInfo.architecture}')"
[ -n "${NODE_ARCH}" ] || die "could not determine the node architecture"
NODE_COUNT="$(kctl get nodes -o jsonpath='{.items[*].metadata.name}' | wc -w | tr -d ' ')"
info "cluster ready: ${NODE_COUNT} node(s), architecture ${NODE_ARCH}"

# ---------------------------------------------------------------------------
# Step 2: compatibility CRDs, test-owned namespace, TLS fixtures
# ---------------------------------------------------------------------------

# The helpers are standalone and discover the cluster by name using the
# same pins; propagate the resolved provider so kind never auto-detects a
# different engine.
info "installing OpenShift compatibility CRDs (ServiceMonitor, APIServer, Route)"
KIND_EXPERIMENTAL_PROVIDER="${PROVIDER}" bash "${SCRIPT_DIR}/install-openshift-crds.sh" ||
    die "compatibility CRD installation failed"

if kctl get namespace "${NAMESPACE}" >/dev/null 2>&1; then
    ns_owner="$(kctl get namespace "${NAMESPACE}" -o json | jq -r ".metadata.annotations[\"${FIXTURE_OWNER_ANNOTATION}\"] // \"\"")"
    if [ "${ns_owner}" != "${KIND_CLUSTER_NAME}" ]; then
        die "namespace '${NAMESPACE}' already exists without fixture ownership (${FIXTURE_OWNER_ANNOTATION}='${ns_owner:-<unset>}'); refusing to adopt it"
    fi
    info "reusing test-owned namespace '${NAMESPACE}'"
else
    kctl create namespace "${NAMESPACE}" >/dev/null ||
        die "could not create namespace '${NAMESPACE}'"
    kctl annotate namespace "${NAMESPACE}" "${FIXTURE_OWNER_ANNOTATION}=${KIND_CLUSTER_NAME}" --overwrite >/dev/null ||
        die "could not mark namespace '${NAMESPACE}' as test-owned"
    info "created test-owned namespace '${NAMESPACE}'"
fi

# Reject unexpected Helm installations before adoption: an existing
# release must be this chart's release.
if helm_run status "${HELM_RELEASE}" --namespace "${NAMESPACE}" --kubeconfig "${KUBECONFIG_FILE}" >/dev/null 2>&1; then
    release_chart="$(helm_run get metadata "${HELM_RELEASE}" --namespace "${NAMESPACE}" --kubeconfig "${KUBECONFIG_FILE}" -o json | jq -r '.chart // empty')"
    if [ "${release_chart}" != "${CHART_NAME}" ]; then
        die "Helm release '${HELM_RELEASE}' in namespace '${NAMESPACE}' belongs to chart '${release_chart:-<unknown>}' (expected '${CHART_NAME}'); refusing to adopt it"
    fi
    info "reusing Helm release '${HELM_RELEASE}' (chart ${CHART_NAME})"
fi

# Static TLS fixtures for the operand's metrics serving certificate. The
# helper is idempotent: an existing valid Secret/CA pair is verified, never
# replaced. Required before RHOAI (and OpenShift-overlay) workloads are
# created; harmless on a fresh ODH run.
info "installing static service-certificate fixtures"
KIND_EXPERIMENTAL_PROVIDER="${PROVIDER}" bash "${SCRIPT_DIR}/setup-service-certs.sh" ||
    die "service-certificate setup failed"

if [ "${MODE}" = "setup-only" ]; then
    info "setup complete (cluster '${KIND_CLUSTER_NAME}', namespace '${NAMESPACE}', CRDs and TLS fixtures installed)"
    exit 0
fi

# ---------------------------------------------------------------------------
# Step 3: local controller image (existing container-build path)
# ---------------------------------------------------------------------------

GIT_SHORT="$(git -C "${PROJECT_ROOT}" rev-parse --short HEAD)"
IMAGE_TAG="e2e-$(date +%Y%m%d%H%M%S)-${GIT_SHORT}"
IMAGE_REF="localhost/${KIND_CLUSTER_NAME}/module:${IMAGE_TAG}"

info "building local controller image ${IMAGE_REF} (linux/${NODE_ARCH})"
(
    cd "${PROJECT_ROOT}" &&
        SKIP_GET_MANIFESTS=1 make container-build \
            IMG="${IMAGE_REF}" \
            CONTAINER_TOOL="${PROVIDER}" \
            CONTAINER_BUILD_PLATFORM="linux/${NODE_ARCH}"
) || die "local controller image build failed"

"${PROVIDER}" image inspect "${IMAGE_REF}" >/dev/null 2>&1 ||
    die "built image ${IMAGE_REF} not present in ${PROVIDER}"

# ---------------------------------------------------------------------------
# Step 4: load the exact tag into every Kind node
# ---------------------------------------------------------------------------

IMAGE_ARCHIVE="${SCRATCH}/module-image.tar"
"${PROVIDER}" save -o "${IMAGE_ARCHIVE}" "${IMAGE_REF}" ||
    die "could not save ${IMAGE_REF} to an image archive"

info "loading ${IMAGE_REF} into every node of cluster '${KIND_CLUSTER_NAME}'"
kind_run load image-archive "${IMAGE_ARCHIVE}" --name "${KIND_CLUSTER_NAME}" ||
    die "could not load the image archive into cluster '${KIND_CLUSTER_NAME}'"

# Verify the exact tag inside a node. crictl json output keeps the full
# repo:tag form; fall back to matching repo and tag columns separately.
NODE_NAME="${KIND_CLUSTER_NAME}-control-plane"
if "${PROVIDER}" exec "${NODE_NAME}" crictl images -o json 2>/dev/null |
    jq -e --arg ref "${IMAGE_REF}" '[.images[].repoTags[]? // empty] | any(. == $ref)' >/dev/null; then
    info "verified ${IMAGE_REF} present in node ${NODE_NAME}"
else
    if "${PROVIDER}" exec "${NODE_NAME}" crictl images 2>/dev/null |
        grep -F "$(printf '%s' "${IMAGE_REF}" | cut -d: -f1)" |
        grep -F "$(printf '%s' "${IMAGE_REF}" | cut -d: -f2-)" >/dev/null; then
        info "verified ${IMAGE_REF} present in node ${NODE_NAME}"
    else
        die "could not verify ${IMAGE_REF} inside node ${NODE_NAME}"
    fi
fi

# ---------------------------------------------------------------------------
# Step 5: Helm install/upgrade with the local image
# ---------------------------------------------------------------------------

# Resolve a pullable digest reference for the same approved Feast release
# bundled in the selected overlay. The digest is textually distinct from
# the bundled tag and proves static image substitution through the
# RELATED_IMAGE mapping.
BUNDLED_FEAST_REF="$(sed -n 's/^RELATED_IMAGE_FEAST_OPERATOR=//p' "${PROJECT_ROOT}/config/manifests/feastoperator/overlays/${PLATFORM}/params.env" | head -1)"
[ -n "${BUNDLED_FEAST_REF}" ] ||
    die "could not read RELATED_IMAGE_FEAST_OPERATOR from the ${PLATFORM} overlay"
case "${BUNDLED_FEAST_REF}" in
    *@*) die "unexpected digest-pinned bundled Feast reference: ${BUNDLED_FEAST_REF}" ;;
    *:*) ;;
    *) die "unexpected bundled Feast reference without a tag: ${BUNDLED_FEAST_REF}" ;;
esac

info "resolving Feast image digest for ${BUNDLED_FEAST_REF}"
# --override-os linux keeps skopeo from selecting an image instance for
# the host platform (e.g. darwin); the reported digest is the tag's
# canonical manifest digest, pullable by any node architecture.
FEAST_DIGEST="$(skopeo inspect --override-os linux --format '{{.Digest}}' "docker://${BUNDLED_FEAST_REF}")" ||
    die "could not resolve the digest of ${BUNDLED_FEAST_REF}"
case "${FEAST_DIGEST}" in
    sha256:*) ;;
    *) die "unexpected digest format: ${FEAST_DIGEST}" ;;
esac
EXPECTED_FEAST_IMAGE="${BUNDLED_FEAST_REF%%:*}@${FEAST_DIGEST}"
[ "${EXPECTED_FEAST_IMAGE}" != "${BUNDLED_FEAST_REF}" ] ||
    die "expected Feast image must differ textually from the bundled tag"

info "expected operand Feast image: ${EXPECTED_FEAST_IMAGE}"

info "installing/upgrading Helm release '${HELM_RELEASE}' with the local image (pullPolicy Never)"
helm_run upgrade --install "${HELM_RELEASE}" "${PROJECT_ROOT}/config/chart" \
    --kubeconfig "${KUBECONFIG_FILE}" \
    --namespace "${NAMESPACE}" \
    --set-string namespace="${NAMESPACE}" \
    --set-string image.fullRef="${IMAGE_REF}" \
    --set-string image.pullPolicy=Never \
    --set-string extraEnvs.ODH_MODULE_OPERATOR_PLATFORM_TYPE="${PLATFORM_VALUE}" \
    --set-string extraEnvs.RELATED_IMAGE_ODH_FEAST_OPERATOR_IMAGE="${EXPECTED_FEAST_IMAGE}" \
    --wait --timeout "${ROLLOUT_TIMEOUT}" ||
    die "Helm install/upgrade failed"

# ---------------------------------------------------------------------------
# Step 6: rollout verification and the e2e suite
# ---------------------------------------------------------------------------

info "waiting for the module rollout"
kctl rollout status "deployment/${HELM_RELEASE}" --namespace "${NAMESPACE}" --timeout="${ROLLOUT_TIMEOUT}" >/dev/null ||
    die "module deployment did not roll out in time"

DEPLOYED_IMAGE="$(kctl get "deployment/${HELM_RELEASE}" --namespace "${NAMESPACE}" -o jsonpath='{.spec.template.spec.containers[0].image}')"
[ "${DEPLOYED_IMAGE}" = "${IMAGE_REF}" ] ||
    die "module deployment image is '${DEPLOYED_IMAGE}', expected '${IMAGE_REF}'"

# rollout status can return while a replaced pod (a failed run preserves
# its pods for inspection) is still terminating, and the container
# runtime's reported status image is a store name for the image ID — not
# necessarily the requested tag when several tags share one ID (each run
# rebuilds the same content under a new tag). So verify the pod SPEC
# image of a live, ready pod instead of the reported status image.
POD_IMAGE=""
deadline=$(( $(date +%s) + POD_READY_TIMEOUT_SECS ))
while [ "$(date +%s)" -lt "${deadline}" ]; do
    POD_IMAGE="$(kctl get pods --namespace "${NAMESPACE}" -l "app.kubernetes.io/name=${HELM_RELEASE}" -o json |
        jq -r --arg image "${IMAGE_REF}" \
            'if ([.items[] |
                  select(.metadata.deletionTimestamp == null) |
                  select(any(.spec.containers[]; .image == $image)) |
                  select(any(.status.containerStatuses[]?; .ready == true))] | length > 0)
             then $image
             else empty end')"
    [ -n "${POD_IMAGE}" ] && break
    sleep 2
done
[ -n "${POD_IMAGE}" ] ||
    die "no live, ready module pod runs image '${IMAGE_REF}' (rollout incomplete or wrong image)"
info "verified running module image: ${POD_IMAGE}"

info "running e2e suite (${PLATFORM}, namespace ${NAMESPACE})"
(
    cd "${PROJECT_ROOT}" &&
        make test-e2e-run \
            OPERATOR_NAMESPACE="${NAMESPACE}" \
            KUBECONFIG="${KUBECONFIG_FILE}" \
            E2E_KIND_CHECKS=true \
            E2E_KIND_CLUSTER="${KIND_CLUSTER_NAME}" \
            E2E_EXPECTED_FEAST_IMAGE="${EXPECTED_FEAST_IMAGE}"
) || die "e2e suite failed"

info "e2e workflow complete (platform ${PLATFORM}, image ${IMAGE_REF})"
