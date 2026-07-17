package invocation

import (
	"errors"
	"reflect"
	"testing"

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

	dispatcher := newTestDispatcher(t)
	contract := capability.MustParseContract[handleRequest, handleResponse]("example.handle/v1")
	for _, available := range []bool{false, true} {
		handle, err := NewHandle(dispatcher, contract, available)
		if err != nil {
			t.Fatalf("NewHandle(%t): %v", available, err)
		}
		if !handle.valid() || handle.dispatcher != dispatcher {
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

	dispatcher := newTestDispatcher(t)
	first := capability.MustParseContract[handleRequest, handleResponse]("example.handle/v1")
	second := capability.MustParseContract[handleRequest, handleResponse]("example.handle/v1")
	firstHandle, err := NewHandle(dispatcher, first, true)
	if err != nil {
		t.Fatalf("NewHandle(first): %v", err)
	}
	secondHandle, err := NewHandle(dispatcher, second, true)
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
	if copyOfFirst.definition != firstHandle.definition || copyOfFirst.dispatcher != firstHandle.dispatcher {
		t.Fatal("handle copy changed its bound Dispatcher or contract identity")
	}
}

func TestNewHandleRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	validDispatcher := newTestDispatcher(t)
	validContract := capability.MustParseContract[handleRequest, handleResponse]("example.handle/v1")
	var zeroContract capability.Contract[handleRequest, handleResponse]
	for _, test := range []struct {
		name       string
		dispatcher *Dispatcher
		contract   capability.Contract[handleRequest, handleResponse]
	}{
		{name: "nil Dispatcher", contract: validContract},
		{name: "zero Dispatcher", dispatcher: &Dispatcher{}, contract: validContract},
		{name: "zero contract", dispatcher: validDispatcher, contract: zeroContract},
	} {
		handle, err := NewHandle(test.dispatcher, test.contract, true)
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

func TestHandleExposesRawInvocationWithoutMutableState(t *testing.T) {
	t.Parallel()

	handleType := reflect.TypeFor[Handle[handleRequest, handleResponse]]()
	if _, exists := handleType.MethodByName("Invoke"); !exists {
		t.Fatal("Handle omitted raw invocation")
	}
	for index := range handleType.NumField() {
		if field := handleType.Field(index); field.IsExported() {
			t.Fatalf("Handle field %q exposes mutable runtime state", field.Name)
		}
	}
}
