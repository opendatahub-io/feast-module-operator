package util

// Option is a generic interface for functional options.
type Option[T any] interface {
	ApplyTo(target *T)
}

// FunctionalOption is a generic functional option type.
type FunctionalOption[T any] func(*T)

// ApplyTo applies the functional option to the target configuration.
func (f FunctionalOption[T]) ApplyTo(target *T) {
	f(target)
}
