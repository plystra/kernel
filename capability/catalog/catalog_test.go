package catalog_test

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/capability/catalog"
	"github.com/plystra/kernel/plugin/manifest"
)

func TestDefinitionsAreValidatedAndSorted(t *testing.T) {
	t.Parallel()

	definitions := catalog.Definitions()
	if got := definitionIDs(definitions); !slices.Equal(got, []string{"kernel.health/v1", "kernel.info/v1"}) {
		t.Fatalf("Definitions() = %v", got)
	}
	for index, definition := range definitions {
		if definition.ID().String() == "" {
			t.Fatalf("definition %d has no ID", index)
		}
		if !strings.HasPrefix(definition.ID().Name(), "kernel.") {
			t.Fatalf("definition %s is not intrinsic", definition.ID())
		}
		if index > 0 && definitions[index-1].ID().String() >= definition.ID().String() {
			t.Fatalf("definitions are not uniquely sorted: %q then %q", definitions[index-1].ID(), definition.ID())
		}
		parsed, err := manifest.ParseCapability(definition.Source())
		if err != nil {
			t.Fatalf("ParseCapability(%s): %v", definition.ID(), err)
		}
		if parsed.ID() != definition.ID() {
			t.Fatalf("source ID = %q, want %q", parsed.ID(), definition.ID())
		}
		digest, err := parsed.SchemaDigest()
		if err != nil {
			t.Fatalf("SchemaDigest(%s): %v", definition.ID(), err)
		}
		if digest != definition.SchemaDigest() {
			t.Fatalf("source digest for %s differs from catalog", definition.ID())
		}
	}
}

func TestLookupIntrinsicDefinitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id          string
		description string
		response    []string
		digest      string
	}{
		{id: "kernel.health/v1", description: "Reports intrinsic Kernel liveness.", response: []string{"status"}, digest: "24b3601547ebd37da37341a86699a51b2e8a2d1706662bafed0b4d6d3a1548a9"},
		{id: "kernel.info/v1", description: "Reports non-sensitive Kernel compatibility information.", response: []string{"assembly_api", "kernel_module", "kernel_version"}, digest: "3ec0d8f2bfda17c88d8bf5e724f8612049fb0779074999f3a7c9fc9495d3695b"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.id, func(t *testing.T) {
			t.Parallel()
			id, err := capability.ParseIdentifier(test.id)
			if err != nil {
				t.Fatalf("ParseIdentifier: %v", err)
			}
			definition, ok := catalog.Lookup(id)
			if !ok {
				t.Fatalf("Lookup(%s) failed", test.id)
			}
			if definition.Contract().Description() != test.description {
				t.Fatalf("description = %q", definition.Contract().Description())
			}
			if got := fieldNames(definition.Contract().Request()); len(got) != 0 {
				t.Fatalf("request fields = %v", got)
			}
			if got := fieldNames(definition.Contract().Response()); !reflect.DeepEqual(got, test.response) {
				t.Fatalf("response fields = %v", got)
			}
			if got := definition.Contract().Errors(); len(got) != 0 {
				t.Fatalf("errors = %v", got)
			}
			if got := definition.Contract().Semantics().Kind(); got != manifest.CapabilityKindQuery {
				t.Fatalf("semantics kind = %q, want query", got)
			}
			if got := fmt.Sprintf("%x", definition.SchemaDigest()); got != test.digest {
				t.Fatalf("schema digest = %s", got)
			}
			if source := definition.Source(); len(source) == 0 || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
				t.Fatalf("source is not canonical LF text: %q", source)
			}
		})
	}
}

func TestCatalogAccessorsAreImmutable(t *testing.T) {
	t.Parallel()

	definitions := catalog.Definitions()
	firstID := definitions[0].ID()
	definitions[0] = catalog.Definition{}
	if catalog.Definitions()[0].ID() != firstID {
		t.Fatal("Definitions exposed catalog storage")
	}

	definition, ok := catalog.Lookup(firstID)
	if !ok {
		t.Fatalf("Lookup(%s) failed", firstID)
	}
	source := definition.Source()
	source[0] = 'x'
	if bytes.Equal(source, definition.Source()) {
		t.Fatal("Source exposed catalog storage")
	}
}

func TestLookupRejectsMissingAndZeroIdentifiers(t *testing.T) {
	t.Parallel()

	missing, err := capability.ParseIdentifier("example.missing/v1")
	if err != nil {
		t.Fatalf("ParseIdentifier: %v", err)
	}
	if definition, ok := catalog.Lookup(missing); ok || definition.ID().String() != "" {
		t.Fatalf("Lookup(missing) = %#v, %v", definition, ok)
	}
	if definition, ok := catalog.Lookup(capability.Identifier{}); ok || definition.ID().String() != "" {
		t.Fatalf("Lookup(zero) = %#v, %v", definition, ok)
	}
}

func fieldNames(schema manifest.Schema) []string {
	fields := schema.Fields()
	names := make([]string, len(fields))
	for index := range fields {
		names[index] = fields[index].Name()
	}
	return names
}

func definitionIDs(definitions []catalog.Definition) []string {
	ids := make([]string, len(definitions))
	for index, definition := range definitions {
		ids[index] = definition.ID().String()
	}
	return ids
}
