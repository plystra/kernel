package invocation

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"github.com/plystra/kernel/audit"
	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/plugin"
)

var (
	// ErrInvalidBinding reports incomplete or contradictory resolved endpoint
	// metadata.
	ErrInvalidBinding = errors.New("invalid resolved capability binding")
	// ErrInvalidCatalog reports an invalid executable catalog snapshot.
	ErrInvalidCatalog = errors.New("invalid capability endpoint catalog")
	// ErrDuplicateBinding reports more than one resolved endpoint for an exact
	// capability identity.
	ErrDuplicateBinding = errors.New("duplicate resolved capability binding")
)

// ProviderKind distinguishes Kernel-owned endpoints from already-selected
// plugin endpoints. It does not perform or influence provider selection.
type ProviderKind string

const (
	ProviderKindKernel ProviderKind = "kernel"
	ProviderKindPlugin ProviderKind = "plugin"
)

// BindingOptions is the generated, already-resolved metadata for one endpoint.
type BindingOptions struct {
	ProviderKind  ProviderKind
	ProviderID    plugin.ID
	ProviderBuild audit.ModuleBuild
	SchemaDigest  [sha256.Size]byte
}

// Binding joins one selected provider, its auditable module provenance, and
// schema digest to its executable endpoint.
type Binding struct {
	providerKind  ProviderKind
	providerID    plugin.ID
	providerBuild audit.ModuleBuild
	schemaDigest  [sha256.Size]byte
	endpoint      Endpoint
}

// NewBinding validates one already-resolved executable endpoint. A plugin
// binding requires its concrete Plugin ID; a Kernel binding must not have one.
// Every binding requires immutable module build provenance for runtime audit.
func NewBinding(options BindingOptions, endpoint Endpoint) (Binding, error) {
	binding := Binding{
		providerKind:  options.ProviderKind,
		providerID:    options.ProviderID,
		providerBuild: options.ProviderBuild,
		schemaDigest:  options.SchemaDigest,
		endpoint:      endpoint,
	}
	if !binding.valid() {
		return Binding{}, ErrInvalidBinding
	}
	return binding, nil
}

// Capability returns the exact provider-independent capability identity.
func (b Binding) Capability() capability.Identifier {
	if !b.valid() {
		return capability.Identifier{}
	}
	return b.endpoint.Definition().Identifier()
}

// Definition returns the endpoint's exact typed contract declaration.
func (b Binding) Definition() capability.Definition {
	if !b.valid() {
		return capability.Definition{}
	}
	return b.endpoint.Definition()
}

// ProviderKind returns whether the selected endpoint belongs to the Kernel or
// a plugin.
func (b Binding) ProviderKind() ProviderKind {
	if !b.valid() {
		return ""
	}
	return b.providerKind
}

// ProviderID returns the selected concrete Plugin ID, or the zero value for a
// Kernel-owned endpoint.
func (b Binding) ProviderID() plugin.ID {
	if !b.valid() {
		return plugin.ID{}
	}
	return b.providerID
}

// ProviderBuild returns the selected implementation's Go module provenance.
func (b Binding) ProviderBuild() audit.ModuleBuild {
	if !b.valid() {
		return audit.ModuleBuild{}
	}
	return b.providerBuild
}

// SchemaDigest returns the selected capability's canonical SHA-256 schema
// digest.
func (b Binding) SchemaDigest() [sha256.Size]byte {
	if !b.valid() {
		return [sha256.Size]byte{}
	}
	return b.schemaDigest
}

func (b Binding) valid() bool {
	if !b.endpoint.valid() || !b.providerBuild.Valid() || b.schemaDigest == [sha256.Size]byte{} {
		return false
	}
	switch b.providerKind {
	case ProviderKindKernel:
		return b.providerID.String() == ""
	case ProviderKindPlugin:
		return b.providerID.String() != ""
	default:
		return false
	}
}

type catalogState struct {
	entries map[capability.Identifier]Binding
	ordered []Binding
}

// Catalog is one immutable complete snapshot of already-resolved executable
// capability bindings.
type Catalog struct {
	state *catalogState
}

// NewCatalog validates, copies, and indexes all resolved bindings. Provider
// discovery and selection must already be complete before this call.
func NewCatalog(bindings []Binding) (Catalog, error) {
	entries := make(map[capability.Identifier]Binding, len(bindings))
	ordered := make([]Binding, len(bindings))
	for index, binding := range bindings {
		if !binding.valid() {
			return Catalog{}, fmt.Errorf("%w: binding %d: %w", ErrInvalidCatalog, index, ErrInvalidBinding)
		}
		identifier := binding.Capability()
		if _, duplicate := entries[identifier]; duplicate {
			return Catalog{}, fmt.Errorf("%w: %w for %s", ErrInvalidCatalog, ErrDuplicateBinding, identifier)
		}
		entries[identifier] = binding
		ordered[index] = binding
	}
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].Capability().String() < ordered[right].Capability().String()
	})
	return Catalog{state: &catalogState{entries: entries, ordered: ordered}}, nil
}

// Lookup returns the one resolved binding for an exact capability version.
func (c Catalog) Lookup(identifier capability.Identifier) (Binding, bool) {
	if !c.valid() {
		return Binding{}, false
	}
	binding, exists := c.state.entries[identifier]
	return binding, exists
}

// Bindings returns a defensive copy in exact capability identity order.
func (c Catalog) Bindings() []Binding {
	if !c.valid() {
		return nil
	}
	bindings := make([]Binding, len(c.state.ordered))
	copy(bindings, c.state.ordered)
	return bindings
}

func (c Catalog) valid() bool {
	return c.state != nil && c.state.entries != nil && c.state.ordered != nil
}
