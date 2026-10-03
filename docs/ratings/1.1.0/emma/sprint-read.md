# nova-sprint READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 2c02b2aa2042
Score: 7/10
README: 8/10

## Reasons
The tool establishes an ambitious and rigorous coordination engine for distributed task execution. Its twin in-memory mode allows rapid offline evaluation without live storage infrastructure, and its deterministic state machine reliably models the card lifecycle from deal to landing.

A score of 10 would require decomposing the monolithic verb registry and oversized app struct, removing colloquial chat transcripts and historical quotations from comments and contracts, harmonizing CLI flag naming across documentation and code, and modularizing internal step handlers.

The first place of confusion was docs/SPEC-SPRINT.md:32, where the contract specification introduces billing and display details filled with conversational chat quotes before defining the primary card lifecycle.
The first place of boredom was cmd/nova-sprint/verbs.go:39, where 61 verb definitions are enumerated in a single flat registry file spanning 2551 lines.
The first place of doubting a claim was cmd/nova-sprint/verbs.go:44, where the reference's syntax lines name --limit while the verb's own syntax and help name --max.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-sprint/verbs.go:39 | 61 verbs declared in a 2551-line monolithic file making discovery and navigation overwhelming | group verbs into subsystem modules for fleet friend reader and workflow | L |
| 2 | cmd/nova-sprint/main.go:52 | app struct contains 34 disparate fields mixing connection pooling simulation state and git environments | partition app state into focused context structs for store runner and git | M |
| 3 | docs/SPEC-SPRINT.md:32 | contract buries core lifecycle semantics under ASCII table layouts and verbatim conversation transcripts | restructure specification to lead with the card lifecycle state machine before display layouts | M |
| 4 | cmd/nova-sprint/verbs.go:44 | the reference's syntax lines name --limit while the verb's own syntax and help name --max | harmonize syntax lines between documentation and command definitions | S |
| 5 | cmd/nova-sprint/every_row_test.go:73 | source code and tests embed dated chat transcript citations as explanatory comments | replace historical chat quotations with present-tense design rationale | S |
| 6 | cmd/nova-sprint/friendclean.go:51 | retention rules retain historical incident ticket references rather than documenting policy | replace issue tracker references with clear documentation of retention invariant | S |

## Good, keep
The in-memory twin engine enables complete offline simulation of multi-agent workflows without requiring external services.
Strict generation stamping and epoch checks prevent race conditions and duplicate executions across distributed workers.
The unified card lifecycle cleanly transitions work units through deal, review, rework, and landing with deterministic status tracking.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 52 verbs | CHANGED | cmd/nova-sprint/verbs.go:39 |
| a 30-field app struct | CHANGED | cmd/nova-sprint/main.go:52 |
| owner quotations as comments | STILL THERE | cmd/nova-sprint/every_row_test.go:73 |
| leftovers of removed features | STILL THERE | cmd/nova-sprint/friendclean.go:51 |
| the first contract buries the workflow under display details and history | STILL THERE | docs/SPEC-SPRINT.md:10 |
| README rated 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:26 |
