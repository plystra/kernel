package lifecycle_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
	"github.com/plystra/kernel/lifecycle"
)

func TestGovernedLifecycleHooksUseOnlyReadyDependencies(t *testing.T) {
	var fixture governedFixture
	var events []string
	var retained context.Context
	fixture = newGovernedFixture(t, []governedMember{
		{name: "Plain"},
		{name: "Dependency", instance: &testLifecycleInstance{
			start: func(ctx context.Context) error {
				events = append(events, "start:dependency")
				assertGovernedCall(t, fixture, ctx, "Plain")
				assertGovernedUnavailable(t, fixture, ctx, "Dependency")
				assertGovernedUnavailable(t, fixture, ctx, "Consumer")
				return nil
			},
			stop: func(ctx context.Context) error {
				events = append(events, "stop:dependency")
				assertGovernedCall(t, fixture, ctx, "Plain")
				assertGovernedUnavailable(t, fixture, ctx, "Consumer")
				assertGovernedUnavailable(t, fixture, ctx, "Dependency")
				return nil
			},
		}},
		{name: "Consumer", instance: &testLifecycleInstance{
			start: func(ctx context.Context) error {
				events = append(events, "start:consumer")
				retained = context.WithoutCancel(ctx)
				assertGovernedCall(t, fixture, retained, "Dependency")
				assertGovernedUnavailable(t, fixture, context.Background(), "Dependency")
				assertGovernedUnavailable(t, fixture, ctx, "Consumer")
				if err := fixture.dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherNotReady) {
					t.Errorf("hook opened public admission: %v", err)
				}
				return nil
			},
			stop: func(ctx context.Context) error {
				events = append(events, "stop:consumer")
				assertGovernedCall(t, fixture, ctx, "Dependency")
				assertGovernedUnavailable(t, fixture, context.Background(), "Dependency")
				return nil
			},
		}},
	})
	ctx := governedContext(t)
	assertGovernedUnavailable(t, fixture, ctx, "Plain")
	if err := fixture.dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherNotReady) {
		t.Fatalf("unstarted manager opened admission: %v", err)
	}
	if err := fixture.manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	assertGovernedUnavailable(t, fixture, ctx, "Consumer")
	if err := fixture.dispatcher.OpenAdmission(); err != nil {
		t.Fatal(err)
	}
	assertGovernedCall(t, fixture, ctx, "Consumer")
	value, err := fixture.handles["Dependency"].Invoke(retained, "stale scope")
	if value != "" || !errors.Is(err, context.Canceled) || invocation.CompletionOf(err) != invocation.CompletionNotStarted {
		t.Fatalf("retained hook context escaped scope: %q, %v", value, err)
	}
	if err := fixture.manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"start:dependency", "start:consumer", "stop:consumer", "stop:dependency"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("lifecycle order = %v", events)
	}
	if fixture.dispatcher.Accepting() || !fixture.dispatcher.AdmissionClosed() || fixture.dispatcher.ActiveAttempts() != 0 {
		t.Fatal("cleanup reopened public admission or retained work")
	}
}

func TestGovernedLifecycleNestedCallsKeepPolicyAndHookDeadline(t *testing.T) {
	var fixture governedFixture
	var leafCalls atomic.Int32
	var expectedDeadline time.Time
	fixture = newGovernedFixture(t, []governedMember{
		{name: "Leaf", handler: func(ctx context.Context, value string) (string, error) {
			leafCalls.Add(1)
			frame, exists := invocation.Current(ctx)
			deadline, bounded := ctx.Deadline()
			if !exists || !frame.ParentInvocationID().Valid() || !bounded || deadline.After(expectedDeadline) {
				t.Error("nested hook call lost ancestry or deadline")
			}
			return value, nil
		}},
		{name: "Middle", instance: &testLifecycleInstance{}, handler: func(ctx context.Context, value string) (string, error) {
			return fixture.handles["Leaf"].Invoke(ctx, value)
		}},
		{name: "Consumer", instance: &testLifecycleInstance{
			start: func(ctx context.Context) error {
				expectedDeadline, _ = ctx.Deadline()
				assertGovernedCall(t, fixture, context.WithoutCancel(ctx), "Middle")
				return nil
			},
			stop: func(ctx context.Context) error {
				expectedDeadline, _ = ctx.Deadline()
				assertGovernedCall(t, fixture, context.WithoutCancel(ctx), "Middle")
				return nil
			},
		}},
	})
	ctx := governedContext(t)
	if err := fixture.manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Stop(ctx); err != nil || leafCalls.Load() != 2 {
		t.Fatalf("nested cleanup = %v; calls %d", err, leafCalls.Load())
	}
}

func TestGovernedLifecycleFailureCleansEveryConstructedValue(t *testing.T) {
	for _, outcome := range []string{"error", "panic", "cancel", "before-start"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(governedContext(t))
			defer cancel()
			var fixture governedFixture
			var stopped []string
			fixture = newGovernedFixture(t, []governedMember{
				{name: "Dependency", instance: &testLifecycleInstance{stop: func(context.Context) error {
					stopped = append(stopped, "dependency")
					return nil
				}}},
				{name: "Failing", instance: &testLifecycleInstance{
					start: func(ctx context.Context) error {
						assertGovernedCall(t, fixture, ctx, "Dependency")
						switch outcome {
						case "error":
							return errors.New("private hook failure")
						case "panic":
							panic("private hook panic")
						case "cancel":
							cancel()
						}
						return nil
					},
					stop: func(cleanup context.Context) error {
						stopped = append(stopped, "failing")
						if outcome != "before-start" {
							assertGovernedCall(t, fixture, cleanup, "Dependency")
						}
						return nil
					},
				}},
				{name: "NeverStarted", instance: &testLifecycleInstance{
					start: func(context.Context) error { t.Error("later Start entered after failure"); return nil },
					stop: func(cleanup context.Context) error {
						stopped = append(stopped, "never-started")
						assertGovernedUnavailable(t, fixture, cleanup, "Failing")
						return nil
					},
				}},
			})
			if outcome == "before-start" {
				cancel()
			}
			err := fixture.manager.Start(ctx)
			if !errors.Is(err, lifecycle.ErrStart) || strings.Contains(err.Error(), "private") || fixture.dispatcher.Accepting() {
				t.Fatalf("startup failure = %v", err)
			}
			if want := []string{"never-started", "failing", "dependency"}; !reflect.DeepEqual(stopped, want) {
				t.Fatalf("rollback = %v", stopped)
			}
			if err := fixture.manager.Stop(governedContext(t)); err != nil || len(stopped) != 3 {
				t.Fatalf("cleanup retry = %v; stopped %v", err, stopped)
			}
		})
	}
}

func TestGovernedLifecycleRetainsLateHookTargetsBeforeCleanup(t *testing.T) {
	for _, mode := range []string{"start-target", "stop-target", "start-response", "stop-response", "resource-start-target", "resource-stop-target", "resource-start-response", "resource-stop-response"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				phase, _, _ := strings.Cut(strings.TrimPrefix(mode, "resource-"), "-")
				processing := strings.HasSuffix(mode, "response")
				resourceName := ""
				if strings.HasPrefix(mode, "resource-") {
					resourceName = "database.consumer"
				}
				var fixture governedFixture
				var dependentStops, dependencyStops, resourceStops atomic.Int32
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				defer once.Do(func() { close(release) })
				hold := func(value string) string {
					close(entered)
					<-release
					if dependencyStops.Load() != 0 || resourceStops.Load() != 0 {
						t.Error("dependency stopped before its late target or response processor")
					}
					return value
				}
				target := func(ctx context.Context) error {
					if processing {
						_, err := fixture.handles["Dependency"].InvokeWithResponse(ctx, "", func(value string) (string, error) { return hold(value), nil })
						return err
					}
					_, err := fixture.handles["Dependency"].Invoke(ctx, "")
					return err
				}
				fixture = newGovernedFixture(t, []governedMember{
					{name: "Resource", resourceName: "database.primary", constructor: resourceProvider, instance: &testLifecycleInstance{stop: func(context.Context) error {
						resourceStops.Add(1)
						return nil
					}}},
					{name: "Dependency", instance: &testLifecycleInstance{stop: func(context.Context) error {
						dependencyStops.Add(1)
						return nil
					}}, handler: func(context.Context, string) (string, error) {
						if !processing {
							return hold("late"), nil
						}
						return "response", nil
					}},
					{name: "Consumer", resourceName: resourceName, instance: &testLifecycleInstance{
						start: func(ctx context.Context) error {
							if phase == "start" {
								return target(ctx)
							}
							return nil
						},
						stop: func(ctx context.Context) error {
							if dependentStops.Add(1) == 1 && phase == "stop" {
								return target(ctx)
							}
							return nil
						},
					}},
				})
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				result := make(chan error, 1)
				go func() {
					err := fixture.manager.Start(ctx)
					if phase == "stop" && err == nil {
						err = fixture.manager.Stop(ctx)
					}
					result <- err
				}()
				<-entered
				err := <-result
				if err == nil || !errors.Is(err, context.DeadlineExceeded) || dependencyStops.Load() != 0 || resourceStops.Load() != 0 || fixture.dispatcher.ActiveAttempts() != 1 {
					t.Fatalf("bounded %s = %v, stopped %d, attempts %d", phase, err, dependencyStops.Load(), fixture.dispatcher.ActiveAttempts())
				}
				if err := fixture.dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherDraining) {
					t.Fatal("failed cleanup reopened admission", err)
				}
				once.Do(func() { close(release) })
				synctest.Wait()
				if err := fixture.manager.Stop(governedContext(t)); err != nil || dependencyStops.Load() != 1 || resourceStops.Load() != 1 || fixture.dispatcher.ActiveAttempts() != 0 {
					t.Fatalf("cleanup retry = %v, stops %d", err, dependencyStops.Load())
				}
			})
		})
	}
}

func TestGovernedDrainWaitsForHookBodyAfterRevokingItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fixture governedFixture
		var stops atomic.Int32
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		defer once.Do(func() { close(release) })
		fixture = newGovernedFixture(t, []governedMember{
			{name: "Plain"},
			{name: "Consumer", instance: &testLifecycleInstance{
				start: func(ctx context.Context) error {
					assertGovernedCall(t, fixture, ctx, "Plain")
					close(entered)
					<-release
					value, err := fixture.handles["Plain"].Invoke(context.WithoutCancel(ctx), "revoked")
					if value != "" || !errors.Is(err, context.Canceled) {
						t.Errorf("drain did not revoke hook context: %q, %v", value, err)
					}
					return nil
				},
				stop: func(context.Context) error { stops.Add(1); return nil },
			}},
		})
		started := make(chan error, 1)
		ctx := governedContext(t)
		go func() { started <- fixture.manager.Start(ctx) }()
		<-entered
		drain, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := fixture.dispatcher.Drain(drain); !errors.Is(err, invocation.ErrDrain) || !errors.Is(err, context.DeadlineExceeded) || stops.Load() != 0 {
			t.Fatalf("drain ignored active hook body: %v", err)
		}
		if err := fixture.manager.Stop(ctx); !errors.Is(err, lifecycle.ErrState) {
			t.Fatalf("concurrent Stop entered during active Start: %v", err)
		}
		once.Do(func() { close(release) })
		if err := <-started; !errors.Is(err, lifecycle.ErrStart) || stops.Load() != 1 {
			t.Fatalf("interrupted startup = %v, stops %d", err, stops.Load())
		}
		if err := fixture.manager.Stop(ctx); err != nil || stops.Load() != 1 {
			t.Fatalf("cleanup repeated completed hook: %v", err)
		}
	})
}

func TestGovernedStopHooksRetainRetryPolicyAfterPublicDrain(t *testing.T) {
	var fixture governedFixture
	var calls atomic.Int32
	policy := invocation.Policy{SchemaVersion: invocation.PolicySchemaVersion, CompilerVersion: invocation.PolicyCompilerVersion,
		DefaultsVersion: invocation.PolicyDefaultsVersion, Timeout: time.Second, ConcurrencyLimit: 1,
		Retry: invocation.RetryPolicy{Eligibility: invocation.RetryReplaySafe, MaxAttempts: 2, Backoff: time.Millisecond}}
	fixture = newGovernedFixture(t, []governedMember{
		{name: "Dependency", instance: &testLifecycleInstance{}, policy: &policy,
			handler: func(_ context.Context, value string) (string, error) {
				if calls.Add(1)%2 == 1 {
					err, _ := invocation.NewError(invocation.ErrorUnavailable, "test.transient")
					return "", err
				}
				return value, nil
			}},
		{name: "Consumer", instance: &testLifecycleInstance{
			start: func(ctx context.Context) error { assertGovernedCall(t, fixture, ctx, "Dependency"); return nil },
			stop:  func(ctx context.Context) error { assertGovernedCall(t, fixture, ctx, "Dependency"); return nil },
		}},
	})
	ctx := governedContext(t)
	if err := fixture.manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Stop(ctx); err != nil || calls.Load() != 4 {
		t.Fatalf("hook policy execution = %v, calls %d", err, calls.Load())
	}
}

func TestGovernedCleanupRetryKeepsDependenciesReady(t *testing.T) {
	for _, phase := range []string{"shutdown", "rollback"} {
		t.Run(phase, func(t *testing.T) {
			var fixture governedFixture
			var live atomic.Bool
			var calls atomic.Int32
			var events []string
			attempts := 0
			fixture = newGovernedFixture(t, []governedMember{
				{name: "Resource", resourceName: "database.primary", constructor: resourceProvider, instance: &testLifecycleInstance{
					start: func(context.Context) error { live.Store(true); return nil },
					stop:  func(context.Context) error { events = append(events, "resource"); live.Store(false); return nil },
				}},
				{name: "Dependency", instance: &testLifecycleInstance{
					stop: func(context.Context) error { events = append(events, "dependency"); return nil },
				}, handler: func(_ context.Context, value string) (string, error) {
					calls.Add(1)
					if !live.Load() {
						return "", errors.New("Resource dependency already stopped")
					}
					return value, nil
				}},
				{name: "Consumer", instance: &testLifecycleInstance{
					start: func(context.Context) error {
						if phase == "rollback" {
							return errors.New("private startup failure")
						}
						return nil
					},
					stop: func(ctx context.Context) error {
						events = append(events, "consumer")
						attempts++
						assertGovernedCall(t, fixture, ctx, "Dependency")
						assertGovernedUnavailable(t, fixture, ctx, "Consumer")
						assertGovernedUnavailable(t, fixture, ctx, "Completed")
						if attempts == 1 {
							return errors.New("private cleanup failure")
						}
						return nil
					},
				}},
				{name: "Completed", instance: &testLifecycleInstance{
					stop: func(context.Context) error { events = append(events, "completed"); return nil },
				}},
			})
			err := fixture.manager.Start(governedContext(t))
			if phase == "shutdown" {
				if err != nil {
					t.Fatal(err)
				}
				if err := fixture.dispatcher.OpenAdmission(); err != nil {
					t.Fatal(err)
				}
				err = fixture.manager.Stop(governedContext(t))
			} else if !errors.Is(err, lifecycle.ErrStart) {
				t.Fatalf("startup rollback = %v", err)
			}
			if !errors.Is(err, lifecycle.ErrStop) || strings.Contains(err.Error(), "private") || !live.Load() || !reflect.DeepEqual(events, []string{"completed", "consumer"}) {
				t.Fatalf("cleanup did not retain dependencies: %v, stops %v", err, events)
			}
			assertGovernedUnavailable(t, fixture, governedContext(t), "Dependency")
			if err := fixture.manager.Stop(governedContext(t)); err != nil || live.Load() || calls.Load() != 2 || fixture.manager.State() != lifecycle.StateStopped {
				t.Fatalf("retry = %v, Resource live %t, calls %d, state %s", err, live.Load(), calls.Load(), fixture.manager.State())
			}
			want := []string{"completed", "consumer", "consumer", "dependency", "resource"}
			if err := fixture.manager.Stop(governedContext(t)); err != nil || !reflect.DeepEqual(events, want) {
				t.Fatalf("cleanup order or successful-stop retention = %v, stops %v", err, events)
			}
			if err := fixture.dispatcher.OpenAdmission(); !errors.Is(err, invocation.ErrDispatcherDraining) {
				t.Fatalf("cleanup reopened public admission: %v", err)
			}
		})
	}
}

func TestGovernedReadinessFollowsConstructorAcrossBindings(t *testing.T) {
	var fixture governedFixture
	fixture = newGovernedFixture(t, []governedMember{
		{name: "Dependency", instance: &testLifecycleInstance{
			start: func(ctx context.Context) error { assertGovernedUnavailable(t, fixture, ctx, "Alias"); return nil },
			stop:  func(ctx context.Context) error { assertGovernedUnavailable(t, fixture, ctx, "Alias"); return nil },
		}},
		{name: "Alias", constructor: "example.com/lifecycle.NewDependency"},
		{name: "Consumer", instance: &testLifecycleInstance{
			start: func(ctx context.Context) error { assertGovernedCall(t, fixture, ctx, "Alias"); return nil },
			stop:  func(ctx context.Context) error { assertGovernedCall(t, fixture, ctx, "Alias"); return nil },
		}},
	})
	ctx := governedContext(t)
	if err := fixture.manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestGovernedSuccessfulStopSurvivesCancellation(t *testing.T) {
	for _, last := range []bool{false, true} {
		ctx, cancel := context.WithCancel(governedContext(t))
		var dependencyStops, consumerStops atomic.Int32
		fixture := newGovernedFixture(t, []governedMember{
			{name: "Dependency", instance: &testLifecycleInstance{stop: func(context.Context) error {
				dependencyStops.Add(1)
				if last {
					cancel()
				}
				return nil
			}}},
			{name: "Consumer", instance: &testLifecycleInstance{stop: func(context.Context) error {
				consumerStops.Add(1)
				if !last {
					cancel()
				}
				return nil
			}}},
		})
		if err := fixture.manager.Start(ctx); err != nil {
			t.Fatal(err)
		}
		err := fixture.manager.Stop(ctx)
		cancel()
		if last && err != nil || !last && !errors.Is(err, context.Canceled) || consumerStops.Load() != 1 {
			t.Fatalf("cancelled cleanup = %v", err)
		}
		if err := fixture.manager.Stop(governedContext(t)); err != nil || consumerStops.Load() != 1 || dependencyStops.Load() != 1 {
			t.Fatalf("cleanup retry repeated successful hook: %v", err)
		}
	}
}

func TestGovernedLifecycleRejectsUnboundedContextsWithoutTransition(t *testing.T) {
	fixture := newGovernedFixture(t, nil)
	if err := fixture.manager.Start(context.Background()); !errors.Is(err, lifecycle.ErrInvalidContext) || fixture.manager.State() != lifecycle.StateNew {
		t.Fatalf("unbounded Start = %v", err)
	}
	if err := fixture.manager.Stop(context.Background()); !errors.Is(err, lifecycle.ErrInvalidContext) || fixture.dispatcher.AdmissionClosed() {
		t.Fatalf("unbounded Stop = %v", err)
	}
	ctx := governedContext(t)
	if err := fixture.manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixture.dispatcher.OpenAdmission(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Stop(ctx); err != nil || fixture.dispatcher.Accepting() {
		t.Fatalf("empty lifecycle cleanup = %v", err)
	}
}

func TestHookScopeDoesNotOpenAnotherDispatcher(t *testing.T) {
	other := newGovernedFixture(t, []governedMember{{name: "Plain"}})
	fixture := newGovernedFixture(t, []governedMember{
		{name: "Consumer", instance: &testLifecycleInstance{
			start: func(ctx context.Context) error { assertGovernedUnavailable(t, other, ctx, "Plain"); return nil },
			stop:  func(ctx context.Context) error { assertGovernedUnavailable(t, other, ctx, "Plain"); return nil },
		}},
	})
	ctx := governedContext(t)
	if err := fixture.manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fixture.manager.Stop(ctx); err != nil || other.dispatcher.Accepting() || other.dispatcher.AdmissionClosed() {
		t.Fatalf("scope affected another dispatcher: %v", err)
	}
}

func TestGovernedCleanupBeforeCatalogPublication(t *testing.T) {
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	var stops atomic.Int32
	member, err := lifecycle.NewBinding("example.com/lifecycle.NewPartial", &testLifecycleInstance{stop: func(context.Context) error {
		stops.Add(1)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := lifecycle.NewManager(lifecycle.ManagerOptions{Dispatcher: dispatcher, RollbackTimeout: time.Second}, []lifecycle.Binding{member})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(governedContext(t)); err != nil || stops.Load() != 1 || dispatcher.Published() || !dispatcher.AdmissionClosed() {
		t.Fatalf("pre-publication cleanup = %v, stops %d", err, stops.Load())
	}
}

func FuzzGovernedLifecycleOperationSequence(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 0, 2})
	f.Add([]byte{1, 3, 0, 2})
	f.Fuzz(func(t *testing.T, sequence []byte) {
		if len(sequence) > 32 {
			sequence = sequence[:32]
		}
		var fixture governedFixture
		var starts, stops atomic.Int32
		fixture = newGovernedFixture(t, []governedMember{
			{name: "Plain"},
			{name: "Consumer", instance: &testLifecycleInstance{
				start: func(ctx context.Context) error {
					starts.Add(1)
					assertGovernedCall(t, fixture, ctx, "Plain")
					return nil
				},
				stop: func(ctx context.Context) error {
					stops.Add(1)
					assertGovernedCall(t, fixture, ctx, "Plain")
					return nil
				},
			}},
		})
		ctx := governedContext(t)
		started, opened, stopped := false, false, false
		for _, action := range sequence {
			switch action % 4 {
			case 0:
				err := fixture.manager.Start(ctx)
				if started || stopped {
					if !errors.Is(err, lifecycle.ErrState) {
						t.Fatal("invalid startup accepted", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					started = true
				}
			case 1:
				err := fixture.dispatcher.OpenAdmission()
				if stopped {
					if !errors.Is(err, invocation.ErrDispatcherDraining) {
						t.Fatal(err)
					}
				} else if !started {
					if !errors.Is(err, invocation.ErrDispatcherNotReady) {
						t.Fatal(err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					opened = true
				}
			case 2:
				if opened && !stopped {
					assertGovernedCall(t, fixture, ctx, "Consumer")
				} else {
					assertGovernedUnavailable(t, fixture, ctx, "Consumer")
				}
			case 3:
				if err := fixture.manager.Stop(ctx); err != nil {
					t.Fatal(err)
				}
				stopped = true
			}
			if starts.Load() > 1 || stops.Load() > 1 || fixture.dispatcher.ActiveAttempts() != 0 || fixture.dispatcher.Accepting() != (opened && !stopped) {
				t.Fatal("lifecycle sequence changed readiness or ownership")
			}
		}
		if err := fixture.manager.Stop(ctx); err != nil || stops.Load() != 1 {
			t.Fatal("final cleanup failed", err)
		}
	})
}

func BenchmarkGovernedLifecycle(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var fixture governedFixture
		hook := func(ctx context.Context) error {
			_, err := fixture.handles["Plain"].Invoke(ctx, "ready")
			return err
		}
		fixture = newGovernedFixture(b, []governedMember{
			{name: "Plain"},
			{name: "Consumer", instance: &testLifecycleInstance{start: hook, stop: hook}},
		})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := fixture.manager.Start(ctx); err != nil {
			cancel()
			b.Fatal(err)
		}
		err := fixture.manager.Stop(ctx)
		cancel()
		if err != nil {
			b.Fatal(err)
		}
	}
}

type governedMember struct {
	name         string
	constructor  string
	resourceName string
	instance     lifecycle.Instance
	handler      capability.Handler[string, string]
	policy       *invocation.Policy
}

type governedFixture struct {
	manager    *lifecycle.Manager
	dispatcher *invocation.Dispatcher
	handles    map[string]invocation.Handle[string, string]
}

func newGovernedFixture(t testing.TB, members []governedMember) governedFixture {
	t.Helper()
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	build, err := invocation.NewModuleBuild("example.com/lifecycle", "v1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	var lifecycles []lifecycle.Binding
	var bindings []invocation.Binding
	handles := make(map[string]invocation.Handle[string, string])
	for _, member := range members {
		constructor := "example.com/lifecycle.New" + member.name
		if member.constructor != "" {
			constructor = member.constructor
		}
		if member.instance != nil {
			binding, err := lifecycle.NewBinding(constructor, member.instance)
			if member.resourceName != "" {
				binding, err = lifecycle.NewResourceBinding(member.resourceName, constructor, member.instance)
			}
			if err != nil {
				t.Fatal(err)
			}
			lifecycles = append(lifecycles, binding)
		}
		if member.resourceName != "" {
			continue
		}
		contract := capability.MustParseContract[string, string]("example." + strings.ToLower(member.name) + "/v1")
		handler := member.handler
		if handler == nil {
			handler = func(_ context.Context, value string) (string, error) { return value, nil }
		}
		endpoint, err := invocation.NewEndpoint(contract, handler)
		if err != nil {
			t.Fatal(err)
		}
		policy := invocation.Policy{SchemaVersion: invocation.PolicySchemaVersion, CompilerVersion: invocation.PolicyCompilerVersion,
			DefaultsVersion: invocation.PolicyDefaultsVersion, ConcurrencyLimit: 64, Retry: invocation.RetryPolicy{MaxAttempts: 1}}
		if member.policy != nil {
			policy = *member.policy
		}
		binding, err := invocation.NewBinding(invocation.BindingOptions{
			Kind: invocation.BindingKindImplementation, Constructor: constructor, ModuleBuild: build,
			SelectionReason: invocation.SelectionReasonExplicit, ContractDigest: sha256.Sum256([]byte(member.name)),
			Policy: policy,
		}, endpoint)
		if err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, binding)
		handle, err := invocation.NewHandle(dispatcher, contract, true)
		if err != nil {
			t.Fatal(err)
		}
		handles[member.name] = handle
	}
	manager, err := lifecycle.NewManager(lifecycle.ManagerOptions{RollbackTimeout: time.Second, Dispatcher: dispatcher}, lifecycles)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := invocation.NewCatalog(bindings)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	return governedFixture{manager: manager, dispatcher: dispatcher, handles: handles}
}

func governedContext(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func assertGovernedCall(t testing.TB, fixture governedFixture, ctx context.Context, name string) {
	t.Helper()
	value, err := fixture.handles[name].Invoke(ctx, "ready")
	if err != nil || value != "ready" {
		t.Errorf("hook call to %s = %q, %v", name, value, err)
	}
}

func assertGovernedUnavailable(t testing.TB, fixture governedFixture, ctx context.Context, name string) {
	t.Helper()
	value, err := fixture.handles[name].Invoke(ctx, "unready")
	var boundary *invocation.Error
	if value != "" || !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorUnavailable || boundary.Completion() != invocation.CompletionNotStarted {
		t.Errorf("unready hook call to %s = %q, %v", name, value, err)
	}
}
