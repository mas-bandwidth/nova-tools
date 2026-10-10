# nova-table: work tables and live views over Redis

## The design

The table's primitive is the ordered set. A work stream table is a series of
ordered sets, one per cell, and the value a cell prints is by default that
set's cardinality, `|s|`. Header cells label the columns and the rows; footer
cells fold the column above them, a sum or another function of its cells. The
table renders to text efficiently and mechanically, once per second in a
console window, and the frame holds the table data alone: nothing around it.

## Start locally

`nova-table help` lists every command. `nova-table help row`, `nova-table row
--help`, and `nova-table row help` list row operations. For syntax, real flags
and an example, use `nova-table help row set` or `nova-table row set --help`.
Requested help exits 0 on stdout and needs no store.

Install `nova-table` and Redis 7 or later. The image the functional tier runs in
(`make test-functional-container`) builds Redis 8.10.2. Every table verb calls
the `nova_sprint` function library (`pkg/nsprint/fn`), which `nova-table`
carries. On first contact with a store that holds no library, `nova-table`
loads its own: the first verb the store answers `Function not found` loads the
library once per process, never replacing one the store holds, and runs again;
that verb's `trips=` counts the load. An empty Redis is enough.
`nova-redis fn load` is the explicit load and upgrade, for the one place that
deploys, and `nova-redis fn check` says whether a store holds this build's
library. From a source checkout, build the client:

```sh
go build -o ./nova-table ./cmd/nova-table
```

This creates an isolated local store with no TCP listener or saved data. The
wrapper clears seat selection and authentication only for these local calls:

```sh
table_demo_dir=$(mktemp -d "${TMPDIR:-/tmp}/nova-table.XXXXXX")
redis-server --port 0 --unixsocket "$table_demo_dir/redis.sock" \
  --unixsocketperm 700 --save '' --appendonly no --daemonize yes \
  --pidfile "$table_demo_dir/redis.pid" --logfile "$table_demo_dir/redis.log"
for _ in $(seq 50); do redis-cli -s "$table_demo_dir/redis.sock" ping >/dev/null 2>&1 && break; sleep 0.1; done
local_table_store() {
  env -u NOVA_SEAT -u NOVA_SPRINT_SEAT -u NOVA_SPRINT_REDIS_USER \
    -u NOVA_SPRINT_REDIS_PASSWORD_ENV -u NOVA_REDIS_BENCH_PASSWORD \
    "$@" --redis "$table_demo_dir/redis.sock"
}
local_table_store ./nova-table create work --columns 'todo,doing,done,note:text,progress:pct(done)' --footer total
local_table_store ./nova-table row add work build docs
local_table_store ./nova-table cell add work build todo check-a check-b
local_table_store ./nova-table cell move work build todo done check-a
local_table_store ./nova-table row set work build 'note=One check passed'
local_table_store ./nova-table member find work check-a
local_table_store ./nova-table view set today --tables work --title 'My work' --summary done
local_table_store ./nova-table watch --view today --once
```

Quote column specs containing parentheses, especially in zsh. `pct(done)` means
the count in `done` divided by all count columns in that row. Here the build
row and the view summary both show 50.0%. Text notes and formula columns do not
add to that denominator. The empty docs row shows 0.0%.

Run `local_table_store ./nova-table watch --view today` to keep it live. Use
another terminal with the same socket path to edit the table or the view; the
next frame picks up those edits. Ctrl-C ends watch. When finished, shut down
only this disposable store:

```sh
redis-cli -s "$table_demo_dir/redis.sock" shutdown nosave
```

For an existing configured store, use `--seat <name>` or explicit `--redis
<host:port>`. An absolute Unix socket path is also accepted. A store whose
library is an older build than this `nova-table` refuses a verb whose function
it lacks, naming `nova-redis fn load`, the deployer's upgrade; the local setup
above is for a new disposable store. The commands below omit connection flags.

## What a table is

The primitive is the ordered set: a Redis ZSET, members with scores, read in
score order. `pkg/ntable` gives it five verbs (`Add`, `Remove`, `Move`,
`Members`, `Card`) and one count (`QueueCount`: the set's size, one member
left out when a row names it) and knows nothing about sprints.

A table is ordered columns and rows. Set cells, text values and formulas have three
kinds of cell:

- **Header cells.** The top row is the column labels (each column has a
  label, default its name); the left column is the row labels (each row has
  a label, default its key), in front of every declared column. Labels, not
  sets.
- **Body cells.** A set cell is printed by its column's
  *projection*: `count` (the set's size, |s|; the default),
  `members` (the members in score order, comma-joined), `first` and `last`
  (the lowest and highest scored member), or `text` (a value per row, set
  by `row set`, blank when none; no set). `pct(<count-column>)`,
  `pct(<count-column>/<a>+<b>)` and `sum(<a>+<b>)` are formulas over count
  cells, with no set of their own.
- **Footer cells.** One per column, the column's *fold* over the body:
  `sum` (the default for a count and a `sum(...)`), `max` or `avg` of the counts, `union` of the
  members, `pooled` for percentages, or `none` (blank). The footer row carries the table's footer
  label (blank by default; use `--footer total` to name it). A table whose columns all fold `none` prints no
  footer row.

A cell's set is either **owned** by the table, at
`table:<t>:cell:<row>:<col>`, and written by the cell verbs, or **bound** to
a set another tool owns. A bound cell is a view: read and rendered freely,
never written here. `cell add`, `cell remove`, `cell move` and `clear`
refuse it in one line naming the set and the verb that owns it
(`<t>.<row>.<col> is bound to <key>, owned elsewhere; run: <owner verb>`),
and `clear` over a table with any bound cell refuses whole and clears
nothing. This is the one-writer principle: a set has one writer, and a
table that shows it does not become a second.

Row keys are nonempty UTF-8 strings without ASCII control characters. Invalid
bytes are refused before JSON encoding can change an identity. Raw `Bind`
requests must supply `rows` as a JSON array, including `[]` for no rows; an
object is refused without changing the table.

A row may name one member its counts and members leave out (`--exclude`).
The sprint's stream sentinel is the case: the stream's stop, not work.

## Member identity, placement and epochs

An owned member has at most one place in a logical table, across all rows and
columns. The same record may have a place in another table. `cell add` creates
a missing record atomically with its placement; a repeated add refuses, even
in the same cell, preserving the existing score. `cell move` moves between
columns of one row and retains the score. Removing a member clears that
table's place field and retains its identity. `member create` makes an unplaced
record and refuses an existing ID; removal never permits ID reuse as a new
record. A removed record may be placed again in its original epoch.

Records default to `table::member:<id>`, with immutable `epoch` and a
`place:<table>` field holding `row:column`. A definition's `--member-prefix`
can select an existing namespace, such as `task:`. Existing unrelated hash
fields survive; absent epoch on a record means zero. Generic task
create/move fields cannot write `place:*`. Other record owners must likewise
reserve those fields and preserve identity. Table operations do not implement
the task/card lifecycle or protect against out-of-band raw Redis writes.

`row add` and module `Bind` refuse a shape edit that would hide or delete a
placed owned member. Their refusal identifies the occupied cell and members;
move or explicitly remove them first. A binding cannot name any `table:*`
storage, including a future cell, epoch or metadata key. External bindings
remain live views of their owner's data, including on historical reads.

A definition with `--epoch-key <hash>` uses its `--epoch-field` (default `n`)
as the shared epoch domain; no key means epoch zero permanently. The domain
owner must advance epochs monotonically. Every mutation carries the epoch the
caller observed (`--epoch`, default zero). A stale write refuses unchanged;
it is never retried in the new epoch. Members from an older epoch cannot be
placed in the current one. Epochs and revisions use exact unsigned decimal
integers, including values beyond floating-point integer precision.

Columns and footer form a stable template. Presence, rows, metadata and owned
cells belong to one epoch. Advancing the domain exposes that template with
empty rows while preserving historical data and member links. `clear` empties
only the active epoch. `drop` removes its active rows and owned cells but keeps
the saved column definition, which is reused in later epochs. `drop --definition`
also removes that saved definition, the identity hash and the rows of every
epoch (with their owned cells and properties, the members' places unset), in the
same call, and is refused beyond 1000 epochs. The definition snapshots of
earlier epochs remain, and `show`/`render --at-epoch <n>` (module `ReadAt`)
read them, with no rows. While the table exists, an identity hash retains the
epoch domain and member prefix, preventing template recreation from silently
reassigning old records to a different namespace. A table created again after
`drop --definition` has its own configuration and no rows, and its revision
counter and change log continue from the dropped table's (the change log is
untrimmed, so a reader chains across the drop). A `create` refuses, naming the
keys, when the rows or properties of an earlier table are left at the epoch it
opens at. A store that holds the identity hash of a table that is gone (an
earlier build's `drop --definition` kept it) says so on every verb, `show`
included, and a `create` with another configuration says so too; the line
carries `nova-table drop <table> --definition`, with `--epoch <n>` at the epoch
the store is at, and that removes it. `show` reports epoch and
revision; `render` and `watch` retain their plain table display.

`check` verifies both directions of record/set membership, duplicate places,
and hidden owned cells in one store instant. The runtime assumes valid initial
state for its preservation guarantees. Legacy owned cells without record
links, or hidden under old bindings, require a separately reviewed migration;
operations refuse detected drift rather than silently inventing identity.

## Change receipts

Every accepted mutation, including an accepted no-op, increments the table's
revision once and appends one event to `table:<t>:changes`. Refusals change no
record, cell, revision or event. The event is the receipt: verb, original call
arguments (JSON, excluding WriteOptions), epoch, `rev_before`, `rev_after`,
actor, fence, idem, affected cells (`row:column`), member transitions
(`id`, `from`, `to`, score), and outcome (`changed` or `noop`). Empty endpoints
are empty strings; empty change lists are JSON arrays.

Writes accept `--actor`, `--fence`, `--idem` and `--receipt`. The last prints the
committed event ID and revision without a second store call. Go callers pass
`WriteOptions{Epoch, Actor, Fence, Idem, Receipt: &receipt}`. Actor, fence and
idem are recorded metadata here: authorization, lease fencing and retry
deduplication belong to the coordinator layer. The primitive makes no claim
that a repeated attempt with the same idem value is deduplicated.

Streams are untrimmed in this foundation. Future trimming needs an archive,
an acknowledgement watermark covering every registered consumer group, and
explicit gap/replay handling. The function validates input, key types, counter
bounds and every staged command's ACL before writing; it appends the event
last. Redis functions do not provide rollback after resource exhaustion or
server failure; this is not a claim of universal error rollback.

## The keys

All under one prefix, so one ACL pattern grants them (`~table:*`), plus the
registry set `tables`:

```
tables                     SET   every table name
table:<t>                  HASH  order (the column names, comma-joined),
                                 footer (the footer label), created_at,
                                 col:<name> = <projection>:<fold>:<width>:<label>
table:<t>:rows             ZSET  row key -> rank: the render order
table:<t>:row:<r>          HASH  label, exclude, owner, key:<col> (a bound
                                 cell's set; absent: the owned cell)
table:<t>:cell:<r>:<c>     ZSET  an owned cell (epoch zero)
table:<t>:<e>:rows/row:/cell:     the same epoch-local keys for e > 0
table:<t>[:<e>]:definition HASH  retained definition, _present, _revision
table:<t>:identity         HASH  epoch_key, epoch_field, member_prefix; fixed while the table exists, removed by drop --definition
table:<t>:revision         HASH  n (revision, retained across epochs/drop)
table:<t>:changes        STREAM  untrimmed committed change receipts
table::member:<id>         HASH  epoch, place:<table> and caller-owned metadata
```

Every table operation takes **one round trip after connecting**, including
cold reads and reads after row or binding changes. Writes validate and
mutate atomically in the `ns_table_*` functions in the shared Redis library.
`ns_table_read`, `ns_table_list`, and `ns_table_members` are read-only
functions (`FCALL_RO`). A multi-table watch pipelines one snapshot function
per table in one exchange, on the first tick as well as later ticks. Normal reads and member writes use neither `KEYS` nor `SCAN`. Rename scans the source table and member namespaces to preserve retained history and placement links. The explicit
maintenance `check` scans the member namespace and current owned-cell keys
inside one read-only function; its cost scales with that namespace.

The typed success receipt includes `trips=1`, measured by `store.CountTrips`.
`render` and `watch` print only their table text; their trip budget is checked
by the module counter and local MONITOR regression, without adding metadata
to the display. Connection setup is excluded from the application-trip count.

## The verbs

Every store verb takes `--redis <addr>`, else `NOVA_SPRINT_REDIS`, then
`NOVA_REDIS_ADDR`, then the seat's address, and dials as the seat. Flags may follow the words; `--` ends flag parsing so a member such as
`--pending` can be passed literally. Unknown flags name the command's available
flags and its specific help page. One typed line per success on stdout;
one line on stderr and exit 2 for a usage refusal, exit 1 when the store
said no.

```
nova-table shell [--redis <addr> | --seat <name>] [--keep-going] [--epoch <n>] [--receipt=false]
nova-table create <table> --columns <name[:projection[:fold[:label]]],...> [--footer <label>] [--width <col=n,...>]
nova-table set <table> [--footer <label>] [--rename <name>] [--columns <spec>] [--hide <cols>] [--show <cols>] [--hidden | --visible]
nova-table drop <table> [--definition]
nova-table list
nova-table row add <table> <row> [--label <text>] [--exclude <member>] [--owner <verb>] [<col>=<key> ...]
nova-table row add <table> <row> <row> ...
nova-table row set <table> <row> <col>=<value> ...
nova-table row hide <table> <row> ...
nova-table row show <table> <row> ...
nova-table row del <table> <row>
nova-table row move <table> <row> --first | --last | --before <row> | --after <row>
nova-table row order <table> <row> <row> ...
nova-table row sort <table> [--by name|label|<col>] [--desc] [--keep]
nova-table row sort <table> --manual
nova-table col add <table> <name[:projection[:fold[:label]]]> [--first | --last | --before <col> | --after <col>]
nova-table col del <table> <col>
nova-table col move <table> <col> --first | --last | --before <col> | --after <col>
nova-table cell add <table> <row> <col> <member>... [--score <n>]
nova-table cell remove <table> <row> <col> <member>...
nova-table cell move <table> <row> <from-col> <to-col> <member>...
nova-table cell members <table> <row> <col>
nova-table member create <table> <id>
nova-table member find <table> <id>
nova-table member read <table> <id>... | <table> --cell <row:col>
nova-table check <table>
nova-table clear <table>
nova-table show <table> [--at-epoch <n>]
nova-table render <table> [--at-epoch <n>] [--width <col=n,...>] [--label-width <n>]
nova-table render --view <name> [--width <col=n,...>] [--label-width <n>]
nova-table view set <name> --tables <a,b,...> [--title <text>] [--summary <count-column>]
nova-table view state <name> (<text> | --clear)
nova-table view show <name>
nova-table view list
nova-table view del <name>
nova-table watch <table>[,<table>...] | --view <name> [--every <duration>] [--out <file>] [--title <text>] [--width <col=n,...>] [--label-width <n>] [--once]
```

| verb | prints |
| --- | --- |
| `create` | `TABLE CREATE table=<t> columns=<n>`; an existing table with the same definition is left; another definition is refused |
| `drop` | `TABLE DROP table=<t> rows=<n>`; active rows and owned cells go; the saved column definition and the identity hash stay unless `--definition`, which removes both, the rows of every epoch and the operation records; the revision counter, the change log, the definition snapshots of earlier epochs and external bound sets stay |
| `list` | `TABLE LIST tables=<n>`, then `TABLE table=<t> columns=<n> rows=<n>` per table |
| `row add` | `TABLE ROW ADD table=<t> row=<r> cols=<n> bound=<n>`; a row already there keeps its place and its cells; a binding wants `--owner` |
| `row del` | `TABLE ROW DEL table=<t> row=<r> existed=<0\|1>`; its owned cells go with it; a missing row succeeds with `existed=0` and a no-op receipt |
| `row move` | `TABLE ROW MOVE table=<t> row=<r> place=<first\|last\|before\|after> [of=<row>]`; the other rows keep their order |
| `row order` | `TABLE ROW ORDER table=<t> first=<r,r,...>`; the named rows first, in the order named; the rest follow in theirs |
| `row sort` | `TABLE ROW SORT table=<t> by=<key> desc=<bool> keep=<bool>`, or `manual=true` |
| `col add` | `TABLE COL ADD table=<t> col=<c> place=<...>`; last unless a place is named |
| `col del` | `TABLE COL DEL table=<t> col=<c>`; refused while the column holds a member or a text value, or a formula reads it |
| `col move` | `TABLE COL MOVE table=<t> col=<c> place=<...>`; the other columns keep their order |
| `cell add`, `cell remove` | `TABLE CELL table=<t> row=<r> col=<c> n=<count after>`; `--score` is the member's place (the unix ms when omitted) |
| `cell move` | `TABLE MOVE table=<t> row=<r> member=<m> from=<c> to=<c> n=<count of to>`; one call, the score kept; `NOTMEMBER` refused |
| `cell members` | `TABLE CELL ... n=<n>`, then `TABLE MEMBER table=<t> row=<r> col=<c> member=<m> score=<s>` per member |
| `clear` | `TABLE CLEAR table=<t> rows=<n> ms=<n>`; one call; the definition stays |
| `show` | `TABLE table=<t> columns=<n> rows=<n> trips=1 epoch=<n> revision=<n>`, then `TABLE ROW table=<t> row=<r> <col>=<projected-value> ...` per row, including hidden rows/columns, text, members and percentages |
| `render` | table text, empty when the table is empty; `--view` prints one stored-view frame with timestamp, title and summary |
| `watch` | the text, once per tick, in place or to `--out` |

Empty values and values holding spaces or quotes are quoted, `note=""`,
`row="swarm: cards"`. Counts stay numeric; percentages have one decimal and `%`.
`show` uses the same full projected cell values as `render`, with `?` for unknown
inputs. It is an unpadded record of every row and column.

`member find` answers where an identity is placed in this table, with
`state=placed row=<r> col=<c>`, `state=unplaced`, or `state=missing`. All three are
successful reads (exit 0), with epoch, `table_revision` and `trips=1`. The read checks
that a reported placement is present in its owned set; disagreement refuses as
drift. It does not search bound external sets. An identity from another epoch
refuses with the two epochs. Use `check` for a full audit of all record/set links.

`member read` reads members in one exchange: their place, score, member revision and
fields, the members that do not exist, and the table's revision and epoch (see
`docs/CLI.md`, Reading members).
A custom member prefix needs read access to that namespace.

`view list` lists stored view names in lexical order. `view show` prints the
configuration, including its summary column; `render --view <name>` renders it
once, as does `watch --view <name> --once`. `--at-epoch` is for table targets;
stored views read active epochs. `view del` deletes only that configuration and reports `existed=0|1`; tables
and their receipts remain. Dropping or renaming a table does not rewrite a view;
edit or delete the reference explicitly. A view summary names a **count** column
in its first table, such as `done`, not a `pct(done)` formula.

`view set` replaces title and summary together, so one left out is cleared.
Every table of a view is drawn, and every row of it, empty or not.

`view state <name> <text>` gives a view a state: while it has one, the summary
line is that text alone, in place of the counts, the percent and the ETA, and
`--clear` removes it so the counts show again. A state is one line of at most
64 bytes. `view set` leaves a view's state as it is; `view show` prints it. A
tool that fills a view writes its state with its own record in one
transaction (`ntable.QueueViewState`), so the two never disagree:
`nova-sprint` writes `STOPPED` with the machine's state record on `stop`,
`clear` and `init`, and clears it on `start`.

## Editing, batches and rename

`set` validates the entire definition edit before writing. Removing a nonempty
owned set, changing it to text or a formula, or removing nonempty text is refused;
move/remove members or clear text first. Bound external sets remain untouched.
`row set` writes text values stored by column; later row metadata or binding
edits retain those text values and row visibility. `pct(<count-column>)` computes
the named count divided by all count columns in the row; `pct(<count-column>/<a>+<b>)`
the named count divided by the named count columns `a`, `b` of the row, for example
`okpct:pct(ok/ok+failed):pooled:ok%`; `sum(<a>+<b>)` the named count columns of the
row added, for example `done:sum(ok+failed)`. Every column a formula names is a
count column of the table, hidden or not. A percentage's default footer is
`pooled`: sum the numerators and the denominators first, then divide. A percentage cannot fold `avg`.
A stored definition with the former `pct:avg` rule can be repaired using
`set --columns`; replacement columns are validated under the current rules.

`cell add/remove/move` accept lists of members. `row add` accepts multiple row
names with a shared metadata specification, and `row hide/show` accepts lists.
Each table call validates every item, checks its observed epoch, and commits
one revision and one receipt for the whole accepted list. Duplicate members or
row names within one list are refused. A late invalid item leaves the complete
store unchanged, including receipts. Go's single-member helpers delegate to
`CellsAdd`, `CellsRemove`, and `CellsMove` with a one-element list; their optional
`WriteOptions` remain available.

`set --hide/--show` applies column visibility changes on the server in one call,
without a read-modify-write race. Hidden rows and columns still contribute to
formulas and folds. `set --hidden/--visible` controls whether watch draws the
whole table. Every table edit uses the epoch and receipt options described above.

Rename moves the stable definition, permanent identity, all materialized epochs,
revision counter, change stream and its consumer groups. It updates every retained
member's `place:<table>` field. A destination with any existing table namespace
keys, including old history, is refused. The final receipt is written at the
new name and records all physical key moves in `renamed_keys`; prior stream
events remain byte-for-byte the same. Consumers must switch to the new name.
Rename does not create an alias or rewrite stored views referencing the former name.

The raw function wire ends every table write with the JSON options object.
`set` takes `name, editJSON, optionsJSON`; `row_set` takes
`name, row, valuesJSON, optionsJSON`; `rows_add` takes
`name, {"rows":[...],"spec":{...}}, optionsJSON`; `rows_hide` takes
`name, 0|1, rowsJSON, optionsJSON`. Cell add takes
`name, row, col, score, members..., optionsJSON`; remove takes
`name, row, col, members..., optionsJSON`; move takes
`name, row, from, to, members..., optionsJSON`. Deploy the rebuilt clients,
function library and source ACL declarations together; the older no-options
wire is refused.

Stored views are presentation configuration, separate from table epoch receipts.
A view write validates all references and command permissions before either its
hash or registry is changed. `watch --view` reloads the view each frame and reads
its tables in one pipeline: two application exchanges, including a summary.
The timestamp, title, summary line and tables form the frame. The summary line is
the view's state alone while it has one, and otherwise the pooled summary, which uses
the same table snapshot as the body; unread inputs print `?`. ETA has no value
until change-stream rate sampling is implemented. Edit a view to change its
tables or title without restarting watch.

## Resident shell

`nova-table shell` reads commands from stdin and reuses one store connection.
Choose `--seat <name>` or `--redis <addr>` when entering. The seat is resolved
once; later commands keep that connection and use the same handlers, help and
output as separate invocations. Ordinary table verbs use one application
exchange each; a stored-view frame uses two. Multi-table watch reads remain
pipelined. Lines run in order and commit independently.

```sh
nova-table shell --redis localhost:6379 <<'TABLE'
create session-demo --columns 'ready,working,done,note:text,pct:pct(done)'
row add session-demo 'the tests'
cell add session-demo 'the tests' ready check-1 check-2
cell move session-demo 'the tests' ready working check-1 check-2
row set session-demo 'the tests' 'note=Running both checks'
watch session-demo --once
check session-demo
quit
TABLE
```

Enter just the verb, or paste a full `nova-table ...` command. Single/double
quotes and backslash escapes keep a value together. Blank lines and `#`
comments are skipped. Variables, globs and command substitutions remain literal;
pipes and command separators must be quoted when they are part of a value.
Use one complete command per line, up to exactly 1 MiB (1,048,576 bytes),
excluding the LF or CRLF delimiter. `help row move`, for example,
shows the same help as outside the session.

Writes print `TABLE RECEIPT` by default. Session flags `--epoch`, `--actor`,
`--fence` and `--idem` supply defaults; a command can override them without
changing the following command's defaults. `--receipt=false` suppresses receipt
lines for the whole session or one command. A stale epoch refuses the write;
the shell does not advance it automatically. A command cannot switch to another
store or select another seat inside the session.

File/pipe input stops at the first error. `--keep-going` continues and retains
the final failure status; a terminal defaults to this behavior and prints
`nova-table> ` on stderr. Exit is 0 for success, 1 if any store call refused, or
2 if any usage/input/connection error occurred. Earlier successful lines remain
committed. `quit`, `exit` or EOF ends the session. `watch` runs in it too: use
`--once` for a script, or Ctrl-C to stop a continuous watch and return to the
prompt. A script can also use `watch --out <path>` to publish each frame.
On Unix, SIGTERM terminates the whole shell (status 143), including during watch;
Ctrl-C at the prompt terminates it (status 130). No later line starts. A write
already sent may have committed even when its reply was not received.

With `--keep-going`, an overlong line is refused and discarded through its
newline, then the next line is read. Other input read errors end the session.
Command failures name the input line on stderr. After a connection failure,
the next store command gets a fresh client at the pinned address and seat;
the shell never replays the failed command. Healthy commands reuse their one
connection, and help/version need no Redis command.

## Order

Order is state the table keeps. Rows draw in the order they were added and
columns in the order they were declared, until a verb moves them. Every order
verb is one call to the staged `set` kernel: checked whole, then written, one
receipt; a refusal writes nothing. A move to where the thing already is
leaves a `noop` receipt.

```
$ nova-table render crew
crew    | busy | idle | note
--------+------+------+-----
bench-a |    1 |    0 |
bench-b |    0 |    0 |
ada     |    0 |    0 |
bob     |    0 |    0 | here
$ nova-table row order crew bob ada        # friends on top, machines keep their order below
$ nova-table row move crew bench-b --before bench-a
$ nova-table col move crew note --first
$ nova-table col add crew 'share:pct(busy)' --after busy
```

| you want | run |
| --- | --- |
| one row or column somewhere else | `row move` / `col move` with `--first`, `--last`, `--before <x>` or `--after <x>` |
| some rows on top, in a given order | `row order <table> <row> <row> ...` |
| all rows sorted once | `row sort <table> --by name` (or `label`, a count column, a text column; `--desc` reverses; ties go by name) |
| rows kept sorted as rows arrive | `row sort <table> --by name --keep` (name or label) |
| to place rows by hand again | `row sort <table> --manual` |
| one more column | `col add <table> <spec>`, with a place or last |
| one column gone | `col del <table> <col>` |

`show` includes `sort=name`, `sort=label` (or `-name`/`-label` for descending)
when a standing sort is active.

While a sort stands, `row move` and `row order` are refused and name
`row sort <table> --manual`. `col del` refuses a column that holds members
(naming all blocking rows and members, with one batch `cell remove` command per
occupied cell), a text column with a value (clear
it with `row set <table> <row> <col>=`), a column a `pct(...)` or `sum(...)` column reads
(remove that one first) and the last column. Quote a column spec that has
parentheses: the shell reads `pct(busy)` unquoted as a pattern.

`ns_table_bind` (the sprint's stream block) uses its supplied order in manual
mode and maintains the table's standing sort when one is active. The model is
`tla/TableOrder.tla`; the tests are
`pkg/ntable/order_functional_test.go`.

## The render rules

The header row of column labels, a rule, one line per row (its label, then
its cells), and, when any column folds, a rule and the footer row. Text,
members, first and last cells are left-aligned; count cells right-aligned.
Cells are separated by ` | ` and the rule joins dashes with `-+-`. A column
is as wide as its widest cell (the footer counts) unless its width is fixed
in the definition (`create --width`) or for one render (`render --width`);
a wider cell is not cut. A last column is padded only when right-aligned,
so no line ends in whitespace. `--label-width <n>` sets the separate row-label
column width. Labels, text, member names, footers and view titles display control
characters as literal escapes (for example, newline as `\x0a` and ESC as `\x1b`).
Widths are measured after escaping. Stored values remain unchanged; text cannot
add a row or execute a terminal control sequence.
Known-empty percentages, including pooled footers, print `0.0%`. A cell whose set did not come back prints `?`,
never a false 0, and so does the fold over it. A row hidden with `set --hide`
stays in the fold.

**The empty rule.** A table always renders: an empty table prints its title
header, its rule and its footer, with no body line, no placeholder and no second
rule above the footer. A row with all
zero counts prints like any other.

## Watching

`watch` renders the named tables once per `--every` (1s), one blank line
between two, `--title` first. With no `--out` it draws in place
on the terminal: the ANSI home-and-clear sequence, then the text, so a
console tab shows the live table with no shell loop. `--out <file>`
publishes each tick by writing a temp file beside it and renaming it over,
so a reader sees one whole table. `--once` renders
once and exits, with no clear. With explicit table names, every tick is exactly one Redis pipeline of read-only snapshots, including cold and changed shapes. A stored view adds one exchange to reload its configuration. An explicit table watch holds the tables; a stored view also has its timestamp,
title and optional summary. In either mode, a tick whose read fails leaves the last good text standing with one
`store unreachable since <time>` line under it, and stderr says why once. While
the store answers the frame is the table and nothing else: no age, no counter. A signal ends it,
exit 0. If continuous watch cannot write stdout or publish to `--out`, it stops
immediately at exit 1 and names the output destination to repair on stderr.

## Module integration and deployment

The module exposes `QueueCells` and `QueueCount` for callers that hold a
shape and batch other reads in the same pipeline. `Reader.Queue`
returns an atomic snapshot; its compatibility `changed` result is always false.
`Shape`, `Bind`, and `Summaries` are also single-call APIs, so synchronizing a
changed table does not introduce a chain of dependent network reads.
Bound cells retain their owner and exclude metadata; deleting a row or dropping
a table never deletes a bound set. A refused write includes the table, row,
column and member where applicable, plus the relevant inspection or repair verb.

The function library and source ACL declarations must be deployed together by
the store owner. Writers need `FCALL` grants for `ns_table_create`, `drop`,
`row_add`, `row_del`, `cell_add`, `cell_remove`, `cell_move`, `bind`, `clear`,
`member_create`, `drop_definition`, `set`, `row_set`, `rows_add`, and `rows_hide`
(each with the `ns_table_` prefix); readers need `FCALL_RO` for
`ns_table_read`, `ns_table_list`, and `ns_table_members`, plus the underlying
commands and authorized key patterns. Writers also need `HDEL`, `TYPE`,
`XINFO STREAM` and `XADD` for records and receipt preflight; revision counters
use the existing `HGET`/`HSET` grants. Rename additionally needs `SCAN` and `RENAME`. Stored views need `ns_view_set`/`ns_view_get` (and `ns_view_state` to set a state) and grants for `view:*` and `views`. The explicit maintenance check needs
`FCALL_RO ns_table_check` and `SCAN`; these are not added to the display-only
reader role. Custom epoch/record namespaces require their own key grants. The standalone ordered-set move retains
`ns_oset_move`. `SCARD` and `SISMEMBER` preflight the registry type before
multi-key writes. No command changes live grants. Function-library setup is described in
[Start locally](#start-locally).

Redis functions run with the caller's ACL permissions. A writer's underlying
command grants also allow those same commands directly; Redis ACLs cannot make
`ZADD` on a key legal only inside `FCALL`. The record/set/receipt contract therefore
assumes trusted writers using the functions, starting from valid state. `check`
detects covered forms of drift, but does not prevent an authorized raw writer
from creating it. Enforcing a function-only boundary for untrusted clients needs
a gateway holding private writer credentials, with clients unable to write those
keys directly. A dedicated Redis username whose credentials clients possess
does not provide that boundary. See the [Redis Lua ACL reference](https://redis.io/docs/latest/develop/programmability/lua-api/#redisacl_check_cmdcommand-arg).

A refused move of an absent member, for example, names its exact source and
suggests `nova-table cell members <table> <row> <source-column>`; it does not
suggest replaying a write whose preconditions have not been checked.
