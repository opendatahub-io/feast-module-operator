package cache

import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	defaultTTL = 5 * time.Minute
)

// Interface is a generic cache interface with TTL-based expiration.
type Interface[T any] interface {
	// Get retrieves a cached value for the given key.
	// The key can be any type and will be converted to a string using the configured KeyFunc.
	// Returns the cached value and true if found and not expired, or the zero value and false otherwise.
	Get(key any) (T, bool)

	// Set stores a value for the given key.
	// The key can be any type and will be converted to a string using the configured KeyFunc.
	// The entry will automatically expire after the configured TTL.
	Set(key any, value T)

	// Sync removes all expired entries from the cache.
	Sync()
}

type entry[T any] struct {
	value      T
	expiration time.Time
}

// defaultCache is the default implementation of Interface[T].
type defaultCache[T any] struct {
	mu      sync.RWMutex
	entries map[string]entry[T]
	ttl     time.Duration
	keyFunc func(any) string
}

// New creates a new cache with the given options.
// If no TTL is specified, defaults to 5 minutes.
// If no KeyFunc is specified, uses DefaultKeyFunc.
func New[T any](opts ...Option) Interface[T] {
	options := Options{
		TTL:     defaultTTL,
		KeyFunc: DefaultKeyFunc,
	}

	for _, opt := range opts {
		opt.ApplyTo(&options)
	}

	if options.TTL <= 0 {
		options.TTL = defaultTTL
	}

	if options.KeyFunc == nil {
		options.KeyFunc = DefaultKeyFunc
	}

	return &defaultCache[T]{
		entries: make(map[string]entry[T]),
		ttl:     options.TTL,
		keyFunc: options.KeyFunc,
	}
}

func (c *defaultCache[T]) Get(key any) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	strKey := c.keyFunc(key)
	val, exists := c.entries[strKey]
	if !exists {
		var zero T

		return zero, false
	}

	if time.Now().After(val.expiration) {
		var zero T

		return zero, false
	}

	return val.value, true
}

func (c *defaultCache[T]) Set(key any, val T) {
	c.mu.Lock()
	defer c.mu.Unlock()

	strKey := c.keyFunc(key)
	c.entries[strKey] = entry[T]{
		value:      val,
		expiration: time.Now().Add(c.ttl),
	}
}

// Sync removes all expired entries from the cache.
//
// Note: Expired entries may still be briefly returned by Get() before Sync() is called,
// as Get() performs lazy expiration checking (returns false for expired entries without
// removing them).
//
// This is intentional for performance - avoiding write locks on every Get().
func (c *defaultCache[T]) Sync() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, val := range c.entries {
		if now.After(val.expiration) {
			delete(c.entries, key)
		}
	}
}

// renderCache wraps a cache and automatically deep clones unstructured slices on get/set.
type renderCache struct {
	cache Interface[[]unstructured.Unstructured]
}

// NewRenderCache creates a new cache for rendering results with automatic deep cloning.
// Entries are deep cloned when stored and when retrieved to prevent cache pollution.
func NewRenderCache(opts ...Option) Interface[[]unstructured.Unstructured] {
	return &renderCache{
		cache: New[[]unstructured.Unstructured](opts...),
	}
}

func (r *renderCache) Get(key any) ([]unstructured.Unstructured, bool) {
	if r == nil || r.cache == nil {
		return nil, false
	}

	cached, found := r.cache.Get(key)
	if !found {
		return nil, false
	}

	if cached == nil {
		return nil, true
	}

	result := make([]unstructured.Unstructured, len(cached))
	for i, obj := range cached {
		result[i] = *obj.DeepCopy()
	}

	return result, true
}

func (r *renderCache) Set(key any, value []unstructured.Unstructured) {
	if r == nil || r.cache == nil {
		return
	}

	if value == nil {
		r.cache.Set(key, nil)

		return
	}

	cloned := make([]unstructured.Unstructured, len(value))
	for i, obj := range value {
		cloned[i] = *obj.DeepCopy()
	}

	r.cache.Set(key, cloned)
}

func (r *renderCache) Sync() {
	if r == nil || r.cache == nil {
		return
	}

	r.cache.Sync()
}
