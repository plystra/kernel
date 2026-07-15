package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/plystra/kernel/capability"
	"go.yaml.in/yaml/v3"
)

// ErrInvalidCapability reports an invalid capability.yaml declaration.
var ErrInvalidCapability = errors.New("invalid capability manifest")

// SchemaType is one supported capability request or response value type.
type SchemaType string

const (
	SchemaString  SchemaType = "string"
	SchemaInteger SchemaType = "integer"
	SchemaNumber  SchemaType = "number"
	SchemaBoolean SchemaType = "boolean"
	SchemaObject  SchemaType = "object"
	SchemaArray   SchemaType = "array"
)

// ParseSchemaType parses one exact capability schema type.
func ParseSchemaType(value string) (SchemaType, error) {
	schemaType := SchemaType(value)
	switch schemaType {
	case SchemaString, SchemaInteger, SchemaNumber, SchemaBoolean, SchemaObject, SchemaArray:
		return schemaType, nil
	default:
		return "", invalidCapability("unknown schema type %q", value)
	}
}

// SchemaField is one immutable validated request or response field.
type SchemaField struct {
	name       string
	schemaType SchemaType
	required   bool
	items      SchemaType
	enumJSON   [][]byte
}

func (f SchemaField) Name() string      { return f.name }
func (f SchemaField) Type() SchemaType  { return f.schemaType }
func (f SchemaField) Required() bool    { return f.required }
func (f SchemaField) Items() SchemaType { return f.items }

// EnumJSON returns defensive copies of the field's canonical JSON enum values.
func (f SchemaField) EnumJSON() [][]byte {
	values := make([][]byte, len(f.enumJSON))
	for index := range f.enumJSON {
		values[index] = append([]byte(nil), f.enumJSON[index]...)
	}
	return values
}

// Schema is an immutable request or response schema sorted by field name.
type Schema struct {
	fields []SchemaField
}

// Fields returns a defensive copy in canonical field-name order.
func (s Schema) Fields() []SchemaField {
	return append([]SchemaField(nil), s.fields...)
}

// Lookup returns one field by exact name.
func (s Schema) Lookup(name string) (SchemaField, bool) {
	index := sort.Search(len(s.fields), func(index int) bool {
		return s.fields[index].name >= name
	})
	if index >= len(s.fields) || s.fields[index].name != name {
		return SchemaField{}, false
	}
	return s.fields[index], true
}

// Capability is one immutable validated capability.yaml declaration.
type Capability struct {
	id          capability.Identifier
	description string
	request     Schema
	response    Schema
	errors      []string
}

func (c Capability) ID() capability.Identifier { return c.id }
func (c Capability) Description() string       { return c.description }
func (c Capability) Request() Schema           { return c.request }
func (c Capability) Response() Schema          { return c.response }

// Errors returns declared semantic error codes in canonical order.
func (c Capability) Errors() []string {
	return append([]string(nil), c.errors...)
}

// ParseCapability parses and validates one strict capability.yaml declaration.
func ParseCapability(data []byte) (Capability, error) {
	root, err := decodeSingleYAMLDocument(data)
	if err != nil {
		return Capability{}, invalidCapability("%v", err)
	}
	if root.Kind != yaml.MappingNode {
		return Capability{}, invalidCapability("document must be a mapping")
	}

	var manifest Capability
	var idNode, descriptionNode, requestNode, responseNode, errorsNode *yaml.Node
	seen := make(map[string]struct{}, len(root.Content)/2)
	for index := 0; index < len(root.Content); index += 2 {
		keyNode, valueNode := root.Content[index], root.Content[index+1]
		key, err := strictString(keyNode)
		if err != nil {
			return Capability{}, invalidCapability("document contains a non-string key")
		}
		if _, duplicate := seen[key]; duplicate {
			return Capability{}, invalidCapability("duplicate key %q", key)
		}
		seen[key] = struct{}{}
		switch key {
		case "id":
			idNode = valueNode
		case "description":
			descriptionNode = valueNode
		case "request":
			requestNode = valueNode
		case "response":
			responseNode = valueNode
		case "errors":
			errorsNode = valueNode
		default:
			return Capability{}, invalidCapability("unknown key %q", key)
		}
	}

	if idNode == nil {
		return Capability{}, invalidCapability("id is required")
	}
	idValue, err := strictString(idNode)
	if err != nil {
		return Capability{}, invalidCapability("id must be a string")
	}
	manifest.id, err = capability.ParseIdentifier(idValue)
	if err != nil {
		return Capability{}, invalidCapability("id %q is not canonical", idValue)
	}
	if descriptionNode != nil {
		manifest.description, err = strictString(descriptionNode)
		if err != nil {
			return Capability{}, invalidCapability("description must be a string")
		}
	}
	if requestNode != nil {
		manifest.request, err = parseSchema("request", requestNode)
		if err != nil {
			return Capability{}, err
		}
	}
	if responseNode != nil {
		manifest.response, err = parseSchema("response", responseNode)
		if err != nil {
			return Capability{}, err
		}
	}
	if errorsNode != nil {
		manifest.errors, err = parseErrorCodes(errorsNode)
		if err != nil {
			return Capability{}, err
		}
	}
	return manifest, nil
}

func parseSchema(section string, node *yaml.Node) (Schema, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return Schema{}, invalidCapability("%s must be a mapping", section)
	}
	fields := make([]SchemaField, 0, len(node.Content)/2)
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		name, err := strictString(keyNode)
		if err != nil || !validFieldName(name) {
			return Schema{}, invalidCapability("%s field name %q is not canonical lower snake case", section, keyNode.Value)
		}
		if _, duplicate := seen[name]; duplicate {
			return Schema{}, invalidCapability("%s contains duplicate field %q", section, name)
		}
		field, err := parseSchemaField(section+"."+name, name, valueNode)
		if err != nil {
			return Schema{}, err
		}
		seen[name] = struct{}{}
		fields = append(fields, field)
	}
	sort.Slice(fields, func(left, right int) bool { return fields[left].name < fields[right].name })
	return Schema{fields: fields}, nil
}

func parseSchemaField(path, name string, node *yaml.Node) (SchemaField, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return SchemaField{}, invalidCapability("%s must be a mapping", path)
	}
	var typeNode, requiredNode, itemsNode, enumNode *yaml.Node
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		key, err := strictString(keyNode)
		if err != nil {
			return SchemaField{}, invalidCapability("%s contains a non-string key", path)
		}
		if _, duplicate := seen[key]; duplicate {
			return SchemaField{}, invalidCapability("%s contains duplicate key %q", path, key)
		}
		seen[key] = struct{}{}
		switch key {
		case "type":
			typeNode = valueNode
		case "required":
			requiredNode = valueNode
		case "items":
			itemsNode = valueNode
		case "enum":
			enumNode = valueNode
		default:
			return SchemaField{}, invalidCapability("%s contains unknown key %q", path, key)
		}
	}
	if typeNode == nil {
		return SchemaField{}, invalidCapability("%s.type is required", path)
	}
	typeName, err := strictString(typeNode)
	if err != nil {
		return SchemaField{}, invalidCapability("%s.type must be a string", path)
	}
	schemaType, err := ParseSchemaType(typeName)
	if err != nil {
		return SchemaField{}, invalidCapability("%s.type %q is not supported", path, typeName)
	}
	field := SchemaField{name: name, schemaType: schemaType}
	if requiredNode != nil {
		field.required, err = strictBool(requiredNode)
		if err != nil {
			return SchemaField{}, invalidCapability("%s.required must be true or false", path)
		}
	}
	if itemsNode != nil {
		itemsName, err := strictString(itemsNode)
		if err != nil || schemaType != SchemaArray {
			return SchemaField{}, invalidCapability("%s.items is valid only for array fields", path)
		}
		field.items, err = ParseSchemaType(itemsName)
		if err != nil || field.items == SchemaArray {
			return SchemaField{}, invalidCapability("%s.items must be a non-array schema type", path)
		}
	} else if schemaType == SchemaArray {
		return SchemaField{}, invalidCapability("%s.items is required for an array field", path)
	}
	if enumNode != nil {
		if schemaType == SchemaObject || schemaType == SchemaArray {
			return SchemaField{}, invalidCapability("%s.enum is not supported for %s fields", path, schemaType)
		}
		if enumNode.Kind != yaml.SequenceNode || len(enumNode.Content) == 0 {
			return SchemaField{}, invalidCapability("%s.enum must be a non-empty sequence", path)
		}
		seenValues := make(map[string]struct{}, len(enumNode.Content))
		for enumIndex, enumValue := range enumNode.Content {
			value, err := decodeSchemaScalar(enumValue, schemaType)
			if err != nil {
				return SchemaField{}, invalidCapability("%s.enum[%d]: %v", path, enumIndex, err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return SchemaField{}, invalidCapability("%s.enum[%d] cannot be encoded", path, enumIndex)
			}
			key := string(encoded)
			if _, duplicate := seenValues[key]; duplicate {
				return SchemaField{}, invalidCapability("%s.enum contains duplicate value %s", path, encoded)
			}
			seenValues[key] = struct{}{}
			field.enumJSON = append(field.enumJSON, encoded)
		}
	}
	return field, nil
}

func decodeSchemaScalar(node *yaml.Node, schemaType SchemaType) (any, error) {
	switch schemaType {
	case SchemaString:
		return decodeConfigValue(node, ConfigString, "", "")
	case SchemaInteger:
		return decodeConfigValue(node, ConfigInteger, "", "")
	case SchemaNumber:
		return decodeConfigValue(node, ConfigNumber, "", "")
	case SchemaBoolean:
		return decodeConfigValue(node, ConfigBoolean, "", "")
	default:
		return nil, fmt.Errorf("unsupported scalar schema type %q", schemaType)
	}
}

func parseErrorCodes(node *yaml.Node) ([]string, error) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, invalidCapability("errors must be a sequence")
	}
	codes := make([]string, 0, len(node.Content))
	seen := make(map[string]struct{}, len(node.Content))
	for index, item := range node.Content {
		code, err := strictString(item)
		if err != nil || !validFieldName(code) {
			return nil, invalidCapability("errors[%d] must be canonical lower snake case", index)
		}
		if _, duplicate := seen[code]; duplicate {
			return nil, invalidCapability("errors contains duplicate code %q", code)
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes, nil
}

func invalidCapability(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidCapability, fmt.Sprintf(format, arguments...))
}
