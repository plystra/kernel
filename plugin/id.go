// Package plugin defines the stable public identities and runtime contracts
// shared by Plystra plugins and the Kernel.
package plugin

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidID reports a non-canonical concrete Plugin ID.
var ErrInvalidID = errors.New("invalid plugin ID")

// ID is a canonical immutable concrete plugin identifier.
type ID struct {
	value string
}

// ParseID parses a lower-case dot-separated Plugin ID.
func ParseID(value string) (ID, error) {
	segments := strings.Split(value, ".")
	if len(segments) < 2 {
		return ID{}, invalidID()
	}
	for _, segment := range segments {
		if !validIDSegment(segment) {
			return ID{}, invalidID()
		}
	}
	return ID{value: value}, nil
}

// String returns the canonical Plugin ID, or an empty string for the zero
// value.
func (id ID) String() string {
	return id.value
}

func validIDSegment(segment string) bool {
	if segment == "" || !isLowerASCII(segment[0]) {
		return false
	}
	previousHyphen := false
	for index := 1; index < len(segment); index++ {
		character := segment[index]
		switch {
		case isLowerASCII(character), isDigitASCII(character):
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

func isLowerASCII(character byte) bool {
	return character >= 'a' && character <= 'z'
}

func isDigitASCII(character byte) bool {
	return character >= '0' && character <= '9'
}

func invalidID() error {
	return fmt.Errorf("%w: expected lower-case dot-separated segments", ErrInvalidID)
}
