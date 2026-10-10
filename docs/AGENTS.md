# AGENTS.md — generated map of docs/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [STANDARD.md](STANDARD.md).

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `acceptance/` | release acceptance records: one measured requirement per file, with the raw numbers | `go test ./internal/ci` | `go test ./internal/ci -run TestAcceptanceRecordsAreWellFormed` |
| `audit/` | audit records of the codebase | `go test ./internal/docs` | `go test ./internal/docs` |
| `dogfood/` | dated dogfood records of the tools, one file per tool and run: every verb used cold, every finding graded | `go test ./internal/docs` | `go test ./internal/docs` |
| `fixtures/` | doc examples and test fixtures | `go test ./internal/docs` | `go test ./internal/docs` |
| `nova-config/` | nova-config guide: the permanent configuration and its apply into Redis | `go test ./internal/docs` | `go test ./internal/docs` |
| `nova-table/` | nova-table guide: the design statement, the keys, the verbs, the render rules | `go test ./internal/docs` | `go test ./internal/docs` |
| `ratings/` | cold ratings of the tools, one file per rater and tool | `go test ./internal/docs` | `go test ./internal/docs` |
| `sprint/` | the nova-sprint 1.0.0 glossary, which moves to nova-sprint's docs/ at the split | `go test ./internal/docs` | `go test ./internal/docs -run TestRetiredWordsAppearOnlyInRecords` |
| `stranger/` | cold stranger runs of the tools, one file per run, every stumble with its proposed card | `go test ./internal/docs` | `go test ./internal/docs` |
