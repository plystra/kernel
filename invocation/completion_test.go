package invocation_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
)

func TestCompletionVocabularyAndLocalCarriers(t *testing.T) {
	t.Parallel()
	for _, value := range []invocation.Completion{
		invocation.CompletionNotStarted, invocation.CompletionResultKnown, invocation.CompletionResultUnknown,
	} {
		if !value.Valid() || value.String() != string(value) {
			t.Fatalf("invalid completion %q", value)
		}
	}
	for _, value := range []invocation.Completion{"", "known", "RESULT_UNKNOWN"} {
		if value.Valid() {
			t.Fatalf("accepted completion %q", value)
		}
	}
	cause := &privateError{}
	unknown := invocation.NewResultUnknown(cause)
	semantic := invocation.NewSemanticError("already_exists", unknown)
	wrapped := fmt.Errorf("private wrapper: %w", semantic)
	if !errors.Is(wrapped, cause) || semantic.Unwrap() != unknown || unknown.Unwrap() != cause {
		t.Fatal("private local cause chain lost")
	}
	var found *privateError
	var marker *invocation.ResultUnknownError
	if !errors.As(wrapped, &found) || found != cause || !errors.As(wrapped, &marker) || marker != unknown {
		t.Fatal("local errors.As recognition lost")
	}
	if semantic.Code() != "already_exists" || semantic.Completion() != invocation.CompletionResultUnknown ||
		unknown.Completion() != invocation.CompletionResultUnknown || invocation.CompletionOf(wrapped) != invocation.CompletionResultUnknown {
		t.Fatal("translation lost result uncertainty")
	}
	var nilSemantic *invocation.SemanticError
	var nilUnknown *invocation.ResultUnknownError
	var nilRuntime *invocation.Error
	for _, err := range []error{nilSemantic, nilUnknown, nilRuntime, &invocation.SemanticError{}, &invocation.Error{}, cause} {
		if invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
			t.Fatal("invalid or unclassified error asserted certainty")
		}
	}
	if nilSemantic.Unwrap() != nil || nilUnknown.Unwrap() != nil || nilSemantic.Code() != "" || nilRuntime.Completion() != "" {
		t.Fatal("nil receiver behavior is unsafe")
	}
	if invocation.CompletionOf(nil) != invocation.CompletionResultKnown ||
		invocation.NewSemanticError("already_exists", cause).Completion() != invocation.CompletionResultKnown {
		t.Fatal("known result misclassified")
	}
}

func ExampleNewResultUnknown() {
	uncertain := invocation.NewResultUnknown(errors.New("private acknowledgement failure"))
	translated := invocation.NewSemanticError("delivery_uncertain", uncertain)
	fmt.Println(translated.Code())
	fmt.Println(invocation.CompletionOf(translated))
	// Output:
	// delivery_uncertain
	// result_unknown
}

func ExampleNewNotStartedError() {
	err, _ := invocation.NewNotStartedError(invocation.ErrorInvalidArgument, "contract.request_invalid")
	fmt.Println(err.Code())
	fmt.Println(invocation.CompletionOf(err))
	// Output:
	// invalid_argument
	// not_started
}

func TestPublicNotStartedErrorConstructor(t *testing.T) {
	for _, code := range []invocation.ErrorCode{
		invocation.ErrorInvalidArgument, invocation.ErrorNotFound, invocation.ErrorConflict,
		invocation.ErrorDenied, invocation.ErrorUnauthenticated, invocation.ErrorUnavailable,
		invocation.ErrorTimeout, invocation.ErrorCancelled, invocation.ErrorInternal,
		invocation.ErrorVersionIncompatible,
		invocation.ErrorResourceExhausted,
	} {
		boundary, err := invocation.NewNotStartedError(code, "contract.request_invalid")
		if err != nil || boundary.Code() != code || boundary.DetailCode() != "contract.request_invalid" || boundary.Completion() != invocation.CompletionNotStarted {
			t.Fatalf("NewNotStartedError(%s) = %v, %v", code, boundary, err)
		}
		for _, wrapped := range []error{boundary, fmt.Errorf("private wrapper: %w", boundary), errors.Join(boundary, boundary)} {
			if invocation.CompletionOf(wrapped) != invocation.CompletionNotStarted {
				t.Fatalf("wrapped rejection lost non-entry: %v", wrapped)
			}
		}
		if errors.Is(boundary, context.Canceled) != (code == invocation.ErrorCancelled) || errors.Is(boundary, context.DeadlineExceeded) != (code == invocation.ErrorTimeout) {
			t.Fatalf("context identities changed: %v", boundary)
		}
		if invocation.CompletionOf(invocation.NewResultUnknown(boundary)) != invocation.CompletionResultUnknown {
			t.Fatal("rejection suppressed uncertainty")
		}
		known, err := invocation.NewError(code, "contract.request_invalid")
		if err != nil || known.Completion() != invocation.CompletionResultKnown {
			t.Fatal("known-result constructor changed")
		}
	}
	for _, test := range []struct {
		code   invocation.ErrorCode
		detail string
	}{
		{"", ""}, {"unknown", "private details"}, {invocation.ErrorDenied, ""},
		{invocation.ErrorInternal, "private details"}, {invocation.ErrorInternal, strings.Repeat("a", 129)},
	} {
		boundary, err := invocation.NewNotStartedError(test.code, test.detail)
		if boundary != nil || !errors.Is(err, invocation.ErrInvalidError) {
			t.Fatalf("invalid rejection = %v, %v", boundary, err)
		}
	}
}

func TestTargetCannotReturnNotStartedForItsEnteredOuterCall(t *testing.T) {
	rejection, err := invocation.NewNotStartedError(invocation.ErrorInvalidArgument, "contract.request_invalid")
	if err != nil {
		t.Fatal(err)
	}
	for _, uncertain := range []bool{false, true} {
		handle := publicErrorHandle(t, func(context.Context, error) (string, error) {
			var err error = rejection
			if uncertain {
				err = invocation.NewResultUnknown(err)
			}
			return "private partial response", fmt.Errorf("private target wrapper: %w", err)
		})
		var wg sync.WaitGroup
		for range 32 {
			wg.Go(func() {
				response, err := handle.Invoke(context.Background(), nil)
				var boundary *invocation.Error
				want := invocation.CompletionResultKnown
				if uncertain {
					want = invocation.CompletionResultUnknown
				}
				if response != "" || !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorInvalidArgument || invocation.CompletionOf(err) != want {
					t.Errorf("outer result = %q, %v (%s)", response, err, invocation.CompletionOf(err))
				}
				if strings.Contains(fmt.Sprint(err), "private") || rejection.Completion() != invocation.CompletionNotStarted {
					t.Error("normalization leaked a cause or mutated a shared rejection")
				}
			})
		}
		wg.Wait()
	}
}

func FuzzNewNotStartedError(f *testing.F) {
	f.Add("invalid_argument", "contract.request_invalid")
	f.Add("denied", "")
	f.Add("internal", "private details")
	f.Fuzz(func(t *testing.T, code, detail string) {
		known, knownErr := invocation.NewError(invocation.ErrorCode(code), detail)
		boundary, err := invocation.NewNotStartedError(invocation.ErrorCode(code), detail)
		if (err != nil) != (knownErr != nil) {
			t.Fatal("constructor validation differs")
		}
		if err != nil {
			if boundary != nil || !errors.Is(err, invocation.ErrInvalidError) {
				t.Fatal("invalid rejection is usable")
			}
			return
		}
		if boundary.Code() != known.Code() || boundary.DetailCode() != known.DetailCode() || boundary.Error() != known.Error() || invocation.CompletionOf(boundary) != invocation.CompletionNotStarted {
			t.Fatal("rejection identity or completion differs")
		}
	})
}

func TestErrorCarriersNeverFormatPrivateCauses(t *testing.T) {
	t.Parallel()
	unknown := invocation.NewResultUnknown(&privateError{})
	semantic := invocation.NewSemanticError("already_exists", unknown)
	invalid := invocation.NewSemanticError("private invalid code", &privateError{})
	for _, value := range []any{unknown, *unknown, semantic, *semantic, invalid, *invalid} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			assertPublicErrorText(t, fmt.Sprintf(format, value))
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		assertPublicErrorText(t, string(data))
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Info("outcome", "error", value)
		assertPublicErrorText(t, log.String())
	}
}

func TestPublicEndpointErrorTreeClassification(t *testing.T) {
	t.Parallel()
	known := invocation.CompletionResultKnown
	unknown := invocation.CompletionResultUnknown
	semantic := func(code string) *invocation.SemanticError { return invocation.NewSemanticError(code, nil) }
	runtimeError := mustRuntimeError(t, invocation.ErrorUnavailable, "runtime.unavailable")
	for _, test := range []struct {
		name       string
		err        error
		semantic   string
		runtime    invocation.ErrorCode
		completion invocation.Completion
	}{
		{"direct semantic", semantic("already_exists"), "already_exists", "", known},
		{"wrapped semantic", fmt.Errorf("private wrapper: %w", semantic("already_exists")), "already_exists", "", known},
		{"private cause", invocation.NewSemanticError("already_exists", &privateError{}), "already_exists", "", known},
		{"same joined codes", errors.Join(semantic("already_exists"), semantic("already_exists")), "already_exists", "", known},
		{"joined private cause", errors.Join(&privateError{}, semantic("already_exists")), "already_exists", "", known},
		{"unknown outside semantic", invocation.NewResultUnknown(semantic("already_exists")), "already_exists", "", unknown},
		{"unknown inside semantic", invocation.NewSemanticError("already_exists", invocation.NewResultUnknown(&privateError{})), "already_exists", "", unknown},
		{"joined unknown", errors.Join(semantic("already_exists"), invocation.NewResultUnknown(nil)), "already_exists", "", unknown},
		{"no semantic match", &privateError{}, "", invocation.ErrorInternal, known},
		{"invalid semantic", semantic("private invalid code"), "", invocation.ErrorInternal, known},
		{"undeclared semantic", semantic("undeclared"), "", invocation.ErrorInternal, known},
		{"distinct semantic codes", errors.Join(semantic("already_exists"), semantic("not_ready")), "", invocation.ErrorInternal, known},
		{"semantic then runtime", errors.Join(semantic("already_exists"), runtimeError), "", invocation.ErrorInternal, known},
		{"runtime then semantic", errors.Join(runtimeError, semantic("already_exists")), "", invocation.ErrorInternal, known},
		{"semantic cancellation", invocation.NewSemanticError("already_exists", context.Canceled), "", invocation.ErrorInternal, known},
		{"uncertain invalid translation", invocation.NewSemanticError("undeclared", invocation.NewResultUnknown(runtimeError)), "", invocation.ErrorInternal, unknown},
		{"unknown without cause", invocation.NewResultUnknown(nil), "", invocation.ErrorInternal, unknown},
		{"unknown private cause", invocation.NewResultUnknown(&privateError{}), "", invocation.ErrorInternal, unknown},
		{"unknown runtime cause", invocation.NewResultUnknown(runtimeError), "", invocation.ErrorUnavailable, unknown},
		{"unknown deadline cause", invocation.NewResultUnknown(context.DeadlineExceeded), "", invocation.ErrorTimeout, unknown},
		{"unknown cancellation cause", invocation.NewResultUnknown(context.Canceled), "", invocation.ErrorCancelled, unknown},
		{"conflicting runtime codes", errors.Join(runtimeError, context.Canceled), "", invocation.ErrorInternal, known},
		{"same runtime codes", errors.Join(runtimeError, runtimeError), "", invocation.ErrorUnavailable, known},
		{"custom recognition ignored", &recognitionTrap{}, "", invocation.ErrorInternal, known},
		{"ordinary unwrap with custom recognition", &recognitionTrap{cause: semantic("already_exists")}, "already_exists", "", known},
		{"legacy structural claim ignored", structuralSemanticClaim{}, "", invocation.ErrorInternal, known},
	} {
		t.Run(test.name, func(t *testing.T) {
			handle := publicErrorHandle(t, func(context.Context, error) (string, error) { return "private response", test.err })
			response, err := handle.Invoke(context.Background(), nil)
			if response != "" || err == nil {
				t.Fatalf("invalid result: %q, %v", response, err)
			}
			assertPublicErrorText(t, fmt.Sprintf("%#v", err))
			if invocation.CompletionOf(err) != test.completion {
				t.Fatalf("completion = %s, want %s", invocation.CompletionOf(err), test.completion)
			}
			var semantic *invocation.SemanticError
			var runtime *invocation.Error
			if test.semantic != "" {
				if !errors.As(err, &semantic) || semantic.Code() != test.semantic || semantic.Unwrap() != nil || semantic.Completion() != test.completion || errors.As(err, &runtime) {
					t.Fatalf("semantic outcome = %v", err)
				}
			} else if !errors.As(err, &runtime) || runtime.Code() != test.runtime || runtime.Completion() != test.completion || errors.As(err, &semantic) {
				t.Fatalf("runtime outcome = %v, want %s", err, test.runtime)
			}
			var private *privateError
			if errors.As(err, &private) {
				t.Fatal("endpoint retained private cause")
			}
		})
	}
}

func TestErrorTraversalBoundsAndCycles(t *testing.T) {
	t.Parallel()
	leaf := invocation.NewSemanticError("already_exists", nil)
	chain := func(levels int) error {
		var result error = leaf
		for range levels {
			result = &errorTree{children: []error{result}}
		}
		return result
	}
	wide := func(nodes int) error {
		children := make([]error, nodes-1)
		for index := range children {
			children[index] = leaf
		}
		return &errorTree{children: children}
	}
	cycle := &errorTree{}
	cycle.children = []error{cycle}
	indirect := &errorTree{}
	indirect.children = []error{leaf, &errorTree{children: []error{indirect}}}
	shared := &errorTree{children: []error{leaf}}
	for _, test := range []struct {
		name string
		err  error
		pass bool
	}{
		{"64 unwrap levels", chain(64), true},
		{"65 unwrap levels", chain(65), false},
		{"1024 visited nodes", wide(1024), true},
		{"1025 visited nodes", wide(1025), false},
		{"shared subtree", errors.Join(shared, shared), true},
		{"nil joined members", &errorTree{children: []error{nil, leaf, nil}}, true},
		{"self cycle", cycle, false},
		{"indirect cycle", indirect, false},
		{"noncomparable cycle", uncomparableCycle{[]int{1}}, false},
		{"panic during unwrap", panicUnwrap{}, false},
		{"unknown behind incomplete traversal", errors.Join(cycle, invocation.NewResultUnknown(nil)), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			handle := publicErrorHandle(t, func(context.Context, error) (string, error) { return "", test.err })
			_, err := handle.Invoke(context.Background(), nil)
			var semantic *invocation.SemanticError
			if test.pass {
				if !errors.As(err, &semantic) || semantic.Code() != "already_exists" || invocation.CompletionOf(test.err) != invocation.CompletionResultKnown {
					t.Fatalf("valid bounded tree rejected: %v", err)
				}
			} else {
				var boundary *invocation.Error
				if !errors.As(err, &boundary) || boundary.Code() != invocation.ErrorInternal ||
					invocation.CompletionOf(err) != invocation.CompletionResultUnknown || invocation.CompletionOf(test.err) != invocation.CompletionResultUnknown {
					t.Fatalf("incomplete traversal asserted certainty: %v", err)
				}
			}
		})
	}
}

func TestDispatchAssignsCompletionWithoutChangingPrimaryCategory(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	handle := publicErrorHandle(t, func(ctx context.Context, input error) (string, error) {
		calls.Add(1)
		if cancel, ok := ctx.Value(cancelKey{}).(context.CancelFunc); ok {
			cancel()
		}
		return "result", input
	})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, ctx := range []context.Context{cancelled, expired} {
		_, err := handle.Invoke(ctx, nil)
		if invocation.CompletionOf(err) != invocation.CompletionNotStarted || calls.Load() != 0 || !errors.Is(err, ctx.Err()) {
			t.Fatalf("pre-entry completion = %s, error %v", invocation.CompletionOf(err), err)
		}
	}
	_, notStarted := handle.Invoke(cancelled, nil)
	_, outer := handle.Invoke(context.Background(), notStarted)
	if invocation.CompletionOf(outer) != invocation.CompletionResultKnown || invocation.CompletionOf(notStarted) != invocation.CompletionNotStarted {
		t.Fatal("outer target inherited nested non-entry, or mutated the nested error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = context.WithValue(ctx, cancelKey{}, cancel)
	response, err := handle.Invoke(ctx, nil)
	if response != "" || invocation.CompletionOf(err) != invocation.CompletionResultUnknown || !errors.Is(err, context.Canceled) {
		t.Fatalf("discarded post-entry result = %q, %v (%s)", response, err, invocation.CompletionOf(err))
	}
	_, err = handle.Invoke(context.Background(), invocation.NewResultUnknown(context.Canceled))
	if invocation.CompletionOf(err) != invocation.CompletionResultUnknown || !errors.Is(err, context.Canceled) {
		t.Fatal("uncertain cancellation became a known failure")
	}
	response, err = handle.Invoke(context.Background(), nil)
	if response != "result" || err != nil || invocation.CompletionOf(err) != invocation.CompletionResultKnown {
		t.Fatal("success misclassified")
	}
}

func TestCompletionIsImmutableAcrossConcurrentCalls(t *testing.T) {
	t.Parallel()
	safe := mustRuntimeError(t, invocation.ErrorUnavailable, "runtime.unavailable")
	unknown := invocation.NewResultUnknown(safe)
	handle := publicErrorHandle(t, func(_ context.Context, input error) (string, error) { return "", input })
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			for range 20 {
				_, err := handle.Invoke(context.Background(), unknown)
				if invocation.CompletionOf(err) != invocation.CompletionResultUnknown || safe.Completion() != invocation.CompletionResultKnown {
					t.Error("completion changed on a shared carrier")
				}
			}
		})
	}
	group.Wait()
}

func TestAllPublicPreDispatchRejectionsAreNotStarted(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	handle := publicErrorHandle(t, func(context.Context, error) (string, error) {
		calls.Add(1)
		return "", nil
	})
	contract := capability.MustParseContract[error, string]("example.unpublished/v1")
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	unpublished, err := invocation.NewHandle(dispatcher, contract, true)
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := invocation.NewHandle(dispatcher, contract, false)
	if err != nil {
		t.Fatal(err)
	}
	var zero invocation.Handle[error, string]
	for _, test := range []struct {
		name   string
		handle invocation.Handle[error, string]
		ctx    context.Context
	}{
		{"zero handle", zero, context.Background()},
		{"nil context", handle, nil},
		{"unpublished catalog", unpublished, context.Background()},
		{"unavailable binding", unavailable, context.Background()},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.handle.Invoke(test.ctx, nil)
			if invocation.CompletionOf(err) != invocation.CompletionNotStarted || calls.Load() != 0 {
				t.Fatalf("non-entry outcome = %v (%s)", err, invocation.CompletionOf(err))
			}
		})
	}
}

func TestNormalizedUncertaintySurvivesNestedEndpoint(t *testing.T) {
	t.Parallel()
	inner := publicErrorHandle(t, func(context.Context, error) (string, error) {
		return "", invocation.NewSemanticError("already_exists", invocation.NewResultUnknown(nil))
	})
	outer := publicErrorHandle(t, func(ctx context.Context, _ error) (string, error) {
		_, err := inner.Invoke(ctx, nil)
		return "", fmt.Errorf("private translation: %w", invocation.NewSemanticError("already_exists", err))
	})
	_, err := outer.Invoke(context.Background(), nil)
	var semantic *invocation.SemanticError
	if !errors.As(err, &semantic) || semantic.Code() != "already_exists" || semantic.Unwrap() != nil ||
		invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
		t.Fatalf("nested invocation lost its uncertain semantic outcome: %v", err)
	}
}

func FuzzPublicErrorTreeBoundary(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3})
	f.Add([]byte{5, 5, 6})
	f.Add(bytes.Repeat([]byte{1}, 70))
	handle := publicErrorHandle(f, func(_ context.Context, input error) (string, error) { return "", input })
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1100 {
			return
		}
		var input error = invocation.NewSemanticError("already_exists", nil)
		for _, value := range data {
			switch value % 7 {
			case 0:
				input = invocation.NewResultUnknown(input)
			case 1:
				input = &errorTree{children: []error{input}}
			case 2:
				input = errors.Join(input, invocation.NewSemanticError("not_ready", nil))
			case 3:
				input = invocation.NewSemanticError("already_exists", input)
			case 4:
				input = errors.Join(input, context.Canceled)
			case 5:
				cycle := &errorTree{}
				cycle.children = []error{input, cycle}
				input = cycle
			case 6:
				input = &recognitionTrap{cause: input}
			}
		}
		_, err := handle.Invoke(context.Background(), input)
		assertPublicErrorText(t, fmt.Sprintf("%#v", err))
		if !invocation.CompletionOf(err).Valid() {
			t.Fatal("invalid completion")
		}
		if invocation.CompletionOf(input) == invocation.CompletionResultUnknown && invocation.CompletionOf(err) != invocation.CompletionResultUnknown {
			t.Fatal("boundary lost uncertainty")
		}
	})
}

func BenchmarkInvocationErrorBoundary(b *testing.B) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"semantic", invocation.NewSemanticError("already_exists", nil)},
		{"wrapped_unknown", invocation.NewSemanticError("already_exists", invocation.NewResultUnknown(&privateError{}))},
	} {
		b.Run(test.name, func(b *testing.B) {
			handle := publicErrorHandle(b, func(_ context.Context, input error) (string, error) { return "", input })
			b.ReportAllocs()
			for b.Loop() {
				if _, err := handle.Invoke(context.Background(), test.err); err == nil {
					b.Fatal("missing error")
				}
			}
		})
	}
}

type cancelKey struct{}
type privateError struct{}

func (*privateError) Error() string { panic("private cause must never be formatted") }

type recognitionTrap struct{ cause error }

func (*recognitionTrap) Error() string   { panic("private message") }
func (*recognitionTrap) As(any) bool     { panic("private As hook") }
func (*recognitionTrap) Is(error) bool   { panic("private Is hook") }
func (e *recognitionTrap) Unwrap() error { return e.cause }

type structuralSemanticClaim struct{}

func (structuralSemanticClaim) Error() string             { return "private message" }
func (structuralSemanticClaim) SemanticErrorCode() string { return "already_exists" }

type errorTree struct{ children []error }

func (*errorTree) Error() string     { panic("private error tree") }
func (e *errorTree) Unwrap() []error { return e.children }

type uncomparableCycle struct{ values []int }

func (uncomparableCycle) Error() string   { panic("private cyclic error") }
func (e uncomparableCycle) Unwrap() error { return e }

type panicUnwrap struct{}

func (panicUnwrap) Error() string { return "private error" }
func (panicUnwrap) Unwrap() error { panic("private unwrap panic") }

func assertPublicErrorText(t testing.TB, text string) {
	t.Helper()
	if strings.Contains(text, "private") || strings.Contains(text, "PANIC") {
		t.Fatalf("private cause or wrapper escaped: %s", text)
	}
}

func mustRuntimeError(t testing.TB, code invocation.ErrorCode, detail string) *invocation.Error {
	t.Helper()
	err, invalid := invocation.NewError(code, detail)
	if invalid != nil {
		t.Fatal(invalid)
	}
	return err
}

func publicErrorHandle(t testing.TB, handler capability.Handler[error, string]) invocation.Handle[error, string] {
	t.Helper()
	handle, _ := publicErrorRuntime(t, time.Second, handler)
	return handle
}

func publicErrorRuntime(t testing.TB, timeout time.Duration, handler capability.Handler[error, string]) (invocation.Handle[error, string], *invocation.Dispatcher) {
	t.Helper()
	return publicResponseRuntime(t, timeout, handler)
}

func publicResponseRuntime[Response any](t testing.TB, timeout time.Duration, handler capability.Handler[error, Response]) (invocation.Handle[error, Response], *invocation.Dispatcher) {
	t.Helper()
	contract := capability.MustParseContractWithSemanticErrors[error, Response]("example.error-boundary/v1", "already_exists", "not_ready")
	endpoint, err := invocation.NewEndpoint(contract, handler)
	if err != nil {
		t.Fatal(err)
	}
	build, err := invocation.NewModuleBuild("github.com/acme/errors", "v1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := invocation.NewBinding(invocation.BindingOptions{
		Kind: invocation.BindingKindImplementation, Constructor: "github.com/acme/errors/implementation.New",
		ModuleBuild: build, SelectionReason: invocation.SelectionReasonUniqueCompatible,
		ContractDigest: sha256.Sum256([]byte("error-boundary-contract")),
		Policy:         publicPolicy(timeout, 256),
	}, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := invocation.NewCatalog([]invocation.Binding{binding})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
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
	return handle, dispatcher
}
