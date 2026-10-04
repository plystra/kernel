package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/plystra/kernel/invocation"
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
	if binding.Constructor() != constructor || binding.ResourceInstanceName() != "" || binding.Owner().Constructor() != constructor || binding.Owner().Kind() != invocation.LifecycleOwnerImplementation {
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

func TestResourceBindingPreservesNameAndProvider(t *testing.T) {
	const provider = "my-app/database.New"
	instance := &testLifecycleInstance{}
	for _, name := range []string{"a", "database.primary", "db-2.read-replica", strings.Repeat("a", 128)} {
		binding, err := lifecycle.NewResourceBinding(name, provider, instance)
		if err != nil {
			t.Fatal(err)
		}
		owner, err := invocation.NewResourceOwner(name)
		if err != nil || binding.ResourceInstanceName() != name || binding.Constructor() != provider || binding.Owner() != owner || binding.Owner().Constructor() != "" {
			t.Fatalf("name or provenance changed: %s, %s, %v", binding.ResourceInstanceName(), binding.Constructor(), binding.Owner())
		}
		otherProvider := resourceBinding(t, name, "example.com/other.New", instance)
		otherName := resourceBinding(t, "other", provider, instance)
		if binding.Owner() != otherProvider.Owner() || binding.Owner() == otherName.Owner() {
			t.Fatal("Resource identity depends on provider or ignores name")
		}
	}
	var typedNil *testLifecycleInstance
	for _, test := range []struct {
		name, provider string
		instance       lifecycle.Instance
	}{
		{"", provider, instance}, {"Database.primary", provider, instance},
		{"db..primary", provider, instance}, {"db--primary", provider, instance},
		{"db.primary-", provider, instance}, {"db.2", provider, instance},
		{strings.Repeat("a", 129), provider, instance}, {"db\u00e9", provider, instance},
		{"private name", provider, instance}, {"database.primary", "private provider", instance},
		{"database.primary", "database.primary", instance}, {"database.primary", "../db.New", instance},
		{"database.primary", "my-app/db.new", instance}, {"database.primary", "", instance},
		{"database.primary", provider, nil}, {"database.primary", provider, typedNil},
	} {
		binding, err := lifecycle.NewResourceBinding(test.name, test.provider, test.instance)
		if !errors.Is(err, lifecycle.ErrInvalidBinding) || binding.Owner().Valid() || binding.Constructor() != "" || binding.ResourceInstanceName() != "" || strings.Contains(fmt.Sprintf("%+v", err), "private") {
			t.Fatalf("invalid Resource binding accepted or disclosed: %v", err)
		}
	}
}

func resourceBinding(t testing.TB, name, provider string, instance lifecycle.Instance) lifecycle.Binding {
	t.Helper()
	binding, err := lifecycle.NewResourceBinding(name, provider, instance)
	if err != nil {
		t.Fatalf("NewResourceBinding: %v", err)
	}
	return binding
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
