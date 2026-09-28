package invocation_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plystra/kernel/invocation"
)

func TestResponseProcessorCopiesTargetStorageBeforeReturn(t *testing.T) {
	retained := []byte("response")
	handle, dispatcher := publicResponseRuntime(t, time.Second, func(context.Context, error) ([]byte, error) {
		return retained, nil
	})
	var calls int
	response, err := handle.InvokeWithResponse(context.Background(), nil, func(value []byte) ([]byte, error) {
		calls++
		if dispatcher.ActiveAttempts() != 1 {
			t.Error("response processor ran outside its attempt")
		}
		return bytes.Clone(value), nil
	})
	if err != nil || !bytes.Equal(response, retained) || calls != 1 || dispatcher.ActiveAttempts() != 0 {
		t.Fatalf("processed response = %q, %v, calls %d, attempts %d", response, err, calls, dispatcher.ActiveAttempts())
	}
	drainDispatcher(t, dispatcher)
	for index := range retained {
		retained[index] = 0
	}
	if string(response) != "response" {
		t.Fatal("dependency cleanup changed caller-owned response storage")
	}
}

func TestResponseProcessingRemainsTrackedUntilDrainCompletes(t *testing.T) {
	processing := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	handle, dispatcher := publicErrorRuntime(t, time.Minute, func(context.Context, error) (string, error) {
		return "retained storage", nil
	})
	caller := make(chan error, 1)
	go func() {
		response, err := handle.InvokeWithResponse(context.Background(), nil, func(response string) (string, error) {
			close(processing)
			<-release
			return response, nil
		})
		if response != "" {
			t.Error("shutdown delivered a late response")
		}
		caller <- err
	}()
	awaitSignal(t, processing)
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := dispatcher.Drain(expired); !errors.Is(err, invocation.ErrDrain) {
		t.Fatalf("expired drain = %v", err)
	}
	if dispatcher.ActiveAttempts() != 1 {
		t.Error("drain released the attempt while its response was still being processed")
	}
	if err := awaitError(t, caller); !errors.Is(err, context.Canceled) || invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
		t.Fatalf("shutdown completion = %v", err)
	}
	once.Do(func() { close(release) })
	drainDispatcher(t, dispatcher)
}

func TestResponseProcessorCancellationDiscardsLateOutcomes(t *testing.T) {
	for _, outcome := range []string{"success", "error", "panic", "goexit"} {
		t.Run(outcome, func(t *testing.T) {
			processing := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			handle, dispatcher := publicErrorRuntime(t, time.Minute, func(context.Context, error) (string, error) {
				return "retained", nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			caller := make(chan error, 1)
			go func() {
				response, err := handle.InvokeWithResponse(ctx, nil, func(value string) (string, error) {
					close(processing)
					<-release
					switch outcome {
					case "error":
						return value, &privateError{}
					case "panic":
						panic("private processor panic")
					case "goexit":
						runtime.Goexit()
					}
					return value, nil
				})
				if response != "" {
					t.Error("late processor response escaped")
				}
				caller <- err
			}()
			awaitSignal(t, processing)
			cancel()
			if err := awaitError(t, caller); !errors.Is(err, context.Canceled) || invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
				t.Fatalf("caller completion = %v", err)
			}
			if dispatcher.ActiveAttempts() != 1 {
				t.Fatal("cancellation released a still-running response processor")
			}
			once.Do(func() { close(release) })
			drainDispatcher(t, dispatcher)
		})
	}
}

func TestResponseProcessorDeadlineAndDrainRetry(t *testing.T) {
	processing := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	handle, dispatcher := publicErrorRuntime(t, 100*time.Millisecond, func(context.Context, error) (string, error) {
		return "retained", nil
	})
	caller := make(chan error, 1)
	go func() {
		_, err := handle.InvokeWithResponse(context.Background(), nil, func(value string) (string, error) {
			close(processing)
			<-release
			return value, nil
		})
		caller <- err
	}()
	awaitSignal(t, processing)
	if err := awaitError(t, caller); !errors.Is(err, context.DeadlineExceeded) || invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
		t.Fatalf("deadline completion = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := dispatcher.Drain(ctx); !errors.Is(err, invocation.ErrDrain) || !errors.Is(err, context.DeadlineExceeded) || dispatcher.ActiveAttempts() != 1 {
		t.Fatalf("drain during processing = %v, attempts %d", err, dispatcher.ActiveAttempts())
	}
	once.Do(func() { close(release) })
	drainDispatcher(t, dispatcher)
}

func TestResponseProcessorSkippedWithoutSuccessfulTargetResult(t *testing.T) {
	for _, outcome := range []string{"nil processor", "cancelled", "nil context", "target error", "target panic", "target goexit", "closed", "invalid handle"} {
		t.Run(outcome, func(t *testing.T) {
			var targets, processors atomic.Int32
			handle, dispatcher := publicErrorRuntime(t, time.Second, func(context.Context, error) (string, error) {
				targets.Add(1)
				switch outcome {
				case "target error":
					return "private partial response", invocation.NewSemanticError("already_exists", &privateError{})
				case "target panic":
					panic("private target panic")
				case "target goexit":
					runtime.Goexit()
				}
				return "target response", nil
			})
			process := func(value string) (string, error) { processors.Add(1); return value, nil }
			ctx := context.Background()
			switch outcome {
			case "nil processor":
				process = nil
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			case "nil context":
				ctx = nil
			case "closed":
				drainDispatcher(t, dispatcher)
			case "invalid handle":
				handle = invocation.Handle[error, string]{}
			}
			response, err := handle.InvokeWithResponse(ctx, nil, process)
			wantTargets := int32(0)
			if strings.HasPrefix(outcome, "target ") {
				wantTargets = 1
			}
			if err == nil || response != "" || processors.Load() != 0 || targets.Load() != wantTargets {
				t.Fatalf("result = %q, %v, targets %d, processors %d", response, err, targets.Load(), processors.Load())
			}
			if wantTargets == 0 && invocation.CompletionOf(err) != invocation.CompletionNotStarted {
				t.Fatalf("pre-entry completion = %v", err)
			}
			if outcome == "target error" {
				var semantic *invocation.SemanticError
				if !errors.As(err, &semantic) || semantic.Code() != "already_exists" {
					t.Fatalf("target error was changed by response processing: %v", err)
				}
			}
			drainDispatcher(t, dispatcher)
		})
	}
}

func TestResponseProcessorSkippedAfterCallerAlreadyCompleted(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	handle, dispatcher := publicErrorRuntime(t, time.Minute, func(context.Context, error) (string, error) {
		close(entered)
		<-release
		return "late target response", nil
	})
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	caller := make(chan error, 1)
	go func() {
		_, err := handle.InvokeWithResponse(ctx, nil, func(value string) (string, error) {
			calls.Add(1)
			return value, nil
		})
		caller <- err
	}()
	awaitSignal(t, entered)
	cancel()
	if err := awaitError(t, caller); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	drainDispatcher(t, dispatcher)
	if calls.Load() != 0 {
		t.Fatal("late target result was processed after caller completion")
	}
}

func TestConcurrentResponseProcessorsArePerCall(t *testing.T) {
	handle, dispatcher := publicErrorRuntime(t, 5*time.Second, func(context.Context, error) (string, error) {
		return "result", nil
	})
	var wg sync.WaitGroup
	for index := range 64 {
		wg.Go(func() {
			want := fmt.Sprintf("result-%d", index)
			got, err := handle.InvokeWithResponse(context.Background(), nil, func(value string) (string, error) {
				return fmt.Sprintf("%s-%d", value, index), nil
			})
			if err != nil || got != want {
				t.Errorf("concurrent result = %q, %v; want %q", got, err, want)
			}
		})
	}
	wg.Wait()
	drainDispatcher(t, dispatcher)
}

func TestResponseProcessorPreservesSafeValidationBoundary(t *testing.T) {
	handle, _ := publicErrorRuntime(t, time.Second, func(context.Context, error) (string, error) { return "invalid", nil })
	boundary, err := invocation.NewError(invocation.ErrorInternal, "contract.response_invalid")
	if err != nil {
		t.Fatal(err)
	}
	response, err := handle.InvokeWithResponse(context.Background(), nil, func(string) (string, error) {
		return "private partial response", fmt.Errorf("private validation detail: %w", boundary)
	})
	var failure *invocation.Error
	if response != "" || !errors.As(err, &failure) || failure.Code() != boundary.Code() || failure.DetailCode() != boundary.DetailCode() || failure.Attempts() != 1 || boundary.Attempts() != 0 || invocation.CompletionOf(err) != invocation.CompletionResultKnown {
		t.Fatalf("validation result = %q, %v", response, err)
	}
}

func FuzzResponseProcessing(f *testing.F) {
	for mode := range uint8(9) {
		f.Add(mode)
	}
	f.Fuzz(func(t *testing.T, mode uint8) {
		mode %= 9
		handle, dispatcher := publicErrorRuntime(t, time.Second, func(context.Context, error) (string, error) { return "success", nil })
		response, err := handle.InvokeWithResponse(context.Background(), nil, func(value string) (string, error) {
			switch mode {
			case 0:
				return value, nil
			case 1:
				boundary, _ := invocation.NewError(invocation.ErrorInternal, "contract.response_invalid")
				return "private partial", boundary
			case 2:
				return "private partial", &privateError{}
			case 3:
				return "private partial", invocation.NewSemanticError("already_exists", &privateError{})
			case 4:
				return "private partial", invocation.NewResultUnknown(&privateError{})
			case 5:
				panic("private processor panic")
			case 6:
				runtime.Goexit()
			case 7:
				return "private partial", context.Canceled
			}
			return "private partial", &responseErrorCycle{}
		})
		if mode == 0 {
			if response != "success" || err != nil {
				t.Fatalf("success = %q, %v", response, err)
			}
		} else {
			var boundary *invocation.Error
			if response != "" || !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorInternal {
				t.Fatalf("processor failure = %q, %v", response, err)
			}
			completion := invocation.CompletionResultKnown
			if mode == 4 || mode == 8 {
				completion = invocation.CompletionResultUnknown
			}
			if invocation.CompletionOf(err) != completion || strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), "private") {
				t.Fatalf("unsafe processor failure = %v (%s)", err, invocation.CompletionOf(err))
			}
		}
		drainDispatcher(t, dispatcher)
	})
}

type responseErrorCycle struct{}

func (*responseErrorCycle) Error() string     { panic("private error must not be formatted") }
func (err *responseErrorCycle) Unwrap() error { return err }

func BenchmarkKernelResponseProcessing(b *testing.B) {
	for _, processed := range []bool{false, true} {
		b.Run(fmt.Sprintf("processed=%t", processed), func(b *testing.B) {
			handle, _ := publicErrorRuntime(b, time.Minute, func(context.Context, error) (string, error) { return "result", nil })
			process := func(value string) (string, error) { return value, nil }
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				var err error
				if processed {
					_, err = handle.InvokeWithResponse(context.Background(), nil, process)
				} else {
					_, err = handle.Invoke(context.Background(), nil)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
