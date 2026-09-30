package invocation

import (
	"context"
	"errors"
	"time"

	"github.com/plystra/kernel/capability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ErrInvalidTelemetry reports failure to construct the intrinsic instruments,
// without exposing a provider's private error or panic value.
var ErrInvalidTelemetry = errors.New("invalid invocation telemetry")

type invocationMetrics struct {
	caller metric.Float64Histogram
	target metric.Float64Histogram
}

func newInvocationMetrics(provider metric.MeterProvider) (instruments invocationMetrics, err error) {
	defer func() {
		if recover() != nil {
			instruments, err = invocationMetrics{}, ErrInvalidTelemetry
		}
	}()
	if provider == nil {
		provider = otel.GetMeterProvider()
	}
	meter := provider.Meter("github.com/plystra/kernel/invocation")
	caller, err := meter.Float64Histogram("plystra.invocation.caller.duration",
		metric.WithUnit("s"), metric.WithDescription("Logical-call duration through caller completion, including preparation and retries."),
		metric.WithExplicitBucketBoundaries(.001, .005, .01, .05, .1, .5, 1, 5, 10, 30, 60))
	if err != nil || caller == nil {
		return invocationMetrics{}, ErrInvalidTelemetry
	}
	target, err := meter.Float64Histogram("plystra.invocation.target.duration",
		metric.WithUnit("s"), metric.WithDescription("Entered target-attempt duration through adapter and response-processor termination."),
		metric.WithExplicitBucketBoundaries(.001, .005, .01, .05, .1, .5, 1, 5, 10, 30, 60))
	if err != nil || target == nil {
		return invocationMetrics{}, ErrInvalidTelemetry
	}
	return invocationMetrics{caller: caller, target: target}, nil
}

func recordInvocationMetric(instrument metric.Float64Histogram, definition capability.Definition, constructor string, elapsed time.Duration, err error, target, late bool) {
	// Instrumentation is not an authority over invocation outcomes or lifetime
	// cleanup. No caller context, payload, or unnormalized error reaches it.
	defer func() { _ = recover() }()
	ctx := context.Background()
	if instrument == nil || !instrument.Enabled(ctx) {
		return
	}
	outcome, code, completion := "success", "", CompletionResultKnown
	switch boundary := err.(type) {
	case nil:
	case *Error:
		outcome, code, completion = "runtime_error", boundary.Code().String(), boundary.Completion()
	case *SemanticError:
		outcome, code, completion = "semantic_error", boundary.Code(), boundary.Completion()
	default:
		outcome, code, completion = "runtime_error", ErrorInternal.String(), CompletionResultUnknown
	}
	attributes := make([]attribute.KeyValue, 0, 6)
	attributes = append(attributes,
		attribute.String("plystra.interface.id", definition.Identifier().String()),
		attribute.String("plystra.implementation.constructor", constructor),
		attribute.String("plystra.invocation.outcome", outcome),
		attribute.String("plystra.error.code", code),
		attribute.String("plystra.invocation.completion", completion.String()),
	)
	if target {
		attributes = append(attributes, attribute.Bool("plystra.invocation.target.late", late))
	}
	instrument.Record(ctx, elapsed.Seconds(), metric.WithAttributeSet(attribute.NewSet(attributes...)))
}
