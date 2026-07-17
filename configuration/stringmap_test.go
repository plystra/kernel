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

func TestExtractStringMapReturnsImmutableSortedPrivateValues(t *testing.T) {
	t.Parallel()

	const source = `
http: {address: ":8080"}
timeouts:
  startup: 2m
  private_value: private-runtime-setting
`
	settings, err := configuration.ExtractStringMap([]byte(source), "timeouts")
	if err != nil || !settings.Valid() {
		t.Fatalf("ExtractStringMap = %#v, %v", settings, err)
	}
	if got := settings.Names(); !reflect.DeepEqual(got, []string{"private_value", "startup"}) {
		t.Fatalf("Names = %v", got)
	}
	if value, exists := settings.Value("startup"); !exists || value != "2m" {
		t.Fatalf("startup = %q, %t", value, exists)
	}
	if value, exists := settings.Value("private_value"); !exists || value != "private-runtime-setting" {
		t.Fatalf("private_value = %q, %t", value, exists)
	}
	names := settings.Names()
	names[0] = "changed"
	if settings.Names()[0] != "private_value" {
		t.Fatal("Names exposed mutable storage")
	}
	if value, exists := settings.Value("missing"); exists || value != "" {
		t.Fatalf("missing = %q, %t", value, exists)
	}
	assertStringMapRedacted(t, settings, []string{"2m", "private-runtime-setting"})
}

func TestExtractStringMapSupportsAbsentAndEmptySections(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"{}\n", "http: {}\n", "timeouts: {}\n"} {
		settings, err := configuration.ExtractStringMap([]byte(source), "timeouts")
		if err != nil || !settings.Valid() || settings.Names() == nil || len(settings.Names()) != 0 {
			t.Fatalf("ExtractStringMap(%q) = %#v, %v", source, settings, err)
		}
	}
	var zero configuration.StringMap
	if zero.Valid() || zero.Names() != nil {
		t.Fatalf("zero StringMap = %#v", zero)
	}
	if value, exists := zero.Value("anything"); exists || value != "" {
		t.Fatalf("zero Value = %q, %t", value, exists)
	}
	assertStringMapRedacted(t, zero, nil)
}

func TestExtractStringMapRejectsUnsafeDocumentsWithoutExposingValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		section string
		reason  error
	}{
		{name: "empty", input: "", section: "timeouts"},
		{name: "multiple documents", input: "{}\n---\n{}\n", section: "timeouts"},
		{name: "root duplicate", input: "timeouts: {}\ntimeouts: {}\n", section: "timeouts"},
		{name: "anchor", input: "timeouts: &private {startup: PRIVATE_RUNTIME_VALUE}\n", section: "timeouts"},
		{name: "section scalar", input: "timeouts: PRIVATE_RUNTIME_VALUE\n", section: "timeouts", reason: configuration.ErrInvalidStringSection},
		{name: "entry non-string", input: "timeouts: {startup: 120}\n", section: "timeouts", reason: configuration.ErrInvalidStringSection},
		{name: "entry duplicate", input: "timeouts: {startup: 2m, startup: PRIVATE_RUNTIME_VALUE}\n", section: "timeouts", reason: configuration.ErrInvalidStringSection},
		{name: "invalid section", input: "timeouts: {}\n", section: "Timeouts", reason: configuration.ErrInvalidStringSection},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			settings, err := configuration.ExtractStringMap([]byte(test.input), test.section)
			if settings.Valid() || !errors.Is(err, configuration.ErrInvalidDocument) || test.reason != nil && !errors.Is(err, test.reason) {
				t.Fatalf("ExtractStringMap = %#v, %v", settings, err)
			}
			if strings.Contains(err.Error(), "PRIVATE_RUNTIME_VALUE") {
				t.Fatalf("error exposed private value: %v", err)
			}
		})
	}
}

func TestExtractStringMapRejectsSizeAndDepthBounds(t *testing.T) {
	t.Parallel()

	oversized := []byte("timeouts: {startup: " + strings.Repeat("x", manifest.MaximumDeclarationSize) + "}\n")
	if settings, err := configuration.ExtractStringMap(oversized, "timeouts"); settings.Valid() || !errors.Is(err, configuration.ErrInvalidDocument) {
		t.Fatalf("oversized ExtractStringMap = %#v, %v", settings, err)
	}
	deep := "other: " + strings.Repeat("{nested: ", 80) + "null" + strings.Repeat("}", 80) + "\ntimeouts: {}\n"
	if settings, err := configuration.ExtractStringMap([]byte(deep), "timeouts"); settings.Valid() || !errors.Is(err, configuration.ErrInvalidDocument) {
		t.Fatalf("deep ExtractStringMap = %#v, %v", settings, err)
	}
}

func TestStringMapIsSafeForConcurrentReaders(t *testing.T) {
	t.Parallel()

	settings, err := configuration.ExtractStringMap([]byte("timeouts: {startup: 2m}\n"), "timeouts")
	if err != nil {
		t.Fatalf("ExtractStringMap: %v", err)
	}
	const readers = 64
	var group sync.WaitGroup
	group.Add(readers)
	for range readers {
		go func() {
			defer group.Done()
			names := settings.Names()
			value, exists := settings.Value("startup")
			if !exists || len(names) != 1 || names[0] != "startup" || value != "2m" {
				t.Errorf("concurrent access = %v, %q, %t", names, value, exists)
				return
			}
			names[0] = "changed"
		}()
	}
	group.Wait()
}

func FuzzExtractStringMap(f *testing.F) {
	f.Add("{}\n", "timeouts")
	f.Add("timeouts: {startup: 2m}\n", "timeouts")
	f.Add("timeouts: &value {startup: *value}\n", "timeouts")
	f.Fuzz(func(t *testing.T, input, section string) {
		settings, err := configuration.ExtractStringMap([]byte(input), section)
		if err != nil {
			if !errors.Is(err, configuration.ErrInvalidDocument) {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), "PRIVATE_RUNTIME_VALUE") {
				t.Fatalf("error exposed private value: %v", err)
			}
			return
		}
		if !settings.Valid() {
			t.Fatal("successful extraction returned invalid StringMap")
		}
	})
}

func assertStringMapRedacted(t testing.TB, settings configuration.StringMap, forbidden []string) {
	t.Helper()
	for _, output := range []string{
		settings.String(),
		settings.GoString(),
		fmt.Sprintf("%v", settings),
		fmt.Sprintf("%+v", settings),
		fmt.Sprintf("%#v", settings),
		fmt.Sprintf("%q", settings),
	} {
		if !strings.Contains(output, "redacted") {
			t.Fatalf("StringMap formatting is not redacted: %q", output)
		}
		for _, value := range forbidden {
			if strings.Contains(output, value) {
				t.Fatalf("StringMap formatting exposed %q: %q", value, output)
			}
		}
	}
	if data, err := json.Marshal(settings); data != nil || !errors.Is(err, configuration.ErrSecretExposure) {
		t.Fatalf("json.Marshal = %q, %v", data, err)
	}
	if data, err := settings.MarshalText(); data != nil || !errors.Is(err, configuration.ErrSecretExposure) {
		t.Fatalf("MarshalText = %q, %v", data, err)
	}
	if data, err := yaml.Marshal(settings); data != nil || !errors.Is(err, configuration.ErrSecretExposure) {
		t.Fatalf("yaml.Marshal = %q, %v", data, err)
	}
	for _, handler := range []func(*bytes.Buffer) slog.Handler{
		func(buffer *bytes.Buffer) slog.Handler { return slog.NewTextHandler(buffer, nil) },
		func(buffer *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(buffer, nil) },
	} {
		var output bytes.Buffer
		slog.New(handler(&output)).Info("configuration", "settings", settings)
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
