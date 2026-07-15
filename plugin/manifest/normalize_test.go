package manifest_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/plystra/kernel/plugin/manifest"
)

func TestCapabilityCanonicalSchemaGolden(t *testing.T) {
	t.Parallel()

	contract, err := manifest.ParseCapability([]byte(validCapability))
	if err != nil {
		t.Fatalf("ParseCapability: %v", err)
	}
	got, err := contract.CanonicalSchemaJSON()
	if err != nil {
		t.Fatalf("CanonicalSchemaJSON: %v", err)
	}
	want, err := os.ReadFile("testdata/email.send.v1.canonical.json")
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	want = bytes.TrimSuffix(want, []byte("\r\n"))
	want = bytes.TrimSuffix(want, []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical schema:\n got: %s\nwant: %s", got, want)
	}
	if !json.Valid(got) {
		t.Fatalf("canonical schema is not JSON: %s", got)
	}

	digest, err := contract.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest: %v", err)
	}
	wantDigest := sha256.Sum256(want)
	if digest != wantDigest {
		t.Fatalf("SchemaDigest() = %s, want %s", hex.EncodeToString(digest[:]), hex.EncodeToString(wantDigest[:]))
	}
}

func TestCapabilityCanonicalSchemaIgnoresNonSemanticDifferences(t *testing.T) {
	t.Parallel()

	first, err := manifest.ParseCapability([]byte(validCapability))
	if err != nil {
		t.Fatalf("ParseCapability(first): %v", err)
	}
	second, err := manifest.ParseCapability([]byte(`errors: [authentication_failed, temporarily_unavailable, invalid_recipient]
response:
  status: {required: true, enum: [sent, queued], type: string}
  message_id: {required: true, type: string}
request:
  urgent: {required: false, type: boolean}
  to: {required: true, items: string, type: array}
  subject: {required: true, type: string}
  ratio: {type: number}
  metadata: {type: object}
  attempts: {type: integer}
description: Provider-specific wording that is not part of the wire schema.
id: email.send/v1
`))
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
		t.Fatalf("non-semantic differences changed schema:\nfirst:  %s\nsecond: %s", firstJSON, secondJSON)
	}
}

func TestCapabilityCanonicalSchemaPreservesSemanticDifferences(t *testing.T) {
	t.Parallel()

	baseline, err := manifest.ParseCapability([]byte("id: example.check/v1\nrequest:\n  value: {type: string}\nerrors: [invalid_value]\n"))
	if err != nil {
		t.Fatalf("ParseCapability(baseline): %v", err)
	}
	baselineJSON, err := baseline.CanonicalSchemaJSON()
	if err != nil {
		t.Fatalf("CanonicalSchemaJSON(baseline): %v", err)
	}
	tests := []struct {
		name  string
		input string
	}{
		{name: "identity", input: "id: example.check/v2\nrequest:\n  value: {type: string}\nerrors: [invalid_value]\n"},
		{name: "type", input: "id: example.check/v1\nrequest:\n  value: {type: integer}\nerrors: [invalid_value]\n"},
		{name: "required", input: "id: example.check/v1\nrequest:\n  value: {type: string, required: true}\nerrors: [invalid_value]\n"},
		{name: "enum", input: "id: example.check/v1\nrequest:\n  value: {type: string, enum: [one]}\nerrors: [invalid_value]\n"},
		{name: "error", input: "id: example.check/v1\nrequest:\n  value: {type: string}\nerrors: []\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			changed, err := manifest.ParseCapability([]byte(test.input))
			if err != nil {
				t.Fatalf("ParseCapability: %v", err)
			}
			changedJSON, err := changed.CanonicalSchemaJSON()
			if err != nil {
				t.Fatalf("CanonicalSchemaJSON: %v", err)
			}
			if bytes.Equal(baselineJSON, changedJSON) {
				t.Fatalf("semantic %s change did not change canonical schema", test.name)
			}
		})
	}
}

func TestZeroCapabilityCannotBeNormalized(t *testing.T) {
	t.Parallel()

	var contract manifest.Capability
	if encoded, err := contract.CanonicalSchemaJSON(); !errors.Is(err, manifest.ErrInvalidCapability) || encoded != nil {
		t.Fatalf("CanonicalSchemaJSON(zero) = %q, %v", encoded, err)
	}
	if digest, err := contract.SchemaDigest(); !errors.Is(err, manifest.ErrInvalidCapability) || digest != [sha256.Size]byte{} {
		t.Fatalf("SchemaDigest(zero) = %x, %v", digest, err)
	}
}
