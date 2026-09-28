package invocation_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
)

func TestBindingRequiresExplicitBoundedConcurrency(t *testing.T) {
	for _, limit := range []int{-1, 0, 1, 32, invocation.MaximumConcurrencyLimit, invocation.MaximumConcurrencyLimit + 1} {
		checkConcurrencyLimit(t, limit)
	}
}

func FuzzBindingConcurrencyLimit(f *testing.F) {
	for _, limit := range []int{-1, 0, 1, 32, invocation.MaximumConcurrencyLimit, invocation.MaximumConcurrencyLimit + 1} {
		f.Add(limit)
	}
	f.Fuzz(func(t *testing.T, limit int) { checkConcurrencyLimit(t, limit) })
}

func checkConcurrencyLimit(t *testing.T, limit int) {
	t.Helper()
	contract := capability.MustParseContract[string, string]("example.admission/v1")
	endpoint, err := invocation.NewEndpoint(contract, func(_ context.Context, value string) (string, error) { return value, nil })
	if err != nil {
		t.Fatal(err)
	}
	options := admissionBindingOptions(t, limit)
	binding, err := invocation.NewBinding(options, endpoint)
	if limit < 1 || limit > invocation.MaximumConcurrencyLimit {
		if !errors.Is(err, invocation.ErrInvalidBinding) || binding.ConcurrencyLimit() != 0 {
			t.Fatalf("invalid limit %d = %d, %v", limit, binding.ConcurrencyLimit(), err)
		}
		return
	}
	if err != nil || binding.ConcurrencyLimit() != limit {
		t.Fatalf("limit %d = %d, %v", limit, binding.ConcurrencyLimit(), err)
	}
	options.Policy.ConcurrencyLimit = 0
	catalog, err := invocation.NewCatalog([]invocation.Binding{binding})
	if err != nil {
		t.Fatal(err)
	}
	bindings := catalog.Bindings()
	bindings[0] = invocation.Binding{}
	stored, found := catalog.Lookup(contract.Identifier())
	if !found || stored.ConcurrencyLimit() != limit {
		t.Fatal("catalog lost its immutable explicit limit")
	}
}

func TestAdmissionPermitSurvivesCallerCompletion(t *testing.T) {
	for _, phase := range []string{"target", "response"} {
		for _, outcome := range []string{"success", "error", "panic", "goexit"} {
			for _, completion := range []string{"cancel", "caller_deadline", "timeout"} {
				t.Run(phase+"/"+outcome+"/"+completion, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						release := make(chan struct{})
						entered := make(chan struct{})
						var first atomic.Bool
						first.Store(true)
						blocked := func(value string) (string, error) {
							if !first.Swap(false) {
								return value, nil
							}
							close(entered)
							<-release
							switch outcome {
							case "error":
								return "private result", errors.New("private failure")
							case "panic":
								panic("private panic")
							case "goexit":
								runtime.Goexit()
							}
							return value, nil
						}
						timeout := time.Minute
						if completion == "timeout" {
							timeout = time.Second
						}
						handles, dispatcher := admissionRuntime(t, 1, timeout, func(_ context.Context, value string) (string, error) {
							if phase == "target" {
								return blocked(value)
							}
							return value, nil
						}, "example.admission/v1")
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						if completion == "caller_deadline" {
							var stop context.CancelFunc
							ctx, stop = context.WithTimeout(ctx, time.Second)
							defer stop()
						}
						returned := make(chan error, 1)
						go func() {
							var value string
							var err error
							if phase == "response" {
								value, err = handles[0].InvokeWithResponse(ctx, "late", blocked)
							} else {
								value, err = handles[0].Invoke(ctx, "late")
							}
							if value != "" {
								t.Errorf("completed caller received late value %q", value)
							}
							returned <- err
						}()
						<-entered
						if completion == "cancel" {
							cancel()
						}
						err := <-returned
						cause := context.DeadlineExceeded
						if completion == "cancel" {
							cause = context.Canceled
						}
						if !errors.Is(err, cause) || invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
							t.Fatalf("caller completion = %v", err)
						}
						if dispatcher.ActiveAttempts() != 1 {
							t.Fatal("caller completion released target ownership")
						}
						for range 3 {
							value, err := handles[0].InvokeWithResponse(context.Background(), "rejected", func(string) (string, error) {
								t.Error("rejected call entered response processor")
								return "", nil
							})
							assertAdmissionRejected(t, value, err)
						}
						close(release)
						synctest.Wait()
						if dispatcher.ActiveAttempts() != 0 {
							t.Fatal("terminated target retained its permit")
						}
						if value, err := handles[0].Invoke(context.Background(), "next"); err != nil || value != "next" {
							t.Fatalf("next call = %q, %v", value, err)
						}
					})
				})
			}
		}
	}
}

func TestAdmissionIsSharedByHandlesButNotBindingsOrDispatchers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		entered := make(chan struct{}, 3)
		handler := func(_ context.Context, value string) (string, error) {
			entered <- struct{}{}
			<-release
			return value, nil
		}
		contracts := []capability.Contract[string, string]{
			capability.MustParseContract[string, string]("example.admission/v1"),
			capability.MustParseContract[string, string]("example.admission/v2"),
		}
		bindings := []invocation.Binding{
			admissionBinding(t, contracts[0], 1, time.Minute, handler),
			admissionBinding(t, contracts[1], 1, time.Minute, handler),
		}
		catalog, err := invocation.NewCatalog(bindings)
		if err != nil {
			t.Fatal(err)
		}
		first := admissionDispatcher(t, catalog)
		second := admissionDispatcher(t, catalog)
		var handles []invocation.Handle[string, string]
		for _, dispatcher := range []*invocation.Dispatcher{first, second} {
			for _, contract := range contracts {
				handle, err := invocation.NewHandle(dispatcher, contract, true)
				if err != nil {
					t.Fatal(err)
				}
				handles = append(handles, handle)
			}
		}
		for _, handle := range handles[:3] {
			go func() {
				if _, err := handle.Invoke(context.Background(), "first"); err != nil {
					t.Errorf("independent invocation = %v", err)
				}
			}()
			<-entered
		}
		alias, err := invocation.NewHandle(first, contracts[0], true)
		if err != nil {
			t.Fatal(err)
		}
		value, err := alias.Invoke(context.Background(), "same binding")
		assertAdmissionRejected(t, value, err)
		if first.ActiveAttempts() != 2 || second.ActiveAttempts() != 1 {
			t.Fatal("exact binding limits were pooled by constructor, version, or catalog")
		}
		close(release)
		synctest.Wait()
	})
}

func TestAdmissionConcurrentSaturationAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const limit, callers = 3, 64
		release := make(chan struct{})
		var entered, rejected, completed atomic.Int32
		handles, dispatcher := admissionRuntime(t, limit, time.Minute, func(context.Context, string) (string, error) {
			entered.Add(1)
			<-release
			return "late", nil
		}, "example.admission/v1")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		for range callers {
			go func() {
				value, err := handles[0].Invoke(ctx, "request")
				if errors.Is(err, context.Canceled) {
					if value != "" || invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
						t.Errorf("cancelled entered caller = %q, %v", value, err)
					}
				} else {
					assertAdmissionRejected(t, value, err)
					rejected.Add(1)
				}
				completed.Add(1)
			}()
		}
		synctest.Wait()
		if entered.Load() != limit || rejected.Load() != callers-limit || dispatcher.ActiveAttempts() != limit {
			t.Fatalf("saturation: entered=%d rejected=%d active=%d", entered.Load(), rejected.Load(), dispatcher.ActiveAttempts())
		}
		cancel()
		synctest.Wait()
		if completed.Load() != callers || dispatcher.ActiveAttempts() != limit {
			t.Fatal("caller cancellation changed executing admission count")
		}
		expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer stop()
		if err := dispatcher.Drain(expired); !errors.Is(err, invocation.ErrDrain) {
			t.Fatalf("drain = %v", err)
		}
		if dispatcher.ActiveAttempts() != limit {
			t.Fatal("failed drain released permits")
		}
		close(release)
		synctest.Wait()
		drainDispatcher(t, dispatcher)
		value, err := handles[0].Invoke(context.Background(), "closed")
		var boundary *invocation.Error
		if value != "" || !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorUnavailable || boundary.DetailCode() != "runtime.dispatcher_draining" || boundary.Completion() != invocation.CompletionNotStarted {
			t.Fatalf("closed dispatcher = %q, %v", value, err)
		}
	})
}

func TestAdmissionPreEntryCancellationDoesNotConsumePermit(t *testing.T) {
	handles, dispatcher := admissionRuntime(t, 1, time.Minute, func(_ context.Context, value string) (string, error) { return value, nil }, "example.admission/v1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range 3 {
		value, err := handles[0].Invoke(ctx, "cancelled")
		if value != "" || !errors.Is(err, context.Canceled) || invocation.CompletionOf(err) != invocation.CompletionNotStarted || dispatcher.ActiveAttempts() != 0 {
			t.Fatalf("pre-entry rejection = %q, %v", value, err)
		}
	}
	if value, err := handles[0].Invoke(context.Background(), "next"); err != nil || value != "next" {
		t.Fatalf("next call = %q, %v", value, err)
	}
}

func TestAdmissionReleasesEveryCompletedAttempt(t *testing.T) {
	for _, phase := range []string{"target", "response"} {
		for _, outcome := range []string{"success", "error", "panic", "goexit"} {
			t.Run(phase+"/"+outcome, func(t *testing.T) {
				finish := func(value string) (string, error) {
					switch outcome {
					case "error":
						return "discarded", errors.New("private failure")
					case "panic":
						panic("private panic")
					case "goexit":
						runtime.Goexit()
					}
					return value, nil
				}
				handles, dispatcher := admissionRuntime(t, 1, time.Minute, func(_ context.Context, value string) (string, error) {
					if phase == "target" {
						return finish(value)
					}
					return value, nil
				}, "example.admission/v1")
				for range 3 {
					var value string
					var err error
					if phase == "response" {
						value, err = handles[0].InvokeWithResponse(context.Background(), "result", finish)
					} else {
						value, err = handles[0].Invoke(context.Background(), "result")
					}
					if outcome == "success" {
						if value != "result" || err != nil {
							t.Fatalf("success = %q, %v", value, err)
						}
					} else {
						var boundary *invocation.Error
						if value != "" || !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorInternal || boundary.Completion() != invocation.CompletionResultKnown {
							t.Fatalf("failure = %q, %v", value, err)
						}
					}
					if dispatcher.ActiveAttempts() != 0 {
						t.Fatal("completed attempt retained its permit")
					}
				}
			})
		}
	}
}

func TestResourceExhaustionPreservesCompletionThroughNestedErrors(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		handles, _ := admissionRuntime(t, 1, time.Minute, func(context.Context, string) (string, error) {
			rejection, _ := invocation.NewNotStartedError(invocation.ErrorResourceExhausted, "runtime.concurrency_exhausted")
			var err error = fmt.Errorf("private wrapper: %w", rejection)
			if unknown {
				err = invocation.NewResultUnknown(err)
			}
			return "discarded", err
		}, "example.admission/v1")
		value, err := handles[0].Invoke(context.Background(), "request")
		completion := invocation.CompletionResultKnown
		if unknown {
			completion = invocation.CompletionResultUnknown
		}
		var boundary *invocation.Error
		if value != "" || !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorResourceExhausted || boundary.Completion() != completion || boundary.Error() != "capability invocation failed: resource_exhausted: runtime.concurrency_exhausted" {
			t.Fatalf("nested resource exhaustion = %q, %v", value, err)
		}
	}
}

func BenchmarkAdmissionConcurrent(b *testing.B) {
	handles, _ := admissionRuntime(b, invocation.MaximumConcurrencyLimit, time.Minute, func(_ context.Context, value string) (string, error) { return value, nil }, "example.admission/v1")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := handles[0].Invoke(context.Background(), "request"); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func assertAdmissionRejected(t *testing.T, value string, err error) {
	t.Helper()
	var boundary *invocation.Error
	if value != "" || !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorResourceExhausted || boundary.Completion() != invocation.CompletionNotStarted || boundary.DetailCode() != "runtime.concurrency_exhausted" {
		t.Errorf("admission rejection = %q, %v", value, err)
	}
}

func admissionBindingOptions(t testing.TB, limit int) invocation.BindingOptions {
	t.Helper()
	build, err := invocation.NewModuleBuild("github.com/acme/admission", "v1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	return invocation.BindingOptions{
		Kind: invocation.BindingKindImplementation, Constructor: "github.com/acme/admission.New",
		ModuleBuild: build, SelectionReason: invocation.SelectionReasonUniqueCompatible,
		ContractDigest: sha256.Sum256([]byte("admission-contract")), Policy: publicPolicy(time.Minute, limit),
	}
}

func admissionBinding(t testing.TB, contract capability.Contract[string, string], limit int, timeout time.Duration, handler capability.Handler[string, string]) invocation.Binding {
	t.Helper()
	endpoint, err := invocation.NewEndpoint(contract, handler)
	if err != nil {
		t.Fatal(err)
	}
	options := admissionBindingOptions(t, limit)
	options.Policy.Timeout = timeout
	binding, err := invocation.NewBinding(options, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func admissionDispatcher(t testing.TB, catalog invocation.Catalog) *invocation.Dispatcher {
	t.Helper()
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	return dispatcher
}

func admissionRuntime(t testing.TB, limit int, timeout time.Duration, handler capability.Handler[string, string], ids ...string) ([]invocation.Handle[string, string], *invocation.Dispatcher) {
	t.Helper()
	var contracts []capability.Contract[string, string]
	var bindings []invocation.Binding
	for _, id := range ids {
		contract := capability.MustParseContract[string, string](id)
		contracts = append(contracts, contract)
		bindings = append(bindings, admissionBinding(t, contract, limit, timeout, handler))
	}
	catalog, err := invocation.NewCatalog(bindings)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := admissionDispatcher(t, catalog)
	var handles []invocation.Handle[string, string]
	for _, contract := range contracts {
		handle, err := invocation.NewHandle(dispatcher, contract, true)
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, handle)
	}
	return handles, dispatcher
}
