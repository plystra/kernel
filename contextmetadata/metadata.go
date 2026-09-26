// Package contextmetadata carries bounded, immutable, process-local opaque
// bytes through ordinary Go contexts. Metadata conveys no identity or authority
// and must not be serialized into Interface messages, logs, or configuration.
package contextmetadata

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/plystra/kernel/capability"
)

var (
	// ErrInvalidMetadata reports an invalid context, key, or storage bound.
	ErrInvalidMetadata = errors.New("invalid context metadata")
	// ErrMetadataConflict reports a key already present in the context lineage.
	ErrMetadataConflict = errors.New("context metadata key already exists")
)

const (
	maximumKeyBytes     = 128
	maximumEntries      = 32
	maximumPayloadBytes = 262_144
	maximumTotalBytes   = 524_288
)

type metadataKey struct{}

type metadata struct {
	entries map[string][]byte
	size    int
}

func (*metadata) String() string   { return "contextmetadata(redacted)" }
func (*metadata) GoString() string { return "contextmetadata(redacted)" }

// WithBytes adds a defensive copy of value under one exact Interface-ID-shaped
// key. Keys contain at most 128 ASCII bytes; values contain 1..262144 bytes.
// A lineage holds at most 32 entries and 524288 aggregate key and value bytes.
// A duplicate key is never replaced. Failure returns nil without changing ctx.
// Payload schemas and trust decisions belong to their owners, not the Kernel.
func WithBytes(ctx context.Context, key string, value []byte) (context.Context, error) {
	if ctx == nil || !validKey(key) || len(value) == 0 || len(value) > maximumPayloadBytes {
		return nil, ErrInvalidMetadata
	}
	previous, _ := ctx.Value(metadataKey{}).(*metadata)
	var entries map[string][]byte
	size := len(key) + len(value)
	if previous != nil {
		if _, exists := previous.entries[key]; exists {
			return nil, ErrMetadataConflict
		}
		if len(previous.entries) >= maximumEntries || previous.size > maximumTotalBytes-size {
			return nil, ErrInvalidMetadata
		}
		// Existing payloads are immutable and never returned without copying.
		entries = maps.Clone(previous.entries)
		size += previous.size
	} else {
		entries = make(map[string][]byte, 1)
	}
	entries[key] = slices.Clone(value)
	return context.WithValue(ctx, metadataKey{}, &metadata{entries: entries, size: size}), nil
}

// Bytes returns a fresh payload copy. A missing valid key returns nil, false,
// nil. Nil contexts and noncanonical keys return ErrInvalidMetadata. Callers
// must not retain a target context or its payload after that attempt terminates.
func Bytes(ctx context.Context, key string) ([]byte, bool, error) {
	if ctx == nil || !validKey(key) {
		return nil, false, ErrInvalidMetadata
	}
	stored, _ := ctx.Value(metadataKey{}).(*metadata)
	if stored == nil {
		return nil, false, nil
	}
	value, exists := stored.entries[key]
	if !exists {
		return nil, false, nil
	}
	return slices.Clone(value), true, nil
}

func validKey(value string) bool {
	if len(value) == 0 || len(value) > maximumKeyBytes {
		return false
	}
	identifier, err := capability.ParseIdentifier(value)
	return err == nil && identifier.String() == value
}
