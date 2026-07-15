package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"sort"
)

type canonicalCapabilitySchema struct {
	ID       string                          `json:"id"`
	Request  map[string]canonicalSchemaField `json:"request"`
	Response map[string]canonicalSchemaField `json:"response"`
	Errors   []string                        `json:"errors"`
}

type canonicalSchemaField struct {
	Type     SchemaType        `json:"type"`
	Items    SchemaType        `json:"items,omitempty"`
	Required bool              `json:"required,omitempty"`
	Enum     []json.RawMessage `json:"enum,omitempty"`
}

// CanonicalSchemaJSON returns the deterministic semantic wire schema. Human
// descriptions and source formatting are intentionally excluded.
func (c Capability) CanonicalSchemaJSON() ([]byte, error) {
	if c.id.String() == "" {
		return nil, invalidCapability("cannot normalize a capability without an ID")
	}
	canonical := canonicalCapabilitySchema{
		ID:       c.id.String(),
		Request:  canonicalizeSchema(c.request),
		Response: canonicalizeSchema(c.response),
		Errors:   append([]string(nil), c.errors...),
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
