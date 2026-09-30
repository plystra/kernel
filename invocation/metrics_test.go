package invocation_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const callerDuration = "plystra.invocation.caller.duration"
const targetDuration = "plystra.invocation.target.duration"

func metricProvider(t testing.TB) (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return provider, reader
}

func metricRuntime(t testing.TB, provider metric.MeterProvider, id string, policy invocation.Policy, handler capability.Handler[string, string]) (invocation.Handle[string, string], *invocation.Dispatcher) {
	t.Helper()
	contract := capability.MustParseContractWithSemanticErrors[string, string](id, "not_ready")
	endpoint, err := invocation.NewEndpoint(contract, handler)
	if err != nil {
		t.Fatal(err)
	}
	build, err := invocation.NewModuleBuild("github.com/acme/metrics", "v1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := invocation.NewBinding(invocation.BindingOptions{
		Kind: invocation.BindingKindImplementation, Constructor: "github.com/acme/metrics.New",
		ModuleBuild: build, SelectionReason: invocation.SelectionReasonUniqueCompatible,
		ContractDigest: sha256.Sum256([]byte(id)), Policy: policy,
	}, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := invocation.NewCatalog([]invocation.Binding{binding})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion, MeterProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.OpenAdmission(); err != nil {
		t.Fatal(err)
	}
	handle, err := invocation.NewHandle(dispatcher, contract, true)
	if err != nil {
		t.Fatal(err)
	}
	return handle, dispatcher
}

func collectLifetime(t testing.TB, reader *sdkmetric.ManualReader) map[string][]metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	result := make(map[string][]metricdata.HistogramDataPoint[float64])
	for _, scope := range collected.ScopeMetrics {
		if scope.Scope.Name != "github.com/plystra/kernel/invocation" {
			t.Fatalf("unexpected scope: %q", scope.Scope.Name)
		}
		for _, instrument := range scope.Metrics {
			histogram, ok := instrument.Data.(metricdata.Histogram[float64])
			if !ok || instrument.Unit != "s" || (instrument.Name != callerDuration && instrument.Name != targetDuration) {
				t.Fatalf("unexpected metric: %#v", instrument)
			}
			result[instrument.Name] = histogram.DataPoints
			for _, point := range histogram.DataPoints {
				for _, attr := range point.Attributes.ToSlice() {
					switch string(attr.Key) {
					case "plystra.interface.id", "plystra.implementation.constructor", "plystra.invocation.outcome", "plystra.error.code", "plystra.invocation.completion", "plystra.invocation.target.late":
					default:
						t.Fatalf("unexpected metric label: %s", attr.Key)
					}
					if strings.Contains(attr.Value.String(), "private") {
						t.Fatal("metric leaked private data")
					}
				}
			}
		}
	}
	return result
}

func assertMetric(t testing.TB, points []metricdata.HistogramDataPoint[float64], count uint64, id, outcome, code, completion string, late bool) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	for _, point := range points {
		attrs := point.Attributes
		identity, _ := attrs.Value("plystra.interface.id")
		status, _ := attrs.Value("plystra.invocation.outcome")
		category, _ := attrs.Value("plystra.error.code")
		certainty, _ := attrs.Value("plystra.invocation.completion")
		isLate, _ := attrs.Value("plystra.invocation.target.late")
		constructor, _ := attrs.Value("plystra.implementation.constructor")
		if identity.AsString() == id && status.AsString() == outcome && category.AsString() == code && certainty.AsString() == completion && isLate.AsBool() == late {
			if point.Count != count || point.Sum < 0 || constructor.AsString() != "github.com/acme/metrics.New" {
				t.Fatalf("incorrect metric: %#v", point)
			}
			return point
		}
	}
	t.Fatalf("missing metric %s %s %s %s late=%v in %#v", id, outcome, code, completion, late, points)
	return metricdata.HistogramDataPoint[float64]{}
}

func TestLifetimeMetricsRecordUnreadyCallerWithoutTargetOrRetry(t *testing.T) {
	provider, reader := metricProvider(t)
	policy := publicPolicy(time.Minute, 1)
	policy.Retry = invocation.RetryPolicy{Eligibility: invocation.RetryReplaySafe, MaxAttempts: 3}
	contract := capability.MustParseContract[string, string]("example.startup-metrics/v1")
	endpoint, err := invocation.NewEndpoint(contract, func(context.Context, string) (string, error) {
		t.Error("unready invocation entered target")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	options := admissionBindingOptions(t, 1)
	options.ModuleBuild, err = invocation.NewModuleBuild("github.com/acme/metrics", "v1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	options.Constructor = "github.com/acme/metrics.New"
	options.Policy = policy
	binding, err := invocation.NewBinding(options, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := invocation.NewCatalog([]invocation.Binding{binding})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion, MeterProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	handle, err := invocation.NewHandle(dispatcher, contract, true)
	if err != nil {
		t.Fatal(err)
	}
	value, err := handle.Invoke(context.Background(), "private request")
	assertStartupRejection(t, value, err, "runtime.dispatcher_not_ready", 0)
	metrics := collectLifetime(t, reader)
	assertMetric(t, metrics[callerDuration], 1, "example.startup-metrics/v1", "runtime_error", "unavailable", "not_started", false)
	if len(metrics[targetDuration]) != 0 || dispatcher.ActiveAttempts() != 0 {
		t.Fatal("unready call recorded a target or retained a permit")
	}
}

func TestLifetimeMetricsIncludePreparationAndResponseProcessing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		provider, reader := metricProvider(t)
		handle, _ := metricRuntime(t, provider, "example.timing/v1", publicPolicy(0, 1), func(context.Context, string) (string, error) {
			time.Sleep(3 * time.Second)
			return "private response", nil
		})
		_, err := handle.InvokeWithPreparation(context.Background(), "private request", func(value string) (string, error) {
			time.Sleep(2 * time.Second)
			return value, nil
		}, func(value string) (string, error) {
			time.Sleep(4 * time.Second)
			return value, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		metrics := collectLifetime(t, reader)
		caller := assertMetric(t, metrics[callerDuration], 1, "example.timing/v1", "success", "", "result_known", false)
		target := assertMetric(t, metrics[targetDuration], 1, "example.timing/v1", "success", "", "result_known", false)
		if caller.Sum != 9 || target.Sum != 7 || caller.Attributes.Len() != 5 || target.Attributes.Len() != 6 {
			t.Fatalf("durations or labels: caller %#v; target %#v", caller, target)
		}
	})
}

func TestLifetimeMetricsSeparateLateTargetFromCompletedCaller(t *testing.T) {
	for _, reason := range []string{"cancel", "deadline", "policy", "shutdown"} {
		for _, outcome := range []string{"success", "error", "panic", "goexit"} {
			t.Run(reason+"/"+outcome, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					provider, reader := metricProvider(t)
					entered, release := make(chan struct{}), make(chan struct{})
					var once sync.Once
					defer once.Do(func() { close(release) })
					handle, dispatcher := metricRuntime(t, provider, "example.late/v1", publicPolicy(2*time.Second, 1), func(context.Context, string) (string, error) {
						close(entered)
						<-release
						switch outcome {
						case "error":
							return "", errors.New("private failure")
						case "panic":
							panic("private panic")
						case "goexit":
							runtime.Goexit()
						}
						return "private response", nil
					})
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					if reason == "deadline" {
						var stop context.CancelFunc
						ctx, stop = context.WithTimeout(ctx, time.Second)
						defer stop()
					}
					returned := make(chan error, 1)
					go func() { _, err := handle.Invoke(ctx, "private request"); returned <- err }()
					<-entered
					expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					defer stop()
					switch reason {
					case "cancel":
						cancel()
					case "shutdown":
						if err := dispatcher.Drain(expired); !errors.Is(err, invocation.ErrDrain) {
							t.Fatal(err)
						}
					}
					if err := <-returned; invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
						t.Fatal(err)
					}
					metrics := collectLifetime(t, reader)
					code := "cancelled"
					if reason == "deadline" || reason == "policy" {
						code = "timeout"
					}
					assertMetric(t, metrics[callerDuration], 1, "example.late/v1", "runtime_error", code, "result_unknown", false)
					if len(metrics[targetDuration]) != 0 || dispatcher.ActiveAttempts() != 1 {
						t.Fatal("caller completion reported target termination")
					}
					if err := dispatcher.Drain(expired); !errors.Is(err, invocation.ErrDrain) {
						t.Fatal(err)
					}
					time.Sleep(3 * time.Second)
					once.Do(func() { close(release) })
					drainDispatcher(t, dispatcher)
					metrics = collectLifetime(t, reader)
					assertMetric(t, metrics[callerDuration], 1, "example.late/v1", "runtime_error", code, "result_unknown", false)
					status, targetCode := "success", ""
					if outcome != "success" {
						status, targetCode = "runtime_error", "internal"
					}
					target := assertMetric(t, metrics[targetDuration], 1, "example.late/v1", status, targetCode, "result_known", true)
					if target.Sum < 3 {
						t.Fatal("late target duration stopped at caller completion")
					}
				})
			})
		}
	}
}

func TestLifetimeMetricsRetainResponseProcessorOwnership(t *testing.T) {
	provider, reader := metricProvider(t)
	handle, dispatcher := metricRuntime(t, provider, "example.response/v1", publicPolicy(0, 1), func(context.Context, string) (string, error) { return "response", nil })
	processing, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := handle.InvokeWithResponse(ctx, "request", func(value string) (string, error) {
			close(processing)
			<-release
			return value, nil
		})
		returned <- err
	}()
	awaitSignal(t, processing)
	cancel()
	if err := awaitError(t, returned); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	metrics := collectLifetime(t, reader)
	assertMetric(t, metrics[callerDuration], 1, "example.response/v1", "runtime_error", "cancelled", "result_unknown", false)
	if len(metrics[targetDuration]) != 0 || dispatcher.ActiveAttempts() != 1 {
		t.Fatal("response processor prematurely terminated")
	}
	once.Do(func() { close(release) })
	drainDispatcher(t, dispatcher)
	assertMetric(t, collectLifetime(t, reader)[targetDuration], 1, "example.response/v1", "success", "", "result_known", true)
}

func TestLifetimeMetricsPreEntryFailuresDoNotInventTargets(t *testing.T) {
	provider, reader := metricProvider(t)
	handle, dispatcher := metricRuntime(t, provider, "example.rejected/v1", publicPolicy(0, 1), func(context.Context, string) (string, error) {
		t.Fatal("rejected call entered target")
		return "", nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = handle.Invoke(ctx, "private")
	_, _ = handle.InvokeWithPreparation(context.Background(), "private", func(string) (string, error) {
		return "", policyError(invocation.ErrorInvalidArgument)
	}, func(value string) (string, error) { return value, nil })
	drainDispatcher(t, dispatcher)
	_, _ = handle.Invoke(context.Background(), "private")
	metrics := collectLifetime(t, reader)
	for _, code := range []string{"cancelled", "invalid_argument", "unavailable"} {
		assertMetric(t, metrics[callerDuration], 1, "example.rejected/v1", "runtime_error", code, "not_started", false)
	}
	if len(metrics[targetDuration]) != 0 {
		t.Fatal("non-entry emitted target telemetry")
	}
}

func TestLifetimeMetricsAdmissionRejectionDoesNotCountRetryAsTarget(t *testing.T) {
	provider, reader := metricProvider(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	handle, dispatcher := metricRuntime(t, provider, "example.capacity/v1", retryPolicy(), func(context.Context, string) (string, error) {
		close(entered)
		<-release
		return "ok", nil
	})
	returned := make(chan error, 1)
	go func() { _, err := handle.Invoke(context.Background(), "first"); returned <- err }()
	awaitSignal(t, entered)
	_, err := handle.Invoke(context.Background(), "rejected")
	var boundary *invocation.Error
	if !errors.As(err, &boundary) || boundary.Attempts() != 3 || boundary.Completion() != invocation.CompletionNotStarted {
		t.Fatal(err)
	}
	metrics := collectLifetime(t, reader)
	assertMetric(t, metrics[callerDuration], 1, "example.capacity/v1", "runtime_error", "resource_exhausted", "not_started", false)
	if len(metrics[targetDuration]) != 0 || dispatcher.ActiveAttempts() != 1 {
		t.Fatal("admission rejection invented a target termination")
	}
	once.Do(func() { close(release) })
	if err := awaitError(t, returned); err != nil {
		t.Fatal(err)
	}
	metrics = collectLifetime(t, reader)
	assertMetric(t, metrics[callerDuration], 1, "example.capacity/v1", "success", "", "result_known", false)
	assertMetric(t, metrics[targetDuration], 1, "example.capacity/v1", "success", "", "result_known", false)
}

func TestLifetimeMetricsGlobalProviderCanBeInstalledAfterDispatcher(t *testing.T) {
	const marker = "PLYSTRA_TEST_GLOBAL_METRICS"
	if os.Getenv(marker) == "1" {
		handle, _ := metricRuntime(t, nil, "example.global/v1", publicPolicy(0, 1), func(context.Context, string) (string, error) { return "ok", nil })
		provider, reader := metricProvider(t)
		otel.SetMeterProvider(provider)
		if _, err := handle.Invoke(context.Background(), "request"); err != nil {
			t.Fatal(err)
		}
		metrics := collectLifetime(t, reader)
		assertMetric(t, metrics[callerDuration], 1, "example.global/v1", "success", "", "result_known", false)
		assertMetric(t, metrics[targetDuration], 1, "example.global/v1", "success", "", "result_known", false)
		return
	}
	// The global delegating provider is intentionally one-shot. Test installation
	// in a separate process so no other test inherits a shutdown provider.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLifetimeMetricsGlobalProviderCanBeInstalledAfterDispatcher$", "-test.count=1")
	command.Env = append(os.Environ(), marker+"=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("global provider: %v\n%s", err, output)
	}
}

func TestLifetimeMetricsRetryAndNestedCallsHaveIndependentCounts(t *testing.T) {
	provider, reader := metricProvider(t)
	var calls atomic.Int32
	inner, _ := metricRuntime(t, provider, "example.inner/v1", retryPolicy(), func(context.Context, string) (string, error) {
		if calls.Add(1) < 3 {
			return "", policyError(invocation.ErrorUnavailable)
		}
		return "ok", nil
	})
	outer, _ := metricRuntime(t, provider, "example.outer/v1", retryPolicy(), inner.Invoke)
	if value, err := outer.Invoke(context.Background(), "private"); value != "ok" || err != nil || calls.Load() != 3 {
		t.Fatalf("retry result: %q %v %d", value, err, calls.Load())
	}
	metrics := collectLifetime(t, reader)
	assertMetric(t, metrics[callerDuration], 1, "example.outer/v1", "success", "", "result_known", false)
	assertMetric(t, metrics[callerDuration], 2, "example.inner/v1", "runtime_error", "unavailable", "result_known", false)
	assertMetric(t, metrics[callerDuration], 1, "example.inner/v1", "success", "", "result_known", false)
	for _, id := range []string{"example.inner/v1", "example.outer/v1"} {
		assertMetric(t, metrics[targetDuration], 2, id, "runtime_error", "unavailable", "result_known", false)
		assertMetric(t, metrics[targetDuration], 1, id, "success", "", "result_known", false)
	}
}

func TestLifetimeMetricsSemanticUncertaintyAndConcurrentCollection(t *testing.T) {
	provider, reader := metricProvider(t)
	otherProvider, otherReader := metricProvider(t)
	handle, _ := metricRuntime(t, provider, "example.semantic/v1", publicPolicy(0, 64), func(context.Context, string) (string, error) {
		return "", invocation.NewSemanticError("not_ready", invocation.NewResultUnknown(errors.New("private cause")))
	})
	_, _ = metricRuntime(t, otherProvider, "example.dormant/v1", publicPolicy(0, 1), func(context.Context, string) (string, error) { return "", nil })
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			if _, err := handle.Invoke(context.Background(), "private request"); invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
				t.Error(err)
			}
		})
	}
	group.Go(func() {
		for range 10 {
			collectLifetime(t, reader)
		}
	})
	group.Wait()
	metrics := collectLifetime(t, reader)
	for _, name := range []string{callerDuration, targetDuration} {
		assertMetric(t, metrics[name], 32, "example.semantic/v1", "semantic_error", "not_ready", "result_unknown", false)
	}
	for _, points := range collectLifetime(t, otherReader) {
		if len(points) != 0 {
			t.Fatal("dormant or unrelated dispatcher emitted telemetry")
		}
	}
}

type faultyMetricProvider struct {
	metric.MeterProvider
	failure string
}

func (p faultyMetricProvider) Meter(name string, options ...metric.MeterOption) metric.Meter {
	if p.failure == "provider" {
		panic("private provider")
	}
	return faultyMeter{Meter: p.MeterProvider.Meter(name, options...), failure: p.failure}
}

type faultyMeter struct {
	metric.Meter
	failure string
}

func (m faultyMeter) Float64Histogram(name string, options ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	if m.failure == name {
		return nil, errors.New("private instrument")
	}
	instrument, err := m.Meter.Float64Histogram(name, options...)
	return faultyHistogram{Float64Histogram: instrument, failure: m.failure}, err
}

type faultyHistogram struct {
	metric.Float64Histogram
	failure string
}

func (h faultyHistogram) Enabled(context.Context) bool {
	if h.failure == "enabled" {
		panic("private enabled")
	}
	return true
}

func (h faultyHistogram) Record(context.Context, float64, ...metric.RecordOption) {
	panic("private record")
}

func TestLifetimeMetricsFailuresCannotReplaceResultsOrRetainAttempts(t *testing.T) {
	for _, failure := range []string{"provider", callerDuration, targetDuration, "enabled", "record"} {
		t.Run(failure, func(t *testing.T) {
			provider := faultyMetricProvider{MeterProvider: noop.NewMeterProvider(), failure: failure}
			if failure != "enabled" && failure != "record" {
				dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion, MeterProvider: provider})
				if dispatcher != nil || !errors.Is(err, invocation.ErrInvalidTelemetry) || strings.Contains(fmt.Sprint(err), "private") {
					t.Fatalf("provider failure: %v %v", dispatcher, err)
				}
				return
			}
			handle, dispatcher := metricRuntime(t, provider, "example.failure/v1", publicPolicy(0, 1), func(context.Context, string) (string, error) { return "ok", nil })
			if value, err := handle.Invoke(context.Background(), "private"); value != "ok" || err != nil || dispatcher.ActiveAttempts() != 0 {
				t.Fatalf("telemetry changed invocation: %q %v", value, err)
			}
			drainDispatcher(t, dispatcher)
		})
	}
}

func BenchmarkInvocationLifetimeMetrics(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		b.Run(fmt.Sprintf("enabled=%t", enabled), func(b *testing.B) {
			var provider metric.MeterProvider = noop.NewMeterProvider()
			if enabled {
				provider, _ = metricProvider(b)
			}
			handle, _ := metricRuntime(b, provider, "example.benchmark/v1", publicPolicy(0, 64), func(context.Context, string) (string, error) { return "ok", nil })
			b.ReportAllocs()
			for b.Loop() {
				if _, err := handle.Invoke(context.Background(), "request"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
