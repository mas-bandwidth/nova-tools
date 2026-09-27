# AGENTS.md — generated map of internal/ghevent/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../../AGENTS.md). Rules: [CONTRIBUTING.md](../../docs/CONTRIBUTING.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `testdata/` | GitHub delivery fixtures for the decoder | `go test ./internal/ghevent` | `go test ./internal/ghevent` |
| `wire/` | shared GitHub event stream identity without ingestion dependencies | `go test ./internal/ci` | `go test ./internal/gh ./internal/wake` |
