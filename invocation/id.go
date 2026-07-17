package invocation

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	encodedRuntimeIDSize     = 32
	maximumRuntimeIDAttempts = 4
)

// ErrInvalidRuntimeID reports a non-canonical or zero runtime ID.
var ErrInvalidRuntimeID = errors.New("invalid runtime ID")

// InvocationID identifies one capability invocation.
type InvocationID struct {
	value string
}

// ParseInvocationID parses a canonical non-zero 128-bit invocation ID.
func ParseInvocationID(value string) (InvocationID, error) {
	if !validRuntimeID(value) {
		return InvocationID{}, ErrInvalidRuntimeID
	}
	return InvocationID{value: value}, nil
}

// NewInvocationID generates a cryptographically random invocation ID.
func NewInvocationID() (InvocationID, error) {
	value, err := randomRuntimeID()
	if err != nil {
		return InvocationID{}, err
	}
	return InvocationID{value: value}, nil
}

// String returns the canonical lower-case hexadecimal ID.
func (id InvocationID) String() string {
	return id.value
}

// Valid reports whether the ID is canonical and non-zero.
func (id InvocationID) Valid() bool {
	return validRuntimeID(id.value)
}

// RequestID identifies one root request across nested invocations.
type RequestID struct {
	value string
}

// ParseRequestID parses a canonical non-zero 128-bit request ID.
func ParseRequestID(value string) (RequestID, error) {
	if !validRuntimeID(value) {
		return RequestID{}, ErrInvalidRuntimeID
	}
	return RequestID{value: value}, nil
}

// NewRequestID generates a cryptographically random request ID.
func NewRequestID() (RequestID, error) {
	value, err := randomRuntimeID()
	if err != nil {
		return RequestID{}, err
	}
	return RequestID{value: value}, nil
}

// String returns the canonical lower-case hexadecimal ID.
func (id RequestID) String() string {
	return id.value
}

// Valid reports whether the ID is canonical and non-zero.
func (id RequestID) Valid() bool {
	return validRuntimeID(id.value)
}

// TraceID identifies one invocation call chain.
type TraceID struct {
	value string
}

// ParseTraceID parses a canonical non-zero 128-bit trace ID.
func ParseTraceID(value string) (TraceID, error) {
	if !validRuntimeID(value) {
		return TraceID{}, ErrInvalidRuntimeID
	}
	return TraceID{value: value}, nil
}

// NewTraceID generates a cryptographically random trace ID.
func NewTraceID() (TraceID, error) {
	value, err := randomRuntimeID()
	if err != nil {
		return TraceID{}, err
	}
	return TraceID{value: value}, nil
}

// String returns the canonical lower-case hexadecimal ID.
func (id TraceID) String() string {
	return id.value
}

// Valid reports whether the ID is canonical and non-zero.
func (id TraceID) Valid() bool {
	return validRuntimeID(id.value)
}

func randomRuntimeID() (string, error) {
	return randomRuntimeIDFrom(rand.Reader)
}

func randomRuntimeIDFrom(source io.Reader) (string, error) {
	if source == nil {
		return "", errors.New("generate runtime ID: nil randomness source")
	}
	var raw [16]byte
	for range maximumRuntimeIDAttempts {
		if _, err := io.ReadFull(source, raw[:]); err != nil {
			return "", fmt.Errorf("generate runtime ID: %w", err)
		}
		value := hex.EncodeToString(raw[:])
		if validRuntimeID(value) {
			return value, nil
		}
	}
	return "", errors.New("generate runtime ID: randomness repeatedly produced zero")
}

func validRuntimeID(value string) bool {
	if len(value) != encodedRuntimeIDSize {
		return false
	}
	nonzero := false
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character != '0' {
			nonzero = true
		}
		if character < '0' || character > '9' && (character < 'a' || character > 'f') {
			return false
		}
	}
	return nonzero
}
