package helm

import (
	"context"
	"fmt"
	"sync"

	"helm.sh/helm/v4/pkg/chart/common"
	commonutil "helm.sh/helm/v4/pkg/chart/common/util"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/engine"
	"helm.sh/helm/v4/pkg/helmpath"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/k8s-manifest-kit/engine/pkg/pipeline"
	"github.com/k8s-manifest-kit/engine/pkg/types"
	"github.com/k8s-manifest-kit/pkg/util/cache"
	"github.com/k8s-manifest-kit/pkg/util/maps"
	"github.com/k8s-manifest-kit/renderer-helm/pkg/locator"
)

const rendererType = "helm"

// Source defines a Helm chart source for rendering.
type Source struct {
	// Repo is the repository URL for chart lookup. Optional for local or OCI charts.
	Repo string

	// Chart specifies the chart to render. Supports OCI references (oci://registry/chart:tag)
	// or local filesystem paths. Required.
	Chart string

	// ReleaseName is the Helm release name used in template rendering metadata.
	// Required for proper .Release.Name substitution in templates.
	ReleaseName string

	// ReleaseNamespace is the Kubernetes namespace for the release.
	// Used for .Release.Namespace substitution in templates. Optional; defaults to empty string.
	ReleaseNamespace string

	// ReleaseVersion constrains the chart version to fetch. Optional; uses latest if empty.
	ReleaseVersion string

	// Values provides template variable overrides during chart rendering.
	// Function is called during rendering to obtain dynamic values.
	// Merged with chart defaults via chartutil.ToRenderValues.
	Values func(context.Context) (types.Values, error)

	// Credentials provides authentication credentials for accessing the chart.
	// Function is called during chart loading to obtain credentials dynamically.
	// Optional; only needed for authenticated registries/repositories.
	Credentials func(context.Context) (*locator.Credentials, error)

	// ProcessDependencies determines whether chart dependencies should be processed.
	// If true, chartutil.ProcessDependencies will be called during rendering.
	// Default is false.
	ProcessDependencies bool

	// PostRenderers are source-specific post-renderers applied to this source's output
	// before combining with other sources.
	PostRenderers []types.PostRenderer
}

// SourceSelector decides whether a Source should be rendered.
// It receives the context and the source, and returns true to include
// the source or false to skip it. Evaluated before rendering.
type SourceSelector = func(ctx context.Context, source Source) (bool, error)

// Renderer handles Helm rendering operations.
// It implements types.Renderer.
//
// Thread-safety: Renderer is safe for concurrent use. Multiple goroutines
// may call Process() concurrently on the same Renderer instance. Chart loading
// is protected by per-Source mutexes to ensure thread-safe lazy initialization.
type Renderer struct {
	sources    []*sourceHolder
	helmEngine engine.Engine
	opts       RendererOptions
	cache      cache.Interface[[]unstructured.Unstructured]
}

// New creates a new Helm Renderer with the given inputs and options.
func New(inputs []Source, opts ...RendererOption) (*Renderer, error) {
	rendererOpts := RendererOptions{
		Filters:          make([]types.Filter, 0),
		Transformers:     make([]types.Transformer, 0),
		ContentHash:      true,
		RepositoryConfig: helmpath.ConfigPath("repositories.yaml"),
		RepositoryCache:  helmpath.CachePath("repository"),
		ContentCache:     helmpath.CachePath("content"),
	}

	for _, opt := range opts {
		opt.ApplyTo(&rendererOpts)
	}

	holders := make([]*sourceHolder, len(inputs))
	for i := range inputs {
		holders[i] = &sourceHolder{
			Source: inputs[i],
			mu:     &sync.RWMutex{},
		}
		if err := holders[i].Validate(); err != nil {
			return nil, err
		}
	}

	r := &Renderer{
		sources: holders,
		helmEngine: engine.Engine{
			LintMode: rendererOpts.LintMode,
			Strict:   rendererOpts.Strict,
		},
		opts:  rendererOpts,
		cache: newCache(rendererOpts.CacheOptions),
	}

	return r, nil
}

// Process executes the rendering logic for all configured sources.
// It implements the types.Renderer interface.
// This method is safe for concurrent use.
func (r *Renderer) Process(ctx context.Context, renderTimeValues types.Values) ([]unstructured.Unstructured, error) {
	allObjects := make([]unstructured.Unstructured, 0)

	for i := range r.sources {
		selected, err := pipeline.ApplySourceSelectors(ctx, r.sources[i].Source, r.opts.SourceSelectors)
		if err != nil {
			return nil, fmt.Errorf(
				"source selector error for helm chart %s (release: %s): %w",
				r.sources[i].Chart,
				r.sources[i].ReleaseName,
				err,
			)
		}

		if !selected {
			continue
		}

		sValues := renderTimeValues.DeepClone()

		objects, err := r.processSingle(ctx, r.sources[i], sValues)
		if err != nil {
			return nil, fmt.Errorf(
				"error rendering helm chart %s (release: %s): %w",
				r.sources[i].Chart,
				r.sources[i].ReleaseName,
				err,
			)
		}

		objects, err = pipeline.ApplyPostRenderers(ctx, objects, r.sources[i].PostRenderers)
		if err != nil {
			return nil, fmt.Errorf(
				"source post-renderer error for helm chart %s (release: %s): %w",
				r.sources[i].Chart,
				r.sources[i].ReleaseName,
				err,
			)
		}

		allObjects = append(allObjects, objects...)
	}

	chain := types.BuildPostRendererChain(r.opts.Filters, r.opts.Transformers, r.opts.PostRenderers)

	result, err := pipeline.ApplyPostRenderers(ctx, allObjects, chain)
	if err != nil {
		return nil, fmt.Errorf("renderer post-renderer error: %w", err)
	}

	return result, nil
}

// Name returns the renderer type identifier.
func (r *Renderer) Name() string {
	return rendererType
}

func (r *Renderer) values(
	ctx context.Context,
	holder *sourceHolder,
	renderTimeValues types.Values,
) (types.Values, error) {
	sourceValues := types.Values{}

	if holder.Values != nil {
		v, err := holder.Values(ctx)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to get values for chart %q (release %q): %w",
				holder.Chart,
				holder.ReleaseName,
				err,
			)
		}

		if v != nil {
			sourceValues = v
		}
	}

	return types.Values(maps.DeepMerge(map[string]any(sourceValues), map[string]any(renderTimeValues))), nil
}

// processValues gets values from the Values function, processes dependencies,
// and prepares render values using chartutil.ToRenderValues.
func (r *Renderer) processValues(
	ctx context.Context,
	holder *sourceHolder,
	renderTimeValues types.Values,
) (common.Values, error) {
	values, err := r.values(ctx, holder, renderTimeValues)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get values for chart %q (release %q): %w",
			holder.Chart,
			holder.ReleaseName,
			err,
		)
	}

	if holder.ProcessDependencies {
		if err := chartutil.ProcessDependencies(holder.chart, map[string]any(values)); err != nil {
			return nil, fmt.Errorf(
				"failed to process dependencies for chart %q (release %q): %w",
				holder.Chart,
				holder.ReleaseName,
				err,
			)
		}
	}

	renderValues, err := commonutil.ToRenderValues(
		holder.chart,
		map[string]any(values),
		common.ReleaseOptions{
			Name:      holder.ReleaseName,
			Namespace: holder.ReleaseNamespace,
			Revision:  1,
			IsInstall: true,
		},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to prepare render values for chart %q (release %q): %w",
			holder.Chart,
			holder.ReleaseName,
			err,
		)
	}

	return renderValues, nil
}

// processSingle performs the rendering for a single Helm chart.
// It processes dependencies, prepares render values, renders the templates,
// and converts the output to unstructured objects.
func (r *Renderer) processSingle(
	ctx context.Context,
	holder *sourceHolder,
	renderTimeValues types.Values,
) ([]unstructured.Unstructured, error) {
	// Load chart if not already loaded (thread-safe lazy loading)
	chart, err := holder.LoadChart(ctx, r.opts.RepositoryCache)
	if err != nil {
		return nil, err
	}

	renderValues, err := r.processValues(ctx, holder, renderTimeValues)
	if err != nil {
		return nil, &RenderError{Chart: holder.Chart, ReleaseName: holder.ReleaseName, Err: err}
	}

	spec := chartSpec{
		Chart:          holder.Chart,
		ReleaseName:    holder.ReleaseName,
		ReleaseVersion: holder.ReleaseVersion,
		Values:         renderValues,
	}

	// Check cache (if enabled)
	if r.cache != nil {
		// ensure objects are evicted
		r.cache.Sync()

		if cached, found := r.cache.Get(spec); found {
			return cached, nil
		}
	}

	// Check context before expensive render operation
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before render: %w", err)
	}

	files, err := r.helmEngine.Render(chart, renderValues)
	if err != nil {
		return nil, &RenderError{
			Chart:       holder.Chart,
			ReleaseName: holder.ReleaseName,
			Err:         fmt.Errorf("failed to render chart %q (release %q): %w", holder.Chart, holder.ReleaseName, err),
		}
	}

	result := make([]unstructured.Unstructured, 0)

	// Process CRDs before other resources to ensure custom resource definitions
	// are available if any rendered templates reference custom resources
	crdObjects, err := r.processCRDs(chart, holder)
	if err != nil {
		return nil, &RenderError{Chart: holder.Chart, ReleaseName: holder.ReleaseName, Err: err}
	}
	result = append(result, crdObjects...)

	templateObjects, err := r.processRenderedTemplates(files, holder)
	if err != nil {
		return nil, &RenderError{Chart: holder.Chart, ReleaseName: holder.ReleaseName, Err: err}
	}
	result = append(result, templateObjects...)

	if r.cache != nil {
		r.cache.Set(spec, result)
	}

	return result, nil
}
