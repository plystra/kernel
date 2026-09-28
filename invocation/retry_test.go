package invocation_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
)

func retryPolicy() invocation.Policy {
	policy := publicPolicy(time.Minute, 1)
	policy.Retry = invocation.RetryPolicy{Eligibility: invocation.RetryReplaySafe, MaxAttempts: 3}
	return policy
}

func policyError(code invocation.ErrorCode) error {
	err, invalid := invocation.NewError(code, "test.outcome")
	if invalid != nil {
		panic(invalid)
	}
	return err
}

func TestRetryEligibilityAndExhaustionPreserveFinalOutcome(t *testing.T) {
	for _, test := range []struct {
		name       string
		failure    func() error
		attempts   int
		code       invocation.ErrorCode
		completion invocation.Completion
	}{
		{"unavailable", func() error { return policyError(invocation.ErrorUnavailable) }, 3, invocation.ErrorUnavailable, invocation.CompletionResultKnown},
		{"exhausted", func() error { return policyError(invocation.ErrorResourceExhausted) }, 3, invocation.ErrorResourceExhausted, invocation.CompletionResultKnown},
		{"unknown", func() error { return invocation.NewResultUnknown(policyError(invocation.ErrorUnavailable)) }, 1, invocation.ErrorUnavailable, invocation.CompletionResultUnknown},
		{"argument", func() error { return policyError(invocation.ErrorInvalidArgument) }, 1, invocation.ErrorInvalidArgument, invocation.CompletionResultKnown},
		{"denied", func() error { return policyError(invocation.ErrorDenied) }, 1, invocation.ErrorDenied, invocation.CompletionResultKnown},
		{"unauthenticated", func() error { return policyError(invocation.ErrorUnauthenticated) }, 1, invocation.ErrorUnauthenticated, invocation.CompletionResultKnown},
		{"timeout", func() error { return policyError(invocation.ErrorTimeout) }, 1, invocation.ErrorTimeout, invocation.CompletionResultKnown},
		{"cancelled", func() error { return policyError(invocation.ErrorCancelled) }, 1, invocation.ErrorCancelled, invocation.CompletionResultKnown},
		{"internal", func() error { return policyError(invocation.ErrorInternal) }, 1, invocation.ErrorInternal, invocation.CompletionResultKnown},
		{"private", func() error { return errors.New("private failure") }, 1, invocation.ErrorInternal, invocation.CompletionResultKnown},
		{"panic", func() error { panic("private panic") }, 1, invocation.ErrorInternal, invocation.CompletionResultKnown},
		{"goexit", func() error { runtime.Goexit(); return nil }, 1, invocation.ErrorInternal, invocation.CompletionResultKnown},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			var dispatcher *invocation.Dispatcher
			var identity invocation.InvocationID
			handle, d := policyRuntime(t, retryPolicy(), func(ctx context.Context, _ string) (string, error) {
				current, ok := invocation.Current(ctx)
				number := int(calls.Add(1))
				if !ok || current.Attempt() != number || current.RetryOwner() != current.InvocationID() || dispatcher.ActiveAttempts() != 1 {
					t.Error("attempt identity, owner, or retained permit is invalid")
				}
				if number == 1 {
					identity = current.InvocationID()
				} else if current.InvocationID() != identity {
					t.Error("retry changed logical-call identity")
				}
				return "discarded", test.failure()
			})
			dispatcher = d
			response, err := handle.Invoke(context.Background(), "request")
			var failure *invocation.Error
			if response != "" || !errors.As(err, &failure) || failure.Code() != test.code || failure.Completion() != test.completion || failure.Attempts() != test.attempts || int(calls.Load()) != test.attempts || d.ActiveAttempts() != 0 {
				t.Fatalf("outcome: %q, %v, calls %d", response, err, calls.Load())
			}
		})
	}
}

func TestRetryDoesNotReplaySemanticOrResponseValidationFailures(t *testing.T) {
	for _, responseFailure := range []bool{false, true} {
		var calls atomic.Int32
		handle, _ := policyRuntime(t, retryPolicy(), func(context.Context, string) (string, error) {
			calls.Add(1)
			if !responseFailure {
				return "", invocation.NewSemanticError("not_ready", nil)
			}
			return "result", nil
		})
		value, err := handle.InvokeWithResponse(context.Background(), "request", func(string) (string, error) { return "", policyError(invocation.ErrorUnavailable) })
		if value != "" || err == nil || calls.Load() != 1 {
			t.Fatalf("nonretryable result: %q, %v, calls %d", value, err, calls.Load())
		}
		if responseFailure {
			var failure *invocation.Error
			if !errors.As(err, &failure) || failure.Code() != invocation.ErrorInternal || failure.Attempts() != 1 {
				t.Fatal(err)
			}
		} else {
			var semantic *invocation.SemanticError
			if !errors.As(err, &semantic) || semantic.Code() != "not_ready" || semantic.Attempts() != 1 {
				t.Fatal(err)
			}
		}
	}
}

func TestRetryBudgetIncludesPreparationAndAllBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		policy := retryPolicy()
		policy.Timeout = 5 * time.Second
		policy.Retry.Backoff = 2 * time.Second
		start := time.Now()
		prepared, calls := 0, 0
		handle, _ := policyRuntime(t, policy, func(ctx context.Context, value string) (string, error) {
			calls++
			deadline, ok := ctx.Deadline()
			if !ok || !deadline.Equal(start.Add(5*time.Second)) || value != "snapshot" {
				t.Error("retry reset the deadline or snapshot")
			}
			return "", policyError(invocation.ErrorUnavailable)
		})
		value, err := handle.InvokeWithPreparation(context.Background(), "request", func(string) (string, error) { prepared++; time.Sleep(2 * time.Second); return "snapshot", nil }, func(value string) (string, error) { return value, nil })
		var failure *invocation.Error
		if value != "" || !errors.As(err, &failure) || !errors.Is(err, context.DeadlineExceeded) || failure.Completion() != invocation.CompletionResultKnown || failure.Attempts() != 2 || prepared != 1 || calls != 2 || time.Since(start) != 5*time.Second {
			t.Fatalf("budget: %q %v; prepared %d calls %d duration %s", value, err, prepared, calls, time.Since(start))
		}
	})
}

func TestPolicyWithoutTimeoutAddsNoDeadlineAndCallerDeadlineWins(t *testing.T) {
	for _, timeout := range []time.Duration{0, time.Minute} {
		for _, callerBound := range []bool{false, true} {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				var cancel context.CancelFunc
				deadline := time.Now().Add(time.Second)
				if callerBound {
					ctx, cancel = context.WithDeadline(ctx, deadline)
					defer cancel()
				}
				handle, _ := policyRuntime(t, publicPolicy(timeout, 1), func(ctx context.Context, value string) (string, error) {
					got, ok := ctx.Deadline()
					current, _ := invocation.Current(ctx)
					if ok != (callerBound || timeout > 0) || (callerBound && !got.Equal(deadline)) || !got.Equal(current.Deadline()) || current.RetryOwner().Valid() {
						t.Error("unexpected effective policy deadline/owner")
					}
					if !ok {
						time.Sleep(2 * time.Minute)
					}
					return value, nil
				})
				if value, err := handle.Invoke(ctx, "result"); err != nil || value != "result" {
					t.Fatal(value, err)
				}
			})
		}
	}
}

func TestRetryPreparationKeepsOneSnapshotAndAdaptersOwnAttemptCopies(t *testing.T) {
	contract := capability.MustParseContract[[]byte, []byte]("example.snapshot/v1")
	snapshotCalls, targetCalls := 0, 0
	endpoint, err := invocation.NewEndpoint(contract, func(ctx context.Context, snapshot []byte) ([]byte, error) {
		// This is the generated adapter's ownership boundary, not a Kernel deep copy.
		attempt := append([]byte(nil), snapshot...)
		targetCalls++
		current, _ := invocation.Current(ctx)
		if len(attempt) != 1 || attempt[0] != 7 || current.Attempt() != targetCalls {
			t.Error("previous attempt mutated the logical snapshot")
		}
		attempt[0] = byte(targetCalls)
		if targetCalls < 3 {
			return nil, policyError(invocation.ErrorUnavailable)
		}
		return attempt, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	options := admissionBindingOptions(t, 1)
	options.Policy = retryPolicy()
	binding, err := invocation.NewBinding(options, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := invocation.NewCatalog([]invocation.Binding{binding})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := admissionDispatcher(t, catalog)
	handle, err := invocation.NewHandle(dispatcher, contract, true)
	if err != nil {
		t.Fatal(err)
	}
	request := []byte{7}
	response, err := handle.InvokeWithPreparation(context.Background(), request, func(value []byte) ([]byte, error) { snapshotCalls++; return append([]byte(nil), value...), nil }, func(value []byte) ([]byte, error) { return append([]byte(nil), value...), nil })
	if err != nil || len(response) != 1 || response[0] != 3 || request[0] != 7 || targetCalls != 3 || snapshotCalls != 1 {
		t.Fatalf("ownership: response %v, input %v, calls %d/%d, %v", response, request, targetCalls, snapshotCalls, err)
	}
	response[0] = 9
	if request[0] != 7 {
		t.Fatal("successful response shares caller request storage")
	}
}

func BenchmarkCompiledPolicy(b *testing.B) {
	for _, attempts := range []int{1, 3} {
		name := "single"
		if attempts > 1 {
			name = "retry"
		}
		b.Run(name, func(b *testing.B) {
			policy := publicPolicy(time.Minute, 1)
			if attempts > 1 {
				policy = retryPolicy()
			}
			handle, _ := policyRuntime(b, policy, func(ctx context.Context, value string) (string, error) {
				current, _ := invocation.Current(ctx)
				if current.Attempt() < attempts {
					return "", policyError(invocation.ErrorUnavailable)
				}
				return value, nil
			})
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if value, err := handle.Invoke(context.Background(), "value"); err != nil || value != "value" {
					b.Fatal(value, err)
				}
			}
		})
	}
}

func TestConcurrentRetriesHaveIndependentOwnersAndAttemptCounts(t *testing.T) {
	policy := retryPolicy()
	policy.ConcurrencyLimit = 64
	var mu sync.Mutex
	owners := make(map[invocation.InvocationID]int)
	handle, _ := policyRuntime(t, policy, func(ctx context.Context, value string) (string, error) {
		current, _ := invocation.Current(ctx)
		mu.Lock()
		owners[current.RetryOwner()]++
		count := owners[current.RetryOwner()]
		mu.Unlock()
		if current.RetryOwner() != current.InvocationID() || count != current.Attempt() {
			t.Error("concurrent call shared retry ownership")
		}
		if count < 3 {
			return "", policyError(invocation.ErrorUnavailable)
		}
		return value, nil
	})
	var wait sync.WaitGroup
	for range 32 {
		wait.Go(func() {
			if value, err := handle.Invoke(context.Background(), "value"); err != nil || value != "value" {
				t.Error(value, err)
			}
		})
	}
	wait.Wait()
	if len(owners) != 32 {
		t.Fatalf("owners=%d", len(owners))
	}
	for _, count := range owners {
		if count != 3 {
			t.Fatalf("attempts=%d", count)
		}
	}
}

func FuzzRetryOutcome(f *testing.F) {
	for mode := range uint8(6) {
		f.Add(mode, uint8(2), uint8(3))
	}
	f.Fuzz(func(t *testing.T, mode, limit, successAt uint8) {
		mode %= 6
		maximum := int(limit%16) + 1
		policy := retryPolicy()
		policy.Retry.MaxAttempts = maximum
		if maximum == 1 {
			policy.Retry = invocation.RetryPolicy{MaxAttempts: 1}
		}
		calls := 0
		handle, _ := policyRuntime(t, policy, func(context.Context, string) (string, error) {
			calls++
			if calls == int(successAt) || mode == 5 {
				return "value", nil
			}
			switch mode {
			case 0:
				return "", policyError(invocation.ErrorUnavailable)
			case 1:
				return "", policyError(invocation.ErrorResourceExhausted)
			case 2:
				return "", invocation.NewSemanticError("not_ready", nil)
			case 3:
				return "", invocation.NewResultUnknown(policyError(invocation.ErrorUnavailable))
			default:
				return "", errors.New("private error")
			}
		})
		value, err := handle.Invoke(context.Background(), "request")
		wantCalls := 1
		if mode < 2 {
			wantCalls = maximum
			if successAt > 0 && int(successAt) <= maximum {
				wantCalls = int(successAt)
			}
		}
		wantSuccess := mode == 5 || int(successAt) == wantCalls
		if calls != wantCalls || (err == nil) != wantSuccess || (value == "value") != wantSuccess {
			t.Fatalf("retry result: calls %d want %d, value %q, err %v", calls, wantCalls, value, err)
		}
		if failure, ok := err.(*invocation.Error); ok && failure.Attempts() != calls {
			t.Fatal("attempt evidence changed")
		}
	})
}

func TestRequestPreparationRejectsBeforeEntry(t *testing.T) {
	for _, mode := range []string{"invalid", "private", "panic", "timeout", "nil"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				policy := retryPolicy()
				policy.Timeout = time.Second
				handle, _ := policyRuntime(t, policy, func(context.Context, string) (string, error) { calls++; return "", nil })
				prepare := func(string) (string, error) {
					switch mode {
					case "invalid":
						return "", policyError(invocation.ErrorInvalidArgument)
					case "private":
						return "", errors.New("private request")
					case "panic":
						panic("private request")
					case "timeout":
						time.Sleep(time.Second)
					}
					return "", nil
				}
				if mode == "nil" {
					prepare = nil
				}
				value, err := handle.InvokeWithPreparation(context.Background(), "request", prepare, func(value string) (string, error) { return value, nil })
				var failure *invocation.Error
				if value != "" || !errors.As(err, &failure) || failure.Completion() != invocation.CompletionNotStarted || failure.Attempts() != 0 || calls != 0 {
					t.Fatalf("preparation: %q, %v, calls %d", value, err, calls)
				}
			})
		})
	}
}

func TestNestedCallsHaveExactlyOneRetryOwner(t *testing.T) {
	for _, outerRetry := range []bool{false, true} {
		t.Run(map[bool]string{false: "inner owns", true: "outer owns"}[outerRetry], func(t *testing.T) {
			var outerFrame invocation.InvocationContext
			innerCalls, outerCalls := 0, 0
			inner, _ := policyRuntime(t, retryPolicy(), func(ctx context.Context, _ string) (string, error) {
				innerCalls++
				current, _ := invocation.Current(ctx)
				if current.ParentInvocationID() != outerFrame.InvocationID() {
					t.Error("nested ancestry lost")
				}
				if outerRetry {
					if current.RetryOwner() != outerFrame.InvocationID() || current.Attempt() != 1 {
						t.Error("nested retry was not suppressed")
					}
				} else if current.RetryOwner() != current.InvocationID() || current.Attempt() != innerCalls {
					t.Error("first nested retry binding did not take ownership")
				}
				return "", policyError(invocation.ErrorUnavailable)
			})
			policy := publicPolicy(time.Minute, 1)
			if outerRetry {
				policy = retryPolicy()
			}
			outer, _ := policyRuntime(t, policy, func(ctx context.Context, value string) (string, error) {
				outerCalls++
				outerFrame, _ = invocation.Current(ctx)
				return inner.Invoke(ctx, value)
			})
			_, err := outer.Invoke(context.Background(), "request")
			var failure *invocation.Error
			want := 1
			if outerRetry {
				want = 3
			}
			if !errors.As(err, &failure) || failure.Attempts() != want || outerCalls != want || innerCalls != 3 {
				t.Fatalf("nested result: %v; outer %d inner %d", err, outerCalls, innerCalls)
			}
		})
	}
}

func TestRetryNeverOverlapsAnAbandonedTargetOrResponseProcessor(t *testing.T) {
	for _, phase := range []string{"target", "response"} {
		for _, completion := range []string{"cancel", "deadline", "timeout", "drain"} {
			t.Run(phase+"/"+completion, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					release := make(chan struct{})
					var calls atomic.Int32
					policy := retryPolicy()
					if completion == "timeout" {
						policy.Timeout = time.Second
					}
					handle, dispatcher := policyRuntime(t, policy, func(context.Context, string) (string, error) {
						calls.Add(1)
						if phase == "target" {
							<-release
							return "", policyError(invocation.ErrorUnavailable)
						}
						return "result", nil
					})
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					if completion == "deadline" {
						var stop context.CancelFunc
						ctx, stop = context.WithTimeout(ctx, time.Second)
						defer stop()
					}
					done := make(chan error, 1)
					go func() {
						value, err := handle.InvokeWithResponse(ctx, "request", func(string) (string, error) { <-release; return "late", nil })
						if value != "" {
							t.Error("late response escaped")
						}
						done <- err
					}()
					synctest.Wait()
					if calls.Load() != 1 || dispatcher.ActiveAttempts() != 1 {
						t.Fatal("target not active")
					}
					switch completion {
					case "cancel":
						cancel()
					case "deadline", "timeout":
						time.Sleep(time.Second)
					case "drain":
						bounded, stop := context.WithTimeout(context.Background(), time.Millisecond)
						err := dispatcher.Drain(bounded)
						stop()
						if !errors.Is(err, invocation.ErrDrain) {
							t.Fatal(err)
						}
					}
					synctest.Wait()
					err := <-done
					var failure *invocation.Error
					if !errors.As(err, &failure) || failure.Completion() != invocation.CompletionResultUnknown || failure.Attempts() != 1 || calls.Load() != 1 || dispatcher.ActiveAttempts() != 1 {
						t.Fatalf("abandoned target: %v", err)
					}
					close(release)
					synctest.Wait()
					if calls.Load() != 1 || dispatcher.ActiveAttempts() != 0 {
						t.Fatal("late termination replayed work or retained a permit")
					}
					drainDispatcher(t, dispatcher)
				})
			})
		}
	}
}

func TestRetryBackoffIsInterruptedByCancellationAndShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			policy := retryPolicy()
			policy.Retry.Backoff = 30 * time.Second
			var calls atomic.Int32
			handle, dispatcher := policyRuntime(t, policy, func(context.Context, string) (string, error) {
				calls.Add(1)
				return "", policyError(invocation.ErrorUnavailable)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := handle.Invoke(ctx, ""); done <- err }()
			synctest.Wait()
			if calls.Load() != 1 || dispatcher.ActiveAttempts() != 0 {
				t.Fatal("not between attempts")
			}
			start := time.Now()
			if shutdown {
				drainDispatcher(t, dispatcher)
			} else {
				cancel()
			}
			synctest.Wait()
			var failure *invocation.Error
			if err := <-done; !errors.As(err, &failure) || failure.Completion() != invocation.CompletionResultKnown || failure.Attempts() != 1 || calls.Load() != 1 || time.Since(start) != 0 {
				t.Fatalf("backoff interruption: %v", err)
			}
		})
	}
}

func TestAdmissionExhaustionCanRetryWithoutEnteringTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var calls atomic.Int32
		handle, dispatcher := policyRuntime(t, retryPolicy(), func(context.Context, string) (string, error) { calls.Add(1); <-release; return "result", nil })
		done := make(chan error, 1)
		go func() { _, err := handle.Invoke(context.Background(), ""); done <- err }()
		synctest.Wait()
		value, err := handle.Invoke(context.Background(), "")
		var failure *invocation.Error
		if value != "" || !errors.As(err, &failure) || failure.Code() != invocation.ErrorResourceExhausted || failure.Completion() != invocation.CompletionNotStarted || failure.Attempts() != 3 || calls.Load() != 1 || dispatcher.ActiveAttempts() != 1 {
			t.Fatalf("saturation retry: %q, %v", value, err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestRetryCompletionRetainsEarlierTargetEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		policy := retryPolicy()
		policy.Retry.Backoff = time.Second
		release := make(chan struct{})
		calls := 0
		handle, dispatcher := policyRuntime(t, policy, func(_ context.Context, value string) (string, error) {
			calls++
			if value == "occupy" {
				<-release
				return value, nil
			}
			return "", policyError(invocation.ErrorUnavailable)
		})
		done := make(chan error, 1)
		go func() { _, err := handle.Invoke(context.Background(), "retry"); done <- err }()
		synctest.Wait()
		if calls != 1 || dispatcher.ActiveAttempts() != 0 {
			t.Fatal("first target has not terminated")
		}
		occupied := make(chan error, 1)
		go func() { _, err := handle.Invoke(context.Background(), "occupy"); occupied <- err }()
		synctest.Wait()
		time.Sleep(2 * time.Second)
		var failure *invocation.Error
		if err := <-done; !errors.As(err, &failure) || failure.Code() != invocation.ErrorResourceExhausted || failure.DetailCode() != "runtime.concurrency_exhausted" || failure.Completion() != invocation.CompletionResultKnown || failure.Attempts() != 3 || calls != 2 || dispatcher.ActiveAttempts() != 1 {
			t.Errorf("later admission erased earlier entry: %v", err)
		}
		close(release)
		if err := <-occupied; err != nil {
			t.Fatal(err)
		}
	})
}
