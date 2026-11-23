#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DEST="${ROOT}/config/manifests/dataconnecthub"
SOURCE_PATH="dc-controller/config"

case "${ODH_PLATFORM_TYPE:-odh}" in
    odh | OpenDataHub)
        REPO="https://github.com/opendatahub-io/data-connect-hub"
        REVISION="756540f540681f59490bbae4c0e92f5f0a05528d"
        ;;
    rhoai | managed-rhoai)
        REPO="https://github.com/red-hat-data-services/data-connect-hub"
        REVISION="bf34a3bd019e9b42cdd622bb38c1b18bc2550688"
        ;;
    *)
        echo "Unsupported ODH_PLATFORM_TYPE: ${ODH_PLATFORM_TYPE}" >&2
        exit 1
        ;;
esac

manifest_digest() {
    python3 - "$1" <<'PY'
import hashlib
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
digest = hashlib.sha256()
for item in sorted(path for path in root.rglob("*") if path.is_file()):
    name = item.relative_to(root).as_posix()
    if name in {".manifest-source-commit", ".manifest-content-sha256"}:
        continue
    digest.update(name.encode())
    digest.update(b"\0")
    digest.update(hashlib.sha256(item.read_bytes()).digest())
print(digest.hexdigest())
PY
}

if [[ -z "${DCH_SOURCE_DIR:-}" && "${FORCE_GET_MANIFESTS:-}" != "true" &&
      -f "${DEST}/.manifest-source-commit" && -f "${DEST}/.manifest-content-sha256" &&
      -f "${DEST}/default/kustomization.yaml" ]]; then
    if [[ "$(cat "${DEST}/.manifest-source-commit")" == "${REVISION}" &&
          "$(cat "${DEST}/.manifest-content-sha256")" == "$(manifest_digest "${DEST}")" ]]; then
        echo "DCH manifests already at ${REVISION} with verified content"
        exit 0
    fi
fi

WORK="$(mktemp -d "${TMPDIR:-/tmp}/dch-manifests.XXXXXXXX")"
trap 'rm -rf -- "${WORK}"' EXIT
mkdir -p "${WORK}/manifests"

if [[ -n "${DCH_SOURCE_DIR:-}" ]]; then
    cp -R "${DCH_SOURCE_DIR}/${SOURCE_PATH}/." "${WORK}/manifests/"
    REVISION="$(git -C "${DCH_SOURCE_DIR}" rev-parse HEAD)"
    if [[ -n "$(git -C "${DCH_SOURCE_DIR}" status --porcelain --untracked-files=all)" ]]; then
        REVISION="${REVISION}-dirty"
    fi
else
    git init -q "${WORK}/source"
    git -C "${WORK}/source" fetch --depth 1 "${REPO}" "${REVISION}"
    git -C "${WORK}/source" archive FETCH_HEAD "${SOURCE_PATH}" |
        tar -xf - -C "${WORK}/manifests" --strip-components=2
fi

test -f "${WORK}/manifests/default/kustomization.yaml"
test -f "${WORK}/manifests/crd/bases/dataconnecthub.opendatahub.io_dataconnectservices.yaml"
echo "${REVISION}" > "${WORK}/manifests/.manifest-source-commit"
manifest_digest "${WORK}/manifests" > "${WORK}/manifests/.manifest-content-sha256"
# Only replace this generated, component-specific bundle after verification.
mkdir -p "$(dirname "${DEST}")"
if [[ -d "${DEST}" ]]; then
    mv "${DEST}" "${WORK}/previous"
fi
mv "${WORK}/manifests" "${DEST}"
echo "DCH controller manifests copied to ${DEST} at ${REVISION}"
