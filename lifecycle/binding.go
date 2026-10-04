package lifecycle

import (
	"context"
	"errors"
	"reflect"

	"github.com/plystra/kernel/invocation"
)

// ErrInvalidBinding reports an invalid lifecycle owner, constructor, or instance.
var ErrInvalidBinding = errors.New("invalid lifecycle binding")

// Instance is the optional lifecycle surface implemented by an in-process
// concrete Implementation or Resource that owns resources requiring startup and
// shutdown. Constructors only assemble values; acquisition and background work
// belong in Start. Stop must tolerate never-started and partially started
// values and be safe to retry after it reports failure.
type Instance interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// Binding joins one typed owner and its constructor provenance to a concrete
// lifecycle instance. Generated assembly determines dependency-first order.
type Binding struct {
	owner       invocation.LifecycleOwner
	constructor string
	instance    Instance
}

// NewBinding validates one already-resolved Implementation lifecycle instance.
func NewBinding(constructor string, instance Instance) (Binding, error) {
	owner, err := invocation.NewImplementationOwner(constructor)
	if err != nil {
		return Binding{}, ErrInvalidBinding
	}
	binding := Binding{owner: owner, constructor: constructor, instance: instance}
	if !binding.valid() {
		return Binding{}, ErrInvalidBinding
	}
	return binding, nil
}

// NewResourceBinding validates one already-constructed named Resource instance.
// The exact configured name owns lifecycle; the provider is provenance and may
// construct several separately managed instances. Non-nil partial constructor
// results must also be bound for cleanup; nil and typed-nil values are rejected.
func NewResourceBinding(instanceName, providerConstructor string, instance Instance) (Binding, error) {
	owner, err := invocation.NewResourceOwner(instanceName)
	if err != nil {
		return Binding{}, ErrInvalidBinding
	}
	binding := Binding{owner: owner, constructor: providerConstructor, instance: instance}
	if !binding.valid() {
		return Binding{}, ErrInvalidBinding
	}
	return binding, nil
}

// Constructor returns the exact Implementation or Resource-provider constructor
// provenance. It never synthesizes a constructor from a Resource instance name.
func (b Binding) Constructor() string {
	if !b.valid() {
		return ""
	}
	return b.constructor
}

// ResourceInstanceName returns the exact configured Resource name, or empty for
// an Implementation or invalid binding.
func (b Binding) ResourceInstanceName() string {
	return b.Owner().ResourceInstanceName()
}

// Owner returns the typed lifecycle identity, or zero for an invalid binding.
func (b Binding) Owner() invocation.LifecycleOwner {
	if !b.valid() {
		return invocation.LifecycleOwner{}
	}
	return b.owner
}

func (b Binding) valid() bool {
	constructor, err := invocation.NewImplementationOwner(b.constructor)
	return err == nil && b.owner.Valid() && !nilInstance(b.instance) &&
		(b.owner.Kind() == invocation.LifecycleOwnerResource || b.owner == constructor)
}

func (b Binding) description() string {
	if b.owner.Kind() == invocation.LifecycleOwnerResource {
		return b.owner.String() + " (provider " + b.constructor + ")"
	}
	return b.owner.String()
}

func nilInstance(instance Instance) bool {
	if instance == nil {
		return true
	}
	value := reflect.ValueOf(instance)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
