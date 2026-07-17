package capability_test

import (
	"context"
	"errors"
	"slices"
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
	var constructor func(capability.Identifier) (capability.Contract[contractRequest, contractResponse], error) = capability.NewContract[contractRequest, contractResponse]
	contract, err := constructor(identifier)
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

func TestNewContractNormalizesSemanticErrors(t *testing.T) {
	t.Parallel()

	identifier, err := capability.ParseIdentifier("example.semantic/v1")
	if err != nil {
		t.Fatalf("ParseIdentifier: %v", err)
	}
	input := []string{"temporarily_unavailable", "invalid_recipient"}
	contract, err := capability.NewContractWithSemanticErrors[contractRequest, contractResponse](identifier, input...)
	if err != nil {
		t.Fatalf("NewContract: %v", err)
	}
	want := []string{"invalid_recipient", "temporarily_unavailable"}
	if got := contract.SemanticErrors(); !slices.Equal(got, want) {
		t.Fatalf("SemanticErrors = %q, want %q", got, want)
	}
	if got := contract.Definition().SemanticErrors(); !slices.Equal(got, want) {
		t.Fatalf("Definition.SemanticErrors = %q, want %q", got, want)
	}
	if !contract.DeclaresSemanticError("invalid_recipient") ||
		!contract.Definition().DeclaresSemanticError("temporarily_unavailable") ||
		contract.DeclaresSemanticError("unknown_failure") ||
		contract.DeclaresSemanticError("InvalidRecipient") {
		t.Fatal("semantic error declaration lookup is incorrect")
	}

	input[0] = "changed_input"
	returned := contract.SemanticErrors()
	returned[0] = "changed_output"
	if got := contract.SemanticErrors(); !slices.Equal(got, want) {
		t.Fatalf("semantic declarations exposed mutable storage: %q", got)
	}
	copyOfContract := contract
	if copyOfContract.Definition() != contract.Definition() || !slices.Equal(copyOfContract.SemanticErrors(), want) {
		t.Fatal("contract copy did not preserve semantic declarations")
	}
}

func TestNewContractRejectsInvalidSemanticErrors(t *testing.T) {
	t.Parallel()

	identifier, err := capability.ParseIdentifier("example.semantic/v1")
	if err != nil {
		t.Fatalf("ParseIdentifier: %v", err)
	}
	for _, codes := range [][]string{
		{""},
		{"InvalidRecipient"},
		{"invalid-recipient"},
		{"invalid__recipient"},
		{"invalid_recipient_"},
		{"provider.invalid_recipient"},
		{strings.Repeat("a", capability.MaximumSemanticErrorCodeSize+1)},
		{"invalid_recipient", "invalid_recipient"},
	} {
		contract, err := capability.NewContractWithSemanticErrors[contractRequest, contractResponse](identifier, codes...)
		if !errors.Is(err, capability.ErrInvalidContract) {
			t.Fatalf("NewContract(%q) error = %v, want ErrInvalidContract", codes, err)
		}
		if contract.Valid() || contract.Definition().Valid() || len(contract.SemanticErrors()) != 0 {
			t.Fatalf("NewContract(%q) returned data: %#v", codes, contract)
		}
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

	var parser func(string) capability.Contract[contractRequest, contractResponse] = capability.MustParseContract[contractRequest, contractResponse]
	plain := parser("example.plain/v1")
	if !plain.Valid() || len(plain.SemanticErrors()) != 0 {
		t.Fatalf("plain contract = %#v", plain)
	}
	contract := capability.MustParseContractWithSemanticErrors[contractRequest, contractResponse]("example.operation/v2", "invalid_request")
	if !contract.Valid() || contract.Identifier().String() != "example.operation/v2" || !contract.DeclaresSemanticError("invalid_request") {
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

func TestMustParseContractPanicsForInvalidSemanticError(t *testing.T) {
	t.Parallel()

	defer func() {
		value := recover()
		panicError, ok := value.(error)
		if !ok || !errors.Is(panicError, capability.ErrInvalidContract) || !strings.Contains(panicError.Error(), "semantic error code") {
			t.Fatalf("panic = %#v, want invalid semantic error", value)
		}
	}()
	_ = capability.MustParseContractWithSemanticErrors[contractRequest, contractResponse]("example.operation/v1", "InvalidError")
}

func TestValidSemanticErrorCode(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"a", "invalid_recipient", "failure2", strings.Repeat("a", capability.MaximumSemanticErrorCodeSize)} {
		if !capability.ValidSemanticErrorCode(code) {
			t.Errorf("ValidSemanticErrorCode(%q) = false", code)
		}
	}
	for _, code := range []string{"", "Invalid", "invalid-error", "invalid__error", "invalid_error_", "invalid.error", strings.Repeat("a", capability.MaximumSemanticErrorCodeSize+1)} {
		if capability.ValidSemanticErrorCode(code) {
			t.Errorf("ValidSemanticErrorCode(%q) = true", code)
		}
	}
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

func FuzzNewContractSemanticError(f *testing.F) {
	f.Add("invalid_recipient")
	f.Add("InvalidRecipient")
	f.Add("")

	identifier, err := capability.ParseIdentifier("example.semantic/v1")
	if err != nil {
		f.Fatalf("ParseIdentifier: %v", err)
	}
	f.Fuzz(func(t *testing.T, code string) {
		contract, err := capability.NewContractWithSemanticErrors[contractRequest, contractResponse](identifier, code)
		if !capability.ValidSemanticErrorCode(code) {
			if !errors.Is(err, capability.ErrInvalidContract) || contract.Valid() {
				t.Fatalf("invalid code %q returned %#v, %v", code, contract, err)
			}
			return
		}
		if err != nil || !contract.Valid() || !contract.DeclaresSemanticError(code) || !slices.Equal(contract.SemanticErrors(), []string{code}) {
			t.Fatalf("valid code %q returned %#v, %v", code, contract, err)
		}
	})
}

func (s contractService) Handle(_ context.Context, request contractRequest) (contractResponse, error) {
	return contractResponse{Value: s.prefix + request.Value}, nil
}
