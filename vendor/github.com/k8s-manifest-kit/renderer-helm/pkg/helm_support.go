package helm

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/k8s-manifest-kit/engine/pkg/types"
	"github.com/k8s-manifest-kit/pkg/util/k8s"
	"github.com/k8s-manifest-kit/renderer-helm/pkg/locator"
)

const (
	// maxReleaseNameLength is the maximum allowed length for a Helm release name.
	// This limit is imposed by Kubernetes label value constraints.
	maxReleaseNameLength = 53

	// releaseNamePattern defines the valid format for Helm release names.
	// Must start and end with lowercase alphanumeric, hyphens allowed in the middle.
	releaseNamePattern = `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
)

var (
	// ErrChartEmpty is returned when a chart name is empty or whitespace-only.
	ErrChartEmpty = errors.New("chart cannot be empty or whitespace-only")

	// ErrReleaseNameEmpty is returned when a release name is empty or whitespace-only.
	ErrReleaseNameEmpty = errors.New("release name cannot be empty or whitespace-only")

	// ErrReleaseNameTooLong is returned when a release name exceeds the maximum length.
	ErrReleaseNameTooLong = errors.New("release name exceeds maximum length")

	// ErrReleaseNameInvalidFormat is returned when a release name contains invalid characters or format.
	ErrReleaseNameInvalidFormat = errors.New(
		"release name must consist of lowercase alphanumeric characters or '-', " +
			"and must start and end with an alphanumeric character",
	)

	// releaseNameRegex is the compiled regex for validating release names.
	releaseNameRegex = regexp.MustCompile(releaseNamePattern)
)

// Values returns a Values function that always returns the provided static values.
// This is a convenience helper for the common case of non-dynamic values.
func Values(values map[string]any) func(context.Context) (types.Values, error) {
	return func(_ context.Context) (types.Values, error) {
		return types.Values(values), nil
	}
}

// StaticCredentials returns a Credentials function that always returns the provided credentials.
// This is a convenience helper for the common case of static authentication.
func StaticCredentials(username string, password string) func(context.Context) (*locator.Credentials, error) {
	return func(_ context.Context) (*locator.Credentials, error) {
		return &locator.Credentials{
			Username: username,
			Password: password,
		}, nil
	}
}

// sourceHolder wraps a Source with internal state for lazy loading and thread-safety.
type sourceHolder struct {
	Source

	// Mutex protects concurrent access to chart field
	mu *sync.RWMutex

	// The loaded Helm chart (protected by mu)
	chart *chart.Chart
}

// Validate checks if the Source configuration is valid.
func (h *sourceHolder) Validate() error {
	if len(strings.TrimSpace(h.Chart)) == 0 {
		return &ValidationError{Field: "Chart", Err: ErrChartEmpty}
	}

	releaseName := strings.TrimSpace(h.ReleaseName)
	switch {
	case len(releaseName) == 0:
		return &ValidationError{Field: "ReleaseName", Err: ErrReleaseNameEmpty}
	case len(releaseName) > maxReleaseNameLength:
		return &ValidationError{
			Field: "ReleaseName",
			Err: fmt.Errorf(
				"%w: must not exceed %d characters (got %d)",
				ErrReleaseNameTooLong,
				maxReleaseNameLength,
				len(releaseName),
			),
		}
	case !releaseNameRegex.MatchString(releaseName):
		return &ValidationError{
			Field: "ReleaseName",
			Err: fmt.Errorf(
				"%w (got %q)",
				ErrReleaseNameInvalidFormat,
				releaseName,
			),
		}
	}

	return nil
}

// LoadChart returns the loaded Helm chart, loading it lazily if needed.
// Thread-safe for concurrent use with optimized read-path performance.
func (h *sourceHolder) LoadChart(
	ctx context.Context,
	repositoryCache string,
) (*chart.Chart, error) {
	// Fast path: read lock for checking if chart is already loaded
	// Multiple goroutines can check concurrently
	h.mu.RLock()
	if h.chart != nil {
		c := h.chart
		h.mu.RUnlock()

		return c, nil
	}
	h.mu.RUnlock()

	// Slow path: write lock for loading chart
	// Only one goroutine loads at a time
	h.mu.Lock()
	defer h.mu.Unlock()

	// Double-check: another goroutine might have loaded while we waited for lock
	if h.chart != nil {
		return h.chart, nil
	}

	// Check context before starting the expensive load operation
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled during chart load: %w", err)
	}

	result, err := locator.Locate(ctx, &locator.Request{
		Name:            h.Chart,
		RepoURL:         h.Repo,
		Version:         h.ReleaseVersion,
		Credentials:     h.Credentials,
		RepositoryCache: repositoryCache,
	})
	if err != nil {
		return nil, &LocateError{
			Chart:   h.Chart,
			Repo:    h.Repo,
			Version: h.ReleaseVersion,
			Err: fmt.Errorf(
				"unable to locate chart (repo: %s, name: %s, version: %s): %w",
				h.Repo,
				h.Chart,
				h.ReleaseVersion,
				err,
			),
		}
	}

	c, err := loader.Load(result.Path)
	if err != nil {
		return nil, &LocateError{
			Chart:   h.Chart,
			Repo:    h.Repo,
			Version: h.ReleaseVersion,
			Err: fmt.Errorf(
				"failed to load chart (repo: %s, name: %s, version: %s): %w",
				h.Repo,
				h.Chart,
				h.ReleaseVersion,
				err,
			),
		}
	}

	h.chart = c

	return h.chart, nil
}

// addContentHash computes and adds a content hash annotation to each object.
// Only modifies objects if content hash is enabled in renderer options.
func (r *Renderer) addContentHash(objects []unstructured.Unstructured) {
	if !r.opts.ContentHash {
		return
	}

	for i := range objects {
		types.SetContentHash(&objects[i])
	}
}

// addSourceAnnotations adds source tracking annotations to a slice of unstructured objects.
// Only modifies objects if source annotations are enabled in renderer options.
func (r *Renderer) addSourceAnnotations(
	objects []unstructured.Unstructured,
	chartPath string,
	fileName string,
) {
	if !r.opts.SourceAnnotations {
		return
	}

	for i := range objects {
		annotations := objects[i].GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}

		annotations[types.AnnotationSourceType] = rendererType
		annotations[types.AnnotationSourcePath] = chartPath
		annotations[types.AnnotationSourceFile] = fileName

		objects[i].SetAnnotations(annotations)
	}
}

// processCRDs extracts and processes CRD objects from a Helm chart.
// Returns the decoded unstructured objects with source annotations added if enabled.
func (r *Renderer) processCRDs(
	helmChart *chart.Chart,
	holder *sourceHolder,
) ([]unstructured.Unstructured, error) {
	result := make([]unstructured.Unstructured, 0)

	for _, crd := range helmChart.CRDObjects() {
		objects, err := k8s.DecodeYAML(crd.File.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to decode CRD %s: %w", crd.Name, err)
		}

		r.addSourceAnnotations(objects, holder.Chart, crd.Name)
		r.addContentHash(objects)
		result = append(result, objects...)
	}

	return result, nil
}

// processRenderedTemplates extracts and processes rendered template files from Helm output.
// Filters for YAML files, decodes them, and adds source annotations if enabled.
func (r *Renderer) processRenderedTemplates(
	files map[string]string,
	holder *sourceHolder,
) ([]unstructured.Unstructured, error) {
	result := make([]unstructured.Unstructured, 0)

	for k, v := range files {
		if !strings.HasSuffix(k, ".yaml") && !strings.HasSuffix(k, ".yml") {
			continue
		}

		objects, err := k8s.DecodeYAML([]byte(v))
		if err != nil {
			return nil, fmt.Errorf(
				"failed to decode %s: %w",
				k,
				err,
			)
		}

		r.addSourceAnnotations(objects, holder.Chart, k)
		r.addContentHash(objects)
		result = append(result, objects...)
	}

	return result, nil
}
