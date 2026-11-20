#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${1:-opendatahub-feast-system}"

# The rhoai overlay annotates the metrics Service with
# service.beta.openshift.io/serving-cert-secret-name and mounts the Secret the
# OpenShift service-ca controller creates from it. The ServiceMonitor also
# reads the CA bundle the controller publishes as openshift-service-ca.crt.
#
# A cluster without that controller — Kind, for example — never gets either, so
# the operand pod sits in ContainerCreating on the metrics-cert volume and the
# module never becomes Ready. Create stand-ins so the rest of the overlay is
# still exercised; the injection itself is only testable on a real cluster.
if kubectl get namespace openshift-service-ca >/dev/null 2>&1; then
    echo "OpenShift service-ca controller present; nothing to prepare."
    exit 0
fi

echo "No OpenShift service-ca controller found; creating its stand-ins in ${NAMESPACE}..."

workdir="$(mktemp -d)"
trap 'rm -rf "${workdir}"' EXIT

openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
    -keyout "${workdir}/tls.key" -out "${workdir}/tls.crt" \
    -subj "/CN=controller-manager-metrics-service" >/dev/null 2>&1

kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "${NAMESPACE}" create secret tls controller-manager-metrics-tls \
    --cert="${workdir}/tls.crt" --key="${workdir}/tls.key" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "${NAMESPACE}" create configmap openshift-service-ca.crt \
    --from-file=service-ca.crt="${workdir}/tls.crt" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "Created the metrics TLS Secret and CA ConfigMap."
