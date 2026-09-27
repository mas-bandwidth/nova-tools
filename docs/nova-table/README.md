# nova-table: a table over Redis, every cell an ordered set

## The design, in Glenn's words (2026-09-27)

"at an even simpler level, I think there should be a concept of ordered
sets." / "The work stream table is really just a series of ordered sets,
per-cell" / "and the value printed, happens to be for each cell, |s|" /
"(but it doesn't need to be always)".

"there are cells that are headers for columns, and cells that are headers
for rows" / "and there are cells at the bottom of each row that are sums or
some function of the column above." / "for example, for the stream table the
bottom rows are the sum of the column above."

"create a way to render this table to text, efficiently and mechanically,
once per-second in a console window" / "it should only contain that table
data, no bullshit around it. don't let extra stuff creep in."

## What a table is

The primitive is the ordered set: a Redis ZSET, members with scores, read in
score order. `internal/ntable` gives it five verbs (`Add`, `Remove`, `Move`,
`Members`, `Card`) and one count (`QueueCount`: the set's size, one member
left out when a row names it) and knows nothing about sprints.

A table is columns, rows and one ordered set per body cell. It has three
kinds of cell:

- **Header cells.** The top row is the column labels (each column has a
  label, default its name); the left column is the row labels (each row has
  a label, default its key). Labels, not sets.
- **Body cells.** Every body cell is an ordered set, printed by its column's
  *projection*: `count` (the set's size, Glenn's |s|; the default),
  `members` (the members in score order, comma-joined), `first` and `last`
  (the lowest and highest scored member), or `text` (a label column: the
  row's label, no set).
- **Footer cells.** One per column, the column's *fold* over the body:
  `sum` (the default for a count) or `max` of the counts, `union` of the
  members, or `none` (blank). The footer row carries the table's footer
  label (default `total`). A table whose columns all fold `none` prints no
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

A row may name one member its counts and members leave out (`--exclude`).
The sprint's stream sentinel is the case: the stream's stop, not work.

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
table:<t>:cell:<r>:<c>     ZSET  an owned cell
```

Reads are pipelines: a reader keeps a table's shape (the definition, the
row order, every row's bindings) across reads, re-reads it in the same
pipeline as the cells, and reads again when it moved, so a watch tick is
one round trip in the steady state. Never `KEYS`, never `SCAN`. Writes are
plain pipelined commands (`HSET`, `ZADD`, `ZREM`, `DEL`), except the two that
touch several keys and must land whole, which are functions of the
nova_sprint library (`internal/nsprint/fn/lua/table.lua`): `ns_oset_move`
(`cell move`: `ZREM` from, `ZADD` to, refused `NOTMEMBER`) and
`ns_table_clear` (`clear`: every owned cell emptied and every row removed,
refused `BOUND`).

## The verbs

Every verb takes `--redis <addr>`, else `NOVA_SPRINT_REDIS`, then
`NOVA_REDIS_ADDR`, then the seat's address, and dials as the seat nova-sprint
dials as. Flags may follow the words. One typed line per success on stdout;
one line on stderr and exit 2 for a usage refusal, exit 1 when the store
said no.

```
nova-table create <table> --columns <name[:projection[:fold[:label]]],...> [--footer <label>] [--width <col=n,...>]
nova-table drop <table>
nova-table list
nova-table row add <table> <row> [--label <text>] [--exclude <member>] [--owner <verb>] [<col>=<key> ...]
nova-table row del <table> <row>
nova-table cell add <table> <row> <col> <member> [--score <n>]
nova-table cell remove <table> <row> <col> <member>
nova-table cell move <table> <row> <from-col> <to-col> <member>
nova-table cell members <table> <row> <col>
nova-table clear <table>
nova-table show <table>
nova-table render <table> [--hide-zero-rows] [--width <col=n,...>]
nova-table watch <table>[,<table>...] [--every <duration>] [--out <file>] [--title <text>] [--hide-zero-rows] [--once]
```

| verb | prints |
| --- | --- |
| `create` | `TABLE CREATE table=<t> columns=<n>`; an existing table with the same definition is left; another definition is refused |
| `drop` | `TABLE DROP table=<t> rows=<n>`; the definition, rows and owned cells go, a bound set stays |
| `list` | `TABLE LIST tables=<n>`, then `TABLE table=<t> columns=<n> rows=<n>` per table |
| `row add` | `TABLE ROW ADD table=<t> row=<r> cols=<n> bound=<n>`; a row already there keeps its place and its cells; a binding wants `--owner` |
| `row del` | `TABLE ROW DEL table=<t> row=<r> existed=<0\|1>`; its owned cells go with it |
| `cell add`, `cell remove` | `TABLE CELL table=<t> row=<r> col=<c> n=<count after>`; `--score` is the member's place (the unix ms when omitted) |
| `cell move` | `TABLE MOVE table=<t> row=<r> member=<m> from=<c> to=<c> n=<count of to>`; one call, the score kept; `NOTMEMBER` refused |
| `cell members` | `TABLE CELL ... n=<n>`, then `TABLE MEMBER table=<t> row=<r> col=<c> member=<m> score=<s>` per member |
| `clear` | `TABLE CLEAR table=<t> rows=<n> ms=<n>`; one call; the definition stays |
| `show` | `TABLE table=<t> columns=<n> rows=<n>`, then `TABLE ROW table=<t> row=<r> <col>=<count> ...` per row |
| `render` | the text, nothing else; nothing at all when the table is empty |
| `watch` | the text, once per tick, in place or to `--out` |

A value holding a space is quoted, `row="swarm: cards"`.

## The render rules

The header row of column labels, a rule, one line per row (its label, then
its cells), and, when any column folds, a rule and the footer row. Text,
members, first and last cells are left-aligned; count cells right-aligned.
Cells are separated by ` | ` and the rule joins dashes with `-+-`. A column
is as wide as its widest cell (the footer counts) unless its width is fixed
in the definition (`create --width`) or for one render (`render --width`);
a wider cell is not cut. A last column is padded only when right-aligned,
so no line ends in a space. A cell whose set did not come back prints `?`,
never a false 0, and so does the fold over it. `--hide-zero-rows` hides a
row whose count cells are all zero and all read; the fold is still the
column's, hidden rows included.

**The empty rule.** An empty table, and a table with no visible row, renders
as the empty string: no header, no newline. The sprint table hides its
stream block that way with no extra blank line.

## Watching

`watch` renders the named tables once per `--every` (1s), one blank line
between two that print, `--title` first. With no `--out` it draws in place
on the terminal: the ANSI home-and-clear sequence, then the text, so a
console tab shows the live table with no shell loop. `--out <file>`
publishes each tick by writing a temp file beside it and renaming it over,
the sprint table's way, so a reader sees one whole table. `--once` renders
once and exits, with no clear. Every tick is exactly one Redis pipeline for
every cell of every named table. The screen holds the tables and nothing
else; a tick whose read fails leaves the last good text standing with one
`stale: <n>s` line under it, and stderr says why once. A signal ends it,
exit 0.

## The first table: the sprint's stream block

The sprint table's stream block (`nova-sprint table --layout live`) is the
table `streams`: one row per stream of `ws:order`, a `stream` text column 25
wide, and one `count:sum` column per state of the stream line (waiting,
ready, working, review, merging, landed), footer `total`. Every cell is
bound to the set the card model already keeps, `ws:<s>:<state>` under the
sprint epoch, with the stream's sentinel excluded; nothing is copied. The
sprint tick reads every cell through `ntable` in its one pipeline (the
headline and the block stay one count), renders the block through
`ntable.Render`, byte for byte what it printed before, and the sprint loop
binds the table in the store whenever its shape moves (a stream added or
gone, a `sprint clear`'s new epoch), so

```
nova-table render streams --hide-zero-rows
```

prints the same block from the same sets, and a write through nova-table is
refused naming `nova-sprint task move`. `sprint clear` needs no table
call: the epoch moves, the tick binds the new epoch's sets, they are empty,
and the block hides.
