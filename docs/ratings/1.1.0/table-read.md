# nova-table READ rating, nova-tools 1.1.0

Rater: qwen3.8-flash (cold reader, reading only)
Build: c28448a54d56
Score: 7.5/10
README: 8/10

## Reasons

Read cold, as a visitor running nothing: the first confusion was at README.md:25. Its setup sentence says `nova-table loads the functions it needs`, and nothing yet says which functions, loaded where, or what happens when the load is refused. The first boredom was at cmd/nova-table/main.go:13: the root help prints a column grammar, fold table, epoch rules and shell rules as prose, and the reader has met that grammar twice already (README.md:25's row, docs/CLI.md's `### First run` block at docs/CLI.md:2333). The first doubt was at docs/STANDARD.md:56: the family contract says every verb accepts `--json`, and by cmd/nova-table/main.go:177 only two verbs of thirty-six do.

The trust is real: the `### First run` transcript is pinned line for line by cmd/nova-table/firstrun_test.go (a functional test that runs the document), every refusal carries a copy-paste remedy, one round trip per verb is printed as `trips=`, the package doc states its key layout, the single-writer rule for bound cells is honest and enforced, and the model and tests stand beside it. This is code I would trust to use.

It falls short of ten as writing. `nova-table help` is roughly a hundred and forty lines: an honest three-question opening, then thirty-six usage lines, then a wall of `usageDetails` before the `example:` block at cmd/nova-table/main.go:86, whose lines are the reader's actual first run. The guide opens with transcribed chat instead of a statement (docs/nova-table/README.md:3). The package doc still opens with dated quotations naming a person (pkg/ntable/ntable.go:2) against the tree's own generality rule. A stranger meets machine and friend names as example row keys (docs/nova-table/README.md:484) and cannot tell what is real. The column grammar lives in five places: banner, verb syntax, CLI.md, guide and spec, so drift is a standing risk. The module keeps five verbs no shipped tool reaches (pkg/ntable/oset.go:24).

A ten: the banner cut to its three answers and its table; quotations out of the opening of every public file; `--json` on every verb from the same value; the dead helpers gone; role names in examples; the grammar written once and pointed at. The reading says: a good tool to use; a body of writing that asks its reader for patience.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/nova-table/README.md:3 | the guide's first section is transcribed design chat, quotations before any statement of what the tool is or does; the raw words of line 17 stand in the reader's first screen | open the guide with what a table is and what a first run gives back; the quotations belong in a dated design note under docs/ | S |
| 2 | pkg/ntable/ntable.go:2 | the package doc opens on a dated quotation naming a person (six sites; render.go:26 the same), against the tree's own generality rule, its debt ledgered at internal/ci/testdata/generality/pkg/ntable.txt:3; the guide's attribution was cleaned on 2026-10-03, the code's was not | state the rule in the comment's own present-tense words; keep the source of the idea in the design doc | M |
| 3 | cmd/nova-table/main.go:13 | usageDetails: about eighty lines of spec prose (column grammar, folds, epochs, order, shell) between the usage list and the example: block; the root help is roughly 140 lines, and the grammar restates CLI.md, the guide and the spec | keep the banner's three answers, the verb table and the example: lines; one sentence and a pointer per subject; details stay in each verb's own help | M |
| 4 | cmd/nova-table/main.go:177 | --json reaches only batch and member read; docs/STANDARD.md:56 promises every verb accepts it, so `show`, `list` and `cell members` leave a program parsing typed text | build the one value each verb already renders and encode it when --json is asked | M |
| 5 | pkg/ntable/oset.go:24 | Add, Remove, Move, Card, MembersOf and CountCmd.Val stand unreachable from every shipped command root; the guide at docs/nova-table/README.md:88 still presents five of them as the module's verbs | land the deletion the quality branch already carries, or name the caller that keeps each | S |
| 6 | docs/nova-table/README.md:484 | the order example's row keys are five fleet names (lines 484 to 489, with a comment about friends and machines); a stranger cannot tell an example from a fact, and the text scan carries the debt | replace them with role names; the fix exists on a quality branch, not landed at this head | S |
| 7 | pkg/ntable/render.go:12 | the general table renderer imports the card-cost cents formatter (line 590) while ntable.go's doc says the package knows nothing of sprints: a sprint-shaped dependency in a general file | fold the numbers in the table; let the caller format money | S |
| 8 | cmd/nova-table/main.go:244 | history prose in comments: seat selection is explained as carried from a named past file with a ticket number; watch.go:34 keeps an uncited owner's finding in quotes | say why the code is so, in the present tense, in your own words | S |

## Good, keep
- cmd/nova-table/firstrun_test.go runs the docs/CLI.md and docs/TESTS.md `### First run` transcripts line for line with nothing normalised: the document is the contract, and it cannot drift silently.
- The refusal grammar (cmd/nova-table/main.go:120): one line, every problem, a remedy that runs; `trips=` printed beside the result; `--dry-run` planning every write verb; each verb's help closing with `effect:` (cmd/nova-table/help.go:259).
- The bound-cell single-writer rule and the epoch/receipt honesty of docs/SPEC-NOVA-TABLE.md: claims the code bears out, recorded metadata never dressed up as deduplication.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| READ 2026-10-02: the guide opens on dated quotations of a named person | CHANGED | docs/nova-table/README.md:3 is now titled in the owner's words, undated and unnamed, but quotations still open the guide |
| READ 2026-10-02: the package doc opens on dated quotations of a named person | STILL THERE | pkg/ntable/ntable.go:2 and render.go:26 name and date the source; ledger internal/ci/testdata/generality/pkg/ntable.txt:3 counts six sites |
| READ 2026-10-02: the banner is a wall | STILL THERE | cmd/nova-table/help.go:142 answers the three questions first, but cmd/nova-table/main.go:13 still prints its wall before the example: block at main.go:86; the 2026-10-03 help wave touched the other tools' banners, not this one's |
| READ 2026-10-02: duplicate CLI plumbing | CHANGED | cmd/nova-table/help.go:22: one command list feeds the dispatcher and every help entry, one seam per verb's flags; the duplicated column grammar between banner, CLI.md, guide and spec still stands |
| README 2026-10-02: 6.5 to 8.4 | CHANGED | README.md:25 keeps the banner's line 1 verbatim and names the Redis prerequisite; the setup sentence still hides the library load, the first confusion named in Reasons |
