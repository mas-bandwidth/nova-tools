# nova-sprint READ rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 7.5/10
README: 8/10

## Reasons

The README row says the tool coordinates work cards, readers and landing, and the help's first-run block sends a cold reader straight to a no-Redis twin walkthrough; the code does that. The first place I was confused: docs/SPEC-SPRINT.md:1 opens with the table layout and column formulas before saying what a tick is, so the workflow is buried under display detail. The first place I was bored: docs/SPEC-SPRINT.md:34, the owner quotations in the table explanations, which stretch a technical description with dated back-and-forth. The first claim I doubted: the help says "Each tick deals ready cards to members", but on a twin a card with no brief still moves to working, so a worker can be handed a task with no task; the code says so in a NOTE, and a cold reader has to trust the NOTE over the headline.

The tool is huge and honest about it: 52 verbs, each with a one-line usage, grouped help for fleet, friend, reader, goal and stream, and exit codes on every verb's help. The data model is clear once found: cards move through work states ready/working/review/merging/landed, readers read in review, merge queues by stream. The names a stranger understands: deal, tick, take, finish, read, land. The code is split into files by area (main.go for the app, fleet.go, reader.go, merge.go, land.go, twin.go) and the spec's demanded tests are named behaviours.

What keeps it from a 10: the app struct at cmd/nova-sprint/main.go:52 is around thirty fields, the one-process view a reader must hold at once; the spec's opening section is display-first; and owner quotations remain in the normative text.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-sprint/main.go:52 | the app struct is around thirty fields, the whole process view in one type; a cold reader has to hold all of it at once | split into the store view, the clock/sleeper, and the test seams | M |
| 2 | docs/SPEC-SPRINT.md:1 | the spec opens with the table layout and column formulas before saying what a tick is; the workflow is buried | move the tick and card-lifecycle sections before the table formulas | M |
| 3 | docs/SPEC-SPRINT.md:34 | owner quotations and dated back-and-forth sit in the normative text, where the family avoids them | replace each quotation with the rule it produced, keep the date in a changelog | S |

## Good, keep

The no-Redis twin walkthrough and the first-run block that makes a cold start real. The grouped help and the verb list that names every refusal. The tests that pin each spec-demanded behaviour by name.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 52 verbs | STILL THERE | `nova-sprint frumble` lists 52 available verbs |
| a 30-field app struct | STILL THERE | cmd/nova-sprint/main.go:52 |
| owner quotations as comments | STILL THERE | docs/SPEC-SPRINT.md has 34 owner mentions |
| leftovers of removed features | CHANGED | `nova-sprint frumble` lists only current verbs; retired spellings are refused |
| the first contract buries the workflow under display details | STILL THERE | docs/SPEC-SPRINT.md:1 opens with the table, not the tick |
