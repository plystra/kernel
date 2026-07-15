package capability

import (
	"context"
	"errors"
	"fmt"
)

// ErrInvalidContract reports an invalid typed capability declaration.
var ErrInvalidContract = errors.New("invalid capability contract")

type definitionToken [1]byte

// Definition is the opaque identity shared by one typed capability
// declaration. Runtime bindings compare definitions so independently declared
// Go types cannot be wired together only because their textual IDs match.
type Definition struct {
	identifier Identifier
	token      *definitionToken
}

// Identifier returns the exact provider-independent capability identity.
func (d Definition) Identifier() Identifier {
	return d.identifier
}

// Valid reports whether the definition came from a successful contract
// declaration.
func (d Definition) Valid() bool {
	return d.identifier.valid() && d.token != nil
}

// Contract binds one exact capability identity to its typed Go request and
// response values.
type Contract[Request, Response any] struct {
	definition Definition
	// Bind both type arguments into the underlying representation so callers
	// cannot explicitly convert a contract to unrelated request/response types.
	_ *Request
	_ *Response
}

// NewContract declares one typed capability contract.
func NewContract[Request, Response any](identifier Identifier) (Contract[Request, Response], error) {
	if !identifier.valid() {
		return Contract[Request, Response]{}, ErrInvalidContract
	}
	return Contract[Request, Response]{
		definition: Definition{
			identifier: identifier,
			token:      &definitionToken{},
		},
	}, nil
}

// MustParseContract parses and declares a package-level typed contract. It
// panics when value is not an exact canonical capability identity.
func MustParseContract[Request, Response any](value string) Contract[Request, Response] {
	identifier, err := ParseIdentifier(value)
	if err != nil {
		panic(fmt.Errorf("declare capability contract: %w", err))
	}
	contract, err := NewContract[Request, Response](identifier)
	if err != nil {
		panic(fmt.Errorf("declare capability contract: %w", err))
	}
	return contract
}

// Identifier returns the contract's exact capability identity.
func (c Contract[Request, Response]) Identifier() Identifier {
	return c.definition.identifier
}

// Definition returns the opaque declaration identity used by runtime
// registration and dispatch.
func (c Contract[Request, Response]) Definition() Definition {
	return c.definition
}

// Valid reports whether the contract was successfully declared.
func (c Contract[Request, Response]) Valid() bool {
	return c.definition.Valid()
}

// Handler is one typed capability implementation function.
type Handler[Request, Response any] func(context.Context, Request) (Response, error)
