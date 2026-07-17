package invocation

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/plugin"
)

func TestProviderKindsAreClosedAndStable(t *testing.T) {
	t.Parallel()

	for kind, want := range map[ProviderKind]string{
		ProviderKindKernel: "kernel",
		ProviderKindPlugin: "plugin",
	} {
		if !kind.Valid() || kind.String() != want {
			t.Fatalf("ProviderKind %q = %q, valid %t", kind, kind.String(), kind.Valid())
		}
	}
	for _, kind := range []ProviderKind{"", "remote", "PLUGIN"} {
		if kind.Valid() || kind.String() != "" {
			t.Fatalf("invalid ProviderKind %q was accepted", kind)
		}
	}
}

func TestCatalogCopiesResolvedPluginBindings(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("email.send/v1")
	endpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	providerID := mustPluginID(t, "acme.email.smtp")
	providerBuild := mustModuleBuild(t, "github.com/acme/email", "v1.4.2", "git:0123456789abcdef")
	digest := sha256.Sum256([]byte("email.send/v1 schema"))
	binding, err := NewBinding(BindingOptions{
		ProviderKind:  ProviderKindPlugin,
		ProviderID:    providerID,
		ProviderBuild: providerBuild,
		SchemaDigest:  digest,
	}, endpoint)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	if binding.Capability() != contract.Identifier() || binding.Definition() != contract.Definition() || binding.ProviderKind() != ProviderKindPlugin || binding.ProviderID() != providerID || binding.ProviderBuild() != providerBuild || binding.SchemaDigest() != digest {
		t.Fatalf("binding accessors = %#v", binding)
	}

	source := []Binding{binding}
	catalog, err := NewCatalog(source)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	source[0] = Binding{}
	got, exists := catalog.Lookup(contract.Identifier())
	if !exists || got.ProviderID() != providerID || got.ProviderBuild() != providerBuild || got.SchemaDigest() != digest {
		t.Fatalf("Lookup = %#v, %t", got, exists)
	}
	bindings := catalog.Bindings()
	bindings[0] = Binding{}
	if got, exists := catalog.Lookup(contract.Identifier()); !exists || got.ProviderID() != providerID {
		t.Fatalf("catalog changed through Bindings result: %#v, %t", got, exists)
	}
}

func TestCatalogSupportsKernelBindingWithoutPluginID(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("kernel.health/v1")
	endpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	providerBuild := mustModuleBuild(t, "github.com/plystra/kernel", "v0.1.0", "")
	binding, err := NewBinding(BindingOptions{
		ProviderKind:  ProviderKindKernel,
		ProviderBuild: providerBuild,
		SchemaDigest:  sha256.Sum256([]byte("kernel.health/v1 schema")),
	}, endpoint)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	if binding.ProviderKind() != ProviderKindKernel || binding.ProviderID().String() != "" || binding.ProviderBuild() != providerBuild {
		t.Fatalf("Kernel binding = %#v", binding)
	}
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	got, exists := catalog.Lookup(contract.Identifier())
	if !exists || got.ProviderKind() != ProviderKindKernel || got.ProviderID().String() != "" || got.ProviderBuild() != providerBuild {
		t.Fatalf("Kernel Lookup = %#v, %t", got, exists)
	}
}

func TestNewBindingRejectsInvalidMetadata(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	endpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	providerID := mustPluginID(t, "acme.example.provider")
	providerBuild := mustModuleBuild(t, "github.com/acme/example", "v1.0.0", "")
	digest := sha256.Sum256([]byte("example.operation/v1 schema"))
	tests := []struct {
		name     string
		options  BindingOptions
		endpoint Endpoint
	}{
		{name: "zero endpoint", options: BindingOptions{ProviderKind: ProviderKindPlugin, ProviderID: providerID, ProviderBuild: providerBuild, SchemaDigest: digest}},
		{name: "zero digest", options: BindingOptions{ProviderKind: ProviderKindPlugin, ProviderID: providerID, ProviderBuild: providerBuild}, endpoint: endpoint},
		{name: "zero provider build", options: BindingOptions{ProviderKind: ProviderKindPlugin, ProviderID: providerID, SchemaDigest: digest}, endpoint: endpoint},
		{name: "unknown provider kind", options: BindingOptions{ProviderID: providerID, ProviderBuild: providerBuild, SchemaDigest: digest}, endpoint: endpoint},
		{name: "Kernel with plugin ID", options: BindingOptions{ProviderKind: ProviderKindKernel, ProviderID: providerID, ProviderBuild: providerBuild, SchemaDigest: digest}, endpoint: endpoint},
		{name: "plugin without ID", options: BindingOptions{ProviderKind: ProviderKindPlugin, ProviderBuild: providerBuild, SchemaDigest: digest}, endpoint: endpoint},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			binding, err := NewBinding(test.options, test.endpoint)
			if !errors.Is(err, ErrInvalidBinding) {
				t.Fatalf("NewBinding error = %v, want ErrInvalidBinding", err)
			}
			if binding.valid() || binding.Capability().String() != "" || binding.Definition().Valid() || binding.ProviderKind() != "" || binding.ProviderID().String() != "" || binding.ProviderBuild().Valid() || binding.SchemaDigest() != [sha256.Size]byte{} {
				t.Fatalf("invalid binding = %#v", binding)
			}
		})
	}
}

func TestNewCatalogRejectsInvalidAndDuplicateBindings(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	independent := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	providerID := mustPluginID(t, "acme.example.provider")
	providerBuild := mustModuleBuild(t, "github.com/acme/example", "v1.0.0", "")
	digest := sha256.Sum256([]byte("example.operation/v1 schema"))
	firstEndpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint first: %v", err)
	}
	secondEndpoint, err := NewEndpoint(independent, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint second: %v", err)
	}
	first, err := NewBinding(BindingOptions{ProviderKind: ProviderKindPlugin, ProviderID: providerID, ProviderBuild: providerBuild, SchemaDigest: digest}, firstEndpoint)
	if err != nil {
		t.Fatalf("NewBinding first: %v", err)
	}
	second, err := NewBinding(BindingOptions{ProviderKind: ProviderKindPlugin, ProviderID: providerID, ProviderBuild: providerBuild, SchemaDigest: digest}, secondEndpoint)
	if err != nil {
		t.Fatalf("NewBinding second: %v", err)
	}

	tests := []struct {
		name     string
		bindings []Binding
		also     error
	}{
		{name: "invalid", bindings: []Binding{{}}, also: ErrInvalidBinding},
		{name: "duplicate exact capability", bindings: []Binding{first, second}, also: ErrDuplicateBinding},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			catalog, err := NewCatalog(test.bindings)
			if !errors.Is(err, ErrInvalidCatalog) || !errors.Is(err, test.also) || catalog.valid() || catalog.Bindings() != nil {
				t.Fatalf("NewCatalog = %#v, %v", catalog, err)
			}
		})
	}
}

func TestCatalogUsesExactVersionsAndDeterministicOrder(t *testing.T) {
	t.Parallel()

	providerID := mustPluginID(t, "acme.example.provider")
	v2 := testBinding(t, "example.operation/v2", providerID)
	v1 := testBinding(t, "example.operation/v1", providerID)
	other := testBinding(t, "audit.write/v1", providerID)
	catalog, err := NewCatalog([]Binding{v2, v1, other})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	want := []string{"audit.write/v1", "example.operation/v1", "example.operation/v2"}
	got := make([]string, 0, len(want))
	for _, binding := range catalog.Bindings() {
		got = append(got, binding.Capability().String())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Bindings order = %v, want %v", got, want)
	}
	for _, binding := range []Binding{v1, v2} {
		resolved, exists := catalog.Lookup(binding.Capability())
		if !exists || resolved.Definition() != binding.Definition() {
			t.Fatalf("Lookup(%s) = %#v, %t", binding.Capability(), resolved, exists)
		}
	}
	v3 := mustCapabilityID(t, "example.operation/v3")
	if binding, exists := catalog.Lookup(v3); exists || binding.valid() {
		t.Fatalf("v3 Lookup = %#v, %t", binding, exists)
	}
}

func TestCatalogAllowsImmutableEmptySnapshot(t *testing.T) {
	t.Parallel()

	catalog, err := NewCatalog(nil)
	if err != nil {
		t.Fatalf("NewCatalog(nil): %v", err)
	}
	if !catalog.valid() || catalog.state == nil || catalog.state.entries == nil || catalog.state.ordered == nil || len(catalog.Bindings()) != 0 {
		t.Fatalf("empty Catalog = %#v", catalog)
	}
	identifier := mustCapabilityID(t, "example.operation/v1")
	if binding, exists := catalog.Lookup(identifier); exists || binding.valid() {
		t.Fatalf("empty Lookup = %#v, %t", binding, exists)
	}
	if binding, exists := catalog.Lookup(capability.Identifier{}); exists || binding.valid() {
		t.Fatalf("zero Lookup = %#v, %t", binding, exists)
	}
	var zero Catalog
	if zero.valid() || zero.Bindings() != nil {
		t.Fatalf("zero Catalog = %#v", zero)
	}
}

func TestCatalogLookupSupportsConcurrentReadsWithoutAllocations(t *testing.T) {
	providerID := mustPluginID(t, "acme.example.provider")
	binding := testBinding(t, "example.concurrent/v1", providerID)
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	identifier := binding.Capability()

	const readers = 64
	var group sync.WaitGroup
	group.Add(readers)
	for range readers {
		go func() {
			defer group.Done()
			for range 1000 {
				got, exists := catalog.Lookup(identifier)
				if !exists || got.Definition() != binding.Definition() {
					t.Errorf("Lookup = %#v, %t", got, exists)
					return
				}
			}
		}()
	}
	group.Wait()

	var result Binding
	allocations := testing.AllocsPerRun(1000, func() {
		var exists bool
		result, exists = catalog.Lookup(identifier)
		if !exists {
			panic("binding disappeared")
		}
	})
	if allocations != 0 || result.Definition() != binding.Definition() {
		t.Fatalf("Lookup allocations = %f, result %#v", allocations, result)
	}
}

func BenchmarkCapabilityLookup(b *testing.B) {
	catalog, identifier := benchmarkCatalog(b)
	var binding Binding
	var exists bool
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		binding, exists = catalog.Lookup(identifier)
	}
	benchmarkBinding, benchmarkExists = binding, exists
}

func BenchmarkRegistryConcurrentRead(b *testing.B) {
	catalog, identifier := benchmarkCatalog(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(parallel *testing.PB) {
		for parallel.Next() {
			binding, exists := catalog.Lookup(identifier)
			if !exists || binding.ProviderKind() != ProviderKindPlugin {
				panic("binding disappeared")
			}
		}
	})
}

var (
	benchmarkBinding Binding
	benchmarkExists  bool
)

func benchmarkCatalog(b *testing.B) (Catalog, capability.Identifier) {
	b.Helper()
	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.benchmark/v1")
	endpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		b.Fatalf("NewEndpoint: %v", err)
	}
	providerID, err := plugin.ParseID("acme.example.provider")
	if err != nil {
		b.Fatalf("ParseID: %v", err)
	}
	binding, err := NewBinding(BindingOptions{
		ProviderKind:  ProviderKindPlugin,
		ProviderID:    providerID,
		ProviderBuild: mustModuleBuildBenchmark(b, "github.com/acme/example", "v1.0.0", ""),
		SchemaDigest:  sha256.Sum256([]byte("example.benchmark/v1 schema")),
	}, endpoint)
	if err != nil {
		b.Fatalf("NewBinding: %v", err)
	}
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		b.Fatalf("NewCatalog: %v", err)
	}
	return catalog, contract.Identifier()
}

func testBinding(t *testing.T, identifier string, providerID plugin.ID) Binding {
	t.Helper()
	contract := capability.MustParseContract[endpointRequest, endpointResponse](identifier)
	endpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	binding, err := NewBinding(BindingOptions{
		ProviderKind:  ProviderKindPlugin,
		ProviderID:    providerID,
		ProviderBuild: mustModuleBuild(t, "github.com/acme/example", "v1.0.0", ""),
		SchemaDigest:  sha256.Sum256([]byte(identifier + " schema")),
	}, endpoint)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	return binding
}

func mustModuleBuild(t *testing.T, modulePath, moduleVersion, buildIdentity string) ModuleBuild {
	t.Helper()
	build, err := NewModuleBuild(modulePath, moduleVersion, buildIdentity)
	if err != nil {
		t.Fatalf("NewModuleBuild: %v", err)
	}
	return build
}

func mustModuleBuildBenchmark(b *testing.B, modulePath, moduleVersion, buildIdentity string) ModuleBuild {
	b.Helper()
	build, err := NewModuleBuild(modulePath, moduleVersion, buildIdentity)
	if err != nil {
		b.Fatalf("NewModuleBuild: %v", err)
	}
	return build
}

func mustPluginID(t *testing.T, value string) plugin.ID {
	t.Helper()
	providerID, err := plugin.ParseID(value)
	if err != nil {
		t.Fatalf("ParseID: %v", err)
	}
	return providerID
}

func mustCapabilityID(t *testing.T, value string) capability.Identifier {
	t.Helper()
	identifier, err := capability.ParseIdentifier(value)
	if err != nil {
		t.Fatalf("ParseIdentifier: %v", err)
	}
	return identifier
}

func successfulEndpointHandler(_ context.Context, request endpointRequest) (endpointResponse, error) {
	return endpointResponse(request), nil
}
