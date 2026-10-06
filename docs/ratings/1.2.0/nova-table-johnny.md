# nova-table READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card
Build: 2d1c36b140e1
READ: 8/10
USE: 8.5/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-table version` prints `nova-table v1.0.1-0.20261006141530-2d1c36b140e1 linux/amd64 go1.26.6`. Built and run on a Linux bench machine against a throwaway Redis 8.0.5 on a Unix socket inside the job's scratch directory, started with the banner's own recipe and stopped with `shutdown nosave`; no live store, no seat, no forge read.

## Reasons

READ. The banner answers the three questions first: what a table is (rows and columns of ordered sets in one Redis store), what the first run needs (a Redis 7 or later, empty is enough), and how to get one by hand in four pasteable lines (cmd/nova-table/main.go:13). Every verb and subverb has a `-h` with usage, an example, flags, a connection block, the write epoch and receipt block where it applies, exit codes and an `effect:` line that says whether it writes. The column grammar is stated once in the banner with a worked example, and the refusal grammar is the same on every verb. `batch -h` explains the manifest, the replay rule for `operation_id`, and that `--idem` does not deduplicate. The package doc and the guide no longer open on quotations.

What keeps READ at 8. The banner is still 127 lines with the `example:` block, the reader's actual first run, at the very end. `batch -h` shows only the create shape; the `expect.place` object and the shapes of move, remove, set and unset are in docs/SPEC-NOVA-TABLE.md:350 and nowhere in the help, so the first move manifest written from the help alone is refused. `clear -h` does not say what clear empties (rows and members of the active epoch; the definition stays). The view summary is documented as `x/y z% -> ETA` and prints exactly that, a literal `ETA` with no value (internal/ntable/render.go:522). Every verb's exit-code line says `2 usage`, while an unreachable store also exits 2; only the shell's page says `2 usage/input/connection failure`. The view verbs' `effect:` line talks about the table's epoch and bound cells, which a view does not have.

USE. Two real jobs ran end to end on the throwaway store. A board: create with five columns (count, text and `pct(done)`), rows, cells, a move, `row set` on the text column, `show`, `render` with a pooled footer, `check`, `col add` and `col move`, `row move`, `row order`, a standing `row sort --keep` that correctly refuses a later `row move` with the command that ends it, `row hide`, `set --hide` and `--show`, a view with a title, summary and `view state STOPPED`, and `watch --once` and `watch --out` to a file. A batch: a dry run, a real batch with `--json` whose receipt carries before and after for place, score, member revision and table revision, and a stale `expected_table_revision` refused with the observed one. `member read --json` and `--cell row:col --json` give a parseable reading. Every refusal met named the problem and a runnable next command: a duplicate member names where it already sits, a stale or future epoch names the active one, `col del` on a column with members names each `cell remove` to run, a bad projection lists every valid one, an unknown flag lists the verb's flags, an unreachable address prints the throwaway-store command. The shell runs a script on one connection, stops at the first failing line, names it, and plans every write under `--dry-run`.

What keeps USE at 8.5. After `drop t2`, the three remedies form a loop: `show t2` says no such table and to run `create`; `create t2 --columns a` says the table exists with another definition and to run `set`; `set t2 --columns a` says no such table and to run `create`. The ways out, `create` with the old columns or `drop t2 --definition`, are named by none of the three. `cell move` of a member not in the source cell is refused with the bare store code `NOTMEMBER`. The reads `show`, `list`, `check`, `render`, `cell members` and `member find` still have no `--json`; only `member read` and `batch` do. A batch manifest is checked one problem at a time. `--redis mem:` is still refused, so there is no way to try the reads without a Redis binary.

A 10 would give every remedy after `drop` the command that actually works, put every manifest shape in `batch -h` and name every manifest problem in one refusal, add `--json` to every read from the same value the lines print, print no `ETA` until it has a value, and say in `clear -h` what goes.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-table drop t2` then `show`, `create`, `set` | After a plain drop, `show t2` refuses "no such table; run: nova-table create 't2' --columns <columns>"; `create t2 --columns a` refuses "exists with another definition; run: nova-table set 't2' --columns <columns>" (internal/ntable/store.go:350); `set t2 --columns a` refuses "no such table; run: nova-table create ..." (internal/ntable/store.go:335). The loop never names `create` with the saved columns or `drop t2 --definition`. | When the saved definition outlives a drop, refuse create with the saved columns and `drop <t> --definition` as the two remedies, and have show and set name the same pair. | M |
| 2 | `nova-table cell move t r ready done nosuch` | Refused "member \"nosuch\": NOTMEMBER; run: nova-table cell members ..." (internal/ntable/store.go:363): the store code is the whole explanation. | Say "not in this cell" in words, and name the member's actual place when it has one (as cell add's duplicate refusal does). | S |
| 3 | `nova-table show demo --json` | Refused "unknown flag --json; the flags of show are --at-epoch, --redis". show, list, check, render, cell members and member find print drawn or key=value lines only; member read and batch have --json. | Give every read the --json member read has, from the same value the lines print. | M |
| 4 | `nova-table batch -h` (cmd/nova-table/batch.go:26) | The help shows only `expect.absent` and `create`. A move manifest written from it with `"expect":{"place":"build:ready"}` is refused "place must be an object"; the object form and the move, remove, set and unset shapes are only in docs/SPEC-NOVA-TABLE.md:350. | Add one example member per change kind and the expect.place object to the help. | S |
| 5 | `nova-table batch --dry-run '{"schema":1,"table":"demo","members":[{"id":"b1"}]}'` | A manifest missing epoch, expected_table_revision and operation_id is refused with only "member entry has no expect record". | Validate the whole manifest and name every problem in one refusal. | S |
| 6 | `nova-table render --view work` (internal/ntable/render.go:522) | The summary line prints `1/4 25.0% -> ETA` with no value after ETA; the code comment says ETA has no value until rate sampling exists. | Print `x/y z%` alone until an ETA can be computed. | S |
| 7 | `nova-table clear -h` (cmd/nova-table/help.go:54) | The page gives no description of what clear removes; on the store it removed both rows and their members (render then shows only the footer). The guide says "clear empties only the active epoch". | Add a line: removes the active epoch's rows and members, keeps the definition and views. | S |
| 8 | cmd/nova-table/help.go:259 | Every verb page says "exit codes: 0 done, 1 refused, 2 usage", but an unreachable or malformed address also exits 2; only `shell -h` says "2 usage/input/connection failure". | Say "2 usage or no store" on every page. | S |
| 9 | `nova-table view set -h` (cmd/nova-table/help.go:266) | The view verbs' effect line ends "what only the store can check (the table, its epoch, its rows and columns, a bound cell) is left to the real run", the table text; a view has no epoch and no bound cell. | Give view writes their own clause (the named tables exist). | S |
| 10 | `nova-table cell add demo build nosuchcol x1` (internal/ntable/store.go:354) | The no-such-row refusal offers `row add`, the no-such-column one only `show`. | Offer `nova-table col add <t> <col>` as the no-such-row refusal offers row add. | S |
| 11 | `nova-table help` (cmd/nova-table/main.go:13) | 127 lines; the example: block that is the reader's first run is last, after the column grammar, ordering, bound cells and shell prose. | Put example: right after the throwaway-store lines and move the grammar prose to `help create`. | S |
| 12 | `nova-table list --redis mem:` | Refused "not an address"; the reads cannot be tried without a Redis binary. | Accept an in-memory store for trial runs, or say in the banner that the reads need a real Redis. | L |

## Good, keep
- Refusals that name the problem, the place it stands and a runnable next command: duplicate member names its cell, stale and future epochs name the active one, `col del` lists every `cell remove` it needs, a standing sort names `row sort --manual`, an unreachable address prints the throwaway-store command.
- `batch --json` receipts with before and after for place, score, member revision and table revision, and a revision mismatch refused with the observed revision.
- The throwaway-store recipe in the banner, which works as printed, and `--dry-run` on every write including the whole shell, printing the exact FCALL with dialled=0 written=0.
- `trips=` on every line, `watch --once` and `watch --out` by atomic rename for scripts.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| USE 1.1.0: reads have no --json | STILL THERE | `show --json` is refused as an unknown flag, and list, check, render, cell members and member find have none; `member read --json` works, and was already in the 1.1.0 build (cmd/nova-table/member.go:132 at c28448a54d56) |
| USE 1.1.0: a manifest is refused one problem at a time | STILL THERE | the same missing-keys manifest is refused with "member entry has no expect record" alone |
| USE 1.1.0: the remedy after drop ping-pongs between create and set | STILL THERE | now tried on a real store: show says create, create says set, set says create (finding 1) |
| USE 1.1.0: no store-less way to run the reads | STILL THERE | `--redis mem:` is refused; the throwaway Redis recipe works as printed |
| READ 1.1.0: the package doc opens on dated quotations | FIXED | internal/ntable/ntable.go:1 opens on "Package ntable is a general, Redis-backed table" |
| READ 1.1.0: the guide opens on quotations | FIXED | docs/nova-table/README.md:3 opens on "## The design" |
| READ 1.1.0: the banner is a wall | STILL THERE | 127 lines, example: last (finding 11) |
| READ 1.1.0: only two of thirty-six verbs take --json | STILL THERE | still member read and batch alone |
