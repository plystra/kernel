package invocation_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plystra/kernel/invocation"
	"github.com/plystra/kernel/lifecycle"
)

func TestCancellationReturnsBeforeUncooperativeTargetTerminates(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	terminated := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	handle, dispatcher := publicErrorRuntime(t, time.Minute, func(ctx context.Context, _ error) (string, error) {
		defer close(terminated)
		close(entered)
		<-release
		return "late response", nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		response, err := handle.Invoke(ctx, nil)
		if response != "" {
			t.Errorf("late response escaped: %q", response)
		}
		returned <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) || invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
			t.Fatalf("cancelled caller = %v (%s)", err, invocation.CompletionOf(err))
		}
	case <-time.After(time.Second):
		once.Do(func() { close(release) })
		<-returned
		t.Fatal("caller waited for an uncooperative target after cancellation")
	}
	select {
	case <-terminated:
		t.Fatal("target unexpectedly terminated before its explicit release")
	default:
	}
	if dispatcher.ActiveAttempts() != 1 {
		t.Fatal("caller completion released its still-running attempt")
	}
	once.Do(func() { close(release) })
	<-terminated
	drainDispatcher(t, dispatcher)
}

func TestDeadlineReturnsBeforeTargetAndDiscardsEveryLateOutcome(t *testing.T) {
	for _, late := range []string{"success", "error", "panic", "goexit"} {
		t.Run(late, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var calls atomic.Int32
			handle, dispatcher := publicErrorRuntime(t, 100*time.Millisecond, func(ctx context.Context, _ error) (string, error) {
				calls.Add(1)
				close(entered)
				<-release
				switch late {
				case "error":
					return "", &privateError{}
				case "panic":
					panic("private panic")
				case "goexit":
					runtime.Goexit()
				}
				return "late success", nil
			})
			returned := make(chan error, 1)
			go func() {
				response, err := handle.Invoke(context.Background(), nil)
				if response != "" {
					t.Errorf("late result escaped: %q", response)
				}
				returned <- err
			}()
			awaitSignal(t, entered)
			err := awaitError(t, returned)
			if !errors.Is(err, context.DeadlineExceeded) || invocation.CompletionOf(err) != invocation.CompletionResultUnknown || dispatcher.ActiveAttempts() != 1 {
				t.Fatalf("deadline result = %v, active = %d", err, dispatcher.ActiveAttempts())
			}
			once.Do(func() { close(release) })
			drainDispatcher(t, dispatcher)
			if calls.Load() != 1 {
				t.Fatalf("late outcome triggered another execution: %d", calls.Load())
			}
		})
	}
}

func TestDrainCancelsClosesAdmissionAndRetriesBeforeDependencyCleanup(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	dependency := &drainDependency{}
	binding, err := lifecycle.NewBinding("github.com/acme/drain.New", dependency)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := lifecycle.NewManager(lifecycle.ManagerOptions{RollbackTimeout: time.Second}, []lifecycle.Binding{binding})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	handle, dispatcher := publicErrorRuntime(t, time.Minute, func(ctx context.Context, _ error) (string, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		if dependency.stopped.Load() {
			t.Error("dependency stopped while its target was still running")
		}
		return "discarded", nil
	})
	caller := make(chan error, 1)
	go func() { _, err := handle.Invoke(context.Background(), nil); caller <- err }()
	awaitSignal(t, entered)
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := dispatcher.Drain(expired); !errors.Is(err, invocation.ErrDrain) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired drain = %v", err)
	}
	awaitSignal(t, cancelled)
	if err := awaitError(t, caller); !errors.Is(err, context.Canceled) || invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
		t.Fatalf("shutdown caller outcome = %v", err)
	}
	if !dispatcher.AdmissionClosed() || !dispatcher.Published() || dispatcher.ActiveAttempts() != 1 || dependency.stopped.Load() {
		t.Fatal("failed drain discarded the catalog, released the target, or stopped a dependency")
	}
	if _, err := handle.Invoke(context.Background(), nil); invocation.CompletionOf(err) != invocation.CompletionNotStarted || calls.Load() != 1 {
		t.Fatalf("closed admission accepted work: %v", err)
	}
	finished := make(chan error, 1)
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	go func() { finished <- dispatcher.Drain(ctx) }()
	select {
	case err := <-finished:
		t.Fatalf("drain completed before actual target termination: %v", err)
	default:
	}
	once.Do(func() { close(release) })
	if err := awaitError(t, finished); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if !dependency.stopped.Load() || dispatcher.ActiveAttempts() != 0 {
		t.Fatal("successful drain did not permit dependency cleanup")
	}
	drainDispatcher(t, dispatcher)
}

func TestDrainRejectsInvalidContextsWithoutClosingAdmission(t *testing.T) {
	_, dispatcher := publicErrorRuntime(t, time.Second, func(context.Context, error) (string, error) { return "", nil })
	for _, ctx := range []context.Context{nil, context.Background()} {
		if err := dispatcher.Drain(ctx); !errors.Is(err, invocation.ErrInvalidDrainContext) || dispatcher.AdmissionClosed() {
			t.Fatalf("invalid drain changed admission: %v", err)
		}
	}
	var nilDispatcher *invocation.Dispatcher
	for _, invalid := range []*invocation.Dispatcher{nilDispatcher, {}} {
		if err := invalid.Drain(context.Background()); !errors.Is(err, invocation.ErrInvalidDispatcher) || !invalid.AdmissionClosed() || invalid.ActiveAttempts() != 0 {
			t.Fatalf("invalid dispatcher drain = %v", err)
		}
	}
	drainDispatcher(t, dispatcher)

	unpublished, err := invocation.NewDispatcher(invocation.DispatcherOptions{DefaultTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	drainDispatcher(t, unpublished)
	catalog, err := invocation.NewCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := unpublished.Publish(catalog); !errors.Is(err, invocation.ErrDispatcherDraining) || unpublished.Published() {
		t.Fatalf("publication reopened a drained dispatcher: %v", err)
	}
}

func TestConcurrentDrainsShareTerminationButNotCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	handle, dispatcher := publicErrorRuntime(t, time.Minute, func(context.Context, error) (string, error) {
		close(entered)
		<-release
		return "", nil
	})
	caller := make(chan error, 1)
	go func() { _, err := handle.Invoke(context.Background(), nil); caller <- err }()
	awaitSignal(t, entered)
	short, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()
	if err := dispatcher.Drain(short); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled drain = %v", err)
	}
	awaitError(t, caller)
	const waiters = 16
	results := make(chan error, waiters)
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	for range waiters {
		go func() { results <- dispatcher.Drain(ctx) }()
	}
	once.Do(func() { close(release) })
	for range waiters {
		if err := awaitError(t, results); err != nil {
			t.Fatal(err)
		}
	}
	if dispatcher.ActiveAttempts() != 0 {
		t.Fatal("drain retained a terminated attempt")
	}
}

func TestTargetGoexitNeverProducesSuccessOrLeakedAttempt(t *testing.T) {
	handle, dispatcher := publicErrorRuntime(t, time.Second, func(context.Context, error) (string, error) {
		runtime.Goexit()
		return "unreachable", nil
	})
	response, err := handle.Invoke(context.Background(), nil)
	var boundary *invocation.Error
	if response != "" || !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorInternal || dispatcher.ActiveAttempts() != 0 {
		t.Fatalf("Goexit result = %q, %v, attempts %d", response, err, dispatcher.ActiveAttempts())
	}
	drainDispatcher(t, dispatcher)
}

func TestConcurrentAdmissionAndDrainLeaveNoTargets(t *testing.T) {
	for range 20 {
		var calls atomic.Int32
		handle, dispatcher := publicErrorRuntime(t, time.Second, func(ctx context.Context, _ error) (string, error) {
			calls.Add(1)
			<-ctx.Done()
			return "", ctx.Err()
		})
		start := make(chan struct{})
		var callers sync.WaitGroup
		for range 32 {
			callers.Go(func() {
				<-start
				if _, err := handle.Invoke(context.Background(), nil); err == nil {
					t.Error("shutdown target returned success")
				}
			})
		}
		close(start)
		drainDispatcher(t, dispatcher)
		callers.Wait()
		before := calls.Load()
		_, err := handle.Invoke(context.Background(), nil)
		if invocation.CompletionOf(err) != invocation.CompletionNotStarted || calls.Load() != before || dispatcher.ActiveAttempts() != 0 {
			t.Fatalf("post-drain invocation = %v", err)
		}
	}
}

func FuzzDispatcherDrainLifecycle(f *testing.F) {
	f.Add(byte(0), false)
	f.Add(byte(1), true)
	f.Add(byte(2), false)
	f.Add(byte(3), false)
	f.Fuzz(func(t *testing.T, outcome byte, preCancelled bool) {
		entered := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		var calls atomic.Int32
		handle, dispatcher := publicErrorRuntime(t, time.Second, func(context.Context, error) (string, error) {
			calls.Add(1)
			close(entered)
			<-release
			switch outcome % 4 {
			case 1:
				return "", invocation.NewResultUnknown(nil)
			case 2:
				panic("private target panic")
			case 3:
				runtime.Goexit()
			}
			return "discarded", nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if preCancelled {
			cancel()
			_, err := handle.Invoke(ctx, nil)
			if invocation.CompletionOf(err) != invocation.CompletionNotStarted || calls.Load() != 0 {
				t.Fatal("pre-cancelled call reached target")
			}
		} else {
			caller := make(chan error, 1)
			go func() { _, err := handle.Invoke(ctx, nil); caller <- err }()
			awaitSignal(t, entered)
			cancel()
			if err := awaitError(t, caller); invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
				t.Fatal("entered cancellation lost uncertainty")
			}
			expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			err := dispatcher.Drain(expired)
			stop()
			if !errors.Is(err, invocation.ErrDrain) || dispatcher.ActiveAttempts() != 1 {
				t.Fatal("failed drain released live target")
			}
		}
		once.Do(func() { close(release) })
		drainDispatcher(t, dispatcher)
		if dispatcher.ActiveAttempts() != 0 || !dispatcher.AdmissionClosed() {
			t.Fatal("drain did not reach terminal state")
		}
	})
}

// Includes fresh dispatcher/catalog construction because a drain is permanent.
func BenchmarkCancelledTargetDrain(b *testing.B) {
	for b.Loop() {
		entered := make(chan struct{})
		handle, dispatcher := publicErrorRuntime(b, time.Second, func(ctx context.Context, _ error) (string, error) {
			close(entered)
			<-ctx.Done()
			return "", ctx.Err()
		})
		caller := make(chan error, 1)
		go func() { _, err := handle.Invoke(context.Background(), nil); caller <- err }()
		<-entered
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := dispatcher.Drain(ctx)
		cancel()
		if err != nil {
			b.Fatal(err)
		}
		<-caller
	}
}

type drainDependency struct{ stopped atomic.Bool }

func (*drainDependency) Start(context.Context) error  { return nil }
func (d *drainDependency) Stop(context.Context) error { d.stopped.Store(true); return nil }

func drainDispatcher(t testing.TB, dispatcher *invocation.Dispatcher) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dispatcher.Drain(ctx); err != nil {
		t.Fatal(err)
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for target signal")
	}
}

func awaitError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for completion")
		return nil
	}
}
