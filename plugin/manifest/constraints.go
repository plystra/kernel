package manifest

import (
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const (
	// MaximumConstraintCount is the largest portable string-length or array-item
	// bound accepted by the canonical constraint model.
	MaximumConstraintCount uint32 = 1<<31 - 1
	// MaximumConstraintPatternBytes bounds regular-expression parsing and
	// compilation work for one canonical string field.
	MaximumConstraintPatternBytes = 4096
)

// NumericBound is one immutable normalized integer or number constraint.
type NumericBound struct {
	present    bool
	schemaType SchemaType
	integer    int64
	number     float64
	json       []byte
}

// JSON returns a defensive copy of the canonical JSON number.
func (b NumericBound) JSON() []byte {
	return append([]byte(nil), b.json...)
}

// Integer returns the exact signed integer bound when it belongs to an integer
// field.
func (b NumericBound) Integer() (int64, bool) {
	return b.integer, b.present && b.schemaType == SchemaInteger
}

// Number returns the normalized finite float64 bound when it belongs to a
// number field.
func (b NumericBound) Number() (float64, bool) {
	return b.number, b.present && b.schemaType == SchemaNumber
}

// FieldConstraints is one closed immutable field-type-specific constraint set.
type FieldConstraints struct {
	minLength    uint32
	hasMinLength bool
	maxLength    uint32
	hasMaxLength bool
	pattern      string
	hasPattern   bool
	minimum      NumericBound
	hasMinimum   bool
	maximum      NumericBound
	hasMaximum   bool
	minItems     uint32
	hasMinItems  bool
	maxItems     uint32
	hasMaxItems  bool
}

func (c FieldConstraints) MinLength() (uint32, bool) { return c.minLength, c.hasMinLength }
func (c FieldConstraints) MaxLength() (uint32, bool) { return c.maxLength, c.hasMaxLength }
func (c FieldConstraints) Pattern() (string, bool)   { return c.pattern, c.hasPattern }
func (c FieldConstraints) Minimum() (NumericBound, bool) {
	return c.minimum, c.hasMinimum
}
func (c FieldConstraints) Maximum() (NumericBound, bool) {
	return c.maximum, c.hasMaximum
}
func (c FieldConstraints) MinItems() (uint32, bool) { return c.minItems, c.hasMinItems }
func (c FieldConstraints) MaxItems() (uint32, bool) { return c.maxItems, c.hasMaxItems }

// Empty reports whether no canonical constraint is present.
func (c FieldConstraints) Empty() bool {
	return !c.hasMinLength && !c.hasMaxLength && !c.hasPattern &&
		!c.hasMinimum && !c.hasMaximum && !c.hasMinItems && !c.hasMaxItems
}

func parseFieldConstraints(path string, node *yaml.Node, schemaType SchemaType) (FieldConstraints, error) {
	if node == nil {
		return FieldConstraints{}, nil
	}
	if node.Kind != yaml.MappingNode {
		return FieldConstraints{}, invalidCapability("%s must be a mapping", path)
	}

	var constraints FieldConstraints
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		keyNode, valueNode := node.Content[index], node.Content[index+1]
		key, err := strictString(keyNode)
		if err != nil {
			return FieldConstraints{}, invalidCapability("%s contains a non-string key", path)
		}
		if _, duplicate := seen[key]; duplicate {
			return FieldConstraints{}, invalidCapability("%s contains duplicate key %q", path, key)
		}
		seen[key] = struct{}{}

		fieldPath := path + "." + key
		switch key {
		case "min_length":
			if schemaType != SchemaString {
				return FieldConstraints{}, unsupportedConstraint(fieldPath, schemaType)
			}
			constraints.minLength, err = parseConstraintCount(fieldPath, valueNode)
			constraints.hasMinLength = err == nil
		case "max_length":
			if schemaType != SchemaString {
				return FieldConstraints{}, unsupportedConstraint(fieldPath, schemaType)
			}
			constraints.maxLength, err = parseConstraintCount(fieldPath, valueNode)
			constraints.hasMaxLength = err == nil
		case "pattern":
			if schemaType != SchemaString {
				return FieldConstraints{}, unsupportedConstraint(fieldPath, schemaType)
			}
			constraints.pattern, err = parseConstraintPattern(fieldPath, valueNode)
			constraints.hasPattern = err == nil
		case "minimum":
			if schemaType != SchemaInteger && schemaType != SchemaNumber {
				return FieldConstraints{}, unsupportedConstraint(fieldPath, schemaType)
			}
			constraints.minimum, err = parseNumericBound(fieldPath, valueNode, schemaType)
			constraints.hasMinimum = err == nil
		case "maximum":
			if schemaType != SchemaInteger && schemaType != SchemaNumber {
				return FieldConstraints{}, unsupportedConstraint(fieldPath, schemaType)
			}
			constraints.maximum, err = parseNumericBound(fieldPath, valueNode, schemaType)
			constraints.hasMaximum = err == nil
		case "min_items":
			if schemaType != SchemaArray {
				return FieldConstraints{}, unsupportedConstraint(fieldPath, schemaType)
			}
			constraints.minItems, err = parseConstraintCount(fieldPath, valueNode)
			constraints.hasMinItems = err == nil
		case "max_items":
			if schemaType != SchemaArray {
				return FieldConstraints{}, unsupportedConstraint(fieldPath, schemaType)
			}
			constraints.maxItems, err = parseConstraintCount(fieldPath, valueNode)
			constraints.hasMaxItems = err == nil
		default:
			return FieldConstraints{}, invalidCapability("%s contains unknown key %q", path, key)
		}
		if err != nil {
			return FieldConstraints{}, err
		}
	}

	if constraints.hasMinLength && constraints.hasMaxLength && constraints.minLength > constraints.maxLength {
		return FieldConstraints{}, invalidCapability("%s.min_length must not exceed %s.max_length", path, path)
	}
	if constraints.hasMinimum && constraints.hasMaximum && compareNumericBounds(constraints.minimum, constraints.maximum) > 0 {
		return FieldConstraints{}, invalidCapability("%s.minimum must not exceed %s.maximum", path, path)
	}
	if constraints.hasMinItems && constraints.hasMaxItems && constraints.minItems > constraints.maxItems {
		return FieldConstraints{}, invalidCapability("%s.min_items must not exceed %s.max_items", path, path)
	}
	return constraints, nil
}

func unsupportedConstraint(path string, schemaType SchemaType) error {
	return invalidCapability("%s is not supported for %q fields", path, schemaType)
}

func parseConstraintCount(path string, node *yaml.Node) (uint32, error) {
	value, err := strictInteger(node)
	if err != nil || value < 0 || value > int64(MaximumConstraintCount) {
		return 0, invalidCapability("%s must be a canonical integer from 0 through %d", path, MaximumConstraintCount)
	}
	return uint32(value), nil
}

func parseConstraintPattern(path string, node *yaml.Node) (string, error) {
	value, err := strictString(node)
	if err != nil || !utf8.ValidString(value) {
		return "", invalidCapability("%s must be a valid UTF-8 string", path)
	}
	if len(value) > MaximumConstraintPatternBytes {
		return "", invalidCapability("%s exceeds %d bytes", path, MaximumConstraintPatternBytes)
	}
	if _, err := regexp.Compile(value); err != nil {
		return "", invalidCapability("%s must use valid deterministic Go regular-expression syntax: %v", path, err)
	}
	return value, nil
}

func parseNumericBound(path string, node *yaml.Node, schemaType SchemaType) (NumericBound, error) {
	if schemaType == SchemaInteger {
		value, err := strictInteger(node)
		if err != nil {
			return NumericBound{}, invalidCapability("%s must be a canonical signed 64-bit integer", path)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return NumericBound{}, invalidCapability("%s cannot be normalized", path)
		}
		return NumericBound{present: true, schemaType: schemaType, integer: value, json: encoded}, nil
	}

	if _, err := strictNumber(node); err != nil {
		return NumericBound{}, invalidCapability("%s must be a finite canonical JSON number", path)
	}
	value, err := strconv.ParseFloat(node.Value, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return NumericBound{}, invalidCapability("%s must be a finite canonical JSON number", path)
	}
	if value == 0 {
		value = 0
	}
	if !numberRoundTripsExactly(node.Value, value) {
		return NumericBound{}, invalidCapability("%s cannot be represented exactly by the normalized number model", path)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return NumericBound{}, invalidCapability("%s cannot be normalized", path)
	}
	return NumericBound{present: true, schemaType: schemaType, number: value, json: encoded}, nil
}

func numberRoundTripsExactly(source string, value float64) bool {
	sourceValue, ok := new(big.Rat).SetString(source)
	if !ok {
		return false
	}
	normalized := strconv.FormatFloat(value, 'g', -1, 64)
	normalizedValue, ok := new(big.Rat).SetString(normalized)
	return ok && sourceValue.Cmp(normalizedValue) == 0
}

func compareNumericBounds(left, right NumericBound) int {
	if left.schemaType == SchemaInteger {
		switch {
		case left.integer < right.integer:
			return -1
		case left.integer > right.integer:
			return 1
		default:
			return 0
		}
	}
	switch {
	case left.number < right.number:
		return -1
	case left.number > right.number:
		return 1
	default:
		return 0
	}
}
