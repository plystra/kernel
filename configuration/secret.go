package configuration

import (
	"fmt"
	"log/slog"
)

// Secret is one immutable resolved non-empty Secret value. Formatting and
// serialization are always redacted or rejected; Bytes is the only value
// accessor and returns a defensive copy.
type Secret struct {
	value       []byte
	initialized bool
}

// Bytes returns a defensive copy of the resolved value, or nil for an invalid
// Secret.
func (s Secret) Bytes() []byte {
	if !s.Valid() {
		return nil
	}
	return append([]byte(nil), s.value...)
}

// Len returns the resolved byte length, or zero for an invalid Secret.
func (s Secret) Len() int {
	if !s.Valid() {
		return 0
	}
	return len(s.value)
}

// Valid reports whether the Secret was resolved successfully.
func (s Secret) Valid() bool {
	return s.initialized && len(s.value) != 0
}

// String returns only a redaction marker.
func (s Secret) String() string { return "<redacted-secret>" }

// GoString prevents Go-syntax formatting from exposing the value.
func (s Secret) GoString() string { return "<redacted-secret>" }

// Format redacts the value for every fmt verb.
func (s Secret) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-secret>"))
}

// LogValue redacts the value for structured standard-library logging.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue("<redacted-secret>")
}

// MarshalJSON rejects accidental Secret serialization.
func (s Secret) MarshalJSON() ([]byte, error) {
	return nil, ErrSecretExposure
}

// MarshalText rejects accidental Secret serialization.
func (s Secret) MarshalText() ([]byte, error) {
	return nil, ErrSecretExposure
}

// MarshalYAML rejects accidental Secret serialization.
func (s Secret) MarshalYAML() (any, error) {
	return nil, ErrSecretExposure
}

func newSecret(value []byte) Secret {
	if len(value) == 0 {
		return Secret{}
	}
	return Secret{value: append([]byte(nil), value...), initialized: true}
}
