package invocation

import (
	"errors"

	"github.com/plystra/kernel/capability"
)

// ErrInvalidHandle reports an invalid caller scope or typed contract.
var ErrInvalidHandle = errors.New("invalid capability handle")

// Handle is an opaque typed, caller-bound reference to one exact capability
// contract. It carries no provider function; Invoke always delegates through
// its bound Dispatcher's governed runtime path.
type Handle[Request, Response any] struct {
	scope      Scope
	definition capability.Definition
	available  bool
	// Bind both type arguments into the underlying representation so callers
	// cannot explicitly convert a handle to unrelated request/response types.
	_ *Request
	_ *Response
}

// NewHandle creates a typed capability reference from generated resolution
// state. Available may be false for an optional capability that had no selected
// provider.
func NewHandle[Request, Response any](
	scope Scope,
	contract capability.Contract[Request, Response],
	available bool,
) (Handle[Request, Response], error) {
	if !scope.valid() || !contract.Valid() {
		return Handle[Request, Response]{}, ErrInvalidHandle
	}
	return Handle[Request, Response]{
		scope:      scope,
		definition: contract.Definition(),
		available:  available,
	}, nil
}

// Identifier returns the exact provider-independent capability identity.
func (h Handle[Request, Response]) Identifier() capability.Identifier {
	if !h.valid() {
		return capability.Identifier{}
	}
	return h.definition.Identifier()
}

// Available reports whether generated resolution selected a provider.
func (h Handle[Request, Response]) Available() bool {
	return h.valid() && h.available
}

func (h Handle[Request, Response]) valid() bool {
	return h.scope.valid() && h.definition.Valid()
}
