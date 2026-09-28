package invocation

import (
	"context"
	"time"
)

func invokePolicy[Request, Response any](ctx context.Context, cancel func(), handle Handle[Request, Response], binding Binding, request Request, process func(Response) (Response, error)) (Response, error) {
	var zero Response
	frame, _ := runtimeFrameFrom(ctx)
	maximum := 1
	if !frame.retryOwner.Valid() && binding.policy.Retry.MaxAttempts > 1 {
		frame.retryOwner = frame.invocationID
		maximum = binding.policy.Retry.MaxAttempts
	}
	entered := false
	for number := 1; number <= maximum; number++ {
		frame.attempt = number
		attemptContext := ctx
		if maximum > 1 {
			attemptContext = context.WithValue(ctx, runtimeFrameKey{}, frame)
		}
		response, err := invokeBounded(attemptContext, cancel, handle, binding, request, process)
		if err == nil {
			return response, nil
		}
		entered = entered || CompletionOf(err) != CompletionNotStarted
		if number == maximum || !retryable(err) {
			return zero, withAttemptOutcome(err, number, entered)
		}
		// invokeBounded only returns a retryable target result after the worker
		// has released its permit and signalled actual termination.
		if boundary := waitRetry(ctx, handle.dispatcher, binding.policy.Retry.Backoff); boundary != nil {
			return zero, withAttemptOutcome(boundary, number, entered)
		}
	}
	panic("unreachable invocation policy")
}

func retryable(err error) bool {
	boundary, ok := err.(*Error)
	if !ok || !boundary.valid() {
		return false
	}
	if boundary.completion == CompletionNotStarted {
		return boundary.code == ErrorResourceExhausted && boundary.detailCode == detailConcurrencyExhausted
	}
	return boundary.completion == CompletionResultKnown && (boundary.code == ErrorUnavailable || boundary.code == ErrorResourceExhausted)
}

func waitRetry(ctx context.Context, dispatcher *Dispatcher, backoff time.Duration) *Error {
	if boundary := invocationContextError(ctx); boundary != nil {
		return boundary
	}
	if backoff > 0 {
		timer := time.NewTimer(backoff)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return invocationContextError(ctx)
		case <-dispatcher.closing:
			return newNotStartedBoundary(ErrorUnavailable, detailDispatcherDraining)
		case <-timer.C:
		}
	}
	if boundary := invocationContextError(ctx); boundary != nil {
		return boundary
	}
	select {
	case <-dispatcher.closing:
		return newNotStartedBoundary(ErrorUnavailable, detailDispatcherDraining)
	default:
		return nil
	}
}

func withAttemptOutcome(err error, count int, entered bool) error {
	switch boundary := err.(type) {
	case *Error:
		copy := *boundary
		copy.attempts = count
		// A later pre-entry rejection cannot erase an earlier known target result.
		if entered && copy.completion == CompletionNotStarted {
			copy.completion = CompletionResultKnown
		}
		return &copy
	case *SemanticError:
		copy := *boundary
		copy.attempts = count
		return &copy
	default:
		return err
	}
}
