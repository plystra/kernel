package audit

import "strings"

// MaximumDetailCodeSize bounds safe machine-readable failure detail codes.
const MaximumDetailCodeSize = 128

// ErrorCode is one stable machine-readable capability failure class mapped
// consistently across transports.
type ErrorCode string

const (
	ErrorInvalidArgument     ErrorCode = "invalid_argument"
	ErrorNotFound            ErrorCode = "not_found"
	ErrorConflict            ErrorCode = "conflict"
	ErrorDenied              ErrorCode = "denied"
	ErrorUnauthenticated     ErrorCode = "unauthenticated"
	ErrorUnavailable         ErrorCode = "unavailable"
	ErrorTimeout             ErrorCode = "timeout"
	ErrorCancelled           ErrorCode = "cancelled"
	ErrorResultUnknown       ErrorCode = "result_unknown"
	ErrorInternal            ErrorCode = "internal"
	ErrorVersionIncompatible ErrorCode = "version_incompatible"
)

// String returns the stable wire representation.
func (c ErrorCode) String() string {
	return string(c)
}

// Valid reports whether the code is one of the closed standard classes.
func (c ErrorCode) Valid() bool {
	switch c {
	case ErrorInvalidArgument,
		ErrorNotFound,
		ErrorConflict,
		ErrorDenied,
		ErrorUnauthenticated,
		ErrorUnavailable,
		ErrorTimeout,
		ErrorCancelled,
		ErrorResultUnknown,
		ErrorInternal,
		ErrorVersionIncompatible:
		return true
	default:
		return false
	}
}

// ValidDetailCode reports whether a code is empty or a bounded canonical
// lower-case dotted identifier safe for errors and transports.
func ValidDetailCode(code string) bool {
	if code == "" {
		return true
	}
	if len(code) > MaximumDetailCodeSize {
		return false
	}
	for _, segment := range strings.Split(code, ".") {
		if segment == "" || segment[0] < 'a' || segment[0] > 'z' {
			return false
		}
		previousUnderscore := false
		for index := 1; index < len(segment); index++ {
			character := segment[index]
			switch {
			case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
				previousUnderscore = false
			case character == '_' && !previousUnderscore:
				previousUnderscore = true
			default:
				return false
			}
		}
		if previousUnderscore {
			return false
		}
	}
	return true
}
