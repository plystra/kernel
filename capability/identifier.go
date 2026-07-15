// Package capability defines stable provider-independent capability identities
// and contracts used by Plystra plugins and the Kernel runtime.
package capability

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidIdentifier reports a non-canonical capability identity.
var ErrInvalidIdentifier = errors.New("invalid capability identifier")

// Identifier names one exact major version of a provider-independent
// capability contract.
type Identifier struct {
	name  string
	major uint64
}

// NewIdentifier validates a capability name and major version.
func NewIdentifier(name string, major uint64) (Identifier, error) {
	if !validName(name) {
		return Identifier{}, fmt.Errorf("%w: invalid name", ErrInvalidIdentifier)
	}
	if major == 0 {
		return Identifier{}, fmt.Errorf("%w: major version must be positive", ErrInvalidIdentifier)
	}
	return Identifier{name: name, major: major}, nil
}

// ParseIdentifier parses an exact canonical identity such as email.send/v1.
func ParseIdentifier(value string) (Identifier, error) {
	name, version, ok := strings.Cut(value, "/")
	if !ok || strings.Contains(version, "/") || len(version) < 2 || version[0] != 'v' {
		return Identifier{}, fmt.Errorf("%w: expected <capability-name>/v<major>", ErrInvalidIdentifier)
	}
	if version[1] == '0' {
		return Identifier{}, fmt.Errorf("%w: major version must not have a leading zero", ErrInvalidIdentifier)
	}
	major, err := strconv.ParseUint(version[1:], 10, 64)
	if err != nil || major == 0 {
		return Identifier{}, fmt.Errorf("%w: invalid major version", ErrInvalidIdentifier)
	}
	return NewIdentifier(name, major)
}

// Name returns the unversioned provider-independent capability name.
func (i Identifier) Name() string {
	return i.name
}

// Major returns the exact capability contract major version.
func (i Identifier) Major() uint64 {
	return i.major
}

// String returns the canonical capability identity, or an empty string for an
// invalid zero value.
func (i Identifier) String() string {
	if !i.valid() {
		return ""
	}
	return i.name + "/v" + strconv.FormatUint(i.major, 10)
}

func (i Identifier) valid() bool {
	return i.name != "" && i.major != 0
}

func validName(name string) bool {
	segments := strings.Split(name, ".")
	if len(segments) < 2 {
		return false
	}
	for _, segment := range segments {
		if !validSegment(segment) {
			return false
		}
	}
	return true
}

func validSegment(segment string) bool {
	if segment == "" || !isLowerASCII(segment[0]) {
		return false
	}
	for index := 1; index < len(segment); index++ {
		character := segment[index]
		if !isLowerASCII(character) && !isDigitASCII(character) && character != '-' {
			return false
		}
	}
	return true
}

func isLowerASCII(character byte) bool {
	return character >= 'a' && character <= 'z'
}

func isDigitASCII(character byte) bool {
	return character >= '0' && character <= '9'
}
