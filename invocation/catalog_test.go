package invocation

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/plystra/kernel/capability"
)

func TestBindingKindsAreClosedAndStable(t *testing.T) {
	t.Parallel()

	for kind, want := range map[BindingKind]string{
		BindingKindIntrinsic:      "intrinsic",
		BindingKindImplementation: "implementation",
	} {
		if !kind.Valid() || kind.String() != want {
			t.Fatalf("BindingKind %q = %q, valid %t", kind, kind.String(), kind.Valid())
		}
	}
	for _, kind := range []BindingKind{"", "remote", "IMPLEMENTATION"} {
		if kind.Valid() || kind.String() != "" {
			t.Fatalf("invalid BindingKind %q was accepted", kind)
		}
	}
}

func TestSelectionReasonsAreClosedAndStable(t *testing.T) {
	t.Parallel()

	for reason, want := range map[SelectionReason]string{
		SelectionReasonIntrinsic:        "intrinsic",
		SelectionReasonUniqueCompatible: "unique-compatible",
		SelectionReasonExplicit:         "explicit",
	} {
		if !reason.Valid() || reason.String() != want {
			t.Fatalf("SelectionReason %q = %q, valid %t", reason, reason.String(), reason.Valid())
		}
	}
	for _, reason := range []SelectionReason{"", "priority", "EXPLICIT"} {
		if reason.Valid() || reason.String() != "" {
			t.Fatalf("invalid SelectionReason %q was accepted", reason)
		}
	}
}

func TestCatalogCopiesResolvedImplementationBindings(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("email.send/v1")
	endpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	moduleBuild := mustModuleBuild(t, "github.com/acme/email", "v1.4.2", "git:0123456789abcdef")
	digest := sha256.Sum256([]byte("email.send/v1 schema"))
	binding, err := NewBinding(BindingOptions{
		Policy:          testPolicy(time.Second, 256),
		Kind:            BindingKindImplementation,
		Constructor:     "github.com/acme/email/smtp.New",
		ModuleBuild:     moduleBuild,
		SelectionReason: SelectionReasonExplicit,
		ContractDigest:  digest,
	}, endpoint)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	if binding.InterfaceID() != contract.Identifier() || binding.Definition() != contract.Definition() || binding.Kind() != BindingKindImplementation || binding.Constructor() != "github.com/acme/email/smtp.New" || binding.ModuleBuild() != moduleBuild || binding.SelectionReason() != SelectionReasonExplicit || binding.ContractDigest() != digest {
		t.Fatalf("binding accessors = %#v", binding)
	}

	source := []Binding{binding}
	catalog, err := NewCatalog(source)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	source[0] = Binding{}
	got, exists := catalog.Lookup(contract.Identifier())
	if !exists || got.Kind() != BindingKindImplementation || got.Constructor() != "github.com/acme/email/smtp.New" || got.ModuleBuild() != moduleBuild || got.SelectionReason() != SelectionReasonExplicit || got.ContractDigest() != digest {
		t.Fatalf("Lookup = %#v, %t", got, exists)
	}
	bindings := catalog.Bindings()
	bindings[0] = Binding{}
	if got, exists := catalog.Lookup(contract.Identifier()); !exists || got.Constructor() != "github.com/acme/email/smtp.New" {
		t.Fatalf("catalog changed through Bindings result: %#v, %t", got, exists)
	}
}

func TestCatalogSupportsIntrinsicBindingWithoutConstructor(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("kernel.health/v1")
	endpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	moduleBuild := mustModuleBuild(t, "github.com/plystra/kernel", "v0.1.0", "")
	binding, err := NewBinding(BindingOptions{
		Policy:          testPolicy(time.Second, 256),
		Kind:            BindingKindIntrinsic,
		ModuleBuild:     moduleBuild,
		SelectionReason: SelectionReasonIntrinsic,
		ContractDigest:  sha256.Sum256([]byte("kernel.health/v1 schema")),
	}, endpoint)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	if binding.Kind() != BindingKindIntrinsic || binding.Constructor() != "" || binding.ModuleBuild() != moduleBuild || binding.SelectionReason() != SelectionReasonIntrinsic {
		t.Fatalf("Kernel binding = %#v", binding)
	}
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	got, exists := catalog.Lookup(contract.Identifier())
	if !exists || got.Kind() != BindingKindIntrinsic || got.Constructor() != "" || got.ModuleBuild() != moduleBuild || got.SelectionReason() != SelectionReasonIntrinsic {
		t.Fatalf("Kernel Lookup = %#v, %t", got, exists)
	}
}

func TestCatalogKeepsShortLocalModuleOwnershipExact(t *testing.T) {
	t.Parallel()
	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	endpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		t.Fatal(err)
	}
	build := mustModuleBuild(t, "my-app", "", "sha256:0123456789abcdef")
	for _, symbol := range []string{"my-app.New", "my-app/provider.New", "my-app-other/provider.New", "another-app/provider.New"} {
		binding, err := NewBinding(BindingOptions{
			Policy: testPolicy(time.Second, 64), Kind: BindingKindImplementation,
			Constructor: symbol, ModuleBuild: build, SelectionReason: SelectionReasonExplicit,
			ContractDigest: sha256.Sum256([]byte("example.operation/v1 schema")),
		}, endpoint)
		if symbol == "my-app-other/provider.New" || symbol == "another-app/provider.New" {
			if !errors.Is(err, ErrInvalidBinding) {
				t.Fatalf("outside-module constructor %s = %v", symbol, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := NewCatalog([]Binding{binding})
		if err != nil {
			t.Fatal(err)
		}
		got, ok := catalog.Lookup(contract.Identifier())
		if !ok || got.ModuleBuild() != build || got.Constructor() != symbol {
			t.Fatal("catalog changed local module ownership")
		}
	}
}

func TestNewBindingRejectsInvalidMetadata(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	endpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	moduleBuild := mustModuleBuild(t, "github.com/acme/example", "v1.0.0", "")
	digest := sha256.Sum256([]byte("example.operation/v1 schema"))
	validImplementation := BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindImplementation, Constructor: "github.com/acme/example/provider.New", ModuleBuild: moduleBuild, SelectionReason: SelectionReasonUniqueCompatible, ContractDigest: digest}
	tests := []struct {
		name     string
		options  BindingOptions
		endpoint Endpoint
	}{
		{name: "zero endpoint", options: validImplementation},
		{name: "zero digest", options: BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindImplementation, Constructor: "github.com/acme/example/provider.New", ModuleBuild: moduleBuild, SelectionReason: SelectionReasonUniqueCompatible}, endpoint: endpoint},
		{name: "zero module build", options: BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindImplementation, Constructor: "github.com/acme/example/provider.New", SelectionReason: SelectionReasonUniqueCompatible, ContractDigest: digest}, endpoint: endpoint},
		{name: "missing constructor", options: BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindImplementation, ModuleBuild: moduleBuild, SelectionReason: SelectionReasonUniqueCompatible, ContractDigest: digest}, endpoint: endpoint},
		{name: "unexported constructor", options: BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindImplementation, Constructor: "github.com/acme/example/provider.new", ModuleBuild: moduleBuild, SelectionReason: SelectionReasonUniqueCompatible, ContractDigest: digest}, endpoint: endpoint},
		{name: "constructor outside module", options: BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindImplementation, Constructor: "github.com/other/provider.New", ModuleBuild: moduleBuild, SelectionReason: SelectionReasonUniqueCompatible, ContractDigest: digest}, endpoint: endpoint},
		{name: "unknown binding kind", options: BindingOptions{Policy: testPolicy(time.Second, 256), Constructor: "github.com/acme/example/provider.New", ModuleBuild: moduleBuild, SelectionReason: SelectionReasonUniqueCompatible, ContractDigest: digest}, endpoint: endpoint},
		{name: "missing selection reason", options: BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindImplementation, Constructor: "github.com/acme/example/provider.New", ModuleBuild: moduleBuild, ContractDigest: digest}, endpoint: endpoint},
		{name: "intrinsic with constructor", options: BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindIntrinsic, Constructor: "github.com/acme/example/provider.New", ModuleBuild: moduleBuild, SelectionReason: SelectionReasonIntrinsic, ContractDigest: digest}, endpoint: endpoint},
		{name: "intrinsic with ordinary selection", options: BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindIntrinsic, ModuleBuild: moduleBuild, SelectionReason: SelectionReasonUniqueCompatible, ContractDigest: digest}, endpoint: endpoint},
		{name: "Implementation with intrinsic selection", options: BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindImplementation, Constructor: "github.com/acme/example/provider.New", ModuleBuild: moduleBuild, SelectionReason: SelectionReasonIntrinsic, ContractDigest: digest}, endpoint: endpoint},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			binding, err := NewBinding(test.options, test.endpoint)
			if !errors.Is(err, ErrInvalidBinding) {
				t.Fatalf("NewBinding error = %v, want ErrInvalidBinding", err)
			}
			if binding.valid() || binding.InterfaceID().String() != "" || binding.Definition().Valid() || binding.Kind() != "" || binding.Constructor() != "" || binding.ModuleBuild().Valid() || binding.SelectionReason() != "" || binding.ContractDigest() != [sha256.Size]byte{} {
				t.Fatalf("invalid binding = %#v", binding)
			}
		})
	}
}

func TestNewCatalogRejectsInvalidAndDuplicateBindings(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	independent := capability.MustParseContract[endpointRequest, endpointResponse]("example.operation/v1")
	moduleBuild := mustModuleBuild(t, "github.com/acme/example", "v1.0.0", "")
	digest := sha256.Sum256([]byte("example.operation/v1 schema"))
	firstEndpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint first: %v", err)
	}
	secondEndpoint, err := NewEndpoint(independent, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint second: %v", err)
	}
	first, err := NewBinding(BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindImplementation, Constructor: "github.com/acme/example/provider.New", ModuleBuild: moduleBuild, SelectionReason: SelectionReasonUniqueCompatible, ContractDigest: digest}, firstEndpoint)
	if err != nil {
		t.Fatalf("NewBinding first: %v", err)
	}
	second, err := NewBinding(BindingOptions{Policy: testPolicy(time.Second, 256), Kind: BindingKindImplementation, Constructor: "github.com/acme/example/provider.New", ModuleBuild: moduleBuild, SelectionReason: SelectionReasonUniqueCompatible, ContractDigest: digest}, secondEndpoint)
	if err != nil {
		t.Fatalf("NewBinding second: %v", err)
	}

	tests := []struct {
		name     string
		bindings []Binding
		also     error
	}{
		{name: "invalid", bindings: []Binding{{}}, also: ErrInvalidBinding},
		{name: "duplicate exact Interface", bindings: []Binding{first, second}, also: ErrDuplicateBinding},
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

	v2 := testBinding(t, "example.operation/v2")
	v1 := testBinding(t, "example.operation/v1")
	other := testBinding(t, "audit.write/v1")
	catalog, err := NewCatalog([]Binding{v2, v1, other})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	want := []string{"audit.write/v1", "example.operation/v1", "example.operation/v2"}
	got := make([]string, 0, len(want))
	for _, binding := range catalog.Bindings() {
		got = append(got, binding.InterfaceID().String())
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Bindings order = %v, want %v", got, want)
	}
	for _, binding := range []Binding{v1, v2} {
		resolved, exists := catalog.Lookup(binding.InterfaceID())
		if !exists || resolved.Definition() != binding.Definition() {
			t.Fatalf("Lookup(%s) = %#v, %t", binding.InterfaceID(), resolved, exists)
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
	binding := testBinding(t, "example.concurrent/v1")
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	identifier := binding.InterfaceID()

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
			if !exists || binding.Kind() != BindingKindImplementation {
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
	binding, err := NewBinding(BindingOptions{
		Policy:          testPolicy(time.Second, 256),
		Kind:            BindingKindImplementation,
		Constructor:     "github.com/acme/example/provider.New",
		ModuleBuild:     mustModuleBuildBenchmark(b, "github.com/acme/example", "v1.0.0", ""),
		SelectionReason: SelectionReasonUniqueCompatible,
		ContractDigest:  sha256.Sum256([]byte("example.benchmark/v1 schema")),
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

func testBinding(t *testing.T, identifier string) Binding {
	t.Helper()
	contract := capability.MustParseContract[endpointRequest, endpointResponse](identifier)
	endpoint, err := NewEndpoint(contract, successfulEndpointHandler)
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	binding, err := NewBinding(BindingOptions{
		Policy:          testPolicy(time.Second, 256),
		Kind:            BindingKindImplementation,
		Constructor:     "github.com/acme/example/provider.New",
		ModuleBuild:     mustModuleBuild(t, "github.com/acme/example", "v1.0.0", ""),
		SelectionReason: SelectionReasonUniqueCompatible,
		ContractDigest:  sha256.Sum256([]byte(identifier + " schema")),
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
