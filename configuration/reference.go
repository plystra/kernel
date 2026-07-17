package configuration

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"unicode/utf8"
)

const (
	maximumEnvironmentNameBytes = 256
	maximumFilePathBytes        = 4096
)

var (
	// ErrInvalidReference reports an invalid Secret reference kind or target.
	ErrInvalidReference = errors.New("invalid Secret reference")
	// ErrSecretExposure reports an attempt to serialize a Secret reference or
	// resolved Secret value.
	ErrSecretExposure = errors.New("Secret serialization is prohibited")
)

// ReferenceKind is one supported intrinsic Secret source.
type ReferenceKind string

const (
	ReferenceEnvironment ReferenceKind = "env"
	ReferenceFile        ReferenceKind = "file"
)

// String returns the stable structural kind, or empty for an invalid value.
func (k ReferenceKind) String() string {
	if !k.Valid() {
		return ""
	}
	return string(k)
}

// Valid reports whether the kind belongs to the closed supported set.
func (k ReferenceKind) Valid() bool {
	return k == ReferenceEnvironment || k == ReferenceFile
}

// Reference is an immutable validated Secret reference. Its target remains
// redacted from formatting and serialization.
type Reference struct {
	kind        ReferenceKind
	target      string
	initialized bool
}

// NewEnvironmentReference validates a portable environment-variable name.
func NewEnvironmentReference(name string) (Reference, error) {
	if !validEnvironmentName(name) {
		return Reference{}, ErrInvalidReference
	}
	return Reference{kind: ReferenceEnvironment, target: name, initialized: true}, nil
}

// NewFileReference validates one clean absolute file path.
func NewFileReference(path string) (Reference, error) {
	if !validFilePath(path) {
		return Reference{}, ErrInvalidReference
	}
	return Reference{kind: ReferenceFile, target: path, initialized: true}, nil
}

// Kind returns the non-sensitive structural reference kind.
func (r Reference) Kind() ReferenceKind {
	if !r.Valid() {
		return ""
	}
	return r.kind
}

// Valid reports whether the reference passed constructor validation.
func (r Reference) Valid() bool {
	if !r.initialized || !r.kind.Valid() {
		return false
	}
	switch r.kind {
	case ReferenceEnvironment:
		return validEnvironmentName(r.target)
	case ReferenceFile:
		return validFilePath(r.target)
	default:
		return false
	}
}

// String returns a redacted structural description.
func (r Reference) String() string { return r.redacted() }

// GoString prevents Go-syntax formatting from exposing the target.
func (r Reference) GoString() string { return r.redacted() }

// Format redacts the target for every fmt verb.
func (r Reference) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte(r.redacted()))
}

// LogValue redacts the target for structured standard-library logging.
func (r Reference) LogValue() slog.Value {
	return slog.StringValue(r.redacted())
}

// MarshalJSON rejects accidental reference serialization.
func (r Reference) MarshalJSON() ([]byte, error) {
	return nil, ErrSecretExposure
}

// MarshalText rejects accidental reference serialization.
func (r Reference) MarshalText() ([]byte, error) {
	return nil, ErrSecretExposure
}

// MarshalYAML rejects accidental reference serialization.
func (r Reference) MarshalYAML() (any, error) {
	return nil, ErrSecretExposure
}

func (r Reference) redacted() string {
	kind := r.Kind().String()
	if kind == "" {
		kind = "invalid"
	}
	return "<redacted-secret-reference:" + kind + ">"
}

func validEnvironmentName(name string) bool {
	if len(name) == 0 || len(name) > maximumEnvironmentNameBytes {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if index == 0 {
			if character != '_' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') {
				return false
			}
			continue
		}
		if character != '_' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func validFilePath(path string) bool {
	if len(path) == 0 || len(path) > maximumFilePathBytes || !utf8.ValidString(path) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	for _, character := range path {
		if character < ' ' || character == 0x7f {
			return false
		}
	}
	return true
}
