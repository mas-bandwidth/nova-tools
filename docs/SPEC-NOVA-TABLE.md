# nova-table — specification

`nova-table` manages work tables, ordered sets, and live tabular views over
Redis. It provides the mechanical, low-latency presentation layer for work
streams, queues, batches, and fleet coordination across Nova tools.

A work stream table is a series of ordered sets per cell, rendered to text
efficiently and mechanically, once per second in a console window or published
by atomic rename. The display presents tabular data directly, without
superfluous chrome or unprompted decoration.

This specification is normative. It is a sibling of [SPEC.md](SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line grammar,
and `internal/oneline` escaping — applies here unchanged.

---

## 1. Data structures

A table is a named two-dimensional grid composed of declared columns and rows
backed by Redis data structures.

- **Table**: A named tabular container (`table:<name>`). Holds an epoch
  (`uint64`), revision counter (`uint64`), an ordered list of columns, an
  ordered list of rows, optional footer aggregation definitions, and visibility
  toggles (`--hidden` / `--visible`).
- **Column**: A declared vertical field with a name, display label, projection,
  fold aggregation rule, width override, and visibility status. Column visibility
  is toggled via `set <table> --hide <cols>` and `set <table> --show <cols>`
  without deleting column data. Projections govern how cell contents are formatted:
  - `count`: Cardinality of the cell's ordered set (`|s|`), the default.
  - `members`: Comma-joined member identifiers in score order.
  - `first`: Earliest member identifier by score.
  - `last`: Latest member identifier by score.
  - `text`: Plain string value stored in the row record, not an ordered set.
  - `pct(<count-column>)`: Percentage calculation of the named count column
    relative to the sum of all count columns in that row.
- **Row**: A declared horizontal entity identified by a row key. Holds an optional
  display label, an optional member exclusion (`--exclude`), an optional owner
  verb (`--owner`), an array of cells matching the table's declared columns, and
  visibility status (`row hide` / `row show`).
- **Cell**: The intersection of a row and a column. Three kinds exist:
  - *Owned cell*: Backed by a dedicated Redis sorted set (ZSET) owned by this
    table (`table:<name>:cell:<row>:<col>` at epoch 0, or
    `table:<name>:<epoch>:cell:<row>:<col>` at non-zero epoch).
  - *Bound cell*: Aliased to an external Redis sorted set (e.g. `ws:*`, `bench:*`)
    managed by another tool, declared via `row add ... <col>=<key>` with an explicit
    owner verb.
  - *Text cell*: A string value stored directly in the row's attribute hash
    (`text:<col>`).
- **Member**: A uniquely identified item placed within a cell's ordered set. Each
  member carries an ID string and a `float64` score (monotonic millisecond
  timestamps from `nowMillis` or explicit priorities). In any table, each member ID
  exists in at most one cell at any time. Member records are stored in Redis
  hashes under the prefix `table::member:<id>` by default (`MemberKey`), or under
  a configurable member prefix specified at table creation via `--member-prefix`
  (stored in `table:<name>:identity`). Valid unplaced member records can exist in
  the registry prior to placement; placement in an owned cell sets
  `place:<table_name>` to `<row>:<col>` and records the member's `epoch`.
- **View**: A named composite layout (`view:<name>`) grouping one or more
  tables together. Displays an optional view title, an optional summary progress
  ratio (`x/y z% -> ETA`) based on a designated done column, and the rendered
  tables.

---

## 2. Key namespaces

`nova-table` operates strictly within the following Redis key patterns:

| Key Pattern | Redis Type | Purpose |
| --- | --- | --- |
| `table:<name>` | Hash | Table schema, epoch, revision, column definitions, footer, hidden attributes |
| `table:<name>:identity` | Hash | Immutable table identity: `epoch_key`, `epoch_field`, `member_prefix` |
| `table:<name>:revision` | Hash | Revision counter tracking mutations |
| `table:<name>:changes` | Stream | Audit stream of table mutations |
| `table:<name>:rows` | Sorted Set | Row keys in display order with ordinal scores (epoch 0) |
| `table:<name>:<epoch>:rows` | Sorted Set | Row keys in display order for `<epoch>` |
| `table:<name>:row:<row>` | Hash | Row labels, external column bindings (`key:<col>`), text cell contents (epoch 0) |
| `table:<name>:<epoch>:row:<row>` | Hash | Row labels, external column bindings, text cell contents for `<epoch>` |
| `table:<name>:cell:<row>:<col>` | Sorted Set | Owned ordered set of member IDs with scores (epoch 0) |
| `table:<name>:<epoch>:cell:<row>:<col>` | Sorted Set | Owned ordered set of member IDs with scores fenced to `<epoch>` |
| `table:<name>:<epoch>:definition` | Hash | Historical definition snapshot for `<epoch>` |
| `<member_prefix><id>` | Hash | Member reverse-index (default `table::member:<id>`) tracking `place:<table_name>` (`<row>:<col>`) and `epoch` |
| `view:<name>` | Hash | View definition storing `title`, `summary`, and comma-separated `tables` |
| `views` | Set | Catalog of all active view names |
| `tables` | Set | Catalog of all active table names |

The member prefix defaults to `table::member:` and can be customized per table
using `--member-prefix` on `create`. All keys outside these namespaces are
external bindings and are read-only to `nova-table`.

---

## 3. Verbs and command grammar

`nova-table` exposes verbs for table schema administration, row and column
management, member placement, view assembly, and display:

```
nova-table create <table> --columns <name[:projection[:fold[:label]]],...> [--footer <label>] [--width <col=n,...>]
nova-table set    <table> [--footer <label>] [--rename <name>] [--columns <spec>] [--hide <cols>] [--show <cols>] [--hidden | --visible]
nova-table drop   <table> [--definition]
nova-table list   [--max <n>]
nova-table row add    <table> <row>... [--label <text>] [--exclude <member>] [--owner <verb>] [<col>=<key> ...]
nova-table row set    <table> <row> <col>=<value>...
nova-table row hide   <table> <row>...
nova-table row show   <table> <row>...
nova-table row del    <table> <row>
nova-table row move   <table> <row> --first | --last | --before <row> | --after <row>
nova-table row order  <table> <row>...
nova-table row sort   <table> [--by name|label|<col>] [--desc] [--keep] | --manual
nova-table col add    <table> <name[:projection[:fold[:label]]]> [--first | --last | --before <col> | --after <col>]
nova-table col del    <table> <col>
nova-table col move   <table> <col> --first | --last | --before <col> | --after <col>
nova-table cell add      <table> <row> <col> <member>... [--score <n>]
nova-table cell remove   <table> <row> <col> <member>...
nova-table cell move     <table> <row> <from-col> <to-col> <member>...
nova-table cell members  <table> <row> <col> [--max <n>]
nova-table member create <table> <id>
nova-table member find   <table> <id>
nova-table check  <table>
nova-table clear  <table>
nova-table show   <table> [--at-epoch <n>] [--max <n>]
nova-table render <table> | --view <name> [--at-epoch <n>] [--hide-zero-rows] [--width <col=n,...>] [--label-width <n>]
nova-table watch  <table>[,<table>...] | --view <name> [--every <duration>] [--out <file>] [--title <text>] [--hide-zero-rows] [--width <col=n,...>] [--label-width <n>] [--check] [--once]
nova-table view set  <name> --tables <a,b,...> [--title <text>] [--summary <count-column>]
nova-table view show <name>
nova-table view list
nova-table view del  <name>
nova-table shell  [--redis <addr> | --seat <name>] [--keep-going] [--epoch <n>] [--receipt=false]
nova-table version
nova-table help   [<verb> [<subverb>]]
```

Table write verbs accept `--epoch <observed epoch>` (default 0), `--actor`,
`--fence`, `--idem`, and `--receipt`. `create` also accepts `--epoch-key <key>`,
`--epoch-field <field>` (default `n`), and `--member-prefix <prefix>` (default
`table::member:`). Column visibility is managed via `set --hide/--show`; there
are no separate `col hide/show` subverbs and `create` does not take `--hidden`.

---

## 4. Bounds and round-trip invariants

`nova-table` enforces strict round-trip bounds:

1. **Per-operation atomic exchange**: Individual table mutations and single-table
   queries invoke registered Redis functions (`FCALL` or `FCALL_RO` from
   `nova_sprint`, such as `ns_table_create`, `ns_table_set`, `ns_table_read`,
   `ns_table_check`) and execute in exactly one application round trip.
2. **Per-frame watch bounds**:
   - **Direct table watch**: When watching named tables without `--check`, all
     watched tables are read in a single Redis pipeline per tick
     (`tableSnapshots`), capturing definition, rows, bindings, and cell states
     in one round trip.
   - **Stored-view watch**: When watching via `--view`, the view definition is
     read first (one round trip) to determine its current table set, followed by
     a single pipelined snapshot reading all constituent tables (two round trips
     per tick total).
   - **Per-tick checks (`--check`)**: When `--check` is specified, `watch`
     performs an independent `ntable.Check` (`FCALL_RO ns_table_check`) for each
     snapshotted table. This adds exactly one check exchange per table per tick.
3. **Single member placement**: A member belongs to at most one cell in a table.
   Placing an already-placed member into a cell via `cell add` is refused with
   `ErrPlaced`. Relocating a member between cells requires `cell move`.
4. **Epoch fencing**: Mutations verify the caller's observed epoch against the
   active table epoch. Writes against a stale epoch refuse with `ErrStale` and
   leave the store unchanged.
5. **Read-only execution**: Queries (`show`, `render`, `list`, and default
   `watch`) perform zero mutations, writes, or deletions.

---

## 5. Per-tick invariant check (`watch --check`)

When `--check` is specified, `nova-table watch` executes an invariant verification
on every tick:

```sh
nova-table watch --view today --check
nova-table watch work --check --once
```

For each table snapshotted during the tick, `watch` runs `ntable.Check`
(`FCALL_RO ns_table_check`), auditing the following invariant properties:

1. **Audited owned cells**: `check` audits the owned cells of the table (skipping
   bound cells and text/formula columns). It verifies that bound cell keys do not
   alias table-owned storage (`ErrOwnedAlias`).
2. **Disjoint placements (no duplicate cells)**: No member ID exists in more
   than one owned cell sorted set across the table (`seen[id]` duplication
   returns `ErrDrift`).
3. **Cell-to-member integrity**: For every member ID present in an owned cell
   sorted set, `T.member` verifies that the member record exists, belongs to the
   table's active epoch (mismatch returns `ErrMemberEpoch`), and has
   `place:<table_name>` matching that exact `<row>:<col>` (mismatch returns
   `ErrDrift`).
4. **Ghost scan (current-epoch placements)**: The ghost scan inspects keys
   matching the table's configured member prefix (`table::member:*` by default).
   Valid unplaced member records (`place:<table_name>` unset) are part of the
   registry and are not corruption. The ghost scan rejects only records where
   `place:<table_name>` is set and the recorded epoch matches the table's active
   epoch, but no corresponding owned cell was found in the table (`ErrDrift`).
5. **Hidden owned cells**: The scan inspects keys matching `<table_prefix>:cell:*`.
   Any non-empty cell sorted set not referenced by the active row and column
   definitions is rejected as `ErrDrift`.

When `--check` is omitted (the default), `ntable.Check` is not called,
preserving the strict single round-trip pipeline invariant tested by the
functional trips gate.

---

## 6. Stall row behavior (never repair)

When `watch --check` detects an invariant violation on any snapshotted table:

1. **Formatting**: The violation is formatted as a single stall line appended
   below the table or view render:
   ```
   stall: <table>: <detail>
   ```
   For example, if a member is duplicated across cells:
   ```
   stall: work: member record and owned set disagree: [build ready task-1 duplicate place]
   ```
   If a member belongs to a mismatching epoch:
   ```
   stall: work: member belongs to another epoch: [job-1 1 2]
   ```
   If an observed table epoch is stale:
   ```
   stall: work: observed epoch is stale: observed 1, active 2
   ```
2. **Terminal lifecycle**: `watch` does not abort or crash on invariant failure;
   it continues ticking, redrawing the table with the stall row present on each
   tick, and exits 0 upon receiving termination signals (`SIGINT`, `SIGTERM`).
3. **CRITICAL LAW — NEVER REPAIR**:
   The check is strictly read-only (`FCALL_RO`). When an invariant check fails,
   `nova-table` **never** attempts automated repair, mutation, reconciliation, or
   deletion. The corrupted keys in Redis remain completely untouched.

   *Rationale*: Automated repair destroys forensic evidence, risks deleting
   valid in-flight work from concurrent writers, and breaches the single-writer
   coordination model. The stall row surfaces the corruption immediately to
   human operators and coordinators so that remediation can be investigated and
   applied deliberately.
