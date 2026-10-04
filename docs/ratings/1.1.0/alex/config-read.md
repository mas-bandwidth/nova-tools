# nova-config READ rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 044b5dfe9c1b
Score: 8/10
README: 8/10

## Reasons

Read cold, top to bottom: README, AGENTS.md, the command reference's section, the spec, the guide, then the code from main.

The README line for this tool is README.md:28, "a fleet's machines and AI friends as rows in PostgreSQL, applied into Redis", and the banner's first line (cmd/nova-config/main.go:67) repeats it word for word: the doc says what the tool is for and the code does that. The grammar is one shape across seven kinds, generated from one descriptor (internal/config/kind.go), so add, set, remove, list, show and history behave identically and a new kind adds no verb code. The `--file` store is the best first-run property in the family: every verb but apply's write runs with no database, the banner's example block runs as printed, and a test executes it as the documented transcript (cmd/nova-config/firstrun_test.go). Refusals name every problem at once, name the next command, and the remedies repeat the `--file` or `--pg` the run was given so they paste (cmd/nova-config/main.go:669-677). Comments say why, cite their spec section, and are in the present tense; names (kind, row, field, verb) are words a stranger starts with.

The first place I was confused: docs/nova-config/README.md:146, the machine row's `--slots` and `--width`, two capacity numbers on one row, joined by a friend's `--slots` and `--width` (two more, different meanings); the placement rule at docs/nova-config/README.md:96 resolves it, but only on a second read, and every field's help carries a "not the X" clause because the words themselves collide. The first place I was bored: docs/nova-config/README.md:538-558, the Ansible wrapper told a third time, whole, after docs/CLI.md:2011-2020 and the verb's own help. The first claim I doubted: cmd/nova-config/main.go:9 says "every kind (machine, fleet, friend, sprint, loop, route)", six, and the registry at internal/config/kind.go:232-363 holds seven: tier, made by migrate, set and read but never added or removed.

What a 10 would need: main.go split one verb family per file (or the kind verbs generated, as the docs claim they are); the one-result-value rule held by status, migrate --dry-run and kinds, which today build the typed line and the JSON object in separate statements; the Ansible material told once with pointers; the capacity words named after their jobs so the "not the X" clauses can retire.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-config/main.go:223 | one 1,645-line file runs six verb families (kinds, migrate, status, apply, inventory and every kind verb), so a reader after `machine set` walks the fleet-endpoint checks and the Ansible renderer to find it | one file per verb family beside machine.go and verbs.go, or generate the kind-verb runners from the descriptor the docs say generates them | M |
| 2 | docs/nova-config/README.md:146 | the capacity words meet a cold reader as four numbers sharing two words (machine slots and width, friend slots and width), each with a different meaning, and the help text fights the confusion it causes with "not the sprint's width" clauses | name one pair after its job (machine `--ceiling`, friend `--desired`) or open the kinds with one diagram placing all four numbers | M |
| 3 | cmd/nova-config/main.go:1340 | status, migrate --dry-run and kinds build the typed line and the JSON facts in separate statements, so the two renderings can drift, against the one-result-value rule the tool itself states and holds at machine width | build the one tool.Out per verb and render both the line and the JSON from it, as cmd/nova-config/machine.go does | M |
| 4 | docs/nova-config/README.md:495 | the Ansible wrapper material (the two-line script, the unparsed-is-failed variable, the env names) is told three times whole: here, docs/CLI.md and the verb's help | keep one full telling in the guide and one-line pointers from the other two | S |
| 5 | cmd/nova-config/main.go:9 | the package comment names six kinds and the registry holds seven (tier), the first claim a cold reader checks against the code | name tier in the comment's list, the seed kind migrate makes and the deal reads | S |

## Good, keep

The `--file` store: every verb but apply's write runs with no database, the banner's example block is that first run, and cmd/nova-config/firstrun_test.go executes the documented transcript against it line by line.

One Kind descriptor per kind generates the flags, help, refusals, SQL, typed lines and the apply diff (internal/config/kind.go), so seven kinds share one grammar and a new kind adds no verb code.

Refusals that name every problem in one run and a next command that pastes, the remedies repeating the `--file` or `--pg` the run was given (cmd/nova-config/main.go:669-677), with the exit table in the banner.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,655-line main.go that writes every result twice | STILL THERE | cmd/nova-config/main.go is 1,645 lines; runStatus builds the line and the JSON object in parallel statements (cmd/nova-config/main.go:1340-1341) and migrateDryRun its lines and items (cmd/nova-config/main.go:1167-1168) |
| flags off the family's shape | FIXED | every verb takes --json (cmd/nova-config/main.go:427-429) and answers -h before anything is dialled (cmd/nova-config/main.go:223-227); machine width holds the one-value shape (cmd/nova-config/machine.go:137-150) |
| contradictory storage guidance | FIXED | docs/nova-config/README.md:9-13 states the one rule: Postgres the permanent store, Redis a copy, `--file` a local stand-in for a trial, never the fleet's store |
| a large command file | STILL THERE | cmd/nova-config/main.go:1-1645, six verb families in one file |
