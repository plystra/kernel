package invocation

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"github.com/plystra/kernel/capability"
	"golang.org/x/mod/module"
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

// BindingKind distinguishes Kernel-owned intrinsic endpoints from ordinary
// already-selected Implementation endpoints. It does not perform or influence
// Implementation selection.
type BindingKind string

const (
	BindingKindIntrinsic      BindingKind = "intrinsic"
	BindingKindImplementation BindingKind = "implementation"
)

// String returns the stable binding-kind representation.
func (k BindingKind) String() string {
	if !k.Valid() {
		return ""
	}
	return string(k)
}

// Valid reports whether the kind identifies one supported binding owner.
func (k BindingKind) Valid() bool {
	return k == BindingKindIntrinsic || k == BindingKindImplementation
}

// SelectionReason records why generated resolution chose one Implementation.
// It is immutable runtime provenance and never performs selection.
type SelectionReason string

const (
	// SelectionReasonIntrinsic identifies a Kernel-owned intrinsic endpoint.
	SelectionReasonIntrinsic SelectionReason = "intrinsic"
	// SelectionReasonUniqueCompatible identifies the only compatible ordinary
	// Implementation visible for a required Interface.
	SelectionReasonUniqueCompatible SelectionReason = "unique-compatible"
	// SelectionReasonExplicit identifies an ordinary Implementation selected by
	// an explicit application interfaces.use declaration.
	SelectionReasonExplicit SelectionReason = "explicit"
)

// String returns the stable selection-reason representation.
func (r SelectionReason) String() string {
	if !r.Valid() {
		return ""
	}
	return string(r)
}

// Valid reports whether the reason is one supported resolved selection.
func (r SelectionReason) Valid() bool {
	return r == SelectionReasonIntrinsic || r == SelectionReasonUniqueCompatible || r == SelectionReasonExplicit
}

// BindingOptions is the generated, already-resolved metadata for one endpoint.
type BindingOptions struct {
	Kind            BindingKind
	Constructor     string
	ModuleBuild     ModuleBuild
	SelectionReason SelectionReason
	ContractDigest  [sha256.Size]byte
	Policy          Policy
}

// Binding joins one selected Implementation constructor, its module
// provenance, and contract digest to its executable endpoint. Intrinsic
// bindings deliberately have no constructor symbol.
type Binding struct {
	kind            BindingKind
	constructor     string
	moduleBuild     ModuleBuild
	selectionReason SelectionReason
	contractDigest  [sha256.Size]byte
	policy          Policy
	endpoint        Endpoint
}

// NewBinding validates one already-resolved executable endpoint. An ordinary
// binding requires the exact selected constructor symbol; an intrinsic binding
// must not have one. Every binding requires immutable module build provenance
// for diagnostics and a complete supported policy, including explicit defaults.
func NewBinding(options BindingOptions, endpoint Endpoint) (Binding, error) {
	binding := Binding{
		kind:            options.Kind,
		constructor:     options.Constructor,
		moduleBuild:     options.ModuleBuild,
		selectionReason: options.SelectionReason,
		contractDigest:  options.ContractDigest,
		policy:          options.Policy,
		endpoint:        endpoint,
	}
	if !binding.policy.valid() {
		return Binding{}, errors.Join(ErrInvalidBinding, ErrInvalidPolicy)
	}
	if !binding.valid() {
		return Binding{}, ErrInvalidBinding
	}
	return binding, nil
}

// InterfaceID returns the exact provider-independent Interface identity.
func (b Binding) InterfaceID() capability.Identifier {
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

// Kind returns whether the selected endpoint is intrinsic or belongs to one
// ordinary Implementation constructor.
func (b Binding) Kind() BindingKind {
	if !b.valid() {
		return ""
	}
	return b.kind
}

// Constructor returns the exact selected fully qualified constructor symbol,
// or an empty string for a Kernel-owned intrinsic endpoint.
func (b Binding) Constructor() string {
	if !b.valid() {
		return ""
	}
	return b.constructor
}

// ModuleBuild returns the selected Implementation's Go Module provenance.
func (b Binding) ModuleBuild() ModuleBuild {
	if !b.valid() {
		return ModuleBuild{}
	}
	return b.moduleBuild
}

// SelectionReason returns why generated resolution chose this implementation.
func (b Binding) SelectionReason() SelectionReason {
	if !b.valid() {
		return ""
	}
	return b.selectionReason
}

// ContractDigest returns the selected Interface's canonical SHA-256 contract
// digest.
func (b Binding) ContractDigest() [sha256.Size]byte {
	if !b.valid() {
		return [sha256.Size]byte{}
	}
	return b.contractDigest
}

// MaximumConcurrencyLimit bounds the supported per-binding admission limit.
const MaximumConcurrencyLimit = 65_536

// ConcurrencyLimit returns the immutable bound on admitted attempts, or zero
// for an invalid binding. The Kernel does not choose an assembly default.
func (b Binding) ConcurrencyLimit() int {
	if !b.valid() {
		return 0
	}
	return b.policy.ConcurrencyLimit
}

// Policy returns a copy of the exact compiled policy, or zero for an invalid binding.
func (b Binding) Policy() Policy {
	if !b.valid() {
		return Policy{}
	}
	return b.policy
}

func (b Binding) valid() bool {
	if !b.policy.valid() {
		return false
	}
	if !b.endpoint.valid() || !b.moduleBuild.Valid() || !b.selectionReason.Valid() || b.contractDigest == [sha256.Size]byte{} {
		return false
	}
	switch b.kind {
	case BindingKindIntrinsic:
		return b.constructor == "" && b.selectionReason == SelectionReasonIntrinsic
	case BindingKindImplementation:
		return validConstructorSymbol(b.moduleBuild.ModulePath(), b.constructor) && b.selectionReason != SelectionReasonIntrinsic
	default:
		return false
	}
}

func validConstructorSymbol(modulePath, symbol string) bool {
	separator := strings.LastIndexByte(symbol, '.')
	if separator <= 0 || separator == len(symbol)-1 {
		return false
	}
	packagePath, functionName := symbol[:separator], symbol[separator+1:]
	if module.CheckImportPath(packagePath) != nil || !token.IsIdentifier(functionName) || !ast.IsExported(functionName) {
		return false
	}
	return packagePath == modulePath || strings.HasPrefix(packagePath, modulePath+"/")
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
		identifier := binding.InterfaceID()
		if _, duplicate := entries[identifier]; duplicate {
			return Catalog{}, fmt.Errorf("%w: %w for %s", ErrInvalidCatalog, ErrDuplicateBinding, identifier)
		}
		entries[identifier] = binding
		ordered[index] = binding
	}
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].InterfaceID().String() < ordered[right].InterfaceID().String()
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
