package cache

import (
	"time"

	"github.com/k8s-manifest-kit/pkg/util"
)

// Option is a generic option for Cache.
type Option = util.Option[Options]

// Options is a struct-based option that can set cache options.
type Options struct {
	// TTL is the time-to-live for cache entries.
	TTL time.Duration

	// KeyFunc converts cache keys to strings for internal storage.
	// If nil, uses DefaultKeyFunc.
	KeyFunc func(any) string
}

// ApplyTo applies the cache options to the target configuration.
func (opts Options) ApplyTo(target *Options) {
	if opts.TTL > 0 {
		target.TTL = opts.TTL
	}
	if opts.KeyFunc != nil {
		target.KeyFunc = opts.KeyFunc
	}
}

// WithTTL sets the time-to-live for cache entries.
func WithTTL(ttl time.Duration) Option {
	return util.FunctionalOption[Options](func(opts *Options) {
		opts.TTL = ttl
	})
}

// WithKeyFunc sets the function used to convert cache keys to strings.
// The KeyFunc receives the key passed to Get/Set and must return a string for internal storage.
//
// Example:
//
//	cache.WithKeyFunc(func(key any) string {
//	    if spec, ok := key.(MySpec); ok {
//	        return spec.Path
//	    }
//	    return dump.ForHash(key)
//	})
func WithKeyFunc(fn func(any) string) Option {
	return util.FunctionalOption[Options](func(opts *Options) {
		opts.KeyFunc = fn
	})
}
