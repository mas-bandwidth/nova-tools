# AGENTS.md — generated map of internal/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [STANDARD.md](../docs/STANDARD.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `cairn/` | the cairn store: session records, entries, the index and receipts, nested and flat | `go test ./internal/cairn` | `go test ./internal/cairn` |
| `card/` | what a brief's writers hold every brief to before it leaves: PATHS computed from its START files, and the card checks | `go test ./internal/card` | `go test ./internal/card` |
| `cardgen/` | nova-card's planner: ledger rows, findings and help to cards with PATHS, waves and the brief, pure over text | `go test ./internal/cardgen` | `go test ./internal/cardgen` |
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
| `scaffold/` | scaffolding engine for class rules and CLI verbs | `go test ./internal/scaffold` | `go test ./internal/scaffold` |
| `selftalk/` | agent self-talk journal stream | `go test ./internal/selftalk` | `go test ./internal/selftalk` |
| `shippedsmoke/` | the smoke test of a shipped nova-check binary, behind the shippedsmoke build tag, run by the certification workflow | `go test -tags shippedsmoke ./internal/shippedsmoke` | `NOVA_SHIPPED_BIN=<binary> go test -tags shippedsmoke -v ./internal/shippedsmoke` |
| `sprint/` | the sprint table's pure core (lifecycle, steps, check, inbox) and its binding to the table layer (store) and its driver (play) | `go test ./internal/sprint/...` | `go test ./internal/sprint/...` |
| `sprintdash/` | the sprint dashboard (nova-sprint dashboard): the page embedded in the binary, a cached copy of where --json, and the check that holds the page equal to docs/SPEC-SPRINT-DASHBOARD.md | `go test ./internal/sprintdash` | `go test ./internal/sprintdash` |
| `tablemodel/` | the table model's checks: the suites, the finding witnesses and the receipt replay against EpochMemberTable | `go test ./internal/tablemodel` | `go test -tags functional ./internal/tablemodel` |
| `testverbhelp/` | per-tool check that every verb answers -h at exit 0 and touches nothing | `go test ./internal/testverbhelp` | `go test ./internal/testverbhelp` |
| `textbody/` | shared line-oriented message body filtering | `go test ./internal/textbody` | `go test ./internal/textbody` |
| `tokens/` | token counter and budget tracker | `go test ./internal/tokens` | `go test ./internal/tokens` |
| `up/` | nova-up's steps: the registry, the plan and apply of each step over a fake-able machine | `go test ./internal/up` | `go test ./internal/up` |
| `update/` | binary updater and checksum verifier | `go test ./internal/update` | `go test ./internal/update` |
| `workfile/` | nova-work tree file: the model, its canonical writer, strict reader and field-for-field diff | `go test ./internal/workfile` | `go test ./internal/workfile` |
| `workgh/` | nova-work read-only GitHub issue capture over GraphQL, every call counted | `go test ./internal/workgh` | `go test ./internal/workgh` |
| `worklang/` | bounded reader for nova-work's restricted s-expression tree file | `go test ./internal/worklang` | `go test ./internal/worklang` |
| `yield/` | CI over work: a copy, a local test run or a sprint card's native launch steps itself to nice 15 before it execs (nova-tools#4293) | `go test ./internal/yield` | `go test ./internal/yield` |
