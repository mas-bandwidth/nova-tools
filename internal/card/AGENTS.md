# AGENTS.md — generated map of internal/card/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../../AGENTS.md). Rules: [CONTRIBUTING.md](../../docs/CONTRIBUTING.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `definition/` | card definitions: parse, validate, pin to committed blobs, canonical admission record | `go test ./internal/card/definition` | `go test ./internal/card/definition` |
| [request/](request/AGENTS.md) | the card layer's typed inputs and receipts: strict decoding, validation, the derived transition, canonical encoding and request hash | `go test ./internal/card/request` | `go test ./internal/card/request` |
