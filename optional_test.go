package plystra_test

import (
	"testing"

	plystra "github.com/plystra/kernel"
)

type optionalOperation interface {
	Name() string
}

type optionalService struct {
	name string
}

func (s *optionalService) Name() string { return s.name }

func TestOptionalAvailableValue(t *testing.T) {
	t.Parallel()

	service := &optionalService{name: "audit"}
	optional := plystra.NewOptional[optionalOperation](service)
	if !optional.Available() {
		t.Fatal("NewOptional value is unavailable")
	}
	if got := optional.Value(); got != service || got.Name() != "audit" {
		t.Fatalf("Value = %#v", got)
	}
}

func TestOptionalZeroValueIsUnavailable(t *testing.T) {
	t.Parallel()

	var optional plystra.Optional[optionalOperation]
	if optional.Available() {
		t.Fatal("zero Optional is available")
	}
	defer func() {
		if recovered := recover(); recovered != "plystra: optional Interface is unavailable" {
			t.Fatalf("Value panic = %#v", recovered)
		}
	}()
	_ = optional.Value()
}

func TestOptionalCanCarryAvailableNilInterface(t *testing.T) {
	t.Parallel()

	var value optionalOperation
	optional := plystra.NewOptional[optionalOperation](value)
	if !optional.Available() || optional.Value() != nil {
		t.Fatalf("nil Optional = available %t value %#v", optional.Available(), optional.Value())
	}
}
