package manifest_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/plugin/manifest"
)

const validPlugin = `id: acme.email.smtp
provides:
  - storage.object.put/v2
  - email.send/v1
requires:
  - secret.read/v1
  - audit.write/v1
config:
  host:
    type: string
    required: true
  password:
    type: secret
    required: true
`

func TestParsePlugin(t *testing.T) {
	t.Parallel()

	plugin, err := manifest.ParsePlugin([]byte(validPlugin))
	if err != nil {
		t.Fatalf("ParsePlugin: %v", err)
	}
	if got := plugin.ID().String(); got != "acme.email.smtp" {
		t.Fatalf("ID() = %q, want acme.email.smtp", got)
	}
	if got := identifierStrings(plugin.Provides()); !reflect.DeepEqual(got, []string{"email.send/v1", "storage.object.put/v2"}) {
		t.Fatalf("Provides() = %v", got)
	}
	if got := identifierStrings(plugin.Requires()); !reflect.DeepEqual(got, []string{"audit.write/v1", "secret.read/v1"}) {
		t.Fatalf("Requires() = %v", got)
	}
	fields := plugin.Config().Fields()
	if len(fields) != 2 || fields[0].Name() != "host" || fields[1].Name() != "password" {
		t.Fatalf("Config().Fields() = %#v", fields)
	}
	if fields[1].Type() != manifest.ConfigSecret || !fields[1].Required() {
		t.Fatalf("password field = %#v", fields[1])
	}

	provided := plugin.Provides()
	provided[0] = capability.Identifier{}
	required := plugin.Requires()
	required[0] = capability.Identifier{}
	fields[0] = manifest.ConfigField{}
	if plugin.Provides()[0].String() != "email.send/v1" || plugin.Requires()[0].String() != "audit.write/v1" || plugin.Config().Fields()[0].Name() != "host" {
		t.Fatal("Plugin accessors exposed mutable storage")
	}
}

func TestParsePluginAllowsStatelessPluginWithoutCapabilities(t *testing.T) {
	t.Parallel()

	plugin, err := manifest.ParsePlugin([]byte("id: acme.account.profile\n"))
	if err != nil {
		t.Fatalf("ParsePlugin: %v", err)
	}
	if plugin.ID().String() != "acme.account.profile" || len(plugin.Provides()) != 0 || len(plugin.Requires()) != 0 || len(plugin.Config().Fields()) != 0 {
		t.Fatalf("minimal plugin = %#v", plugin)
	}
}

func TestParsePluginRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "not mapping", input: "[]\n"},
		{name: "multiple documents", input: "id: acme.one\n---\nid: acme.two\n"},
		{name: "anchor", input: "id: &identity acme.one\n"},
		{name: "alias", input: "id: &identity acme.one\nprovides: [*identity]\n"},
		{name: "non string key", input: "1: value\n"},
		{name: "unknown key", input: "id: acme.one\nversion: 1.0.0\n"},
		{name: "duplicate key", input: "id: acme.one\nid: acme.two\n"},
		{name: "missing id", input: "provides: []\n"},
		{name: "non string id", input: "id: 1\n"},
		{name: "invalid id", input: "id: Acme.One\n"},
		{name: "provides not sequence", input: "id: acme.one\nprovides: email.send/v1\n"},
		{name: "non string provides item", input: "id: acme.one\nprovides: [1]\n"},
		{name: "invalid provided capability", input: "id: acme.one\nprovides: [email.send]\n"},
		{name: "duplicate provided capability", input: "id: acme.one\nprovides: [email.send/v1, email.send/v1]\n"},
		{name: "requires not sequence", input: "id: acme.one\nrequires: audit.write/v1\n"},
		{name: "invalid required capability", input: "id: acme.one\nrequires: [Audit.write/v1]\n"},
		{name: "duplicate required capability", input: "id: acme.one\nrequires: [audit.write/v1, audit.write/v1]\n"},
		{name: "config not mapping", input: "id: acme.one\nconfig: []\n"},
		{name: "invalid config field", input: "id: acme.one\nconfig:\n  BadName: {type: string}\n"},
		{name: "secret default", input: "id: acme.one\nconfig:\n  password: {type: secret, default: plaintext}\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plugin, err := manifest.ParsePlugin([]byte(test.input))
			if !errors.Is(err, manifest.ErrInvalidPlugin) {
				t.Fatalf("ParsePlugin error = %v, want ErrInvalidPlugin", err)
			}
			if plugin.ID().String() != "" || len(plugin.Provides()) != 0 || len(plugin.Requires()) != 0 || len(plugin.Config().Fields()) != 0 {
				t.Fatalf("invalid declaration returned data: %#v", plugin)
			}
		})
	}
}

func TestParsePluginRejectsOversizedDocument(t *testing.T) {
	t.Parallel()

	input := strings.Repeat("x", manifest.MaximumDeclarationSize+1)
	if _, err := manifest.ParsePlugin([]byte(input)); !errors.Is(err, manifest.ErrInvalidPlugin) {
		t.Fatalf("ParsePlugin oversized error = %v, want ErrInvalidPlugin", err)
	}
}

func FuzzParsePlugin(f *testing.F) {
	for _, seed := range []string{"id: acme.one\n", validPlugin, "[]\n", "id: &x acme.one\nrequires: [*x]\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		plugin, err := manifest.ParsePlugin([]byte(input))
		if err != nil {
			if !errors.Is(err, manifest.ErrInvalidPlugin) {
				t.Fatalf("ParsePlugin returned unexpected error: %v", err)
			}
			return
		}
		if plugin.ID().String() == "" {
			t.Fatal("ParsePlugin returned a plugin without an ID")
		}
		assertSortedIdentifiers(t, plugin.Provides())
		assertSortedIdentifiers(t, plugin.Requires())
	})
}

func assertSortedIdentifiers(t *testing.T, identifiers []capability.Identifier) {
	t.Helper()
	for index := 1; index < len(identifiers); index++ {
		if identifiers[index-1].String() >= identifiers[index].String() {
			t.Fatalf("identifiers are not uniquely sorted: %q then %q", identifiers[index-1], identifiers[index])
		}
	}
}

func identifierStrings(identifiers []capability.Identifier) []string {
	values := make([]string, len(identifiers))
	for index := range identifiers {
		values[index] = identifiers[index].String()
	}
	return values
}
