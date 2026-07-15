// Package invocation provides the Kernel's governed typed capability runtime.
package invocation

import (
	"context"
	"errors"
	"fmt"

	"github.com/plystra/kernel/capability"
)

var (
	// ErrInvalidEndpoint reports an invalid contract or provider handler.
	ErrInvalidEndpoint = errors.New("invalid capability endpoint")
	// ErrContractMismatch reports an attempt to cross independently declared
	// typed capability boundaries.
	ErrContractMismatch = errors.New("capability contract definition mismatch")
	// ErrProviderPanic reports a recovered provider panic without exposing its
	// payload across the capability boundary.
	ErrProviderPanic = errors.New("capability provider panicked")
)

type erasedInvoker func(context.Context, any) (any, error)

// Endpoint is an opaque typed provider adapter owned by the Kernel runtime.
type Endpoint struct {
	definition capability.Definition
	invoke     erasedInvoker
}

// NewEndpoint adapts one typed handler without exposing erased invocation to
// callers. Provider panics become ErrProviderPanic and never include the panic
// payload in the returned error.
func NewEndpoint[Request, Response any](
	contract capability.Contract[Request, Response],
	handler capability.Handler[Request, Response],
) (Endpoint, error) {
	if !contract.Valid() || handler == nil {
		return Endpoint{}, ErrInvalidEndpoint
	}
	return Endpoint{
		definition: contract.Definition(),
		invoke: func(ctx context.Context, value any) (result any, err error) {
			request, ok := value.(requestBox[Request])
			if !ok {
				return nil, fmt.Errorf("%w: %s request", ErrContractMismatch, contract.Identifier())
			}
			defer func() {
				if recover() != nil {
					result = nil
					err = fmt.Errorf("%w: %s", ErrProviderPanic, contract.Identifier())
				}
			}()
			response, err := handler(ctx, request.value)
			if err != nil {
				return nil, err
			}
			return responseBox[Response]{value: response}, nil
		},
	}, nil
}

// Definition returns the exact typed capability declaration bound to the
// endpoint.
func (e Endpoint) Definition() capability.Definition {
	return e.definition
}

func (e Endpoint) valid() bool {
	return e.definition.Valid() && e.invoke != nil
}

type requestBox[Request any] struct {
	value Request
}

type responseBox[Response any] struct {
	value Response
}

func invokeEndpoint[Request, Response any](
	ctx context.Context,
	endpoint Endpoint,
	definition capability.Definition,
	request Request,
) (Response, error) {
	var zero Response
	if !endpoint.valid() {
		return zero, ErrInvalidEndpoint
	}
	if !definition.Valid() || endpoint.definition != definition {
		return zero, fmt.Errorf("%w: %s", ErrContractMismatch, endpoint.definition.Identifier())
	}
	result, err := endpoint.invoke(ctx, requestBox[Request]{value: request})
	if err != nil {
		return zero, err
	}
	response, ok := result.(responseBox[Response])
	if !ok {
		return zero, fmt.Errorf("%w: %s response", ErrContractMismatch, endpoint.definition.Identifier())
	}
	return response.value, nil
}
