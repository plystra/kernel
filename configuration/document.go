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
	// ErrInvalidDocument reports a malformed, unbounded, or ambiguous runtime
	// configuration document.
	ErrInvalidDocument = errors.New("invalid runtime configuration document")
	// ErrInvalidObjectSection reports a selected section that is not a strict
	// string-keyed mapping of object mappings.
	ErrInvalidObjectSection = errors.New("invalid runtime configuration object section")
)

// ObjectMap is one immutable name-sorted mapping of private YAML objects
// extracted from a bounded runtime configuration document. Names are public
// configuration identities; object values and Secret targets remain redacted
// except through the explicit YAML accessor.
type ObjectMap struct {
	names       []string
	objects     map[string][]byte
	initialized bool
}

// Valid reports whether the map was produced by ExtractObjectMap.
func (m ObjectMap) Valid() bool {
	return m.initialized && m.objects != nil && len(m.names) == len(m.objects)
}

// Names returns a defensive name-sorted copy of all configured identities.
func (m ObjectMap) Names() []string {
	if !m.Valid() {
		return nil
	}
	names := make([]string, len(m.names))
	copy(names, m.names)
	return names
}

// YAML returns defensive normalized YAML for one exact configured object.
// Callers must not copy these private bytes into logs, errors, generated
// output, telemetry, or another plugin's configuration adapter.
func (m ObjectMap) YAML(name string) ([]byte, bool) {
	if !m.Valid() {
		return nil, false
	}
	data, exists := m.objects[name]
	if !exists {
		return nil, false
	}
	return append([]byte(nil), data...), true
}

// String returns only a redaction marker.
func (ObjectMap) String() string { return "<redacted-runtime-configuration-objects>" }

// GoString prevents Go-syntax formatting from exposing private values.
func (ObjectMap) GoString() string { return "<redacted-runtime-configuration-objects>" }

// Format redacts private values for every fmt verb.
func (ObjectMap) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-runtime-configuration-objects>"))
}

// LogValue redacts private values for structured standard-library logging.
func (ObjectMap) LogValue() slog.Value {
	return slog.StringValue("<redacted-runtime-configuration-objects>")
}

// MarshalJSON rejects accidental configuration serialization.
func (ObjectMap) MarshalJSON() ([]byte, error) { return nil, ErrSecretExposure }

// MarshalText rejects accidental configuration serialization.
func (ObjectMap) MarshalText() ([]byte, error) { return nil, ErrSecretExposure }

// MarshalYAML rejects accidental configuration serialization.
func (ObjectMap) MarshalYAML() (any, error) { return nil, ErrSecretExposure }

// ExtractObjectMap selects one exact top-level section from a bounded strict
// YAML document. The section may be absent, producing a valid empty map; when
// present, every entry must be a string-keyed object mapping. The function is
// deliberately generic: it does not interpret the section name, entry names,
// other document fields, Plugin IDs, or provider selection.
func ExtractObjectMap(data []byte, section string) (ObjectMap, error) {
	if !validSectionName(section) {
		return ObjectMap{}, fmt.Errorf("%w: %w: invalid section name", ErrInvalidDocument, ErrInvalidObjectSection)
	}
	root, err := decodeValuesDocument(data)
	if err != nil {
		return ObjectMap{}, fmt.Errorf("%w: %w", ErrInvalidDocument, err)
	}
	sections, err := indexProvidedValues(root)
	if err != nil {
		return ObjectMap{}, fmt.Errorf("%w: %w", ErrInvalidDocument, err)
	}
	selected, exists := sections[section]
	if !exists {
		return ObjectMap{names: []string{}, objects: make(map[string][]byte), initialized: true}, nil
	}
	if selected.Kind != yaml.MappingNode {
		return ObjectMap{}, fmt.Errorf("%w: %w: section %q must be an object mapping", ErrInvalidDocument, ErrInvalidObjectSection, section)
	}
	indexed, err := indexProvidedValues(selected)
	if err != nil {
		return ObjectMap{}, fmt.Errorf("%w: %w: section %q", ErrInvalidDocument, ErrInvalidObjectSection, section)
	}
	names := make([]string, 0, len(indexed))
	objects := make(map[string][]byte, len(indexed))
	for name, node := range indexed {
		if node.Kind != yaml.MappingNode {
			return ObjectMap{}, fmt.Errorf("%w: %w: entry %q must be an object mapping", ErrInvalidDocument, ErrInvalidObjectSection, name)
		}
		normalized, err := yaml.Marshal(node)
		if err != nil {
			return ObjectMap{}, fmt.Errorf("%w: %w: entry %q cannot be normalized", ErrInvalidDocument, ErrInvalidObjectSection, name)
		}
		names = append(names, name)
		objects[name] = append([]byte(nil), normalized...)
	}
	sort.Strings(names)
	return ObjectMap{names: names, objects: objects, initialized: true}, nil
}

func validSectionName(value string) bool {
	if value == "" || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousUnderscore := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousUnderscore = false
		case character == '_' && !previousUnderscore:
			previousUnderscore = true
		default:
			return false
		}
	}
	return !previousUnderscore
}

var _ json.Marshaler = ObjectMap{}
