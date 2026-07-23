package lifecycle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/plystra/kernel/lifecycle"
)

func TestBindingRequiresConstructorAndInstance(t *testing.T) {
	t.Parallel()

	constructor := "example.com/acme/lifecycle.New"
	instance := &testLifecycleInstance{}
	binding, err := lifecycle.NewBinding(constructor, instance)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	if binding.Constructor() != constructor {
		t.Fatalf("Constructor = %s, want %s", binding.Constructor(), constructor)
	}
	if copied := binding; copied.Constructor() != constructor {
		t.Fatalf("copied binding = %#v", copied)
	}

	var typedNil *testLifecycleInstance
	for _, test := range []struct {
		name        string
		constructor string
		instance    lifecycle.Instance
	}{
		{name: "empty constructor", instance: instance},
		{name: "malformed constructor", constructor: "example.com/acme/lifecycle", instance: instance},
		{name: "unexported constructor", constructor: "example.com/acme/lifecycle.new", instance: instance},
		{name: "invalid import path", constructor: "../lifecycle.New", instance: instance},
		{name: "nil instance", constructor: constructor},
		{name: "typed nil instance", constructor: constructor, instance: typedNil},
	} {
		invalid, err := lifecycle.NewBinding(test.constructor, test.instance)
		if !errors.Is(err, lifecycle.ErrInvalidBinding) || invalid.Constructor() != "" {
			t.Fatalf("%s NewBinding = %#v, %v", test.name, invalid, err)
		}
	}

	var zero lifecycle.Binding
	if zero.Constructor() != "" {
		t.Fatalf("zero binding Constructor = %s", zero.Constructor())
	}
}

type testLifecycleInstance struct {
	start func(context.Context) error
	stop  func(context.Context) error
}

func (instance *testLifecycleInstance) Start(ctx context.Context) error {
	if instance.start == nil {
		return nil
	}
	return instance.start(ctx)
}

func (instance *testLifecycleInstance) Stop(ctx context.Context) error {
	if instance.stop == nil {
		return nil
	}
	return instance.stop(ctx)
}
