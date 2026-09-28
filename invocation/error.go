package invocation

import (
	"context"
	"errors"
)

// ErrInvalidError reports an invalid failure class or detail code.
var ErrInvalidError = errors.New("invalid capability invocation error")

// Error is an immutable safe capability-boundary failure. It carries only a
// closed failure class and an optional validated machine-readable detail code;
// provider causes and free-form messages are never stored.
type Error struct {
	code       ErrorCode
	detailCode string
	cause      error
	completion Completion
}

// NewError creates one safe classified capability failure. Denials require a
// stable detail code so callers can distinguish policy reasons safely.
// The initial completion is result_known; wrap with NewResultUnknown when the
// operation's final result cannot be determined.
func NewError(code ErrorCode, detailCode string) (*Error, error) {
	boundary := &Error{
		code:       code,
		detailCode: detailCode,
		cause:      contextCause(code),
		completion: CompletionResultKnown,
	}
	if !boundary.valid() {
		return nil, ErrInvalidError
	}
	return boundary, nil
}

// Error returns only validated machine-readable information.
func (e *Error) Error() string {
	if !e.valid() {
		return ErrInvalidError.Error()
	}
	message := "capability invocation failed: " + e.code.String()
	if e.detailCode != "" {
		message += ": " + e.detailCode
	}
	return message
}

// Code returns the standardized failure class, or zero for an invalid error.
func (e *Error) Code() ErrorCode {
	if !e.valid() {
		return ""
	}
	return e.code
}

// DetailCode returns the optional contract, policy, or runtime detail code.
func (e *Error) DetailCode() string {
	if !e.valid() {
		return ""
	}
	return e.detailCode
}

// Completion returns the result certainty, or zero for an invalid error.
// NewError describes a known result; dispatch assigns not_started to failures
// before entry and result_unknown when cancellation suppresses a target result.
func (e *Error) Completion() Completion {
	if !e.valid() {
		return ""
	}
	return e.completion
}

// Is preserves only the safe standard cancellation and deadline identities.
func (e *Error) Is(target error) bool {
	return e.valid() && e.cause != nil && target != nil && errors.Is(e.cause, target)
}

func (e *Error) valid() bool {
	if e == nil || !e.code.Valid() || !ValidDetailCode(e.detailCode) || !e.completion.Valid() {
		return false
	}
	if e.code == ErrorDenied && e.detailCode == "" {
		return false
	}
	return e.cause == contextCause(e.code)
}

func contextCause(code ErrorCode) error {
	switch code {
	case ErrorTimeout:
		return context.DeadlineExceeded
	case ErrorCancelled:
		return context.Canceled
	default:
		return nil
	}
}
