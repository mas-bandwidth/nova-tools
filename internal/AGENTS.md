# AGENTS.md — generated map of internal/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [STANDARD.md](../docs/STANDARD.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `cairn/` | the cairn store: session records, entries, the index and receipts, nested and flat | `go test ./internal/cairn` | `go test ./internal/cairn` |
| `check/` | the record checks: attest, links, kernel, nocode, floors, corpus, spelling | `go test ./internal/check` | `go test ./internal/check` |
| `ci/` | class tests and CI budget invariants | `go test ./internal/ci` | `go test ./internal/ci` |
| `cireceipt/` | ci-ok's run receipt: one ev:github row of the workflow_run shape | `go test ./internal/cireceipt` | `go test -tags functional ./internal/cireceipt` |
| `converge/` | convergence state and progress math | `go test ./internal/converge` | `go test ./internal/converge` |
| `docs/` | documentation guards and map generator | `go test ./internal/docs` | `go test ./internal/docs` |
| `doctor/` | nova-doctor frame: check registry, Env, results, and one check file per dependency | `go test ./internal/doctor` | `go test ./internal/doctor` |
| `fuse/` | the fuse box: read and write the lockdown and quarantine state | `go test ./internal/fuse` | `go test ./internal/fuse` |
| [ghevent/](ghevent/AGENTS.md) | the GitHub event Redis stream: append and read | `go test ./internal/ghevent` | `go test ./internal/ghevent` |
| `memindex/` | memory vector and text index | `go test ./internal/memindex` | `go test ./internal/memindex` |
| `nogh/` | the refusing gh command installed first on child shell PATHs | `go test ./internal/nogh` | `go test ./internal/nogh` |
| `nsprint/` | the shared Redis store and login, its Functions, and the verb flags the living tools use | `go test ./internal/nsprint/...` | `go test ./internal/nsprint/...` |
| `record/` | decision and execution records | `go test ./internal/record` | `go test ./internal/record` |
| `roadmap/` | docs/roadmap.sexp read through its bounded s-expression reader (sexp/) into typed records, and the ROADMAP.md it renders | `go test ./internal/roadmap/...` | `make roadmap` |
| `scaffold/` | scaffolding engine for class rules and CLI verbs | `go test ./internal/scaffold` | `go test ./internal/scaffold` |
| `selftalk/` | agent self-talk journal stream | `go test ./internal/selftalk` | `go test ./internal/selftalk` |
| `shippedsmoke/` | the smoke test of a shipped nova-check binary, behind the shippedsmoke build tag, run by the certification workflow | `go test -tags shippedsmoke ./internal/shippedsmoke` | `NOVA_SHIPPED_BIN=<binary> go test -tags shippedsmoke -v ./internal/shippedsmoke` |
| `tablemodel/` | the table model's checks: the suites, the finding witnesses and the receipt replay against EpochMemberTable | `go test ./internal/tablemodel` | `go test -tags functional ./internal/tablemodel` |
| `testverbhelp/` | per-tool check that every verb answers -h at exit 0 and touches nothing | `go test ./internal/testverbhelp` | `go test ./internal/testverbhelp` |
| `textbody/` | shared line-oriented message body filtering | `go test ./internal/textbody` | `go test ./internal/textbody` |
| `tokens/` | token counter and budget tracker | `go test ./internal/tokens` | `go test ./internal/tokens` |
| `up/` | nova-up's steps: the registry, the plan and apply of each step over a fake-able machine | `go test ./internal/up` | `go test ./internal/up` |
| `update/` | binary updater and checksum verifier | `go test ./internal/update` | `go test ./internal/update` |
| `yield/` | CI over work: a copy, a local test run or a sprint card's native launch steps itself to nice 15 before it execs (nova-tools#4293) | `go test ./internal/yield` | `go test ./internal/yield` |
