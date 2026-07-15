package plugin_test

import (
	"errors"
	"testing"

	"github.com/plystra/kernel/plugin"
)

func TestParseID(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"acme.email.smtp",
		"plystra.authz.rbac.default",
		"example.document-processing.extractor2",
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			id, err := plugin.ParseID(value)
			if err != nil {
				t.Fatalf("ParseID(%q): %v", value, err)
			}
			if id.String() != value {
				t.Fatalf("String() = %q, want %q", id.String(), value)
			}
		})
	}
}

func TestParseIDRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"",
		"plystra",
		"Plystra.plugin",
		"plystra_plugin",
		"plystra..plugin",
		"plystra.-plugin",
		"plystra.plugin-",
		"plystra.plugin--name",
		" plystra.plugin",
		"plystra.plugin ",
		"普莱斯特拉.plugin",
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			id, err := plugin.ParseID(value)
			if !errors.Is(err, plugin.ErrInvalidID) {
				t.Fatalf("ParseID(%q) error = %v, want ErrInvalidID", value, err)
			}
			if id.String() != "" {
				t.Fatalf("invalid ID String() = %q, want empty", id.String())
			}
		})
	}
}

func TestZeroIDHasEmptyString(t *testing.T) {
	t.Parallel()

	var id plugin.ID
	if id.String() != "" {
		t.Fatalf("zero ID String() = %q, want empty", id.String())
	}
}

func FuzzParseID(f *testing.F) {
	for _, seed := range []string{"acme.email.smtp", "bad", "普莱斯特拉.plugin"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		id, err := plugin.ParseID(value)
		if err != nil {
			if !errors.Is(err, plugin.ErrInvalidID) {
				t.Fatalf("ParseID(%q) returned unexpected error: %v", value, err)
			}
			return
		}
		if id.String() != value {
			t.Fatalf("round trip = %q, want %q", id.String(), value)
		}
		reparsed, err := plugin.ParseID(id.String())
		if err != nil || reparsed != id {
			t.Fatalf("reparse = %#v, %v; want %#v", reparsed, err, id)
		}
	})
}
