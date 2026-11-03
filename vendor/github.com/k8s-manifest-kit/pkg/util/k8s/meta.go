package k8s

import "maps"

// SetAnnotation sets a single annotation key-value pair on an Object.
func SetAnnotation(obj Object, key string, value string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[key] = value
	obj.SetAnnotations(annotations)
}

// SetAnnotations merges the provided annotations into the object's existing annotations.
// Existing keys are overwritten if they appear in the provided map.
func SetAnnotations(obj Object, toSet map[string]string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, len(toSet))
	}

	maps.Copy(annotations, toSet)

	obj.SetAnnotations(annotations)
}

// SetLabel sets a single label key-value pair on an Object.
func SetLabel(obj Object, key string, value string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}

	labels[key] = value
	obj.SetLabels(labels)
}

// SetLabels merges the provided labels into the object's existing labels.
// Existing keys are overwritten if they appear in the provided map.
func SetLabels(obj Object, toSet map[string]string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string, len(toSet))
	}

	maps.Copy(labels, toSet)

	obj.SetLabels(labels)
}
