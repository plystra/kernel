package invocation

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/plystra/kernel/capability"
)

type pendingDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c pendingDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestScheduledAttemptCannotEnterAfterBudgetBeforeTimerCallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dispatcher := newTestDispatcher(t)
		// Model the interval after a deadline passes but before its timer runs.
		ctx := pendingDeadlineContext{Context: context.Background(), deadline: time.Now().Add(time.Second)}
		result := &targetResult[struct{}]{attempt: targetAttempt{cancel: func() {}}, done: make(chan struct{})}
		if dispatcher.registerAttempt(ctx, &result.attempt, testBinding(t, "example.budget/v1")) != nil {
			t.Fatal("attempt was not registered")
		}
		time.Sleep(time.Second)
		contract := capability.MustParseContract[struct{}, struct{}]("example.budget/v1")
		endpoint, err := NewEndpoint(contract, func(context.Context, struct{}) (struct{}, error) {
			t.Error("expired scheduled attempt entered target")
			return struct{}{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		executeAttempt(ctx, dispatcher, endpoint, struct{}{}, result, nil)
		var boundary *Error
		if !errors.As(result.err, &boundary) || boundary.Code() != ErrorTimeout || boundary.Completion() != CompletionNotStarted || dispatcher.ActiveAttempts() != 0 {
			t.Fatalf("scheduled expiration = %v", result.err)
		}
	})
}

func TestAbandonedScheduledAttemptCannotEnterTarget(t *testing.T) {
	dispatcher := newTestDispatcher(t)
	attempt := &targetAttempt{cancel: func() {}}
	binding := testBinding(t, "example.attempt/v1")
	binding.policy.ConcurrencyLimit = 1
	if dispatcher.registerAttempt(context.Background(), attempt, binding) != nil {
		t.Fatal("attempt was not registered")
	}
	boundary := dispatcher.abandonAttempt(attempt, newNotStartedBoundary(ErrorCancelled, detailInvocationCancelled))
	if boundary.Completion() != CompletionNotStarted || dispatcher.enterAttempt(context.Background(), attempt) {
		t.Fatal("caller cancellation allowed its scheduled target to enter later")
	}
	if dispatcher.ActiveAttempts() != 1 {
		t.Fatal("scheduled worker lost ownership before acknowledging abandonment")
	}
	next := &targetAttempt{cancel: func() {}}
	if err := dispatcher.registerAttempt(context.Background(), next, binding); err == nil || err.Code() != ErrorResourceExhausted {
		t.Fatalf("scheduled worker released its permit early: %v", err)
	}
	dispatcher.finishAttempt(attempt)
	if err := dispatcher.registerAttempt(context.Background(), next, binding); err != nil {
		t.Fatalf("acknowledged abandonment retained its permit: %v", err)
	}
	dispatcher.finishAttempt(next)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := dispatcher.Drain(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestEnteredAttemptRetainsUnknownCompletionUntilTermination(t *testing.T) {
	dispatcher := newTestDispatcher(t)
	attempt := &targetAttempt{cancel: func() {}}
	if dispatcher.registerAttempt(context.Background(), attempt, testBinding(t, "example.attempt/v1")) != nil {
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
	if dispatcher.registerAttempt(context.Background(), &result.attempt, testBinding(t, "example.closing/v1")) != nil {
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
	if dispatcher.registerAttempt(context.Background(), &result.attempt, testBinding(t, "example.closing/v1")) != nil {
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
