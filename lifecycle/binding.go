package lifecycle

import (
	"context"
	"errors"
	"go/ast"
	"go/token"
	"reflect"
	"strings"

	"golang.org/x/mod/module"
)

// ErrInvalidBinding reports an invalid constructor symbol or Implementation
// lifecycle.
var ErrInvalidBinding = errors.New("invalid implementation lifecycle binding")

// Instance is the optional lifecycle surface implemented by an in-process
// concrete Implementation that owns resources requiring explicit startup and
// shutdown. Constructors only assemble values; acquisition and background work
// belong in Start. Stop must tolerate never-started and partially started
// values and be safe to retry after it reports failure.
type Instance interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// Binding joins one selected constructor symbol to its concrete lifecycle
// instance. Generated assembly determines binding order before the Kernel
// starts.
type Binding struct {
	constructor string
	instance    Instance
}

// NewBinding validates one already-resolved Implementation lifecycle instance.
func NewBinding(constructor string, instance Instance) (Binding, error) {
	binding := Binding{constructor: constructor, instance: instance}
	if !binding.valid() {
		return Binding{}, ErrInvalidBinding
	}
	return binding, nil
}

// Constructor returns the exact fully qualified constructor symbol that owns
// the lifecycle instance.
func (b Binding) Constructor() string {
	if !b.valid() {
		return ""
	}
	return b.constructor
}

func (b Binding) valid() bool {
	return validConstructorSymbol(b.constructor) && !nilInstance(b.instance)
}

func validConstructorSymbol(symbol string) bool {
	separator := strings.LastIndexByte(symbol, '.')
	if separator <= 0 || separator == len(symbol)-1 {
		return false
	}
	packagePath, functionName := symbol[:separator], symbol[separator+1:]
	return module.CheckImportPath(packagePath) == nil && token.IsIdentifier(functionName) && ast.IsExported(functionName)
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
