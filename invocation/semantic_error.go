package invocation

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/plystra/kernel/capability"
)

var errInvalidProviderSemanticError = errors.New("invalid capability semantic error")

// SemanticError carries a contract-owned code and an optional private cause.
// Endpoints validate the code against the invoked contract and return a new
// carrier containing only that code and its completion, never the cause.
type SemanticError struct {
	code       string
	cause      error
	completion Completion
}

// NewSemanticError creates a semantic result for validation by the invoked
// Interface. Invalid codes produce an invalid carrier that normalizes to
// internal failure. Neither code validation nor formatting calls cause.Error().
func NewSemanticError(code string, cause error) *SemanticError {
	if !capability.ValidSemanticErrorCode(code) {
		code = ""
	}
	return &SemanticError{code: code, cause: cause, completion: CompletionResultKnown}
}

// Error returns only the validated machine-readable semantic code.
func (e *SemanticError) Error() string {
	if !e.valid() {
		return errInvalidProviderSemanticError.Error()
	}
	return "capability invocation failed: capability_error: " + e.code
}

// Code returns the semantic code, or an empty string for an
// invalid value.
func (e *SemanticError) Code() string {
	if !e.valid() {
		return ""
	}
	return e.code
}

// Unwrap returns the optional private cause for local errors.Is/errors.As use.
func (e *SemanticError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Completion preserves uncertainty anywhere in the bounded private cause tree.
func (e *SemanticError) Completion() Completion { return CompletionOf(e) }

// Format omits private causes for every fmt verb, including %#v.
func (e SemanticError) Format(state fmt.State, _ rune) { fmt.Fprint(state, e.Error()) }

// LogValue omits private causes from structured logs.
func (e SemanticError) LogValue() slog.Value { return slog.StringValue(e.Error()) }

func (e *SemanticError) valid() bool {
	return e != nil && capability.ValidSemanticErrorCode(e.code) && e.completion.Valid()
}
