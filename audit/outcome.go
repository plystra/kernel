package audit

import (
	"errors"
	"strings"
)

// MaximumDetailCodeSize bounds safe machine-readable failure detail codes.
const MaximumDetailCodeSize = 128

// ErrInvalidOutcome reports an incomplete or contradictory invocation result.
var ErrInvalidOutcome = errors.New("invalid runtime invocation outcome")

// OutcomeStatus is one terminal audited invocation state.
type OutcomeStatus string

const (
	OutcomeSucceeded     OutcomeStatus = "succeeded"
	OutcomeFailed        OutcomeStatus = "failed"
	OutcomeDenied        OutcomeStatus = "denied"
	OutcomeTimedOut      OutcomeStatus = "timed_out"
	OutcomeCancelled     OutcomeStatus = "cancelled"
	OutcomeResultUnknown OutcomeStatus = "result_unknown"
)

// String returns the stable audited status representation.
func (s OutcomeStatus) String() string {
	if !s.Valid() {
		return ""
	}
	return string(s)
}

// Valid reports whether the status is a closed terminal invocation state.
func (s OutcomeStatus) Valid() bool {
	switch s {
	case OutcomeSucceeded, OutcomeFailed, OutcomeDenied, OutcomeTimedOut, OutcomeCancelled, OutcomeResultUnknown:
		return true
	default:
		return false
	}
}

// Outcome is one immutable safe terminal invocation result. It stores no
// provider error, panic payload, stack trace, or free-form message.
type Outcome struct {
	status     OutcomeStatus
	errorCode  ErrorCode
	detailCode string
}

// NewSucceededOutcome creates a successful terminal outcome.
func NewSucceededOutcome() Outcome {
	return Outcome{status: OutcomeSucceeded}
}

// NewErrorOutcome creates the exact terminal state implied by a standardized
// capability error class. Denials require a stable detail code.
func NewErrorOutcome(code ErrorCode, detailCode string) (Outcome, error) {
	outcome := Outcome{
		status:     outcomeStatusForError(code),
		errorCode:  code,
		detailCode: detailCode,
	}
	if !outcome.Valid() {
		return Outcome{}, ErrInvalidOutcome
	}
	return outcome, nil
}

// Status returns the terminal audited state.
func (o Outcome) Status() OutcomeStatus {
	if !o.Valid() {
		return ""
	}
	return o.status
}

// ErrorCode returns the standardized failure class, or zero for success.
func (o Outcome) ErrorCode() ErrorCode {
	if !o.Valid() {
		return ""
	}
	return o.errorCode
}

// DetailCode returns the optional safe machine-readable failure detail.
func (o Outcome) DetailCode() string {
	if !o.Valid() {
		return ""
	}
	return o.detailCode
}

// Valid reports whether the outcome has one complete non-contradictory shape.
func (o Outcome) Valid() bool {
	if !o.status.Valid() || !ValidDetailCode(o.detailCode) {
		return false
	}
	if o.status == OutcomeSucceeded {
		return o.errorCode == "" && o.detailCode == ""
	}
	if !o.errorCode.Valid() || o.status != outcomeStatusForError(o.errorCode) {
		return false
	}
	return o.errorCode != ErrorDenied || o.detailCode != ""
}

// ValidDetailCode reports whether a code is empty or a bounded canonical
// lower-case dotted identifier safe for errors, audit, and transports.
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

func outcomeStatusForError(code ErrorCode) OutcomeStatus {
	switch code {
	case ErrorDenied:
		return OutcomeDenied
	case ErrorTimeout:
		return OutcomeTimedOut
	case ErrorCancelled:
		return OutcomeCancelled
	case ErrorResultUnknown:
		return OutcomeResultUnknown
	default:
		if code.Valid() {
			return OutcomeFailed
		}
		return ""
	}
}
