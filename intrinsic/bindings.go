package intrinsic

import (
	"context"
	"errors"
	"fmt"

	"github.com/plystra/kernel/capability/catalog"
	"github.com/plystra/kernel/invocation"
)

// ErrBindings reports invalid intrinsic build provenance or an internal
// endpoint/catalog contract mismatch.
var ErrBindings = errors.New("construct intrinsic Kernel capability bindings")

// BindingOptions supplies immutable Kernel build provenance. ModuleVersion is
// a canonical Go Module version when available. Development builds without a
// version require a safe non-secret BuildIdentity.
type BindingOptions struct {
	ModuleVersion string
	BuildIdentity string
}

// NewBindings constructs every reserved intrinsic endpoint in canonical ID
// order. The returned bindings contain no ordinary Plugin ID and require no
// ordinary provider.
func NewBindings(options BindingOptions) ([]invocation.Binding, error) {
	build, err := invocation.NewModuleBuild(ModulePath, options.ModuleVersion, options.BuildIdentity)
	if err != nil {
		return nil, fmt.Errorf("%w: build provenance: %w", ErrBindings, err)
	}

	healthEndpoint, err := invocation.NewEndpoint(healthContract, func(context.Context, HealthRequest) (HealthResponse, error) {
		return HealthResponse{Status: HealthStatusHealthy}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: kernel.health/v1 endpoint: %w", ErrBindings, err)
	}
	health, err := newBinding(build, healthEndpoint)
	if err != nil {
		return nil, err
	}

	version := options.ModuleVersion
	if version == "" {
		version = "devel"
	}
	infoResponse := InfoResponse{
		AssemblyAPI:   "v1",
		KernelModule:  ModulePath,
		KernelVersion: version,
	}
	infoEndpoint, err := invocation.NewEndpoint(infoContract, func(context.Context, InfoRequest) (InfoResponse, error) {
		return infoResponse, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: kernel.info/v1 endpoint: %w", ErrBindings, err)
	}
	info, err := newBinding(build, infoEndpoint)
	if err != nil {
		return nil, err
	}

	return []invocation.Binding{health, info}, nil
}

func newBinding(build invocation.ModuleBuild, endpoint invocation.Endpoint) (invocation.Binding, error) {
	identifier := endpoint.Definition().Identifier()
	definition, exists := catalog.Lookup(identifier)
	if !exists {
		return invocation.Binding{}, fmt.Errorf("%w: catalog omits %s", ErrBindings, identifier.String())
	}
	binding, err := invocation.NewBinding(invocation.BindingOptions{
		ProviderKind:    invocation.ProviderKindKernel,
		ProviderPackage: ProviderPackage,
		ProviderBuild:   build,
		SelectionReason: invocation.SelectionReasonIntrinsic,
		SchemaDigest:    definition.SchemaDigest(),
	}, endpoint)
	if err != nil {
		return invocation.Binding{}, fmt.Errorf("%w: bind %s: %w", ErrBindings, identifier.String(), err)
	}
	return binding, nil
}
