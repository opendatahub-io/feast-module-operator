// Package transformer provides combinators for composing multiple transformers.
package transformer

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/k8s-manifest-kit/engine/pkg/types"
)

// Chain explicitly chains transformers in sequence.
// Each transformer receives the output of the previous transformer.
// If no transformers are provided, returns the object unchanged.
// If any transformer returns an error, the error is returned immediately.
func Chain(transformers ...types.Transformer) types.Transformer {
	return func(ctx context.Context, obj unstructured.Unstructured) (unstructured.Unstructured, error) {
		result := obj

		for _, transformer := range transformers {
			var err error
			result, err = transformer(ctx, result)
			if err != nil {
				return unstructured.Unstructured{}, err
			}
		}

		return result, nil
	}
}

// If applies a transformer conditionally based on a filter.
// If the filter passes, the transformer is applied.
// If the filter fails, the object is returned unchanged.
// If the filter returns an error, the error is returned.
func If(condition types.Filter, transformer types.Transformer) types.Transformer {
	return func(ctx context.Context, obj unstructured.Unstructured) (unstructured.Unstructured, error) {
		ok, err := condition(ctx, obj)
		if err != nil {
			return unstructured.Unstructured{}, err
		}

		if !ok {
			return obj, nil
		}

		return transformer(ctx, obj)
	}
}

// Case represents a conditional branch in a Switch.
type Case struct {
	// When is the condition to check
	When types.Filter
	// Then is the transformer to apply if the condition passes
	Then types.Transformer
}

// Switch applies different transformers based on filter conditions.
// Each case is evaluated in order, and the first matching case's transformer is applied.
// If no cases match and defaultTransformer is provided, it is applied.
// If no cases match and defaultTransformer is nil, the object is returned unchanged.
func Switch(cases []Case, defaultTransformer types.Transformer) types.Transformer {
	return func(ctx context.Context, obj unstructured.Unstructured) (unstructured.Unstructured, error) {
		for _, c := range cases {
			ok, err := c.When(ctx, obj)
			if err != nil {
				return unstructured.Unstructured{}, err
			}

			if ok {
				return c.Then(ctx, obj)
			}
		}

		if defaultTransformer != nil {
			return defaultTransformer(ctx, obj)
		}

		return obj, nil
	}
}
