# nova-table READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 in Claude Code, a sprint worker on a friend's re-rate card
Build: c7257c468a7a
READ: 8/10
USE: 7.5/10

No v1.2.0 tag exists on the forge yet, so this rates the release candidate at the head of sprint/mechanical-2026-10-02. `nova-table version` prints `nova-table v1.0.1-0.20261006145440-c7257c468a7a linux/amd64 go1.26.6`. Built and run on a Linux bench machine against a throwaway Redis 8.0.5 on a Unix socket with no TCP port, made inside the job's scratch directory by the recipe the help prints, and shut down after. No live store.

## Reasons
READ. `nova-table help` and every verb's and subverb's `-h` (41 pages) were read cold, then docs/SPEC-NOVA-TABLE.md (863 lines). The banner says what a table is in its first lines, gives a first run that works with no store (`--dry-run`), and prints a throwaway-store recipe that worked as written. Every verb page has the same shape: usage, example, flags, connection, write epoch and receipt, exit codes and an `effect:` line that says whether it writes. The column grammar, the folds, the epoch rule, ordering and the shell are all explained in plain words. The first place of doubting a claim is cmd/nova-table/help.go:147, "Every write names the epoch it read and prints a receipt", repeated on every write verb's `effect:` line (help.go:289) and in cmd/nova-table/main.go:42 ("off the receipt of every write"). A write prints no receipt unless `--receipt` is given. The second is the exit-code line on every page, "0 done, 1 refused, 2 usage", when an unreachable store also exits 2; only the banner text and `shell -h` say so. The first place of confusion is the epoch. The help explains a stale epoch but never says who advances it (whoever writes the `--epoch-key` hash), or that a new epoch starts with no rows: after the epoch moved, `show` printed `rows=0`. Also only in the spec: member records are shared by every table using the default prefix, so writes in one table bump `member_revision` for the same id in another (spec line 77). The first place of boredom is the root help: 65 lines of usageDetails, and the spec repeats the whole `batch -h` page as a code block.

USE. About 110 invocations covered every verb. That included create, set (rename, hide, columns), drop, drop --definition, row add/set/hide/del/move/order/sort (with and without --keep), col add/del/move, cell add/remove/move/members, member create/find/read (with --cell, --json and --at-epoch), batch (create, move, replay, changed-manifest conflict, stale revision, stale member revision, bad score type, missing file, --dry-run and --json), check, clear, show, render (with --view, --width and --label-width), watch (--once, --out, --every, --check), view set/state/show/list/del, and shell from a file and under --dry-run. Formulas computed correctly (sum, pct, pooled footer 60.0% over 3/5). A standing sort refused a hand move and named `row sort --manual`. A stale epoch, a second placement of one member, an operation id reused with another manifest, and a revision mismatch were all refused with `changed=no` and the store left alone. Batch replay returned the recorded receipt with `replay=yes`. A drift I wrote by hand (an owned-set member with no record) was refused by `check` and drawn as a `stall:` row by `watch --check`, with nothing repaired. Unreachable and malformed addresses were refused with the address, the cause and the throwaway recipe.

The score is held at 7.5 by one rendering bug and a cluster of next commands that do not lead anywhere. `render --label-width 3` with labels longer than 3 prints each label at full length and breaks the grid's alignment (internal/ntable/render.go:114 does not clip). A bound cell bound to a key of the wrong type is accepted by `row add`. Then `show` prints `?`, exits 1 and says "run: nova-table set -h, and write the cell again", a cell no table verb may write, then "run: nova-table check", which passes. `clear` refuses the whole table when any row is bound and names the other tool's owner verb as the next step. A second placement and a duplicate `member create` both say "run: nova-table show", which does not list members. The reads still have no `--json`: show, list, cell members, member find, check and view show print text only. Smaller costs: `cell remove` of a member that is not there exits 0 with no count of what was removed. A flag before the verb (`nova-table --redis X list`) is called an unknown verb. The unknown-flag answer leaves `--seat` out of the verb's flags. `show` and `check` print `revision=` where member read, find and batch print `table_revision=`. The view summary ends in a bare `-> ETA` with no estimate. `drop` of a table a view names succeeds silently and the view then refuses to render.

A 10 needs:
- receipts printed by default, or the help saying `--receipt` is needed;
- `--label-width` and `--width` that clip, or that refuse a width smaller than the content;
- a bound key's type checked at `row add`, and next commands that lead to the fix;
- `--json` on every read;
- the epoch's owner, the empty new epoch and the shared member records stated in the help.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/ntable/render.go:114 | `render demo --label-width 3` with labels "Building" and "Testing" prints them at full length; the header separator is 4 wide and every body row shifts, so the grid no longer lines up | clip a label (and a `--width` cell) to the fixed width with a marker, or refuse a width smaller than the widest label | S |
| 2 | cmd/nova-table/help.go:147 | the banner says every write "prints a receipt", as do every write verb's effect: line (help.go:289) and main.go:42; a write prints `TABLE CELL ...` and no `TABLE RECEIPT` unless `--receipt` is given (shell prints one by default) | print the receipt by default outside the shell too, or say "with --receipt" in all three places | S |
| 3 | cmd/nova-table/help.go:259 | every verb page says "exit codes: 0 done, 1 refused, 2 usage", but an unreachable or malformed store address also exits 2 | say "2 usage or store unreachable" on every page, as shell -h already does | S |
| 4 | internal/ntable/store.go:1082 | `row add demo boundrow ready=ext:set` accepts a key holding a plain set; `show` then prints `?` and exits 1, `render` draws `?` and exits 0, and `check` passes | check the bound key's type at row add (absent or zset) and refuse any other type, naming it | S |
| 5 | cmd/nova-table/table.go:339 | show's unreadable-cell warning says "run: nova-table set -h, and write the cell again" for a bound cell no table verb may write, then "run: nova-table check", which passes | for a bound cell, name the owner verb or `row add <table> <row> <col>=<key>` to rebind; send check only when check would report it | S |
| 6 | internal/ntable/store.go:126 | `clear demo` refuses the whole table when one row has a bound cell, and the next step is the other tool's owner verb, which cannot help a clear | clear the owned cells and skip the bound ones, reporting `bound=<n>`; or name `row del` of the bound row as the next step | S |
| 7 | internal/ntable/store.go:272 | the refusals for a placed member (ErrPlaced), an existing member identity (ErrMemberExists) and a check drift all say "run: nova-table show <table>", and show prints counts, not members | name `nova-table member find <table> <id>` (or `cell move` for a placed member) as the next step | S |
| 8 | cmd/nova-table/table.go:314 | the reads have no --json: show, list, cell members, member find, check and view show refuse `--json`; only member read and batch have it | give every read the --json object its line already carries | M |
| 9 | internal/ntable/manifest_validate.go:246 | `batch '{"schema":1,"table":"bt","members":[{"id":"x"}]}'` is refused only for "member entry has no expect record"; epoch, expected_table_revision and operation_id are missing too, so it takes one call per fault | check the whole manifest and name every fault in one refusal | S |
| 10 | internal/ntable/store.go:1255 | `cell remove demo build ready zz` of a member that is not in the cell exits 0 with `n=2` and no word that nothing was removed | print `removed=<n>` (and `absent=<ids>`) on the line | S |
| 11 | cmd/nova-table/help.go:126 | `nova-table --redis 127.0.0.1:1 list` is refused as `unknown verb "--redis"` | when args[0] starts with "-", say flags follow the verb: `nova-table <verb> --redis <addr>` | S |
| 12 | cmd/nova-table/main.go:351 | the unknown-flag answer omits --seat: `list --bogus` says "the flags of list are --redis", `show --json` says "--at-epoch, --redis" | list the connection flags the verb accepts, --seat among them | S |
| 13 | cmd/nova-table/main.go:35 | the help explains a stale epoch but not who advances it (a write to the --epoch-key hash) or that a new epoch starts with no rows (`show` printed rows=0 after the epoch moved) | add two sentences: the epoch is read from --epoch-key; rows and cells are per epoch, so a new epoch starts empty | S |
| 14 | docs/SPEC-NOVA-TABLE.md:77 | member records are shared across tables under the default prefix: q was created by a batch in g1 (member_revision 1), `cell add g2 x a q` in another table moved it to 2, and g1's next batch guarding revision 1 was refused MEMBERREVISION; the help never says so | say it in the help beside --member-prefix, and suggest a per-table prefix where batch guards are used | S |
| 15 | cmd/nova-table/member.go:72 | `check` and `show` (table.go:314) print `revision=`, while member read, member find and batch print `table_revision=`, against the spec's own rule that a revision is labelled for what it counts | print `table_revision=` on show and check | S |
| 16 | internal/ntable/render.go:522 | the view summary prints `0/4 0.0% -> ETA` with nothing after ETA | print an estimate, or `-> ETA -` when there is none, as nova-sprint does | S |
| 17 | internal/ntable/store.go:1042 | `drop t2b` succeeds while view work names it; `render --view work` then refuses for the missing table | refuse the drop naming the views, or report `views=<names>` on the drop line | S |
| 18 | cmd/nova-table/watch.go:323 | a --out with a missing parent prints `nova-table watch: --out: atomicfile: ...` and exits 1, outside the `WATCH REFUSED: ...; run:` grammar every other refusal uses, though it is a usage fault | use the refusal grammar with a runnable `mkdir -p` and exit 2 | S |
| 19 | cmd/nova-table/write.go:73 | dry-run lines name positionals `arg1=demo arg2=build arg3=done arg4=s2` instead of `table= row= col= member=` | name each positional by its usage word | S |
| 20 | cmd/nova-table/main.go:35 | the root help is 65 lines of usageDetails between the usage list and the example, and the spec's batch section repeats the whole `batch -h` page | keep one sentence and a pointer per subject in the banner; drop the copied help page from the spec | M |

## Good, keep
Refusals that name the table, row, column and member, the state expected against the state found, `changed=no`, and one runnable next command; a stale epoch names both epochs.
The throwaway-store recipe in the banner and in every no-store refusal, which worked as printed.
`--dry-run` on every write, printing the exact FCALL with `dialled=0 written=0`, and a shell that refuses `--dry-run=false` inside a dry-run session.
Batch: atomic, replayable by operation id, refusing a changed manifest under a used id, with `--json` receipts and type errors named by their place (`members[0].create.score`).
`watch --check` draws a `stall:` row and never repairs; `col del` names every blocking member and the exact `cell remove` line.
Formula columns with pooled footers that come out right, and a standing sort that refuses a hand move and names `row sort --manual`.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 1.1.0 use: no store-less way to run the reads; mem: refused | CHANGED | still no in-memory store, but the banner and every no-store refusal print a throwaway Redis recipe that ran as written, and every read was used for real here |
| 1.1.0 use and read: the reads have no --json | CHANGED | member read now has --json; show, list, cell members, member find, check and view show still refuse it (finding 8) |
| 1.1.0 use: a manifest with several faults is refused one at a time | STILL THERE | finding 9 |
| 1.1.0 use: the unknown-option refusal is generic | CHANGED | it lists the verb's flags and the nearest, but leaves --seat out (finding 12) |
| 1.1.0 use: write verbs lack --dry-run | FIXED | every write verb's -h lists --dry-run; create, cell add and batch ran under it |
| 1.1.0 read: the guide opens with transcribed design chat | FIXED | docs/nova-table/README.md:3 opens on "The design" in present tense |
| 1.1.0 read: the package doc opens on a dated quotation | FIXED | internal/ntable/ntable.go:1 states the design in its own words |
| 1.1.0 read: about 80 lines of spec prose in the root help | CHANGED | usageDetails is 65 lines (finding 20) |
| 1.1.0 read: the general renderer imports the card-cost formatter | STILL THERE | internal/ntable/render.go:12 imports internal/cardcost |
| 1.1.0 read: the guide's order example uses fleet names | CHANGED | the rows are placeholder names now; docs/nova-table/README.md:481 still comments "friends on top, machines keep their order below" |
