package invocation

import (
	"fmt"
	"log/slog"
)

// Completion is the certainty of an invocation result, independent of its
// primary runtime or semantic error code.
type Completion string

const (
	CompletionNotStarted    Completion = "not_started"
	CompletionResultKnown   Completion = "result_known"
	CompletionResultUnknown Completion = "result_unknown"
)

// String returns the stable machine-readable classification.
func (c Completion) String() string { return string(c) }

// Valid reports whether c belongs to the closed completion vocabulary.
func (c Completion) Valid() bool {
	return c == CompletionNotStarted || c == CompletionResultKnown || c == CompletionResultUnknown
}

// ResultUnknownError marks a possibly effected operation whose final result
// cannot be determined. Its optional cause is available only for local error
// traversal; formatting never exposes the cause.
type ResultUnknownError struct {
	cause error
}

// NewResultUnknown preserves result uncertainty through wrapping and semantic
// translation. It accepts a nil cause and never calls cause.Error().
func NewResultUnknown(cause error) *ResultUnknownError {
	return &ResultUnknownError{cause: cause}
}

func (e *ResultUnknownError) Error() string { return "capability invocation result unknown" }

// Completion always returns result_unknown, including for a zero or nil receiver.
func (e *ResultUnknownError) Completion() Completion { return CompletionResultUnknown }

// Unwrap returns the optional private cause for local errors.Is/errors.As use.
func (e *ResultUnknownError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Format omits private causes for every fmt verb, including %#v.
func (e ResultUnknownError) Format(state fmt.State, _ rune) { fmt.Fprint(state, e.Error()) }

// LogValue omits private causes from structured logs.
func (e ResultUnknownError) LogValue() slog.Value { return slog.StringValue(e.Error()) }

// CompletionOf inspects ordinary wrapped and joined errors using the same
// bounded, cycle-safe traversal as the endpoint boundary. Any result_unknown
// marker wins. Nil is a known success; unrecognized errors, invalid carriers,
// and incomplete traversal conservatively report result_unknown. Custom As and
// Is methods are not invoked and cannot manufacture Kernel classifications.
func CompletionOf(err error) Completion {
	if err == nil {
		return CompletionResultKnown
	}
	completion := Completion("")
	complete := walkErrorTree(err, func(node error) {
		var next Completion
		switch value := node.(type) {
		case *ResultUnknownError:
			next = CompletionResultUnknown
		case *Error:
			next = value.Completion()
		case *SemanticError:
			if value.valid() {
				next = value.completion
			}
		default:
			return
		}
		if !next.Valid() || next == CompletionResultUnknown {
			completion = CompletionResultUnknown
		} else if completion != CompletionResultUnknown && (completion == "" || next == CompletionResultKnown) {
			completion = next
		}
	})
	if !complete || completion == "" {
		return CompletionResultUnknown
	}
	return completion
}
