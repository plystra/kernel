package audit_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plystra/kernel/audit"
)

func TestInvocationRecorderDeliversAndFlushesWithBoundedContexts(t *testing.T) {
	t.Parallel()

	sink := &captureInvocationSink{}
	const writeTimeout = 5 * time.Second
	recorder, err := audit.NewInvocationRecorder(sink, writeTimeout)
	if err != nil {
		t.Fatalf("NewInvocationRecorder: %v", err)
	}
	record := testInvocationRecord(t)
	if err := recorder.Record(record); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := recorder.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	records, persistRemaining, flushRemaining, flushes := sink.snapshot()
	if !recorder.Valid() || len(records) != 1 || records[0] != record || flushes != 1 {
		t.Fatalf("recorder delivery = %#v, flushes %d", records, flushes)
	}
	for name, remaining := range map[string]time.Duration{
		"persist": persistRemaining,
		"flush":   flushRemaining,
	} {
		if remaining <= 0 || remaining > writeTimeout {
			t.Fatalf("%s context remaining = %s", name, remaining)
		}
	}
}

func TestNewInvocationRecorderRejectsMissingSinkOrTimeout(t *testing.T) {
	t.Parallel()

	if recorder, err := audit.NewInvocationRecorder(nil, time.Second); !errors.Is(err, audit.ErrInvalidInvocationRecorder) || recorder != nil {
		t.Fatalf("nil sink recorder = %#v, %v", recorder, err)
	}
	var typedNil *captureInvocationSink
	if recorder, err := audit.NewInvocationRecorder(typedNil, time.Second); !errors.Is(err, audit.ErrInvalidInvocationRecorder) || recorder != nil {
		t.Fatalf("typed nil sink recorder = %#v, %v", recorder, err)
	}
	for _, timeout := range []time.Duration{0, -time.Nanosecond, -time.Second} {
		recorder, err := audit.NewInvocationRecorder(&captureInvocationSink{}, timeout)
		if !errors.Is(err, audit.ErrInvalidInvocationRecorder) || recorder != nil {
			t.Fatalf("NewInvocationRecorder(%s) = %#v, %v", timeout, recorder, err)
		}
	}
}

func TestInvocationRecorderRejectsInvalidRecordBeforeCallingSink(t *testing.T) {
	t.Parallel()

	sink := &countingInvocationSink{}
	recorder, err := audit.NewInvocationRecorder(sink, time.Second)
	if err != nil {
		t.Fatalf("NewInvocationRecorder: %v", err)
	}
	if err := recorder.Record(audit.InvocationRecord{}); !errors.Is(err, audit.ErrInvalidInvocationRecord) {
		t.Fatalf("zero Record error = %v", err)
	}
	if sink.persisted.Load() != 0 {
		t.Fatalf("invalid record reached sink %d time(s)", sink.persisted.Load())
	}
}

func TestInvocationRecorderRedactsSinkFailuresAndPanics(t *testing.T) {
	t.Parallel()

	record := testInvocationRecord(t)
	for _, test := range []struct {
		name string
		sink audit.InvocationSink
		run  func(*audit.InvocationRecorder) error
	}{
		{name: "persist error", sink: failingInvocationSink{persistError: errors.New("database password=secret")}, run: func(recorder *audit.InvocationRecorder) error { return recorder.Record(record) }},
		{name: "persist panic", sink: failingInvocationSink{persistPanic: true}, run: func(recorder *audit.InvocationRecorder) error { return recorder.Record(record) }},
		{name: "flush error", sink: failingInvocationSink{flushError: errors.New("queue token=secret")}, run: func(recorder *audit.InvocationRecorder) error { return recorder.Flush() }},
		{name: "flush panic", sink: failingInvocationSink{flushPanic: true}, run: func(recorder *audit.InvocationRecorder) error { return recorder.Flush() }},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder, err := audit.NewInvocationRecorder(test.sink, time.Second)
			if err != nil {
				t.Fatalf("NewInvocationRecorder: %v", err)
			}
			err = test.run(recorder)
			if !errors.Is(err, audit.ErrAuditRecording) || err.Error() != audit.ErrAuditRecording.Error() {
				t.Fatalf("recorder error = %v", err)
			}
		})
	}
}

func TestInvocationRecorderBoundsBlockedSinkOperations(t *testing.T) {
	t.Parallel()

	const timeout = 20 * time.Millisecond
	record := testInvocationRecord(t)
	for _, test := range []struct {
		name string
		sink audit.InvocationSink
		run  func(*audit.InvocationRecorder) error
	}{
		{name: "persist", sink: blockingInvocationSink{blockPersist: true}, run: func(recorder *audit.InvocationRecorder) error { return recorder.Record(record) }},
		{name: "flush", sink: blockingInvocationSink{blockFlush: true}, run: func(recorder *audit.InvocationRecorder) error { return recorder.Flush() }},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder, err := audit.NewInvocationRecorder(test.sink, timeout)
			if err != nil {
				t.Fatalf("NewInvocationRecorder: %v", err)
			}
			started := time.Now()
			err = test.run(recorder)
			if elapsed := time.Since(started); !errors.Is(err, audit.ErrAuditRecording) || elapsed < timeout || elapsed > 2*time.Second {
				t.Fatalf("blocked %s = %v after %s", test.name, err, elapsed)
			}
		})
	}
}

func TestInvocationRecorderIsSafeForConcurrentDelivery(t *testing.T) {
	sink := &countingInvocationSink{}
	recorder, err := audit.NewInvocationRecorder(sink, time.Second)
	if err != nil {
		t.Fatalf("NewInvocationRecorder: %v", err)
	}
	record := testInvocationRecord(t)

	const deliveries = 128
	var group sync.WaitGroup
	group.Add(deliveries)
	for range deliveries {
		go func() {
			defer group.Done()
			if err := recorder.Record(record); err != nil {
				t.Errorf("Record: %v", err)
			}
		}()
	}
	group.Wait()
	if sink.persisted.Load() != deliveries {
		t.Fatalf("persisted = %d, want %d", sink.persisted.Load(), deliveries)
	}
}

func TestZeroInvocationRecorderFailsClosed(t *testing.T) {
	t.Parallel()

	for name, recorder := range map[string]*audit.InvocationRecorder{
		"nil":  nil,
		"zero": {},
	} {
		if recorder.Valid() {
			t.Fatalf("%s recorder is valid", name)
		}
		if err := recorder.Record(audit.InvocationRecord{}); !errors.Is(err, audit.ErrInvalidInvocationRecorder) {
			t.Fatalf("%s Record error = %v", name, err)
		}
		if err := recorder.Flush(); !errors.Is(err, audit.ErrInvalidInvocationRecorder) {
			t.Fatalf("%s Flush error = %v", name, err)
		}
	}
}

func testInvocationRecord(t *testing.T) audit.InvocationRecord {
	t.Helper()
	record, err := audit.NewInvocationRecord(validInvocationRecordOptions(t))
	if err != nil {
		t.Fatalf("NewInvocationRecord: %v", err)
	}
	return record
}

type captureInvocationSink struct {
	mu               sync.Mutex
	records          []audit.InvocationRecord
	persistRemaining time.Duration
	flushRemaining   time.Duration
	flushes          int
}

func (s *captureInvocationSink) PersistInvocation(ctx context.Context, record audit.InvocationRecord) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("missing persist deadline")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persistRemaining = time.Until(deadline)
	s.records = append(s.records, record)
	return nil
}

func (s *captureInvocationSink) Flush(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("missing flush deadline")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushRemaining = time.Until(deadline)
	s.flushes++
	return nil
}

func (s *captureInvocationSink) snapshot() ([]audit.InvocationRecord, time.Duration, time.Duration, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.InvocationRecord(nil), s.records...), s.persistRemaining, s.flushRemaining, s.flushes
}

type countingInvocationSink struct {
	persisted atomic.Int64
}

func (s *countingInvocationSink) PersistInvocation(context.Context, audit.InvocationRecord) error {
	s.persisted.Add(1)
	return nil
}

func (*countingInvocationSink) Flush(context.Context) error { return nil }

type failingInvocationSink struct {
	persistError error
	flushError   error
	persistPanic bool
	flushPanic   bool
}

func (s failingInvocationSink) PersistInvocation(context.Context, audit.InvocationRecord) error {
	if s.persistPanic {
		panic("sensitive persist panic")
	}
	return s.persistError
}

func (s failingInvocationSink) Flush(context.Context) error {
	if s.flushPanic {
		panic("sensitive flush panic")
	}
	return s.flushError
}

type blockingInvocationSink struct {
	blockPersist bool
	blockFlush   bool
}

func (s blockingInvocationSink) PersistInvocation(ctx context.Context, _ audit.InvocationRecord) error {
	if s.blockPersist {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (s blockingInvocationSink) Flush(ctx context.Context) error {
	if s.blockFlush {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

var _ audit.InvocationSink = (*captureInvocationSink)(nil)
var _ audit.InvocationSink = (*countingInvocationSink)(nil)
var _ audit.InvocationSink = failingInvocationSink{}
var _ audit.InvocationSink = blockingInvocationSink{}
