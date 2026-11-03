package types

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	utilmaps "github.com/k8s-manifest-kit/pkg/util/maps"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	// ErrRendererNil is returned when a renderer is nil.
	ErrRendererNil = errors.New("renderer cannot be nil")

	// ErrRendererNameEmpty is returned when a renderer name is empty.
	ErrRendererNameEmpty = errors.New("renderer must return a non-empty name")
)

// Values represents the rendering values passed through the pipeline.
type Values map[string]any

// Clone returns a shallow copy of the Values map (top-level keys only).
func (v Values) Clone() Values {
	if v == nil {
		return nil
	}

	return maps.Clone(v)
}

// DeepClone returns a fully independent copy of the Values map.
// It recursively copies JSON-like trees:
//   - nested map[string]any
//   - []any slices (including maps and slices contained within them)
//   - common typed slices ([]string, []int, []int64, []float64, []bool)
//   - all other slice types are shallow-copied via reflection
//   - all other types (primitives, strings, structs, pointers) are copied by value
//
// After DeepClone, mutating any level of the returned Values
// (including nested maps and slices) does not affect the original.
//
// Non-JSON types (e.g., pointers, structs with pointer fields) are
// shallow-copied. If Values contain such types and isolation is needed,
// callers must handle those separately.
func (v Values) DeepClone() Values {
	if v == nil {
		return nil
	}

	return utilmaps.DeepCloneMap(v)
}

// PostRenderer runs after rendering at its scope level.
// It receives the full batch of rendered objects and can modify, reorder, validate,
// or enrich them as a whole.
type PostRenderer func(ctx context.Context, objects []unstructured.Unstructured) ([]unstructured.Unstructured, error)

// Filter is a function type that processes a single unstructured.Unstructured object
// and returns true if the object should be kept, or false if it should be discarded.
type Filter func(ctx context.Context, object unstructured.Unstructured) (bool, error)

// Transformer is a function type that processes a single unstructured.Unstructured object
// and returns the transformed object.
type Transformer func(ctx context.Context, object unstructured.Unstructured) (unstructured.Unstructured, error)

// Renderer is a non-generic interface that concrete renderer types implement.
// This allows the Engine to manage them heterogeneously.
type Renderer interface {
	// Process executes the rendering logic for all configured inputs of this renderer.
	// The values parameter contains render-time values from render.WithValues(...).
	// Renderers that support dynamic values (Helm, Kustomize, GoTemplate) should deep merge
	// these values with Source-level values, with render-time values taking precedence.
	Process(ctx context.Context, values Values) ([]unstructured.Unstructured, error)

	// Name returns the renderer type identifier for metrics and logging.
	// Examples: "helm", "kustomize", "gotemplate", "yaml", "mem"
	Name() string
}

// ValidateRenderer checks if a Renderer implementation is valid.
// Returns an error if the renderer is nil or if Name() returns an empty string.
func ValidateRenderer(r Renderer) error {
	if r == nil {
		return ErrRendererNil
	}

	name := r.Name()
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: %T", ErrRendererNameEmpty, r)
	}

	return nil
}

// FilterAsPostRenderer wraps a per-object Filter into a batch-level PostRenderer.
// Mutates the input slice in place — callers must not retain references.
func FilterAsPostRenderer(f Filter) PostRenderer {
	return func(ctx context.Context, objects []unstructured.Unstructured) ([]unstructured.Unstructured, error) {
		n := 0
		for _, obj := range objects {
			keep, err := f(ctx, obj)
			if err != nil {
				return nil, err
			}

			if keep {
				objects[n] = obj
				n++
			}
		}

		return objects[:n], nil
	}
}

// TransformerAsPostRenderer wraps a per-object Transformer into a batch-level PostRenderer.
// Mutates the input slice in place — callers must not retain references.
func TransformerAsPostRenderer(t Transformer) PostRenderer {
	return func(ctx context.Context, objects []unstructured.Unstructured) ([]unstructured.Unstructured, error) {
		for i, obj := range objects {
			transformed, err := t(ctx, obj)
			if err != nil {
				return nil, err
			}

			objects[i] = transformed
		}

		return objects, nil
	}
}

// BuildPostRendererChain concatenates Filters, Transformers, and PostRenderers
// into a single PostRenderer slice. Execution order: Filters first (as PostRenderers),
// then Transformers (as PostRenderers), then explicit PostRenderers.
func BuildPostRendererChain(
	filters []Filter,
	transformers []Transformer,
	postRenderers []PostRenderer,
) []PostRenderer {
	total := len(filters) + len(transformers) + len(postRenderers)
	if total == 0 {
		return nil
	}

	chain := make([]PostRenderer, 0, total)

	for _, f := range filters {
		chain = append(chain, FilterAsPostRenderer(f))
	}

	for _, t := range transformers {
		chain = append(chain, TransformerAsPostRenderer(t))
	}

	chain = append(chain, postRenderers...)

	return chain
}
