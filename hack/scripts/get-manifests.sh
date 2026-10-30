#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

COMPONENT_NAME="feastoperator"
SOURCE_PATH="infra/feast-operator/config"
DST_MANIFESTS_DIR="${PROJECT_ROOT}/config/manifests/${COMPONENT_NAME}"
MARKER_FILE="${DST_MANIFESTS_DIR}/.manifest-source-commit"
DIGEST_FILE="${DST_MANIFESTS_DIR}/.manifest-content-sha256"

# The commit marker records provenance; the digest detects later edits to the
# bundled files so a stale marker cannot cause an incorrect skip.
manifest_digest() {
    python3 - "$1" <<'PY'
import hashlib
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
digest = hashlib.sha256()
for path in sorted(p for p in root.rglob("*") if p.is_file()):
    relative = path.relative_to(root).as_posix()
    if relative in {".manifest-source-commit", ".manifest-content-sha256"}:
        continue
    digest.update(relative.encode("utf-8"))
    digest.update(b"\0")
    digest.update(hashlib.sha256(path.read_bytes()).digest())
print(digest.hexdigest())
PY
}

# ODH_PLATFORM_TYPE accepts the same friendly names as the Makefile
# (odh | rhoai | managed-rhoai); the legacy value "OpenDataHub" is kept
# for compatibility. An unset or empty value defaults to ODH.
platform_type="${ODH_PLATFORM_TYPE:-odh}"
case "${platform_type}" in
    odh | OpenDataHub)
        echo "Downloading manifests for ODH"
        REPO_URL="https://github.com/opendatahub-io/feast"
        COMMIT_SHA="4cab4bca4ad1af8b28b35eafc2f49ebe3800a732"
        ;;
    rhoai | managed-rhoai)
        echo "Downloading manifests for RHOAI"
        REPO_URL="https://github.com/red-hat-data-services/feast"
        COMMIT_SHA="95d67f02ffbc9c722482a148cf2b7fbe4aa51766"
        ;;
    *)
        echo "invalid ODH_PLATFORM_TYPE '${platform_type}' (expected odh, OpenDataHub, rhoai, or managed-rhoai)" >&2
        exit 1
        ;;
esac

if [[ "${USE_LOCAL:-}" == "true" ]] && [[ -d "${PROJECT_ROOT}/../feast" ]]; then
    echo "Copying manifests from adjacent feast checkout"
    local_commit="$(git -C "${PROJECT_ROOT}/../feast" rev-parse HEAD 2>/dev/null || echo "local-unknown")"
    if [[ -n "$(git -C "${PROJECT_ROOT}/../feast" status --porcelain --untracked-files=all 2>/dev/null)" ]]; then
        local_commit="${local_commit}-dirty"
    fi
    rm -rf "${DST_MANIFESTS_DIR}"
    mkdir -p "${DST_MANIFESTS_DIR}"
    cp -a "${PROJECT_ROOT}/../feast/${SOURCE_PATH}/." "${DST_MANIFESTS_DIR}/"
    echo "${local_commit}" > "${DST_MANIFESTS_DIR}/.manifest-source-commit"
    manifest_digest "${DST_MANIFESTS_DIR}" > "${DIGEST_FILE}"
    echo "Manifests copied to ${DST_MANIFESTS_DIR} (local commit: ${local_commit})"
    exit 0
fi

if [[ "${FORCE_GET_MANIFESTS:-}" != "true" && -f "${MARKER_FILE}" ]]; then
    if [[ "$(tr -d '[:space:]' < "${MARKER_FILE}")" == "${COMMIT_SHA}" ]]; then
        if [[ -f "${DIGEST_FILE}" && -f "${DST_MANIFESTS_DIR}/manager/manager.yaml" \
            && "$(tr -d '[:space:]' < "${DIGEST_FILE}")" == "$(manifest_digest "${DST_MANIFESTS_DIR}")" ]]; then
            echo "Manifests already at ${COMMIT_SHA} with verified content, skipping download"
            exit 0
        fi
        echo "WARNING: bundled manifest content does not match its recorded digest; re-fetching" >&2
    else
        echo "WARNING: bundled manifests were fetched at $(cat "${MARKER_FILE}"), expected ${COMMIT_SHA}" >&2
        echo "Re-fetching to match expected commit (set FORCE_GET_MANIFESTS=true to always refresh)"
    fi
elif [[ "${FORCE_GET_MANIFESTS:-}" != "true" && -f "${DST_MANIFESTS_DIR}/manager/manager.yaml" ]]; then
    echo "WARNING: manifests present but no provenance marker; re-fetching to ensure correct version" >&2
fi

TMP_DIR=$(mktemp -d -t "odh-feast-manifests.XXXXXXXXXX")
trap 'rm -rf -- "${TMP_DIR}"' EXIT

git_fetch_with_retry() {
    local attempt=1 max_attempts=3
    while [[ "${attempt}" -le "${max_attempts}" ]]; do
        if git -C "${TMP_DIR}" fetch --depth 1 origin "${COMMIT_SHA}"; then
            return 0
        fi
        echo "git fetch failed (attempt ${attempt}/${max_attempts})" >&2
        if [[ "${attempt}" -lt "${max_attempts}" ]]; then
            sleep $((attempt * 5))
        fi
        attempt=$((attempt + 1))
    done
    return 1
}

echo "Fetching ${REPO_URL}@${COMMIT_SHA} ..."
echo "(shallow git clone — usually 30-90s; retries on network blips)"

git -C "${TMP_DIR}" init -q
git -C "${TMP_DIR}" remote add origin "${REPO_URL}"
if ! git_fetch_with_retry; then
    echo "ERROR: could not fetch manifests from GitHub after 3 attempts." >&2
    echo "  - Bundled manifests already in repo? Run: SKIP_GET_MANIFESTS=1 make deploy-openshift" >&2
    echo "  - Or use local feast checkout: USE_LOCAL=true ./hack/scripts/get-manifests.sh" >&2
    exit 1
fi
git -C "${TMP_DIR}" reset -q --hard "${COMMIT_SHA}"

rm -rf "${DST_MANIFESTS_DIR}"
mkdir -p "${DST_MANIFESTS_DIR}"
cp -a "${TMP_DIR}/${SOURCE_PATH}/." "${DST_MANIFESTS_DIR}/"
echo "${COMMIT_SHA}" > "${MARKER_FILE}"
manifest_digest "${DST_MANIFESTS_DIR}" > "${DIGEST_FILE}"

echo "Manifests downloaded to ${DST_MANIFESTS_DIR}"
