package manifest_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/kernel/plugin/manifest"
)

const validConfig = `sender:
  type: string
  format: email
  required: true
host:
  type: string
  required: true
port:
  type: integer
  default: 587
security:
  type: string
  enum: [none, starttls, tls]
  default: starttls
timeout:
  type: duration
  default: 10s
username:
  type: secret
  required: true
password:
  type: secret
  required: true
endpoint:
  type: url
  default: https://smtp.example.com/api
retry_delays:
  type: array
  items: duration
  default: [1s, 5s]
metadata:
  type: object
  default: {region: hk, retries: 3, enabled: true}
ratio:
  type: number
  default: 1.5
`

func TestParseConfig(t *testing.T) {
	t.Parallel()

	config, err := manifest.ParseConfig([]byte(validConfig))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	fields := config.Fields()
	var names []string
	for _, field := range fields {
		names = append(names, field.Name())
	}
	wantNames := []string{"endpoint", "host", "metadata", "password", "port", "ratio", "retry_delays", "security", "sender", "timeout", "username"}
	// The fixture intentionally contains eleven distinct fields; keep the
	// canonical list explicit so adding or losing a field cannot pass silently.
	if len(fields) != len(wantNames) || !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("field names = %v, want %v", names, wantNames)
	}
	security, ok := config.Lookup("security")
	if !ok || security.Type() != manifest.ConfigString || !security.HasDefault() || string(security.DefaultJSON()) != `"starttls"` {
		t.Fatalf("security field = %#v", security)
	}
	if got := jsonStrings(security.EnumJSON()); !reflect.DeepEqual(got, []string{`"none"`, `"starttls"`, `"tls"`}) {
		t.Fatalf("security enum = %v", got)
	}
	retries, ok := config.Lookup("retry_delays")
	if !ok || retries.Type() != manifest.ConfigArray || retries.Items() != manifest.ConfigDuration || string(retries.DefaultJSON()) != `["1s","5s"]` {
		t.Fatalf("retry_delays field = %#v", retries)
	}
	metadata, ok := config.Lookup("metadata")
	if !ok || string(metadata.DefaultJSON()) != `{"enabled":true,"region":"hk","retries":3}` {
		t.Fatalf("metadata default = %s", metadata.DefaultJSON())
	}
	sender, ok := config.Lookup("sender")
	if !ok || sender.Format() != "email" || !sender.Required() {
		t.Fatalf("sender field = %#v", sender)
	}
	password, ok := config.Lookup("password")
	if !ok || password.Type() != manifest.ConfigSecret || password.HasDefault() {
		t.Fatalf("password field = %#v", password)
	}
	if _, exists := config.Lookup("missing"); exists {
		t.Fatal("Lookup(missing) succeeded")
	}

	fields[0] = manifest.ConfigField{}
	defaultValue := security.DefaultJSON()
	defaultValue[0] = 'x'
	enum := security.EnumJSON()
	enum[0][0] = 'x'
	if config.Fields()[0].Name() != "endpoint" || string(security.DefaultJSON()) != `"starttls"` || string(security.EnumJSON()[0]) != `"none"` {
		t.Fatal("configuration accessors exposed mutable storage")
	}
}

func TestParseConfigAllowsEmptyMapping(t *testing.T) {
	t.Parallel()

	config, err := manifest.ParseConfig([]byte("{}\n"))
	if err != nil || len(config.Fields()) != 0 {
		t.Fatalf("ParseConfig(empty) = %#v, %v", config, err)
	}
}

func TestParseConfigRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "not mapping", input: "[]\n"},
		{name: "multiple documents", input: "{}\n---\n{}\n"},
		{name: "anchor", input: "field: &shared {type: string}\n"},
		{name: "alias", input: "first: &shared {type: string}\nsecond: *shared\n"},
		{name: "invalid field name", input: "BadName: {type: string}\n"},
		{name: "duplicate field", input: "field: {type: string}\nfield: {type: string}\n"},
		{name: "field not mapping", input: "field: string\n"},
		{name: "missing type", input: "field: {required: true}\n"},
		{name: "unknown type", input: "field: {type: bytes}\n"},
		{name: "unknown key", input: "field: {type: string, description: text}\n"},
		{name: "non boolean required", input: "field: {type: string, required: yes}\n"},
		{name: "format on integer", input: "field: {type: integer, format: email}\n"},
		{name: "unknown format", input: "field: {type: string, format: hostname}\n"},
		{name: "items on string", input: "field: {type: string, items: string}\n"},
		{name: "array missing items", input: "field: {type: array}\n"},
		{name: "nested array", input: "field: {type: array, items: array}\n"},
		{name: "secret array", input: "field: {type: array, items: secret}\n"},
		{name: "secret default", input: "password: {type: secret, default: plaintext}\n"},
		{name: "integer string", input: "field: {type: integer, default: '1'}\n"},
		{name: "noncanonical integer", input: "field: {type: integer, default: 01}\n"},
		{name: "integer overflow", input: "field: {type: integer, default: 9223372036854775808}\n"},
		{name: "non finite number", input: "field: {type: number, default: .nan}\n"},
		{name: "negative duration", input: "field: {type: duration, default: -1s}\n"},
		{name: "relative url", input: "field: {type: url, default: /relative}\n"},
		{name: "invalid email", input: "field: {type: string, format: email, default: not-an-email}\n"},
		{name: "empty enum", input: "field: {type: string, enum: []}\n"},
		{name: "duplicate enum", input: "field: {type: string, enum: [one, one]}\n"},
		{name: "object enum", input: "field: {type: object, enum: [{a: b}]}\n"},
		{name: "default outside enum", input: "field: {type: string, enum: [one, two], default: three}\n"},
		{name: "array item mismatch", input: "field: {type: array, items: integer, default: [1, two]}\n"},
		{name: "object non string key", input: "field: {type: object, default: {1: value}}\n"},
		{name: "unsupported object scalar", input: "field: {type: object, default: {when: 2026-07-15}}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config, err := manifest.ParseConfig([]byte(test.input))
			if !errors.Is(err, manifest.ErrInvalidConfig) {
				t.Fatalf("ParseConfig error = %v, want ErrInvalidConfig", err)
			}
			if len(config.Fields()) != 0 {
				t.Fatalf("invalid config returned fields: %#v", config.Fields())
			}
		})
	}
}

func TestParseConfigRejectsOversizedDocument(t *testing.T) {
	t.Parallel()

	input := strings.Repeat("x", manifest.MaximumDeclarationSize+1)
	if _, err := manifest.ParseConfig([]byte(input)); !errors.Is(err, manifest.ErrInvalidConfig) {
		t.Fatalf("ParseConfig oversized error = %v, want ErrInvalidConfig", err)
	}
}

func TestParseConfigType(t *testing.T) {
	t.Parallel()

	for _, value := range []manifest.ConfigType{
		manifest.ConfigString,
		manifest.ConfigInteger,
		manifest.ConfigNumber,
		manifest.ConfigBoolean,
		manifest.ConfigDuration,
		manifest.ConfigURL,
		manifest.ConfigSecret,
		manifest.ConfigObject,
		manifest.ConfigArray,
	} {
		parsed, err := manifest.ParseConfigType(string(value))
		if err != nil || parsed != value {
			t.Fatalf("ParseConfigType(%q) = %q, %v", value, parsed, err)
		}
	}
	if parsed, err := manifest.ParseConfigType("unknown"); !errors.Is(err, manifest.ErrInvalidConfig) || parsed != "" {
		t.Fatalf("ParseConfigType(unknown) = %q, %v", parsed, err)
	}
}

func FuzzParseConfig(f *testing.F) {
	for _, seed := range []string{"{}\n", validConfig, "field: {type: string}\n", "field: &x {type: string}\ncopy: *x\n", "[]\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		config, err := manifest.ParseConfig([]byte(input))
		if err != nil {
			if !errors.Is(err, manifest.ErrInvalidConfig) {
				t.Fatalf("ParseConfig returned unexpected error: %v", err)
			}
			return
		}
		fields := config.Fields()
		for index := 1; index < len(fields); index++ {
			if fields[index-1].Name() >= fields[index].Name() {
				t.Fatalf("fields are not uniquely sorted: %q then %q", fields[index-1].Name(), fields[index].Name())
			}
		}
	})
}

func jsonStrings(values [][]byte) []string {
	result := make([]string, len(values))
	for index := range values {
		result[index] = string(values[index])
	}
	return result
}
