# AGENTS.md — generated map of docs/

Do not edit. `make map` regenerates this file. Root: [AGENTS.md](../AGENTS.md). Rules: [STANDARD.md](STANDARD.md).

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

A HOLD note is read by the machine only for these four lines, each at the start of a trimmed REPORT line. Member finish and friend sync carry them as semicolon-delimited segments after their one-line summary. Nothing else in a HOLD note is read by a machine. `docs/SPEC-SPRINT.md` section 8 is the rule.

- `PATHS-PROPOSED: <glob>[,<glob>...]` widens PATHS in place when the stream is marked land-protected for the card's repository. The card keeps its id and returns to ready.
- `NEEDS: <card-id>` adds that dependency when the card has landed, and parks this card waiting when it has not.
- `TIER: flash|pro|heavy` recuts the tier in place when a friend of the stream serves it.
- `GATE-HOST: linux` marks the next attempt's executable Go gates for a Linux bench.
