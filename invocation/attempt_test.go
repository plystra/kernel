package invocation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/plystra/kernel/capability"
)

func TestAbandonedScheduledAttemptCannotEnterTarget(t *testing.T) {
	dispatcher := newTestDispatcher(t)
	attempt := &targetAttempt{cancel: func() {}}
	if !dispatcher.registerAttempt(attempt) {
		t.Fatal("attempt was not registered")
	}
	boundary := dispatcher.abandonAttempt(attempt, newNotStartedBoundary(ErrorCancelled, detailInvocationCancelled))
	if boundary.Completion() != CompletionNotStarted || dispatcher.enterAttempt(context.Background(), attempt) {
		t.Fatal("caller cancellation allowed its scheduled target to enter later")
	}
	if dispatcher.ActiveAttempts() != 1 {
		t.Fatal("scheduled worker lost ownership before acknowledging abandonment")
	}
	dispatcher.finishAttempt(attempt)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Drain(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestEnteredAttemptRetainsUnknownCompletionUntilTermination(t *testing.T) {
	dispatcher := newTestDispatcher(t)
	attempt := &targetAttempt{cancel: func() {}}
	if !dispatcher.registerAttempt(attempt) {
		t.Fatal("attempt was not registered")
	}
	if !dispatcher.enterAttempt(context.Background(), attempt) {
		t.Fatal("target was not admitted")
	}
	boundary := dispatcher.abandonAttempt(attempt, newNotStartedBoundary(ErrorCancelled, detailInvocationCancelled))
	if boundary.Completion() != CompletionResultUnknown || dispatcher.ActiveAttempts() != 1 {
		t.Fatal("caller cancellation released an entered target")
	}
	dispatcher.finishAttempt(attempt)
}

func TestDrainClosureBeforeContextCancellationReturnsNotStarted(t *testing.T) {
	dispatcher := newTestDispatcher(t)
	result := &targetResult[struct{}]{attempt: targetAttempt{cancel: func() {}}, done: make(chan struct{})}
	if !dispatcher.registerAttempt(&result.attempt) {
		t.Fatal("attempt was not registered")
	}
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := dispatcher.Drain(expired); !errors.Is(err, ErrDrain) {
		t.Fatalf("drain = %v", err)
	}
	contract := capability.MustParseContract[struct{}, struct{}]("example.closing/v1")
	endpoint, err := NewEndpoint(contract, func(context.Context, struct{}) (struct{}, error) {
		t.Error("closed dispatcher entered target")
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	executeAttempt(context.Background(), dispatcher, endpoint, struct{}{}, result, nil)
	var boundary *Error
	if !errors.As(result.err, &boundary) || boundary.Code() != ErrorUnavailable || boundary.Completion() != CompletionNotStarted {
		t.Fatalf("closure outcome = %v", result.err)
	}
	if dispatcher.ActiveAttempts() != 0 {
		t.Fatal("rejected target retained its attempt")
	}
}

func TestDrainClosureBeforeContextCancellationSuppressesResponseProcessor(t *testing.T) {
	dispatcher := newTestDispatcher(t)
	result := &targetResult[string]{attempt: targetAttempt{cancel: func() {}}, done: make(chan struct{})}
	if !dispatcher.registerAttempt(&result.attempt) {
		t.Fatal("attempt was not registered")
	}
	contract := capability.MustParseContract[struct{}, string]("example.closing/v1")
	endpoint, err := NewEndpoint(contract, func(context.Context, struct{}) (string, error) {
		expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		if err := dispatcher.Drain(expired); !errors.Is(err, ErrDrain) {
			t.Fatalf("drain = %v", err)
		}
		return "unprocessed", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	executeAttempt(context.Background(), dispatcher, endpoint, struct{}{}, result, func(value string) (string, error) {
		t.Error("closed dispatcher started response processing")
		return value, nil
	})
	var boundary *Error
	if result.response != "" || !errors.As(result.err, &boundary) || boundary.Code() != ErrorCancelled || boundary.Completion() != CompletionResultUnknown {
		t.Fatalf("closure outcome = %q, %v", result.response, result.err)
	}
	if dispatcher.ActiveAttempts() != 0 {
		t.Fatal("suppressed response retained its attempt")
	}
}
