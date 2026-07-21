package manifest_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/plystra/kernel/plugin/manifest"
)

func TestParseCapabilityFieldConstraints(t *testing.T) {
	t.Parallel()

	contract, err := manifest.ParseCapability([]byte(`id: example.constraints/v1
request:
  email:
    type: string
    constraints:
      min_length: 3
      max_length: 254
      pattern: '^[^@]+@[^@]+$'
  attempt:
    type: integer
    constraints: {minimum: -10, maximum: 10}
  ratio:
    type: number
    constraints: {minimum: 0.1, maximum: 10.5}
  recipients:
    type: array
    items: string
    constraints: {min_items: 1, max_items: 100}
  enabled: {type: boolean, constraints: {}}
response:
  label: {type: string, constraints: {min_length: 0, max_length: 2147483647, pattern: ''}}
semantics:
  kind: query
  effects: none
  idempotency: {mode: none}
  retry: {safety: never}
  cancellation: {mode: unsupported}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`))
	if err != nil {
		t.Fatalf("ParseCapability: %v", err)
	}

	email, _ := contract.Request().Lookup("email")
	if minimum, ok := email.Constraints().MinLength(); !ok || minimum != 3 {
		t.Fatalf("email min_length = %d, %t", minimum, ok)
	}
	if maximum, ok := email.Constraints().MaxLength(); !ok || maximum != 254 {
		t.Fatalf("email max_length = %d, %t", maximum, ok)
	}
	if pattern, ok := email.Constraints().Pattern(); !ok || pattern != `^[^@]+@[^@]+$` {
		t.Fatalf("email pattern = %q, %t", pattern, ok)
	}

	attempt, _ := contract.Request().Lookup("attempt")
	minimum, ok := attempt.Constraints().Minimum()
	if !ok {
		t.Fatal("attempt minimum is absent")
	}
	if value, integer := minimum.Integer(); !integer || value != -10 || string(minimum.JSON()) != "-10" {
		t.Fatalf("attempt minimum = %d, %t, %s", value, integer, minimum.JSON())
	}
	maximum, ok := attempt.Constraints().Maximum()
	if !ok {
		t.Fatal("attempt maximum is absent")
	}
	if value, integer := maximum.Integer(); !integer || value != 10 || string(maximum.JSON()) != "10" {
		t.Fatalf("attempt maximum = %d, %t, %s", value, integer, maximum.JSON())
	}

	ratio, _ := contract.Request().Lookup("ratio")
	minimum, ok = ratio.Constraints().Minimum()
	if !ok {
		t.Fatal("ratio minimum is absent")
	}
	if value, number := minimum.Number(); !number || value != 0.1 || string(minimum.JSON()) != "0.1" {
		t.Fatalf("ratio minimum = %v, %t, %s", value, number, minimum.JSON())
	}
	maximum, ok = ratio.Constraints().Maximum()
	if !ok {
		t.Fatal("ratio maximum is absent")
	}
	if value, number := maximum.Number(); !number || value != 10.5 || string(maximum.JSON()) != "10.5" {
		t.Fatalf("ratio maximum = %v, %t, %s", value, number, maximum.JSON())
	}

	recipients, _ := contract.Request().Lookup("recipients")
	if minimum, ok := recipients.Constraints().MinItems(); !ok || minimum != 1 {
		t.Fatalf("recipients min_items = %d, %t", minimum, ok)
	}
	if maximum, ok := recipients.Constraints().MaxItems(); !ok || maximum != 100 {
		t.Fatalf("recipients max_items = %d, %t", maximum, ok)
	}
	enabled, _ := contract.Request().Lookup("enabled")
	if !enabled.Constraints().Empty() {
		t.Fatalf("enabled constraints = %#v", enabled.Constraints())
	}
	label, _ := contract.Response().Lookup("label")
	if minimum, ok := label.Constraints().MinLength(); !ok || minimum != 0 {
		t.Fatalf("label min_length = %d, %t", minimum, ok)
	}
	if maximum, ok := label.Constraints().MaxLength(); !ok || maximum != manifest.MaximumConstraintCount {
		t.Fatalf("label max_length = %d, %t", maximum, ok)
	}
	if pattern, ok := label.Constraints().Pattern(); !ok || pattern != "" {
		t.Fatalf("label pattern = %q, %t", pattern, ok)
	}

	encoded := minimum.JSON()
	encoded[0] = '9'
	if string(minimum.JSON()) != "0.1" {
		t.Fatal("NumericBound.JSON exposed mutable storage")
	}
}

func TestParseCapabilityRejectsInvalidFieldConstraints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
		want  string
	}{
		{name: "not mapping", field: "value: {type: string, constraints: []}", want: "request.value.constraints must be a mapping"},
		{name: "non-string key", field: "value:\n  type: string\n  constraints: {1: 2}", want: "contains a non-string key"},
		{name: "duplicate key", field: "value:\n  type: string\n  constraints:\n    min_length: 1\n    min_length: 2", want: "contains duplicate key"},
		{name: "unknown key", field: "value: {type: string, constraints: {format: email}}", want: `contains unknown key "format"`},
		{name: "string key on integer", field: "value: {type: integer, constraints: {min_length: 1}}", want: "is not supported for \"integer\" fields"},
		{name: "number key on string", field: "value: {type: string, constraints: {minimum: 1}}", want: "is not supported for \"string\" fields"},
		{name: "array key on object", field: "value: {type: object, constraints: {min_items: 1}}", want: "is not supported for \"object\" fields"},
		{name: "constraint on boolean", field: "value: {type: boolean, constraints: {pattern: x}}", want: "is not supported for \"boolean\" fields"},
		{name: "length not integer", field: "value: {type: string, constraints: {min_length: 1.5}}", want: "must be a canonical integer"},
		{name: "negative length", field: "value: {type: string, constraints: {min_length: -1}}", want: "from 0 through 2147483647"},
		{name: "excessive length", field: "value: {type: string, constraints: {max_length: 2147483648}}", want: "from 0 through 2147483647"},
		{name: "reversed length", field: "value: {type: string, constraints: {min_length: 2, max_length: 1}}", want: "min_length must not exceed"},
		{name: "pattern not string", field: "value: {type: string, constraints: {pattern: 1}}", want: "must be a valid UTF-8 string"},
		{name: "invalid pattern", field: "value: {type: string, constraints: {pattern: '['}}", want: "valid deterministic Go regular-expression syntax"},
		{name: "oversized pattern", field: "value: {type: string, constraints: {pattern: '" + strings.Repeat("a", manifest.MaximumConstraintPatternBytes+1) + "'}}", want: "exceeds 4096 bytes"},
		{name: "integer minimum not integer", field: "value: {type: integer, constraints: {minimum: 1.5}}", want: "canonical signed 64-bit integer"},
		{name: "number minimum not number", field: "value: {type: number, constraints: {minimum: low}}", want: "finite canonical JSON number"},
		{name: "non-finite number", field: "value: {type: number, constraints: {minimum: .inf}}", want: "finite canonical JSON number"},
		{name: "overflowing number", field: "value: {type: number, constraints: {minimum: 1e309}}", want: "finite canonical JSON number"},
		{name: "inexact normalized number", field: "value: {type: number, constraints: {minimum: 9007199254740993}}", want: "cannot be represented exactly"},
		{name: "reversed integer bounds", field: "value: {type: integer, constraints: {minimum: 2, maximum: 1}}", want: "minimum must not exceed"},
		{name: "reversed number bounds", field: "value: {type: number, constraints: {minimum: 2.5, maximum: 1.5}}", want: "minimum must not exceed"},
		{name: "items not integer", field: "value: {type: array, items: string, constraints: {min_items: false}}", want: "must be a canonical integer"},
		{name: "negative items", field: "value: {type: array, items: string, constraints: {min_items: -1}}", want: "from 0 through 2147483647"},
		{name: "excessive items", field: "value: {type: array, items: string, constraints: {max_items: 2147483648}}", want: "from 0 through 2147483647"},
		{name: "reversed items", field: "value: {type: array, items: string, constraints: {min_items: 2, max_items: 1}}", want: "min_items must not exceed"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contract, err := manifest.ParseCapability([]byte(capabilityWithSemantics(test.field, validQuerySemantics)))
			if !errors.Is(err, manifest.ErrInvalidCapability) || contract.ID().String() != "" {
				t.Fatalf("ParseCapability = %#v, %v", contract, err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseCapability error = %q, want %q", err, test.want)
			}
		})
	}
}

func TestCapabilityConstraintsNormalizeDeterministically(t *testing.T) {
	t.Parallel()

	firstSource := capabilityWithSemantics(`label:
  type: string
  constraints: {pattern: '', max_length: 10, min_length: 1}
ratio:
  type: number
  constraints: {maximum: 1e3, minimum: -0.0}
empty: {type: object, constraints: {}}`, validQuerySemantics)
	secondSource := capabilityWithSemantics(`empty: {constraints: {}, type: object}
ratio:
  constraints: {minimum: 0, maximum: 1000}
  type: number
label:
  constraints:
    min_length: 1
    max_length: 10
    pattern: ''
  type: string`, validQuerySemantics)
	first, err := manifest.ParseCapability([]byte(firstSource))
	if err != nil {
		t.Fatalf("ParseCapability(first): %v", err)
	}
	second, err := manifest.ParseCapability([]byte(secondSource))
	if err != nil {
		t.Fatalf("ParseCapability(second): %v", err)
	}
	firstJSON, err := first.CanonicalSchemaJSON()
	if err != nil {
		t.Fatalf("CanonicalSchemaJSON(first): %v", err)
	}
	secondJSON, err := second.CanonicalSchemaJSON()
	if err != nil {
		t.Fatalf("CanonicalSchemaJSON(second): %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("equivalent constraints changed canonical schema:\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
	for _, want := range []string{
		`"constraints":{"min_length":1,"max_length":10,"pattern":""}`,
		`"constraints":{"minimum":0,"maximum":1000}`,
	} {
		if !bytes.Contains(firstJSON, []byte(want)) {
			t.Fatalf("canonical schema %s omits %s", firstJSON, want)
		}
	}
	if bytes.Contains(firstJSON, []byte(`"empty":{"type":"object","constraints":`)) {
		t.Fatalf("empty constraints were not normalized away: %s", firstJSON)
	}
	firstDigest, err := first.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest(first): %v", err)
	}
	secondDigest, err := second.SchemaDigest()
	if err != nil || firstDigest != secondDigest {
		t.Fatalf("equivalent constraint digest = %x then %x, %v", firstDigest, secondDigest, err)
	}

	changed, err := manifest.ParseCapability([]byte(strings.Replace(firstSource, "max_length: 10", "max_length: 11", 1)))
	if err != nil {
		t.Fatalf("ParseCapability(changed): %v", err)
	}
	changedJSON, err := changed.CanonicalSchemaJSON()
	if err != nil || bytes.Equal(firstJSON, changedJSON) {
		t.Fatalf("changed constraints canonical schema = %s, %v", changedJSON, err)
	}
	changedDigest, err := changed.SchemaDigest()
	if err != nil || changedDigest == firstDigest {
		t.Fatalf("changed constraints digest = %x, %v", changedDigest, err)
	}
}

func FuzzParseCapabilityConstraints(f *testing.F) {
	for _, seed := range []string{
		"min_length: 1\nmax_length: 10\npattern: '^[a-z]+$'",
		"unknown: true",
		"min_length: -1",
		"pattern: '['",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, constraints string) {
		field := fmt.Sprintf("value:\n  type: string\n  constraints:\n%s", indentConstraintFuzzInput(constraints))
		contract, err := manifest.ParseCapability([]byte(capabilityWithSemantics(field, validQuerySemantics)))
		if err != nil {
			if !errors.Is(err, manifest.ErrInvalidCapability) {
				t.Fatalf("ParseCapability returned unexpected error: %v", err)
			}
			return
		}
		first, err := contract.CanonicalSchemaJSON()
		if err != nil {
			t.Fatalf("CanonicalSchemaJSON(first): %v", err)
		}
		second, err := contract.CanonicalSchemaJSON()
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("constraint normalization is not deterministic: %s then %s, %v", first, second, err)
		}
	})
}

func indentConstraintFuzzInput(value string) string {
	var builder strings.Builder
	for _, line := range strings.Split(value, "\n") {
		builder.WriteString("    ")
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	return builder.String()
}
