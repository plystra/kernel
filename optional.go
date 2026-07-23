// Package plystra exposes the minimal public Kernel types used directly by
// authored Plystra implementation packages and generated assembly.
package plystra

// Optional carries an Interface dependency whose compatible Implementation may
// be absent from the resolved application. Its zero value is unavailable.
// Generated assembly constructs an available value only when that Interface is
// otherwise selected.
type Optional[T any] struct {
	value     T
	available bool
}

// NewOptional constructs an available optional dependency. Generated assembly
// supplies the already resolved typed Interface value.
func NewOptional[T any](value T) Optional[T] {
	return Optional[T]{value: value, available: true}
}

// Available reports whether generated assembly supplied a compatible resolved
// Interface value.
func (o Optional[T]) Available() bool { return o.available }

// Value returns the supplied typed Interface value. It panics with a fixed safe
// message when called while unavailable; callers must check Available first.
func (o Optional[T]) Value() T {
	if !o.available {
		panic("plystra: optional Interface is unavailable")
	}
	return o.value
}
