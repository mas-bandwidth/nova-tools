# nova-table — specification

`nova-table` manages work tables, ordered sets, and live tabular views over
Redis. It provides the mechanical, low-latency presentation layer for work
streams, queues, batches, and fleet coordination across Nova tools.

The design is grounded in Glenn's 2026-09-27 directive:

> "The work stream table is really just a series of ordered sets, per-cell...
> and the value printed, happens to be for each cell, |s| (but it doesn't need to
> be always)... create a way to render this table to text, efficiently and
> mechanically, once per-second in a console window; it should only contain
> that table data, no bullshit around it. don't let extra stuff creep in."

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
  toggles.
- **Column**: A declared vertical field with a name, display label, projection,
  fold aggregation rule, width override, and visibility status (`hidden`).
  Projections govern how cell contents are formatted:
  - `count`: Cardinality of the cell's ordered set (`|s|`), the default.
  - `members`: Comma-joined member identifiers in score order.
  - `first`: Earliest member identifier by score.
  - `last`: Latest member identifier by score.
  - `text`: Plain string value stored in the row record, not an ordered set.
  - `pct(<count-column>)`: Percentage calculation of the named count column
    relative to the sum of all count columns in that row.
- **Row**: A declared horizontal entity identified by a row key. Holds a display
  label, an array of cells matching the table's declared columns, and a
  visibility status (`hidden`).
- **Cell**: The intersection of a row and a column. Three kinds exist:
  - *Owned cell*: Backed by a dedicated Redis sorted set (ZSET) owned by this
    table.
  - *Bound cell*: Aliased to an external Redis sorted set (e.g. `ws:*`, `bench:*`)
    managed by another tool, declared with an explicit owner verb.
  - *Text cell*: A string value stored directly in the row's attribute hash.
- **Member**: A uniquely identified item placed within a cell's ordered set. Each
  member carries an ID string and a `float64` score (typically monotonic
  millisecond timestamps from `nowMillis` or explicit priorities). In any table,
  each member ID exists in at most one cell at any time.
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
| `table:<name>:rows` | Sorted Set | Row keys in display order with ordinal scores |
| `table:<name>:row:<row>` | Hash | Row labels, external column binding keys (`key:<col>`), text cell contents |
| `table:<name>:cell:<row>:<col>` | Sorted Set | Owned ordered set of member IDs with scores (epoch 0) |
| `table:<name>:cell:<row>:<col>:<epoch>` | Sorted Set | Owned ordered set of member IDs with scores fenced to `<epoch>` |
| `task:<id>` | Hash | Member reverse-index tracking `place:<table_name>` (`<row>:<col>`) and `epoch` |
| `view:<name>` | Hash | View definition storing `title`, `summary`, and comma-separated `tables` |
| `views` | Set | Catalog of all active view names |
| `tables` | Set | Catalog of all active table names |

All keys outside these namespaces are external bindings and are read-only to
`nova-table`.

---

## 3. Verbs and command grammar

`nova-table` exposes verbs for table schema administration, member placement,
view assembly, and display:

```
nova-table create <table> --columns <spec> [--footer <fold>] [--hidden]
nova-table set    <table> [--columns <spec>] [--footer <fold>] [--hidden|--visible]
nova-table drop   <table> [--definition]
nova-table list
nova-table row    add|set|hide|show|del|move|order|sort <table> ...
nova-table col    add|del|move|hide|show <table> ...
nova-table cell   add|remove|move|members <table> <row> <col> ...
nova-table member create|find <table> <id>
nova-table check  <table>
nova-table clear  <table>
nova-table show   <table> [--at-epoch <n>]
nova-table render <table> | --view <name> [--at-epoch <n>] [--hide-zero-rows] [--width <c=n>] [--label-width <n>]
nova-table watch  <table>[,<table>...] | --view <name> [--every <dur>] [--out <file>] [--title <text>] [--check] [--once]
nova-table view   set|show|list|del <name> ...
nova-table shell
nova-table version
nova-table help
```

---

## 4. Single-trip invariants

`nova-table` enforces strict round-trip bounds:

1. **Every mutation or query is a single atomic exchange**: Every table command
   invokes a registered Redis function (`FCALL` or `FCALL_RO` from `nova_sprint`)
   and executes in exactly one application round trip.
2. **Unflagged watch uses one pipeline per tick**: When `nova-table watch` runs
   without `--check`, all watched tables are read in a single Redis pipeline per
   tick. The snapshot captures schema and cell states atomically.
3. **Single member placement**: A member belongs to at most one cell in a table.
   Placing an already-placed member into another cell requires `cell move` or
   refuses with `ErrDrift`.
4. **Epoch fencing**: Mutations verify the caller's observed epoch. Writes
   against a stale epoch refuse with `ErrStale` and leave the store unchanged.
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

1. **Member-to-cell integrity**: Every member recorded in `task:<id>` for this
   table corresponds to an active cell in the schema that contains that member ID.
2. **Cell-to-member integrity**: Every member ID present in an owned cell sorted
   set has an accompanying `task:<id>` record whose `place:<table_name>` matches
   that exact `<row>:<col>`.
3. **Disjoint placements (no duplicate cells)**: No member ID exists in more
   than one cell sorted set across the table.
4. **Epoch consistency**: Members and owned cells match the table's active
   epoch. Stale epoch references are flagged as violations (`ErrMemberEpoch` or
   `ErrStale`).
5. **No ghost records or orphan cells**: Unplaced member records and unreferenced
   cell sorted sets outside the defined row/column structure are detected.

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
   If an epoch mismatch is detected:
   ```
   stall: work: observed epoch is stale: observed 1, active 2
   ```
2. **Terminal lifecycle**: `watch` does not abort or crash on invariant failure;
   it continues ticking, redrawing the table with the stall row present, and
   exits 0 upon receiving termination signals (`SIGINT`, `SIGTERM`).
3. **CRITICAL LAW — NEVER REPAIR**:
   The check is strictly read-only (`FCALL_RO`). When an invariant check fails,
   `nova-table` **never** attempts automated repair, mutation, reconciliation, or
   deletion. The corrupted keys in Redis remain completely untouched.

   *Rationale*: Automated repair destroys forensic evidence, risks deleting
   valid in-flight work from concurrent writers, and breaches the single-writer
   coordination model. The stall row surfaces the corruption immediately to
   human operators and coordinators so that remediation can be investigated and
   applied deliberately.
