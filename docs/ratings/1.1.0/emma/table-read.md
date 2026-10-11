# nova-table READ rating, nova-tools 1.1.0

Rater: gemini-2.5-pro
Build: 264f9c0135c7
Score: 7.5/10
README: 8/10

## Reasons
The tool provides an expressive, mathematically disciplined tabular model over Redis. Treating cells as ordered sets with projection formatting and pooled aggregation folds is an elegant design. Atomic batch mutations with epoch verification and append-only audit stream receipts ensure strong consistency across multi-card fleet workflows.

A score of 10 would require migrating the command-line entry point to the standard tool skeleton, supporting structured JSON output across all read and inspection verbs, removing dated design quotations from documentation and package headers, decomposing the help banner wall into modular subverb topics, and resolving documentation contradictions regarding empty table rendering.

The first place of confusion was docs/CLI.md:2198, where drop without the definition flag leaves behind table identity keys and column schemas, causing subsequent create commands to be refused without an obvious reset procedure.
The first place of boredom was docs/nova-table/README.md:5, where the user guide opens with thirteen lines of verbatim colloquial quotations about ordered sets rather than an architectural overview of data structures and verbs.
The first place of doubting a claim was cmd/nova-table/main.go:61, where the banner asserts that render prints nothing at all when a table is empty, directly contradicting docs/CLI.md:2220 which states that empty tables print header and footer boundaries.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-table/main.go:94 | Hand-rolled CLI dispatcher duplicates command plumbing and omits json output on read verbs | migrate dispatcher to internal tool skeleton to provide json output across all verbs | L |
| 2 | cmd/nova-table/main.go:29 | The usageDetails string dumps a 60-line wall of dense text into root help output | break monolithic usage notes into per-verb help topics | M |
| 3 | docs/nova-table/README.md:5 | Guide opens with thirteen lines of personal design quotations and colloquial text | replace conversational quotes with an architectural overview of the tool | S |
| 4 | pkg/ntable/ntable.go:2 | Package comment opens with dated chat quotations of a named person | replace dated chat citations with technical documentation in present tense | S |
| 5 | cmd/nova-table/main.go:61 | Banner asserts empty table renders nothing contradicting CLI documentation | reconcile rendering documentation so both describe identical header output | S |
| 6 | docs/nova-table/README.md:46 | Local setup documentation provides instructions to launch an external daemon on host | document disposable container workflow or memory test fixtures | S |
| 7 | cmd/nova-table/main.go:30 | Option idem is documented as metadata only without providing deduplication | clarify retry semantics and align with standard idempotency contracts | S |

## Good, keep
The ordered set primitive with configurable projections and pooled percentage folds provides a clean mathematical foundation.
Atomic batch manifests with epoch preconditions and stream change receipts ensure safe concurrent writes.
Dynamic column additions and row reordering maintain stable layouts without destroying cell contents.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the guide and package doc open on dated quotations of a named person | STILL THERE | docs/nova-table/README.md:5 and pkg/ntable/ntable.go:2 |
| the banner is a wall | STILL THERE | cmd/nova-table/main.go:29 |
| personal design quotations and duplicate CLI plumbing | STILL THERE | cmd/nova-table/help.go:24 and docs/nova-table/README.md:5 |
| the README then: 6.5 to 7 from one rater, 8.4 from another | CHANGED | README.md:25 |
