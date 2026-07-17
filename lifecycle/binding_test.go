package lifecycle_test

import (
	"context"
	"errors"
	"testing"

	"github.com/plystra/kernel/lifecycle"
	"github.com/plystra/kernel/plugin"
)

func TestBindingRequiresConcretePluginAndProvider(t *testing.T) {
	t.Parallel()

	id := lifecyclePluginID(t, "acme.lifecycle.valid")
	provider := &testLifecycleProvider{}
	binding, err := lifecycle.NewBinding(id, provider)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	if binding.PluginID() != id {
		t.Fatalf("PluginID = %s, want %s", binding.PluginID(), id)
	}
	if copied := binding; copied.PluginID() != id {
		t.Fatalf("copied binding = %#v", copied)
	}

	var typedNil *testLifecycleProvider
	for _, test := range []struct {
		name     string
		pluginID plugin.ID
		provider lifecycle.Provider
	}{
		{name: "zero Plugin ID", provider: provider},
		{name: "nil provider", pluginID: id},
		{name: "typed nil provider", pluginID: id, provider: typedNil},
	} {
		invalid, err := lifecycle.NewBinding(test.pluginID, test.provider)
		if !errors.Is(err, lifecycle.ErrInvalidBinding) || invalid.PluginID().String() != "" {
			t.Fatalf("%s NewBinding = %#v, %v", test.name, invalid, err)
		}
	}

	var zero lifecycle.Binding
	if zero.PluginID().String() != "" {
		t.Fatalf("zero binding PluginID = %s", zero.PluginID())
	}
}

type testLifecycleProvider struct {
	start func(context.Context) error
	stop  func(context.Context) error
}

func (p *testLifecycleProvider) Start(ctx context.Context) error {
	if p.start == nil {
		return nil
	}
	return p.start(ctx)
}

func (p *testLifecycleProvider) Stop(ctx context.Context) error {
	if p.stop == nil {
		return nil
	}
	return p.stop(ctx)
}

func lifecyclePluginID(t testing.TB, value string) plugin.ID {
	t.Helper()
	id, err := plugin.ParseID(value)
	if err != nil {
		t.Fatalf("ParseID(%q): %v", value, err)
	}
	return id
}
