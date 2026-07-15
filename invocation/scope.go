package invocation

import (
	"errors"

	"github.com/plystra/kernel/audit"
)

// ErrInvalidScope reports an invalid Dispatcher or caller identity.
var ErrInvalidScope = errors.New("invalid capability caller scope")

// Scope is an opaque immutable caller identity bound to one Dispatcher. It
// grants no invocation surface by itself.
type Scope struct {
	dispatcher *Dispatcher
	caller     audit.CallerIdentity
}

// Scope binds one validated runtime caller to this Dispatcher. A scope may be
// created before catalog publication so generated assembly can stage caller-
// bound handles before the complete runtime snapshot becomes visible.
func (d *Dispatcher) Scope(caller audit.CallerIdentity) (Scope, error) {
	if !d.valid() || !caller.Valid() {
		return Scope{}, ErrInvalidScope
	}
	return Scope{dispatcher: d, caller: caller}, nil
}

func (s Scope) valid() bool {
	return s.dispatcher != nil && s.dispatcher.valid() && s.caller.Valid()
}
