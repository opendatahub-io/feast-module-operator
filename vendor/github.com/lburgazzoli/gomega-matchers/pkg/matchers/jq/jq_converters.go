package jq

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"sync"

	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/gbytes"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ErrTypeNotSupported is returned by converters when the input type is not supported.
// When a converter returns this error, the registry will try the next converter.
var ErrTypeNotSupported = errors.New("type not supported by this converter")

// ConverterFunc converts an input value to a JQ-compatible type (map or slice).
// Returns ErrTypeNotSupported if the input type is not handled by this converter.
type ConverterFunc func(any) (any, error)

//nolint:gochecknoglobals
var (
	convertersMu sync.RWMutex
	converters   []ConverterFunc
)

// RegisterConverter registers a type converter function.
// User-registered converters are prepended to the list and checked before built-in converters.
func RegisterConverter(converter ConverterFunc) {
	convertersMu.Lock()
	defer convertersMu.Unlock()

	converters = append([]ConverterFunc{converter}, converters...)
}

// Convert converts an input value to a JQ-compatible type (map or slice).
// It iterates through registered converters until one successfully converts the value.
func Convert(in any) (any, error) {
	convertersMu.RLock()
	defer convertersMu.RUnlock()

	for _, converter := range converters {
		result, err := converter(in)
		if err == nil {
			return normalizeForJQ(result), nil
		}

		if !errors.Is(err, ErrTypeNotSupported) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("unsuported type:\n%s", format.Object(in, 1))
}

// UnmarshalJSON unmarshals JSON bytes into a JQ-compatible type (map or slice).
// Returns an error if the input is not valid JSON or is a JSON primitive.
func UnmarshalJSON(in []byte) (any, error) {
	if len(in) == 0 {
		return nil, errors.New("a valid Json document is expected")
	}

	var result any
	if err := json.Unmarshal(in, &result); err != nil {
		return nil, fmt.Errorf("unable to unmarshal result, %w", err)
	}

	if result == nil {
		return nil, errors.New("a Json Array or Object is required")
	}

	kind := reflect.TypeOf(result).Kind()

	//nolint:exhaustive
	switch kind {
	case reflect.Map, reflect.Slice:
		return result, nil
	default:
		return nil, errors.New("a Json Array or Object is required")
	}
}

//nolint:gochecknoinits
func init() {
	registerBuiltinConverters()
}

func registerBuiltinConverters() {
	RegisterConverter(StringConverter)
	RegisterConverter(ByteSliceConverter)
	RegisterConverter(RawMessageConverter)
	RegisterConverter(GBytesBufferConverter)
	RegisterConverter(ReaderConverter)
	RegisterConverter(UnstructuredConverter)
	RegisterConverter(UnstructuredPtrConverter)
	RegisterConverter(UnstructuredListPtrConverter)
	RegisterConverter(MapConverter)
	RegisterConverter(SliceConverter)
}

// StringConverter converts string to JQ-compatible type.
func StringConverter(in any) (any, error) {
	v, ok := in.(string)
	if !ok {
		return nil, ErrTypeNotSupported
	}

	return UnmarshalJSON([]byte(v))
}

// ByteSliceConverter converts []byte to JQ-compatible type.
func ByteSliceConverter(in any) (any, error) {
	v, ok := in.([]byte)
	if !ok {
		return nil, ErrTypeNotSupported
	}

	return UnmarshalJSON(v)
}

// RawMessageConverter converts json.RawMessage to JQ-compatible type.
func RawMessageConverter(in any) (any, error) {
	v, ok := in.(json.RawMessage)
	if !ok {
		return nil, ErrTypeNotSupported
	}

	return UnmarshalJSON(v)
}

// GBytesBufferConverter converts *gbytes.Buffer to JQ-compatible type.
func GBytesBufferConverter(in any) (any, error) {
	v, ok := in.(*gbytes.Buffer)
	if !ok {
		return nil, ErrTypeNotSupported
	}

	return UnmarshalJSON(v.Contents())
}

// ReaderConverter converts io.Reader to JQ-compatible type.
func ReaderConverter(in any) (any, error) {
	v, ok := in.(io.Reader)
	if !ok {
		return nil, ErrTypeNotSupported
	}

	data, err := io.ReadAll(v)
	if err != nil {
		return nil, fmt.Errorf("failed to read from reader: %w", err)
	}

	return UnmarshalJSON(data)
}

// UnstructuredConverter converts unstructured.Unstructured to JQ-compatible type.
func UnstructuredConverter(in any) (any, error) {
	v, ok := in.(unstructured.Unstructured)
	if !ok {
		return nil, ErrTypeNotSupported
	}

	return v.Object, nil
}

// UnstructuredPtrConverter converts *unstructured.Unstructured to JQ-compatible type.
func UnstructuredPtrConverter(in any) (any, error) {
	v, ok := in.(*unstructured.Unstructured)
	if !ok {
		return nil, ErrTypeNotSupported
	}

	return v.Object, nil
}

// UnstructuredListPtrConverter converts *unstructured.UnstructuredList to JQ-compatible type.
func UnstructuredListPtrConverter(in any) (any, error) {
	v, ok := in.(*unstructured.UnstructuredList)
	if !ok {
		return nil, ErrTypeNotSupported
	}

	items := make([]any, len(v.Items))
	for i, item := range v.Items {
		items[i] = item.Object
	}

	return items, nil
}

// MapConverter converts map types to JQ-compatible type (pass-through).
func MapConverter(in any) (any, error) {
	if reflect.TypeOf(in).Kind() != reflect.Map {
		return nil, ErrTypeNotSupported
	}

	return in, nil
}

// SliceConverter converts slice types to JQ-compatible type (pass-through).
func SliceConverter(in any) (any, error) {
	if reflect.TypeOf(in).Kind() != reflect.Slice {
		return nil, ErrTypeNotSupported
	}

	// Exclude []byte which is handled by ByteSliceConverter
	if _, ok := in.([]byte); ok {
		return nil, ErrTypeNotSupported
	}

	return in, nil
}

// normalizeForJQ converts Go numeric types that gojq does not accept
// (int64, int32, uint64, etc.) into the types gojq supports: int,
// float64, and *big.Int. Kubernetes unstructured objects typically
// store integers as int64, which gojq >= 0.12.18 no longer normalizes.
func normalizeForJQ(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, val := range v {
			v[k] = normalizeForJQ(val)
		}

		return v
	case []any:
		for i, val := range v {
			v[i] = normalizeForJQ(val)
		}

		return v
	default:
		return normalizeNumeric(v)
	}
}

func normalizeNumeric(v any) any {
	switch v := v.(type) {
	case int64, int32, int16, int8:
		return normalizeSignedInt(v)
	case uint64, uint32, uint16, uint8, uint:
		return normalizeUnsignedInt(v)
	case float32:
		return float64(v)
	default:
		return v
	}
}

func normalizeSignedInt(v any) any {
	switch v := v.(type) {
	case int64:
		if v >= math.MinInt && v <= math.MaxInt {
			return int(v)
		}

		return big.NewInt(v)
	case int32:
		return int(v)
	case int16:
		return int(v)
	case int8:
		return int(v)
	default:
		return v
	}
}

func normalizeUnsignedInt(v any) any {
	switch v := v.(type) {
	case uint64:
		if v <= math.MaxInt {
			return int(v)
		}

		return new(big.Int).SetUint64(v)
	case uint32:
		return int(v)
	case uint16:
		return int(v)
	case uint8:
		return int(v)
	case uint:
		if v <= math.MaxInt {
			return int(v)
		}

		return new(big.Int).SetUint64(uint64(v))
	default:
		return v
	}
}
