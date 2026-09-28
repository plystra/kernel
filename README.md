# Plystra Kernel

`github.com/plystra/kernel` is the runtime half of Plystra Core. It exposes stable Go APIs used by plugins and by application source generated through the Plystra CLI.

Plystra Core is exactly:

```text
Kernel + CLI
```

The Kernel is intrinsically complete. It does not depend on the CLI at runtime and never consumes an ordinary plugin Capability or service to implement a Kernel function.

## Runtime responsibilities

The Kernel owns:

- One immutable already-resolved provider registry.
- Exact typed in-process Capability lookup and dispatch.
- Provider lifecycle and graceful startup and shutdown.
- Validated typed runtime configuration injection.
- Ordinary Go `context.Context`, deadlines, cancellation, trace data, and bounded opaque metadata propagation.
- Panic recovery and safe typed error normalization.
- Runtime concurrency, timeout, queue, buffering, and memory limits.
- Intrinsic logs, metrics, traces, health, and implementation diagnostics.
- Reserved intrinsic `kernel.*` Capabilities.
- Stable plugin-facing and generated-assembly APIs.

The Kernel does not:

- Own a User, account, Person, profile, Member, Space, credential, session, token, permission, or policy model.
- Define an application-wide identity abstraction.
- Authenticate application callers or authorize business operations.
- Call AuthN, AuthZ, Audit, or another plugin before or after target dispatch.
- Depend on a plugin for logging, health, security, storage, transport, lifecycle, tracing, metrics, configuration, or another intrinsic feature.
- Scan Go Modules, choose providers, evaluate plugin build-time rules, or generate application code at runtime.

Applications without AuthN, AuthZ, a User model, or any ordinary plugin remain valid.

## Dispatch boundary

The CLI resolves providers and generates the supported invocation path before build:

```text
generated HTTP adapter, SDK adapter, or Capability client
-> generated application preparation and checks
-> Kernel exact dispatch
-> selected in-process provider
```

The Kernel receives one complete immutable registry. It distinguishes exact Capability versions, performs no runtime provider selection, and never starts with silently missing application requirements.

Go plugins share one process and are not sandboxed. Generated Capability clients are the supported application API; raw dispatch remains a low-level Kernel boundary rather than an encouraged bypass for ordinary plugin code.

Typed contracts declare their exact semantic error codes. Implementations return
`invocation.NewSemanticError(code, cause)`, directly or through ordinary `%w`
wrapping. `Code()` exposes the code and `Unwrap()` retains the optional cause
for local `errors.Is` and `errors.As` traversal. Formatting and structured
logging never expose the cause. The superseded structural
`capability.SemanticError` interface is not accepted.

The endpoint examines both single and joined unwrap edges, with at most 64
unwrap levels and 1,024 visited nodes. Shared subtrees are permitted within the
node budget; cycles, panicking unwrap methods, exceeded bounds, invalid or
undeclared codes, distinct semantic codes, and conflicting primary runtime
categories fail closed as `internal`. Custom `As` and `Is` methods are not
executed. Ordinary wrapping and joined errors remain recognizable. The returned
semantic carrier contains only the declared code and completion classification;
private causes, wrapper messages, and concrete implementation types are removed.

### Completion classification

`invocation.CompletionOf(err)` reads the separate closed completion vocabulary:
`not_started`, `result_known`, or `result_unknown`. A nil error is a known
success. `Error.Completion()` and `SemanticError.Completion()` expose the same
classification on safe runtime and semantic outcomes. Pre-dispatch rejection
is `not_started`; an executed target cannot inherit that assertion from a
nested call. A target's ordinary returned safe error is `result_known`.
Cancellation that suppresses a dispatched target's result is `result_unknown`,
not a claim that business effects were rolled back.

Use `invocation.NewResultUnknown(cause)` for an uncertain result, including
commit-acknowledgement loss. It retains an optional private cause locally and
can wrap, or be wrapped by, a semantic error. Uncertainty survives ordinary
wrapping, joins, semantic translation, and endpoint redaction without replacing
the primary runtime category or declared semantic code. The old
`ErrorResultUnknown` primary category is removed. A deadline can therefore
remain a deadline while also reporting `result_unknown`. Incomplete error-tree
traversal conservatively returns internal failure with `result_unknown`; the
public completion helper also treats unclassified or invalid errors as unknown.

This Kernel API does not establish generated transport or SDK completion
projection. Caller completion independent of an uncooperative target, actual
attempt tracking, admission-permit retention, and shutdown drain remain separate
runtime work; dispatch is currently synchronous.

## Implementation lifecycle

Implementations that own resources requiring explicit startup and shutdown implement `lifecycle.Instance`. Constructors only assemble values and store configuration and dependencies; resource acquisition and background work belong in `Start`. Generated assembly supplies constructed lifecycle values in dependency order through `lifecycle.NewBinding` and `lifecycle.NewManager`; the Kernel does not discover Implementations or recompute dependencies. Values without lifecycle work need no lifecycle methods.

The lifecycle manager owns cleanup of every supplied constructed value from creation, including before `Start`. It starts values in generated order and stops them in reverse order. Startup cancellation, error, or panic performs bounded rollback of all constructed values, including the failing value and values whose `Start` was never entered. Rollback preserves context values but uses a fresh timeout independent of startup cancellation. `Stop` must tolerate never-started and partially started values. Cleanup attempts every pending value, skips successful stops on later calls, and leaves failed stops retryable with a fresh context. A `Stop` hook returning nil confirms cleanup even if cancellation arrived during that hook; later hooks remain pending when cancellation prevents entry. Errors and panics expose only safe constructor symbols and standard context cancellation or deadline causes, never Implementation error text.

## Context and configuration

The Kernel propagates ordinary Go context, cancellation, deadlines, trace correlation, and bounded opaque metadata without interpreting application identity. Generated code may carry typed AuthN-owned or AuthZ-owned values, but those remain application data.

`contextmetadata.WithBytes(ctx, key, value)` derives a context containing an
immutable copy of one opaque payload. `contextmetadata.Bytes(ctx, key)` returns
a fresh copy, or `nil, false, nil` for a missing key. Keys use the exact
Interface-ID lexical grammar, such as `example.request-hint/v1`, and contain
at most 128 ASCII bytes. A context lineage holds at most 32 keys, each payload
contains 1 through 262,144 bytes, and combined key plus payload storage is at
most 524,288 bytes. Invalid contexts, keys, and bounds return
`contextmetadata.ErrInvalidMetadata`; duplicate keys return
`contextmetadata.ErrMetadataConflict` without replacing a value. Insertion
never mutates the input context, and sibling contexts remain independent.

Governed calls preserve this container through ordinary context propagation.
The Kernel does not decode, log, serialize, or interpret the bytes as identity,
credentials, authority, or configuration. Each key's owner defines and checks
its payload schema. Metadata is process-local and remains outside Interface
messages and transport projections; target code must not retain the context or
payload after its attempt terminates.

Runtime configuration is validated and resolved through intrinsic Kernel facilities, then injected only into the owning plugin as typed data. One selected Plugin ID has one configuration object. Secret values never enter generated source, manifests, logs, traces, diagnostics, errors, or intrinsic Capability responses.

The `configuration` package is the runtime boundary used by generated typed adapters and bootstrap code. Its generic document loader accepts only a bounded regular file, rejects a symbolic final path, and detects file replacement or observable modification while reading without exposing paths or private values. Generic immutable object and string-map extractors let generated code select its own application sections without teaching the Kernel their names or meaning. Its per-plugin decoder rejects unknown, missing, malformed, excessively deep, and oversized values; applies declared defaults and enums; converts every supported scalar, object, and array type; and resolves only fields declared as Secrets. The matching non-resolving validation path checks the exact final value contract, while partial normalization validates only explicitly present fields and returns redacted immutable field data plus semantic digests for CLI-owned typed composition; neither path reads environment variables or files. Secret references accept only validated portable environment-variable names or clean absolute POSIX and Windows regular-file targets within a mandatory byte bound. Resolved values are immutable and defensive, while configuration objects, setting maps, references, Secrets, and partial values are redacted for every formatting and structured-logging path and reject JSON, text, and YAML serialization. Errors retain only declared field names, safe failure classes, and standard cancellation or deadline causes.

## Intrinsic Interfaces

Reserved identities include:

```text
kernel.health/v1
kernel.info/v1
```

They are implemented directly by the Kernel, require no ordinary Implementation or `interfaces.use` entry, cannot be overridden by application code, and remain available regardless of the selected application package set. HTTP exposure is still explicit, and responses redact private configuration, Secrets, sensitive paths, and unsafe build details.

The canonical authored packages are `interfaces/kernel/health/v1` and `interfaces/kernel/info/v1`. Each defines one versioned Go Interface plus its request and response types. The `intrinsic` package publishes their deterministic reserved inventory and constructs both executable Kernel bindings in canonical ID order. Assembly supplies only validated Kernel Go Module provenance; the bindings use intrinsic selection, carry no application Implementation identity, and can form a complete catalog with no ordinary bindings. `kernel.health/v1` returns only `healthy`. `kernel.info/v1` returns assembly API `v1`, `github.com/plystra/kernel`, and the canonical Kernel module version, or the fixed `devel` marker for an unversioned build; build identities and other private build details are not returned.

## Telemetry and audit

The Kernel emits its own bounded runtime logs, metrics, traces, health state, and implementation diagnostics. It never calls `audit.write/v1`.

CLI-generated application invocation audit and explicit business audit events are separate application concerns. Removing an Audit plugin can remove its generated application behavior but cannot change Kernel telemetry or dispatch.

## Capability and plugin contracts

Provider-independent identities use the exact form `<capability-name>/v<number>`, such as `email.send/v1` or `authn.login.oidc.complete/v1`. A capability name has at least two dot-separated segments; each starts with a lower-case letter and continues with lower-case letters, digits, or hyphens. Segment count expresses logical hierarchy and never implies a fixed namespace/operation split. Callers never encode a Plugin ID or Go Module in a Capability identity.

Capability contracts, typed operation semantics, closed field constraints, semantic errors, canonical schema digests, Plugin IDs, strict plugin manifests, and typed configuration declarations use stable Kernel packages consumed by plugins and the CLI. Every canonical Capability declaration carries one closed `semantics` object describing its kind, effects, idempotency, retry safety, cancellation, completion, ordering, and request/response data classification. String, integer, number, and array fields may carry only their type-specific bounded constraint vocabulary; the Kernel validates and exposes immutable normalized constraints, and includes them in exact contract equality and `SchemaDigest`. The Kernel validates the operation-specific consistency rules and exact request-field references, then exposes the immutable normalized values to the CLI; Providers cannot override them. The Kernel's `capability/catalog` package contains only its intrinsic reserved definitions; official AuthN, AuthZ, and business contracts remain owned by their corresponding modules. Capability declarations may carry lower-kebab namespaced JSON-compatible build-time metadata. The Kernel parses and preserves that metadata as immutable, canonically ordered contract data without interpreting namespace semantics. Normalized constraints, extension metadata, and typed semantics participate in canonical semantic equality and `SchemaDigest`; omitting or changing any of them makes otherwise identical provider contracts incompatible. Exact official versions are immutable, and providers of the same identity must use semantically identical contracts.

There is no separately distributed Go Plugin SDK. Plugins and generated source depend directly on the stable Kernel API.

## Assembly compatibility

Generated assembly declares the exact Kernel assembly API version it targets. The Kernel rejects incompatible assembly metadata explicitly rather than interpreting it under a different contract.

## Development

Run validation from the Kernel module root. Set `GOWORK=off` once for the
PowerShell session so every command proves that the module is independent of a
workspace file:

```powershell
$env:GOWORK = "off"

go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go mod verify
```

Go runs one fuzz target per command. The following PowerShell discovers every
committed `Fuzz...` function, derives its package, and runs each target with a
10-second local bound:

```powershell
$fuzzTargets = Get-ChildItem -Recurse -File -Filter "*_test.go" |
    Select-String -Pattern '^func (Fuzz[A-Za-z0-9_]+)\(f \*testing\.F\)' |
    ForEach-Object {
        $relativePackage = [IO.Path]::GetRelativePath(
            (Get-Location).Path,
            (Split-Path -Parent $_.Path)
        ).Replace('\', '/')

        [pscustomobject]@{
            Package = if ($relativePackage -eq '.') { '.' } else { "./$relativePackage" }
            Target  = $_.Matches[0].Groups[1].Value
        }
    } |
    Sort-Object Package, Target -Unique

if (-not $fuzzTargets) {
    throw "No fuzz targets found."
}

foreach ($fuzz in $fuzzTargets) {
    go test $fuzz.Package -run '^$' -fuzz ('^{0}$' -f $fuzz.Target) -fuzztime 10s
    if ($LASTEXITCODE -ne 0) {
        throw "Fuzz target failed: $($fuzz.Package) $($fuzz.Target)"
    }
}
```

The 10-second duration is a reproducible bounded local validation choice, not
a universal sufficiency threshold. Increase `-fuzztime` for longer campaigns.

Run the committed intrinsic Kernel and error-boundary benchmarks separately so their
allocation and timing results remain attributable:

```powershell
go test ./invocation -run '^$' -bench '^BenchmarkCapabilityLookup$' -benchmem -count=5
go test ./invocation -run '^$' -bench '^BenchmarkRegistryConcurrentRead$' -benchmem -count=5
go test ./invocation -run '^$' -bench '^BenchmarkKernelCanonicalDispatch$' -benchmem -count=5
go test ./invocation -run '^$' -bench '^BenchmarkInvocationErrorBoundary$' -benchmem -count=5
```

Benchmark results depend on the machine and Go toolchain. Record that context
with local evidence and compare applicable results with the binding Kernel
runtime performance requirements; do not treat one machine's numbers as
universal expectations.
