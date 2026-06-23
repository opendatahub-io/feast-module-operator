#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat >&2 <<'EOF'
Usage: push-openshift-image.sh <source-image> <namespace> <image-name>

Log into the OpenShift image registry route, push the source image, and print
the internal cluster pullspec (image-registry.openshift-image-registry.svc:5000/...).
EOF
}

source_image="${1:-}"
namespace="${2:-}"
image_name="${3:-}"

route_name="${OCP_REGISTRY_ROUTE_NAME:-default-route}"
route_namespace="${OCP_REGISTRY_ROUTE_NAMESPACE:-openshift-image-registry}"
internal_registry_host="${OCP_INTERNAL_REGISTRY_HOST:-image-registry.openshift-image-registry.svc:5000}"

if [[ -z "${source_image}" || -z "${namespace}" || -z "${image_name}" ]]; then
    usage
    exit 1
fi

external_host="$(oc get route "${route_name}" -n "${route_namespace}" -o jsonpath='{.spec.host}' 2>/dev/null || true)"
if [[ -z "${external_host}" ]]; then
    echo "OpenShift image registry route ${route_name} not found in namespace ${route_namespace}" >&2
    echo "Verify the cluster exposes the default route and that 'oc registry login --insecure=true' works." >&2
    exit 1
fi

if [[ -z "$(kubectl get namespace "${namespace}" -o name --ignore-not-found 2>/dev/null)" ]]; then
    echo "Ensuring namespace ${namespace} exists" >&2
    kubectl create namespace "${namespace}" >/dev/null
fi

ocp_tag="$(uuidgen | tr '[:upper:]' '[:lower:]')"
external_image="${external_host}/${namespace}/${image_name}:${ocp_tag}"
internal_image="${internal_registry_host}/${namespace}/${image_name}:${ocp_tag}"

insecure_flag=""
if [[ "${INSECURE_REGISTRY:-false}" == "true" ]]; then
    insecure_flag="--insecure=true"
fi

tls_verify="true"
if [[ "${INSECURE_REGISTRY:-false}" == "true" ]]; then
    tls_verify="false"
fi

echo "Logging into ${external_host}" >&2
oc registry login ${insecure_flag} --registry "${external_host}" >/dev/null

echo "Tagging ${source_image} as ${external_image}" >&2
podman tag "${source_image}" "${external_image}"

echo "Pushing ${external_image}" >&2
podman push "${external_image}" --tls-verify="${tls_verify}" >/dev/null

printf '%s\n' "${internal_image}"
