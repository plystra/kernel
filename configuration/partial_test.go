package configuration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/kernel/configuration"
	"go.yaml.in/yaml/v3"
)

func TestNormalizePartialValidatesOnlyProvidedFields(t *testing.T) {
	t.Parallel()

	schema := configurationSchema(t, `
count: {type: integer, required: true}
labels: {type: object, required: true}
mode: {type: string, enum: [one, two], required: true}
ratio: {type: number}
timeout: {type: duration}
token: {type: secret, required: true}
`)
	partial, err := configuration.NormalizePartial(schema, []byte(`
token: {env: PLYSTRA_PARTIAL_TOKEN}
labels: {z: 2, a: {enabled: true}}
timeout: 60s
ratio: 1.0
`))
	if err != nil || !partial.Valid() {
		t.Fatalf("NormalizePartial = %#v, %v", partial, err)
	}
	if got := partial.Names(); !reflect.DeepEqual(got, []string{"labels", "ratio", "timeout", "token"}) {
		t.Fatalf("Names = %v", got)
	}
	labels, ok := partial.YAML("labels")
	if !ok || string(labels) != "a:\n    enabled: true\nz: 2\n" {
		t.Fatalf("labels YAML = %q, %t", labels, ok)
	}
	labels[0] = 'X'
	if again, _ := partial.YAML("labels"); bytes.Equal(labels, again) {
		t.Fatal("YAML exposed mutable storage")
	}
	for _, name := range partial.Names() {
		digest, ok := partial.Digest(name)
		if !ok || !validTestDigest(digest) {
			t.Fatalf("Digest(%s) = %q, %t", name, digest, ok)
		}
	}
	if _, ok := partial.YAML("missing"); ok {
		t.Fatal("YAML(missing) succeeded")
	}
	if _, ok := partial.Digest("missing"); ok {
		t.Fatal("Digest(missing) succeeded")
	}
}

func TestNormalizePartialUsesSemanticDeterministicDigests(t *testing.T) {
	t.Parallel()

	schema := configurationSchema(t, `
labels: {type: object}
ratio: {type: number}
timeouts: {type: array, items: duration}
token: {type: secret}
`)
	left, err := configuration.NormalizePartial(schema, []byte(`
labels: {b: 2.0, a: true}
ratio: 1
timeouts: [60s, 2m]
token: {env: PLYSTRA_PARTIAL_TOKEN}
`))
	if err != nil {
		t.Fatalf("NormalizePartial(left): %v", err)
	}
	right, err := configuration.NormalizePartial(schema, []byte(`
token: {env: PLYSTRA_PARTIAL_TOKEN}
timeouts: [1m, 120s]
ratio: 1.0
labels: {a: true, b: 2}
`))
	if err != nil {
		t.Fatalf("NormalizePartial(right): %v", err)
	}
	for _, name := range left.Names() {
		leftDigest, _ := left.Digest(name)
		rightDigest, _ := right.Digest(name)
		if leftDigest != rightDigest {
			t.Fatalf("Digest(%s) = %q, %q", name, leftDigest, rightDigest)
		}
	}
	different, err := configuration.NormalizePartial(schema, []byte("token: {env: PLYSTRA_PARTIAL_OTHER}\n"))
	if err != nil {
		t.Fatalf("NormalizePartial(different): %v", err)
	}
	leftDigest, _ := left.Digest("token")
	differentDigest, _ := different.Digest("token")
	if leftDigest == differentDigest {
		t.Fatal("different Secret references have equal digests")
	}
}

func TestNormalizePartialRejectsInvalidProvidedFieldsWithoutLeakingValues(t *testing.T) {
	t.Parallel()

	schema := configurationSchema(t, `
count: {type: integer, required: true}
mode: {type: string, enum: [one, two]}
token: {type: secret}
`)
	tests := []struct {
		name   string
		input  string
		reason error
	}{
		{name: "unknown", input: "private_unknown: hidden-value\n", reason: configuration.ErrUnknownField},
		{name: "type", input: "count: hidden-value\n", reason: configuration.ErrInvalidValue},
		{name: "enum", input: "mode: hidden-value\n", reason: configuration.ErrInvalidValue},
		{name: "secret", input: "token: {env: BAD=HIDDEN}\n", reason: configuration.ErrInvalidValue},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			partial, err := configuration.NormalizePartial(schema, []byte(test.input))
			if partial.Valid() || !errors.Is(err, configuration.ErrInvalidValues) || !errors.Is(err, test.reason) {
				t.Fatalf("NormalizePartial = %#v, %v", partial, err)
			}
			for _, forbidden := range []string{"private_unknown", "hidden-value", "BAD=HIDDEN"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error exposed %q: %v", forbidden, err)
				}
			}
		})
	}
}

func TestPartialValuesRedactAndFailClosed(t *testing.T) {
	t.Parallel()

	partial, err := configuration.NormalizePartial(configurationSchema(t, "token: {type: secret}\n"), []byte("token: {env: PLYSTRA_PARTIAL_PRIVATE}\n"))
	if err != nil {
		t.Fatalf("NormalizePartial: %v", err)
	}
	formatted := []string{
		partial.String(),
		partial.GoString(),
		fmt.Sprintf("%v", partial),
		fmt.Sprintf("%+v", partial),
		fmt.Sprintf("%#v", partial),
		fmt.Sprintf("%s", partial),
		fmt.Sprintf("%q", partial),
		fmt.Sprintf("%x", partial),
	}
	for _, output := range formatted {
		if !strings.Contains(output, "redacted") || strings.Contains(output, "PLYSTRA_PARTIAL_PRIVATE") {
			t.Fatalf("formatting exposed PartialValues: %q", output)
		}
	}
	for _, marshal := range []func() ([]byte, error){
		func() ([]byte, error) { return json.Marshal(partial) },
		func() ([]byte, error) { return partial.MarshalText() },
		func() ([]byte, error) { return yaml.Marshal(partial) },
	} {
		if data, err := marshal(); data != nil || !errors.Is(err, configuration.ErrSecretExposure) {
			t.Fatalf("serialization = %q, %v", data, err)
		}
	}
	var output bytes.Buffer
	slog.New(slog.NewTextHandler(&output, nil)).Info("partial", "values", partial)
	if !strings.Contains(output.String(), "redacted") || strings.Contains(output.String(), "PLYSTRA_PARTIAL_PRIVATE") {
		t.Fatalf("structured log exposed PartialValues: %s", output.String())
	}

	var zero configuration.PartialValues
	if zero.Valid() || zero.Names() != nil {
		t.Fatalf("zero PartialValues = %#v", zero)
	}
	if _, ok := zero.YAML("token"); ok {
		t.Fatal("zero YAML accessor succeeded")
	}
}

func FuzzNormalizePartial(f *testing.F) {
	f.Add("{}\n")
	f.Add("count: 1\nlabels: {owner: runtime}\ntoken: {env: PLYSTRA_PARTIAL_FUZZ}\n")
	f.Add("count: &value 1\nlabels: *value\n")
	schema := configurationSchema(f, `
count: {type: integer, required: true}
labels: {type: object}
token: {type: secret}
`)
	f.Fuzz(func(t *testing.T, input string) {
		partial, err := configuration.NormalizePartial(schema, []byte(input))
		if err != nil {
			if !errors.Is(err, configuration.ErrInvalidValues) {
				t.Fatalf("NormalizePartial returned unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), "PLYSTRA_PARTIAL_FUZZ") {
				t.Fatalf("NormalizePartial error exposed Secret reference: %v", err)
			}
			return
		}
		if !partial.Valid() {
			t.Fatal("NormalizePartial succeeded with invalid PartialValues")
		}
		for _, name := range partial.Names() {
			if digest, ok := partial.Digest(name); !ok || !validTestDigest(digest) {
				t.Fatalf("Digest(%q) = %q, %t", name, digest, ok)
			}
		}
	})
}

func validTestDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
