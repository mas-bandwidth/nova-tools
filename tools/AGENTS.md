# AGENTS.md — generated map of tools/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [STANDARD.md](../docs/STANDARD.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `agentsmap/` | AGENTS.md map generator CLI | `go test ./internal/docs` | `make map` |
| `analyzers/` | vetlaw verb-law analyzers | `go test ./tools/analyzers/...` | `make vet-laws` |
| `benchstandard/` | the Linux bench's acceptance witness: one DRIFT line per finding against the standard, STANDARD OK or STANDARD DRIFT, and --apply kills stray runner listeners and nothing else | `go test ./tools/benchstandard` | `go run ./tools/benchstandard` |
| `ci/` | CI helper and build scripts | `go test ./internal/ci` | `make test` |
| `fardelay/` | a store at a distance as a process: a loopback proxy that holds each write of its clients back by a fixed delay | `go test ./tools/fardelay` | `go run ./tools/fardelay --target HOST:PORT --delay 64ms` |
| `functionalrun/` | the functional tier inside one container per run, and the reaper of its overdue containers | `go test ./tools/functionalrun` | `make test-functional-container` |
| `newrule/` | class rule scaffolding CLI | `go test ./tools/newrule` | `go test ./tools/newrule` |
| `newverb/` | CLI verb scaffolding CLI | `go test ./tools/newverb` | `go test ./tools/newverb` |
| `preflight/` | the standard check before a card or a pull request: gofmt, go vet and the unit tests through make test-full | `go test ./tools/preflight` | `make preflight` |
| `sandboxcheck/` | the darwin profile check of nova-sandbox: fills the profile template for a scratch write set and runs the first second of a job inside the wall, each denial beside a control outside it | `go test ./tools/sandboxcheck` | `go run ./tools/sandboxcheck` |
| `sessiontrace/` | bounded shell trace replay against TableSession | `go test ./tools/sessiontrace` | `go test ./tools/sessiontrace` |
| `sprintsize/` | nova-sprint's size run: the sprint at 10x its largest real size on a local store, each operation timed against its limit | `go vet ./tools/sprintsize` | `go run ./tools/sprintsize --bin <nova-sprint>` |
| `testmanifest/` | exact named Go test manifest checker | `go test ./tools/testmanifest` | `go test ./tools/testmanifest` |
| `tlacheck/` | TLA+ model check CLI: run the declared cases, the table and member suites, the receipt replay and the finding witnesses | `go test ./tools/tlacheck` | `go test ./tools/tlacheck` |
