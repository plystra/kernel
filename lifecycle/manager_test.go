package lifecycle_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plystra/kernel/lifecycle"
)

type lifecycleContextKey struct{}

func TestStatesAreClosedAndStable(t *testing.T) {
	t.Parallel()

	states := map[lifecycle.State]string{
		lifecycle.StateNew:      "new",
		lifecycle.StateStarting: "starting",
		lifecycle.StateRunning:  "running",
		lifecycle.StateStopping: "stopping",
		lifecycle.StateStopped:  "stopped",
		lifecycle.StateFailed:   "failed",
	}
	for state, want := range states {
		if !state.Valid() || state.String() != want {
			t.Fatalf("State %q = %q, valid %t", state, state.String(), state.Valid())
		}
	}
	for _, state := range []lifecycle.State{"", "ready", "RUNNING"} {
		if state.Valid() || state.String() != "" {
			t.Fatalf("invalid State %q = %q, valid %t", state, state.String(), state.Valid())
		}
	}
}

func TestManagerStartsForwardAndStopsReverse(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, 4)
	bindings := []lifecycle.Binding{
		lifecycleBinding(t, "example.com/acme/lifecycle/second.New", &testLifecycleInstance{
			start: func(context.Context) error { events = append(events, "start:second"); return nil },
			stop:  func(context.Context) error { events = append(events, "stop:second"); return nil },
		}),
		lifecycleBinding(t, "example.com/acme/lifecycle/first.New", &testLifecycleInstance{
			start: func(context.Context) error { events = append(events, "start:first"); return nil },
			stop:  func(context.Context) error { events = append(events, "stop:first"); return nil },
		}),
	}
	manager := newLifecycleManager(t, bindings)
	bindings[0] = lifecycle.Binding{}

	if manager.State() != lifecycle.StateNew {
		t.Fatalf("initial State = %s", manager.State())
	}
	if err := manager.Start(context.Background()); err != nil || manager.State() != lifecycle.StateRunning {
		t.Fatalf("Start = %v, State %s", err, manager.State())
	}
	if err := manager.Stop(context.Background()); err != nil || manager.State() != lifecycle.StateStopped {
		t.Fatalf("Stop = %v, State %s", err, manager.State())
	}
	want := []string{"start:second", "start:first", "stop:first", "stop:second"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if err := manager.Stop(context.Background()); err != nil || !reflect.DeepEqual(events, want) {
		t.Fatalf("idempotent Stop = %v, events %v", err, events)
	}
	if err := manager.Start(context.Background()); !errors.Is(err, lifecycle.ErrState) {
		t.Fatalf("restart error = %v, want ErrState", err)
	}
}

func TestManagerSupportsNoLifecycleInstances(t *testing.T) {
	t.Parallel()

	manager := newLifecycleManager(t, nil)
	if err := manager.Start(context.Background()); err != nil || manager.State() != lifecycle.StateRunning {
		t.Fatalf("empty Start = %v, State %s", err, manager.State())
	}
	if err := manager.Stop(context.Background()); err != nil || manager.State() != lifecycle.StateStopped {
		t.Fatalf("empty Stop = %v, State %s", err, manager.State())
	}

	neverStarted := newLifecycleManager(t, nil)
	if err := neverStarted.Stop(context.Background()); err != nil || neverStarted.State() != lifecycle.StateStopped {
		t.Fatalf("unstarted Stop = %v, State %s", err, neverStarted.State())
	}
}

func TestManagerRejectsInvalidOptionsAndBindings(t *testing.T) {
	t.Parallel()

	valid := lifecycleBinding(t, "example.com/acme/lifecycle/duplicate.New", &testLifecycleInstance{})
	for _, test := range []struct {
		name     string
		options  lifecycle.ManagerOptions
		bindings []lifecycle.Binding
	}{
		{name: "zero rollback timeout"},
		{name: "negative rollback timeout", options: lifecycle.ManagerOptions{RollbackTimeout: -time.Second}},
		{name: "zero binding", options: lifecycle.ManagerOptions{RollbackTimeout: time.Second}, bindings: []lifecycle.Binding{{}}},
		{name: "duplicate constructor", options: lifecycle.ManagerOptions{RollbackTimeout: time.Second}, bindings: []lifecycle.Binding{valid, valid}},
	} {
		manager, err := lifecycle.NewManager(test.options, test.bindings)
		if !errors.Is(err, lifecycle.ErrInvalidManager) || manager != nil {
			t.Fatalf("%s NewManager = %#v, %v", test.name, manager, err)
		}
	}

	var nilManager *lifecycle.Manager
	if nilManager.State().Valid() {
		t.Fatalf("nil manager State = %s", nilManager.State())
	}
	if !errors.Is(nilManager.Start(context.Background()), lifecycle.ErrInvalidManager) ||
		!errors.Is(nilManager.Stop(context.Background()), lifecycle.ErrInvalidManager) {
		t.Fatal("nil manager operations did not fail closed")
	}
	manager := newLifecycleManager(t, nil)
	var nilContext context.Context
	if !errors.Is(manager.Start(nilContext), lifecycle.ErrInvalidContext) ||
		!errors.Is(manager.Stop(nilContext), lifecycle.ErrInvalidContext) || manager.State() != lifecycle.StateNew {
		t.Fatalf("nil contexts changed manager State to %s", manager.State())
	}
}

func TestManagerRollsBackFailingInstanceWithoutLeakingError(t *testing.T) {
	t.Parallel()

	secret := errors.New("password=secret")
	events := make([]string, 0, 4)
	rollbackValuePreserved := false
	manager := newLifecycleManager(t, []lifecycle.Binding{
		lifecycleBinding(t, "example.com/acme/lifecycle/ready.New", &testLifecycleInstance{
			start: func(context.Context) error { events = append(events, "start:ready"); return nil },
			stop:  func(context.Context) error { events = append(events, "stop:ready"); return nil },
		}),
		lifecycleBinding(t, "example.com/acme/lifecycle/failing.New", &testLifecycleInstance{
			start: func(context.Context) error { events = append(events, "start:failing"); return secret },
			stop: func(ctx context.Context) error {
				events = append(events, "stop:failing")
				rollbackValuePreserved = ctx.Value(lifecycleContextKey{}) == "preserved"
				return nil
			},
		}),
	})

	err := manager.Start(context.WithValue(context.Background(), lifecycleContextKey{}, "preserved"))
	if !errors.Is(err, lifecycle.ErrStart) || errors.Is(err, secret) || strings.Contains(err.Error(), "secret") ||
		!strings.Contains(err.Error(), "example.com/acme/lifecycle/failing.New") || manager.State() != lifecycle.StateFailed {
		t.Fatalf("Start failure = %v, State %s", err, manager.State())
	}
	want := []string{"start:ready", "start:failing", "stop:failing", "stop:ready"}
	if !reflect.DeepEqual(events, want) || !rollbackValuePreserved {
		t.Fatalf("rollback events = %v, want %v; context value preserved %t", events, want, rollbackValuePreserved)
	}
	if err := manager.Stop(context.Background()); err != nil || manager.State() != lifecycle.StateStopped || !reflect.DeepEqual(events, want) {
		t.Fatalf("post-rollback Stop = %v, State %s, events %v", err, manager.State(), events)
	}
}

func TestManagerReportsRollbackFailureAndRetriesOnlyActiveInstance(t *testing.T) {
	t.Parallel()

	secretStart := errors.New("token=start-secret")
	secretStop := errors.New("token=stop-secret")
	var stopAttempts atomic.Int32
	events := make([]string, 0, 5)
	manager := newLifecycleManager(t, []lifecycle.Binding{
		lifecycleBinding(t, "example.com/acme/lifecycle/stable.New", &testLifecycleInstance{
			start: func(context.Context) error { events = append(events, "start:stable"); return nil },
			stop:  func(context.Context) error { events = append(events, "stop:stable"); return nil },
		}),
		lifecycleBinding(t, "example.com/acme/lifecycle/retry.New", &testLifecycleInstance{
			start: func(context.Context) error { events = append(events, "start:retry"); return secretStart },
			stop: func(context.Context) error {
				events = append(events, "stop:retry")
				if stopAttempts.Add(1) == 1 {
					return secretStop
				}
				return nil
			},
		}),
	})

	err := manager.Start(context.Background())
	if !errors.Is(err, lifecycle.ErrStart) || !errors.Is(err, lifecycle.ErrStop) || errors.Is(err, secretStart) || errors.Is(err, secretStop) ||
		strings.Contains(err.Error(), "secret") || manager.State() != lifecycle.StateFailed {
		t.Fatalf("Start with rollback failure = %v, State %s", err, manager.State())
	}
	if err := manager.Stop(context.Background()); err != nil || manager.State() != lifecycle.StateStopped {
		t.Fatalf("retry Stop = %v, State %s", err, manager.State())
	}
	want := []string{"start:stable", "start:retry", "stop:retry", "stop:stable", "stop:retry"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("retry events = %v, want %v", events, want)
	}
}

func TestManagerRecoversLifecyclePanics(t *testing.T) {
	t.Parallel()

	t.Run("start", func(t *testing.T) {
		manager := newLifecycleManager(t, []lifecycle.Binding{
			lifecycleBinding(t, "example.com/acme/lifecycle/start-panic.New", &testLifecycleInstance{
				start: func(context.Context) error { panic("password=start-secret") },
			}),
		})
		err := manager.Start(context.Background())
		if !errors.Is(err, lifecycle.ErrStart) || strings.Contains(err.Error(), "secret") || manager.State() != lifecycle.StateFailed {
			t.Fatalf("panicking Start = %v, State %s", err, manager.State())
		}
	})

	t.Run("stop", func(t *testing.T) {
		manager := newLifecycleManager(t, []lifecycle.Binding{
			lifecycleBinding(t, "example.com/acme/lifecycle/stop-panic.New", &testLifecycleInstance{
				stop: func(context.Context) error { panic("password=stop-secret") },
			}),
		})
		if err := manager.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		err := manager.Stop(context.Background())
		if !errors.Is(err, lifecycle.ErrStop) || strings.Contains(err.Error(), "secret") || manager.State() != lifecycle.StateFailed {
			t.Fatalf("panicking Stop = %v, State %s", err, manager.State())
		}
	})
}

func TestManagerClassifiesOperationContexts(t *testing.T) {
	t.Parallel()

	t.Run("pre-cancelled start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var calls atomic.Int32
		manager := newLifecycleManager(t, []lifecycle.Binding{
			lifecycleBinding(t, "example.com/acme/lifecycle/cancelled.New", &testLifecycleInstance{start: func(context.Context) error {
				calls.Add(1)
				return nil
			}}),
		})
		err := manager.Start(ctx)
		if !errors.Is(err, lifecycle.ErrStart) || !errors.Is(err, context.Canceled) || calls.Load() != 0 || manager.State() != lifecycle.StateFailed {
			t.Fatalf("cancelled Start = %v, calls %d, State %s", err, calls.Load(), manager.State())
		}
	})

	t.Run("deadline during start", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		manager := newLifecycleManager(t, []lifecycle.Binding{
			lifecycleBinding(t, "example.com/acme/lifecycle/deadline.New", &testLifecycleInstance{start: func(ctx context.Context) error {
				<-ctx.Done()
				return errors.New("deadline secret")
			}}),
		})
		err := manager.Start(ctx)
		if !errors.Is(err, lifecycle.ErrStart) || !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("deadline Start = %v", err)
		}
	})

	t.Run("cancelled stop can retry", func(t *testing.T) {
		var stops atomic.Int32
		manager := newLifecycleManager(t, []lifecycle.Binding{
			lifecycleBinding(t, "example.com/acme/lifecycle/stop-cancel.New", &testLifecycleInstance{stop: func(context.Context) error {
				stops.Add(1)
				return nil
			}}),
		})
		if err := manager.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := manager.Stop(ctx)
		if !errors.Is(err, lifecycle.ErrStop) || !errors.Is(err, context.Canceled) || stops.Load() != 0 || manager.State() != lifecycle.StateFailed {
			t.Fatalf("cancelled Stop = %v, stops %d, State %s", err, stops.Load(), manager.State())
		}
		if err := manager.Stop(context.Background()); err != nil || stops.Load() != 1 || manager.State() != lifecycle.StateStopped {
			t.Fatalf("retry Stop = %v, stops %d, State %s", err, stops.Load(), manager.State())
		}
	})
}

func TestManagerRejectsConcurrentTransitions(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	manager := newLifecycleManager(t, []lifecycle.Binding{
		lifecycleBinding(t, "example.com/acme/lifecycle/concurrent.New", &testLifecycleInstance{start: func(context.Context) error {
			close(entered)
			<-release
			return nil
		}}),
	})
	result := make(chan error, 1)
	go func() { result <- manager.Start(context.Background()) }()
	<-entered

	if manager.State() != lifecycle.StateStarting {
		t.Fatalf("concurrent State = %s", manager.State())
	}
	if err := manager.Start(context.Background()); !errors.Is(err, lifecycle.ErrState) {
		t.Fatalf("concurrent Start = %v", err)
	}
	if err := manager.Stop(context.Background()); !errors.Is(err, lifecycle.ErrState) {
		t.Fatalf("concurrent Stop = %v", err)
	}
	close(release)
	if err := <-result; err != nil || manager.State() != lifecycle.StateRunning {
		t.Fatalf("first Start = %v, State %s", err, manager.State())
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func FuzzManagerOperationSequence(f *testing.F) {
	f.Add([]byte{0, 1, 1, 0})
	f.Add([]byte{1, 0})
	f.Fuzz(func(t *testing.T, operations []byte) {
		manager, err := lifecycle.NewManager(lifecycle.ManagerOptions{RollbackTimeout: time.Millisecond}, nil)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		if len(operations) > 64 {
			operations = operations[:64]
		}
		for _, operation := range operations {
			switch operation % 3 {
			case 0:
				_ = manager.Start(context.Background())
			case 1:
				_ = manager.Stop(context.Background())
			case 2:
				if !manager.State().Valid() {
					t.Fatalf("invalid State during operations: %q", manager.State())
				}
			}
		}
		if !manager.State().Valid() {
			t.Fatalf("invalid final State: %q", manager.State())
		}
	})
}

func lifecycleBinding(t testing.TB, constructor string, instance lifecycle.Instance) lifecycle.Binding {
	t.Helper()
	binding, err := lifecycle.NewBinding(constructor, instance)
	if err != nil {
		t.Fatalf("NewBinding(%s): %v", constructor, err)
	}
	return binding
}

func newLifecycleManager(t testing.TB, bindings []lifecycle.Binding) *lifecycle.Manager {
	t.Helper()
	manager, err := lifecycle.NewManager(lifecycle.ManagerOptions{RollbackTimeout: time.Second}, bindings)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return manager
}
