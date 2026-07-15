package catalog_test

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/capability/catalog"
	"github.com/plystra/kernel/plugin/manifest"
)

func TestDefinitionsAreValidatedAndSorted(t *testing.T) {
	t.Parallel()

	definitions := catalog.Definitions()
	if len(definitions) == 0 {
		t.Fatal("Definitions() is empty")
	}
	for index, definition := range definitions {
		if definition.ID().String() == "" {
			t.Fatalf("definition %d has no ID", index)
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

func TestLookupEmailSendV1(t *testing.T) {
	t.Parallel()

	id, err := capability.ParseIdentifier("email.send/v1")
	if err != nil {
		t.Fatalf("ParseIdentifier: %v", err)
	}
	definition, ok := catalog.Lookup(id)
	if !ok {
		t.Fatal("Lookup(email.send/v1) failed")
	}
	if definition.Contract().Description() != "Sends an email message." {
		t.Fatalf("description = %q", definition.Contract().Description())
	}
	if got := fieldNames(definition.Contract().Request()); !reflect.DeepEqual(got, []string{"html", "subject", "text", "to"}) {
		t.Fatalf("request fields = %v", got)
	}
	if got := definition.Contract().Errors(); !reflect.DeepEqual(got, []string{"authentication_failed", "invalid_recipient", "temporarily_unavailable"}) {
		t.Fatalf("errors = %v", got)
	}
	if got := fmt.Sprintf("%x", definition.SchemaDigest()); got != "72c29e8589b3491986a5076b375844eea3f088b610ec9c8c40416f09b768c18f" {
		t.Fatalf("schema digest = %s", got)
	}
	if source := definition.Source(); len(source) == 0 || source[len(source)-1] != '\n' || bytes.Contains(source, []byte{'\r'}) {
		t.Fatalf("source is not canonical LF text: %q", source)
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
