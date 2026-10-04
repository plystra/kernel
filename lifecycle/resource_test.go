package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/plystra/kernel/invocation"
	"github.com/plystra/kernel/lifecycle"
)

const resourceProvider = "example.com/lifecycle.NewPool"

func TestNamedResourcesShareProviderWithoutSharingLifecycle(t *testing.T) {
	for _, governed := range []bool{false, true} {
		t.Run(fmt.Sprint(governed), func(t *testing.T) {
			var events []string
			instance := func(name string) lifecycle.Instance {
				return &testLifecycleInstance{
					start: func(context.Context) error { events = append(events, "start:"+name); return nil },
					stop:  func(context.Context) error { events = append(events, "stop:"+name); return nil },
				}
			}
			bindings := []lifecycle.Binding{
				resourceBinding(t, "database.primary", resourceProvider, instance("primary")),
				resourceBinding(t, "database.replica", resourceProvider, instance("replica")),
				lifecycleBinding(t, resourceProvider, instance("implementation")),
			}
			manager, dispatcher := resourceManager(t, governed, bindings)
			bindings[0] = lifecycle.Binding{}
			ctx := governedContext(t)
			if err := manager.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if dispatcher != nil {
				if err := dispatcher.OpenAdmission(); err != nil {
					t.Fatal(err)
				}
			}
			if err := manager.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			want := []string{"start:primary", "start:replica", "start:implementation", "stop:implementation", "stop:replica", "stop:primary"}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("instance order = %v", events)
			}
			if err := manager.Stop(ctx); err != nil || !reflect.DeepEqual(events, want) {
				t.Fatalf("repeated cleanup = %v, %v", err, events)
			}
		})
	}
}

func TestDuplicateResourceNameAcrossProvidersDoesNotBindDispatcher(t *testing.T) {
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	first := resourceBinding(t, "database.primary", resourceProvider, &testLifecycleInstance{})
	for _, provider := range []string{resourceProvider, "example.com/other.New"} {
		second := resourceBinding(t, "database.primary", provider, &testLifecycleInstance{})
		manager, err := lifecycle.NewManager(lifecycle.ManagerOptions{RollbackTimeout: time.Second, Dispatcher: dispatcher}, []lifecycle.Binding{first, second})
		if manager != nil || !errors.Is(err, lifecycle.ErrInvalidManager) || !strings.Contains(err.Error(), "duplicate resource database.primary") {
			t.Fatalf("duplicate instance accepted: %v", err)
		}
	}
	if _, err := lifecycle.NewManager(lifecycle.ManagerOptions{RollbackTimeout: time.Second, Dispatcher: dispatcher}, []lifecycle.Binding{first}); err != nil {
		t.Fatalf("rejected assembly mutated dispatcher: %v", err)
	}
}

func TestResourceFailureOwnsAllConstructedCleanup(t *testing.T) {
	for _, governed := range []bool{false, true} {
		for _, mode := range []string{"never-started", "pre-cancelled", "error", "panic", "cancel"} {
			t.Run(fmt.Sprintf("%t/%s", governed, mode), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.WithValue(governedContext(t), lifecycleContextKey{}, "preserved"))
				defer cancel()
				var events []string
				var bindings []lifecycle.Binding
				private := errors.New("private constructor configuration")
				for _, name := range []string{"database.primary", "database.failing", "database.never-started"} {
					bindings = append(bindings, resourceBinding(t, name, resourceProvider, &testLifecycleInstance{
						start: func(context.Context) error {
							events = append(events, "start:"+name)
							if name == "database.failing" {
								switch mode {
								case "error":
									return private
								case "panic":
									panic(private)
								case "cancel":
									cancel()
								}
							}
							return nil
						},
						stop: func(cleanup context.Context) error {
							events = append(events, "stop:"+name)
							if cleanup.Err() != nil || cleanup.Value(lifecycleContextKey{}) != "preserved" {
								t.Error("cleanup inherited cancellation or lost context values")
							}
							if _, bounded := cleanup.Deadline(); !bounded {
								t.Error("cleanup is unbounded")
							}
							return nil
						},
					}))
				}
				manager, _ := resourceManager(t, governed, bindings)
				var want []string
				if mode == "never-started" {
					if err := manager.Stop(ctx); err != nil {
						t.Fatal(err)
					}
				} else {
					if mode == "pre-cancelled" {
						cancel()
					} else {
						want = append(want, "start:database.primary", "start:database.failing")
					}
					err := manager.Start(ctx)
					if !errors.Is(err, lifecycle.ErrStart) || errors.Is(err, private) || strings.Contains(fmt.Sprintf("%+v", err), "private") {
						t.Fatalf("Resource failure leaked or was lost: %v", err)
					}
					if mode == "error" || mode == "panic" {
						if !strings.Contains(err.Error(), "resource database.failing (provider "+resourceProvider+")") {
							t.Fatalf("error lost exact owner or provenance: %v", err)
						}
					}
					if strings.Contains(mode, "cancel") && !errors.Is(err, context.Canceled) {
						t.Fatal("cancellation cause was lost", err)
					}
				}
				want = append(want, "stop:database.never-started", "stop:database.failing", "stop:database.primary")
				if !reflect.DeepEqual(events, want) {
					t.Fatalf("constructed cleanup = %v, want %v", events, want)
				}
				if err := manager.Stop(governedContext(t)); err != nil || !reflect.DeepEqual(events, want) {
					t.Fatalf("cleanup repeated completed instances: %v, %v", err, events)
				}
			})
		}
	}
}

func TestPartialResourceConstructorResultCleanedBeforePublication(t *testing.T) {
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	var stopped []string
	construct := func(name string) (*testLifecycleInstance, error) {
		return &testLifecycleInstance{
			start: func(context.Context) error { t.Error("partial construction started"); return nil },
			stop:  func(context.Context) error { stopped = append(stopped, name); return nil },
		}, errors.New("private construction error")
	}
	partial, constructionErr := construct("partial")
	if constructionErr == nil || partial == nil {
		t.Fatal("fixture did not return a partial value")
	}
	bindings := []lifecycle.Binding{
		resourceBinding(t, "database.first", resourceProvider, &testLifecycleInstance{stop: func(context.Context) error { stopped = append(stopped, "first"); return nil }}),
		resourceBinding(t, "database.partial", resourceProvider, partial),
	}
	manager, err := lifecycle.NewManager(lifecycle.ManagerOptions{Dispatcher: dispatcher, RollbackTimeout: time.Second}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(governedContext(t)); err != nil || !reflect.DeepEqual(stopped, []string{"partial", "first"}) || dispatcher.Published() || !dispatcher.AdmissionClosed() {
		t.Fatalf("partial cleanup = %v, stopped %v", err, stopped)
	}
}

func TestResourceCleanupRetryRetainsSuccessfulInstances(t *testing.T) {
	for _, governed := range []bool{false, true} {
		for _, mode := range []string{"error", "panic", "deadline", "cancelled-before", "cancelled-success"} {
			t.Run(fmt.Sprintf("%t/%s", governed, mode), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					var events []string
					attempts := 0
					manager, _ := resourceManager(t, governed, []lifecycle.Binding{
						resourceBinding(t, "database.primary", resourceProvider, &testLifecycleInstance{stop: func(context.Context) error { events = append(events, "primary"); return nil }}),
						resourceBinding(t, "database.replica", resourceProvider, &testLifecycleInstance{stop: func(ctx context.Context) error {
							events = append(events, "replica")
							attempts++
							if attempts == 1 {
								switch mode {
								case "error":
									return errors.New("private stop failure")
								case "panic":
									panic("private stop panic")
								case "deadline":
									<-ctx.Done()
									return ctx.Err()
								case "cancelled-success":
									cancel()
								}
							}
							return nil
						}}),
					})
					if mode == "cancelled-before" {
						cancel()
					}
					err := manager.Stop(ctx)
					if !errors.Is(err, lifecycle.ErrStop) || strings.Contains(fmt.Sprintf("%+v", err), "private") {
						t.Fatal("unsafe or missing cleanup failure", err)
					}
					if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) || strings.HasPrefix(mode, "cancelled") && !errors.Is(err, context.Canceled) {
						t.Fatal("lost cleanup context cause", err)
					}
					if err := manager.Stop(governedContext(t)); err != nil {
						t.Fatal(err)
					}
					want := []string{"replica", "replica", "primary"}
					if strings.HasPrefix(mode, "cancelled") {
						want = []string{"replica", "primary"}
					}
					if !reflect.DeepEqual(events, want) {
						t.Fatalf("retry cleanup = %v, want %v", events, want)
					}
				})
			})
		}
	}
}

func TestCleanupRetryKeepsResourceDependencyLive(t *testing.T) {
	for _, governed := range []bool{false, true} {
		for _, phase := range []string{"shutdown", "rollback"} {
			for _, owner := range []string{"resource", "implementation"} {
				for _, outcome := range []string{"error", "panic"} {
					t.Run(fmt.Sprintf("%t/%s/%s/%s", governed, phase, owner, outcome), func(t *testing.T) {
						var events []string
						var live bool
						attempts := 0
						private := errors.New("private cleanup failure")
						consumer := &testLifecycleInstance{
							start: func(context.Context) error {
								if phase == "rollback" {
									return private
								}
								return nil
							},
							stop: func(context.Context) error {
								events = append(events, "consumer")
								attempts++
								if !live {
									return errors.New("dependency already stopped")
								}
								if attempts == 1 {
									if outcome == "panic" {
										panic(private)
									}
									return private
								}
								return nil
							},
						}
						consumerBinding := lifecycleBinding(t, "example.com/lease.New", consumer)
						if owner == "resource" {
							consumerBinding = resourceBinding(t, "lease", "example.com/lease.New", consumer)
						}
						manager, dispatcher := resourceManager(t, governed, []lifecycle.Binding{
							resourceBinding(t, "database.primary", resourceProvider, &testLifecycleInstance{
								start: func(context.Context) error { live = true; return nil },
								stop:  func(context.Context) error { events = append(events, "dependency"); live = false; return nil },
							}),
							consumerBinding,
							lifecycleBinding(t, "example.com/completed.New", &testLifecycleInstance{
								start: func(context.Context) error {
									if phase == "rollback" {
										t.Error("later instance started after failure")
									}
									return nil
								},
								stop: func(context.Context) error { events = append(events, "completed"); return nil },
							}),
						})
						err := manager.Start(governedContext(t))
						if phase == "shutdown" {
							if err != nil {
								t.Fatal(err)
							}
							if dispatcher != nil {
								if err := dispatcher.OpenAdmission(); err != nil {
									t.Fatal(err)
								}
							}
							err = manager.Stop(governedContext(t))
						} else if !errors.Is(err, lifecycle.ErrStart) {
							t.Fatalf("startup rollback = %v", err)
						}
						if !errors.Is(err, lifecycle.ErrStop) || errors.Is(err, private) || strings.Contains(fmt.Sprint(err), "private") || manager.State() != lifecycle.StateFailed {
							t.Fatalf("initial cleanup = %v, state %s", err, manager.State())
						}
						if !live || !reflect.DeepEqual(events, []string{"completed", "consumer"}) {
							t.Fatalf("failed consumer lost live dependency: live %t, stops %v", live, events)
						}
						if dispatcher != nil && (!dispatcher.AdmissionClosed() || dispatcher.Accepting()) {
							t.Fatal("cleanup failure left public admission open")
						}
						if err := manager.Stop(governedContext(t)); err != nil || live || manager.State() != lifecycle.StateStopped {
							t.Fatalf("cleanup retry = %v, dependency live %t, state %s", err, live, manager.State())
						}
						want := []string{"completed", "consumer", "consumer", "dependency"}
						if err := manager.Stop(governedContext(t)); err != nil || !reflect.DeepEqual(events, want) {
							t.Fatalf("successful cleanup repeated: %v, stops %v", err, events)
						}
					})
				}
			}
		}
	}
}

func TestResourceRollbackTimeoutLeavesNeverStartedCleanupRetryable(t *testing.T) {
	for _, governed := range []bool{false, true} {
		t.Run(fmt.Sprint(governed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var events []string
				attempts := 0
				manager, _ := resourceManager(t, governed, []lifecycle.Binding{
					resourceBinding(t, "database.primary", resourceProvider, &testLifecycleInstance{
						start: func(context.Context) error { return errors.New("private startup failure") },
						stop:  func(context.Context) error { events = append(events, "primary"); return nil },
					}),
					resourceBinding(t, "database.replica", resourceProvider, &testLifecycleInstance{
						start: func(context.Context) error { t.Error("later Resource started after failure"); return nil },
						stop: func(ctx context.Context) error {
							events = append(events, "replica")
							attempts++
							if attempts == 1 {
								<-ctx.Done()
								return ctx.Err()
							}
							return nil
						},
					}),
				})
				err := manager.Start(governedContext(t))
				if !errors.Is(err, lifecycle.ErrStart) || !errors.Is(err, lifecycle.ErrStop) || !errors.Is(err, context.DeadlineExceeded) || strings.Contains(fmt.Sprint(err), "private") {
					t.Fatal("bounded rollback failure was lost or leaked", err)
				}
				if !reflect.DeepEqual(events, []string{"replica"}) {
					t.Fatalf("expired rollback entered later cleanup: %v", events)
				}
				if err := manager.Stop(governedContext(t)); err != nil || !reflect.DeepEqual(events, []string{"replica", "replica", "primary"}) {
					t.Fatalf("rollback retry = %v, cleanup %v", err, events)
				}
			})
		})
	}
}

func TestMixedResourceHooksKeepImplementationReadinessAndRetry(t *testing.T) {
	var fixture governedFixture
	var events []string
	var calls atomic.Int32
	var retained context.Context
	policy := invocation.Policy{SchemaVersion: invocation.PolicySchemaVersion, CompilerVersion: invocation.PolicyCompilerVersion,
		DefaultsVersion: invocation.PolicyDefaultsVersion, Timeout: time.Second, ConcurrencyLimit: 1,
		Retry: invocation.RetryPolicy{Eligibility: invocation.RetryReplaySafe, MaxAttempts: 2}}
	resource := func(name string) lifecycle.Instance {
		return &testLifecycleInstance{
			start: func(ctx context.Context) error {
				events = append(events, "start:"+name)
				assertGovernedCall(t, fixture, ctx, "Dependency")
				assertGovernedUnavailable(t, fixture, ctx, "Consumer")
				retained = context.WithoutCancel(ctx)
				return nil
			},
			stop: func(ctx context.Context) error {
				events = append(events, "stop:"+name)
				assertGovernedCall(t, fixture, ctx, "Dependency")
				assertGovernedUnavailable(t, fixture, ctx, "Consumer")
				return nil
			},
		}
	}
	fixture = newGovernedFixture(t, []governedMember{
		{name: "Dependency", constructor: resourceProvider, policy: &policy, instance: &testLifecycleInstance{
			start: func(ctx context.Context) error {
				events = append(events, "start:dependency")
				assertGovernedUnavailable(t, fixture, ctx, "Dependency")
				return nil
			},
			stop: func(ctx context.Context) error {
				events = append(events, "stop:dependency")
				assertGovernedUnavailable(t, fixture, ctx, "Dependency")
				return nil
			},
		}, handler: func(_ context.Context, value string) (string, error) {
			if calls.Add(1)%2 == 1 {
				err, _ := invocation.NewError(invocation.ErrorUnavailable, "test.transient")
				return "", err
			}
			return value, nil
		}},
		{name: "Primary", resourceName: "database.primary", constructor: resourceProvider, instance: resource("primary")},
		{name: "Replica", resourceName: "database.replica", constructor: resourceProvider, instance: resource("replica")},
		{name: "Consumer", instance: &testLifecycleInstance{
			start: func(ctx context.Context) error {
				events = append(events, "start:consumer")
				assertGovernedCall(t, fixture, ctx, "Dependency")
				return nil
			},
			stop: func(ctx context.Context) error {
				events = append(events, "stop:consumer")
				assertGovernedCall(t, fixture, ctx, "Dependency")
				return nil
			},
		}},
	})
	ctx := governedContext(t)
	if err := fixture.manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fixture.handles) != 2 {
		t.Fatal("Resources created Interface handles")
	}
	if err := fixture.dispatcher.OpenAdmission(); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.handles["Dependency"].Invoke(retained, "stale"); !errors.Is(err, context.Canceled) {
		t.Fatal("Resource retained governed scope", err)
	}
	if err := fixture.manager.Stop(ctx); err != nil || calls.Load() != 12 {
		t.Fatalf("mixed hook cleanup = %v, target attempts %d", err, calls.Load())
	}
	want := []string{"start:dependency", "start:primary", "start:replica", "start:consumer", "stop:consumer", "stop:replica", "stop:primary", "stop:dependency"}
	if !reflect.DeepEqual(events, want) || fixture.dispatcher.Accepting() {
		t.Fatalf("mixed lifecycle order = %v", events)
	}
}

func TestResourceHookBodyRemainsOwnedAfterDrainTimeout(t *testing.T) {
	for _, phase := range []string{"start", "stop"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var fixture governedFixture
				var dependencyStops atomic.Int32
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				defer once.Do(func() { close(release) })
				hold := func(ctx context.Context) error {
					assertGovernedCall(t, fixture, ctx, "Dependency")
					close(entered)
					<-release
					if dependencyStops.Load() != 0 {
						t.Error("cleanup overtook Resource hook body")
					}
					if _, err := fixture.handles["Dependency"].Invoke(context.WithoutCancel(ctx), "revoked"); !errors.Is(err, context.Canceled) {
						t.Error("drain did not revoke Resource hook calls", err)
					}
					return nil
				}
				instance := &testLifecycleInstance{}
				if phase == "start" {
					instance.start = hold
				} else {
					instance.stop = hold
				}
				fixture = newGovernedFixture(t, []governedMember{
					{name: "Dependency", instance: &testLifecycleInstance{stop: func(context.Context) error { dependencyStops.Add(1); return nil }}},
					{name: "Resource", resourceName: "database.primary", constructor: resourceProvider, instance: instance},
				})
				ctx := governedContext(t)
				result := make(chan error, 1)
				go func() {
					err := fixture.manager.Start(ctx)
					if phase == "stop" && err == nil {
						err = fixture.manager.Stop(ctx)
					}
					result <- err
				}()
				<-entered
				drain, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := fixture.dispatcher.Drain(drain); !errors.Is(err, invocation.ErrDrain) || !errors.Is(err, context.DeadlineExceeded) || dependencyStops.Load() != 0 {
					t.Fatalf("uncooperative Resource drain = %v", err)
				}
				if err := fixture.manager.Stop(ctx); !errors.Is(err, lifecycle.ErrState) {
					t.Fatal("concurrent cleanup entered active hook", err)
				}
				once.Do(func() { close(release) })
				err := <-result
				if phase == "start" && !errors.Is(err, lifecycle.ErrStart) || phase == "stop" && err != nil || dependencyStops.Load() != 1 {
					t.Fatalf("resumed Resource cleanup = %v, stops %d", err, dependencyStops.Load())
				}
			})
		})
	}
}

func resourceManager(t testing.TB, governed bool, bindings []lifecycle.Binding) (*lifecycle.Manager, *invocation.Dispatcher) {
	t.Helper()
	var dispatcher *invocation.Dispatcher
	if governed {
		var err error
		dispatcher, err = invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
		if err != nil {
			t.Fatal(err)
		}
	}
	manager, err := lifecycle.NewManager(lifecycle.ManagerOptions{Dispatcher: dispatcher, RollbackTimeout: time.Second}, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if dispatcher != nil {
		catalog, err := invocation.NewCatalog(nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := dispatcher.Publish(catalog); err != nil {
			t.Fatal(err)
		}
	}
	return manager, dispatcher
}
