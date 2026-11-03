package cache

import (
	"strconv"

	"k8s.io/apimachinery/pkg/util/dump"
)

// DefaultKeyFunc is the default key conversion function.
// It handles common types efficiently and falls back to reflection-based hashing.
func DefaultKeyFunc(key any) string {
	switch k := key.(type) {
	case string:
		return k
	case []byte:
		return string(k)
	case int:
		return strconv.Itoa(k)
	case int64:
		return strconv.FormatInt(k, 10)
	default:
		return dump.ForHash(key)
	}
}
