package configuration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/plystra/kernel/configuration"
	"github.com/plystra/kernel/plugin/manifest"
	"go.yaml.in/yaml/v3"
)

func TestExtractObjectMapReturnsImmutableSortedPrivateObjects(t *testing.T) {
	t.Parallel()

	const source = `
http: {address: ":8080"}
config:
  zeta.storage:
    endpoint: https://private.storage.example.com
  acme.email.smtp:
    host: private.smtp.example.com
    password: {env: PLYSTRA_OBJECT_MAP_PRIVATE_SECRET}
`
	objects, err := configuration.ExtractObjectMap([]byte(source), "config")
	if err != nil || !objects.Valid() {
		t.Fatalf("ExtractObjectMap = %#v, %v", objects, err)
	}
	if got := objects.Names(); !reflect.DeepEqual(got, []string{"acme.email.smtp", "zeta.storage"}) {
		t.Fatalf("Names = %v", got)
	}
	smtp, exists := objects.YAML("acme.email.smtp")
	if !exists || !bytes.Contains(smtp, []byte("private.smtp.example.com")) || !bytes.Contains(smtp, []byte("PLYSTRA_OBJECT_MAP_PRIVATE_SECRET")) {
		t.Fatalf("smtp YAML = %q, %t", smtp, exists)
	}
	smtp[0] = 'X'
	if again, _ := objects.YAML("acme.email.smtp"); bytes.Equal(smtp, again) {
		t.Fatal("YAML exposed mutable storage")
	}
	names := objects.Names()
	names[0] = "changed"
	if objects.Names()[0] != "acme.email.smtp" {
		t.Fatal("Names exposed mutable storage")
	}
	if data, exists := objects.YAML("missing.plugin"); exists || data != nil {
		t.Fatalf("missing YAML = %q, %t", data, exists)
	}
	assertObjectMapRedacted(t, objects, []string{"private.smtp.example.com", "PLYSTRA_OBJECT_MAP_PRIVATE_SECRET"})
}

func TestExtractObjectMapSupportsAbsentAndEmptySections(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"{}\n", "http: {}\n", "config: {}\n"} {
		objects, err := configuration.ExtractObjectMap([]byte(source), "config")
		if err != nil || !objects.Valid() || objects.Names() == nil || len(objects.Names()) != 0 {
			t.Fatalf("ExtractObjectMap(%q) = %#v, %v", source, objects, err)
		}
	}
	var zero configuration.ObjectMap
	if zero.Valid() || zero.Names() != nil {
		t.Fatalf("zero ObjectMap = %#v", zero)
	}
	if data, exists := zero.YAML("anything"); exists || data != nil {
		t.Fatalf("zero YAML = %q, %t", data, exists)
	}
	assertObjectMapRedacted(t, zero, nil)
}

func TestExtractObjectMapRejectsUnsafeDocumentsWithoutExposingValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		section string
		reason  error
	}{
		{name: "empty", input: "", section: "config"},
		{name: "non mapping document", input: "[]\n", section: "config"},
		{name: "multiple documents", input: "{}\n---\n{}\n", section: "config"},
		{name: "root duplicate", input: "config: {}\nconfig: {}\n", section: "config"},
		{name: "root non-string key", input: "1: {}\n", section: "config"},
		{name: "anchor", input: "config: &private {acme.plugin: {token: SECRET_PRIVATE_VALUE}}\n", section: "config"},
		{name: "section scalar", input: "config: SECRET_PRIVATE_VALUE\n", section: "config", reason: configuration.ErrInvalidObjectSection},
		{name: "entry scalar", input: "config: {acme.plugin: SECRET_PRIVATE_VALUE}\n", section: "config", reason: configuration.ErrInvalidObjectSection},
		{name: "entry duplicate", input: "config:\n  acme.plugin: {}\n  acme.plugin: {token: SECRET_PRIVATE_VALUE}\n", section: "config", reason: configuration.ErrInvalidObjectSection},
		{name: "entry non-string key", input: "config:\n  ? [acme, plugin]\n  : {token: SECRET_PRIVATE_VALUE}\n", section: "config", reason: configuration.ErrInvalidObjectSection},
		{name: "invalid section", input: "config: {}\n", section: "Config", reason: configuration.ErrInvalidObjectSection},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			objects, err := configuration.ExtractObjectMap([]byte(test.input), test.section)
			if objects.Valid() || !errors.Is(err, configuration.ErrInvalidDocument) || test.reason != nil && !errors.Is(err, test.reason) {
				t.Fatalf("ExtractObjectMap = %#v, %v", objects, err)
			}
			if strings.Contains(err.Error(), "SECRET_PRIVATE_VALUE") {
				t.Fatalf("error exposed private value: %v", err)
			}
		})
	}
}

func TestExtractObjectMapRejectsSizeAndDepthBounds(t *testing.T) {
	t.Parallel()

	oversized := []byte("config: {acme.plugin: {field: " + strings.Repeat("x", manifest.MaximumDeclarationSize) + "}}\n")
	if objects, err := configuration.ExtractObjectMap(oversized, "config"); objects.Valid() || !errors.Is(err, configuration.ErrInvalidDocument) {
		t.Fatalf("oversized ExtractObjectMap = %#v, %v", objects, err)
	}
	deep := "config: {acme.plugin: " + strings.Repeat("{nested: ", 80) + "null" + strings.Repeat("}", 80) + "}\n"
	if objects, err := configuration.ExtractObjectMap([]byte(deep), "config"); objects.Valid() || !errors.Is(err, configuration.ErrInvalidDocument) {
		t.Fatalf("deep ExtractObjectMap = %#v, %v", objects, err)
	}
}

func TestObjectMapIsSafeForConcurrentReaders(t *testing.T) {
	t.Parallel()

	objects, err := configuration.ExtractObjectMap([]byte("config: {acme.plugin: {host: private.example.com}}\n"), "config")
	if err != nil {
		t.Fatalf("ExtractObjectMap: %v", err)
	}
	const readers = 64
	var group sync.WaitGroup
	group.Add(readers)
	for range readers {
		go func() {
			defer group.Done()
			names := objects.Names()
			data, exists := objects.YAML("acme.plugin")
			if !exists || len(names) != 1 || names[0] != "acme.plugin" || !bytes.Contains(data, []byte("private.example.com")) {
				t.Errorf("concurrent access = %v, %q, %t", names, data, exists)
				return
			}
			names[0] = "changed"
			data[0] = 'X'
		}()
	}
	group.Wait()
}

func FuzzExtractObjectMap(f *testing.F) {
	f.Add("{}\n", "config")
	f.Add("config: {acme.plugin: {token: {env: PRIVATE_TARGET}}}\n", "config")
	f.Add("config: &value {acme.plugin: *value}\n", "config")
	f.Fuzz(func(t *testing.T, input, section string) {
		objects, err := configuration.ExtractObjectMap([]byte(input), section)
		if err != nil {
			if !errors.Is(err, configuration.ErrInvalidDocument) {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, forbidden := range []string{"PRIVATE_TARGET", "private-secret-value"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error exposed %q: %v", forbidden, err)
				}
			}
			return
		}
		if !objects.Valid() {
			t.Fatal("successful extraction returned invalid ObjectMap")
		}
	})
}

func assertObjectMapRedacted(t testing.TB, objects configuration.ObjectMap, forbidden []string) {
	t.Helper()
	for _, output := range []string{
		objects.String(),
		objects.GoString(),
		fmt.Sprintf("%v", objects),
		fmt.Sprintf("%+v", objects),
		fmt.Sprintf("%#v", objects),
		fmt.Sprintf("%q", objects),
	} {
		if !strings.Contains(output, "redacted") {
			t.Fatalf("ObjectMap formatting is not redacted: %q", output)
		}
		for _, value := range forbidden {
			if strings.Contains(output, value) {
				t.Fatalf("ObjectMap formatting exposed %q: %q", value, output)
			}
		}
	}
	if data, err := json.Marshal(objects); data != nil || !errors.Is(err, configuration.ErrSecretExposure) {
		t.Fatalf("json.Marshal = %q, %v", data, err)
	}
	if data, err := objects.MarshalText(); data != nil || !errors.Is(err, configuration.ErrSecretExposure) {
		t.Fatalf("MarshalText = %q, %v", data, err)
	}
	if data, err := yaml.Marshal(objects); data != nil || !errors.Is(err, configuration.ErrSecretExposure) {
		t.Fatalf("yaml.Marshal = %q, %v", data, err)
	}
	for _, handler := range []func(*bytes.Buffer) slog.Handler{
		func(buffer *bytes.Buffer) slog.Handler { return slog.NewTextHandler(buffer, nil) },
		func(buffer *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(buffer, nil) },
	} {
		var output bytes.Buffer
		slog.New(handler(&output)).Info("configuration", "objects", objects)
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
