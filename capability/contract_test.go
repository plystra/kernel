package capability_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/plystra/kernel/capability"
)

type contractRequest struct {
	Value string
}

type contractResponse struct {
	Value string
}

func TestNewContract(t *testing.T) {
	t.Parallel()

	identifier, err := capability.ParseIdentifier("example.operation/v1")
	if err != nil {
		t.Fatalf("ParseIdentifier: %v", err)
	}
	contract, err := capability.NewContract[contractRequest, contractResponse](identifier)
	if err != nil {
		t.Fatalf("NewContract: %v", err)
	}
	if !contract.Valid() || contract.Identifier() != identifier {
		t.Fatalf("contract = %#v", contract)
	}
	if !contract.Definition().Valid() || contract.Definition().Identifier() != identifier {
		t.Fatalf("definition = %#v", contract.Definition())
	}

	copyOfContract := contract
	if copyOfContract.Definition() != contract.Definition() {
		t.Fatal("copy did not preserve the shared definition")
	}
}

func TestIndependentContractsWithSameIdentifierHaveDistinctDefinitions(t *testing.T) {
	t.Parallel()

	identifier, err := capability.ParseIdentifier("example.operation/v1")
	if err != nil {
		t.Fatalf("ParseIdentifier: %v", err)
	}
	first, err := capability.NewContract[contractRequest, contractResponse](identifier)
	if err != nil {
		t.Fatalf("NewContract first: %v", err)
	}
	second, err := capability.NewContract[contractRequest, contractResponse](identifier)
	if err != nil {
		t.Fatalf("NewContract second: %v", err)
	}
	if first.Definition() == second.Definition() {
		t.Fatal("independent declarations shared an opaque definition")
	}
	if first.Identifier() != second.Identifier() {
		t.Fatal("same identifier did not round trip")
	}
}

func TestNewContractRejectsZeroIdentifier(t *testing.T) {
	t.Parallel()

	contract, err := capability.NewContract[contractRequest, contractResponse](capability.Identifier{})
	if !errors.Is(err, capability.ErrInvalidContract) {
		t.Fatalf("NewContract error = %v, want ErrInvalidContract", err)
	}
	if contract.Valid() || contract.Identifier().String() != "" || contract.Definition().Valid() {
		t.Fatalf("invalid contract = %#v", contract)
	}
}

func TestMustParseContract(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[contractRequest, contractResponse]("example.operation/v2")
	if !contract.Valid() || contract.Identifier().String() != "example.operation/v2" {
		t.Fatalf("contract = %#v", contract)
	}
}

func TestMustParseContractPanicsForInvalidIdentifier(t *testing.T) {
	t.Parallel()

	defer func() {
		value := recover()
		if value == nil {
			t.Fatal("MustParseContract did not panic")
		}
		panicError, ok := value.(error)
		if !ok {
			t.Fatalf("panic = %#v, want error", value)
		}
		if message := panicError.Error(); !strings.Contains(message, "declare capability contract") || !strings.Contains(message, "invalid capability identifier") {
			t.Fatalf("panic = %q", message)
		}
	}()
	_ = capability.MustParseContract[contractRequest, contractResponse]("invalid")
}

func TestHandlerSupportsFunctionsAndMethodValues(t *testing.T) {
	t.Parallel()

	service := contractService{prefix: "handled:"}
	tests := []struct {
		name    string
		handler capability.Handler[contractRequest, contractResponse]
		want    string
	}{
		{
			name: "function",
			handler: func(_ context.Context, request contractRequest) (contractResponse, error) {
				return contractResponse(request), nil
			},
			want: "value",
		},
		{name: "method value", handler: service.Handle, want: "handled:value"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response, err := test.handler(context.Background(), contractRequest{Value: "value"})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}
			if response.Value != test.want {
				t.Fatalf("response = %#v, want %q", response, test.want)
			}
		})
	}

	var nilHandler capability.Handler[contractRequest, contractResponse]
	if nilHandler != nil {
		t.Fatal("zero Handler is not nil")
	}
}

type contractService struct {
	prefix string
}

func (s contractService) Handle(_ context.Context, request contractRequest) (contractResponse, error) {
	return contractResponse{Value: s.prefix + request.Value}, nil
}
