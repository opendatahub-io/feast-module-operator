package k8s

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"k8s.io/utils/dump"
)

// ContentHash computes a deterministic SHA-256 hash of a Kubernetes Object.
// It uses k8s.io/utils/dump.ForHash for serialization, which is the same mechanism
// Kubernetes uses internally (via DeepHashObject).
// The returned string is prefixed with "sha256:" following the convention used
// by container image digests, making the value self-describing.
func ContentHash(obj Object) string {
	hasher := sha256.New()
	_, _ = fmt.Fprintf(hasher, "%v", dump.ForHash(obj))

	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}
