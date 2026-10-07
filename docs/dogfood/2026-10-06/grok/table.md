# nova-table dogfood — grok (zhi), 2026-10-06

Read as a stranger: only `nova-table -h`, `nova-table help`, `nova-table help <verb>`
and the tool's own pages under `cmd/nova-table/README.md` and `docs/nova-table/README.md`,
nothing of its code. Built from the staged checkout at
abb9bfecc72930ef36ce116ac2f652b0e85ad044 and used as
`nova-table v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6`. Every verb ran
at least once with its real flags against a throwaway redis-server whose only endpoint is
a Unix socket in the job's bench run directory (`$S`; `$R` is that directory), the
refusals too: `create`, `set`, `drop`, `list`, `row add/set/hide/show/del/move/order/sort`,
`col add/del/move`, `cell add/remove/move/members`, `member create/find/read`, `batch`,
`check`, `clear`, `show`, `render`, `watch`, `view set/state/show/list/del`, `shell`,
`help` and `version`, at epoch zero and at an advanced epoch, with owned and bound cells,
with a standing sort, and with one corrupted cell. Commands below are as typed;
`nova-table` is written for the binary at its build path in the job's checkout. No code
changed; a finding is recorded, never fixed here.

## Findings

1. A wrong-type owned cell makes the table unrecoverable through the tool, and the one
   breadcrumb it prints is a dead end.

   ```
   nova-table cell add r2 r1 a m1 --redis "$S"
   TABLE CELL table=r2 row=r1 col=a n=1 trips=1
   redis-cli -s "$S" SET table:r2:cell:r1:a oops
   OK
   ```

   `show` prints `a=?` and warns, then every verb that could repair or remove the table
   refuses with an inspection as its remedy:

   ```
   nova-table show r2 --redis "$S"
   TABLE table=r2 columns=1 rows=1 trips=1 epoch=0 revision=3
   TABLE ROW table=r2 row=r1 a=?
   (stderr) nova-table show: warning: ... cannot be read: key table:r2:cell:r1:a is string, expected zset; run: nova-table set -h, and write the cell again

   nova-table cell remove r2 r1 a m1 --redis "$S"
   CELL-REMOVE REFUSED: table "r2" row "r1" column "a" member "m1": WRONGTYPE: key table:r2:cell:r1:a is string, expected zset; run: nova-table show 'r2'
   nova-table cell add r2 r1 a m9 --redis "$S"
   CELL-ADD REFUSED: table "r2" row "r1" column "a" member "m9": WRONGTYPE: key table:r2:cell:r1:a is string, expected zset; run: nova-table show 'r2'
   nova-table set r2 --columns b --redis "$S"
   SET REFUSED: table "r2": WRONGTYPE: key table:r2:cell:r1:a is string, expected zset; run: nova-table show 'r2'
   nova-table clear r2 --redis "$S"
   CLEAR REFUSED: table "r2": WRONGTYPE: key table:r2:cell:r1:a is string, expected zset; run: nova-table show 'r2'
   nova-table drop r2 --redis "$S"
   DROP REFUSED: table "r2": WRONGTYPE: key table:r2:cell:r1:a is string, expected zset; run: nova-table show 'r2'
   nova-table drop r2 --definition --redis "$S"
   DROP REFUSED: table "r2": WRONGTYPE: key table:r2:cell:r1:a is string, expected zset; run: nova-table show 'r2'
   nova-table check r2 --redis "$S"
   CHECK REFUSED: table "r2": WRONGTYPE: key table:r2:cell:r1:a is string, expected zset; run: nova-table show 'r2'
   ```

   `show`'s warning says "write the cell again", but every write path refuses with the
   same WRONGTYPE, and each repair verb names `show` — an inspection — as the remedy.
   The table can never be dropped, cleared or edited again by this tool; its only escape
   is an out-of-band `DEL` the tool never names. Expected a refusal whose remedy reaches:
   a `drop`/`clear` that names the offending key and how to remove it, or a named repair
   path. Grade: URGENT.

2. Only `batch` and `member read` accept `--json`; fourteen other verbs refuse it, and
   `version --json` answers as if a word, not a flag, was given.

   ```
   nova-table show jt --json --redis "$S"
   SHOW REFUSED: unknown flag --json; the flags of show are --at-epoch, --redis; run: nova-table help show
   nova-table version --json
   VERSION REFUSED: takes no arguments; run: nova-table help version
   nova-table render jt --json --redis "$S"
   RENDER REFUSED: unknown flag --json; the flags of render are --at-epoch, --label-width, --redis, --view, --width; run: nova-table help render
   ```

   `nova-table help` lists `--json` under `batch` and `member read` only; `show`, `render`,
   `list`, `check`, `cell members`, `member find`, `view list`, `set`, `create` and the
   rest have none. Expected the house shape: every verb accepts `--json` and renders its
   one result value two ways, so a caller that consumes other tools' JSON does not have
   to parse these lines by hand. Grade: NEXT.

3. `member read --json` prints a custom object, not the house `result`/`facts` envelope
   that `batch --json` prints.

   ```
   nova-table member read work --json check-a --redis "$S"
   {"epoch":"0","members":[{"fields":{},"id":"check-a","member_revision":"2","place":"build:done","score":"1791387930404"}],"missing":[],"table":"work","table_revision":"28","trips":1}
   nova-table batch --json '{"schema":1,"table":"bx","epoch":"0","expected_table_revision":"1","operation_id":"op-2","members":[{"id":"bb2","expect":{"absent":true},"create":{"row":"build","col":"todo","score":0}}]}' --redis "$S"
   {"result":{"verb":"batch","status":"refused","exit":1,"remedy":"nova-table show 'bx'","why":["table \"bx\" batch \"op-2\" row \"build\" member \"bb2\": no such row; code=NOROW; changed=no"]},"facts":{}}
   ```

   The two `--json` renderings disagree on structure: `batch` builds the one documented
   value (`result {verb, status, exit, remedy}`, `facts`), while `member read` emits a
   bare verb-specific object. Expected both from the same value, so the two cannot drift.
   Grade: NEXT.

4. `list` has a total but no bound and no `--max`.

   ```
   nova-table list --max 1 --redis "$S"
   LIST REFUSED: unknown flag --max; the flags of list are --redis; run: nova-table help list
   nova-table list --redis "$S"
   TABLE LIST tables=2 trips=1
   TABLE table=work columns=5 rows=0
   ```

   `list` prints `tables=<n>` and then one line per table, unbounded, while the total is
   already known. Expected the house output rule: a listing cut by `--max` with a
   `MORE shown=<n> total=<n>` line, never silently. Grade: NEXT.

5. Batch manifest-shape errors are usage refusals but exit 1 where the page and every
   verb's help say usage is 2.

   ```
   nova-table batch '{"schema":2}' --redis "$S"
   BATCH REFUSED: schema 2 is not supported, expected 1; checked before sending, so this call changed nothing; it says nothing about an earlier call with the same operation id; code=SCHEMA; changed=no; run: nova-table batch -h
   (exit 1)
   nova-table batch --dry-run '{"schema":1,"table":"bx","epoch":"0"}' --redis "$S"
   BATCH REFUSED: operation_id must be a nonempty string without control characters; checked before sending, so this call changed nothing; ...; code=OPERATION; changed=no; run: nova-table batch -h
   (exit 1)
   nova-table batch '{not json' --redis "$S"
   BATCH REFUSED: invalid batch manifest: invalid character 'n'; checked before sending, ...; changed=no; run: nova-table batch -h
   (exit 2)
   ```

   Every verb's help prints `exit codes: 0 done, 1 refused, 2 usage`, and
   `docs/nova-table/README.md` says "one line on stderr and exit 2 for a usage refusal,
   exit 1 when the store said no"; all three of these are checked before sending, yet
   two exit 1 and one exits 2. A script cannot tell a malformed manifest from the store
   saying no. Expected these pre-send shape errors to exit 2. Grade: NEXT.

6. `set --rename` to a name that is taken blames the source table and gives an unrelated
   remedy.

   ```
   nova-table set m1 --rename m2 --redis "$S"
   SET REFUSED: table "m1": exists with another definition; run: nova-table set 'm1' --columns <columns>
   ```

   `m2` is the table that already exists; the refusal names the source `m1`, and its
   remedy would replace `m1`'s columns, which changes nothing about the collision.
   Expected a refusal naming the destination and a destination remedy (choose another
   name, or rename/drop `m2`). Grade: NEXT.

7. Refusing a nonempty text cell calls it "placed members" and remedies with an
   inspection, where the tool's own page names the clearing command.

   ```
   nova-table col del work note --redis "$S"
   COL-DEL REFUSED: table "work" row "build" column "note": shape would delete or hide placed members: text cell is nonempty; clear it with row set first; run: nova-table show 'work'
   nova-table set work --columns todo,doing,done --redis "$S"
   SET REFUSED: table "work" row "build" column "note": shape would delete or hide placed members: text cell is nonempty; clear it with row set first; run: nova-table show 'work'
   ```

   The reason calls a text value "placed members", and although it says "clear it with row
   set first", the `run:` line sends the reader to `show`, not to the clearing command
   `nova-table row set 'work' 'build' 'note='` that `docs/nova-table/README.md` names for
   exactly this refusal. Expected the reason to name the text value and the remedy to be
   the command that clears it. Grade: NEXT.

8. `member read --cell` reports a formula column as a text column.

   ```
   nova-table member read fx --cell r1:sm --redis "$S"
   MEMBER-READ REFUSED: table "fx" read set row "r1" column "sm": text column holds no ordered set; choose a body column; code=TEXT; changed=no; run: nova-table show 'fx'
   ```

   `sm` is `sum(todo+done)`, a formula over count cells, not a text column; the same
   refusal is right for `note:text` and wrong here. Expected the reason to name the
   column's kind (formula), so a reader knows why there is no set to read. Grade: NEXT.

9. `watch --out` refusals use a private grammar and leak an internal package name.

   ```
   nova-table watch crew --once --out "$R/sym.txt" --redis "$S"
   nova-table watch: --out: atomicfile: target "$R/sym.txt" is a symlink; next: make --out "$R/sym.txt" writable (and its parent directory present and writable), then rerun this watch
   nova-table watch crew --once --out "$R/missingdir/frame.txt" --redis "$S"
   nova-table watch: --out: atomicfile: parent directory for "$R/missingdir/frame.txt": lstat ...: no such file or directory; next: make --out "..." writable ...
   ```

   (both exit 1 on stderr). Every other one-line refusal is `VERB REFUSED: <reason>;
   run: <remedy>`; this family is `nova-table watch:` and names the internal `atomicfile`
   package. Expected the same shape, without the internal name. Grade: NEXT.

10. `help help` refuses and answers "did you mean help?".

    ```
    nova-table help help
    HELP REFUSED: unknown verb "help"; did you mean help? the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help
    ```

    `help` is a listed verb, but asking it about itself is an unknown-verb refusal that
    suggests `help` — a loop. Expected `help help` to print the help banner at exit 0,
    as `help version` does. Grade: NEXT.

11. `help` folds flags into the verb name it reports.

    ```
    nova-table help batch --json --redis "$S"
    HELP REFUSED: unknown verb "batch --json --redis $S"; the verbs are help, create, set, drop, list, row, col, cell, member, batch, check, clear, show, render, watch, view, shell, version; run: nova-table help
    nova-table help create --json --redis "$S"
    HELP REFUSED: unknown verb "create --json --redis $S"; ...
    ```

    A reader who adds a flag to a help request is told the verb does not exist. Expected
    `help` to ignore flags after the verb, or to name only `batch` as the unknown part.
    Grade: NEXT.

12. A repeated `--columns` silently keeps the last value and discards the first.

    ```
    nova-table create rep --columns a,b --columns c --redis "$S"
    TABLE CREATE table=rep columns=1 trips=1
    nova-table show rep --redis "$S"
    TABLE table=rep columns=1 rows=0 trips=1 epoch=0 revision=1
    ```

    The first definition (`a,b`) is dropped with no refusal and no NOTE, so a duplicated
    flag creates a table the caller did not ask for. Expected a refusal naming the
    repeated flag; the tool is meant to refuse to guess. Grade: NEXT.

Right: the core result is correct everywhere it was checked. `create`/`set` edits were
atomic (a late invalid item and a late refusal changed nothing), a repeated identical
`create` was left as a `trips=1` no-op and another definition refused; `show`, `render`,
`member read` and `cell members` agreed; counts, `pct` (`progress` 1/3 = 33.3%) and
`sum(todo+done)` formulas computed as the guide says; `row sort` by name/label and a
standing `--keep` sort with ties by name behaved, and `row move` was refused under a
standing sort naming `row sort --manual`; `--exclude` left a member out of a row's counts
and `cell members` while the set's other members stayed; an advanced epoch refused every
stale write naming the live epoch, and `show`/`render --at-epoch` read materialised
epochs; bound cells refused `cell add`, `cell remove`, `cell move` and whole-table
`clear`, naming the owner, and a bound set survived `drop`; `member find` answered
`placed`, `unplaced` and `missing` at exit 0; `view set/state/--clear/show/list/del`,
`render --view`, `watch --view --once` and `watch --out` (atomic file present) worked, a
view referencing a missing table and a non-count summary refused, and `--hidden` kept a
table out of `watch` while `render` still drew it; `batch` applied in one call, replayed
its operation id byte for byte, refused the same id with a changed manifest, and its
`--json` refusal is the house envelope; the resident `shell` stopped at its first error
with the input line named and `--keep-going` continued; and the missing-store refusal
named the address, the error and a runnable throwaway-store command.

READ 8/10: the banner answers the three questions, every write verb has a store-free
`--dry-run`, the missing-store refusal is a runnable remedy, and the page is deep; but
`list` is unbounded, `help` cannot take a flag and does not know itself, fourteen verbs
have no `--json` and the two that do use two different JSON shapes.

USE 7/10: every verb did its job and the store-side result was correct in every check,
including atomic refusals, the epoch guards and bound cells; but one corrupted cell
dead-ends a table with no in-tool repair, a renamed collision blames the wrong table, and
several refusals name an inspection where a runnable remedy exists.

urgent=1 next=11
