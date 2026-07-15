package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const MaximumDeclarationSize = 1 << 20

var ErrInvalidConfig = errors.New("invalid plugin configuration declaration")

// ConfigType is one supported plugin configuration value type.
type ConfigType string

const (
	ConfigString   ConfigType = "string"
	ConfigInteger  ConfigType = "integer"
	ConfigNumber   ConfigType = "number"
	ConfigBoolean  ConfigType = "boolean"
	ConfigDuration ConfigType = "duration"
	ConfigURL      ConfigType = "url"
	ConfigSecret   ConfigType = "secret"
	ConfigObject   ConfigType = "object"
	ConfigArray    ConfigType = "array"
)

// ParseConfigType parses one exact configuration DSL type.
func ParseConfigType(value string) (ConfigType, error) {
	configType := ConfigType(value)
	switch configType {
	case ConfigString, ConfigInteger, ConfigNumber, ConfigBoolean, ConfigDuration, ConfigURL, ConfigSecret, ConfigObject, ConfigArray:
		return configType, nil
	default:
		return "", invalidConfig("unknown type %q", value)
	}
}

// ConfigField is one immutable validated plugin configuration field.
type ConfigField struct {
	name        string
	configType  ConfigType
	required    bool
	format      string
	items       ConfigType
	defaultJSON []byte
	enumJSON    [][]byte
}

func (f ConfigField) Name() string        { return f.name }
func (f ConfigField) Type() ConfigType    { return f.configType }
func (f ConfigField) Required() bool      { return f.required }
func (f ConfigField) Format() string      { return f.format }
func (f ConfigField) Items() ConfigType   { return f.items }
func (f ConfigField) HasDefault() bool    { return f.defaultJSON != nil }
func (f ConfigField) DefaultJSON() []byte { return append([]byte(nil), f.defaultJSON...) }

// EnumJSON returns defensive copies of the field's canonical JSON enum values.
func (f ConfigField) EnumJSON() [][]byte {
	values := make([][]byte, len(f.enumJSON))
	for index := range f.enumJSON {
		values[index] = append([]byte(nil), f.enumJSON[index]...)
	}
	return values
}

// Config is an immutable configuration declaration sorted by field name.
type Config struct {
	fields []ConfigField
}

// Fields returns a defensive copy in canonical field-name order.
func (c Config) Fields() []ConfigField {
	return append([]ConfigField(nil), c.fields...)
}

// Lookup returns one field by exact name.
func (c Config) Lookup(name string) (ConfigField, bool) {
	index := sort.Search(len(c.fields), func(index int) bool {
		return c.fields[index].name >= name
	})
	if index >= len(c.fields) || c.fields[index].name != name {
		return ConfigField{}, false
	}
	return c.fields[index], true
}

// ParseConfig parses the YAML mapping used under plugin.yaml's config key.
func ParseConfig(data []byte) (Config, error) {
	root, err := decodeSingleYAMLDocument(data)
	if err != nil {
		return Config{}, err
	}
	return parseConfigNode(root)
}

func decodeSingleYAMLDocument(data []byte) (*yaml.Node, error) {
	if len(data) == 0 {
		return nil, invalidConfig("document is empty")
	}
	if len(data) > MaximumDeclarationSize {
		return nil, invalidConfig("document exceeds %d bytes", MaximumDeclarationSize)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, invalidConfig("decode YAML: %v", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, invalidConfig("multiple YAML documents are not allowed")
		}
		return nil, invalidConfig("decode trailing YAML: %v", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return nil, invalidConfig("expected one YAML document")
	}
	if err := rejectYAMLReferences(&document); err != nil {
		return nil, err
	}
	return document.Content[0], nil
}

func rejectYAMLReferences(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.AliasNode || node.Alias != nil || node.Anchor != "" {
		return invalidConfig("YAML anchors and aliases are not allowed")
	}
	for _, child := range node.Content {
		if err := rejectYAMLReferences(child); err != nil {
			return err
		}
	}
	return nil
}

func parseConfigNode(root *yaml.Node) (Config, error) {
	if root == nil || root.Kind != yaml.MappingNode {
		return Config{}, invalidConfig("config must be a mapping")
	}
	fields := make([]ConfigField, 0, len(root.Content)/2)
	seen := make(map[string]struct{}, len(root.Content)/2)
	for index := 0; index < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		name, err := strictString(key)
		if err != nil || !validConfigName(name) {
			return Config{}, invalidConfig("field name %q is not canonical lower snake case", key.Value)
		}
		if _, duplicate := seen[name]; duplicate {
			return Config{}, invalidConfig("duplicate field %q", name)
		}
		field, err := parseConfigField(name, value)
		if err != nil {
			return Config{}, err
		}
		seen[name] = struct{}{}
		fields = append(fields, field)
	}
	sort.Slice(fields, func(left, right int) bool { return fields[left].name < fields[right].name })
	return Config{fields: fields}, nil
}

type rawConfigField struct {
	typeNode     *yaml.Node
	requiredNode *yaml.Node
	defaultNode  *yaml.Node
	enumNode     *yaml.Node
	formatNode   *yaml.Node
	itemsNode    *yaml.Node
}

func parseConfigField(name string, node *yaml.Node) (ConfigField, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return ConfigField{}, invalidConfig("config.%s must be a mapping", name)
	}
	raw := rawConfigField{}
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		key, err := strictString(keyNode)
		if err != nil {
			return ConfigField{}, invalidConfig("config.%s contains a non-string key", name)
		}
		if _, duplicate := seen[key]; duplicate {
			return ConfigField{}, invalidConfig("config.%s contains duplicate key %q", name, key)
		}
		seen[key] = struct{}{}
		switch key {
		case "type":
			raw.typeNode = valueNode
		case "required":
			raw.requiredNode = valueNode
		case "default":
			raw.defaultNode = valueNode
		case "enum":
			raw.enumNode = valueNode
		case "format":
			raw.formatNode = valueNode
		case "items":
			raw.itemsNode = valueNode
		default:
			return ConfigField{}, invalidConfig("config.%s contains unknown key %q", name, key)
		}
	}
	if raw.typeNode == nil {
		return ConfigField{}, invalidConfig("config.%s.type is required", name)
	}
	typeName, err := strictString(raw.typeNode)
	if err != nil {
		return ConfigField{}, invalidConfig("config.%s.type must be a string", name)
	}
	configType, err := ParseConfigType(typeName)
	if err != nil {
		return ConfigField{}, invalidConfig("config.%s.type: %v", name, err)
	}

	field := ConfigField{name: name, configType: configType}
	if raw.requiredNode != nil {
		required, err := strictBool(raw.requiredNode)
		if err != nil {
			return ConfigField{}, invalidConfig("config.%s.required must be true or false", name)
		}
		field.required = required
	}
	if raw.formatNode != nil {
		format, err := strictString(raw.formatNode)
		if err != nil || configType != ConfigString || format != "email" {
			return ConfigField{}, invalidConfig("config.%s.format must be email on a string field", name)
		}
		field.format = format
	}
	if raw.itemsNode != nil {
		itemsName, err := strictString(raw.itemsNode)
		if err != nil || configType != ConfigArray {
			return ConfigField{}, invalidConfig("config.%s.items is valid only for array fields", name)
		}
		field.items, err = ParseConfigType(itemsName)
		if err != nil || field.items == ConfigSecret || field.items == ConfigArray {
			return ConfigField{}, invalidConfig("config.%s.items must be a non-secret, non-array type", name)
		}
	} else if configType == ConfigArray {
		return ConfigField{}, invalidConfig("config.%s.items is required for an array field", name)
	}
	if raw.defaultNode != nil {
		if configType == ConfigSecret {
			return ConfigField{}, invalidConfig("config.%s secret fields cannot declare defaults", name)
		}
		value, err := decodeConfigValue(raw.defaultNode, configType, field.items, field.format)
		if err != nil {
			return ConfigField{}, invalidConfig("config.%s.default: %v", name, err)
		}
		field.defaultJSON, err = json.Marshal(value)
		if err != nil {
			return ConfigField{}, invalidConfig("config.%s.default cannot be encoded", name)
		}
	}
	if raw.enumNode != nil {
		if configType == ConfigSecret || configType == ConfigObject || configType == ConfigArray {
			return ConfigField{}, invalidConfig("config.%s.enum is not supported for %s fields", name, configType)
		}
		if raw.enumNode.Kind != yaml.SequenceNode || len(raw.enumNode.Content) == 0 {
			return ConfigField{}, invalidConfig("config.%s.enum must be a non-empty sequence", name)
		}
		seenValues := make(map[string]struct{}, len(raw.enumNode.Content))
		for enumIndex, enumNode := range raw.enumNode.Content {
			value, err := decodeConfigValue(enumNode, configType, "", field.format)
			if err != nil {
				return ConfigField{}, invalidConfig("config.%s.enum[%d]: %v", name, enumIndex, err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return ConfigField{}, invalidConfig("config.%s.enum[%d] cannot be encoded", name, enumIndex)
			}
			key := string(encoded)
			if _, duplicate := seenValues[key]; duplicate {
				return ConfigField{}, invalidConfig("config.%s.enum contains duplicate value %s", name, encoded)
			}
			seenValues[key] = struct{}{}
			field.enumJSON = append(field.enumJSON, encoded)
		}
		if field.defaultJSON != nil {
			if _, exists := seenValues[string(field.defaultJSON)]; !exists {
				return ConfigField{}, invalidConfig("config.%s.default must be present in enum", name)
			}
		}
	}
	return field, nil
}

func decodeConfigValue(node *yaml.Node, configType, items ConfigType, format string) (any, error) {
	switch configType {
	case ConfigString:
		value, err := strictString(node)
		if err != nil {
			return nil, errors.New("must be a string")
		}
		if format == "email" {
			parsed, err := mail.ParseAddress(value)
			if err != nil || parsed.Address != value {
				return nil, errors.New("must be an email address")
			}
		}
		return value, nil
	case ConfigInteger:
		return strictInteger(node)
	case ConfigNumber:
		return strictNumber(node)
	case ConfigBoolean:
		return strictBool(node)
	case ConfigDuration:
		value, err := strictString(node)
		if err != nil {
			return nil, errors.New("must be a duration string")
		}
		duration, err := time.ParseDuration(value)
		if err != nil || duration < 0 {
			return nil, errors.New("must be a non-negative Go duration")
		}
		return value, nil
	case ConfigURL:
		value, err := strictString(node)
		if err != nil {
			return nil, errors.New("must be a URL string")
		}
		parsed, err := url.Parse(value)
		if err != nil || !parsed.IsAbs() || parsed.Host == "" {
			return nil, errors.New("must be an absolute URL with a host")
		}
		return value, nil
	case ConfigObject:
		return decodeJSONObject(node)
	case ConfigArray:
		if node == nil || node.Kind != yaml.SequenceNode {
			return nil, errors.New("must be an array")
		}
		values := make([]any, 0, len(node.Content))
		for index, child := range node.Content {
			value, err := decodeConfigValue(child, items, "", "")
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", index, err)
			}
			values = append(values, value)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unsupported type %q", configType)
	}
}

func decodeJSONObject(node *yaml.Node) (map[string]any, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, errors.New("must be an object")
	}
	result := make(map[string]any, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, err := strictString(node.Content[index])
		if err != nil {
			return nil, errors.New("object keys must be strings")
		}
		if _, duplicate := result[key]; duplicate {
			return nil, fmt.Errorf("duplicate object key %q", key)
		}
		value, err := decodeJSONValue(node.Content[index+1])
		if err != nil {
			return nil, fmt.Errorf("object key %q: %w", key, err)
		}
		result[key] = value
	}
	return result, nil
}

func decodeJSONValue(node *yaml.Node) (any, error) {
	if node == nil {
		return nil, errors.New("invalid JSON-compatible value")
	}
	switch node.Kind {
	case yaml.MappingNode:
		return decodeJSONObject(node)
	case yaml.SequenceNode:
		values := make([]any, 0, len(node.Content))
		for index, child := range node.Content {
			value, err := decodeJSONValue(child)
			if err != nil {
				return nil, fmt.Errorf("array item %d: %w", index, err)
			}
			values = append(values, value)
		}
		return values, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str":
			return node.Value, nil
		case "!!bool":
			return strictBool(node)
		case "!!int":
			return strictInteger(node)
		case "!!float":
			return strictNumber(node)
		case "!!null":
			return nil, nil
		default:
			return nil, fmt.Errorf("unsupported scalar tag %q", node.Tag)
		}
	default:
		return nil, errors.New("must be JSON-compatible")
	}
}

func strictString(node *yaml.Node) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", errors.New("must be a string")
	}
	return node.Value, nil
}

func strictBool(node *yaml.Node) (bool, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return false, errors.New("must be a boolean")
	}
	switch node.Value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("must be true or false")
	}
}

func strictInteger(node *yaml.Node) (int64, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" || !canonicalInteger(node.Value) {
		return 0, errors.New("must be a canonical base-10 integer")
	}
	value, err := strconv.ParseInt(node.Value, 10, 64)
	if err != nil {
		return 0, errors.New("integer is outside the signed 64-bit range")
	}
	return value, nil
}

func canonicalInteger(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value == "-0" {
		return false
	}
	start := 0
	if value[0] == '-' {
		if len(value) == 1 {
			return false
		}
		start = 1
	}
	if value[start] < '1' || value[start] > '9' {
		return false
	}
	for index := start + 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func strictNumber(node *yaml.Node) (any, error) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return nil, errors.New("must be a number")
	}
	if node.Tag == "!!int" {
		return strictInteger(node)
	}
	if node.Tag != "!!float" {
		return nil, errors.New("must be a number")
	}
	var value float64
	decoder := json.NewDecoder(strings.NewReader(node.Value))
	if err := decoder.Decode(&value); err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return nil, errors.New("must be a finite canonical JSON number")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("must be a finite canonical JSON number")
	}
	return value, nil
}

func validConfigName(value string) bool {
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

func invalidConfig(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, fmt.Sprintf(format, arguments...))
}
