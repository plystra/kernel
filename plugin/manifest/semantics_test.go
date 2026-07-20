package manifest_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/plystra/kernel/plugin/manifest"
)

const validQuerySemantics = `kind: query
effects: none
idempotency: {mode: inherent}
retry: {safety: safe}
cancellation: {mode: best-effort}
completion: {mode: completed-before-return}
ordering: {mode: none}
data: {request: public, response: internal}`

const validCommandSemantics = `kind: command
effects: external-write
idempotency:
  mode: keyed
  request_field: idempotency_key
retry: {safety: requires-idempotency-key}
cancellation: {mode: best-effort}
completion: {mode: completed-before-return}
ordering:
  mode: per-key
  request_field: partition
data: {request: confidential, response: restricted}`

const validEventSemantics = `kind: event
effects: external
idempotency: {mode: inherent}
retry: {safety: safe}
cancellation: {mode: best-effort}
completion: {mode: accepted-for-processing}
ordering: {mode: global}
data: {request: internal, response: internal}`

const validStreamSemantics = `kind: stream
effects: external
idempotency: {mode: none}
retry: {safety: never}
cancellation: {mode: unsupported}
completion: {mode: accepted-for-processing}
ordering: {mode: none}
data: {request: restricted, response: restricted}`

func TestParseCapabilitySemanticsKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		request   string
		semantics string
		kind      manifest.CapabilityKind
	}{
		{name: "query", semantics: validQuerySemantics, kind: manifest.CapabilityKindQuery},
		{
			name: "command",
			request: `idempotency_key: {type: string, required: true}
partition: {type: integer, required: true}`,
			semantics: validCommandSemantics,
			kind:      manifest.CapabilityKindCommand,
		},
		{name: "event", semantics: validEventSemantics, kind: manifest.CapabilityKindEvent},
		{name: "stream", semantics: validStreamSemantics, kind: manifest.CapabilityKindStream},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contract, err := manifest.ParseCapability([]byte(capabilityWithSemantics(test.request, test.semantics)))
			if err != nil {
				t.Fatalf("ParseCapability: %v", err)
			}
			if contract.Semantics().Kind() != test.kind {
				t.Fatalf("kind = %q, want %q", contract.Semantics().Kind(), test.kind)
			}
		})
	}

	contract, err := manifest.ParseCapability([]byte(capabilityWithSemantics(
		"idempotency_key: {type: string, required: true}\npartition: {type: integer, required: true}",
		validCommandSemantics,
	)))
	if err != nil {
		t.Fatalf("ParseCapability(command): %v", err)
	}
	semantics := contract.Semantics()
	if semantics.Effects() != manifest.CapabilityEffectsExternalWrite ||
		semantics.Idempotency().Mode() != manifest.IdempotencyModeKeyed ||
		semantics.Idempotency().RequestField() != "idempotency_key" ||
		semantics.Retry().Safety() != manifest.RetrySafetyRequiresIdempotencyKey ||
		semantics.Cancellation().Mode() != manifest.CancellationModeBestEffort ||
		semantics.Completion().Mode() != manifest.CompletionModeCompletedBeforeReturn ||
		semantics.Ordering().Mode() != manifest.OrderingModePerKey ||
		semantics.Ordering().RequestField() != "partition" ||
		semantics.Data().Request() != manifest.DataClassificationConfidential ||
		semantics.Data().Response() != manifest.DataClassificationRestricted {
		t.Fatalf("command semantics = %#v", semantics)
	}
}

func TestParseCapabilityRejectsInvalidSemantics(t *testing.T) {
	t.Parallel()

	query := func(replacements ...string) string {
		value := validQuerySemantics
		for index := 0; index < len(replacements); index += 2 {
			value = strings.Replace(value, replacements[index], replacements[index+1], 1)
		}
		return capabilityWithSemantics("", value)
	}
	commandRequest := "idempotency_key: {type: string, required: true}\npartition: {type: integer, required: true}"
	command := func(request string, replacements ...string) string {
		value := validCommandSemantics
		for index := 0; index < len(replacements); index += 2 {
			value = strings.Replace(value, replacements[index], replacements[index+1], 1)
		}
		return capabilityWithSemantics(request, value)
	}

	tests := []struct {
		name  string
		input string
		path  string
	}{
		{name: "missing semantics", input: "id: example.operation/v1\n", path: "semantics is required"},
		{name: "semantics not mapping", input: "id: example.operation/v1\nsemantics: []\n", path: "semantics must be a mapping"},
		{name: "unknown semantics key", input: query("kind: query", "kind: query\nunknown: value"), path: "semantics contains unknown key"},
		{name: "duplicate semantics key", input: query("kind: query", "kind: query\nkind: query"), path: "semantics contains duplicate key"},
		{name: "missing kind", input: query("kind: query\n", ""), path: "semantics.kind is required"},
		{name: "unknown kind", input: query("kind: query", "kind: mutation"), path: "semantics.kind"},
		{name: "unknown effects", input: query("effects: none", "effects: remote"), path: "semantics.effects"},
		{name: "idempotency not mapping", input: query("idempotency: {mode: inherent}", "idempotency: inherent"), path: "semantics.idempotency must be a mapping"},
		{name: "unknown idempotency", input: query("mode: inherent", "mode: automatic"), path: "semantics.idempotency.mode"},
		{name: "unknown retry", input: query("safety: safe", "safety: maybe"), path: "semantics.retry.safety"},
		{name: "unknown cancellation", input: query("mode: best-effort", "mode: transactional"), path: "semantics.cancellation.mode"},
		{name: "unknown completion", input: query("mode: completed-before-return", "mode: eventual"), path: "semantics.completion.mode"},
		{name: "unknown ordering", input: query("ordering: {mode: none}", "ordering: {mode: causal}"), path: "semantics.ordering.mode"},
		{name: "unknown request classification", input: query("request: public", "request: secret"), path: "semantics.data.request"},
		{name: "unknown response classification", input: query("response: internal", "response: secret"), path: "semantics.data.response"},
		{name: "keyed idempotency missing field", input: query("idempotency: {mode: inherent}", "idempotency: {mode: keyed}", "retry: {safety: safe}", "retry: {safety: requires-idempotency-key}"), path: "semantics.idempotency.request_field is required"},
		{name: "keyed idempotency absent field", input: command("other: {type: string, required: true}"), path: "names absent request field"},
		{name: "keyed idempotency optional field", input: command("idempotency_key: {type: string}\npartition: {type: integer, required: true}"), path: "must name a required request field"},
		{name: "keyed idempotency wrong field type", input: command("idempotency_key: {type: integer, required: true}\npartition: {type: integer, required: true}"), path: "unsupported type"},
		{name: "keyed idempotency nested field", input: command(commandRequest, "request_field: idempotency_key", "request_field: request.idempotency_key"), path: "canonical top-level request field"},
		{name: "non-keyed idempotency field", input: query("idempotency: {mode: inherent}", "idempotency: {mode: inherent, request_field: key}"), path: "valid only"},
		{name: "non-keyed empty idempotency field", input: query("idempotency: {mode: inherent}", "idempotency: {mode: inherent, request_field: ''}"), path: "valid only"},
		{name: "per-key ordering missing field", input: query("ordering: {mode: none}", "ordering: {mode: per-key}"), path: "semantics.ordering.request_field is required"},
		{name: "per-key ordering absent field", input: command(commandRequest, "request_field: partition", "request_field: missing"), path: "names absent request field"},
		{name: "per-key ordering optional field", input: command("idempotency_key: {type: string, required: true}\npartition: {type: integer}"), path: "must name a required request field"},
		{name: "per-key ordering wrong field type", input: command("idempotency_key: {type: string, required: true}\npartition: {type: boolean, required: true}"), path: "unsupported type"},
		{name: "non-per-key ordering field", input: query("ordering: {mode: none}", "ordering: {mode: none, request_field: partition}"), path: "valid only"},
		{name: "non-per-key empty ordering field", input: query("ordering: {mode: none}", "ordering: {mode: none, request_field: ''}"), path: "valid only"},
		{name: "safe retry without inherent idempotency", input: query("idempotency: {mode: inherent}", "idempotency: {mode: none}"), path: "requires semantics.idempotency.mode"},
		{name: "key-required retry without keyed idempotency", input: query("retry: {safety: safe}", "retry: {safety: requires-idempotency-key}"), path: "requires semantics.idempotency.mode"},
		{name: "query with effects", input: query("effects: none", "effects: external"), path: "kind \"query\" requires semantics.effects"},
		{name: "query accepted completion", input: query("mode: completed-before-return", "mode: accepted-for-processing"), path: "kind \"query\" requires semantics.completion.mode"},
		{name: "query ordered", input: query("ordering: {mode: none}", "ordering: {mode: global}"), path: "kind \"query\" requires semantics.ordering.mode"},
		{name: "command without effects", input: command(commandRequest, "effects: external-write", "effects: none"), path: "kind \"command\" requires effects"},
		{name: "event without effects", input: capabilityWithSemantics("", strings.Replace(validEventSemantics, "effects: external", "effects: none", 1)), path: "kind \"event\" requires effects"},
		{name: "event synchronous completion", input: capabilityWithSemantics("", strings.Replace(validEventSemantics, "mode: accepted-for-processing", "mode: completed-before-return", 1)), path: "kind \"event\" requires semantics.completion.mode"},
		{name: "stream local effects", input: capabilityWithSemantics("", strings.Replace(validStreamSemantics, "effects: external", "effects: local", 1)), path: "kind \"stream\" permits only semantics.effects"},
		{name: "stream idempotent", input: capabilityWithSemantics("", strings.Replace(validStreamSemantics, "idempotency: {mode: none}", "idempotency: {mode: inherent}", 1)), path: "kind \"stream\" requires semantics.idempotency.mode"},
		{name: "stream completed synchronously", input: capabilityWithSemantics("", strings.Replace(validStreamSemantics, "mode: accepted-for-processing", "mode: completed-before-return", 1)), path: "kind \"stream\" requires semantics.completion.mode"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contract, err := manifest.ParseCapability([]byte(test.input))
			if !errors.Is(err, manifest.ErrInvalidCapability) {
				t.Fatalf("ParseCapability error = %v, want ErrInvalidCapability", err)
			}
			if !strings.Contains(err.Error(), test.path) {
				t.Fatalf("ParseCapability error = %q, want path %q", err, test.path)
			}
			if contract.ID().String() != "" {
				t.Fatalf("invalid semantics returned contract %#v", contract)
			}
		})
	}
}

func TestCapabilitySemanticsNormalizeDeterministically(t *testing.T) {
	t.Parallel()

	first, err := manifest.ParseCapability([]byte(capabilityWithSemantics("", validQuerySemantics)))
	if err != nil {
		t.Fatalf("ParseCapability(first): %v", err)
	}
	secondSemantics := `data: {response: internal, request: public}
ordering: {mode: none}
completion: {mode: completed-before-return}
cancellation: {mode: best-effort}
retry: {safety: safe}
idempotency: {mode: inherent}
effects: none
kind: query`
	second, err := manifest.ParseCapability([]byte(capabilityWithSemantics("", secondSemantics)))
	if err != nil {
		t.Fatalf("ParseCapability(second): %v", err)
	}
	firstJSON, err := first.CanonicalSchemaJSON()
	if err != nil {
		t.Fatalf("CanonicalSchemaJSON(first): %v", err)
	}
	secondJSON, err := second.CanonicalSchemaJSON()
	if err != nil {
		t.Fatalf("CanonicalSchemaJSON(second): %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("semantic mapping order changed canonical JSON:\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
	firstDigest, err := first.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest(first): %v", err)
	}
	secondDigest, err := second.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest(second): %v", err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("semantic mapping order changed digest: %x then %x", firstDigest, secondDigest)
	}
}

func FuzzParseCapabilitySemantics(f *testing.F) {
	for _, semantics := range []string{validQuerySemantics, validCommandSemantics, validEventSemantics, validStreamSemantics, "kind: unknown"} {
		f.Add(semantics)
	}
	f.Fuzz(func(t *testing.T, semantics string) {
		contract, err := manifest.ParseCapability([]byte(capabilityWithSemantics(
			"idempotency_key: {type: string, required: true}\npartition: {type: integer, required: true}",
			semantics,
		)))
		if err != nil {
			if !errors.Is(err, manifest.ErrInvalidCapability) {
				t.Fatalf("ParseCapability returned unexpected error: %v", err)
			}
			return
		}
		if contract.Semantics().Kind() == "" {
			t.Fatal("ParseCapability returned empty semantics")
		}
	})
}

func capabilityWithSemantics(request, semantics string) string {
	var builder strings.Builder
	builder.WriteString("id: example.operation/v1\n")
	if request == "" {
		builder.WriteString("request: {}\n")
	} else {
		builder.WriteString("request:\n")
		writeIndented(&builder, request, "  ")
	}
	builder.WriteString("response: {}\nerrors: []\nsemantics:\n")
	writeIndented(&builder, semantics, "  ")
	return builder.String()
}

func writeIndented(builder *strings.Builder, value, indent string) {
	for _, line := range strings.Split(strings.TrimSpace(value), "\n") {
		builder.WriteString(indent)
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
}
