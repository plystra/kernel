package assembly

import (
	"errors"
	"fmt"
	"strconv"
)

var (
	// ErrInvalidVersion reports a malformed or zero assembly API version.
	ErrInvalidVersion = errors.New("invalid assembly API version")
	// ErrUnsupportedVersion reports a valid assembly API version that this
	// Kernel release cannot execute.
	ErrUnsupportedVersion = errors.New("unsupported assembly API version")
)

// Version identifies one exact Kernel assembly API contract.
type Version uint32

const (
	// V1 is the initial static assembly contract.
	V1 Version = 1
	// Current is the assembly API emitted by the matching Plystra CLI release.
	Current = V1
)

// ParseVersion parses a canonical assembly API version such as "v1".
func ParseVersion(value string) (Version, error) {
	if len(value) < 2 || value[0] != 'v' || value[1] == '0' {
		return 0, ErrInvalidVersion
	}
	parsed, err := strconv.ParseUint(value[1:], 10, 32)
	if err != nil || parsed == 0 {
		return 0, ErrInvalidVersion
	}
	version := Version(parsed)
	if version.String() != value {
		return 0, ErrInvalidVersion
	}
	return version, nil
}

// String returns the canonical textual form of a Version.
func (v Version) String() string {
	if v == 0 {
		return ""
	}
	return "v" + strconv.FormatUint(uint64(v), 10)
}

// Supported reports whether this Kernel implements v.
func (v Version) Supported() bool {
	return v == Current
}

// RequireVersion rejects invalid or incompatible generated assembly source.
func RequireVersion(v Version) error {
	if v == 0 {
		return ErrInvalidVersion
	}
	if !v.Supported() {
		return fmt.Errorf("%w: generated source targets %s; Kernel supports %s", ErrUnsupportedVersion, v, Current)
	}
	return nil
}
