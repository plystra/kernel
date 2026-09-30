package invocation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/plystra/kernel/invocation"
)

func TestLifecycleAssemblyRejectsInvalidOrderWithoutChangingDispatcher(t *testing.T) {
	for _, invalid := range [][]string{{""}, {"private malformed constructor"}, {"example.com/policy.lower"}, {"example.com/policy.New", "example.com/policy.New"}} {
		_, dispatcher, catalog := startupRuntime(t, func(context.Context, string) (string, error) { return "", nil })
		if coordinator, err := dispatcher.BindLifecycle(invalid); coordinator != nil || !errors.Is(err, invocation.ErrInvalidLifecycle) {
			t.Fatalf("invalid lifecycle = %v", err)
		}
		constructors := []string{"example.com/policy.New"}
		coordinator, err := dispatcher.BindLifecycle(constructors)
		if err != nil {
			t.Fatal(err)
		}
		constructors[0] = "example.com/policy.Other"
		if duplicate, err := dispatcher.BindLifecycle(nil); duplicate != nil || !errors.Is(err, invocation.ErrInvalidLifecycle) {
			t.Fatalf("repeated lifecycle = %v", err)
		}
		if err := dispatcher.Publish(catalog); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = coordinator.Start(ctx, "example.com/policy.New", func(context.Context) error { return nil })
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
	coordinator, err := dispatcher.BindLifecycle([]string{"example.com/policy.New", "example.com/policy.Other"})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	hook := func(context.Context) error { t.Error("invalid hook was entered"); return nil }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := coordinator.Start(ctx, "example.com/policy.Other", hook); !errors.Is(err, invocation.ErrLifecycleState) {
		t.Fatal("out of order Start", err)
	}
	if err := coordinator.Stop(ctx, "example.com/policy.New", hook); !errors.Is(err, invocation.ErrLifecycleState) {
		t.Fatal("Stop before drain", err)
	}
	if err := coordinator.Start(context.Background(), "example.com/policy.New", hook); !errors.Is(err, invocation.ErrInvalidDrainContext) {
		t.Fatal("unbounded Start", err)
	}
	var nilContext context.Context
	if err := coordinator.Start(nilContext, "example.com/policy.New", hook); !errors.Is(err, invocation.ErrInvalidLifecycle) {
		t.Fatal("nil Start", err)
	}
	if err := coordinator.Start(ctx, "example.com/policy.New", nil); !errors.Is(err, invocation.ErrInvalidLifecycle) {
		t.Fatal("nil hook", err)
	}
	for _, invalid := range []*invocation.Lifecycle{nil, {}} {
		if err := invalid.Start(ctx, "example.com/policy.New", hook); !errors.Is(err, invocation.ErrInvalidLifecycle) {
			t.Fatal("invalid coordinator Start", err)
		}
		if err := invalid.Drain(ctx); !errors.Is(err, invocation.ErrInvalidLifecycle) {
			t.Fatal("invalid coordinator drain", err)
		}
	}
}

func TestFailedLifecycleStartCannotRetryIntoReadiness(t *testing.T) {
	_, dispatcher, catalog := startupRuntime(t, func(context.Context, string) (string, error) { return "", nil })
	coordinator, err := dispatcher.BindLifecycle([]string{"example.com/policy.New"})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := coordinator.Start(ctx, "example.com/policy.New", func(context.Context) error { return errors.New("private startup failure") }); !errors.Is(err, invocation.ErrLifecycleHook) {
		t.Fatal("startup error was not redacted", err)
	}
	if err := coordinator.Start(ctx, "example.com/policy.New", func(context.Context) error { t.Error("failed startup restarted"); return nil }); !errors.Is(err, invocation.ErrLifecycleState) {
		t.Fatal("failed lifecycle retried into readiness", err)
	}
	if err := dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherNotReady) {
		t.Fatal("failed startup opened admission", err)
	}
	if err := coordinator.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Stop(ctx, "example.com/policy.New", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
