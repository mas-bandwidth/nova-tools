# nova-table READ and USE rating, nova-tools 1.2.0

Rater: Muse Spark (opencode/muse-spark-1.3-contributor-free) in OpenCode, a sprint worker on a friend's re-rate card
Build: 65416146b5bb
READ: 8/10
USE: 7/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-table version` prints `nova-table v1.0.1-0.20261007020011-65416146b5bb linux/amd64 go1.26.6`. Built and run on a Linux bench machine. The root help, `help <verb>`, `<verb> -h` and `help <verb> <subverb>` were read cold for every verb, plus docs/nova-table/README.md, internal/ntable/ntable.go and the help seam in cmd/nova-table/help.go. Used for real in a throwaway directory inside the job directory with no live store, no server and no network, per the card rules: every write verb under `--dry-run`, `batch --dry-run` with and without `--json`, and about twenty refusals (unknown verb, unknown flag, missing args, bad score, missing store, malformed manifests). No live store was touched and no server was started, so the live read paths (list, show, render, watch, shell, check), epoch moves, the drop/create/set loop, member sharing across tables, cell no-op writes and fold arithmetic were judged from help and code only and are reported as not done below.

## Reasons

READ. The banner answers the three questions first: one line saying what the tool is, a how-it-works paragraph naming rows, columns, ordered-set cells, projections, folds, epochs, receipts and views, and a first run that names the Redis prerequisite, the store-less `--dry-run` form and a throwaway-store recipe. Every verb answers `-h` at exit 0 with usage, a runnable example, grouped flags, connection, exit codes and an `effect:` line. Layered help works: `help cell`, `cell -h` and `help cell add` all answer. Unknown verbs, flags and arities are refused with the valid names and a `run:` line, and a read with no store refuses at exit 2 with the throwaway recipe and the address it tried.

What keeps READ at 8. The banner is 127 lines and the column grammar, fold table, epoch rules, bound-cell rules, order rules and shell prose still sit before the final `example:` block. The same long `--dry-run` sentence is repeated on every write page, and the view verbs reuse it word for word, naming the table, its epoch, its rows and columns and a bound cell, none of which a view has. `--score` and `--at-epoch` print as `<string>` while the usage lines and the score refusal show they take numbers. `check -h` says only that it reads the store and writes nothing, never what it checks. `create -h` prints the `--member-prefix` default but never says member ids are shared across tables on that prefix. Dated owner quotations are still in the code comments (internal/ntable/render.go:26 and neighbours, cmd/nova-table/order.go:15). The guide now opens with a design statement (docs/nova-table/README.md `## The design`) and internal/ntable/ntable.go opens plain, both better than 1.1.0.

USE. Every write verb ran under `--dry-run` with no store and printed one `TABLE DRY-RUN` line naming the exact FCALL with `dialled=0 written=0`. `batch --dry-run` prints the plan with members, changes and guards counts, and `--json` renders the same plan as one object. Refusals are precise: a bad `--score` says it wants a number, a missing row says what `row add` wants, an unknown flag names the verb help, and a read with no store names the throwaway command. Exit codes held: 0 for dry runs, 2 for usage and unreachable store, 1 for store-side manifest refusals.

What keeps USE at 7. The reads have no `--json`: `show --json`, `list --json` and `cell members --json` are refused as unknown flags, so an AI consumer parses drawn text; only `batch` and `member read` take `--json`. The unknown-flag list disagrees with `-h`: `list --bogus` says the flags of list are `--redis` while `-h` lists `--redis` and `--seat`. A malformed manifest names one problem at a time (`batch '{"schema":1,"table":"t2"}'` names only the operation id) and schema failures exit 1 while usage exits 2. `shell --dry-run` still requires a store: piped dry-run lines are refused for `--redis` at exit 2, so the shell cannot be tried store-less the way each verb can. The view summary promises an ETA the help cannot fill (`--summary` help shows a literal arrow with no value). Live paths were not run here by rule, so to reach 10 the tool needs `--json` on the reads, one-turn manifest checks, a store-less shell dry run, a scoped-member sentence in help, and the live traps already witnessed by the parallel re-rate (drop/create/set loop, shared member ids, no-op cell writes) closed.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-table help` | the banner is 127 lines; the column grammar, fold table, epoch, bound-cell, order and shell prose sit before the final example block | keep the three answers, the verb table and the example lines; move each prose subject to its own verb help with one pointer line | M |
| 2 | `nova-table view set -h` | every write page repeats the same long dry-run sentence, and the view verbs reuse it naming the table, its epoch, its rows and columns and a bound cell, none of which a view has | give each verb a short effect line and give the view verbs their own line with no epoch or bound cell | S |
| 3 | `nova-table cell add -h` | usage says `[--score <n>]` but the flag list says `--score <string>`; the refusal proves it wants a number | name the value `<n>` in the flag list | S |
| 4 | `nova-table show -h` | `--at-epoch <string>` takes an epoch number; render and member read print the same wrong type | name the value `<n>` in each usage text | S |
| 5 | `nova-table check -h` | says only reads the store and writes nothing; never says what is checked or what a failure looks like | name the invariants checked and the refusal line | S |
| 6 | `nova-table show demo --json` | unknown flag; only batch and member read take --json, so list, show, render, cell members, watch and the view reads leave an AI parsing drawn text | give every read verb --json from the value it already renders | M |
| 7 | `nova-table list --bogus` | the refusal lists the flags of list as --redis; -h lists --redis and --seat | build the refusal list from the same flag set the help prints | S |
| 8 | `nova-table view set -h` | --summary help shows a literal arrow with no value in the summary shape | print counts only until an ETA can be computed, and say so in the flag help | S |
| 9 | `nova-table batch --dry-run '{"schema":1,"table":"t2"}'` | names only the operation id though epoch, revision and members are also missing; schema failures exit 1 while usage errors exit 2 | name every missing key in one refusal and exit 2 for a manifest that fails its schema | S |
| 10 | `printf 'create demo2 --columns a,b --dry-run' \| nova-table shell --dry-run` | refused for --redis at exit 2, so the shell cannot be tried store-less the way each verb can | let shell --dry-run plan write lines with no store, as each verb does | S |
| 11 | `nova-table create -h` | prints the --member-prefix default but never says member ids are shared across tables on that prefix or how to scope a table | one sentence on the shared scope and the per-table prefix remedy | S |
| 12 | internal/ntable/render.go:26 | dated owner quotations in comments (also render.go:39, :42, :45, :60, :67, :396 and cmd/nova-table/order.go:15) | state each rule in the present tense in the comment's own words | S |
| 13 | internal/ntable/render.go:12 | the general renderer imports internal/cardcost to format cents (used at render.go:590) while ntable.go says the package knows nothing of sprints | let the caller format money | S |

## Good, keep

The banner's three answers up front, the store-less `--dry-run` example, and the throwaway-store recipe that the no-store refusal reprints with the address it tried.
Every dry run printing the exact FCALL with `dialled=0 written=0`, and `batch --dry-run --json` rendering the same plan as one object.
Layered help (`help cell`, `cell -h`, `help cell add`) answering with no store, each page closing with `effect:` and exit codes.
Refusals that name what the input wants with a runnable `run:` line, and exit 2 for usage against exit 1 for store-side refusals.
The plain package doc in internal/ntable/ntable.go and the guide opening on `## The design` instead of transcribed chat.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| READ 1.1.0: the guide opens on transcribed chat | FIXED | docs/nova-table/README.md opens on `## The design`, a statement of the primitive |
| READ 1.1.0: the package doc opens on dated quotations | CHANGED | internal/ntable/ntable.go now opens plain; internal/ntable/render.go and cmd/nova-table/order.go still carry eight quoted sites |
| READ 1.1.0: the banner is a wall before the example | STILL THERE | `nova-table help` is 127 lines with the grammar and rules before the final example block |
| READ 1.1.0: --json on two verbs only against the family promise of every verb | STILL THERE | `show demo --json` is an unknown flag; only batch and member read take --json |
| READ 1.1.0: dead helpers in oset.go | NOT CHECKED HERE | not re-audited in this pass; no claim made |
| USE 1.1.0: reads untriable without a daemon, judged from help alone | NOT DONE HERE | no server started per card rules; reads, watch, shell live paths and folds judged from help and code only |
| USE 1.1.0: a manifest with several missing keys refused one at a time | STILL THERE | `batch '{"schema":1,"table":"t2"}'` names only the operation id |
| USE 1.1.0: drop/create/set remedy loop | NOT WITNESSED HERE | no live store per card rules; the parallel 1.2.0 re-rate still reports the loop and it is not claimed as fixed here |
| READ 1.1.0: render.go imports the card-cost formatter | STILL THERE | internal/ntable/render.go:12 imports internal/cardcost, used at :590 |
