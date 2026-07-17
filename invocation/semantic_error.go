package invocation

import (
	"errors"

	"github.com/plystra/kernel/capability"
)

var errInvalidProviderSemanticError = errors.New("invalid capability semantic error")

var _ capability.SemanticError = (*SemanticError)(nil)

// SemanticError is an immutable provider-neutral semantic failure declared by
// the exact capability contract. Provider messages and causes are not stored.
// Values are created only by the Kernel endpoint boundary.
type SemanticError struct {
	code string
}

// Error returns only the validated machine-readable semantic code.
func (e *SemanticError) Error() string {
	if !e.valid() {
		return errInvalidProviderSemanticError.Error()
	}
	return "capability invocation failed: capability_error: " + e.code
}

// SemanticErrorCode returns the declared code, or an empty string for an
// invalid value.
func (e *SemanticError) SemanticErrorCode() string {
	if !e.valid() {
		return ""
	}
	return e.code
}

func newSemanticError(code string) *SemanticError {
	if !capability.ValidSemanticErrorCode(code) {
		return nil
	}
	return &SemanticError{code: code}
}

func (e *SemanticError) valid() bool {
	return e != nil && capability.ValidSemanticErrorCode(e.code)
}
