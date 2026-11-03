#!/usr/bin/env bash
# Run Feast module-operator e2e tests offline-friendly for ods-ci.
#
# CI clones this repo fresh with no module cache. Downloading deps via
# proxy.golang.org routinely exceeds the Robot Framework process timeout
# (~15m) before any test runs. Prefer the committed vendor/ tree.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

if [[ ! -d vendor ]]; then
  echo "ERROR: vendor/ is missing. Run 'make deps' (go mod vendor) and commit vendor/." >&2
  exit 1
fi

export GOFLAGS="${GOFLAGS:--mod=vendor}"
export GOPROXY="${GOPROXY:-off}"
export GOSUMDB="${GOSUMDB:-off}"

OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-redhat-ods-applications}"

echo "Running e2e with GOFLAGS=${GOFLAGS} GOPROXY=${GOPROXY} OPERATOR_NAMESPACE=${OPERATOR_NAMESPACE}"
exec go test -count=1 -ldflags "${LDFLAGS:-}" ./test/e2e/ -tags=e2e -v -timeout 5m -failfast "$@"
