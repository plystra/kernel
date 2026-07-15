package invocation

import (
	"errors"
	"reflect"
	"testing"

	"github.com/plystra/kernel/audit"
	"github.com/plystra/kernel/capability"
)

type handleRequest struct {
	Value string
}

type handleResponse struct {
	Value string
}

func TestNewHandleBindsExactContractAndAvailability(t *testing.T) {
	t.Parallel()

	scope := testHandleScope(t)
	contract := capability.MustParseContract[handleRequest, handleResponse]("example.handle/v1")
	for _, available := range []bool{false, true} {
		handle, err := NewHandle(scope, contract, available)
		if err != nil {
			t.Fatalf("NewHandle(%t): %v", available, err)
		}
		if !handle.valid() || handle.scope.dispatcher != scope.dispatcher || handle.scope.caller != scope.caller {
			t.Fatalf("NewHandle(%t) = %#v", available, handle)
		}
		if handle.definition != contract.Definition() || handle.Identifier() != contract.Identifier() {
			t.Fatalf("NewHandle(%t) definition = %#v", available, handle.definition)
		}
		if handle.Available() != available {
			t.Fatalf("NewHandle(%t) Available = %t", available, handle.Available())
		}
	}
}

func TestHandlePreservesOpaqueDeclarationIdentity(t *testing.T) {
	t.Parallel()

	scope := testHandleScope(t)
	first := capability.MustParseContract[handleRequest, handleResponse]("example.handle/v1")
	second := capability.MustParseContract[handleRequest, handleResponse]("example.handle/v1")
	firstHandle, err := NewHandle(scope, first, true)
	if err != nil {
		t.Fatalf("NewHandle(first): %v", err)
	}
	secondHandle, err := NewHandle(scope, second, true)
	if err != nil {
		t.Fatalf("NewHandle(second): %v", err)
	}
	if firstHandle.Identifier() != secondHandle.Identifier() {
		t.Fatal("same capability identifier did not round trip")
	}
	if firstHandle.definition == secondHandle.definition {
		t.Fatal("independent contract declarations shared handle identity")
	}
	copyOfFirst := firstHandle
	if copyOfFirst.definition != firstHandle.definition || copyOfFirst.scope != firstHandle.scope {
		t.Fatal("handle copy changed its bound scope or contract identity")
	}
}

func TestNewHandleRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	validScope := testHandleScope(t)
	validContract := capability.MustParseContract[handleRequest, handleResponse]("example.handle/v1")
	var zeroScope Scope
	var zeroContract capability.Contract[handleRequest, handleResponse]
	for _, test := range []struct {
		name     string
		scope    Scope
		contract capability.Contract[handleRequest, handleResponse]
	}{
		{name: "zero scope", scope: zeroScope, contract: validContract},
		{name: "zero contract", scope: validScope, contract: zeroContract},
	} {
		handle, err := NewHandle(test.scope, test.contract, true)
		if !errors.Is(err, ErrInvalidHandle) {
			t.Fatalf("%s error = %v, want ErrInvalidHandle", test.name, err)
		}
		if handle.valid() || handle.Available() || handle.Identifier().String() != "" {
			t.Fatalf("%s handle = %#v", test.name, handle)
		}
	}
	var zero Handle[handleRequest, handleResponse]
	if zero.valid() || zero.Available() || zero.Identifier().String() != "" {
		t.Fatalf("zero handle = %#v", zero)
	}
}

func TestHandleExposesNoInvocationOrMutableState(t *testing.T) {
	t.Parallel()

	handleType := reflect.TypeFor[Handle[handleRequest, handleResponse]]()
	if _, exists := handleType.MethodByName("Invoke"); exists {
		t.Fatal("Handle exposed invocation before governance was bound")
	}
	for index := range handleType.NumField() {
		if field := handleType.Field(index); field.IsExported() {
			t.Fatalf("Handle field %q exposes mutable runtime state", field.Name)
		}
	}
}

func testHandleScope(t *testing.T) Scope {
	t.Helper()

	dispatcher := newTestDispatcher(t)
	scope, err := dispatcher.Scope(audit.NewKernelCallerIdentity())
	if err != nil {
		t.Fatalf("Scope: %v", err)
	}
	return scope
}
