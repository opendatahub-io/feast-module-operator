package pipeline

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/k8s-manifest-kit/engine/pkg/filter"
	"github.com/k8s-manifest-kit/engine/pkg/transformer"
	"github.com/k8s-manifest-kit/engine/pkg/types"
)

// ApplySourceSelectors evaluates all source selectors for a given source.
// Returns true if the source should be rendered (all selectors pass).
// Returns false if any selector rejects the source.
// Returns an error if any selector fails.
//
// S is the concrete source type for the renderer (e.g., helm.Source, mem.Source).
// The type parameter is inferred from the call site arguments.
func ApplySourceSelectors[S any](
	ctx context.Context,
	source S,
	selectors []func(context.Context, S) (bool, error),
) (bool, error) {
	for _, selector := range selectors {
		ok, err := selector(ctx, source)
		if err != nil {
			return false, fmt.Errorf("source selector error: %w", err)
		}

		if !ok {
			return false, nil
		}
	}

	return true, nil
}

// ApplyPostRenderers chains post-renderers sequentially, feeding each post-renderer's
// output into the next. Returns the final batch of objects.
func ApplyPostRenderers(
	ctx context.Context,
	objects []unstructured.Unstructured,
	postRenderers []types.PostRenderer,
) ([]unstructured.Unstructured, error) {
	if len(postRenderers) == 0 {
		return objects, nil
	}

	result := objects
	for _, pr := range postRenderers {
		var err error

		result, err = pr(ctx, result)
		if err != nil {
			return nil, fmt.Errorf("post-renderer error: %w", err)
		}
	}

	return result, nil
}

// ApplyFilters applies a series of filters to objects, returning only those that match all filters.
// Returns Error with detailed context if any filter fails.
//
// Deprecated: Use ApplyPostRenderers with types.BuildPostRendererChain instead.
func ApplyFilters(
	ctx context.Context,
	objects []unstructured.Unstructured,
	filters []types.Filter,
) ([]unstructured.Unstructured, error) {
	if len(filters) == 0 {
		return objects, nil
	}

	filtered := make([]unstructured.Unstructured, 0, len(objects))

	for _, obj := range objects {
		matches := true
		for _, f := range filters {
			ok, err := f(ctx, obj)
			if err != nil {
				return nil, filter.Wrap(obj, err)
			}
			if !ok {
				matches = false

				break
			}
		}

		if matches {
			filtered = append(filtered, obj)
		}
	}

	return filtered, nil
}

// ApplyTransformers applies a series of transformers to objects, transforming each object sequentially.
// Returns Error with detailed context if any transformer fails.
//
// Deprecated: Use ApplyPostRenderers with types.BuildPostRendererChain instead.
func ApplyTransformers(
	ctx context.Context,
	objects []unstructured.Unstructured,
	transformers []types.Transformer,
) ([]unstructured.Unstructured, error) {
	if len(transformers) == 0 {
		return objects, nil
	}

	transformed := make([]unstructured.Unstructured, 0, len(objects))

	for _, obj := range objects {
		result := obj
		for _, t := range transformers {
			r, err := t(ctx, result)
			if err != nil {
				return nil, transformer.Wrap(obj, err)
			}
			result = r
		}

		transformed = append(transformed, result)
	}

	return transformed, nil
}

// Apply executes a filter and transformer pipeline on the given objects.
// It applies filters first, then transformers, returning the transformed objects.
// Callers should wrap returned errors with appropriate context.
//
// Deprecated: Use ApplyPostRenderers with types.BuildPostRendererChain instead.
func Apply(
	ctx context.Context,
	objects []unstructured.Unstructured,
	filters []types.Filter,
	transformers []types.Transformer,
) ([]unstructured.Unstructured, error) {
	filtered, err := ApplyFilters(ctx, objects, filters)
	if err != nil {
		return nil, fmt.Errorf("filter error: %w", err)
	}

	transformed, err := ApplyTransformers(ctx, filtered, transformers)
	if err != nil {
		return nil, fmt.Errorf("transformer error: %w", err)
	}

	return transformed, nil
}
