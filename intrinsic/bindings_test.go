package intrinsic_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/capability/catalog"
	"github.com/plystra/kernel/intrinsic"
	"github.com/plystra/kernel/invocation"
)

func TestContractsMatchAuthoritativeIntrinsicCatalog(t *testing.T) {
	t.Parallel()

	health := intrinsic.HealthContract()
	info := intrinsic.InfoContract()
	if !health.Valid() || health.Identifier().String() != "kernel.health/v1" || len(health.SemanticErrors()) != 0 {
		t.Fatalf("HealthContract = %#v", health)
	}
	if !info.Valid() || info.Identifier().String() != "kernel.info/v1" || len(info.SemanticErrors()) != 0 {
		t.Fatalf("InfoContract = %#v", info)
	}
	for _, identifier := range []capability.Identifier{health.Identifier(), info.Identifier()} {
		definition, exists := catalog.Lookup(identifier)
		if !exists || definition.ID().String() != identifier.String() || definition.SchemaDigest() == [32]byte{} {
			t.Fatalf("catalog definition for %s = %#v, %t", identifier.String(), definition, exists)
		}
	}

	healthJSON, err := json.Marshal(intrinsic.HealthResponse{Status: intrinsic.HealthStatusHealthy})
	if err != nil || string(healthJSON) != `{"status":"healthy"}` {
		t.Fatalf("HealthResponse JSON = %s, %v", healthJSON, err)
	}
	infoJSON, err := json.Marshal(intrinsic.InfoResponse{AssemblyAPI: "v1", KernelModule: intrinsic.ModulePath, KernelVersion: "v0.1.0"})
	if err != nil || string(infoJSON) != `{"assembly_api":"v1","kernel_module":"github.com/plystra/kernel","kernel_version":"v0.1.0"}` {
		t.Fatalf("InfoResponse JSON = %s, %v", infoJSON, err)
	}
}

func TestBindingsPublishHealthAndInfoWithoutOrdinaryPlugins(t *testing.T) {
	t.Parallel()

	bindings, err := intrinsic.NewBindings(intrinsic.BindingOptions{
		ModuleVersion: "v0.1.0",
		BuildIdentity: "git:0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("NewBindings: %v", err)
	}
	if len(bindings) != 2 || bindings[0].Capability().String() != "kernel.health/v1" || bindings[1].Capability().String() != "kernel.info/v1" {
		t.Fatalf("bindings = %#v", bindings)
	}
	for _, binding := range bindings {
		build := binding.ProviderBuild()
		if binding.ProviderKind() != invocation.ProviderKindKernel || binding.ProviderID().String() != "" ||
			binding.ProviderPackage() != intrinsic.ProviderPackage || binding.SelectionReason() != invocation.SelectionReasonIntrinsic ||
			binding.SchemaDigest() == [32]byte{} || build.ModulePath() != intrinsic.ModulePath ||
			build.ModuleVersion() != "v0.1.0" || build.BuildIdentity() != "git:0123456789abcdef" {
			t.Fatalf("intrinsic binding %s provenance is incomplete", binding.Capability())
		}
	}

	catalogSnapshot, err := invocation.NewCatalog(bindings)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	bindings[0] = invocation.Binding{}
	if got := catalogSnapshot.Bindings(); len(got) != 2 || got[0].Capability().String() != "kernel.health/v1" {
		t.Fatalf("catalog changed with source bindings: %#v", got)
	}
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{DefaultTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	if err := dispatcher.Publish(catalogSnapshot); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	health, err := invocation.NewHandle(dispatcher, intrinsic.HealthContract(), true)
	if err != nil {
		t.Fatalf("NewHandle(health): %v", err)
	}
	info, err := invocation.NewHandle(dispatcher, intrinsic.InfoContract(), true)
	if err != nil {
		t.Fatalf("NewHandle(info): %v", err)
	}

	healthResponse, err := health.Invoke(context.Background(), intrinsic.HealthRequest{})
	if err != nil || healthResponse != (intrinsic.HealthResponse{Status: intrinsic.HealthStatusHealthy}) {
		t.Fatalf("health.Invoke = %#v, %v", healthResponse, err)
	}
	infoResponse, err := info.Invoke(context.Background(), intrinsic.InfoRequest{})
	if err != nil || infoResponse.AssemblyAPI != "v1" || infoResponse.KernelModule != intrinsic.ModulePath || infoResponse.KernelVersion != "v0.1.0" {
		t.Fatalf("info.Invoke = %#v, %v", infoResponse, err)
	}
	if formatted := fmt.Sprintf("%+v", infoResponse); strings.Contains(formatted, "0123456789abcdef") {
		t.Fatalf("info response exposed build identity: %s", formatted)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if response, err := health.Invoke(cancelled, intrinsic.HealthRequest{}); response != (intrinsic.HealthResponse{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled health.Invoke = %#v, %v", response, err)
	}
}

func TestDevelopmentBindingsExposeOnlyDevelopmentVersionMarker(t *testing.T) {
	t.Parallel()

	bindings, err := intrinsic.NewBindings(intrinsic.BindingOptions{BuildIdentity: "workspace:0123456789abcdef"})
	if err != nil {
		t.Fatalf("NewBindings: %v", err)
	}
	dispatcher, info := intrinsicInfoHandle(t, bindings)
	if !dispatcher.Published() {
		t.Fatal("intrinsic dispatcher is unpublished")
	}
	response, err := info.Invoke(context.Background(), intrinsic.InfoRequest{})
	if err != nil || response.KernelVersion != "devel" || strings.Contains(fmt.Sprintf("%+v", response), "workspace") {
		t.Fatalf("development info = %#v, %v", response, err)
	}
}

func TestBindingsAreSafeForConcurrentIntrinsicReads(t *testing.T) {
	t.Parallel()

	bindings, err := intrinsic.NewBindings(intrinsic.BindingOptions{ModuleVersion: "v0.1.0"})
	if err != nil {
		t.Fatalf("NewBindings: %v", err)
	}
	catalogSnapshot, err := invocation.NewCatalog(bindings)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{DefaultTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	if err := dispatcher.Publish(catalogSnapshot); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	health, err := invocation.NewHandle(dispatcher, intrinsic.HealthContract(), true)
	if err != nil {
		t.Fatalf("NewHandle: %v", err)
	}

	var wait sync.WaitGroup
	errorsFound := make(chan error, 32)
	for worker := 0; worker < cap(errorsFound); worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for call := 0; call < 100; call++ {
				response, err := health.Invoke(context.Background(), intrinsic.HealthRequest{})
				if err != nil || response.Status != intrinsic.HealthStatusHealthy {
					errorsFound <- fmt.Errorf("health response %#v: %w", response, err)
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
}

func TestBindingsRejectInvalidBuildProvenanceSafely(t *testing.T) {
	t.Parallel()

	tests := []intrinsic.BindingOptions{
		{},
		{ModuleVersion: "v2.0.0"},
		{ModuleVersion: "development"},
		{BuildIdentity: "runtime private secret"},
		{ModuleVersion: "v0.1.0", BuildIdentity: strings.Repeat("a", invocation.MaximumBuildIdentitySize+1)},
	}
	for _, options := range tests {
		options := options
		t.Run(fmt.Sprintf("%q-%q", options.ModuleVersion, options.BuildIdentity), func(t *testing.T) {
			t.Parallel()
			bindings, err := intrinsic.NewBindings(options)
			if bindings != nil || !errors.Is(err, intrinsic.ErrBindings) || !errors.Is(err, invocation.ErrInvalidModuleBuild) {
				t.Fatalf("NewBindings = %#v, %v", bindings, err)
			}
			if options.BuildIdentity != "" && strings.Contains(err.Error(), options.BuildIdentity) {
				t.Fatalf("error exposed rejected build identity: %v", err)
			}
		})
	}
}

func FuzzNewBindings(f *testing.F) {
	f.Add("v0.1.0", "")
	f.Add("", "fuzz-build")
	f.Add("v2.0.0", "invalid identity")
	f.Fuzz(func(t *testing.T, version, identity string) {
		bindings, err := intrinsic.NewBindings(intrinsic.BindingOptions{ModuleVersion: version, BuildIdentity: identity})
		if err != nil {
			if bindings != nil || !errors.Is(err, intrinsic.ErrBindings) || !errors.Is(err, invocation.ErrInvalidModuleBuild) {
				t.Fatalf("NewBindings = %#v, %v", bindings, err)
			}
			return
		}
		if len(bindings) != 2 || bindings[0].Capability().String() != "kernel.health/v1" || bindings[1].Capability().String() != "kernel.info/v1" {
			t.Fatalf("NewBindings = %#v", bindings)
		}
	})
}

func intrinsicInfoHandle(t testing.TB, bindings []invocation.Binding) (*invocation.Dispatcher, invocation.Handle[intrinsic.InfoRequest, intrinsic.InfoResponse]) {
	t.Helper()
	catalogSnapshot, err := invocation.NewCatalog(bindings)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{DefaultTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	if err := dispatcher.Publish(catalogSnapshot); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	handle, err := invocation.NewHandle(dispatcher, intrinsic.InfoContract(), true)
	if err != nil {
		t.Fatalf("NewHandle(info): %v", err)
	}
	return dispatcher, handle
}
