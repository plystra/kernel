package invocation_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
	"github.com/plystra/kernel/lifecycle"
)

func TestPublishDoesNotOpenAdmission(t *testing.T) {
	var calls atomic.Int32
	handle, dispatcher, catalog := startupRuntime(t, func(context.Context, string) (string, error) {
		calls.Add(1)
		return "unexpected target result", nil
	})
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	if !handle.Available() {
		t.Fatal("startup readiness changed resolved optional availability")
	}
	value, err := handle.Invoke(context.Background(), "request")
	var boundary *invocation.Error
	if value != "" || !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorUnavailable ||
		boundary.DetailCode() != "runtime.dispatcher_not_ready" || boundary.Completion() != invocation.CompletionNotStarted ||
		calls.Load() != 0 || dispatcher.ActiveAttempts() != 0 {
		t.Fatalf("published but unready dispatch = %q, %v, target calls %d", value, err, calls.Load())
	}
}

func TestOpenAdmissionRequiresValidPublishedDispatcher(t *testing.T) {
	for _, dispatcher := range []*invocation.Dispatcher{nil, {}} {
		if err := dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrInvalidDispatcher) || dispatcher.Accepting() {
			t.Fatalf("invalid dispatcher accepted opening: %v", err)
		}
	}
	handle, dispatcher, catalog := startupRuntime(t, func(_ context.Context, value string) (string, error) { return value, nil })
	if err := dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherNotReady) || dispatcher.Accepting() || dispatcher.Published() || dispatcher.AdmissionClosed() {
		t.Fatalf("unpublished opening = %v", err)
	}
	if err := dispatcher.Publish(invocation.Catalog{}); !errors.Is(err, invocation.ErrInvalidCatalog) {
		t.Fatal(err)
	}
	if err := dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherNotReady) {
		t.Fatal("invalid publication permitted opening", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	if !dispatcher.Published() || dispatcher.Accepting() || dispatcher.AdmissionClosed() {
		t.Fatal("publication changed admission")
	}
	for range 2 {
		if err := dispatcher.OpenAdmission(); err != nil || !dispatcher.Accepting() || dispatcher.AdmissionClosed() {
			t.Fatalf("opening = %v", err)
		}
		if value, err := handle.Invoke(context.Background(), "ready"); err != nil || value != "ready" {
			t.Fatalf("ready invocation = %q, %v", value, err)
		}
	}
	if err := dispatcher.Publish(catalog); !errors.Is(err, invocation.ErrCatalogPublished) {
		t.Fatal("opening permitted catalog replacement", err)
	}
	drainDispatcher(t, dispatcher)
	if err := dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherDraining) || dispatcher.Accepting() {
		t.Fatalf("reopening = %v", err)
	}
}

func TestEmptyCatalogCanOpenAdmission(t *testing.T) {
	_, dispatcher, _ := startupRuntime(t, func(context.Context, string) (string, error) { return "", nil })
	empty, err := invocation.NewCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(empty); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.OpenAdmission(); err != nil || !dispatcher.Accepting() {
		t.Fatalf("empty catalog opening = %v", err)
	}
}

func TestUnreadyAdmissionRejectsEveryInvocationSurface(t *testing.T) {
	for _, surface := range []string{"raw", "response", "preparation"} {
		t.Run(surface, func(t *testing.T) {
			var calls, preparations, responses atomic.Int32
			handle, dispatcher, catalog := startupRuntime(t, func(_ context.Context, value string) (string, error) {
				calls.Add(1)
				return value, nil
			})
			if err := dispatcher.Publish(catalog); err != nil {
				t.Fatal(err)
			}
			prepare := func(value string) (string, error) { preparations.Add(1); return value, nil }
			process := func(value string) (string, error) { responses.Add(1); return value, nil }
			var value string
			var err error
			switch surface {
			case "raw":
				value, err = handle.Invoke(context.Background(), "private request")
			case "response":
				value, err = handle.InvokeWithResponse(context.Background(), "private request", process)
			case "preparation":
				value, err = handle.InvokeWithPreparation(context.Background(), "private request", prepare, process)
			}
			assertStartupRejection(t, value, err, "runtime.dispatcher_not_ready", 0)
			if calls.Load() != 0 || preparations.Load() != 0 || responses.Load() != 0 || dispatcher.ActiveAttempts() != 0 {
				t.Fatal("unready invocation performed work or retained admission")
			}
			if err := dispatcher.OpenAdmission(); err != nil {
				t.Fatal(err)
			}
			value, err = handle.InvokeWithPreparation(context.Background(), "ready", prepare, process)
			if err != nil || value != "ready" || calls.Load() != 1 || preparations.Load() != 1 || responses.Load() != 1 {
				t.Fatalf("ready invocation = %q, %v", value, err)
			}
		})
	}
}

func TestUnreadyAdmissionPreservesCallerCancellation(t *testing.T) {
	handle, dispatcher, catalog := startupRuntime(t, func(context.Context, string) (string, error) {
		t.Error("cancelled unready call entered target")
		return "", nil
	})
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, ctx := range []context.Context{cancelled, expired} {
		value, err := handle.Invoke(ctx, "")
		if value != "" || !errors.Is(err, ctx.Err()) || invocation.CompletionOf(err) != invocation.CompletionNotStarted {
			t.Fatalf("cancelled unready invocation = %q, %v", value, err)
		}
	}
}

func TestDrainBeforeOpeningPermanentlyPreventsAdmission(t *testing.T) {
	for _, published := range []bool{false, true} {
		for _, expired := range []bool{false, true} {
			_, dispatcher, catalog := startupRuntime(t, func(context.Context, string) (string, error) { return "", nil })
			if published {
				if err := dispatcher.Publish(catalog); err != nil {
					t.Fatal(err)
				}
			}
			deadline := time.Now().Add(time.Minute)
			if expired {
				deadline = time.Now().Add(-time.Second)
			}
			ctx, cancel := context.WithDeadline(context.Background(), deadline)
			err := dispatcher.Drain(ctx)
			cancel()
			if expired && !errors.Is(err, context.DeadlineExceeded) || !expired && err != nil {
				t.Fatalf("drain before startup = %v", err)
			}
			if err := dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherDraining) || dispatcher.Accepting() || !dispatcher.AdmissionClosed() || dispatcher.Published() != published {
				t.Fatalf("opening after drain = %v", err)
			}
			drainDispatcher(t, dispatcher)
		}
	}
}

func TestDrainDuringPreparationCannotAdmitTarget(t *testing.T) {
	handle, dispatcher, catalog := startupRuntime(t, func(context.Context, string) (string, error) {
		t.Error("drain during preparation admitted target")
		return "", nil
	})
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.OpenAdmission(); err != nil {
		t.Fatal(err)
	}
	value, err := handle.InvokeWithPreparation(context.Background(), "private request", func(value string) (string, error) {
		drainDispatcher(t, dispatcher)
		return value, nil
	}, func(value string) (string, error) {
		t.Error("drain during preparation admitted response processing")
		return value, nil
	})
	assertStartupRejection(t, value, err, "runtime.dispatcher_draining", 1)
	if dispatcher.ActiveAttempts() != 0 || dispatcher.Accepting() {
		t.Fatal("closed dispatcher retained admission")
	}
}

func TestConcurrentOpeningInvocationAndDrainCannotReopenAdmission(t *testing.T) {
	for range 32 {
		handle, dispatcher, catalog := startupRuntime(t, func(_ context.Context, value string) (string, error) { return value, nil })
		if err := dispatcher.Publish(catalog); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var group sync.WaitGroup
		for range 16 {
			group.Go(func() {
				<-start
				if err := dispatcher.OpenAdmission(); err != nil && !errors.Is(err, invocation.ErrDispatcherDraining) {
					t.Error(err)
				}
				value, err := handle.Invoke(context.Background(), "ready")
				if err == nil {
					if value != "ready" {
						t.Errorf("ready response = %q", value)
					}
					return
				}
				var boundary *invocation.Error
				if value != "" || !errors.As(err, &boundary) || (boundary.Code() != invocation.ErrorUnavailable && boundary.Code() != invocation.ErrorCancelled) {
					t.Errorf("concurrent invocation = %q, %v", value, err)
				}
			})
		}
		group.Go(func() {
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := dispatcher.Drain(ctx); err != nil {
				t.Error(err)
			}
		})
		close(start)
		group.Wait()
		if dispatcher.Accepting() || !dispatcher.AdmissionClosed() || !dispatcher.Published() || dispatcher.ActiveAttempts() != 0 {
			t.Fatal("concurrent opening escaped shutdown")
		}
		if err := dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherDraining) {
			t.Fatal("drained dispatcher reopened", err)
		}
	}
}

func TestSharedCatalogDoesNotShareAdmission(t *testing.T) {
	handle, dispatcher, catalog := startupRuntime(t, func(_ context.Context, value string) (string, error) { return value, nil })
	_, other, _ := startupRuntime(t, func(context.Context, string) (string, error) { return "", nil })
	for _, runtime := range []*invocation.Dispatcher{dispatcher, other} {
		if err := runtime.Publish(catalog); err != nil {
			t.Fatal(err)
		}
	}
	if err := dispatcher.OpenAdmission(); err != nil {
		t.Fatal(err)
	}
	otherHandle, err := invocation.NewHandle(other, policyTestContract, true)
	if err != nil {
		t.Fatal(err)
	}
	value, err := otherHandle.Invoke(context.Background(), "")
	assertStartupRejection(t, value, err, "runtime.dispatcher_not_ready", 0)
	drainDispatcher(t, other)
	if value, err := handle.Invoke(context.Background(), "independent"); err != nil || value != "independent" {
		t.Fatalf("other dispatcher changed live admission = %q, %v", value, err)
	}
}

func TestAssemblyCanHoldAdmissionAcrossDependencyOrderedStartup(t *testing.T) {
	for _, outcome := range []string{"success", "error", "panic", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			var started, stopped, calls atomic.Int32
			handle, dispatcher, catalog := startupRuntime(t, func(_ context.Context, value string) (string, error) {
				calls.Add(1)
				return value, nil
			})
			if err := dispatcher.Publish(catalog); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var bindings []lifecycle.Binding
			for _, symbol := range []string{"example.com/startup.NewDependency", "example.com/startup.NewConsumer"} {
				binding, err := lifecycle.NewBinding(symbol, startupInstance{
					start: func(context.Context) error {
						if started.Add(1) == 1 {
							return nil
						}
						close(entered)
						<-release
						switch outcome {
						case "error":
							return errors.New("private acquisition failure")
						case "panic":
							panic("private acquisition panic")
						case "cancel":
							cancel()
						}
						return nil
					},
					stop: func(context.Context) error { stopped.Add(1); return nil },
				})
				if err != nil {
					t.Fatal(err)
				}
				bindings = append(bindings, binding)
			}
			manager, err := lifecycle.NewManager(lifecycle.ManagerOptions{RollbackTimeout: time.Second}, bindings)
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				err := manager.Start(ctx)
				if err == nil {
					err = dispatcher.OpenAdmission()
				}
				finished <- err
			}()
			awaitSignal(t, entered)
			value, err := handle.Invoke(context.Background(), "too early")
			assertStartupRejection(t, value, err, "runtime.dispatcher_not_ready", 0)
			if started.Load() != 2 || calls.Load() != 0 || dispatcher.Accepting() {
				t.Fatal("partial startup accepted work")
			}
			once.Do(func() { close(release) })
			err = awaitError(t, finished)
			if outcome == "success" {
				if err != nil || !dispatcher.Accepting() || stopped.Load() != 0 {
					t.Fatalf("startup = %v", err)
				}
				if value, err := handle.Invoke(context.Background(), "ready"); err != nil || value != "ready" {
					t.Fatalf("ready call = %q, %v", value, err)
				}
			} else {
				if !errors.Is(err, lifecycle.ErrStart) || dispatcher.Accepting() || stopped.Load() != 2 {
					t.Fatalf("failed startup = %v, cleaned %d", err, stopped.Load())
				}
				value, err := handle.Invoke(context.Background(), "failed startup")
				assertStartupRejection(t, value, err, "runtime.dispatcher_not_ready", 0)
			}
			drainDispatcher(t, dispatcher)
			if err := manager.Stop(context.Background()); err != nil || stopped.Load() != 2 {
				t.Fatalf("cleanup = %v, cleaned %d", err, stopped.Load())
			}
		})
	}
}

type startupInstance struct {
	start func(context.Context) error
	stop  func(context.Context) error
}

func (instance startupInstance) Start(ctx context.Context) error { return instance.start(ctx) }
func (instance startupInstance) Stop(ctx context.Context) error  { return instance.stop(ctx) }

func FuzzDispatcherAdmissionSequence(f *testing.F) {
	f.Add([]byte{1, 2, 0, 2, 1, 2, 3, 1, 2})
	f.Add([]byte{3, 0, 1, 2})
	f.Fuzz(func(t *testing.T, operations []byte) {
		if len(operations) > 64 {
			operations = operations[:64]
		}
		var calls atomic.Int32
		handle, dispatcher, catalog := startupRuntime(t, func(_ context.Context, value string) (string, error) {
			calls.Add(1)
			return value, nil
		})
		published, opened, drained := false, false, false
		for _, operation := range operations {
			switch operation % 4 {
			case 0:
				err := dispatcher.Publish(catalog)
				switch {
				case drained:
					if !errors.Is(err, invocation.ErrDispatcherDraining) {
						t.Fatal(err)
					}
				case published:
					if !errors.Is(err, invocation.ErrCatalogPublished) {
						t.Fatal(err)
					}
				default:
					if err != nil {
						t.Fatal(err)
					}
					published = true
				}
			case 1:
				err := dispatcher.OpenAdmission()
				switch {
				case drained:
					if !errors.Is(err, invocation.ErrDispatcherDraining) {
						t.Fatal(err)
					}
				case !published:
					if !errors.Is(err, invocation.ErrDispatcherNotReady) {
						t.Fatal(err)
					}
				default:
					if err != nil {
						t.Fatal(err)
					}
					opened = true
				}
			case 2:
				before := calls.Load()
				value, err := handle.Invoke(context.Background(), "ready")
				if opened && !drained {
					if err != nil || value != "ready" || calls.Load() != before+1 {
						t.Fatalf("ready call = %q, %v", value, err)
					}
				} else {
					detail := "runtime.dispatcher_not_ready"
					if drained && published {
						detail = "runtime.dispatcher_draining"
					}
					assertStartupRejection(t, value, err, detail, 0)
					if calls.Load() != before {
						t.Fatal("closed admission entered target")
					}
				}
			case 3:
				drainDispatcher(t, dispatcher)
				drained = true
			}
			if dispatcher.Published() != published || dispatcher.Accepting() != (opened && !drained) || dispatcher.AdmissionClosed() != drained || dispatcher.ActiveAttempts() != 0 {
				t.Fatal("admission state does not match operation sequence")
			}
		}
	})
}

func assertStartupRejection(t testing.TB, value string, err error, detail string, attempts int) {
	t.Helper()
	var boundary *invocation.Error
	if value != "" || !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorUnavailable ||
		boundary.DetailCode() != detail || boundary.Completion() != invocation.CompletionNotStarted || boundary.Attempts() != attempts {
		t.Fatalf("startup rejection = %q, %v; want %s with %d attempts", value, err, detail, attempts)
	}
}

func startupRuntime(t testing.TB, handler capability.Handler[string, string]) (invocation.Handle[string, string], *invocation.Dispatcher, invocation.Catalog) {
	t.Helper()
	binding, err := policyBinding(t, publicPolicy(0, 64), policyTestContract, handler)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := invocation.NewCatalog([]invocation.Binding{binding})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := invocation.NewHandle(dispatcher, policyTestContract, true)
	if err != nil {
		t.Fatal(err)
	}
	return handle, dispatcher, catalog
}
