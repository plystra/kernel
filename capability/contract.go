package capability

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// ErrInvalidContract reports an invalid typed capability declaration.
var ErrInvalidContract = errors.New("invalid capability contract")

type definitionToken struct {
	semanticErrors []string
}

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

// SemanticErrors returns the contract's declared semantic error codes in
// canonical order. The returned slice does not share mutable storage with the
// definition.
func (d Definition) SemanticErrors() []string {
	if !d.Valid() {
		return nil
	}
	return append([]string(nil), d.token.semanticErrors...)
}

// DeclaresSemanticError reports whether code is part of this exact contract.
func (d Definition) DeclaresSemanticError(code string) bool {
	if !d.Valid() || !ValidSemanticErrorCode(code) {
		return false
	}
	index := sort.SearchStrings(d.token.semanticErrors, code)
	return index < len(d.token.semanticErrors) && d.token.semanticErrors[index] == code
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

// NewContract declares one typed capability contract without semantic errors.
func NewContract[Request, Response any](identifier Identifier) (Contract[Request, Response], error) {
	return NewContractWithSemanticErrors[Request, Response](identifier)
}

// NewContractWithSemanticErrors declares one typed capability contract.
// Semantic errors are validated, required to be unique, and stored in
// canonical order.
func NewContractWithSemanticErrors[Request, Response any](identifier Identifier, semanticErrors ...string) (Contract[Request, Response], error) {
	if !identifier.valid() {
		return Contract[Request, Response]{}, ErrInvalidContract
	}
	normalizedErrors, err := normalizeSemanticErrors(semanticErrors)
	if err != nil {
		return Contract[Request, Response]{}, err
	}
	return Contract[Request, Response]{
		definition: Definition{
			identifier: identifier,
			token:      &definitionToken{semanticErrors: normalizedErrors},
		},
	}, nil
}

// MustParseContract parses and declares a package-level typed contract without
// semantic errors. It panics when value is not a canonical capability ID.
func MustParseContract[Request, Response any](value string) Contract[Request, Response] {
	return MustParseContractWithSemanticErrors[Request, Response](value)
}

// MustParseContractWithSemanticErrors parses and declares a package-level
// typed contract. It panics when the identifier or a semantic error
// declaration is invalid.
func MustParseContractWithSemanticErrors[Request, Response any](value string, semanticErrors ...string) Contract[Request, Response] {
	identifier, err := ParseIdentifier(value)
	if err != nil {
		panic(fmt.Errorf("declare capability contract: %w", err))
	}
	contract, err := NewContractWithSemanticErrors[Request, Response](identifier, semanticErrors...)
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

// SemanticErrors returns the contract's declared semantic error codes in
// canonical order. The returned slice is a defensive copy.
func (c Contract[Request, Response]) SemanticErrors() []string {
	return c.definition.SemanticErrors()
}

// DeclaresSemanticError reports whether code is part of this exact contract.
func (c Contract[Request, Response]) DeclaresSemanticError(code string) bool {
	return c.definition.DeclaresSemanticError(code)
}

func normalizeSemanticErrors(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	normalized := append([]string(nil), values...)
	for _, code := range normalized {
		if !ValidSemanticErrorCode(code) {
			return nil, fmt.Errorf("%w: semantic error code %q is invalid", ErrInvalidContract, code)
		}
	}
	sort.Strings(normalized)
	for index := 1; index < len(normalized); index++ {
		if normalized[index-1] == normalized[index] {
			return nil, fmt.Errorf("%w: duplicate semantic error code %q", ErrInvalidContract, normalized[index])
		}
	}
	return normalized, nil
}

// Handler is one typed capability implementation function.
type Handler[Request, Response any] func(context.Context, Request) (Response, error)
