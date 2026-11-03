package render

import (
	"github.com/k8s-manifest-kit/pkg/util"

	"github.com/k8s-manifest-kit/engine/pkg/types"
)

// Options represents the processing options for a single Render() call.
type Options struct {
	// Filters are render-time filters applied only to this specific Render() call.
	// These are merged with (appended to) engine-level filters.
	Filters []types.Filter

	// Transformers are render-time transformers applied only to this specific Render() call.
	// These are merged with (appended to) engine-level transformers.
	Transformers []types.Transformer

	// PostRenderers are render-time post-renderers applied only to this specific Render() call.
	// These are merged with (appended to) engine-level post-renderers.
	PostRenderers []types.PostRenderer

	// Values are render-time values passed to all renderers during this specific Render() call.
	// These values are deep merged with Source-level values, with render-time values taking precedence.
	Values types.Values
}

// ApplyTo implements the Option interface for Options.
func (opts Options) ApplyTo(target *Options) {
	target.Filters = append(target.Filters, opts.Filters...)
	target.Transformers = append(target.Transformers, opts.Transformers...)
	target.PostRenderers = append(target.PostRenderers, opts.PostRenderers...)

	if opts.Values != nil {
		target.Values = opts.Values.DeepClone()
	}
}

// Option is a generic option for render-time configuration.
type Option = util.Option[Options]

// WithValues sets render-time values for a single Render() call.
// These values are passed to all renderers and deep merged with Source-level values,
// with render-time values taking precedence for conflicting keys.
func WithValues(values types.Values) Option {
	return util.FunctionalOption[Options](func(o *Options) {
		o.Values = values
	})
}

// WithFilter adds a render-time filter function for a single Render() call.
// Render-time filters are merged with (appended to) engine-level filters.
func WithFilter(f types.Filter) Option {
	return util.FunctionalOption[Options](func(o *Options) {
		o.Filters = append(o.Filters, f)
	})
}

// WithTransformer adds a render-time transformer function for a single Render() call.
// Render-time transformers are merged with (appended to) engine-level transformers.
func WithTransformer(t types.Transformer) Option {
	return util.FunctionalOption[Options](func(o *Options) {
		o.Transformers = append(o.Transformers, t)
	})
}

// WithPostRenderer adds a render-time post-renderer for a single Render() call.
// Render-time post-renderers are merged with (appended to) engine-level post-renderers.
func WithPostRenderer(p types.PostRenderer) Option {
	return util.FunctionalOption[Options](func(o *Options) {
		o.PostRenderers = append(o.PostRenderers, p)
	})
}
