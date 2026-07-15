# Plystra Kernel

`github.com/plystra/kernel` is the runtime half of Plystra Core. It exposes the stable Go APIs used by plugins and by source generated through the Plystra CLI.

The module is intentionally independent from the CLI and from official plugin modules. Authentication and authorization implementations belong in `github.com/plystra/authn` and `github.com/plystra/authz`, not in the Kernel. The Kernel owns no `User` model, account lifecycle, user persistence, user CRUD API, credentials, or user-specific attributes; applications without AuthN or a user system remain valid.

Provider-independent capability identities are parsed by `capability.ParseIdentifier`. Exact major versions are mandatory and canonical, for example `email.send/v1`; callers never encode a provider or Go Module in that identity.

Typed capability declarations use `capability.Contract[Request, Response]`. Each successful declaration has an opaque identity shared by its copies, preventing independently declared Go types from being wired together merely because their textual capability IDs match; `capability.Handler` defines the corresponding implementation function shape.

`invocation.NewEndpoint` binds one typed contract to its handler while keeping type erasure inside the Kernel. Endpoints reject independently declared contract definitions, preserve typed nil values and provider errors, and recover panics as `invocation.ErrProviderPanic` without exposing panic payloads.

`invocation.NewCatalog` accepts only validated, already-resolved endpoint bindings carrying their canonical schema digest and selected Kernel or Plugin provider identity. It performs no runtime provider selection, distinguishes exact capability versions, copies source state, and serves immutable lock-free lookups, including a valid empty catalog for zero-plugin applications.

An `invocation.Dispatcher` requires a positive default execution timeout and an explicit capability authorizer, then atomically publishes one complete copied catalog exactly once. Missing authorization policy fails construction rather than defaulting to allow. Before publication the Dispatcher is explicitly not ready; failed validation leaves it unpublished, concurrent publishers have one winner, and readers observe either no catalog or one complete immutable snapshot.

Runtime caller provenance uses immutable `audit.CallerIdentity` values. Kernel-owned callers have no fabricated Plugin ID, while plugin callers carry one exact canonical Plugin ID. Module build data, the caller Principal or security subject, optional authorization-boundary references, and transport metadata remain separate governed invocation facts.

`Dispatcher.Scope` binds one validated caller identity to exactly one Dispatcher without exposing an invocation surface. Scopes can be staged before catalog publication and remain opaque to plugin code, allowing generated assembly to create caller-bound typed handles only after their contracts and dependency resolutions are known.

`invocation.NewHandle` turns a scope, exact typed contract, and generated provider-availability result into an inert typed reference. A handle currently exposes only its capability identifier and availability; both generic type arguments are part of its representation, and no invocation method is exposed before context, authorization, audit, deadline, and safe-error governance are bound.

Capability failures use the closed `audit.ErrorCode` taxonomy and immutable `invocation.Error` boundary values. Only validated machine-readable classes and detail codes can cross the boundary; denials require an auditable reason, cancellation and timeout preserve their standard Go identities, and no provider cause or free-form message is stored.

Runtime audit uses distinct `audit.RequestID`, `audit.TraceID`, and `audit.InvocationID` types backed by canonical non-zero 128-bit lower-case hexadecimal values. Root IDs are generated with cryptographic randomness, while external representations must pass the same strict parsers before entering runtime context.

`audit.ModuleBuild` carries bounded immutable provider provenance using a canonical Go module path plus a canonical matching module version, a safe generated or VCS build identity, or both. Development modules without a version must still provide a build identity. This embedded observability fact never replaces `go.mod`, `go.sum`, or Go's dependency resolution.

The Kernel security-context target is a minimal domain-neutral `Principal` reference, not a full User object. A Principal may identify an anonymous caller, User, service, plugin, system process, device, or another extensible principal kind using only an opaque subject identifier, kind, and issuer where applicable. AuthN resolves credentials and authentication identities to that reference, AuthZ consumes it for authorization, and the Kernel neither interprets nor queries the User behind it. `Principal` is a runtime security-context type, not a fifth top-level public concept.

The current incremental API exposes the provider-neutral portion of that boundary as `audit.SubjectContext`, which carries bounded opaque subject and tenant or authorization-space references without defining a Kernel User domain. Anonymous access is represented by an explicitly constructed empty context; the zero value remains invalid so a missing ingress decision cannot silently become anonymous.

Protected dispatch may use minimal trusted AuthN and AuthZ hooks to establish a Principal and make authorization decisions before ordinary capability entry. Those hooks must not recursively traverse the same protected dispatch path they govern. Login, logout, token refresh, account management, policy administration, and business-facing permission checks remain ordinary versioned capabilities. The Kernel owns only hook contracts, sequencing, security-context propagation, failure handling, auditing, and governance; AuthN and AuthZ own the corresponding domain behavior.

`Scope.NewRootContext` lets only an explicit Kernel caller mint a root runtime frame with cryptographic request and trace IDs plus validated subject context. Existing or malformed frames, plugin callers, and missing inputs fail closed. The frame retains the original trusted deadline and cancellation authority even if ordinary Go cancellation is later detached.

Each governed provider entry derives an immutable child frame with a unique invocation ID and immediate parent ancestry. The effective deadline is the earliest configured, caller, or trusted-frame deadline, and trusted cancellation remains linked through detached Go contexts. Providers can inspect only the safe request, trace, ancestry, subject, tenant, and deadline snapshot through `invocation.Current`; root frames, cancellation authority, and mutable runtime state are not exposed.

Capability authorization receives an immutable Kernel-owned request containing caller provenance, the provider-independent capability identity, safe subject and tenant references, and audit correlation IDs. Policies must return an explicit valid allow or stable-code denial; a missing policy, zero or malformed decision, callback error, or panic fails closed without retaining policy implementation details. Selected provider identity is deliberately outside the authorization request so provider replacement cannot change caller permission.

Concrete implementation identities are parsed separately by `plugin.ParseID`, for example `acme.email.smtp`. A Plugin ID is never a capability identity and carries no independent version; the containing Go Module supplies distribution versioning.

Plugin configuration declarations use the strict DSL parsed by `plugin/manifest.ParseConfig`. Supported types are `string`, `integer`, `number`, `boolean`, `duration`, `url`, `secret`, `object`, and `array`; secret fields cannot contain defaults, and all generated defaults and enums have deterministic JSON forms.

The current `plugin.yaml` envelope is parsed by `plugin/manifest.ParsePlugin`. It requires a concrete `id` and accepts optional `provides`, `requires`, and `config` fields. Unknown fields, duplicate keys or capabilities, YAML references, multiple documents, and non-canonical identities are rejected.

Capability contracts use the strict `plugin/manifest.ParseCapability` parser. A `capability.yaml` contains an exact `id`, optional description, request and response field mappings, and semantic error codes. Contract fields support the JSON-oriented `string`, `integer`, `number`, `boolean`, `object`, and typed `array` forms used by Go and JavaScript generation.

`Capability.CanonicalSchemaJSON` removes source-only differences and `Capability.SchemaDigest` derives its stable SHA-256 fingerprint. Field, enum, and error order, YAML formatting, descriptions, and explicit false values do not change the wire schema; identities, types, required fields, and semantic errors do.

The immutable `capability/catalog` package distributes official definitions with the Kernel. `catalog.Lookup` and `catalog.Definitions` expose validated contracts, semantic schema digests, and defensive copies of canonical LF-only source suitable for CLI materialization; the initial catalog includes the documented `email.send/v1` contract.

## Assembly compatibility

Generated assembly source declares the assembly API version it targets. The Kernel validates that version through `assembly.RequireVersion` before accepting generated assembly metadata. The current contract is `assembly.V1`; incompatible versions fail explicitly instead of being interpreted by a different runtime contract.

## Development

```powershell
go test ./...
go test -race ./...
go vet ./...
```
