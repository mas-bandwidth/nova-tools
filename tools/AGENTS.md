# AGENTS.md — generated map of tools/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [CONTRIBUTING.md](../docs/CONTRIBUTING.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `agentsmap/` | AGENTS.md map generator CLI | `go test ./internal/docs` | `make map` |
| `analyzers/` | vetlaw verb-law analyzers | `go test ./tools/analyzers/...` | `make vet-laws` |
| `ci/` | CI helper and build scripts | `go test ./internal/ci` | `make test` |
| `newrule/` | class rule scaffolding CLI | `go test ./tools/newrule` | `go test ./tools/newrule` |
| `newverb/` | CLI verb scaffolding CLI | `go test ./tools/newverb` | `go test ./tools/newverb` |
| `testdur/` | test duration analyzer and budget checker | `go test ./tools/testdur` | `go test ./tools/testdur` |
| `testmanifest/` | exact named Go test manifest checker | `go test ./tools/testmanifest` | `go test ./tools/testmanifest` |
