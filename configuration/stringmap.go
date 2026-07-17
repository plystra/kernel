package configuration

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"go.yaml.in/yaml/v3"
)

var (
	// ErrInvalidStringSection reports a selected section that is not a strict
	// string-keyed mapping of string scalar values.
	ErrInvalidStringSection = errors.New("invalid runtime configuration string section")
)

// StringMap is one immutable name-sorted mapping of runtime setting strings
// extracted from a bounded configuration document. Names are public setting
// identities; values remain redacted except through the explicit accessor.
type StringMap struct {
	names       []string
	values      map[string]string
	initialized bool
}

// Valid reports whether the map was produced by ExtractStringMap.
func (m StringMap) Valid() bool {
	return m.initialized && m.values != nil && len(m.names) == len(m.values)
}

// Names returns a defensive name-sorted copy of all setting identities.
func (m StringMap) Names() []string {
	if !m.Valid() {
		return nil
	}
	names := make([]string, len(m.names))
	copy(names, m.names)
	return names
}

// Value returns one exact configured string. Callers must not copy private
// values into logs, errors, generated output, telemetry, or plugin settings.
func (m StringMap) Value(name string) (string, bool) {
	if !m.Valid() {
		return "", false
	}
	value, exists := m.values[name]
	return value, exists
}

// String returns only a redaction marker.
func (StringMap) String() string { return "<redacted-runtime-configuration-strings>" }

// GoString prevents Go-syntax formatting from exposing private values.
func (StringMap) GoString() string { return "<redacted-runtime-configuration-strings>" }

// Format redacts private values for every fmt verb.
func (StringMap) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-runtime-configuration-strings>"))
}

// LogValue redacts private values for structured standard-library logging.
func (StringMap) LogValue() slog.Value {
	return slog.StringValue("<redacted-runtime-configuration-strings>")
}

// MarshalJSON rejects accidental configuration serialization.
func (StringMap) MarshalJSON() ([]byte, error) { return nil, ErrSecretExposure }

// MarshalText rejects accidental configuration serialization.
func (StringMap) MarshalText() ([]byte, error) { return nil, ErrSecretExposure }

// MarshalYAML rejects accidental configuration serialization.
func (StringMap) MarshalYAML() (any, error) { return nil, ErrSecretExposure }

// ExtractStringMap selects one exact top-level section from a bounded strict
// YAML document. The section may be absent, producing a valid empty map; when
// present, every entry must have a string key and string scalar value. The
// function does not interpret the section name, setting names, other document
// fields, application identity, or provider selection.
func ExtractStringMap(data []byte, section string) (StringMap, error) {
	if !validSectionName(section) {
		return StringMap{}, fmt.Errorf("%w: %w: invalid section name", ErrInvalidDocument, ErrInvalidStringSection)
	}
	root, err := decodeValuesDocument(data)
	if err != nil {
		return StringMap{}, fmt.Errorf("%w: %w", ErrInvalidDocument, err)
	}
	sections, err := indexProvidedValues(root)
	if err != nil {
		return StringMap{}, fmt.Errorf("%w: %w", ErrInvalidDocument, err)
	}
	selected, exists := sections[section]
	if !exists {
		return StringMap{names: []string{}, values: make(map[string]string), initialized: true}, nil
	}
	if selected.Kind != yaml.MappingNode {
		return StringMap{}, fmt.Errorf("%w: %w: section %q must be a string mapping", ErrInvalidDocument, ErrInvalidStringSection, section)
	}
	indexed, err := indexProvidedValues(selected)
	if err != nil {
		return StringMap{}, fmt.Errorf("%w: %w: section %q", ErrInvalidDocument, ErrInvalidStringSection, section)
	}
	names := make([]string, 0, len(indexed))
	values := make(map[string]string, len(indexed))
	for name, node := range indexed {
		value, err := strictValueString(node)
		if err != nil {
			return StringMap{}, fmt.Errorf("%w: %w: entry %q must be a string", ErrInvalidDocument, ErrInvalidStringSection, name)
		}
		names = append(names, name)
		values[name] = value
	}
	sort.Strings(names)
	return StringMap{names: names, values: values, initialized: true}, nil
}

var _ json.Marshaler = StringMap{}
