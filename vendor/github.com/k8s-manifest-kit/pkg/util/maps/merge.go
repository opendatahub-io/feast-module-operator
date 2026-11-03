package maps

// DeepMerge recursively merges overlay into base, with overlay values taking precedence.
// Returns a new map without modifying the inputs.
//
// Merge Semantics:
//   - Maps: Recursively merged. Keys from both maps are preserved.
//     Overlapping keys use the overlay value.
//   - Slices: Completely replaced by overlay (NOT appended or merged).
//   - Other types: Overlay value replaces base value.
//   - Type mismatches: Overlay value wins regardless of types.
//   - Nil values: Treated as empty - overlay nil returns cloned base, base nil returns cloned overlay.
//
// Examples:
//
// Nested map merge:
//
//	base := map[string]any{
//	    "config": map[string]any{
//	        "host": "localhost",
//	        "port": 8080,
//	        "timeout": 30,
//	    },
//	}
//	overlay := map[string]any{
//	    "config": map[string]any{
//	        "port": 9090,  // Override
//	        "retries": 3,  // Add new
//	    },
//	}
//	result := DeepMerge(base, overlay)
//	// result["config"] = {"host": "localhost", "port": 9090, "timeout": 30, "retries": 3}
//
// Slice replacement (NOT merge):
//
//	base := map[string]any{"tags": []string{"dev", "test"}}
//	overlay := map[string]any{"tags": []string{"prod"}}
//	result := DeepMerge(base, overlay)
//	// result["tags"] = ["prod"]  // NOT ["dev", "test", "prod"]
//
// Type mismatch (overlay wins):
//
//	base := map[string]any{"service": map[string]any{"type": "ClusterIP"}}
//	overlay := map[string]any{"service": "NodePort"}
//	result := DeepMerge(base, overlay)
//	// result["service"] = "NodePort"  // Map replaced by string
//
// Use Case - Render-Time Values:
//
//	// Configuration-time values
//	source := helm.Source{
//	    Values: helm.Values(map[string]any{
//	        "replicaCount": 2,
//	        "image": map[string]any{
//	            "repository": "nginx",
//	            "tag": "1.25.0",
//	            "pullPolicy": "IfNotPresent",
//	        },
//	    }),
//	}
//	// Render-time override (merged with source values)
//	objects, err := engine.Render(ctx, engine.WithValues(map[string]any{
//	    "replicaCount": 5,           // Override
//	    "image": map[string]any{
//	        "tag": "1.26.0",          // Override tag only
//	        // repository and pullPolicy preserved from source
//	    },
//	}))
//	// Final values: {replicaCount: 5, image: {repository: "nginx", tag: "1.26.0", pullPolicy: "IfNotPresent"}}
func DeepMerge(base map[string]any, overlay map[string]any) map[string]any {
	if base == nil && overlay == nil {
		return map[string]any{}
	}
	if base == nil {
		return DeepCloneMap(overlay)
	}
	if overlay == nil {
		return DeepCloneMap(base)
	}

	result := make(map[string]any, len(base)+len(overlay))

	for k, baseValue := range base {
		if overlayValue, willOverride := overlay[k]; willOverride {
			baseMap, baseIsMap := baseValue.(map[string]any)
			overlayMap, overlayIsMap := overlayValue.(map[string]any)

			if baseIsMap && overlayIsMap {
				result[k] = DeepMerge(baseMap, overlayMap)
			} else {
				result[k] = DeepCloneValue(overlayValue)
			}
		} else {
			result[k] = DeepCloneValue(baseValue)
		}
	}

	for k, overlayValue := range overlay {
		if _, exists := base[k]; !exists {
			result[k] = DeepCloneValue(overlayValue)
		}
	}

	return result
}
