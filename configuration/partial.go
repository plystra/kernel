package configuration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"time"

	"github.com/plystra/kernel/plugin/manifest"
	"go.yaml.in/yaml/v3"
)

// PartialValues is one immutable name-sorted set of explicitly provided
// Plugin configuration fields. Values and Secret reference targets remain
// redacted except through the deliberate YAML accessor.
type PartialValues struct {
	names       []string
	fields      map[string]partialField
	initialized bool
}

type partialField struct {
	yaml   []byte
	digest string
}

// NormalizePartial validates only the fields present in one Plugin
// configuration mapping. It does not apply defaults or require omitted fields,
// making it suitable for typed CLI composition before final configuration
// validation. No Secret is resolved.
func NormalizePartial(schema manifest.Config, data []byte) (PartialValues, error) {
	root, err := decodeValuesDocument(data)
	if err != nil {
		return PartialValues{}, err
	}
	provided, err := indexProvidedValues(root)
	if err != nil {
		return PartialValues{}, err
	}

	names := make([]string, 0, len(provided))
	for name := range provided {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make(map[string]partialField, len(provided))
	for _, name := range names {
		node := provided[name]
		field, exists := schema.Lookup(name)
		if !exists {
			return PartialValues{}, newValuesError("", ErrUnknownField, nil)
		}
		digest, err := normalizedFieldDigest(field, node)
		if err != nil {
			return PartialValues{}, newValuesError(field.Name(), ErrInvalidValue, nil)
		}
		normalized, err := marshalSortedNode(node)
		if err != nil {
			return PartialValues{}, newValuesError(field.Name(), ErrInvalidValue, nil)
		}
		fields[name] = partialField{yaml: normalized, digest: digest}
	}
	return PartialValues{names: names, fields: fields, initialized: true}, nil
}

// Valid reports whether the value was produced by NormalizePartial.
func (p PartialValues) Valid() bool {
	return p.initialized && p.fields != nil && len(p.names) == len(p.fields)
}

// Names returns a defensive field-name-sorted copy.
func (p PartialValues) Names() []string {
	if !p.Valid() {
		return nil
	}
	return append([]string(nil), p.names...)
}

// YAML returns defensive normalized YAML for one explicitly provided field.
// Callers must not copy it into logs, diagnostics, generated source, or
// telemetry because it may contain a Secret reference target.
func (p PartialValues) YAML(name string) ([]byte, bool) {
	if !p.Valid() {
		return nil, false
	}
	field, exists := p.fields[name]
	if !exists {
		return nil, false
	}
	return append([]byte(nil), field.yaml...), true
}

// Digest returns a stable semantic SHA-256 digest for one provided field. It
// may be used for equality and drift checks but cannot reveal the field value
// or a Secret reference target.
func (p PartialValues) Digest(name string) (string, bool) {
	if !p.Valid() {
		return "", false
	}
	field, exists := p.fields[name]
	if !exists {
		return "", false
	}
	return field.digest, true
}

// String returns only a redaction marker.
func (PartialValues) String() string { return "<redacted-partial-plugin-configuration>" }

// GoString prevents Go-syntax formatting from exposing configuration values.
func (PartialValues) GoString() string { return "<redacted-partial-plugin-configuration>" }

// Format redacts values for every fmt verb.
func (PartialValues) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-partial-plugin-configuration>"))
}

// LogValue redacts values for structured standard-library logging.
func (PartialValues) LogValue() slog.Value {
	return slog.StringValue("<redacted-partial-plugin-configuration>")
}

// MarshalJSON rejects accidental configuration serialization.
func (PartialValues) MarshalJSON() ([]byte, error) { return nil, ErrSecretExposure }

// MarshalText rejects accidental configuration serialization.
func (PartialValues) MarshalText() ([]byte, error) { return nil, ErrSecretExposure }

// MarshalYAML rejects accidental configuration serialization.
func (PartialValues) MarshalYAML() (any, error) { return nil, ErrSecretExposure }

func normalizedFieldDigest(field manifest.ConfigField, node *yaml.Node) (string, error) {
	var value any
	if field.Type() == manifest.ConfigSecret {
		reference, err := decodeSecretReference(node)
		if err != nil {
			return "", err
		}
		value = []string{reference.kind.String(), reference.target}
		reference.target = ""
		reference.initialized = false
	} else {
		decoded, err := decodeDeclaredValue(node, field.Type(), field.Items(), field.Format())
		if err != nil || !enumContains(field, node, decoded) {
			return "", ErrInvalidValue
		}
		value = canonicalPartialValue(field.Type(), field.Items(), decoded)
	}
	encoded, err := json.Marshal(struct {
		Type  manifest.ConfigType `json:"type"`
		Items manifest.ConfigType `json:"items,omitempty"`
		Value any                 `json:"value"`
	}{Type: field.Type(), Items: field.Items(), Value: value})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func canonicalPartialValue(kind, items manifest.ConfigType, value any) any {
	switch kind {
	case manifest.ConfigDuration:
		return int64(value.(time.Duration))
	case manifest.ConfigURL:
		parsed := value.(url.URL)
		return parsed.String()
	case manifest.ConfigArray:
		array := value.(resolvedArray)
		switch items {
		case manifest.ConfigDuration:
			values := array.value.([]time.Duration)
			result := make([]int64, len(values))
			for index := range values {
				result[index] = int64(values[index])
			}
			return result
		case manifest.ConfigURL:
			values := array.value.([]url.URL)
			result := make([]string, len(values))
			for index := range values {
				result[index] = (&values[index]).String()
			}
			return result
		default:
			return array.value
		}
	default:
		return value
	}
}

func marshalSortedNode(node *yaml.Node) ([]byte, error) {
	cloned := cloneSortedNode(node)
	return yaml.Marshal(cloned)
}

func cloneSortedNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	cloned := *node
	cloned.Anchor = ""
	cloned.Alias = nil
	cloned.Style = 0
	cloned.HeadComment = ""
	cloned.LineComment = ""
	cloned.FootComment = ""
	cloned.Content = nil
	if node.Kind == yaml.MappingNode {
		type pair struct {
			key   *yaml.Node
			value *yaml.Node
		}
		pairs := make([]pair, 0, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			pairs = append(pairs, pair{key: cloneSortedNode(node.Content[index]), value: cloneSortedNode(node.Content[index+1])})
		}
		sort.Slice(pairs, func(left, right int) bool { return pairs[left].key.Value < pairs[right].key.Value })
		for _, item := range pairs {
			cloned.Content = append(cloned.Content, item.key, item.value)
		}
		return &cloned
	}
	for _, child := range node.Content {
		cloned.Content = append(cloned.Content, cloneSortedNode(child))
	}
	return &cloned
}

var _ json.Marshaler = PartialValues{}
