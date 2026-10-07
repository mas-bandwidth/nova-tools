# nova-table dogfood — codex, 2026-10-06

One cold stranger pass of `nova-table` (v1.0.1-0.20261006183932-5844884e267c,
linux/amd64) on a bench, against a throwaway Redis over a Unix socket.
Read only: `nova-table -h`, `nova-table help`, every `nova-table <verb> -h`,
[`docs/nova-table/README.md`](../nova-table/README.md) and the `## nova-table`
section of `docs/CLI.md`. No source was read. Every verb ran at least once with
its real flags, the refusals too.

## Findings

1. **URGENT — `help help` refuses the help it was asked for.**
   Command: `nova-table help help`
   Printed:
   ```
   HELP REFUSED: unknown verb "help"; did you mean help? the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help
   ```
   (exit 2)
   Expected: the usage is `nova-table help [<verb> [<subverb>]]` and requested
   help exits 0, and `help` is listed among the verbs, so `help help` should
   print this door's own help. A refusal that answers "did you mean help?" is
   the tool disagreeing with itself.
   Grade: URGENT.

2. **URGENT — after a plain `drop`, `create` sends the reader to `set`, and `set` refuses.**
   Commands:
   ```
   nova-table create dd --columns a,b
   nova-table drop dd
   nova-table create dd --columns x,y
   nova-table set dd --columns x,y
   ```
   Printed (`create`): `CREATE REFUSED: table "dd": exists with another definition; run: nova-table set 'dd' --columns <columns>`
   Printed (`set`): `SET REFUSED: table "dd": no such table; run: nova-table create 'dd' --columns <columns>`
   Expected: one of the two printed remedies gets the reader to the table they
   want; in fact only `create dd --columns a,b` (the same definition) restores
   it, and `drop dd --definition` is the other way out, neither named. The two
   refusals point at each other and leave no one-turn path.
   Grade: URGENT.

3. **URGENT — `cell remove` from the wrong column reports success and leaves the member placed.**
   Commands:
   ```
   nova-table create rx --columns a,b
   nova-table row add rx r1
   nova-table cell add rx r1 a rx1
   nova-table cell remove rx r1 b rx1
   nova-table member find rx rx1
   ```
   Printed (`cell remove`): `TABLE CELL table=rx row=r1 col=b n=0 trips=1` (exit 0)
   Printed (`member find`): `TABLE MEMBER table=rx member=rx1 state=placed row=r1 col=a epoch=0 table_revision=4 trips=1`
   Expected: a refusal naming the cell `rx1` actually sits in, as `cell move`
   does with `NOTMEMBER`, or the removal itself. The guide says "Removing a
   member clears that table's place field"; a write that exits 0 while leaving
   the place in row `r1` column `a` untouched tells the reader the member is
   gone when it is not.
   Grade: URGENT.

4. **NEXT — duplicate row names are refused with an empty `ROW []`.**
   Command: `nova-table row add crew dup dup`
   Printed:
   ```
   ROW-ADD REFUSED: table "crew": ROW []; run: nova-table show 'crew'
   ```
   Expected: the duplicate named, the way the sibling member refusal does it
   (`duplicate manifest member (TWICE): member d1 appears more than once`).
   `ROW []` is not a sentence and names nothing.
   Grade: NEXT.

5. **NEXT — a relative Unix socket path is refused as "missing port in address".**
   Command: `nova-table list --redis ../scratch/redis.sock`
   Printed:
   ```
   LIST REFUSED: redis at "../scratch/redis.sock" given to this tool: unreachable: not an address: missing port in address; next: give host:port (a port from 1 to 65535) or the absolute path of a Unix socket; for a first try, start a throwaway store and give each verb the --redis this prints; run: d=$(mktemp -d) && '/usr/bin/redis-server' --port 0 --unixsocket "$d/redis.sock" --save '' --appendonly no --daemonize yes && echo "--redis $d/redis.sock"
   ```
   Expected: the reason is "the Unix socket path must be absolute"; the path has
   slashes, so a port was never the missing thing.
   Grade: NEXT.

6. **NEXT — only `batch` and `member read` accept `--json`; the other verbs refuse it.**
   Command: `nova-table show demo --json`
   Printed:
   ```
   SHOW REFUSED: unknown flag --json; the flags of show are --at-epoch, --redis; run: nova-table help show
   ```
   Expected: the tool standard is one value with two renderings, every verb
   accepting `--json`; `list`, `render`, `show` and others refuse it.
   Grade: NEXT.

7. **NEXT — connection flags before the verb are read as an unknown verb.**
   Command: `nova-table --redis <socket> list`
   Printed:
   ```
   TABLE REFUSED: unknown verb "--redis"; the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help
   ```
   Expected: either accept the leading connection flag or say in the help that
   flags come after the verb; the help only says "Flags may follow the words".
   Every `--redis` example in the guide and in `help` puts it last, so a
   stranger who leads with it is refused.
   Grade: NEXT.

8. **NEXT — the nearest-verb suggestion for `cel` is `col`, not `cell`.**
   Command: `nova-table cel add demo build ready b1`
   Printed:
   ```
   TABLE REFUSED: unknown verb "cel"; did you mean col? the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help
   ```
   Expected: `did you mean cell?` — the reader typed three of `cell`'s four
   letters.
   Grade: NEXT.

9. **NEXT — multi-row and row-visibility success lines are not the documented shape.**
   Commands: `nova-table row add crew bench-b ada bob`; `nova-table row hide crew ada`
   Printed:
   ```
   TABLE ROWS ADD table=crew rows=3 trips=1
   TABLE ROWS HIDDEN table=crew rows=1 trips=1
   ```
   Expected: the guide's verb table documents `row add` as
   `TABLE ROW ADD table=<t> row=<r> cols=<n> bound=<n>` and documents no plural
   line; `row show` likewise prints an undocumented `TABLE ROWS SHOWN`, and all
   three drop the `cols=`/`bound=` fields the single-row line carries.
   Grade: NEXT.

10. **NEXT — the top-level `example:` block does not run as printed without a store.**
    Command: `nova-table create demo --columns ready,working,done` (the first line under `example:` in `nova-table -h`)
    Printed:
    ``` CREATE REFUSED: --redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat); for a first try, start a throwaway store and give each verb the --redis this prints; run: d=$(mktemp -d) && '/usr/bin/redis-server' --port 0 --unixsocket "$d/redis.sock" --save '' --appendonly no --daemonize yes && echo "--redis $d/redis.sock" ```
    (exit 2)
    Expected: the onboarding rule is that the example lines run; the
    `--dry-run` line that does run sits in the prose above the block, not in it.
    Grade: NEXT.

11. **NEXT — `shell` refuses to enter without a store, though its own text says help and version need no Redis.**
    Command: `printf 'version\nquit\n' | nova-table shell`
    Printed:
    ``` SHELL REFUSED: --redis <addr> is required (or a configured seat); run: nova-table help shell ```
    Expected: the guide says "help/version need no Redis command" and the usage
    brackets `--redis`, so a storeless shell should at least answer `help` and
    `version` before any store verb.
    Grade: NEXT.

12. **NEXT — an unknown subverb is reported by concatenating the arguments.**
    Command: `nova-table help row bogus`
    Printed:
    ``` HELP REFUSED: unknown verb "row bogus"; the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help ```
    Expected: name the unknown row subverb (`bogus`) and list the row subverbs,
    not a fabricated verb called "row bogus".
    Grade: NEXT.

13. **NEXT — `members`, `first` and `last` over an empty set print an undocumented `-`.**
    Command: `nova-table show proj` (column `m:members`, `f:first`, `l:last`, cell empty)
    Printed:
    ``` TABLE ROW table=proj row=esc\x1bseq a=0 b=0 m=- f=- l=- x=0 p=0.0% q=0.0% ```
    Expected: the render rules define `?` for a set that did not come back and
    quote empty values in `show`; `-` is defined nowhere in the help or the
    guide, so a scripted reader cannot tell "no members" from a member named `-`.
    Grade: NEXT.

## Not done

- `--seat` and a configured seat were not exercised (no seat on the bench);
  every run used `--redis` with an absolute Unix socket.
- A TCP `host:port` store was not used.
- Continuous `watch` ran only under `timeout -s TERM`; the in-place ANSI
  redraw, the `store unreachable since` line and the `--out` failure path were
  not staged.
- `batch` was exercised with one create manifest, its replay and a conflict;
  its `move`, `remove`, `set`, `unset` and `props` members were not.
- Rename with a stored view still naming the old table, and rename into an
  occupied namespace, were not run.
- No ACL-restricted store and no older function library was available, so the
  deploy/upgrade refusals were not reached.
- The named TEST `./internal/docs TestDocsTreeIsConsistent` does not exist at
  this tip; `go test ./internal/docs -run TestDocsTreeIsConsistent` reports
  `[no tests to run]` at exit 0.

READ 7/10 — the banner, the per-verb help, the one-line refusal grammar and the 589-line guide are unusually complete and the epoch and receipt story is stated plainly, but `help help` refusing itself, the `ROW []` line and the missing `--json` hold it back.

USE 6/10 — the happy path is one round trip per verb, receipts and batch replay are honest, and ordering, views and watch all did what the guide says, but a `cell remove` that exits 0 while leaving the member in place, and the `drop`/`create`/`set` dead end, are the kind of trap that makes a builder read the store by hand.

urgent=3 next=10
