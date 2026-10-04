package invocation_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
)

func TestLifecycleAssemblyRejectsInvalidOrderWithoutChangingDispatcher(t *testing.T) {
	implementation := implementationOwner(t, "example.com/policy.New")
	resource := resourceOwner(t, "database.primary")
	for _, invalid := range [][]invocation.LifecycleOwner{{{}}, {implementation, {}}, {implementation, implementation}, {resource, resource}} {
		_, dispatcher, catalog := startupRuntime(t, func(context.Context, string) (string, error) { return "", nil })
		if coordinator, err := dispatcher.BindLifecycle(invalid); coordinator != nil || !errors.Is(err, invocation.ErrInvalidLifecycle) {
			t.Fatalf("invalid lifecycle = %v", err)
		}
		constructors := []invocation.LifecycleOwner{implementation}
		coordinator, err := dispatcher.BindLifecycle(constructors)
		if err != nil {
			t.Fatal(err)
		}
		constructors[0] = resource
		if duplicate, err := dispatcher.BindLifecycle(nil); duplicate != nil || !errors.Is(err, invocation.ErrInvalidLifecycle) {
			t.Fatalf("repeated lifecycle = %v", err)
		}
		if err := dispatcher.Publish(catalog); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = coordinator.Start(ctx, implementation, func(context.Context) error { return nil })
		cancel()
		if err != nil {
			t.Fatal("source mutation changed lifecycle order", err)
		}
		if err := dispatcher.OpenAdmission(); err != nil {
			t.Fatal(err)
		}
	}
	for _, dispatcher := range []*invocation.Dispatcher{nil, {}} {
		if coordinator, err := dispatcher.BindLifecycle(nil); coordinator != nil || !errors.Is(err, invocation.ErrInvalidDispatcher) {
			t.Fatalf("invalid dispatcher accepted lifecycle = %v", err)
		}
	}
	_, published, catalog := startupRuntime(t, func(context.Context, string) (string, error) { return "", nil })
	if err := published.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := published.BindLifecycle(nil); !errors.Is(err, invocation.ErrInvalidLifecycle) {
		t.Fatal("late lifecycle binding changed publication", err)
	}
}

func TestLifecycleRejectsOutOfOrderAndUnboundedHooks(t *testing.T) {
	_, dispatcher, catalog := startupRuntime(t, func(context.Context, string) (string, error) { return "", nil })
	owner := implementationOwner(t, "example.com/policy.New")
	other := implementationOwner(t, "example.com/policy.Other")
	coordinator, err := dispatcher.BindLifecycle([]invocation.LifecycleOwner{owner, other})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	hook := func(context.Context) error { t.Error("invalid hook was entered"); return nil }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := coordinator.Start(ctx, other, hook); !errors.Is(err, invocation.ErrLifecycleState) {
		t.Fatal("out of order Start", err)
	}
	if err := coordinator.Stop(ctx, owner, hook); !errors.Is(err, invocation.ErrLifecycleState) {
		t.Fatal("Stop before drain", err)
	}
	if err := coordinator.Start(context.Background(), owner, hook); !errors.Is(err, invocation.ErrInvalidDrainContext) {
		t.Fatal("unbounded Start", err)
	}
	var nilContext context.Context
	if err := coordinator.Start(nilContext, owner, hook); !errors.Is(err, invocation.ErrInvalidLifecycle) {
		t.Fatal("nil Start", err)
	}
	if err := coordinator.Start(ctx, owner, nil); !errors.Is(err, invocation.ErrInvalidLifecycle) {
		t.Fatal("nil hook", err)
	}
	for _, invalid := range []*invocation.Lifecycle{nil, {}} {
		if err := invalid.Start(ctx, owner, hook); !errors.Is(err, invocation.ErrInvalidLifecycle) {
			t.Fatal("invalid coordinator Start", err)
		}
		if err := invalid.Drain(ctx); !errors.Is(err, invocation.ErrInvalidLifecycle) {
			t.Fatal("invalid coordinator drain", err)
		}
	}
}

func TestFailedLifecycleStartCannotRetryIntoReadiness(t *testing.T) {
	_, dispatcher, catalog := startupRuntime(t, func(context.Context, string) (string, error) { return "", nil })
	owner := implementationOwner(t, "example.com/policy.New")
	coordinator, err := dispatcher.BindLifecycle([]invocation.LifecycleOwner{owner})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := coordinator.Start(ctx, owner, func(context.Context) error { return errors.New("private startup failure") }); !errors.Is(err, invocation.ErrLifecycleHook) {
		t.Fatal("startup error was not redacted", err)
	}
	if err := coordinator.Start(ctx, owner, func(context.Context) error { t.Error("failed startup restarted"); return nil }); !errors.Is(err, invocation.ErrLifecycleState) {
		t.Fatal("failed lifecycle retried into readiness", err)
	}
	if err := dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherNotReady) {
		t.Fatal("failed startup opened admission", err)
	}
	if err := coordinator.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Stop(ctx, owner, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleOwnersAreValidatedTypedIdentities(t *testing.T) {
	for _, name := range []string{"a", "a0", "a-0", "db.primary-2", "a.b.c", strings.Repeat("a", 128), strings.Repeat("a.", 63) + "a0"} {
		owner := resourceOwner(t, name)
		if owner.Kind() != invocation.LifecycleOwnerResource || owner.ResourceInstanceName() != name || owner.Constructor() != "" || owner.String() != "resource "+name {
			t.Fatalf("Resource identity changed: %+v", owner)
		}
		if copy := owner; copy != owner || !copy.Valid() {
			t.Fatal("owner was not a comparable value")
		}
	}
	for _, name := range []string{"", strings.Repeat("a", 129), "A", "0db", "db.Primary", "db.0", ".db", "db.", "db..a", "-db", "db-", "db--a", "db.-a", "db-.a", "db_a", "db/a", "db:v1", "db a", " db", "db\n", "db\x00", "db\u00e9", "\u6570\u636e"} {
		owner, err := invocation.NewResourceOwner(name)
		if !errors.Is(err, invocation.ErrInvalidLifecycleOwner) || owner != (invocation.LifecycleOwner{}) || strings.Contains(fmt.Sprint(err), name) && name != "" {
			t.Fatalf("invalid Resource name accepted or disclosed: %q, %v", name, err)
		}
	}
	for _, symbol := range []string{"example.com/policy.New", "my-app.New", "example.com/policy.New\u6570"} {
		owner := implementationOwner(t, symbol)
		if owner.Kind() != invocation.LifecycleOwnerImplementation || owner.Constructor() != symbol || owner.ResourceInstanceName() != "" || owner.String() != "implementation "+symbol {
			t.Fatalf("Implementation identity changed: %+v", owner)
		}
	}
	for _, symbol := range []string{"", "private malformed constructor", "example.com/policy.lower", "../policy.New", "example.com/policy.", "database.primary"} {
		owner, err := invocation.NewImplementationOwner(symbol)
		if !errors.Is(err, invocation.ErrInvalidLifecycleOwner) || owner.Valid() {
			t.Fatalf("invalid constructor accepted: %q, %v", symbol, err)
		}
	}
	var zero invocation.LifecycleOwner
	if zero.Valid() || zero.Kind() != "" || zero.String() != "" || zero.Constructor() != "" || zero.ResourceInstanceName() != "" {
		t.Fatal("zero owner exposed identity")
	}
	for _, kind := range []invocation.LifecycleOwnerKind{"", "intrinsic", "RESOURCE"} {
		if kind.Valid() || kind.String() != "" {
			t.Fatalf("invalid kind accepted: %q", kind)
		}
	}
	for _, kind := range []invocation.LifecycleOwnerKind{invocation.LifecycleOwnerImplementation, invocation.LifecycleOwnerResource} {
		if !kind.Valid() || kind.String() != string(kind) {
			t.Fatalf("valid kind rejected: %q", kind)
		}
	}
	if invocation.BindingKind(invocation.LifecycleOwnerResource).Valid() {
		t.Fatal("Resource lifecycle kind became a catalog binding kind")
	}
}

func TestResourceLifecycleOrderAndAdmissionWithoutResourceEndpoints(t *testing.T) {
	_, dispatcher, catalog := startupRuntime(t, func(context.Context, string) (string, error) { return "", nil })
	first, second := resourceOwner(t, "database.first"), resourceOwner(t, "database.second")
	owners := []invocation.LifecycleOwner{first, second}
	coordinator, err := dispatcher.BindLifecycle(owners)
	if err != nil {
		t.Fatal(err)
	}
	owners[0] = second
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unexpected := func(context.Context) error { t.Error("invalid hook entered"); return nil }
	for _, owner := range []invocation.LifecycleOwner{second, resourceOwner(t, "database.missing"), implementationOwner(t, "example.com/policy.New")} {
		if err := coordinator.Start(ctx, owner, unexpected); !errors.Is(err, invocation.ErrLifecycleState) {
			t.Fatalf("out-of-order or unbound owner: %v", err)
		}
	}
	if err := coordinator.Start(ctx, invocation.LifecycleOwner{}, unexpected); !errors.Is(err, invocation.ErrInvalidLifecycle) {
		t.Fatalf("invalid owner accepted: %v", err)
	}
	for _, owner := range []invocation.LifecycleOwner{first, second} {
		if err := dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherNotReady) {
			t.Fatalf("pending Resource allowed admission: %v", err)
		}
		if err := coordinator.Start(ctx, owner, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := dispatcher.OpenAdmission(); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []invocation.LifecycleOwner{second, first} {
		if err := coordinator.Stop(ctx, owner, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResourceLifecycleKindCannotCreateCatalogBinding(t *testing.T) {
	_, _, catalog := startupRuntime(t, func(context.Context, string) (string, error) { return "", nil })
	ordinary := catalog.Bindings()[0]
	endpoint, err := invocation.NewEndpoint(capability.MustParseContract[string, string]("example.resource/v1"), func(_ context.Context, value string) (string, error) { return value, nil })
	if err != nil {
		t.Fatal(err)
	}
	options := invocation.BindingOptions{
		Kind: invocation.BindingKind(invocation.LifecycleOwnerResource), Constructor: ordinary.Constructor(),
		ModuleBuild: ordinary.ModuleBuild(), SelectionReason: ordinary.SelectionReason(),
		ContractDigest: ordinary.ContractDigest(), Policy: ordinary.Policy(),
	}
	if _, err := invocation.NewBinding(options, endpoint); !errors.Is(err, invocation.ErrInvalidBinding) {
		t.Fatal("Resource catalog binding was accepted", err)
	}
	options.Kind = invocation.BindingKindImplementation
	options.Constructor = "database.primary"
	if _, err := invocation.NewBinding(options, endpoint); !errors.Is(err, invocation.ErrInvalidBinding) {
		t.Fatal("Resource name was accepted as a constructor", err)
	}
}

func FuzzResourceLifecycleOwnerName(f *testing.F) {
	for _, name := range []string{"database.primary", "a-0.b2", "a..b", "a--b", "a.-b", "a-.b", "\u00e9", strings.Repeat("a", 128), strings.Repeat("a", 129)} {
		f.Add(name)
	}
	grammar := regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)*$`)
	f.Fuzz(func(t *testing.T, name string) {
		owner, err := invocation.NewResourceOwner(name)
		valid := len(name) >= 1 && len(name) <= 128 && grammar.MatchString(name)
		if (err == nil) != valid || owner.Valid() != valid || valid && owner.ResourceInstanceName() != name {
			t.Fatalf("grammar disagreement for %q: %v", name, err)
		}
	})
}

func implementationOwner(t testing.TB, constructor string) invocation.LifecycleOwner {
	t.Helper()
	owner, err := invocation.NewImplementationOwner(constructor)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func resourceOwner(t testing.TB, name string) invocation.LifecycleOwner {
	t.Helper()
	owner, err := invocation.NewResourceOwner(name)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}
