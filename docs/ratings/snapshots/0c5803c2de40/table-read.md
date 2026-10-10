# nova-table READ rating, current baseline 0c5803c2de40

Rater: opencode/qwen3.8-flash
Build: 0c5803c2de40
Score: 7.5/10
README: 7/10

## Reasons

The README's line for this tool (README.md:25) is the banner's line 1, kept identical: `nova-table: tables whose cells are ordered sets, kept in Redis and drawn as text` (cmd/nova-table/help.go:125). The row is honest about the prerequisite: a separate running Redis.

Read first, before any code, the three moments the card asks for:

- First confused: docs/nova-table/README.md:109 says the footer label is blank by default; pkg/ntable/ntable.go:24 says default total. One of these is a bug and a cold reader cannot tell which.
- First bored: docs/nova-table/README.md:5. The guide opens with three blocks of dated historical quotations before any prose tells me what the tool is for; the design reads as archaeology, not as a statement.
- First doubted a claim: cmd/nova-table/watch.go:52. A `--check` flag stands in the usage syntax; scanning docs/CLI.md:2222 and the guide's Watching section finds no sentence about it. A flag that ships unexplained makes me trust the rest of the docs less.

What earns the 7.5: the model is one primitive held all the way through (an ordered set per cell; a projection per column; a fold per footer), and the docs say so in three lines. Entry point, verbs and data are found in a minute: the dispatch table (cmd/nova-table/help.go:25) lists every verb with syntax and a paste-able example; the keys are named in one block (docs/nova-table/README.md:222). Refusals carry their remedy in one line with the command that fixes them (cmd/nova-table/main.go:103). Every result line prints its round trips (`trips=`), so the store contract is visible from the terminal. The first-run transcript in docs/CLI.md:2157 is executed line by line by cmd/nova-table/firstrun_test.go:13 (functional), and docs/nova-table/README.md:584 is candid about the boundary Redis ACLs cannot hold. Comments cite the TLA+ models (cmd/nova-table/library.go:22, cmd/nova-table/session.go:51). I would use this tool and trust this code.

What keeps it from a 10: the tool sits outside the family's shared skeleton, so the one-flag-one-meaning promises (`--json` on every verb, a dry run on every write, bounded listings) are missing (finding 1, pkg/tool/tool.go:7 states them against this tool's own dispatch at cmd/nova-table/main.go:124); two doc lines contradict the code (finding 2); there is no store-free verb, so nothing but help and version runs from the binary alone (finding 3); the design provenance is quoted history by name instead of present-tense prose (finding 4); and weight shows where 28 handlers hand-repeat the same preamble (finding 9). A 10 would need: the skeleton shape, the docs and code saying the same thing everywhere, one real verb that runs with no store, prose where the quotations are now, and the repeated preamble hoisted.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-table/main.go:124 | the tool builds its own dispatch, banner and flag seam instead of pkg/tool, so of 28 verbs only batch and member read answer `--json` (cmd/nova-table/batch.go:43, cmd/nova-table/member.go:127), no write verb takes --dry-run, and no listing takes --max with a MORE total | route the verbs through pkg/tool: one result value, two renderings; --dry-run on the writes; --max with MORE on cell members and list | L |
| 2 | pkg/ntable/ntable.go:24 | the package doc says the footer label's default is total; pkg/ntable/ntable.go:186 and docs/nova-table/README.md:109 say none; the cell-arity grammar also disagrees with itself (cmd/nova-table/cell.go:17: add takes members in batch, remove refuses at one) | one sentence per fact in one place: fix the doc comment and the refusal text to match the code and the guide | S |
| 3 | cmd/nova-table/help.go:132 | the first run needs a Redis, and the guide's trial recipe (docs/nova-table/README.md:46) starts its own server process, so a visitor cannot run one real verb from the binary alone the way other tools offer a quickstart or example verb | add a store-free example verb that seeds and renders a demo table (the create grammar is already checked client-side at cmd/nova-table/table.go:53) | M |
| 4 | docs/nova-table/README.md:3 | the guide opens on three blocks of dated quotations by a named person, and the same quotations as code comments stand at pkg/ntable/ntable.go:2, :77, :183 and pkg/ntable/render.go:25, :59; the design is archaeology instead of present-tense prose | rewrite the guide opening and package doc in the tool's own present tense; keep the ruled decisions in docs/SPEC-NOVA-TABLE.md as rules, not as quotes | M |
| 5 | cmd/nova-table/main.go:165 | seat plumbing names another tool: the environment is NOVA_SPRINT_SEAT (cmd/nova-table/main.go:156 says the resolution was carried from that tool's old file), and the profile lookup at cmd/nova-table/main.go:179 hardcodes that tool's name; a second copy can drift from the first | hoist the selection into pkg/seatcred as one function the whole family calls, or name the environment and profile after the concept, not one tool | M |
| 6 | cmd/nova-table/watch.go:52 | `--check` is advertised in the usage syntax (cmd/nova-table/help.go:54) and has no sentence in docs/CLI.md:2222 or in the guide's Watching section | document the stall-row behavior in both docs, or drop the flag; a verb nobody documents is a verb nobody runs | S |
| 7 | pkg/ntable/render.go:521 | the summary line always ends in the literal `-> ETA` while ETA is an unshipped feature (docs/nova-table/README.md:413); printed output carries a placeholder the tool will not fill | print `x/y z%` until rate sampling exists; ship the ETA suffix with the number it names | S |
| 8 | docs/nova-table/README.md:488 | the Order walkthrough's example rows are fleet words the generality rule refuses (the debt ledger internal/ci/testdata/generality-text/docs/nova-table.txt records them); a stranger reads them as concepts | rename the example rows and view titles to neutral words; the ledger row then shrinks | S |
| 9 | cmd/nova-table/table.go:79 | the same six-line preamble repeats 28 times (table.go x8, cmd/nova-table/cell.go x4, cmd/nova-table/member.go x4, cmd/nova-table/row.go x5, order.go, batch.go, watch.go); shellWords (cmd/nova-table/session.go:291) is another hand-rolled parser | hoist connect-prepare-run-refuse into one helper or the skeleton; keep the tokenizer only with a written reason beside it | M |

## Good, keep

One round trip per verb, printed on the line as `trips=`, with the batch replay contract (same operation id answers the original receipt) documented and tested: keep the budget visible.

The refusal grammar: every no names the input it wants and the exact next command (`; run:`, cmd/nova-table/main.go:103), and help is never a refusal: keep both.

The executed first-run transcript: docs/CLI.md:2157 and docs/TESTS.md:825 are run line by line by the functional tests, so the document cannot drift quietly: keep that pin.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the guide and package doc open on dated quotations of a named person (6.5 rater) | STILL THERE | docs/nova-table/README.md:3 opens on quotation blocks; pkg/ntable/ntable.go:2 still names the person with a date; :183 keeps the quoted ruling |
| the banner is a wall (6.5 rater) | CHANGED | the command-line banner is now a compact answer block (cmd/nova-table/help.go:125), while docs/nova-table/README.md:5 still opens on the quotation wall |
| personal design quotations (8.5 rater) | STILL THERE | pkg/ntable/render.go:25 and :59, cmd/nova-table/order.go:13, cmd/nova-table/watch.go:34 |
| duplicate CLI plumbing (8.5 rater) | STILL THERE | cmd/nova-table/main.go:162 carries a second seat selection; cmd/nova-table/main.go:165 leaks the other tool's environment name |
| the README rated 6.5 to 7 by one rater and 8.4 by another | CHANGED | README.md:25 equals the banner's line 1 and names the prerequisite honestly; the tool has no entry in the adoption guide's sections (docs/USAGE.md:140 onward) |
