package helm

import (
	"fmt"

	engine "github.com/k8s-manifest-kit/engine/pkg"
)

// NewEngine creates an Engine configured with a single Helm renderer.
// This is a convenience function for simple Helm-only rendering scenarios.
//
// Example:
//
//	e, _ := helm.NewEngine(
//	    helm.Source{
//	        Chart:       "oci://registry/chart:1.0.0",
//	        ReleaseName: "my-release",
//	    },
//	    helm.WithCache(cache.WithTTL(5*time.Minute)),
//	)
//	objects, _ := e.Render(ctx)
func NewEngine(source Source, opts ...RendererOption) (*engine.Engine, error) {
	renderer, err := New([]Source{source}, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create helm renderer: %w", err)
	}

	e, err := engine.New(engine.WithRenderer(renderer))
	if err != nil {
		return nil, fmt.Errorf("failed to create engine: %w", err)
	}

	return e, nil
}
