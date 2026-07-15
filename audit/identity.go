// Package audit defines immutable identities and records owned by the Kernel's
// runtime audit boundary.
package audit

import (
	"errors"

	"github.com/plystra/kernel/plugin"
)

// ErrInvalidCallerIdentity reports an incomplete runtime caller identity.
var ErrInvalidCallerIdentity = errors.New("invalid runtime caller identity")

// CallerKind distinguishes Kernel-owned callers from concrete plugin callers.
type CallerKind string

const (
	CallerKindKernel CallerKind = "kernel"
	CallerKindPlugin CallerKind = "plugin"
)

// CallerIdentity is immutable logical provenance for the Kernel or plugin code
// making one runtime call. Module build data, Principal security context, and
// transport metadata are separate governed invocation facts.
type CallerIdentity struct {
	kind     CallerKind
	pluginID plugin.ID
}

// NewKernelCallerIdentity creates the explicit identity used by Kernel-owned
// invocation adapters.
func NewKernelCallerIdentity() CallerIdentity {
	return CallerIdentity{kind: CallerKindKernel}
}

// NewPluginCallerIdentity creates an identity for one validated concrete
// plugin caller.
func NewPluginCallerIdentity(pluginID plugin.ID) (CallerIdentity, error) {
	identity := CallerIdentity{kind: CallerKindPlugin, pluginID: pluginID}
	if !identity.Valid() {
		return CallerIdentity{}, ErrInvalidCallerIdentity
	}
	return identity, nil
}

// Kind returns whether the caller is the Kernel or a concrete plugin.
func (i CallerIdentity) Kind() CallerKind {
	if !i.Valid() {
		return ""
	}
	return i.kind
}

// PluginID returns the concrete Plugin ID, or zero for a Kernel caller.
func (i CallerIdentity) PluginID() plugin.ID {
	if !i.Valid() {
		return plugin.ID{}
	}
	return i.pluginID
}

// Valid reports whether the identity has one exact legal shape.
func (i CallerIdentity) Valid() bool {
	switch i.kind {
	case CallerKindKernel:
		return i.pluginID.String() == ""
	case CallerKindPlugin:
		return i.pluginID.String() != ""
	default:
		return false
	}
}
