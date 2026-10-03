# nova-table READ rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 7/10
README: 8/10

## Reasons

The README row says the tool is "tables whose cells are ordered sets, kept in Redis and drawn as text", and the package comment at cmd/nova-table/main.go:1 says the same in one paragraph; the code does that. The first place I was confused: docs/SPEC-NOVA-TABLE.md:1 describes work tables, ordered sets and live views, but the tool's first run in help starts with write-epoch metadata at cmd/nova-table/main.go:29 before any table exists, so a cold reader meets receipt fields before the table concept. The first place I was bored: the verb list in the banner runs to twenty verbs with subverbs, and each line's flags make it a wall. The first claim I doubted: the card this rating answers says the help offers a mem or in-memory form, but nova-table help offers none — --redis takes host:port or an absolute Unix socket path only, so the no-Redis promise is absent from the tool itself.

The spec is otherwise clean and present-tense, the data model is explicit (table, column, row, projection, fold), and the code is split into files by area (main.go, batch.go, cell.go, library.go, help.go). The names a stranger understands: create, set, drop, row add, cell move, member find, render, watch. The tests are extensive and pin the functional paths.

What keeps it from a 10: no store-free or mem path for a cold first run, the banner wall, and the hand-rolled dispatch at cmd/nova-table/main.go:94 beside the family skeleton.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-table/main.go:29 | the help opens with write-epoch and receipt metadata before any table exists, so a cold reader meets bookkeeping first | move the metadata paragraph under create's own help and open with a one-line table example | S |
| 2 | cmd/nova-table/main.go:94 | the dispatch is hand-rolled with flag instead of internal/tool, so this tool keeps its own CLI plumbing | port the verbs onto internal/tool or document why this tool stays on flag | M |
| 3 | cmd/nova-table/main.go:29 | the banner lists twenty verbs with subverbs and flags; it reads as a wall | shorten the banner to the five core verbs and let help list the rest | M |

## Good, keep

The explicit projection and fold grammar with examples. The package comment that names the store and the sprint table as the first table. The tests that pin the first-run transcript.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the guide and package doc open on dated quotations of a named person | FIXED | cmd/nova-table/main.go:1 package doc is one present-tense paragraph with no quotations |
| the banner is a wall | STILL THERE | cmd/nova-table/main.go:29 lists twenty verbs with subverbs |
| duplicate CLI plumbing | STILL THERE | cmd/nova-table/main.go:94 hand-dispatches with flag, not internal/tool |
