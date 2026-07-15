# Plystra Kernel

`github.com/plystra/kernel` is the runtime half of Plystra Core. It exposes the stable Go APIs used by plugins and by source generated through the Plystra CLI.

The module is intentionally independent from the CLI and from official plugin modules. Authentication and authorization implementations belong in `github.com/plystra/authn` and `github.com/plystra/authz`, not in the Kernel.

## Assembly compatibility

Generated assembly source declares the assembly API version it targets. The Kernel validates that version through `assembly.RequireVersion` before accepting generated assembly metadata. The current contract is `assembly.V1`; incompatible versions fail explicitly instead of being interpreted by a different runtime contract.

## Development

```powershell
go test ./...
go test -race ./...
go vet ./...
```
