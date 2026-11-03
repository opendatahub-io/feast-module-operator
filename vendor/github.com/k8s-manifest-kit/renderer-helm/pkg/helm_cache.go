package helm

import (
	"fmt"

	"helm.sh/helm/v4/pkg/chart/common"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/k8s-manifest-kit/pkg/util/cache"
)

// chartSpec contains the data used to generate cache keys for rendered charts.
type chartSpec struct {
	Chart          string
	ReleaseName    string
	ReleaseVersion string
	Values         common.Values
}

// FastCacheKeyFunc generates cache keys based only on chart identity, ignoring values.
// This provides significantly better cache performance but means all renders of the
// same chart+release+version will share cached results regardless of values.
//
// Use this when:
//   - Values are static and don't change between renders
//   - You accept that value changes won't trigger re-rendering from cache
//
// Usage:
//
//	helm.WithCache(cache.WithKeyFunc(helm.FastCacheKeyFunc))
func FastCacheKeyFunc(key any) string {
	if spec, ok := key.(chartSpec); ok {
		return fmt.Sprintf("%s:%s:%s", spec.Chart, spec.ReleaseName, spec.ReleaseVersion)
	}

	return ""
}

// newCache creates a cache instance with Helm-specific default KeyFunc.
func newCache(opts *cache.Options) cache.Interface[[]unstructured.Unstructured] {
	if opts == nil {
		return nil
	}

	co := *opts

	// Inject default KeyFunc for Helm charts
	if co.KeyFunc == nil {
		co.KeyFunc = cache.DefaultKeyFunc
	}

	return cache.NewRenderCache(co)
}
