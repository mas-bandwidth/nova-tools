# AGENTS.md — generated map of docs/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [STANDARD.md](STANDARD.md).

The session contract (docs/SPEC-FRIEND.md, "The session contract", internal/friend/session_contract.go): the daemon tells a session friend everything the machine expects of her, the moment it changes — the wake file and the exact monitor line, the pong line, the start stamp (`nova-sprint progress --as friend.<me> <card>@<gen> --epoch <n>`) and the finish form. It is carried by every session check until the session answers one, pushed as one message titled `your contract` on the daemon's start, on a reinstall and whenever the wake path, the server or the epoch changes (the first epoch included), written to `<state-dir>/CONTRACT.md`, and printed by `nova-friend contract --as <me>`. A check deferred because the session runs no monitor over its wake file is pushed as a message (`answer <nonce>: <pong line>`) that still carries that contract; three in a row unanswered put the friend down with the reason `session runs no monitor over <file>`. The daemon stamps a start wherever it can see one: a lane it starts, and a held work card whose job directory gains a worktree or a branch push.

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `acceptance/` | release acceptance records: one measured requirement per file, with the raw numbers | `go test ./internal/ci` | `go test ./internal/ci -run TestAcceptanceRecordsAreWellFormed` |
| `audit/` | cold audit records of the codebase, one directory per auditor and date: numbered issues with file:line, evidence, a grade and a fix | `go test ./internal/docs` | `go test ./internal/docs` |
| `dogfood/` | dated dogfood records of the tools, one file per tool and run: every verb used cold, every finding graded | `go test ./internal/docs` | `go test ./internal/docs` |
| `fixtures/` | doc examples and test fixtures | `go test ./internal/docs` | `go test ./internal/docs` |
| `nova-config/` | nova-config guide: the permanent configuration and its apply into Redis | `go test ./internal/docs` | `go test ./internal/docs` |
| `nova-table/` | nova-table guide: the design statement, the keys, the verbs, the render rules | `go test ./internal/docs` | `go test ./internal/docs` |
| `ratings/` | cold ratings of the tools, one file per rater and tool | `go test ./internal/docs` | `go test ./internal/docs` |
| `sprint/` | the nova-sprint 1.0.0 glossary, which moves to nova-sprint's docs/ at the split | `go test ./internal/docs` | `go test ./internal/docs -run TestRetiredWordsAppearOnlyInRecords` |
| `stranger/` | cold stranger runs of the tools, one file per run, every stumble with its proposed card | `go test ./internal/docs` | `go test ./internal/docs` |
