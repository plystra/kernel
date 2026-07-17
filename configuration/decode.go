package configuration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/plystra/kernel/plugin/manifest"
	"go.yaml.in/yaml/v3"
)

var (
	// ErrInvalidValues reports runtime values that do not conform to one
	// validated plugin configuration declaration.
	ErrInvalidValues = errors.New("invalid plugin configuration values")
	// ErrUnknownField reports a value with no declared configuration field.
	ErrUnknownField = errors.New("unknown plugin configuration field")
	// ErrMissingField reports a required field without a value or default.
	ErrMissingField = errors.New("missing required plugin configuration field")
	// ErrInvalidValue reports a declared field with an incompatible value.
	ErrInvalidValue = errors.New("invalid plugin configuration field value")
)

const (
	maximumConfigurationDepth = 64
	maximumConfigurationNodes = 1 << 16
)

// Decode validates one plugin-owned YAML configuration mapping, applies
// declaration defaults, resolves only declared Secret references, and returns
// immutable typed values. Errors identify only declared field names and safe
// failure classes; input values and Secret reference targets are never copied
// into an error.
func Decode(ctx context.Context, resolver *Resolver, schema manifest.Config, data []byte) (Values, error) {
	if ctx == nil {
		return Values{}, ErrInvalidContext
	}
	if resolver == nil || resolver.maximumValueBytes <= 0 || resolver.maximumValueBytes > MaximumSecretValueBytes {
		return Values{}, ErrInvalidResolver
	}
	if err := ctx.Err(); err != nil {
		return Values{}, newValuesError("", nil, err)
	}
	resolved, pending, err := decodeUnresolved(schema, data)
	if err != nil {
		return Values{}, err
	}

	for index := range pending {
		if err := ctx.Err(); err != nil {
			clearResolvedValues(resolved)
			clearPendingSecrets(pending)
			return Values{}, newValuesError(pending[index].name, nil, err)
		}
		secret, resolveErr := resolver.Resolve(ctx, pending[index].reference)
		pending[index].reference.target = ""
		pending[index].reference.initialized = false
		if resolveErr != nil {
			clearResolvedValues(resolved)
			clearPendingSecrets(pending[index+1:])
			return Values{}, newValuesError(pending[index].name, resolveErr, nil)
		}
		resolved[pending[index].name] = resolvedValue{kind: manifest.ConfigSecret, value: secret}
	}
	return Values{fields: resolved, initialized: true}, nil
}

// Validate checks one plugin-owned YAML configuration mapping against its
// declaration without reading environment variables or files. It applies the
// same type, default, enum, size, depth, and Secret-reference validation used
// by Decode, then discards every parsed value and reference target.
func Validate(schema manifest.Config, data []byte) error {
	resolved, pending, err := decodeUnresolved(schema, data)
	if err != nil {
		return err
	}
	clearResolvedValues(resolved)
	clearPendingSecrets(pending)
	return nil
}

func decodeUnresolved(schema manifest.Config, data []byte) (map[string]resolvedValue, []pendingSecret, error) {
	root, err := decodeValuesDocument(data)
	if err != nil {
		return nil, nil, err
	}
	provided, err := indexProvidedValues(root)
	if err != nil {
		return nil, nil, err
	}

	declared := schema.Fields()
	declaredNames := make(map[string]struct{}, len(declared))
	for _, field := range declared {
		declaredNames[field.Name()] = struct{}{}
	}
	for name := range provided {
		if _, exists := declaredNames[name]; !exists {
			return nil, nil, newValuesError("", ErrUnknownField, nil)
		}
	}

	resolved := make(map[string]resolvedValue, len(declared))
	pending := make([]pendingSecret, 0)
	for _, field := range declared {
		node, exists := provided[field.Name()]
		if !exists {
			if field.HasDefault() {
				value, valueErr := decodeDefault(field)
				if valueErr != nil {
					clearResolvedValues(resolved)
					return nil, nil, newValuesError(field.Name(), ErrInvalidValue, nil)
				}
				resolved[field.Name()] = value
				continue
			}
			if field.Required() {
				clearResolvedValues(resolved)
				return nil, nil, newValuesError(field.Name(), ErrMissingField, nil)
			}
			continue
		}
		if field.Type() == manifest.ConfigSecret {
			reference, referenceErr := decodeSecretReference(node)
			if referenceErr != nil {
				clearResolvedValues(resolved)
				clearPendingSecrets(pending)
				return nil, nil, newValuesError(field.Name(), ErrInvalidValue, nil)
			}
			pending = append(pending, pendingSecret{name: field.Name(), reference: reference})
			continue
		}
		value, valueErr := decodeDeclaredValue(node, field.Type(), field.Items(), field.Format())
		if valueErr != nil || !enumContains(field, node, value) {
			clearResolvedValues(resolved)
			clearPendingSecrets(pending)
			return nil, nil, newValuesError(field.Name(), ErrInvalidValue, nil)
		}
		resolved[field.Name()] = resolvedValue{kind: field.Type(), value: value}
	}

	return resolved, pending, nil
}

type pendingSecret struct {
	name      string
	reference Reference
}

func decodeValuesDocument(data []byte) (*yaml.Node, error) {
	if len(data) == 0 || len(data) > manifest.MaximumDeclarationSize {
		return nil, newValuesError("", ErrInvalidValue, nil)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, newValuesError("", ErrInvalidValue, nil)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, newValuesError("", ErrInvalidValue, nil)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, newValuesError("", ErrInvalidValue, nil)
	}
	if !validValuesTree(&document) {
		return nil, newValuesError("", ErrInvalidValue, nil)
	}
	return document.Content[0], nil
}

func validValuesTree(root *yaml.Node) bool {
	type entry struct {
		node  *yaml.Node
		depth int
	}
	stack := []entry{{node: root}}
	nodes := 0
	for len(stack) != 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		node := current.node
		if node == nil {
			continue
		}
		nodes++
		if nodes > maximumConfigurationNodes || current.depth > maximumConfigurationDepth || node.Kind == yaml.AliasNode || node.Alias != nil || node.Anchor != "" {
			return false
		}
		for _, child := range node.Content {
			stack = append(stack, entry{node: child, depth: current.depth + 1})
		}
	}
	return true
}

func indexProvidedValues(root *yaml.Node) (map[string]*yaml.Node, error) {
	result := make(map[string]*yaml.Node, len(root.Content)/2)
	for index := 0; index < len(root.Content); index += 2 {
		name, err := strictValueString(root.Content[index])
		if err != nil {
			return nil, newValuesError("", ErrUnknownField, nil)
		}
		if _, duplicate := result[name]; duplicate {
			return nil, newValuesError("", ErrInvalidValue, nil)
		}
		result[name] = root.Content[index+1]
	}
	return result, nil
}

func decodeSecretReference(node *yaml.Node) (Reference, error) {
	if node == nil || node.Kind != yaml.MappingNode || len(node.Content) != 2 {
		return Reference{}, ErrInvalidReference
	}
	kind, err := strictValueString(node.Content[0])
	if err != nil {
		return Reference{}, ErrInvalidReference
	}
	target, err := strictValueString(node.Content[1])
	if err != nil {
		return Reference{}, ErrInvalidReference
	}
	switch kind {
	case string(ReferenceEnvironment):
		return NewEnvironmentReference(target)
	case string(ReferenceFile):
		return NewFileReference(target)
	default:
		return Reference{}, ErrInvalidReference
	}
}

func decodeDeclaredValue(node *yaml.Node, kind, items manifest.ConfigType, format string) (any, error) {
	switch kind {
	case manifest.ConfigString:
		value, err := strictValueString(node)
		if err != nil {
			return nil, err
		}
		if format == "email" {
			parsed, parseErr := mail.ParseAddress(value)
			if parseErr != nil || parsed.Address != value {
				return nil, ErrInvalidValue
			}
		}
		return value, nil
	case manifest.ConfigInteger:
		return strictValueInteger(node)
	case manifest.ConfigNumber:
		return strictValueNumber(node)
	case manifest.ConfigBoolean:
		return strictValueBoolean(node)
	case manifest.ConfigDuration:
		value, err := strictValueString(node)
		if err != nil {
			return nil, err
		}
		duration, err := time.ParseDuration(value)
		if err != nil || duration < 0 {
			return nil, ErrInvalidValue
		}
		return duration, nil
	case manifest.ConfigURL:
		value, err := strictValueString(node)
		if err != nil {
			return nil, err
		}
		parsed, err := url.Parse(value)
		if err != nil || !parsed.IsAbs() || parsed.Host == "" {
			return nil, ErrInvalidValue
		}
		return *parsed, nil
	case manifest.ConfigObject:
		return decodeValueObject(node)
	case manifest.ConfigArray:
		return decodeValueArray(node, items)
	default:
		return nil, ErrInvalidValue
	}
}

func decodeValueArray(node *yaml.Node, items manifest.ConfigType) (resolvedArray, error) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return resolvedArray{}, ErrInvalidValue
	}
	values := make([]any, len(node.Content))
	for index, child := range node.Content {
		value, err := decodeDeclaredValue(child, items, "", "")
		if err != nil {
			return resolvedArray{}, ErrInvalidValue
		}
		values[index] = value
	}
	array := resolvedArray{items: items}
	switch items {
	case manifest.ConfigString:
		array.value = mapSlice(values, func(value any) string { return value.(string) })
	case manifest.ConfigInteger:
		array.value = mapSlice(values, func(value any) int64 { return value.(int64) })
	case manifest.ConfigNumber:
		array.value = mapSlice(values, func(value any) float64 { return value.(float64) })
	case manifest.ConfigBoolean:
		array.value = mapSlice(values, func(value any) bool { return value.(bool) })
	case manifest.ConfigDuration:
		array.value = mapSlice(values, func(value any) time.Duration { return value.(time.Duration) })
	case manifest.ConfigURL:
		array.value = mapSlice(values, func(value any) url.URL { return value.(url.URL) })
	case manifest.ConfigObject:
		array.value = mapSlice(values, func(value any) map[string]any { return value.(map[string]any) })
	default:
		return resolvedArray{}, ErrInvalidValue
	}
	return array, nil
}

func mapSlice[T any](values []any, convert func(any) T) []T {
	result := make([]T, len(values))
	for index, value := range values {
		result[index] = convert(value)
	}
	return result
}

func decodeValueObject(node *yaml.Node) (map[string]any, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, ErrInvalidValue
	}
	result := make(map[string]any, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, err := strictValueString(node.Content[index])
		if err != nil {
			return nil, ErrInvalidValue
		}
		if _, duplicate := result[key]; duplicate {
			return nil, ErrInvalidValue
		}
		value, err := decodeJSONCompatibleValue(node.Content[index+1])
		if err != nil {
			return nil, ErrInvalidValue
		}
		result[key] = value
	}
	return result, nil
}

func decodeJSONCompatibleValue(node *yaml.Node) (any, error) {
	if node == nil {
		return nil, ErrInvalidValue
	}
	switch node.Kind {
	case yaml.MappingNode:
		return decodeValueObject(node)
	case yaml.SequenceNode:
		result := make([]any, len(node.Content))
		for index, child := range node.Content {
			value, err := decodeJSONCompatibleValue(child)
			if err != nil {
				return nil, err
			}
			result[index] = value
		}
		return result, nil
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!str":
			return node.Value, nil
		case "!!bool":
			return strictValueBoolean(node)
		case "!!int", "!!float":
			return strictJSONNumber(node)
		case "!!null":
			return nil, nil
		default:
			return nil, ErrInvalidValue
		}
	default:
		return nil, ErrInvalidValue
	}
}

func strictValueString(node *yaml.Node) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", ErrInvalidValue
	}
	return node.Value, nil
}

func strictValueBoolean(node *yaml.Node) (bool, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return false, ErrInvalidValue
	}
	switch node.Value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, ErrInvalidValue
	}
}

func strictValueInteger(node *yaml.Node) (int64, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" || !canonicalValueInteger(node.Value) {
		return 0, ErrInvalidValue
	}
	value, err := strconv.ParseInt(node.Value, 10, 64)
	if err != nil {
		return 0, ErrInvalidValue
	}
	return value, nil
}

func canonicalValueInteger(value string) bool {
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

func strictValueNumber(node *yaml.Node) (float64, error) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return 0, ErrInvalidValue
	}
	if node.Tag == "!!int" {
		integer, err := strictValueInteger(node)
		if err != nil {
			return 0, err
		}
		return float64(integer), nil
	}
	if node.Tag != "!!float" {
		return 0, ErrInvalidValue
	}
	decoder := json.NewDecoder(strings.NewReader(node.Value))
	var value float64
	if err := decoder.Decode(&value); err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, ErrInvalidValue
	}
	if decoder.Decode(new(any)) != io.EOF {
		return 0, ErrInvalidValue
	}
	return value, nil
}

func strictJSONNumber(node *yaml.Node) (json.Number, error) {
	if node.Tag == "!!int" {
		value, err := strictValueInteger(node)
		if err != nil {
			return "", err
		}
		return json.Number(strconv.FormatInt(value, 10)), nil
	}
	value, err := strictValueNumber(node)
	if err != nil {
		return "", err
	}
	return json.Number(strconv.FormatFloat(value, 'g', -1, 64)), nil
}

func decodeDefault(field manifest.ConfigField) (resolvedValue, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(field.DefaultJSON()))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil || document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return resolvedValue{}, ErrInvalidValue
	}
	value, err := decodeDeclaredValue(document.Content[0], field.Type(), field.Items(), field.Format())
	if err != nil {
		return resolvedValue{}, err
	}
	return resolvedValue{kind: field.Type(), value: value}, nil
}

func enumContains(field manifest.ConfigField, node *yaml.Node, value any) bool {
	allowed := field.EnumJSON()
	if len(allowed) == 0 {
		return true
	}
	encoded, err := encodeEnumInput(field.Type(), node, value)
	if err != nil {
		return false
	}
	for _, candidate := range allowed {
		if bytes.Equal(candidate, encoded) {
			return true
		}
	}
	return false
}

func encodeEnumInput(kind manifest.ConfigType, node *yaml.Node, value any) ([]byte, error) {
	switch kind {
	case manifest.ConfigString, manifest.ConfigDuration, manifest.ConfigURL:
		input, err := strictValueString(node)
		if err != nil {
			return nil, ErrInvalidValue
		}
		return json.Marshal(input)
	default:
		return json.Marshal(value)
	}
}

func clearPendingSecrets(values []pendingSecret) {
	for index := range values {
		values[index].reference.target = ""
		values[index].reference.initialized = false
	}
}

func clearResolvedValues(values map[string]resolvedValue) {
	for name, field := range values {
		if secret, ok := field.value.(Secret); ok {
			clear(secret.value)
			secret.value = nil
			secret.initialized = false
			field.value = Secret{}
			values[name] = field
		}
	}
}

type valuesError struct {
	field  string
	reason error
	cause  error
}

func newValuesError(field string, reason, cause error) error {
	return &valuesError{field: field, reason: reason, cause: contextError(cause)}
}

func (e *valuesError) Error() string {
	message := ErrInvalidValues.Error()
	if e.field != "" {
		message += ": field " + strconv.Quote(e.field)
	}
	if e.reason != nil {
		message += ": " + safeValueReason(e.reason).Error()
	}
	if e.cause != nil {
		message += ": " + e.cause.Error()
	}
	return message
}

func (e *valuesError) Is(target error) bool {
	if e == nil {
		return false
	}
	reason := safeValueReason(e.reason)
	return target == ErrInvalidValues || target == reason || e.cause != nil && target == e.cause || errors.Is(e.reason, target)
}

func safeValueReason(reason error) error {
	switch {
	case reason == nil:
		return nil
	case errors.Is(reason, ErrUnknownField):
		return ErrUnknownField
	case errors.Is(reason, ErrMissingField):
		return ErrMissingField
	case errors.Is(reason, ErrInvalidValue), errors.Is(reason, ErrInvalidReference):
		return ErrInvalidValue
	case errors.Is(reason, ErrSecretTooLarge):
		return ErrSecretTooLarge
	case errors.Is(reason, ErrSecretUnavailable):
		return ErrSecretUnavailable
	case errors.Is(reason, ErrResolve):
		return ErrResolve
	default:
		return ErrInvalidValue
	}
}
