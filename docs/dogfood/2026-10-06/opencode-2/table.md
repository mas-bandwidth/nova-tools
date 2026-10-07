# nova-table dogfood — opencode-2, 2026-10-06

One cold stranger pass of `nova-table`
(v1.0.1-0.20261006183932-5844884e267c, linux/amd64) on a bench, against a
throwaway Redis over a Unix socket (`--redis "$socket"`, `--save ''`,
`--appendonly no`, no seat and no password). Read only: `nova-table -h`,
`nova-table help`, every `nova-table <verb> -h`, and
[docs/nova-table/README.md](../nova-table/README.md). No source was read. Every
verb ran at least once with its real flags against the scratch store, the
refusals too, and the `--dry-run` plan of the write verbs was printed. A sibling
dogfood record was read after the pass only to check whether its defects
reproduce here; every finding below was reproduced with this session's own
commands and its own outputs.

## Findings

1. **URGENT — `cell remove` from a column that does not hold the member exits 0 and leaves the member placed.**
   Commands:
   ```
   nova-table create rx --columns a,b
   nova-table row add rx r1
   nova-table cell add rx r1 a rx1
   nova-table cell remove rx r1 b rx1
   nova-table member find rx rx1
   ```
   Printed (`cell remove`):
   ```
   TABLE CELL table=rx row=r1 col=b n=0 trips=1
   ```
   (exit 0)
   Printed (`member find`):
   ```
   TABLE MEMBER table=rx member=rx1 state=placed row=r1 col=a epoch=0 table_revision=4 trips=1
   ```
   Expected: a refusal naming the cell `rx1` really occupies, as `cell move`
   does with `NOTMEMBER`, or a removal. A write that exits 0 and prints a
   receipt while the member stays in `r1:a` tells the reader the member is gone
   when it is not.
   Grade: URGENT.

2. **URGENT — duplicate row names are refused with an empty `ROW []`.**
   Command: `nova-table row add proj x x`
   Printed:
   ```
   ROW-ADD REFUSED: table "proj": ROW []; run: nova-table show 'proj'
   ```
   Expected: the duplicate named, as the sibling member refusal does
   (`duplicate manifest member (TWICE): member dupe appears more than once`).
   `ROW []` names nothing, and the `show` remedy cannot fix the call.
   Grade: URGENT.

3. **URGENT — `help help` refuses the help it was asked for.**
   Command: `nova-table help help`
   Printed:
   ```
   HELP REFUSED: unknown verb "help"; did you mean help? the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help
   ```
   (exit 2)
   Expected: requested help exits 0 and `help` is listed among the verbs, so
   `nova-table help help` should print this door's own help. A refusal that
   answers "did you mean help?" is the tool disagreeing with itself.
   Grade: URGENT.

4. **URGENT — `render`'s help says an empty table prints nothing; it prints its header, rule and footer.**
   Command: `nova-table render empty1` (table created with `--columns a,b` and no rows)
   Printed:
   ```
   empty1 | a | b
   -------+---+--
          | 0 | 0
   ```
   (exit 0)
   Expected: the help says "render <table> prints the table and nothing else,
   nothing at all when it is empty", while the guide's "empty rule" says a table
   always renders its header, its rule and its footer. The tool follows the
   guide, and the help sentence is false.
   Grade: URGENT.

5. **URGENT — `set --rename` into an occupied name names the source, not the destination, and its remedy cannot complete the rename.**
   Command: `nova-table set nn2 --rename work` (`work` exists with a different definition)
   Printed:
   ```
   SET REFUSED: table "nn2": exists with another definition; run: nova-table set 'nn2' --columns <columns>
   ```
   (exit 1)
   Expected: the occupied destination named (`work` already exists) and a remedy
   that reaches a rename, or a sentence saying the destination must be dropped
   first. The printed sentence is about the source table, which exists by
   definition, and the remedy changes `nn2`'s columns without ever renaming it.
   Grade: URGENT.

6. **URGENT — after a plain `drop`, `create` sends the reader to `set`, and `set` refuses: a two-refusal dead end.**
   Commands:
   ```
   nova-table create dd --columns a,b
   nova-table drop dd
   nova-table create dd --columns x,y
   nova-table set dd --columns x,y
   ```
   Printed (`create`):
   ```
   CREATE REFUSED: table "dd": exists with another definition; run: nova-table set 'dd' --columns <columns>
   ```
   Printed (`set`):
   ```
   SET REFUSED: table "dd": no such table; run: nova-table create 'dd' --columns <columns>
   ```
   Expected: one of the two printed remedies gets the reader to the table they
   want. Neither does; only `create dd --columns a,b` (the old definition) or
   `drop dd --definition` works, and neither is named.
   Grade: URGENT.

7. **NEXT — only `batch` and `member read` accept `--json`; the other verbs refuse the standard flag.**
   Command: `nova-table list --json`
   Printed:
   ```
   LIST REFUSED: unknown flag --json; the flags of list are --redis; run: nova-table help list
   ```
   Expected: the tool standard is one value with two renderings, every verb
   accepting `--json`. `show`, `render`, `check`, `view list`, `create` and the
   rest refuse it too.
   Grade: NEXT.

8. **NEXT — no listing verb accepts `--max`; output cannot be bounded and keeps no total.**
   Command: `nova-table list --max 1`
   Printed:
   ```
   LIST REFUSED: unknown flag --max; the flags of list are --redis; run: nova-table help list
   ```
   Expected: a bound with a `MORE shown=<n> total=<n>` line, the standard's
   listing shape. `list` and `view list` print every row with no cut and no
   widening flag.
   Grade: NEXT.

9. **NEXT — no write verb takes `--op`; `--idem` is recorded but explicitly does not deduplicate.**
   Command: `nova-table cell add proj r doing --op op1 zz`
   Printed:
   ```
   CELL-ADD REFUSED: unknown flag --op; the flags of cell add are --actor, --dry-run, --epoch, --fence, --idem, --receipt, --redis, --score; run: nova-table help cell add
   ```
   Expected: the standard's idempotency flag `--op <id>`, whose repeat returns
   the recorded result. The help instead says `--idem ... does not deduplicate`,
   so a retry after a timeout is the caller's problem.
   Grade: NEXT.

10. **NEXT — `--json` uses two different shapes: a bespoke object on success, the `result`/`facts` envelope on refusal.**
    Command: `nova-table member read proj r --json`
    Printed:
    ```
    {"epoch":"0","members":[],"missing":["r"],"table":"proj","table_revision":"10","trips":1}
    ```
    Printed (`nova-table member read proj --cell r:nope --json`):
    ```
    {"result":{"verb":"member read","status":"refused","exit":1,"remedy":"nova-table show 'proj'","why":["table \"proj\" read set row \"r\" column \"nope\": no such column; code=NOCOL; changed=no"]},"facts":{}}
    ```
    Expected: one structure, so a reader parses success and refusal the same
    way. `batch --json` prints a third shape on success (`{"changed":...}`) and
    the envelope on refusal.
    Grade: NEXT.

11. **NEXT — `watch --out` to a directory or a symlink leaks an internal error and gives a remedy that cannot help.**
    Command: `nova-table watch work --once --out <a directory>`
    Printed:
    ```
    nova-table watch: --out: atomicfile: target "<a directory>" is a directory; next: make --out "<a directory>" writable (and its parent directory present and writable), then rerun this watch
    ```
    (exit 1)
    Expected: the `VERB REFUSED: <reason>; run: <remedy>` grammar, no internal
    `atomicfile:` prefix, and a remedy that fits — the target is a directory (or
    a symlink), not unwritable.
    Grade: NEXT.

12. **NEXT — a stored view accepts the same table twice and draws it twice.**
    Command: `nova-table view set v2 --tables work,work`
    Printed:
    ```
    VIEW SET view=v2 tables=work,work title="" summary="" trips=1
    ```
    (exit 0)
    Expected: a duplicate table refused (or folded to one), the way reference
    validation already runs. `render --view v2` then draws `work` twice in one
    frame.
    Grade: NEXT.

13. **NEXT — `--seat` alone is reported as a missing `--redis`, not as the seat that failed.**
    Command: `nova-table list --seat nosuch`
    Printed:
    ```
    LIST REFUSED: --redis <addr> is required (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat); for a first try, start a throwaway store and give each verb the --redis this prints; run: d=$(mktemp -d) && '/usr/bin/redis-server' --port 0 --unixsocket "$d/redis.sock" --save '' --appendonly no --daemonize yes && echo "--redis $d/redis.sock"
    ```
    (exit 2)
    Expected: the seat problem the same call reports when `--redis` is also
    given (the missing seats file, and the nova-secrets seats that do exist).
    The reader is sent to start a store instead of fixing the seat.
    Grade: NEXT.

14. **NEXT — `member create` with an empty id prints a dangling reason and an unrelated remedy.**
    Command: `nova-table member create work ''`
    Printed:
    ```
    MEMBER-CREATE REFUSED: table "work": member: a member id is a nonempty string without control characters; run: nova-table show 'work'
    ```
    Expected: a clean sentence (the `member:` fragment reads as a broken label)
    and a `run:` that addresses the id, not an inspection of the table.
    Grade: NEXT.

15. **NEXT — `col del` on a nonempty text column names `row set` but not the command, and points `run:` at `show`.**
    Command: `nova-table col del crew note`
    Printed:
    ```
    COL-DEL REFUSED: table "crew" row "bob" column "note": shape would delete or hide placed members: text cell is nonempty; clear it with row set first; run: nova-table show 'crew'
    ```
    Expected: the exact repair, `nova-table row set crew bob note=`, as the
    sibling `col del` refusal does for a formula column (`run: nova-table col del
    'crew' 'share'`).
    Grade: NEXT.

16. **NEXT — `batch -h` names `move`, `remove`, `set` and `unset` members but documents only `create`'s grammar.**
    Command: `nova-table batch -h`
    Printed:
    ```
    usage: nova-table batch (<manifest-file> | - | '<json>')

    example:
    ```
    (the manifest paragraph says "with create, move, remove, set or unset for a
    change" and shows only a `create` example)
    Expected: the key grammar for `move`, `remove`, `set` and `unset`, or a
    pointer to where it is written. A stranger cannot build those members from
    the help.
    Grade: NEXT.

17. **NEXT — `members`, `first` and `last` over an empty set print an undocumented `-` that a member named `-` also prints.**
    Command: `nova-table render mempty` (column `m:members:union`, `f:first:none`, cell empty)
    Printed:
    ```
    mempty | m | f
    -------+---+--
    r      | - | -
    ```
    Printed after `nova-table cell add mempty r m -- -`: the same `-`. Expected:
    the render rules define `?` for a set that did not come back and quote empty
    values in `show`; `-` is defined nowhere, so a scripted reader cannot tell
    "no members" from a member named `-`.
    Grade: NEXT.

18. **NEXT — connection flags before the verb are read as an unknown verb.**
    Command: `nova-table --redis "$socket" list`
    Printed:
    ```
    TABLE REFUSED: unknown verb "--redis"; the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help
    ```
    Expected: either the leading flag accepted, or the help saying plainly that
    flags follow the verb. The help only offers "Flags may follow the words",
    and every example puts `--redis` last.
    Grade: NEXT.

19. **NEXT — `shell` refuses to enter without a store, though its own text says `help` and `version` need no Redis.**
    Command: `printf 'version\nquit\n' | nova-table shell`
    Printed:
    ```
    SHELL REFUSED: --redis <addr> is required (or a configured seat); run: nova-table help shell
    ```
    (exit 2)
    Expected: the guide says "help/version need no Redis command" and the usage
    brackets `--redis`, so a storeless shell should answer those before any
    store verb.
    Grade: NEXT.

20. **NEXT — the multi-row and row-visibility success lines are not the documented shape.**
    Commands: `nova-table row add crew bench-b ada bob`; `nova-table row hide crew ada`
    Printed:
    ```
    TABLE ROWS ADD table=crew rows=3 trips=1
    TABLE ROWS HIDDEN table=crew rows=1 trips=1
    ```
    Expected: the guide documents `row add` as `TABLE ROW ADD table=<t> row=<r>
    cols=<n> bound=<n>` and documents no plural line; `row show` likewise prints
    an undocumented `TABLE ROWS SHOWN`, and all three drop the `cols=`/`bound=`
    fields the single-row line carries.
    Grade: NEXT.

## Not done

- `--seat` with a configured, working seat was not exercised (no seat on the
  bench); only the unknown-seat refusal and the `--redis`/environment
  precedence were run.
- A TCP `host:port` store and an ACL-restricted store were not used.
- Continuous `watch` ran only under a signal; the in-place ANSI redraw and the
  `store unreachable since <time>` line were not staged.
- `batch` was exercised with a create manifest, its replay, a conflict and
  `--json`; its `move`, `remove`, `set` and `unset` members were not, because
  their grammar is not in the help.
- Rename with a stored view naming the old table, and `drop` past 1000 epochs,
  were not run.
- The named TEST `./internal/docs TestDocsTreeIsConsistent` does not exist at
  this tip; `go test ./internal/docs -run TestDocsTreeIsConsistent` reports
  `[no tests to run]` at exit 0.

READ 7/10 — the banner, the per-verb help, the guide and the epoch, receipt and
dry-run story are unusually complete, but `help help` refuses itself, the
`render` help contradicts the guide on an empty table, and the batch manifest
grammar stops at `create`.

USE 6/10 — the happy path is one round trip per verb, batch replay and epochs
are honest, and ordering, views and watch did what the guide says, but a
`cell remove` in the wrong column exits 0 while leaving the member placed, and
the `drop`/`create` and rename refusals leave no one-turn path.

urgent=6 next=14
