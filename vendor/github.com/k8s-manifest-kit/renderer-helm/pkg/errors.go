package helm

import "errors"

// ValidationError indicates invalid input that cannot be retried.
// The inner Err typically wraps one of the sentinel errors
// (ErrChartEmpty, ErrReleaseNameEmpty, etc.).
type ValidationError struct {
	Field string
	Err   error
}

func (e *ValidationError) Error() string {
	return e.Err.Error()
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}

// LocateError indicates a failure to locate or download a chart.
// These errors are potentially retryable (e.g. transient network failures).
type LocateError struct {
	Chart   string
	Repo    string
	Version string
	Err     error
}

func (e *LocateError) Error() string {
	return e.Err.Error()
}

func (e *LocateError) Unwrap() error {
	return e.Err
}

// RenderError indicates a failure during template rendering or YAML decoding.
// These errors are terminal and not retryable.
type RenderError struct {
	Chart       string
	ReleaseName string
	Err         error
}

func (e *RenderError) Error() string {
	return e.Err.Error()
}

func (e *RenderError) Unwrap() error {
	return e.Err
}

// IsValidationError reports whether err or any error in its chain is a *ValidationError.
func IsValidationError(err error) bool {
	var target *ValidationError

	return errors.As(err, &target)
}

// IsLocateError reports whether err or any error in its chain is a *LocateError.
func IsLocateError(err error) bool {
	var target *LocateError

	return errors.As(err, &target)
}

// IsRenderError reports whether err or any error in its chain is a *RenderError.
func IsRenderError(err error) bool {
	var target *RenderError

	return errors.As(err, &target)
}
