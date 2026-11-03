package k8s

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// DecodeYAML decodes YAML content into a slice of unstructured objects.
func DecodeYAML(content []byte) ([]unstructured.Unstructured, error) {
	results := make([]unstructured.Unstructured, 0)

	r := bytes.NewReader(content)
	yd := yaml.NewDecoder(r)

	docIndex := 0
	for {
		var out map[string]any

		err := yd.Decode(&out)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, fmt.Errorf("unable to decode YAML document[%d]: %w", docIndex, err)
		}

		docIndex++

		if len(out) == 0 {
			continue
		}

		// Validate kind field exists and is a non-empty string
		kind, ok := out["kind"].(string)
		if !ok || kind == "" {
			continue
		}

		// Validate apiVersion field exists and is a non-empty string
		apiVersion, ok := out["apiVersion"].(string)
		if !ok || apiVersion == "" {
			continue
		}

		obj, err := ToUnstructured(&out)
		if err != nil {
			if runtime.IsMissingKind(err) {
				continue
			}

			return nil, fmt.Errorf("unable to decode YAML document[%d]: %w", docIndex-1, err)
		}

		results = append(results, *obj)
	}

	return results, nil
}

// ToUnstructured converts any object to an unstructured.Unstructured representation.
func ToUnstructured(obj any) (*unstructured.Unstructured, error) {
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("unable to convert object %T to unstructured: %w", obj, err)
	}

	u := unstructured.Unstructured{
		Object: data,
	}

	return &u, nil
}
