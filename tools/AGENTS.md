# AGENTS.md — generated map of tools/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [STANDARD.md](../docs/STANDARD.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `agentsmap/` | AGENTS.md map generator CLI | `go test ./internal/docs` | `make map` |
| `analyzers/` | vetlaw verb-law analyzers | `go test ./tools/analyzers/...` | `make vet-laws` |
| `ci/` | the verbs CI and the Makefile call: package selection and the shard deal, the test-step checks, the ancestry fetch, gofmt, the redis, postgres and sbcl installs, the lisp tier, the job aggregates, revert-on-red and the run reports; one runner for every process | `go test ./tools/ci` | `go run ./tools/ci help` |
| `fardelay/` | a store at a distance as a process: a loopback proxy that holds each write of its clients back by a fixed delay | `go test ./tools/fardelay` | `go run ./tools/fardelay --target HOST:PORT --delay 64ms` |
| `functionalrun/` | the functional tier inside one container per run, and the reaper of its overdue containers | `go test ./tools/functionalrun` | `make test-functional-container` |
| `newrule/` | class rule scaffolding CLI | `go test ./tools/newrule` | `go test ./tools/newrule` |
| `newverb/` | CLI verb scaffolding CLI | `go test ./tools/newverb` | `go test ./tools/newverb` |
| `sessiontrace/` | bounded shell trace replay against TableSession | `go test ./tools/sessiontrace` | `go test ./tools/sessiontrace` |
| `sprintsize/` | nova-sprint's size run: the sprint at 10x its largest real size on a local store, each operation timed against its limit | `go vet ./tools/sprintsize` | `go run ./tools/sprintsize --bin <nova-sprint>` |
| `testmanifest/` | exact named Go test manifest checker | `go test ./tools/testmanifest` | `go test ./tools/testmanifest` |
| `tlacheck/` | TLA+ model check CLI: run the declared cases, the table and member suites, the receipt replay and the finding witnesses | `go test ./tools/tlacheck` | `go test ./tools/tlacheck` |
