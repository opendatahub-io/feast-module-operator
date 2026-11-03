package helm

import (
	"github.com/k8s-manifest-kit/engine/pkg/types"
	"github.com/k8s-manifest-kit/pkg/util"
	"github.com/k8s-manifest-kit/pkg/util/cache"
)

// RendererOption is a generic option for RendererOptions.
type RendererOption = util.Option[RendererOptions]

// RendererOptions is a struct-based option that can set multiple renderer options at once.
type RendererOptions struct {
	// Filters are renderer-specific filters applied during Process().
	Filters []types.Filter

	// Transformers are renderer-specific transformers applied during Process().
	Transformers []types.Transformer

	// PostRenderers are renderer-specific post-renderers applied during Process().
	PostRenderers []types.PostRenderer

	// SourceSelectors are renderer-specific source selectors evaluated before rendering each source.
	SourceSelectors []SourceSelector

	// RepositoryConfig is the path to the repositories file.
	RepositoryConfig string

	// RepositoryCache is the path to the repository cache directory.
	RepositoryCache string

	// ContentCache is the path to the content-addressable cache directory.
	ContentCache string

	// CacheOptions holds cache configuration. nil = caching disabled.
	CacheOptions *cache.Options

	// SourceAnnotations enables automatic addition of source tracking annotations.
	SourceAnnotations bool

	// ContentHash enables automatic addition of a SHA-256 content hash annotation.
	// Default: true (enabled).
	ContentHash bool

	// LintMode allows some 'required' template values to be missing without failing.
	// This is useful during linting when not all values are available.
	LintMode bool

	// Strict enables strict template rendering mode.
	// When enabled, template rendering will fail if a template references a value that was not passed in.
	Strict bool
}

// ApplyTo applies the renderer options to the target configuration.
func (opts RendererOptions) ApplyTo(target *RendererOptions) {
	target.Filters = opts.Filters
	target.Transformers = opts.Transformers
	target.PostRenderers = append(target.PostRenderers, opts.PostRenderers...)
	target.SourceSelectors = append(target.SourceSelectors, opts.SourceSelectors...)

	if opts.RepositoryConfig != "" {
		target.RepositoryConfig = opts.RepositoryConfig
	}

	if opts.RepositoryCache != "" {
		target.RepositoryCache = opts.RepositoryCache
	}

	if opts.ContentCache != "" {
		target.ContentCache = opts.ContentCache
	}

	if opts.CacheOptions != nil {
		if target.CacheOptions == nil {
			target.CacheOptions = &cache.Options{}
		}
		opts.CacheOptions.ApplyTo(target.CacheOptions)
	}

	target.SourceAnnotations = opts.SourceAnnotations
	target.ContentHash = opts.ContentHash
	target.LintMode = opts.LintMode
	target.Strict = opts.Strict
}

// WithFilter adds a renderer-specific filter to this Helm renderer's processing chain.
func WithFilter(f types.Filter) RendererOption {
	return util.FunctionalOption[RendererOptions](func(opts *RendererOptions) {
		opts.Filters = append(opts.Filters, f)
	})
}

// WithTransformer adds a renderer-specific transformer to this Helm renderer's processing chain.
func WithTransformer(t types.Transformer) RendererOption {
	return util.FunctionalOption[RendererOptions](func(opts *RendererOptions) {
		opts.Transformers = append(opts.Transformers, t)
	})
}

// WithPostRenderer adds a renderer-specific post-renderer to this Helm renderer's processing chain.
// Post-renderers run after all sources have been rendered and combined, after Filters and Transformers.
func WithPostRenderer(p types.PostRenderer) RendererOption {
	return util.FunctionalOption[RendererOptions](func(opts *RendererOptions) {
		opts.PostRenderers = append(opts.PostRenderers, p)
	})
}

// WithSourceSelector adds a source selector to this Helm renderer.
// Source selectors are evaluated before rendering each source. If any selector
// returns false, the source is skipped entirely.
func WithSourceSelector(s SourceSelector) RendererOption {
	return util.FunctionalOption[RendererOptions](func(opts *RendererOptions) {
		opts.SourceSelectors = append(opts.SourceSelectors, s)
	})
}

// WithRepositoryConfig sets the path to the repositories file.
func WithRepositoryConfig(path string) RendererOption {
	return util.FunctionalOption[RendererOptions](func(opts *RendererOptions) {
		opts.RepositoryConfig = path
	})
}

// WithRepositoryCache sets the path to the repository cache directory.
func WithRepositoryCache(path string) RendererOption {
	return util.FunctionalOption[RendererOptions](func(opts *RendererOptions) {
		opts.RepositoryCache = path
	})
}

// WithContentCache sets the path to the content-addressable cache directory.
func WithContentCache(path string) RendererOption {
	return util.FunctionalOption[RendererOptions](func(opts *RendererOptions) {
		opts.ContentCache = path
	})
}

// WithCache enables render result caching with the specified options.
func WithCache(opts ...cache.Option) RendererOption {
	return util.FunctionalOption[RendererOptions](func(rendererOpts *RendererOptions) {
		if rendererOpts.CacheOptions == nil {
			rendererOpts.CacheOptions = &cache.Options{}
		}

		for _, opt := range opts {
			opt.ApplyTo(rendererOpts.CacheOptions)
		}
	})
}

// WithSourceAnnotations enables or disables automatic addition of source tracking annotations.
func WithSourceAnnotations(enabled bool) RendererOption {
	return util.FunctionalOption[RendererOptions](func(opts *RendererOptions) {
		opts.SourceAnnotations = enabled
	})
}

// WithContentHash enables or disables automatic addition of a SHA-256 content hash annotation.
func WithContentHash(enabled bool) RendererOption {
	return util.FunctionalOption[RendererOptions](func(opts *RendererOptions) {
		opts.ContentHash = enabled
	})
}

// WithLintMode enables or disables lint mode during template rendering.
func WithLintMode(enabled bool) RendererOption {
	return util.FunctionalOption[RendererOptions](func(opts *RendererOptions) {
		opts.LintMode = enabled
	})
}

// WithStrict enables or disables strict template rendering mode.
func WithStrict(enabled bool) RendererOption {
	return util.FunctionalOption[RendererOptions](func(opts *RendererOptions) {
		opts.Strict = enabled
	})
}
