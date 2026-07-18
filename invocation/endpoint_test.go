package invocation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/plystra/kernel/capability"
)

type endpointRequest struct {
	Value string
}

type endpointResponse struct {
	Value string
}

type endpointContextKey struct{}

func TestEndpointInvokesTypedHandler(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	callContext := context.WithValue(context.Background(), endpointContextKey{}, "trusted")
	endpoint, err := NewEndpoint(contract, func(ctx context.Context, request endpointRequest) (endpointResponse, error) {
		if ctx.Value(endpointContextKey{}) != "trusted" {
			t.Fatalf("handler context value was not preserved")
		}
		return endpointResponse{Value: "handled:" + request.Value}, nil
	})
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	if endpoint.Definition() != contract.Definition() || !endpoint.valid() {
		t.Fatalf("endpoint definition = %#v, valid = %t", endpoint.Definition(), endpoint.valid())
	}
	response, err := invokeEndpoint[endpointRequest, endpointResponse](callContext, endpoint, contract.Definition(), endpointRequest{Value: "request"})
	if err != nil {
		t.Fatalf("invokeEndpoint: %v", err)
	}
	if response.Value != "handled:request" {
		t.Fatalf("response = %#v", response)
	}
}

func TestEndpointPreservesTypedNilValues(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[*endpointRequest, *endpointResponse]("example.pointer/v1")
	endpoint, err := NewEndpoint(contract, func(_ context.Context, request *endpointRequest) (*endpointResponse, error) {
		if request != nil {
			t.Fatalf("request = %#v, want typed nil", request)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	response, err := invokeEndpoint[*endpointRequest, *endpointResponse](context.Background(), endpoint, contract.Definition(), (*endpointRequest)(nil))
	if err != nil {
		t.Fatalf("invokeEndpoint: %v", err)
	}
	if response != nil {
		t.Fatalf("response = %#v, want typed nil", response)
	}
}

func TestEndpointReturnsProviderErrorAndZeroResponse(t *testing.T) {
	t.Parallel()

	providerError := errors.New("provider error")
	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.failure/v1")
	endpoint, err := NewEndpoint(contract, func(context.Context, endpointRequest) (endpointResponse, error) {
		return endpointResponse{Value: "must not escape"}, providerError
	})
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	response, err := invokeEndpoint[endpointRequest, endpointResponse](context.Background(), endpoint, contract.Definition(), endpointRequest{})
	if !errors.Is(err, providerError) {
		t.Fatalf("invokeEndpoint error = %v, want provider error", err)
	}
	if response != (endpointResponse{}) {
		t.Fatalf("response = %#v, want zero", response)
	}
}

func TestEndpointPreservesOnlyDeclaredSemanticErrors(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContractWithSemanticErrors[endpointRequest, endpointResponse](
		"example.semantic/v1",
		"invalid_recipient",
	)
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "direct", err: endpointSemanticError("invalid_recipient")},
		{name: "wrapped", err: fmt.Errorf("provider password=secret: %w", endpointSemanticError("invalid_recipient"))},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			endpoint, err := NewEndpoint(contract, func(context.Context, endpointRequest) (endpointResponse, error) {
				return endpointResponse{Value: "must not escape"}, test.err
			})
			if err != nil {
				t.Fatalf("NewEndpoint: %v", err)
			}
			response, err := invokeEndpoint[endpointRequest, endpointResponse](context.Background(), endpoint, contract.Definition(), endpointRequest{})
			if semantic, ok := errors.AsType[*SemanticError](err); !ok || !semantic.valid() || semantic.SemanticErrorCode() != "invalid_recipient" {
				t.Fatalf("semantic error = %#v / %v", semantic, err)
			}
			if response != (endpointResponse{}) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("semantic failure = %#v, %v", response, err)
			}
		})
	}
}

func TestEndpointRejectsUnsafeSemanticErrors(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContractWithSemanticErrors[endpointRequest, endpointResponse](
		"example.semantic/v1",
		"invalid_recipient",
	)
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "undeclared", err: endpointSemanticError("temporarily_unavailable")},
		{name: "malformed", err: endpointSemanticError("InvalidRecipient")},
		{name: "code panic", err: endpointPanickingSemanticError{}},
		{name: "as panic", err: endpointPanickingAsError{}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			endpoint, err := NewEndpoint(contract, func(context.Context, endpointRequest) (endpointResponse, error) {
				return endpointResponse{Value: "must not escape"}, test.err
			})
			if err != nil {
				t.Fatalf("NewEndpoint: %v", err)
			}
			response, err := invokeEndpoint[endpointRequest, endpointResponse](context.Background(), endpoint, contract.Definition(), endpointRequest{})
			if !errors.Is(err, errInvalidProviderSemanticError) || response != (endpointResponse{}) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe semantic failure = %#v, %v", response, err)
			}
		})
	}
}

func TestEndpointRecoversProviderPanicWithoutLeakingPayload(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.panic/v1")
	endpoint, err := NewEndpoint(contract, func(context.Context, endpointRequest) (endpointResponse, error) {
		panic("secret provider details")
	})
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	response, err := invokeEndpoint[endpointRequest, endpointResponse](context.Background(), endpoint, contract.Definition(), endpointRequest{})
	if !errors.Is(err, ErrProviderPanic) {
		t.Fatalf("invokeEndpoint error = %v, want ErrProviderPanic", err)
	}
	if strings.Contains(err.Error(), "secret provider details") {
		t.Fatalf("panic error leaked payload: %v", err)
	}
	if !strings.Contains(err.Error(), "example.panic/v1") {
		t.Fatalf("panic error omitted capability identity: %v", err)
	}
	if response != (endpointResponse{}) {
		t.Fatalf("response = %#v, want zero", response)
	}
}

func TestNewEndpointRejectsInvalidContractAndHandler(t *testing.T) {
	t.Parallel()

	valid := capability.MustParseContract[endpointRequest, endpointResponse]("example.valid/v1")
	var zero capability.Contract[endpointRequest, endpointResponse]
	var nilHandler capability.Handler[endpointRequest, endpointResponse]
	tests := []struct {
		name     string
		contract capability.Contract[endpointRequest, endpointResponse]
		handler  capability.Handler[endpointRequest, endpointResponse]
	}{
		{
			name:     "zero contract",
			contract: zero,
			handler: func(context.Context, endpointRequest) (endpointResponse, error) {
				return endpointResponse{}, nil
			},
		},
		{name: "nil handler", contract: valid, handler: nilHandler},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			endpoint, err := NewEndpoint(test.contract, test.handler)
			if !errors.Is(err, ErrInvalidEndpoint) {
				t.Fatalf("NewEndpoint error = %v, want ErrInvalidEndpoint", err)
			}
			if endpoint.valid() || endpoint.Definition().Valid() {
				t.Fatalf("invalid endpoint = %#v", endpoint)
			}
		})
	}
}

func TestInvokeEndpointRejectsDefinitionMismatch(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	independent := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	endpoint, err := NewEndpoint(contract, func(context.Context, endpointRequest) (endpointResponse, error) {
		return endpointResponse{}, nil
	})
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	for _, definition := range []capability.Definition{independent.Definition(), {}} {
		response, err := invokeEndpoint[endpointRequest, endpointResponse](context.Background(), endpoint, definition, endpointRequest{})
		if !errors.Is(err, ErrContractMismatch) {
			t.Fatalf("invokeEndpoint error = %v, want ErrContractMismatch", err)
		}
		if response != (endpointResponse{}) {
			t.Fatalf("response = %#v, want zero", response)
		}
	}
}

func TestInvokeEndpointRejectsZeroEndpoint(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	var endpoint Endpoint
	response, err := invokeEndpoint[endpointRequest, endpointResponse](context.Background(), endpoint, contract.Definition(), endpointRequest{})
	if !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("invokeEndpoint error = %v, want ErrInvalidEndpoint", err)
	}
	if response != (endpointResponse{}) || endpoint.Definition().Valid() || endpoint.valid() {
		t.Fatalf("zero endpoint/response = %#v / %#v", endpoint, response)
	}
}

func TestEndpointRejectsWrongErasedRequestType(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	endpoint, err := NewEndpoint(contract, func(context.Context, endpointRequest) (endpointResponse, error) {
		return endpointResponse{}, nil
	})
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	if _, err := endpoint.invoke(context.Background(), requestBox[string]{value: "wrong"}); !errors.Is(err, ErrContractMismatch) {
		t.Fatalf("erased invoke error = %v, want ErrContractMismatch", err)
	}
}

func TestInvokeEndpointRejectsWrongErasedResponseType(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	endpoint, err := NewEndpoint(contract, func(context.Context, endpointRequest) (endpointResponse, error) {
		return endpointResponse{}, nil
	})
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	endpoint.invoke = func(context.Context, any) (any, error) {
		return responseBox[string]{value: "wrong"}, nil
	}
	response, err := invokeEndpoint[endpointRequest, endpointResponse](context.Background(), endpoint, contract.Definition(), endpointRequest{})
	if !errors.Is(err, ErrContractMismatch) {
		t.Fatalf("invokeEndpoint error = %v, want ErrContractMismatch", err)
	}
	if response != (endpointResponse{}) {
		t.Fatalf("response = %#v, want zero", response)
	}
}

type endpointSemanticError string

func (e endpointSemanticError) Error() string { return "provider secret: " + string(e) }

func (e endpointSemanticError) SemanticErrorCode() string { return string(e) }

type endpointPanickingSemanticError struct{}

func (endpointPanickingSemanticError) Error() string { return "provider secret" }

func (endpointPanickingSemanticError) SemanticErrorCode() string { panic("provider secret") }

type endpointPanickingAsError struct{}

func (endpointPanickingAsError) Error() string { return "provider secret" }

func (endpointPanickingAsError) As(any) bool { panic("provider secret") }

func FuzzEndpointSemanticErrorBoundary(f *testing.F) {
	f.Add("invalid_recipient")
	f.Add("temporarily_unavailable")
	f.Add("InvalidRecipient")
	f.Add("")

	contract := capability.MustParseContractWithSemanticErrors[endpointRequest, endpointResponse]("example.semantic/v1", "invalid_recipient")
	endpoint, err := NewEndpoint(contract, func(_ context.Context, request endpointRequest) (endpointResponse, error) {
		return endpointResponse{Value: "must not escape"}, endpointSemanticError(request.Value)
	})
	if err != nil {
		f.Fatalf("NewEndpoint: %v", err)
	}
	f.Fuzz(func(t *testing.T, code string) {
		response, err := invokeEndpoint[endpointRequest, endpointResponse](context.Background(), endpoint, contract.Definition(), endpointRequest{Value: code})
		if response != (endpointResponse{}) {
			t.Fatalf("response = %#v, want zero", response)
		}
		if code == "invalid_recipient" {
			if semantic, ok := errors.AsType[*SemanticError](err); !ok || semantic.SemanticErrorCode() != code {
				t.Fatalf("declared semantic error = %#v / %v", semantic, err)
			}
			return
		}
		if !errors.Is(err, errInvalidProviderSemanticError) {
			t.Fatalf("unsafe semantic code %q returned %v", code, err)
		}
	})
}
