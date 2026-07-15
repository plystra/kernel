package invocation

import (
	"context"
	"errors"

	"github.com/plystra/kernel/audit"
)

// ErrInvalidError reports an invalid failure class or detail code.
var ErrInvalidError = errors.New("invalid capability invocation error")

// Error is an immutable safe capability-boundary failure. It carries only a
// closed audit class and an optional validated machine-readable detail code;
// provider causes and free-form messages are never stored.
type Error struct {
	code       audit.ErrorCode
	detailCode string
	cause      error
}

// NewError creates one safe classified capability failure. Denials require a
// stable detail code so their authorization reason remains auditable.
func NewError(code audit.ErrorCode, detailCode string) (*Error, error) {
	boundary := &Error{
		code:       code,
		detailCode: detailCode,
		cause:      contextCause(code),
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
func (e *Error) Code() audit.ErrorCode {
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

// Is preserves only the safe standard cancellation and deadline identities.
func (e *Error) Is(target error) bool {
	return e.valid() && e.cause != nil && target != nil && errors.Is(e.cause, target)
}

func (e *Error) valid() bool {
	if e == nil || !e.code.Valid() || !audit.ValidDetailCode(e.detailCode) {
		return false
	}
	if e.code == audit.ErrorDenied && e.detailCode == "" {
		return false
	}
	return e.cause == contextCause(e.code)
}

func contextCause(code audit.ErrorCode) error {
	switch code {
	case audit.ErrorTimeout:
		return context.DeadlineExceeded
	case audit.ErrorCancelled:
		return context.Canceled
	default:
		return nil
	}
}
