package audit

import (
	"context"
	"errors"
	"reflect"
	"time"
)

var (
	// ErrInvalidInvocationRecorder reports missing sink configuration, an
	// invalid timeout, or use of a zero recorder.
	ErrInvalidInvocationRecorder = errors.New("invalid runtime invocation audit recorder")
	// ErrAuditRecording reports a sink rejection, timeout, or panic without
	// exposing sink implementation details across the runtime boundary.
	ErrAuditRecording = errors.New("runtime invocation audit recording failed")
)

// InvocationSink accepts complete validated invocation records according to
// one documented persistence policy. Implementations must be safe for
// concurrent calls and honor the supplied context. PersistInvocation returns
// nil only after the record is durably stored or accepted by a bounded queue;
// queue saturation and backpressure rejection must return an error rather than
// silently dropping the record. Flush waits for all accepted records and
// reports deferred persistence failures.
type InvocationSink interface {
	PersistInvocation(context.Context, InvocationRecord) error
	Flush(context.Context) error
}

// InvocationRecorder is the mandatory validated handoff between governed
// dispatch and a configured synchronous or bounded-asynchronous audit sink.
// It is safe for concurrent use.
type InvocationRecorder struct {
	sink         InvocationSink
	writeTimeout time.Duration
}

// NewInvocationRecorder creates a recorder with one bounded independent
// timeout for each sink acceptance or flush operation.
func NewInvocationRecorder(sink InvocationSink, writeTimeout time.Duration) (*InvocationRecorder, error) {
	if nilInvocationSink(sink) || writeTimeout <= 0 {
		return nil, ErrInvalidInvocationRecorder
	}
	return &InvocationRecorder{sink: sink, writeTimeout: writeTimeout}, nil
}

// Record asks the configured sink to accept one complete terminal record.
// Audit delivery uses an independent bounded context so caller cancellation or
// an expired provider deadline cannot suppress the terminal record.
func (r *InvocationRecorder) Record(record InvocationRecord) (err error) {
	if !r.Valid() {
		return ErrInvalidInvocationRecorder
	}
	if !record.Valid() {
		return ErrInvalidInvocationRecord
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.writeTimeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = ErrAuditRecording
		}
	}()
	if r.sink.PersistInvocation(ctx, record) != nil {
		return ErrAuditRecording
	}
	return nil
}

// Flush waits for the configured sink to finish every previously accepted
// record using the same independent bounded audit context.
func (r *InvocationRecorder) Flush() (err error) {
	if !r.Valid() {
		return ErrInvalidInvocationRecorder
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.writeTimeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			err = ErrAuditRecording
		}
	}()
	if r.sink.Flush(ctx) != nil {
		return ErrAuditRecording
	}
	return nil
}

// Valid reports whether the recorder has complete immutable configuration.
func (r *InvocationRecorder) Valid() bool {
	return r != nil && r.sink != nil && r.writeTimeout > 0
}

func nilInvocationSink(sink InvocationSink) bool {
	if sink == nil {
		return true
	}
	value := reflect.ValueOf(sink)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
