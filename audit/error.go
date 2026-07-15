package audit

// ErrorCode is one stable machine-readable capability failure class recorded
// by runtime audit and mapped consistently across transports.
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

// String returns the stable wire and audit representation.
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
