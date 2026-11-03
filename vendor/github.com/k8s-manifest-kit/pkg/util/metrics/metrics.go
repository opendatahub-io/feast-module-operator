package metrics

import (
	"context"
	"time"
)

// RenderMetric observes engine-level render operations.
//
// This interface is called once per Engine.Render() invocation to record
// aggregate metrics across all renderers, filters, and transformers.
//
// Implementations must be thread-safe as renders may be called from multiple goroutines.
type RenderMetric interface {
	// Observe records metrics for a single render operation.
	//
	// Parameters:
	//   - ctx: Context for cancellation and tracing
	//   - duration: Total time for the complete render operation (including all renderers, filters, and transformers)
	//   - objectCount: Total number of Kubernetes objects produced after all processing
	//
	// Example usage:
	//   Observe(ctx, 150*time.Millisecond, 15)
	//   // Records a render that took 150ms and produced 15 objects
	Observe(ctx context.Context, duration time.Duration, objectCount int)
}

// RendererMetric observes individual renderer executions.
//
// This interface is called once per Renderer.Process() invocation to record
// metrics for each specific renderer type (helm, kustomize, gotemplate, yaml, mem).
//
// Implementations must be thread-safe as renderers may be called from multiple goroutines.
type RendererMetric interface {
	// Observe records metrics for a single renderer execution.
	//
	// Parameters:
	//   - ctx: Context for cancellation and tracing
	//   - rendererType: Type of renderer ("helm", "kustomize", "gotemplate", "yaml", "mem")
	//   - duration: Time spent in this renderer's Process() method
	//   - objectCount: Number of objects produced by this renderer (0 if err is non-nil)
	//   - err: Error if the renderer failed, nil on success
	//
	// Example usage (success):
	//   Observe(ctx, "helm", 100*time.Millisecond, 10, nil)
	//   // Records a successful helm render that took 100ms and produced 10 objects
	//
	// Example usage (failure):
	//   Observe(ctx, "kustomize", 50*time.Millisecond, 0, fmt.Errorf("chart not found"))
	//   // Records a failed kustomize render that took 50ms
	Observe(ctx context.Context, rendererType string, duration time.Duration, objectCount int, err error)
}

// Metrics holds all available metrics collectors.
//
// All fields are optional (may be nil). If a field is nil, the corresponding
// ObserveRender or ObserveRenderer helper will safely no-op.
//
// This struct is designed to be attached to a context using WithMetrics() and
// retrieved using FromContext(). This allows metrics to flow through the
// rendering pipeline without explicit parameter passing.
//
// Example:
//
//	m := &metrics.Metrics{
//		RenderMetric:   &memory.RenderMetric{},
//		RendererMetric: memory.NewRendererMetric(),
//	}
//	ctx := metrics.WithMetrics(context.Background(), m)
//	objects, err := engine.Render(ctx)
//	// Metrics are automatically collected during rendering
type Metrics struct {
	// RenderMetric collects engine-level metrics (one observation per Render() call).
	// Optional - may be nil.
	RenderMetric RenderMetric

	// RendererMetric collects renderer-specific metrics (one observation per renderer execution).
	// Optional - may be nil.
	RendererMetric RendererMetric
}

type contextKey struct{}

// WithMetrics returns a context with metrics attached.
//
// The metrics will be automatically used by the engine and renderers
// to record performance data. Pass this context to Engine.Render().
//
// Example:
//
//	m := &metrics.Metrics{RendererMetric: memory.NewRendererMetric()}
//	ctx := metrics.WithMetrics(context.Background(), m)
//	objects, err := engine.Render(ctx)
func WithMetrics(ctx context.Context, m *Metrics) context.Context {
	return context.WithValue(ctx, contextKey{}, m)
}

// FromContext extracts metrics from context, or returns nil if not present.
//
// This is primarily used internally by the engine and renderers.
// Users typically don't need to call this directly.
func FromContext(ctx context.Context) *Metrics {
	if m, ok := ctx.Value(contextKey{}).(*Metrics); ok {
		return m
	}

	return nil
}

// ObserveRenderer records renderer-specific metrics if available in context.
//
// This is a convenience helper that safely handles cases where:
//   - No metrics are in the context
//   - Metrics exist but RendererMetric is nil
//
// Called internally by each renderer's Process() method. Users typically
// don't need to call this directly unless implementing a custom renderer.
//
// This function is safe to call even when metrics are not configured - it will
// simply no-op, ensuring zero overhead when metrics are disabled.
func ObserveRenderer(ctx context.Context, rendererType string, duration time.Duration, objectCount int, err error) {
	if m := FromContext(ctx); m != nil && m.RendererMetric != nil {
		m.RendererMetric.Observe(ctx, rendererType, duration, objectCount, err)
	}
}

// ObserveRender records engine-level render metrics if available in context.
//
// This is a convenience helper that safely handles cases where:
//   - No metrics are in the context
//   - Metrics exist but RenderMetric is nil
//
// Called internally by the engine's Render() method. Users typically
// don't need to call this directly.
//
// This function is safe to call even when metrics are not configured - it will
// simply no-op, ensuring zero overhead when metrics are disabled.
func ObserveRender(ctx context.Context, duration time.Duration, objectCount int) {
	if m := FromContext(ctx); m != nil && m.RenderMetric != nil {
		m.RenderMetric.Observe(ctx, duration, objectCount)
	}
}
