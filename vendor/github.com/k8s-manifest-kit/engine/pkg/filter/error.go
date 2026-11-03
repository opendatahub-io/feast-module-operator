package filter

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Error represents an error that occurred during filter application.
// It provides context about which object failed and the underlying error.
type Error struct {
	Object unstructured.Unstructured
	Err    error
}

func (e *Error) Error() string {
	return fmt.Sprintf(
		"filter error for %s:%s %s (namespace: %s): %v",
		e.Object.GroupVersionKind().GroupVersion(),
		e.Object.GroupVersionKind().Kind,
		e.Object.GetName(),
		e.Object.GetNamespace(),
		e.Err,
	)
}

func (e *Error) Unwrap() error {
	return e.Err
}

// Wrap wraps an error with filter context.
// If err is already an Error, it returns it as-is to avoid double-wrapping.
// Otherwise, it wraps err in a new Error with the provided object context.
func Wrap(obj unstructured.Unstructured, err error) error {
	if err == nil {
		return nil
	}

	var filterErr *Error
	if errors.As(err, &filterErr) {
		return err
	}

	return &Error{
		Object: obj,
		Err:    err,
	}
}
