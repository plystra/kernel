package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"sort"
)

type canonicalCapabilitySchema struct {
	ID         string                          `json:"id"`
	Request    map[string]canonicalSchemaField `json:"request"`
	Response   map[string]canonicalSchemaField `json:"response"`
	Errors     []string                        `json:"errors"`
	Semantics  canonicalCapabilitySemantics    `json:"semantics"`
	Extensions map[string]json.RawMessage      `json:"extensions,omitempty"`
}

type canonicalCapabilitySemantics struct {
	Kind         CapabilityKind                 `json:"kind"`
	Effects      CapabilityEffects              `json:"effects"`
	Idempotency  canonicalIdempotencySemantics  `json:"idempotency"`
	Retry        canonicalRetrySemantics        `json:"retry"`
	Cancellation canonicalCancellationSemantics `json:"cancellation"`
	Completion   canonicalCompletionSemantics   `json:"completion"`
	Ordering     canonicalOrderingSemantics     `json:"ordering"`
	Data         canonicalDataSemantics         `json:"data"`
}

type canonicalIdempotencySemantics struct {
	Mode         IdempotencyMode `json:"mode"`
	RequestField string          `json:"request_field,omitempty"`
}

type canonicalRetrySemantics struct {
	Safety RetrySafety `json:"safety"`
}

type canonicalCancellationSemantics struct {
	Mode CancellationMode `json:"mode"`
}

type canonicalCompletionSemantics struct {
	Mode CompletionMode `json:"mode"`
}

type canonicalOrderingSemantics struct {
	Mode         OrderingMode `json:"mode"`
	RequestField string       `json:"request_field,omitempty"`
}

type canonicalDataSemantics struct {
	Request  DataClassification `json:"request"`
	Response DataClassification `json:"response"`
}

type canonicalSchemaField struct {
	Type     SchemaType        `json:"type"`
	Items    SchemaType        `json:"items,omitempty"`
	Required bool              `json:"required,omitempty"`
	Enum     []json.RawMessage `json:"enum,omitempty"`
}

// CanonicalSchemaJSON returns the deterministic semantic wire schema and
// build-time metadata. Human descriptions and source formatting are excluded.
func (c Capability) CanonicalSchemaJSON() ([]byte, error) {
	if c.id.String() == "" {
		return nil, invalidCapability("cannot normalize a capability without an ID")
	}
	canonical := canonicalCapabilitySchema{
		ID:         c.id.String(),
		Request:    canonicalizeSchema(c.request),
		Response:   canonicalizeSchema(c.response),
		Errors:     append([]string(nil), c.errors...),
		Semantics:  canonicalizeCapabilitySemantics(c.semantics),
		Extensions: canonicalizeCapabilityExtensions(c.extensions),
	}
	sort.Strings(canonical.Errors)
	if canonical.Errors == nil {
		canonical.Errors = []string{}
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, invalidCapability("encode canonical schema: %v", err)
	}
	return encoded, nil
}

func canonicalizeCapabilitySemantics(semantics CapabilitySemantics) canonicalCapabilitySemantics {
	return canonicalCapabilitySemantics{
		Kind:    semantics.kind,
		Effects: semantics.effects,
		Idempotency: canonicalIdempotencySemantics{
			Mode:         semantics.idempotency.mode,
			RequestField: semantics.idempotency.requestField,
		},
		Retry:        canonicalRetrySemantics{Safety: semantics.retry.safety},
		Cancellation: canonicalCancellationSemantics{Mode: semantics.cancellation.mode},
		Completion:   canonicalCompletionSemantics{Mode: semantics.completion.mode},
		Ordering: canonicalOrderingSemantics{
			Mode:         semantics.ordering.mode,
			RequestField: semantics.ordering.requestField,
		},
		Data: canonicalDataSemantics{
			Request:  semantics.data.request,
			Response: semantics.data.response,
		},
	}
}

func canonicalizeCapabilityExtensions(extensions CapabilityExtensions) map[string]json.RawMessage {
	if len(extensions.values) == 0 {
		return nil
	}
	canonical := make(map[string]json.RawMessage, len(extensions.values))
	for _, extension := range extensions.values {
		canonical[extension.namespace] = json.RawMessage(extension.ValueJSON())
	}
	return canonical
}

// SchemaDigest returns the SHA-256 digest of CanonicalSchemaJSON.
func (c Capability) SchemaDigest() ([sha256.Size]byte, error) {
	encoded, err := c.CanonicalSchemaJSON()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}

func canonicalizeSchema(schema Schema) map[string]canonicalSchemaField {
	canonical := make(map[string]canonicalSchemaField, len(schema.fields))
	for _, field := range schema.fields {
		enum := field.EnumJSON()
		sort.Slice(enum, func(left, right int) bool {
			return bytes.Compare(enum[left], enum[right]) < 0
		})
		canonicalEnum := make([]json.RawMessage, len(enum))
		for index := range enum {
			canonicalEnum[index] = json.RawMessage(enum[index])
		}
		canonical[field.name] = canonicalSchemaField{
			Type:     field.schemaType,
			Items:    field.items,
			Required: field.required,
			Enum:     canonicalEnum,
		}
	}
	return canonical
}
