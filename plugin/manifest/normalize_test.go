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
	firstDigest, err := first.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest(first): %v", err)
	}
	secondDigest, err := second.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest(second): %v", err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("non-semantic differences changed digest: %x then %x", firstDigest, secondDigest)
	}
}

func TestCapabilityCanonicalSchemaNormalizesExtensions(t *testing.T) {
	t.Parallel()

	first, err := manifest.ParseCapability([]byte(`id: order.cancel/v1
extensions:
  authz:
    resource:
      required: true
      kind: order
    permission: order.cancel
  authn:
    authenticated: true
`))
	if err != nil {
		t.Fatalf("ParseCapability(first): %v", err)
	}
	second, err := manifest.ParseCapability([]byte(`extensions:
  authn: {authenticated: true}
  authz: {permission: order.cancel, resource: {kind: order, required: true}}
id: order.cancel/v1
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
	want := `{"id":"order.cancel/v1","request":{},"response":{},"errors":[],"extensions":{"authn":{"authenticated":true},"authz":{"permission":"order.cancel","resource":{"kind":"order","required":true}}}}`
	if string(firstJSON) != want || !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("canonical extensions:\nfirst:  %s\nsecond: %s\nwant:   %s", firstJSON, secondJSON, want)
	}
	firstDigest, err := first.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest(first): %v", err)
	}
	secondDigest, err := second.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest(second): %v", err)
	}
	wantDigest := sha256.Sum256([]byte(want))
	if firstDigest != wantDigest || secondDigest != wantDigest {
		t.Fatalf("extension digests = %x and %x, want %x", firstDigest, secondDigest, wantDigest)
	}
	changed, err := manifest.ParseCapability([]byte(`id: order.cancel/v1
extensions:
  authn: {authenticated: false}
  authz: {permission: order.cancel, resource: {kind: order, required: true}}
`))
	if err != nil {
		t.Fatalf("ParseCapability(changed): %v", err)
	}
	changedDigest, err := changed.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest(changed): %v", err)
	}
	if changedDigest == firstDigest {
		t.Fatalf("changed extension value preserved digest %x", changedDigest)
	}

	withoutExtensions, err := manifest.ParseCapability([]byte("id: kernel.health/v1\n"))
	if err != nil {
		t.Fatalf("ParseCapability(withoutExtensions): %v", err)
	}
	emptyExtensions, err := manifest.ParseCapability([]byte("id: kernel.health/v1\nextensions: {}\n"))
	if err != nil {
		t.Fatalf("ParseCapability(emptyExtensions): %v", err)
	}
	withoutJSON, err := withoutExtensions.CanonicalSchemaJSON()
	if err != nil {
		t.Fatalf("CanonicalSchemaJSON(withoutExtensions): %v", err)
	}
	emptyJSON, err := emptyExtensions.CanonicalSchemaJSON()
	if err != nil {
		t.Fatalf("CanonicalSchemaJSON(emptyExtensions): %v", err)
	}
	if !bytes.Equal(withoutJSON, emptyJSON) {
		t.Fatalf("empty extensions changed canonical schema:\nwithout: %s\nempty:   %s", withoutJSON, emptyJSON)
	}
	withoutDigest, err := withoutExtensions.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest(withoutExtensions): %v", err)
	}
	emptyDigest, err := emptyExtensions.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest(emptyExtensions): %v", err)
	}
	if withoutDigest != emptyDigest {
		t.Fatalf("empty extensions changed digest: %x then %x", withoutDigest, emptyDigest)
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
	baselineDigest, err := baseline.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest(baseline): %v", err)
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
		{name: "extension namespace", input: "id: example.check/v1\nrequest:\n  value: {type: string}\nerrors: [invalid_value]\nextensions: {authn: {authenticated: true}}\n"},
		{name: "extension value", input: "id: example.check/v1\nrequest:\n  value: {type: string}\nerrors: [invalid_value]\nextensions: {authz: {permission: example.read}}\n"},
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
			changedDigest, err := changed.SchemaDigest()
			if err != nil {
				t.Fatalf("SchemaDigest: %v", err)
			}
			if baselineDigest == changedDigest {
				t.Fatalf("semantic %s change did not change schema digest", test.name)
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
