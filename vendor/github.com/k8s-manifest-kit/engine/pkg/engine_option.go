package engine

import (
	"github.com/k8s-manifest-kit/pkg/util"

	"github.com/k8s-manifest-kit/engine/pkg/types"
)

// Options represents the processing options for the engine.
type Options struct {
	// Filters are engine-level filters applied to all renders.
	Filters []types.Filter

	// Transformers are engine-level transformers applied to all renders.
	Transformers []types.Transformer

	// PostRenderers are engine-level post-renderers applied to all renders.
	PostRenderers []types.PostRenderer

	// Renderers are the manifest sources to process (e.g., Helm, Kustomize, YAML).
	Renderers []types.Renderer
}

// ApplyTo implements the Option interface for Options.
func (opts Options) ApplyTo(target *Options) {
	target.Renderers = append(target.Renderers, opts.Renderers...)
	target.Filters = append(target.Filters, opts.Filters...)
	target.Transformers = append(target.Transformers, opts.Transformers...)
	target.PostRenderers = append(target.PostRenderers, opts.PostRenderers...)
}

// Option is a generic option for Options.
type Option = util.Option[Options]

// WithRenderer adds a configured renderer to the engine.
// Can only be used during engine creation.
func WithRenderer(r types.Renderer) Option {
	return util.FunctionalOption[Options](func(o *Options) {
		o.Renderers = append(o.Renderers, r)
	})
}

// WithFilter adds an engine-level filter function to the processing chain.
// Engine-level filters are applied to aggregated results from all renderers on every Render() call.
// For renderer-specific filtering, use the renderer's WithFilter option (e.g., helm.WithFilter).
// For one-time filtering on a single Render() call, use render.WithFilter.
func WithFilter(f types.Filter) Option {
	return util.FunctionalOption[Options](func(o *Options) {
		o.Filters = append(o.Filters, f)
	})
}

// WithTransformer adds an engine-level transformer function to the processing chain.
// Engine-level transformers are applied to aggregated results from all renderers on every Render() call.
// For renderer-specific transformation, use the renderer's WithTransformer option (e.g., helm.WithTransformer).
// For one-time transformation on a single Render() call, use render.WithTransformer.
func WithTransformer(t types.Transformer) Option {
	return util.FunctionalOption[Options](func(o *Options) {
		o.Transformers = append(o.Transformers, t)
	})
}

// WithPostRenderer adds an engine-level post-renderer to the processing chain.
// Engine-level post-renderers are applied to aggregated results from all renderers on every Render() call.
// For renderer-specific post-rendering, use the renderer's WithPostRenderer option.
// For one-time post-rendering on a single Render() call, use render.WithPostRenderer.
func WithPostRenderer(p types.PostRenderer) Option {
	return util.FunctionalOption[Options](func(o *Options) {
		o.PostRenderers = append(o.PostRenderers, p)
	})
}
