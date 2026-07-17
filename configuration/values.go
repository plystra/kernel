package configuration

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/plystra/kernel/plugin/manifest"
)

// Values is one immutable, fully validated and Secret-resolved plugin
// configuration object. Typed accessors return defensive copies where the
// underlying value is mutable.
type Values struct {
	fields      map[string]resolvedValue
	initialized bool
}

type resolvedValue struct {
	kind  manifest.ConfigType
	value any
}

// Valid reports whether the values were produced by Decode.
func (v Values) Valid() bool { return v.initialized }

// Has reports whether an optional or required field has a resolved value.
func (v Values) Has(name string) bool {
	if !v.Valid() {
		return false
	}
	_, exists := v.fields[name]
	return exists
}

// StringValue returns one string field.
func (v Values) StringValue(name string) (string, bool) {
	value, ok := v.lookup(name, manifest.ConfigString)
	if !ok {
		return "", false
	}
	result, ok := value.(string)
	return result, ok
}

// IntegerValue returns one signed 64-bit integer field.
func (v Values) IntegerValue(name string) (int64, bool) {
	value, ok := v.lookup(name, manifest.ConfigInteger)
	if !ok {
		return 0, false
	}
	result, ok := value.(int64)
	return result, ok
}

// NumberValue returns one finite number field.
func (v Values) NumberValue(name string) (float64, bool) {
	value, ok := v.lookup(name, manifest.ConfigNumber)
	if !ok {
		return 0, false
	}
	result, ok := value.(float64)
	return result, ok
}

// BooleanValue returns one boolean field.
func (v Values) BooleanValue(name string) (bool, bool) {
	value, ok := v.lookup(name, manifest.ConfigBoolean)
	if !ok {
		return false, false
	}
	result, ok := value.(bool)
	return result, ok
}

// DurationValue returns one non-negative duration field.
func (v Values) DurationValue(name string) (time.Duration, bool) {
	value, ok := v.lookup(name, manifest.ConfigDuration)
	if !ok {
		return 0, false
	}
	result, ok := value.(time.Duration)
	return result, ok
}

// URLValue returns one absolute URL field by value.
func (v Values) URLValue(name string) (url.URL, bool) {
	value, ok := v.lookup(name, manifest.ConfigURL)
	if !ok {
		return url.URL{}, false
	}
	result, ok := value.(url.URL)
	return result, ok
}

// SecretValue returns one immutable Secret field.
func (v Values) SecretValue(name string) (Secret, bool) {
	value, ok := v.lookup(name, manifest.ConfigSecret)
	if !ok {
		return Secret{}, false
	}
	result, ok := value.(Secret)
	return result, ok && result.Valid()
}

// ObjectValue returns a deep defensive copy of one JSON-compatible object field.
func (v Values) ObjectValue(name string) (map[string]any, bool) {
	value, ok := v.lookup(name, manifest.ConfigObject)
	if !ok {
		return nil, false
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	return cloneObject(result), true
}

// Strings returns a defensive copy of one string array field.
func (v Values) Strings(name string) ([]string, bool) {
	value, ok := v.lookupArray(name, manifest.ConfigString)
	if !ok {
		return nil, false
	}
	result, ok := value.([]string)
	return cloneSlice(result), ok
}

// Integers returns a defensive copy of one integer array field.
func (v Values) Integers(name string) ([]int64, bool) {
	value, ok := v.lookupArray(name, manifest.ConfigInteger)
	if !ok {
		return nil, false
	}
	result, ok := value.([]int64)
	return cloneSlice(result), ok
}

// Numbers returns a defensive copy of one number array field.
func (v Values) Numbers(name string) ([]float64, bool) {
	value, ok := v.lookupArray(name, manifest.ConfigNumber)
	if !ok {
		return nil, false
	}
	result, ok := value.([]float64)
	return cloneSlice(result), ok
}

// Booleans returns a defensive copy of one boolean array field.
func (v Values) Booleans(name string) ([]bool, bool) {
	value, ok := v.lookupArray(name, manifest.ConfigBoolean)
	if !ok {
		return nil, false
	}
	result, ok := value.([]bool)
	return cloneSlice(result), ok
}

// Durations returns a defensive copy of one duration array field.
func (v Values) Durations(name string) ([]time.Duration, bool) {
	value, ok := v.lookupArray(name, manifest.ConfigDuration)
	if !ok {
		return nil, false
	}
	result, ok := value.([]time.Duration)
	return cloneSlice(result), ok
}

// URLs returns a defensive copy of one URL array field.
func (v Values) URLs(name string) ([]url.URL, bool) {
	value, ok := v.lookupArray(name, manifest.ConfigURL)
	if !ok {
		return nil, false
	}
	result, ok := value.([]url.URL)
	return cloneSlice(result), ok
}

// Objects returns deep defensive copies of one object array field.
func (v Values) Objects(name string) ([]map[string]any, bool) {
	value, ok := v.lookupArray(name, manifest.ConfigObject)
	if !ok {
		return nil, false
	}
	result, ok := value.([]map[string]any)
	if !ok {
		return nil, false
	}
	copyValue := make([]map[string]any, len(result))
	for index := range result {
		copyValue[index] = cloneObject(result[index])
	}
	return copyValue, true
}

// String returns only a redaction marker.
func (Values) String() string { return "<redacted-plugin-configuration>" }

// GoString prevents Go-syntax formatting from exposing configuration values.
func (Values) GoString() string { return "<redacted-plugin-configuration>" }

// Format redacts configuration values for every fmt verb.
func (Values) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-plugin-configuration>"))
}

// LogValue redacts configuration values for structured standard-library
// logging.
func (Values) LogValue() slog.Value {
	return slog.StringValue("<redacted-plugin-configuration>")
}

// MarshalJSON rejects accidental configuration serialization.
func (Values) MarshalJSON() ([]byte, error) { return nil, ErrSecretExposure }

// MarshalText rejects accidental configuration serialization.
func (Values) MarshalText() ([]byte, error) { return nil, ErrSecretExposure }

// MarshalYAML rejects accidental configuration serialization.
func (Values) MarshalYAML() (any, error) { return nil, ErrSecretExposure }

func (v Values) lookup(name string, kind manifest.ConfigType) (any, bool) {
	if !v.Valid() {
		return nil, false
	}
	field, exists := v.fields[name]
	if !exists || field.kind != kind {
		return nil, false
	}
	return field.value, true
}

func (v Values) lookupArray(name string, items manifest.ConfigType) (any, bool) {
	if !v.Valid() {
		return nil, false
	}
	field, exists := v.fields[name]
	if !exists || field.kind != manifest.ConfigArray {
		return nil, false
	}
	array, ok := field.value.(resolvedArray)
	if !ok || array.items != items {
		return nil, false
	}
	return array.value, true
}

type resolvedArray struct {
	items manifest.ConfigType
	value any
}

func cloneSlice[T any](value []T) []T {
	if value == nil {
		return nil
	}
	copyValue := make([]T, len(value))
	copy(copyValue, value)
	return copyValue
}

func cloneObject(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	copyValue := make(map[string]any, len(value))
	for key, item := range value {
		copyValue[key] = cloneJSONValue(item)
	}
	return copyValue
}

func cloneJSONValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneObject(value)
	case []any:
		copyValue := make([]any, len(value))
		for index := range value {
			copyValue[index] = cloneJSONValue(value[index])
		}
		return copyValue
	case json.Number:
		return json.Number(value.String())
	default:
		return value
	}
}
