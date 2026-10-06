# nova-table READ and USE rating, nova-tools 1.2.0

Rater: Claude Opus 5.5 (claude-opus-5-5), harness Claude Code
Build: 193a6f8002d0
READ: 8/10
USE: 8/10

## Reasons

The build is the sprint base tip 193a6f8002d0, built from source on a Linux
bench; no v1.2.0 tag exists on the remote yet, so the binary's version line
reads `nova-table v1.0.1-0.20261006150140-193a6f8002d0`. Read cold from
`nova-table help`, the `-h` of all 36 verbs, docs/SPEC-NOVA-TABLE.md, the
nova-table section of docs/CLI.md and docs/nova-table/README.md, then used in
a throwaway directory on that bench. No store was started (the card forbids a
server on the machine), so the read verbs, watch and shell were used against
no address, a dead loopback port and a missing socket path; everything that
needs no store ran for real: every write verb under --dry-run, batch --dry-run
in lines and JSON, member read --json, and every refusal class met. The
throwaway directory was empty afterwards.

READ 8. The banner now answers what, how and the first run in its first
eighteen lines, with a store-free example that runs as printed and the
throwaway-store recipe beside it. Every verb's `-h` has usage, a runnable
example, flags grouped as flags, connection and write epoch, and an effect
line. The 1.1.0 READ debts in the code are paid: the package doc and the guide
open with statements, not quotations, the generality ledger for the package is
at 0, and the unreachable helpers in oset.go are gone. What keeps it from 10:
the exit table on the banner and on every verb says `2 usage`, though an
absent or unreachable store also exits 2 and the banner's own prose and the
shell's `-h` say so (finding 1); the banner promises every write prints a
receipt, while `--receipt` is off by default outside the shell and its `-h`
does not say so (finding 6); the views' effect line tells of tables, epochs
and bound cells (finding 9); the banner is still 127 lines, the column grammar
told again in usageDetails (finding 11); two comments still tell tickets
(finding 13); and the general package still imports a card-cost formatter
and describes itself through the sprint's stream (findings 14, 15).

USE 8. Two jobs went end to end with no wrong guess from this tool's help:
planning a board one verb at a time (create, row add, cell add, cell move, row
set, col add, row sort --keep, view set, drop --definition, all under
--dry-run, each one `TABLE DRY-RUN ... dialled=0 written=0` at exit 0), and
planning a batch manifest in lines and in --json. Refusals are one line, name
the bad value and the valid set (`a projection of count, members, ...`, `a
fold of sum, max, ...`, a formula's missing count column with the `set --hide`
hint, a --width column not declared), and a read with no store prints the
whole throwaway-store command with the resolved redis-server path. What keeps
it from 10: contradictory flags are planned, not refused (`set --hidden
--visible`, `set --show c --hide c`; finding 2); `set --rename ''` is silently
no change and refused at exit 1, the store's exit, from a dry run (finding 3);
the dry-run line names positional values `arg1=` to `arg5=` instead of table,
row, col and member (finding 4); `shell --dry-run` refuses without --redis, so
the one verb whose effect line promises a dry session cannot be planned
store-free (finding 5); the reads still take no --json, and no dry run but
batch's does (finding 7); batch still names one manifest problem per run, and
its pre-send refusals split between exit 1 and exit 2 (finding 8); and the
read verbs, watch and shell were judged from their help alone, the store
being forbidden here.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-table/help.go:259 | every verb's `-h` and the banner (main.go:89) say `exit codes: 0 done, 1 refused, 2 usage`; `list` with no --redis and `list --redis 127.0.0.1:1` both exit 2, which the banner's prose (`refuses at exit 2`) and `shell -h` (`2 usage/input/connection failure`) admit, so an AI reading the exit table takes a dead store for its own typo | write `2 could not run (usage, input, no store or an unreachable one)` in the one exit table | S |
| 2 | cmd/nova-table/table.go:109 | `set demo --hidden --visible --dry-run` and `set demo --show nope --hide nope --dry-run` print a plan at exit 0, though the usage writes `[--hidden \| --visible]` as exclusive and a column cannot be hidden and shown in one call | refuse both pairs before the plan, naming the two flags | S |
| 3 | cmd/nova-table/table.go:118 | `set demo --rename '' --dry-run` drops the empty name silently and refuses `set wants a change` at exit 1 (internal/ntable/store.go:934), the store's exit, though nothing was dialled | refuse an empty --rename (and set with no change) at exit 2 as the other usage refusals do | S |
| 4 | cmd/nova-table/write.go:73 | the dry-run line names positionals `arg1=demo arg2=build arg3=ready arg4=b1`, and `row set` prints `arg3=note=hi`; the reader must map positions back to the usage, where batch's dry run (batch.go:238) says `table=` and `operation=` | print the usage's own names (`table= row= col= member=`) for every verb | S |
| 5 | cmd/nova-table/session.go:165 | `shell --dry-run` with a piped script refuses `--redis <addr> is required (or a configured seat)` at exit 2; the shell's effect line (help.go:281) promises that entered with --dry-run every write line is planned, and the shell's usage does not list --dry-run | let a dry shell run with no store (writes planned, a read line refused as no store), and list --dry-run in the usage | M |
| 6 | cmd/nova-table/write.go:25 | `--receipt` says `print the committed event ID, epoch and revision` with no default, the banner (help.go:147) says every write `prints a receipt`, and the effect line says the same (help.go:289); outside the shell the default is off, as the First run transcript shows | state the default in the flag (`off outside the shell`) and say in the banner which line every write prints | S |
| 7 | cmd/nova-table/table.go:528 | `show demo --json` is refused as an unknown flag; list, show, render, check, cell members, view show and view list are prose only, and no --dry-run plan but batch's has --json, while member read and batch give one object | give the reads and the dry-run line --json through the shared result type | M |
| 8 | cmd/nova-table/batch.go:114 | `batch --dry-run '{"schema":1}'` names only the empty table, and a manifest with four bad keys names only the epoch (one problem per run, as in 1.1.0); pre-send refusals with a code (`code=NAME`, `code=OPERATION`) exit 1 while `invalid batch manifest` exits 2 (batch.go:117), both `checked before sending` | check the whole manifest and name every problem in one refusal, at exit 2 | S |
| 9 | cmd/nova-table/help.go:266 | `view set`, `view state` and `view del` end their effect line with `what only the store can check (the table, its epoch, its rows and columns, a bound cell)`, though the same line says a view has no epoch | give the view verbs their own store-check words (the tables the view names) | S |
| 10 | cmd/nova-table/help.go:122 | `nova-table "cell add" -h` answers `unknown verb "cell add"` with the root list, and `help row nosuch` lists the root verbs, not row's; nothing says a verb is two words | match the joined form, and list the parent's verbs in a sub-verb refusal | S |
| 11 | cmd/nova-table/main.go:35 | usageDetails keeps the banner at 127 lines: epoch, receipt, connection, the column grammar, order and shell rules between the usage and the example block, all told again in each verb's `-h`, docs/CLI.md and the spec | keep the banner's answers, usage and example; one sentence and a pointer per subject | M |
| 12 | cmd/nova-table/help.go:79 | `version --json` refuses `takes no arguments`; the input was a flag, and the tool's other verbs say `unknown flag` with the flag list | refuse it as an unknown flag, or give version --json as the other tools do | S |
| 13 | cmd/nova-table/main.go:249 | comments still tell tickets (`nova-tools#4330`, and `#4492` at main.go:359); a cold reader cannot resolve them | state the rule in present tense and drop the ticket numbers | S |
| 14 | internal/ntable/render.go:590 | the general renderer still returns `cardcost.Cents(acc)`, a sprint dependency in a package whose doc says it knows nothing of sprints (1.1.0 READ finding 7) | fold the numbers in the table and let the caller format money | S |
| 15 | internal/ntable/ntable.go:22 | the general package doc explains bound cells and exclude through `the stream block's ws:<s>:<state>` and `the stream's sentinel` | describe a bound cell and an excluded member in general terms; keep the stream as an example in the guide | S |
| 16 | README.md:26 | the trial is a live `create --redis 127.0.0.1:6379` against a store the reader must already run; the banner's own first run is the store-free `create demo --columns ready,working,done --dry-run` | make the trial the dry run, and point at the throwaway-store lines | S |
| 17 | docs/nova-table/README.md:481 | the order example's rows are now role names, but its comment still reads `friends on top, machines keep their order below` | say `people on top, benches keep their order below`, or drop the comment | S |
| 18 | cmd/nova-table/watch.go:48 | `watch demo --out /nonexistent/dir/f --once --redis 127.0.0.1:1` reports only the dead store; the unwritable --out is found after the dial | check --out's directory before dialling and name both problems | S |

## Good, keep

- Every write verb plans under --dry-run with no store: the exact function it would send, `dialled=0 written=0`, at exit 0, and it refuses where the real run refuses before sending.
- Refusals are one line with the valid set and a runnable next command; a read with no store prints the whole throwaway-store command with the resolved redis-server path, and a relative socket path is refused with `give host:port ... or the absolute path of a Unix socket`.
- The column grammar is checked whole before anything is sent: an unknown projection, an unknown fold, a duplicate column, a formula naming an undeclared count column (with the `set --hide` hint) and a --width for an undeclared column were each refused in one line.
- docs/CLI.md's `### First run` is the transcript cmd/nova-table/firstrun_test.go runs, and it says what to do with no store at all.
- member read --json and batch --json give one object for the result and for the refusal alike, with the exit the refusal carried.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| the guide opens on transcribed chat (READ 1.1.0, finding 1) | FIXED | docs/nova-table/README.md:3 opens `## The design` with a statement of the primitive |
| the package doc opens on dated quotations naming a person (READ 1.1.0, finding 2) | FIXED | internal/ntable/ntable.go:1 is present-tense prose; internal/ci/testdata/generality/internal/ntable.txt has ceiling 0 |
| unreachable helpers in oset.go (READ 1.1.0, finding 5) | FIXED | internal/ntable/oset.go keeps members, QueueCount and queueMembers; Add, Remove, Move, Card and MembersOf are gone |
| fleet names as example row keys (READ 1.1.0, finding 6) | CHANGED | docs/nova-table/README.md:475-481 uses bench-a, ada and bob; the comment still speaks of friends and machines (finding 17) |
| the banner is a wall (READ 1.1.0, finding 3) | CHANGED | the first eighteen lines now answer what, how and the first run; usageDetails still makes it 127 lines (finding 11) |
| reads have no --json (READ 1.1.0 finding 4, USE 1.1.0 finding 2) | STILL THERE | `show demo --json` refused `unknown flag --json` (finding 7) |
| batch names one manifest problem per run (USE 1.1.0, finding 3) | STILL THERE | `batch --dry-run '{"schema":1}'` named only the table (finding 8) |
| the renderer imports the card-cost formatter (READ 1.1.0, finding 7) | STILL THERE | internal/ntable/render.go:590 (finding 14) |
| history prose with ticket numbers in comments (READ 1.1.0, finding 8) | STILL THERE | cmd/nova-table/main.go:249 and :359 (finding 13) |
| the README setup sentence hides the library load (READ 1.1.0, README) | CHANGED | README.md:26 says `nova-table loads the functions it needs`; the trial is still a live write (finding 16) |
| no store-free way to run the reads (USE 1.1.0, finding 1) | NOT RE-SEEN | no store was started here; the read refusal now prints a throwaway-store command, and `--redis mem:` was not retried |
