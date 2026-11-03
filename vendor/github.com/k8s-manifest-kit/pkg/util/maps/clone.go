package maps

import "reflect"

// DeepCloneMap creates a fully independent copy of a map[string]any.
// It recursively copies JSON-like trees:
//   - nested map[string]any
//   - []any slices (including maps and slices contained within them)
//   - common typed slices ([]string, []int, []int64, []float64, []bool)
//   - all other slice types are shallow-copied via reflection
//   - all other types (primitives, strings, structs, pointers) are copied by value
//
// After DeepCloneMap, mutating any level of the returned map
// (including nested maps and slices) does not affect the original.
//
// Non-JSON types (e.g., pointers, structs with pointer fields) are
// shallow-copied. If the map contains such types and isolation is needed,
// callers must handle those separately.
func DeepCloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}

	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = DeepCloneValue(v)
	}

	return result
}

// DeepCloneValue creates a deep copy of a single value.
// For maps, recursively clones all nested maps and slices.
// For []any slices, recursively clones all elements.
// For common typed slices ([]string, []int, []int64, []float64, []bool),
// creates a copy of the slice.
// For other slice types, creates a shallow copy via reflection.
// For primitives and other types, returns the value as-is.
func DeepCloneValue(v any) any {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case map[string]any:
		return DeepCloneMap(val)
	case []any:
		clone := make([]any, len(val))
		for i, elem := range val {
			clone[i] = DeepCloneValue(elem)
		}

		return clone
	case []string:
		clone := make([]string, len(val))
		copy(clone, val)

		return clone
	case []int:
		clone := make([]int, len(val))
		copy(clone, val)

		return clone
	case []int64:
		clone := make([]int64, len(val))
		copy(clone, val)

		return clone
	case []float64:
		clone := make([]float64, len(val))
		copy(clone, val)

		return clone
	case []bool:
		clone := make([]bool, len(val))
		copy(clone, val)

		return clone
	default:
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Slice {
			sliceLen := rv.Len()
			clone := reflect.MakeSlice(rv.Type(), sliceLen, sliceLen)

			for i := range sliceLen {
				clone.Index(i).Set(rv.Index(i))
			}

			return clone.Interface()
		}

		return v
	}
}
