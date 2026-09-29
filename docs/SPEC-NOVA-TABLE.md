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
nova-table list
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
nova-table cell members  <table> <row> <col>
nova-table member create <table> <id>
nova-table member find   <table> <id>
nova-table check  <table>
nova-table clear  <table>
nova-table show   <table> [--at-epoch <n>]
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

## Batched member read and conditional write

The data-oriented rule is **batch always**: members are plain data in arrays,
and the manager transforms the selected array in one store call.

This section accompanies the implementation of the table batch API. The card
manager waits for this table extension's gate; it does not implement private
placement or emulate a batch using repeated client calls.

### Scope and calls

`ns_table_read_set` is one read-only application call over one table and active
epoch. Its explicit scope is a member-ID array or a complete declared row/column
selection. It returns table identity, epoch/revision, every selected member's
record revision, fields, verified owned placement and score, and explicit missing
members. A selection beyond the declared bound refuses; it cannot return a prefix
marked complete. Bound/external cells are not writable through this interface.

`ns_table_apply` is one application call with one conditional mutation manifest.
It creates placed members, moves existing members across owned cells (including
across rows), removes owned placement while retaining the member record, and
sets/unsets permitted member fields. All guards read one pre-state;
all mutations share one logical commit and one batch receipt. The primitive handles
mechanical conditions, not card lifecycle policy, prose, reviews, jobs or leases.
Clients may prepare using the read-only call, but a write is one call, not a loop.

The entry-point names and wire protocol are library interfaces. Any exposed CLI
form uses the same manifest and prints its batch receipt; no shell-evaluated command
list or separate implementation of the transaction is permitted.

### Manifest, identity and bounds

A version-1 manifest contains table, epoch, expected_table_revision, operation_id,
actor and a members array. Epoch/revision counters are decimal strings bounded as
uint64; they never traverse floating-point numbers. Members have unique IDs across
the entire array. Each entry has an `expect` record and zero or more compatible
changes. Read-only guard entries have no changes. All referenced rows/columns must
be declared and owned. Unknown schema fields and duplicate JSON keys refuse.

```json
{
  "schema": 1,
  "table": "work",
  "epoch": "0",
  "expected_table_revision": "12",
  "operation_id": "op-17",
  "actor": "coordinator",
  "members": [
    {
      "id": "c1",
      "expect": {
        "revision": "2",
        "place": {"row": "build", "col": "ready"},
        "fields": {"definition": {"equals": "definition-id"}}
      },
      "move": {"row": "build", "col": "working"},
      "set": {"result": "event-17"}
    },
    {
      "id": "c2",
      "expect": {"absent": true},
      "create": {"row": "build", "col": "waiting", "score": 1},
      "set": {"definition": "definition-2"}
    },
    {
      "id": "c0",
      "expect": {
        "revision": "4",
        "place": {"row": "build", "col": "landed"}
      }
    }
  ]
}
```

A member appears exactly once: its expected revision, position, extra field guards,
move and field updates are grouped in that entry. A move plus set/unset is one
member mutation; duplicate entries are never merged. Create requires absence of
both record and any owned placement; create plus move, remove, or an existing
member expectation refuses. Remove is expressed only as `"remove": true`; any
other value refuses. A field cannot occur in both `set` and `unset` in one entry.
An existing member may omit its revision guard; omission means no member-revision
comparison, not a comparison against zero. Its other explicit guards and the
expected table revision still apply. A guard-only entry participates in validation
but not the changed count. All references, including guard-only dependencies, are
explicit.

The member `revision` is a table-owned counter. A legacy existing member with no
record revision reads as zero. A newly created member starts at one; any accepted
change to its placement, score or application fields increments it once,
irrespective of how many fields changed. A move with no score preserves the current score; an
explicit finite score replaces it. An entry whose placement, score and application
fields remain unchanged does not increment the member revision or changed count.
In particular, moving to the current cell at the current score is a member no-op
unless its application fields change. A score-only change increments the member
revision once. Removal requires existing owned placement; an already-unplaced
member refuses with `NOTMEMBER` before writes. Removal clears that placement and
retains the member record, application fields and advanced member revision.
Ordinary table member writers use the same revision helper so they cannot bypass a prepared batch's guard. Epoch, placement/index fields
and revision cannot be set/unset through application metadata. Counter overflow
refuses before any write. Table revision remains the existing table-wide counter;
its expected value rejects changes to the complete observed scope between read and
write, including insertion/deletion not named by the caller.

Field guards are data: exactly one of equals, absent or one_of (a nonempty string
array). Present empty string differs from absent. Values match exact bytes. No Lua,
expression language, function name or arbitrary key is supplied by a guard.
Member fields are below the configured table member prefix; this interface never
writes arbitrary Redis keys. Evidence outside the table is not guarded by this API:
its verified immutable identity must be represented in a member field, or a later
explicit table extension must provide its guard. A prior unguarded client read is
not an atomic prerequisite proof.

Initial proposed bounds: 128 entries with changes, 1,024 guard-only entries and
1 MiB canonical encoded request. IDs/field counts and value byte limits must also
be explicitly bounded by the implementation's reviewed manifest schema. Candidate
limits are measured on supported benches before acceptance. Exceeding a limit
refuses the whole request with the limit and remedy; no automatic chunking turns
one requested transaction into several. A caller can explicitly narrow its next
request, accepting the separately identified transaction scope.

### Validation, atomicity and replay

The server rejects malformed canonical encoding, repeated IDs/keys, incompatible
changes, nonfinite scores, invalid paths/names and bounds before mutation. Then it
validates active epoch and expected table revision, every expected member revision
and source placement, every field condition, all destination types and all required
permissions. It validates record/set agreement, including no duplicate hidden owned
placement, before creating or relocating a member. Every guard is evaluated against
the same pre-state; an update made in this batch cannot satisfy another guard.

Use the existing table validation and staged-write machinery inside this one server
invocation. After all validation, stage cells, reverse indexes, member fields and
revisions, the operation record, table revision and receipt together. An accepted
batch increments the table revision once; every changed member increments its own
revision once. An accepted no-op batch has a recorded result and one noop receipt;
a refused batch changes no key, revision, operation record or receipt. Tests must
pin no-op and refused results separately.

Redis scripts do not provide rollback of writes after a runtime command error.
The design must therefore show that every staged command's type, bounds, permissions
and other preventable failure conditions were checked before writes. Inject failure
at those conditions in tests and compare the complete store image. Redis process
loss/durability guarantees remain those of the configured store; a transport error
is uncertain, not evidence of rollback. No stronger crash guarantee is asserted
merely because the function is one FCALL.

Operation identity is table plus epoch plus operation_id. Persist the canonical
request bytes and result, with its digest for evidence. Caller-provided digest
equality is insufficient to establish identical requests. An identical recorded
request returns the original receipt/result without writes, even when its old
expected revisions no longer match; operation lookup precedes those checks. A
changed request with an existing operation ID refuses. An unrecorded operation at
a stale epoch refuses. Original result epoch/revisions remain visible, so retry
cannot masquerade as a new current-epoch action. No retry loop in transport may
silently invent a new operation ID.

The ordinary --fence/--idem fields remain receipt metadata on existing table verbs;
they do not acquire false historical deduplication semantics through this extension.
The batch entry point's dedicated operation record supplies its replay contract.

### Receipt and refusals

Extend the existing receipt through an explicit batch delta, preserving ordinary
verb receipt compatibility. One receipt identifies operation/request digest, table,
epoch, before/after table revision, actor, changed/noop result and all affected
members' before/after placements, scores, revisions and application-field changes.
For each affected application field, include its before and after values with
absence distinguished from a present empty string. An absent placement has no
score. Set/unset instructions alone do not supply the before values. Include
explicit guard/selection counts; a missing member is not silently omitted. One
batch receipt maps to one model action. Returning the original result on retry
must return the same receipt identity.

Required refusals include stale epoch, table/member revision mismatch, failed field
guard, existing/placed member on create, duplicate manifest member, invalid/missing
record, reverse-index or set drift, unknown row/column, bound cell, reserved-field
write, invalid score/counter, operation-ID conflict and over-limit input. Each names
operation, member or batch, expected/observed state, changed=no and the next usable
command. A transport failure reports changed=unknown and operation reconciliation,
never changed=no without evidence.

### Model and table-layer gate

Extend the member/epoch table model with an atomic batch action, member revisions,
field guards, operation records and one receipt per accepted batch. Reuse the
one-place/epoch definitions; preserve the current per-verb model cases separately.
A named batch wrapper/config uses 3 members, 2 rows, 2 columns, 2 epochs and batch
sizes 1..3. Include a guard-only dependency and a cross-row move. Retain exact model,
config, executable and input hashes; larger configurations are not silently replaced
by smaller ones when they exceed a budget. TLC runs on a bench.

The reversed witnesses must catch: a second member validation failure after a first
member writes; guard evaluation against partially updated state; duplicate placement;
stale epoch, table or member revision; missing revision advancement by an ordinary
writer; wrong-type/permission refusal after partial writes; lost reply causing a
second effect; request-hash collision without byte comparison; receipt emitted before
its complete delta; and stale retry reported as a new operation.

Gate items are the full table-layer bar: model plus reversed witnesses; trip/commit
and complete unchanged-store refusal tests; bounded randomized batches on owned
Redis (16 fixed seeds, 128 steps each) plus runtime checks; Go receipt replay with
one batch receipt per action; real-use rehearsal on an owned store; two independent
exact-revision reads of at least 9/10 with accepting dispositions; then the maintainer's
interactive mini-quack with every verb/refusal, watched table, findings repaired and
repeated until clean. Test N=1 and maximum configured N separately, including a late
invalid entry, interacting moves and a replay. Ordinary table callers retain their
regression suite. The card layer resumes only after this extension has passed the
table gate; no green card test can substitute for proving the lower layer.
