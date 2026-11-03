// Package filter provides combinators for composing multiple filters using boolean logic.
package filter

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/k8s-manifest-kit/engine/pkg/types"
)

// Or returns a filter that passes if ANY of the provided filters pass.
// If no filters are provided, it returns a filter that always passes.
// If any filter returns an error, the error is returned immediately.
func Or(filters ...types.Filter) types.Filter {
	return func(ctx context.Context, obj unstructured.Unstructured) (bool, error) {
		if len(filters) == 0 {
			return true, nil
		}

		for _, filter := range filters {
			ok, err := filter(ctx, obj)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}

		return false, nil
	}
}

// And returns a filter that passes if ALL of the provided filters pass.
// If no filters are provided, it returns a filter that always passes.
// If any filter returns an error, the error is returned immediately.
func And(filters ...types.Filter) types.Filter {
	return func(ctx context.Context, obj unstructured.Unstructured) (bool, error) {
		if len(filters) == 0 {
			return true, nil
		}

		for _, filter := range filters {
			ok, err := filter(ctx, obj)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}

		return true, nil
	}
}

// Not inverts the result of the provided filter.
// If the filter returns an error, the error is returned unchanged.
func Not(filter types.Filter) types.Filter {
	return func(ctx context.Context, obj unstructured.Unstructured) (bool, error) {
		ok, err := filter(ctx, obj)
		if err != nil {
			return false, err
		}

		return !ok, nil
	}
}

// If applies a filter conditionally.
// If the condition passes, the then filter is applied.
// If the condition fails, the object passes through (returns true).
// This is useful for applying filters only to specific object types or attributes.
func If(condition types.Filter, then types.Filter) types.Filter {
	return func(ctx context.Context, obj unstructured.Unstructured) (bool, error) {
		ok, err := condition(ctx, obj)
		if err != nil {
			return false, err
		}

		if !ok {
			return true, nil
		}

		return then(ctx, obj)
	}
}
