package engine

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/k8s-manifest-kit/pkg/util/metrics"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/k8s-manifest-kit/engine/pkg/pipeline"
	"github.com/k8s-manifest-kit/engine/pkg/render"
	"github.com/k8s-manifest-kit/engine/pkg/types"
)

// Engine represents the core manifest rendering and processing engine.
type Engine struct {
	options Options
}

// New creates a new Engine with the given options.
func New(opts ...Option) (*Engine, error) {
	options := Options{
		Renderers:     make([]types.Renderer, 0),
		Filters:       make([]types.Filter, 0),
		Transformers:  make([]types.Transformer, 0),
		PostRenderers: make([]types.PostRenderer, 0),
	}

	for _, opt := range opts {
		opt.ApplyTo(&options)
	}

	for _, renderer := range options.Renderers {
		if err := types.ValidateRenderer(renderer); err != nil {
			return nil, fmt.Errorf("invalid renderer: %w", err)
		}
	}

	e := Engine{
		options: options,
	}

	return &e, nil
}

// Render processes all inputs associated with the registered renderer configurations
// and returns a consolidated slice of unstructured.Unstructured objects.
//
// The rendering pipeline:
//  1. Render-time options are merged with engine-level options
//  2. Each renderer executes Process() with deep-cloned values
//  3. Global Filters -> Transformers -> PostRenderers are applied to combined output
//
// Render-time options are additive - they append to engine-level options.
// Render-time values are passed to all renderers and deep merged with Source-level values.
func (e *Engine) Render(ctx context.Context, opts ...render.Option) ([]unstructured.Unstructured, error) {
	startTime := time.Now()

	renderOpts := render.Options{
		Filters:       slices.Clone(e.options.Filters),
		Transformers:  slices.Clone(e.options.Transformers),
		PostRenderers: slices.Clone(e.options.PostRenderers),
		Values:        make(types.Values),
	}

	for _, opt := range opts {
		opt.ApplyTo(&renderOpts)
	}

	allObjects := make([]unstructured.Unstructured, 0)

	for _, renderer := range e.options.Renderers {
		rValues := renderOpts.Values.DeepClone()

		objects, err := e.processRenderer(ctx, renderer, rValues)
		if err != nil {
			return nil, fmt.Errorf("rendering failed: %w", err)
		}

		allObjects = append(allObjects, objects...)
	}

	chain := types.BuildPostRendererChain(
		renderOpts.Filters,
		renderOpts.Transformers,
		renderOpts.PostRenderers,
	)

	result, err := pipeline.ApplyPostRenderers(ctx, allObjects, chain)
	if err != nil {
		return nil, fmt.Errorf("engine post-renderer error: %w", err)
	}

	metrics.ObserveRender(ctx, time.Since(startTime), len(result))

	return result, nil
}

// processRenderer executes a single renderer with timing, metrics, and error handling.
func (e *Engine) processRenderer(
	ctx context.Context,
	renderer types.Renderer,
	values types.Values,
) ([]unstructured.Unstructured, error) {
	startTime := time.Now()
	objects, err := renderer.Process(ctx, values)

	metrics.ObserveRenderer(ctx, renderer.Name(), time.Since(startTime), len(objects), err)

	if err != nil {
		return nil, fmt.Errorf(
			"error processing renderer %q (%T): %w",
			renderer.Name(),
			renderer,
			err,
		)
	}

	return objects, nil
}
