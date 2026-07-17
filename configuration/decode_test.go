package configuration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plystra/kernel/configuration"
	"github.com/plystra/kernel/plugin/manifest"
	"go.yaml.in/yaml/v3"
)

func TestDecodeProducesImmutableTypedPluginValues(t *testing.T) {
	const (
		secretName  = "PLYSTRA_CONFIGURATION_TYPED_SECRET"
		secretValue = "typed-secret-value"
	)
	t.Setenv(secretName, secretValue)
	schema := configurationSchema(t, `
active: {type: boolean, required: true}
backoffs: {type: array, items: duration, required: true}
count: {type: integer, required: true}
default_hosts: {type: array, items: url, default: [https://default.example.com/path]}
default_labels: {type: object, default: {source: default, weight: 2}}
default_timeout: {type: duration, default: 5s}
delay: {type: duration, enum: [60s, 2m], required: true}
email: {type: string, format: email, required: true}
empty_names: {type: array, items: string, required: true}
endpoints: {type: array, items: url, required: true}
flags: {type: array, items: boolean, required: true}
labels: {type: object, required: true}
mode: {type: string, enum: [starttls, tls], default: starttls}
names: {type: array, items: string, required: true}
objects: {type: array, items: object, required: true}
optional_note: {type: string}
password: {type: secret, required: true}
ports: {type: array, items: integer, required: true}
ratio: {type: number, required: true}
ratios: {type: array, items: number, required: true}
service_url: {type: url, required: true}
`)
	input := `
active: true
backoffs: [1s, 2.5s]
count: -42
delay: 60s
email: sender@example.com
empty_names: []
endpoints: [https://one.example.com, https://two.example.com/path]
flags: [true, false]
labels:
  nested: {enabled: true, values: [1, 2.5, null]}
  owner: runtime
names: [alpha, beta]
objects: [{name: first}, {name: second, count: 2}]
password: {env: PLYSTRA_CONFIGURATION_TYPED_SECRET}
ports: [443, 8443]
ratio: 1.25
ratios: [1, 2.5]
service_url: https://service.example.com/base
`
	values, err := configuration.Decode(context.Background(), configurationResolver(t, 1024), schema, []byte(input))
	if err != nil || !values.Valid() {
		t.Fatalf("Decode = %#v, %v", values, err)
	}

	active, ok := values.BooleanValue("active")
	assertScalarValue(t, active, ok, true, "active")
	count, ok := values.IntegerValue("count")
	assertScalarValue(t, count, ok, int64(-42), "count")
	ratio, ok := values.NumberValue("ratio")
	assertScalarValue(t, ratio, ok, 1.25, "ratio")
	email, ok := values.StringValue("email")
	assertScalarValue(t, email, ok, "sender@example.com", "email")
	mode, ok := values.StringValue("mode")
	assertScalarValue(t, mode, ok, "starttls", "mode default")
	defaultTimeout, ok := values.DurationValue("default_timeout")
	assertScalarValue(t, defaultTimeout, ok, 5*time.Second, "duration default")
	delay, ok := values.DurationValue("delay")
	assertScalarValue(t, delay, ok, time.Minute, "duration enum")
	serviceURL, ok := values.URLValue("service_url")
	if !ok || serviceURL.String() != "https://service.example.com/base" {
		t.Fatalf("service_url = %#v, %t", serviceURL, ok)
	}
	secret, ok := values.SecretValue("password")
	if !ok || string(secret.Bytes()) != secretValue {
		t.Fatalf("password = %#v, %t", secret, ok)
	}
	if values.Has("optional_note") {
		t.Fatal("absent optional value is present")
	}
	if _, ok := values.StringValue("count"); ok {
		t.Fatal("wrong typed accessor succeeded")
	}

	namesValue, ok := values.Strings("names")
	assertSliceValue(t, namesValue, ok, []string{"alpha", "beta"}, "names")
	emptyNames, ok := values.Strings("empty_names")
	if !ok || emptyNames == nil || len(emptyNames) != 0 {
		t.Fatalf("empty_names = %#v, %t", emptyNames, ok)
	}
	ports, ok := values.Integers("ports")
	assertSliceValue(t, ports, ok, []int64{443, 8443}, "ports")
	ratios, ok := values.Numbers("ratios")
	assertSliceValue(t, ratios, ok, []float64{1, 2.5}, "ratios")
	flags, ok := values.Booleans("flags")
	assertSliceValue(t, flags, ok, []bool{true, false}, "flags")
	backoffs, ok := values.Durations("backoffs")
	assertSliceValue(t, backoffs, ok, []time.Duration{time.Second, 2500 * time.Millisecond}, "backoffs")
	endpoints, ok := values.URLs("endpoints")
	if !ok || len(endpoints) != 2 || endpoints[1].String() != "https://two.example.com/path" {
		t.Fatalf("endpoints = %#v, %t", endpoints, ok)
	}
	defaultHosts, ok := values.URLs("default_hosts")
	if !ok || len(defaultHosts) != 1 || defaultHosts[0].String() != "https://default.example.com/path" {
		t.Fatalf("default_hosts = %#v, %t", defaultHosts, ok)
	}

	labels, ok := values.ObjectValue("labels")
	if !ok || labels["owner"] != "runtime" {
		t.Fatalf("labels = %#v, %t", labels, ok)
	}
	nested := labels["nested"].(map[string]any)
	if _, ok := nested["values"].([]any)[0].(json.Number); !ok {
		t.Fatalf("object number type = %T", nested["values"].([]any)[0])
	}
	labels["owner"] = "mutated"
	nested["enabled"] = false
	again, _ := values.ObjectValue("labels")
	if again["owner"] != "runtime" || again["nested"].(map[string]any)["enabled"] != true {
		t.Fatal("ObjectValue exposed mutable storage")
	}
	defaultLabels, ok := values.ObjectValue("default_labels")
	if !ok || defaultLabels["source"] != "default" || defaultLabels["weight"].(json.Number).String() != "2" {
		t.Fatalf("default_labels = %#v, %t", defaultLabels, ok)
	}
	objects, ok := values.Objects("objects")
	if !ok || len(objects) != 2 || objects[1]["name"] != "second" {
		t.Fatalf("objects = %#v, %t", objects, ok)
	}
	objects[0]["name"] = "mutated"
	objectsAgain, _ := values.Objects("objects")
	if objectsAgain[0]["name"] != "first" {
		t.Fatal("Objects exposed mutable storage")
	}

	names, _ := values.Strings("names")
	names[0] = "mutated"
	namesAgain, _ := values.Strings("names")
	if namesAgain[0] != "alpha" {
		t.Fatal("Strings exposed mutable storage")
	}
	endpoints[0].Host = "mutated.example.com"
	endpointsAgain, _ := values.URLs("endpoints")
	if endpointsAgain[0].Host != "one.example.com" {
		t.Fatal("URLs exposed mutable storage")
	}
	secretBytes := secret.Bytes()
	secretBytes[0] = 'X'
	secretAgain, _ := values.SecretValue("password")
	if string(secretAgain.Bytes()) != secretValue {
		t.Fatal("SecretValue exposed mutable storage")
	}
	assertValuesRedacted(t, values, []string{secretName, secretValue, "sender@example.com", "service.example.com"})
}

func TestDecodeResolvesFileSecretExactly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	want := []byte("file-secret\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	schema := configurationSchema(t, "token: {type: secret, required: true}\n")
	input := fmt.Sprintf("token:\n  file: %q\n", path)
	values, err := configuration.Decode(context.Background(), configurationResolver(t, 128), schema, []byte(input))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	secret, ok := values.SecretValue("token")
	if !ok || !reflect.DeepEqual(secret.Bytes(), want) {
		t.Fatalf("token = %#v, %t", secret, ok)
	}
	if strings.Contains(fmt.Sprintf("%#v", values), path) {
		t.Fatal("formatted Values exposed file reference")
	}
}

func TestValidateChecksValuesWithoutResolvingSecretSources(t *testing.T) {
	const missingEnvironment = "PLYSTRA_CONFIGURATION_VALIDATE_MISSING"
	_ = os.Unsetenv(missingEnvironment)
	t.Cleanup(func() { _ = os.Unsetenv(missingEnvironment) })
	missingFile := filepath.Join(t.TempDir(), "missing-secret")
	schema := configurationSchema(t, `
count: {type: integer, required: true}
environment_token: {type: secret, required: true}
file_token: {type: secret, required: true}
mode: {type: string, enum: [one, two], default: one}
`)
	input := fmt.Sprintf("count: 2\nenvironment_token: {env: %s}\nfile_token: {file: %q}\n", missingEnvironment, missingFile)
	if err := configuration.Validate(schema, []byte(input)); err != nil {
		t.Fatalf("Validate unresolved values: %v", err)
	}
	for _, invalid := range []string{
		fmt.Sprintf("count: wrong\nenvironment_token: {env: %s}\nfile_token: {file: %q}\n", missingEnvironment, missingFile),
		fmt.Sprintf("count: 2\nenvironment_token: {env: BAD=TARGET}\nfile_token: {file: %q}\n", missingFile),
	} {
		if err := configuration.Validate(schema, []byte(invalid)); !errors.Is(err, configuration.ErrInvalidValues) || !errors.Is(err, configuration.ErrInvalidValue) {
			t.Fatalf("Validate invalid values = %v", err)
		}
	}
}

func TestDecodeRejectsInvalidValuesWithSafeTypedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema string
		input  string
		reason error
	}{
		{name: "empty document", schema: "{}\n", input: "", reason: configuration.ErrInvalidValue},
		{name: "non mapping", schema: "{}\n", input: "[]\n", reason: configuration.ErrInvalidValue},
		{name: "multiple documents", schema: "{}\n", input: "{}\n---\n{}\n", reason: configuration.ErrInvalidValue},
		{name: "anchor", schema: "field: {type: string}\n", input: "field: &value hidden\n", reason: configuration.ErrInvalidValue},
		{name: "duplicate", schema: "field: {type: string}\n", input: "field: one\nfield: two\n", reason: configuration.ErrInvalidValue},
		{name: "non string key", schema: "{}\n", input: "1: value\n", reason: configuration.ErrUnknownField},
		{name: "unknown field", schema: "{}\n", input: "highly_sensitive_field: hidden-value\n", reason: configuration.ErrUnknownField},
		{name: "missing required", schema: "required_field: {type: string, required: true}\n", input: "{}\n", reason: configuration.ErrMissingField},
		{name: "string type", schema: "field: {type: string}\n", input: "field: 1\n", reason: configuration.ErrInvalidValue},
		{name: "email format", schema: "field: {type: string, format: email}\n", input: "field: not-an-email\n", reason: configuration.ErrInvalidValue},
		{name: "integer spelling", schema: "field: {type: integer}\n", input: "field: 01\n", reason: configuration.ErrInvalidValue},
		{name: "integer overflow", schema: "field: {type: integer}\n", input: "field: 9223372036854775808\n", reason: configuration.ErrInvalidValue},
		{name: "number non finite", schema: "field: {type: number}\n", input: "field: .nan\n", reason: configuration.ErrInvalidValue},
		{name: "boolean string", schema: "field: {type: boolean}\n", input: "field: 'true'\n", reason: configuration.ErrInvalidValue},
		{name: "negative duration", schema: "field: {type: duration}\n", input: "field: -1s\n", reason: configuration.ErrInvalidValue},
		{name: "relative URL", schema: "field: {type: url}\n", input: "field: /relative\n", reason: configuration.ErrInvalidValue},
		{name: "object type", schema: "field: {type: object}\n", input: "field: []\n", reason: configuration.ErrInvalidValue},
		{name: "object scalar tag", schema: "field: {type: object}\n", input: "field: {created: 2026-07-18}\n", reason: configuration.ErrInvalidValue},
		{name: "array type", schema: "field: {type: array, items: string}\n", input: "field: value\n", reason: configuration.ErrInvalidValue},
		{name: "array item", schema: "field: {type: array, items: integer}\n", input: "field: [1, wrong]\n", reason: configuration.ErrInvalidValue},
		{name: "enum", schema: "field: {type: string, enum: [one, two]}\n", input: "field: three\n", reason: configuration.ErrInvalidValue},
		{name: "Secret plaintext", schema: "field: {type: secret}\n", input: "field: plaintext-secret\n", reason: configuration.ErrInvalidValue},
		{name: "Secret two sources", schema: "field: {type: secret}\n", input: "field: {env: VALID_ENV, file: /secret}\n", reason: configuration.ErrInvalidValue},
		{name: "Secret unknown source", schema: "field: {type: secret}\n", input: "field: {vault: hidden-target}\n", reason: configuration.ErrInvalidValue},
		{name: "Secret invalid environment", schema: "field: {type: secret}\n", input: "field: {env: BAD=TARGET}\n", reason: configuration.ErrInvalidValue},
		{name: "Secret relative file", schema: "field: {type: secret}\n", input: "field: {file: relative/target}\n", reason: configuration.ErrInvalidValue},
		{name: "null is not absent", schema: "field: {type: string}\n", input: "field: null\n", reason: configuration.ErrInvalidValue},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values, err := configuration.Decode(context.Background(), configurationResolver(t, 64), configurationSchema(t, test.schema), []byte(test.input))
			if values.Valid() || !errors.Is(err, configuration.ErrInvalidValues) || !errors.Is(err, test.reason) {
				t.Fatalf("Decode = %#v, %v", values, err)
			}
			for _, forbidden := range []string{"hidden-value", "highly_sensitive_field", "plaintext-secret", "hidden-target", "BAD=TARGET", "relative/target"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error exposed %q: %v", forbidden, err)
				}
			}
		})
	}
}

func TestDecodeRejectsOversizedDocument(t *testing.T) {
	t.Parallel()

	input := []byte("field: " + strings.Repeat("x", manifest.MaximumDeclarationSize) + "\n")
	values, err := configuration.Decode(context.Background(), configurationResolver(t, 64), configurationSchema(t, "field: {type: string}\n"), input)
	if values.Valid() || !errors.Is(err, configuration.ErrInvalidValues) || !errors.Is(err, configuration.ErrInvalidValue) {
		t.Fatalf("Decode oversized = %#v, %v", values, err)
	}
}

func TestDecodeRejectsExcessiveValueDepth(t *testing.T) {
	t.Parallel()

	input := "field: " + strings.Repeat("{nested: ", 80) + "null" + strings.Repeat("}", 80) + "\n"
	values, err := configuration.Decode(context.Background(), configurationResolver(t, 64), configurationSchema(t, "field: {type: object}\n"), []byte(input))
	if values.Valid() || !errors.Is(err, configuration.ErrInvalidValues) || !errors.Is(err, configuration.ErrInvalidValue) {
		t.Fatalf("Decode deep values = %#v, %v", values, err)
	}
}

func TestDecodePreservesSafeSecretResolutionFailures(t *testing.T) {
	const (
		missing = "PLYSTRA_CONFIGURATION_DECODE_MISSING"
		large   = "PLYSTRA_CONFIGURATION_DECODE_LARGE"
	)
	_ = os.Unsetenv(missing)
	t.Cleanup(func() { _ = os.Unsetenv(missing) })
	t.Setenv(large, "oversized-secret-value")
	schema := configurationSchema(t, "token: {type: secret, required: true}\n")
	for _, test := range []struct {
		name   string
		target string
		reason error
	}{
		{name: "missing", target: missing, reason: configuration.ErrSecretUnavailable},
		{name: "large", target: large, reason: configuration.ErrSecretTooLarge},
	} {
		values, err := configuration.Decode(context.Background(), configurationResolver(t, 8), schema, []byte("token: {env: "+test.target+"}\n"))
		if values.Valid() || !errors.Is(err, configuration.ErrInvalidValues) || !errors.Is(err, configuration.ErrResolve) || !errors.Is(err, test.reason) {
			t.Fatalf("%s Decode = %#v, %v", test.name, values, err)
		}
		if strings.Contains(err.Error(), test.target) || strings.Contains(err.Error(), "oversized-secret-value") {
			t.Fatalf("%s error exposed Secret input: %v", test.name, err)
		}
	}
}

func TestDecodeClearsEarlierSecretsWhenLaterResolutionFails(t *testing.T) {
	const (
		availableName  = "PLYSTRA_CONFIGURATION_DECODE_AVAILABLE"
		availableValue = "available-secret-value"
		missingName    = "PLYSTRA_CONFIGURATION_DECODE_LATER_MISSING"
	)
	t.Setenv(availableName, availableValue)
	_ = os.Unsetenv(missingName)
	t.Cleanup(func() { _ = os.Unsetenv(missingName) })
	schema := configurationSchema(t, `
a_available: {type: secret, required: true}
z_missing: {type: secret, required: true}
`)
	input := "a_available: {env: " + availableName + "}\nz_missing: {env: " + missingName + "}\n"
	values, err := configuration.Decode(context.Background(), configurationResolver(t, 128), schema, []byte(input))
	if values.Valid() || !errors.Is(err, configuration.ErrResolve) || !errors.Is(err, configuration.ErrSecretUnavailable) {
		t.Fatalf("Decode = %#v, %v", values, err)
	}
	for _, forbidden := range []string{availableName, availableValue, missingName} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error exposed %q: %v", forbidden, err)
		}
	}
}

func TestDecodeDiscardsPendingReferencesWhenAnotherFieldIsInvalid(t *testing.T) {
	const target = "PLYSTRA_CONFIGURATION_DECODE_PENDING"
	t.Setenv(target, "pending-secret")
	schema := configurationSchema(t, `
a_token: {type: secret, required: true}
z_count: {type: integer, required: true}
`)
	values, err := configuration.Decode(context.Background(), configurationResolver(t, 128), schema, []byte("a_token: {env: "+target+"}\nz_count: wrong\n"))
	if values.Valid() || !errors.Is(err, configuration.ErrInvalidValue) || strings.Contains(err.Error(), target) || strings.Contains(err.Error(), "pending-secret") {
		t.Fatalf("Decode = %#v, %v", values, err)
	}
}

func TestDecodeValidatesContextAndResolver(t *testing.T) {
	t.Parallel()

	schema := configurationSchema(t, "{}\n")
	resolver := configurationResolver(t, 64)
	var nilContext context.Context
	if values, err := configuration.Decode(nilContext, resolver, schema, []byte("{}\n")); values.Valid() || !errors.Is(err, configuration.ErrInvalidContext) {
		t.Fatalf("nil context Decode = %#v, %v", values, err)
	}
	if values, err := configuration.Decode(context.Background(), nil, schema, []byte("{}\n")); values.Valid() || !errors.Is(err, configuration.ErrInvalidResolver) {
		t.Fatalf("nil resolver Decode = %#v, %v", values, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if values, err := configuration.Decode(cancelled, resolver, schema, []byte("{}\n")); values.Valid() || !errors.Is(err, configuration.ErrInvalidValues) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Decode = %#v, %v", values, err)
	}
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if values, err := configuration.Decode(deadline, resolver, schema, []byte("{}\n")); values.Valid() || !errors.Is(err, configuration.ErrInvalidValues) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline Decode = %#v, %v", values, err)
	}
	values, err := configuration.Decode(context.Background(), resolver, schema, []byte("{}\n"))
	if err != nil || !values.Valid() || values.Has("anything") {
		t.Fatalf("empty Decode = %#v, %v", values, err)
	}
}

func TestDecodedValuesAreSafeForConcurrentReaders(t *testing.T) {
	const name = "PLYSTRA_CONFIGURATION_DECODE_CONCURRENT"
	t.Setenv(name, "concurrent-secret")
	values, err := configuration.Decode(context.Background(), configurationResolver(t, 128), configurationSchema(t, `
labels: {type: object, required: true}
names: {type: array, items: string, required: true}
token: {type: secret, required: true}
`), []byte("labels: {owner: runtime}\nnames: [one, two]\ntoken: {env: "+name+"}\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	const readers = 64
	var group sync.WaitGroup
	group.Add(readers)
	for range readers {
		go func() {
			defer group.Done()
			labels, labelsOK := values.ObjectValue("labels")
			names, namesOK := values.Strings("names")
			secret, secretOK := values.SecretValue("token")
			if !labelsOK || !namesOK || !secretOK || labels["owner"] != "runtime" || names[0] != "one" || string(secret.Bytes()) != "concurrent-secret" {
				t.Errorf("concurrent access = %#v, %#v, %#v", labels, names, secret)
				return
			}
			labels["owner"] = "changed"
			names[0] = "changed"
			bytes := secret.Bytes()
			bytes[0] = 'X'
		}()
	}
	group.Wait()
}

func TestZeroValuesFailClosed(t *testing.T) {
	t.Parallel()

	var values configuration.Values
	if values.Valid() || values.Has("field") {
		t.Fatalf("zero Values = %#v", values)
	}
	if _, ok := values.StringValue("field"); ok {
		t.Fatal("zero Values accessor succeeded")
	}
	assertValuesRedacted(t, values, nil)
}

func FuzzDecodePluginValues(f *testing.F) {
	f.Add("{}\n")
	f.Add("name: value\ncount: 1\ntoken: {env: PLYSTRA_CONFIGURATION_FUZZ_SECRET}\n")
	f.Add("name: &value hidden\ncount: *value\n")
	f.Fuzz(func(t *testing.T, input string) {
		t.Setenv("PLYSTRA_CONFIGURATION_FUZZ_SECRET", "fuzz-secret")
		schema := configurationSchema(t, `
count: {type: integer}
name: {type: string}
token: {type: secret}
`)
		validationErr := configuration.Validate(schema, []byte(input))
		if validationErr != nil && !errors.Is(validationErr, configuration.ErrInvalidValues) {
			t.Fatalf("Validate returned unexpected error: %v", validationErr)
		}
		values, err := configuration.Decode(context.Background(), configurationResolver(t, 64), schema, []byte(input))
		if err != nil {
			if !errors.Is(err, configuration.ErrInvalidValues) {
				t.Fatalf("Decode returned unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), "fuzz-secret") || strings.Contains(err.Error(), "PLYSTRA_CONFIGURATION_FUZZ_SECRET") {
				t.Fatalf("Decode error exposed Secret input: %v", err)
			}
			return
		}
		if !values.Valid() {
			t.Fatal("Decode succeeded with invalid Values")
		}
		if validationErr != nil {
			t.Fatalf("Decode accepted values rejected by Validate: %v", validationErr)
		}
	})
}

func configurationSchema(t testing.TB, source string) manifest.Config {
	t.Helper()
	schema, err := manifest.ParseConfig([]byte(source))
	if err != nil {
		t.Fatalf("ParseConfig: %v\n%s", err, source)
	}
	return schema
}

func assertScalarValue[T comparable](t testing.TB, got T, ok bool, want T, name string) {
	t.Helper()
	if !ok || got != want {
		t.Fatalf("%s = %#v, %t; want %#v", name, got, ok, want)
	}
}

func assertSliceValue[T comparable](t testing.TB, got []T, ok bool, want []T, name string) {
	t.Helper()
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, %t; want %#v", name, got, ok, want)
	}
}

func assertValuesRedacted(t testing.TB, values configuration.Values, forbidden []string) {
	t.Helper()
	formatted := []string{
		values.String(),
		values.GoString(),
		fmt.Sprintf("%v", values),
		fmt.Sprintf("%+v", values),
		fmt.Sprintf("%#v", values),
		fmt.Sprintf("%s", values),
		fmt.Sprintf("%q", values),
		fmt.Sprintf("%x", values),
	}
	for _, output := range formatted {
		if !strings.Contains(output, "redacted") {
			t.Fatalf("Values formatting is not redacted: %q", output)
		}
		for _, value := range forbidden {
			if strings.Contains(output, value) {
				t.Fatalf("Values formatting exposed %q as %q", value, output)
			}
		}
	}
	if data, err := json.Marshal(values); !errors.Is(err, configuration.ErrSecretExposure) || data != nil {
		t.Fatalf("Marshal(Values) = %q, %v", data, err)
	}
	if data, err := values.MarshalText(); !errors.Is(err, configuration.ErrSecretExposure) || data != nil {
		t.Fatalf("MarshalText(Values) = %q, %v", data, err)
	}
	if data, err := yaml.Marshal(values); !errors.Is(err, configuration.ErrSecretExposure) || data != nil {
		t.Fatalf("YAML Values = %q, %v", data, err)
	}
	for _, handler := range []func(*bytes.Buffer) slog.Handler{
		func(buffer *bytes.Buffer) slog.Handler { return slog.NewTextHandler(buffer, nil) },
		func(buffer *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(buffer, nil) },
	} {
		var output bytes.Buffer
		slog.New(handler(&output)).Info("configuration", "values", values)
		if !strings.Contains(output.String(), "redacted") {
			t.Fatalf("structured log is not redacted: %s", output.String())
		}
		for _, value := range forbidden {
			if strings.Contains(output.String(), value) {
				t.Fatalf("structured log exposed %q: %s", value, output.String())
			}
		}
	}
}
