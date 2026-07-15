package manifest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/kernel/plugin/manifest"
)

const validCapability = `id: email.send/v1
description: Sends an email message.
request:
  to:
    type: array
    items: string
    required: true
  subject:
    type: string
    required: true
  attempts:
    type: integer
  ratio:
    type: number
  urgent:
    type: boolean
  metadata:
    type: object
response:
  message_id:
    type: string
    required: true
  status:
    type: string
    enum: [queued, sent]
    required: true
errors:
  - temporarily_unavailable
  - invalid_recipient
  - authentication_failed
`

func TestParseCapability(t *testing.T) {
	t.Parallel()

	contract, err := manifest.ParseCapability([]byte(validCapability))
	if err != nil {
		t.Fatalf("ParseCapability: %v", err)
	}
	if got := contract.ID().String(); got != "email.send/v1" {
		t.Fatalf("ID() = %q, want email.send/v1", got)
	}
	if got := contract.Description(); got != "Sends an email message." {
		t.Fatalf("Description() = %q", got)
	}
	wantRequest := []string{"attempts", "metadata", "ratio", "subject", "to", "urgent"}
	if got := schemaFieldNames(contract.Request()); !reflect.DeepEqual(got, wantRequest) {
		t.Fatalf("Request().Fields() = %v, want %v", got, wantRequest)
	}
	to, ok := contract.Request().Lookup("to")
	if !ok || to.Type() != manifest.SchemaArray || to.Items() != manifest.SchemaString || !to.Required() {
		t.Fatalf("request.to = %#v", to)
	}
	status, ok := contract.Response().Lookup("status")
	if !ok || status.Type() != manifest.SchemaString || !status.Required() || !reflect.DeepEqual(jsonStrings(status.EnumJSON()), []string{`"queued"`, `"sent"`}) {
		t.Fatalf("response.status = %#v", status)
	}
	wantErrors := []string{"authentication_failed", "invalid_recipient", "temporarily_unavailable"}
	if got := contract.Errors(); !reflect.DeepEqual(got, wantErrors) {
		t.Fatalf("Errors() = %v, want %v", got, wantErrors)
	}
	if _, exists := contract.Request().Lookup("missing"); exists {
		t.Fatal("Request().Lookup(missing) succeeded")
	}

	requestFields := contract.Request().Fields()
	requestFields[0] = manifest.SchemaField{}
	errors := contract.Errors()
	errors[0] = "changed"
	enum := status.EnumJSON()
	enum[0][0] = 'x'
	if contract.Request().Fields()[0].Name() != "attempts" || contract.Errors()[0] != "authentication_failed" || string(status.EnumJSON()[0]) != `"queued"` {
		t.Fatal("Capability accessors exposed mutable storage")
	}
}

func TestParseCapabilityAllowsEmptySchemas(t *testing.T) {
	t.Parallel()

	contract, err := manifest.ParseCapability([]byte("id: kernel.health/v1\nrequest: {}\nresponse: {}\nerrors: []\n"))
	if err != nil {
		t.Fatalf("ParseCapability: %v", err)
	}
	if contract.ID().String() != "kernel.health/v1" || len(contract.Request().Fields()) != 0 || len(contract.Response().Fields()) != 0 || len(contract.Errors()) != 0 {
		t.Fatalf("empty capability = %#v", contract)
	}
}

func TestParseCapabilityCanonicalizesScalarEnums(t *testing.T) {
	t.Parallel()

	contract, err := manifest.ParseCapability([]byte(`id: example.scalar-enums/v1
request:
  integer_value: {type: integer, enum: [-1, 0, 2]}
  number_value: {type: number, enum: [1, 1.5]}
  boolean_value: {type: boolean, enum: [true, false]}
`))
	if err != nil {
		t.Fatalf("ParseCapability: %v", err)
	}
	tests := []struct {
		name string
		want []string
	}{
		{name: "integer_value", want: []string{"-1", "0", "2"}},
		{name: "number_value", want: []string{"1", "1.5"}},
		{name: "boolean_value", want: []string{"true", "false"}},
	}
	for _, test := range tests {
		field, ok := contract.Request().Lookup(test.name)
		if !ok || !reflect.DeepEqual(jsonStrings(field.EnumJSON()), test.want) {
			t.Fatalf("request.%s enum = %v, want %v", test.name, jsonStrings(field.EnumJSON()), test.want)
		}
	}
}

func TestParseCapabilityRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "not mapping", input: "[]\n"},
		{name: "multiple documents", input: "id: email.send/v1\n---\nid: email.send/v2\n"},
		{name: "anchor", input: "id: &identity email.send/v1\n"},
		{name: "alias", input: "id: &identity email.send/v1\ndescription: *identity\n"},
		{name: "non string key", input: "1: value\n"},
		{name: "unknown key", input: "id: email.send/v1\nversion: 1\n"},
		{name: "duplicate key", input: "id: email.send/v1\nid: email.send/v2\n"},
		{name: "missing id", input: "request: {}\n"},
		{name: "non string id", input: "id: 1\n"},
		{name: "invalid id", input: "id: email.send\n"},
		{name: "non string description", input: "id: email.send/v1\ndescription: 1\n"},
		{name: "request not mapping", input: "id: email.send/v1\nrequest: []\n"},
		{name: "response not mapping", input: "id: email.send/v1\nresponse: []\n"},
		{name: "invalid field name", input: "id: email.send/v1\nrequest:\n  BadName: {type: string}\n"},
		{name: "duplicate field", input: "id: email.send/v1\nrequest:\n  field: {type: string}\n  field: {type: string}\n"},
		{name: "field not mapping", input: "id: email.send/v1\nrequest:\n  field: string\n"},
		{name: "missing field type", input: "id: email.send/v1\nrequest:\n  field: {required: true}\n"},
		{name: "non string field type", input: "id: email.send/v1\nrequest:\n  field: {type: 1}\n"},
		{name: "unknown field type", input: "id: email.send/v1\nrequest:\n  field: {type: bytes}\n"},
		{name: "unknown field key", input: "id: email.send/v1\nrequest:\n  field: {type: string, default: value}\n"},
		{name: "duplicate field key", input: "id: email.send/v1\nrequest:\n  field: {type: string, type: string}\n"},
		{name: "non boolean required", input: "id: email.send/v1\nrequest:\n  field: {type: string, required: yes}\n"},
		{name: "items on string", input: "id: email.send/v1\nrequest:\n  field: {type: string, items: string}\n"},
		{name: "array missing items", input: "id: email.send/v1\nrequest:\n  field: {type: array}\n"},
		{name: "nested array", input: "id: email.send/v1\nrequest:\n  field: {type: array, items: array}\n"},
		{name: "unknown item type", input: "id: email.send/v1\nrequest:\n  field: {type: array, items: bytes}\n"},
		{name: "empty enum", input: "id: email.send/v1\nrequest:\n  field: {type: string, enum: []}\n"},
		{name: "enum not sequence", input: "id: email.send/v1\nrequest:\n  field: {type: string, enum: one}\n"},
		{name: "enum type mismatch", input: "id: email.send/v1\nrequest:\n  field: {type: integer, enum: [one]}\n"},
		{name: "noncanonical integer enum", input: "id: email.send/v1\nrequest:\n  field: {type: integer, enum: [01]}\n"},
		{name: "non finite number enum", input: "id: email.send/v1\nrequest:\n  field: {type: number, enum: [.nan]}\n"},
		{name: "duplicate enum", input: "id: email.send/v1\nrequest:\n  field: {type: string, enum: [one, one]}\n"},
		{name: "object enum", input: "id: email.send/v1\nrequest:\n  field: {type: object, enum: [{key: value}]}\n"},
		{name: "array enum", input: "id: email.send/v1\nrequest:\n  field: {type: array, items: string, enum: [[one]]}\n"},
		{name: "errors not sequence", input: "id: email.send/v1\nerrors: invalid_recipient\n"},
		{name: "non string error", input: "id: email.send/v1\nerrors: [1]\n"},
		{name: "invalid error code", input: "id: email.send/v1\nerrors: [InvalidRecipient]\n"},
		{name: "duplicate error", input: "id: email.send/v1\nerrors: [invalid_recipient, invalid_recipient]\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contract, err := manifest.ParseCapability([]byte(test.input))
			if !errors.Is(err, manifest.ErrInvalidCapability) {
				t.Fatalf("ParseCapability error = %v, want ErrInvalidCapability", err)
			}
			if contract.ID().String() != "" || len(contract.Request().Fields()) != 0 || len(contract.Response().Fields()) != 0 || len(contract.Errors()) != 0 {
				t.Fatalf("invalid declaration returned data: %#v", contract)
			}
		})
	}
}

func TestParseSchemaType(t *testing.T) {
	t.Parallel()

	for _, value := range []manifest.SchemaType{
		manifest.SchemaString,
		manifest.SchemaInteger,
		manifest.SchemaNumber,
		manifest.SchemaBoolean,
		manifest.SchemaObject,
		manifest.SchemaArray,
	} {
		parsed, err := manifest.ParseSchemaType(string(value))
		if err != nil || parsed != value {
			t.Fatalf("ParseSchemaType(%q) = %q, %v", value, parsed, err)
		}
	}
	if parsed, err := manifest.ParseSchemaType("unknown"); !errors.Is(err, manifest.ErrInvalidCapability) || parsed != "" {
		t.Fatalf("ParseSchemaType(unknown) = %q, %v", parsed, err)
	}
}

func TestParseCapabilityRejectsOversizedDocument(t *testing.T) {
	t.Parallel()

	input := strings.Repeat("x", manifest.MaximumDeclarationSize+1)
	if _, err := manifest.ParseCapability([]byte(input)); !errors.Is(err, manifest.ErrInvalidCapability) {
		t.Fatalf("ParseCapability oversized error = %v, want ErrInvalidCapability", err)
	}
}

func FuzzParseCapability(f *testing.F) {
	for _, seed := range []string{"id: kernel.health/v1\n", validCapability, "[]\n", "id: &x email.send/v1\ndescription: *x\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		contract, err := manifest.ParseCapability([]byte(input))
		if err != nil {
			if !errors.Is(err, manifest.ErrInvalidCapability) {
				t.Fatalf("ParseCapability returned unexpected error: %v", err)
			}
			return
		}
		if contract.ID().String() == "" {
			t.Fatal("ParseCapability returned a capability without an ID")
		}
		first, err := contract.CanonicalSchemaJSON()
		if err != nil || !json.Valid(first) {
			t.Fatalf("CanonicalSchemaJSON = %q, %v", first, err)
		}
		second, err := contract.CanonicalSchemaJSON()
		if err != nil || !bytes.Equal(first, second) {
			t.Fatalf("canonical schema is not deterministic: %q then %q, %v", first, second, err)
		}
		assertSortedSchema(t, contract.Request())
		assertSortedSchema(t, contract.Response())
		for index := 1; index < len(contract.Errors()); index++ {
			if contract.Errors()[index-1] >= contract.Errors()[index] {
				t.Fatalf("errors are not uniquely sorted: %q then %q", contract.Errors()[index-1], contract.Errors()[index])
			}
		}
	})
}

func assertSortedSchema(t *testing.T, schema manifest.Schema) {
	t.Helper()
	fields := schema.Fields()
	for index := 1; index < len(fields); index++ {
		if fields[index-1].Name() >= fields[index].Name() {
			t.Fatalf("fields are not uniquely sorted: %q then %q", fields[index-1].Name(), fields[index].Name())
		}
	}
}

func schemaFieldNames(schema manifest.Schema) []string {
	fields := schema.Fields()
	names := make([]string, len(fields))
	for index := range fields {
		names[index] = fields[index].Name()
	}
	return names
}
