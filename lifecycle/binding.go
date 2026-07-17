package lifecycle

import (
	"context"
	"errors"
	"reflect"

	"github.com/plystra/kernel/plugin"
)

// ErrInvalidBinding reports an invalid Plugin ID or provider lifecycle.
var ErrInvalidBinding = errors.New("invalid provider lifecycle binding")

// Provider is the optional lifecycle surface implemented by an in-process
// plugin that owns resources requiring explicit startup and shutdown. Stop
// must tolerate partially completed startup and be safe to retry after it
// reports failure.
type Provider interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// Binding joins one concrete Plugin ID to its optional lifecycle provider.
// Generated assembly determines binding order before the Kernel starts.
type Binding struct {
	pluginID plugin.ID
	provider Provider
}

// NewBinding validates one already-resolved lifecycle provider.
func NewBinding(pluginID plugin.ID, provider Provider) (Binding, error) {
	binding := Binding{pluginID: pluginID, provider: provider}
	if !binding.valid() {
		return Binding{}, ErrInvalidBinding
	}
	return binding, nil
}

// PluginID returns the concrete plugin that owns the lifecycle.
func (b Binding) PluginID() plugin.ID {
	if !b.valid() {
		return plugin.ID{}
	}
	return b.pluginID
}

func (b Binding) valid() bool {
	return b.pluginID.String() != "" && !nilProvider(b.provider)
}

func nilProvider(provider Provider) bool {
	if provider == nil {
		return true
	}
	value := reflect.ValueOf(provider)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
