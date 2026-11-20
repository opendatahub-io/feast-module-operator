#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
classifier="$repo_root/scripts/classify-e2e-failure.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

expect_classification() {
  local name="$1" expected="$2" platform="$3" evidence="$4"
  local actual
  actual="$("$classifier" "$platform" "$evidence")"
  if [[ "$actual" != "$expected" ]]; then
    echo "not ok - $name: expected $expected, got $actual" >&2
    return 1
  fi
  echo "ok - $name"
}

cat > "$tmp/upstream-signature.log" <<'EOF'
=== e2e failure evidence ===
operand deployment feast-operator-controller-manager
operand deployment args: [--leader-elect --health-probe-bind-address=:8081 --metrics-bind-address=:8443 --metrics-cert-path=/etc/metrics-certs]
--- pod feast-operator-controller-manager-abc/manager current logs ---
flag provided but not defined: -metrics-cert-path
Usage of /manager:
EOF

cat > "$tmp/missing-sa.log" <<'EOF'
=== e2e failure evidence ===
operand deployment feast-operator-controller-manager
Warning FailedMount MountVolume.SetUp failed for volume "kube-api-access": failed to fetch token: serviceaccounts "feast-operator-controller-manager" not found
EOF

cat > "$tmp/unrelated.log" <<'EOF'
=== e2e failure evidence ===
Timed out after 90.001s waiting for the module to become Ready.
operand deployment feast-operator-controller-manager
Error: an unrelated API failure
EOF

expect_classification upstream-signature expected-upstream-metrics-cert-path rhoai "$tmp/upstream-signature.log"
expect_classification missing-sa unexpected rhoai "$tmp/missing-sa.log"
expect_classification unrelated unexpected rhoai "$tmp/unrelated.log"
expect_classification missing-evidence missing-evidence rhoai "$tmp/does-not-exist.log"
expect_classification wrong-platform unexpected-platform odh "$tmp/upstream-signature.log"

echo "e2e failure classification tests passed"
