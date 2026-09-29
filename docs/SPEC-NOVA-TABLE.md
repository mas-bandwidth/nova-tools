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
nova-table member read   <table> <id>... | <table> --cell <row:col>
nova-table batch  (<manifest-file> | - | '<json>') [--redis <addr> | --seat <name>] [--epoch <n>] [--actor <name>] [--receipt=true|false]
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

## Batch extension acceptance

The batch model is required for acceptance; the runtime tests do not
replace it. It must add an atomic batch action, member revisions, field guards,
operation records and one receipt per accepted batch to the member/epoch table
model. It reuses the one-place/epoch definitions and preserves the per-verb
model cases separately. A named batch wrapper/config uses 3 members, 2 rows,
2 columns, 2 epochs and batch sizes 1..3. Its cases include a guard-only
dependency and a cross-row move. Evidence retains exact model, config,
executable and input hashes; a larger configuration that exceeds a budget is
not silently replaced by a smaller one. TLC runs on a bench.

Reversed witnesses must catch a second member validation failure after a first
member writes; guards evaluated against a partially updated state; duplicate
placement; stale epoch, table or member revision; missing revision advancement
by an ordinary writer; wrong-type or permission refusal after partial writes;
lost reply causing a second effect; request-hash collision without byte
comparison; a receipt emitted before its complete delta; and stale retry
reported as a new operation.

The full table-layer gate requires the model and reversed witnesses; trip,
commit and complete unchanged-store refusal tests; bounded randomized batches
on owned Redis (16 fixed seeds, 128 steps each) with runtime checks; Go receipt
replay with one batch receipt per action; real-use rehearsal on an owned store;
two independent exact-revision reads of at least 9/10 with accepting
dispositions; and the maintainer's interactive mini-quack with every verb and
refusal, a watched table, and findings repaired and repeated until clean.
Separate cases test N=1 and maximum configured N, including a late invalid
entry, interacting moves and a replay. Ordinary table callers retain their
regression suite. The card layer resumes only after this extension passes the
table gate; a green card test cannot substitute for proving the lower layer.

## Batched member read and conditional write

The data-oriented rule is **batch always**: members are plain data in arrays,
and the manager transforms the selected array in one store call.

A caller reads the members it depends on, then writes against what it read.
The table batch API owns atomic placement and conditional writes. A caller uses
one batch call for the selected members; repeated client calls cannot provide
the same atomic change.

### Scope and calls

`ns_table_read_set` is one read-only application call over one table and active
epoch. Its explicit scope is a member-ID array or a complete declared row/column
selection. It returns table identity, epoch/revision, every selected member's
record revision, fields, verified owned placement and score, and explicit missing
members. A selection beyond the declared bound refuses; it cannot return a prefix
marked complete. Bound/external cells are not writable through this interface.
The request is `{"members": [<id>, ...]}`, `{"selection": [{"row": <row>, "col":
<col>}, ...]}` or a bare array of ids; its list is nonempty, an id is a nonempty
string, and an unknown key, a key named twice in one object (as in a manifest),
both lists together or any other shape refuses as `ARGS`. A malformed request is never answered as an empty set; an empty cell in a
valid selection is a complete, empty answer.

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
uint64; they never traverse floating-point numbers. A manifest names at least one member: an empty `members` array refuses, so a request
that does nothing cannot advance the table revision. Members have unique IDs across
the entire array. Each entry has an `expect` record and zero or more compatible
changes. Read-only guard entries have no changes. All referenced rows/columns must
be declared and owned. Unknown schema fields and duplicate JSON keys refuse. Every value has one JSON
type and nothing is coerced: ids, rows, columns, field names and values,
`operation_id` and `actor` are strings; `epoch`, `expected_table_revision` and a
member `revision` guard are canonical decimal strings; `schema` is the integer
1; `absent` and `remove` are the boolean `true` only; a `score` is a finite JSON
number, and a string never is, whatever a parser would read from it (`"0x10"`,
`" 7 "`, `"1e3"`). A JSON string is well-formed UTF-8 and holds no lone surrogate escape; a name repeated in `unset` or an option repeated in `one_of` refuses. The server and the Go validator accept the same manifests.

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
member expectation refuses. `absent` stands alone in an `expect`: with `revision`,
`place` or `fields` it refuses. Remove is expressed only as `"remove": true`; any
other value refuses. A field cannot occur in both `set` and `unset` in one entry.
An existing member may omit its revision guard; omission means no member-revision
comparison, not a comparison against zero. Its other explicit guards and the
expected table revision still apply. A guard-only entry participates in validation
but not the changed count. All references, including guard-only dependencies, are
explicit. An existing unplaced member may receive field-only set/unset changes,
but cannot be moved into a cell.

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
Ordinary table member writers use the same revision helper so they cannot bypass
a prepared batch's guard. Direct application-field `HSET` outside the batch
protocol does not advance table or member revisions; callers relying on those
fields as prerequisites must include explicit field guards. Epoch,
placement/index fields and revision cannot be set/unset through application
metadata. Counter overflow refuses before any write. Table revision remains the
existing table-wide counter;
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

The bound values are shared. The server enforces batch bounds in `ns_table_apply`
and the read-set member bound in `ns_table_read_set`. The Go manifest validator,
also used by `ApplyBatch`, checks encoded manifest bytes and decoded member and
entry bounds. Receipt bytes and aggregate value bytes per batch depend on the
stored values and are enforced by `ns_table_apply`, not the Go manifest validator.
A test compares the shared constants with this table:

| Bound | Value |
| --- | --- |
| entries with changes | 128 |
| guard-only entries | 1024 |
| manifest bytes | 1048576 |
| member id bytes | 256 |
| field value bytes | 65536 |
| set fields per member | 128 |
| unset fields per member | 1000 |
| guards per member | 1000 |
| one_of options | 1000 |
| read set members | 1024 |
| columns per table | 1000 |
| rows per table | 100000 |
| receipt bytes | 1048576 |
| value bytes per batch | 16777216 |

The 128 set-fields and 1000 unset-fields limits are intentionally different.
The field-value byte limit applies to each value in `set`.
The read-set member limit counts unique IDs in a selection. Manifest size
counts the encoded bytes supplied to the server, including whitespace and JSON
escaping; ID and field-value sizes count decoded UTF-8 bytes.

`columns per table` and `rows per table` bound the size of a table: `create`, `bind`,
`set` (`--columns`, `col add`) and `row add`, `rows add` refuse the column or the row
past the bound as `LIMIT`, naming the bound and the count, before any write. A bind
leaves the table with the rows it names, so it is bound by their number. A table
already over a bound (one written by hand, or under an older rule) can shrink or stay
and never grows: `col del`, `row del` and a batch work on it, `col add` and `row add`
past the bound refuse. The column bound is chosen from what one call writes: a definition is written by one
`HSET` of 2 x (columns + 8) arguments and a script's stack refuses past about 8000,
which is about 3,990 columns measured, so 1,000 leaves a margin of four. The row bound
is chosen from cost: rows are written in chunks of 256, and a table of 100,000 rows
took about a second to write and 1.6 seconds to read whole on the bench, the longest a
single call should hold the store.

A batch holds the store for a time its manifest bounds, not the store's content. It
reads the head of each member (epoch, revision, place) and the fields its entries name
(set, unset and guarded), never a whole record. Before any value is read or hashed it
counts, from lengths (`HSTRLEN`), the bytes of every before-value and after-value of
those fields, and the least size its receipt can have, and refuses at once as `LIMIT`
over either: `value bytes per batch` (16 MiB) or `receipt bytes`. What the store holds
in the fields a batch does not name costs the batch nothing. Measured on the bench, on
four cores, a table of two rows, three runs each: unsetting 16 MiB of before-values
(the most a batch may touch) held the store 0.045 s; a manifest of 873,735 bytes of
guards, every one satisfied (the largest accepted batch measured), 0.47 s; a receipt
near its bound (128 members, 48 fields each), 0.05 to 0.07 s; refused for its receipt
(a manifest of 886,533 bytes), 0.40 s, which is the time to decode and check the
manifest; refused for its value bytes over a store holding 256 MiB in the named
fields, 0.013 s. Decoding and checking a manifest of about 1 MiB is most of the time of
the larger figures. A batch entry whose member has no recorded placement also checks
the table's cells for a stray placement, so the time of such a batch grows with the
table's size,
which the value bounds do not limit: 128 creates held the store 0.36 s on a table of
1,000 rows and 4.1 s on a table of 10,000 rows. The figures above are for a table of
two rows.

An entry has changes when it holds a create, a move, a remove, a nonempty set or
a nonempty unset; otherwise it is guard-only. A manifest at a bound is accepted;
one over it refuses the whole request as `LIMIT` before any write, naming the
bound, its value and the count found (and the member at fault for a per-member
bound), and never echoing the input. The raw 1 MiB envelope is checked before
operation lookup, including for a retry. Decoded manifest bounds are checked
for an unrecorded request after lookup. The early touched-value and minimum
receipt checks use field lengths before reading or hashing values; the final
conservative receipt-size preflight also runs before writes. No automatic chunking turns
one requested transaction into several. A caller can explicitly narrow its
next request, accepting the separately identified transaction scope.

### Validation, atomicity and replay

Validation checks the raw request envelope, then permissively decodes the full
payload and extracts the identity needed to locate a recorded operation. A
matching record returns its original result before decoded static or state checks; different bytes under
the same identity refuse. For an unrecorded request, static checks reject
malformed canonical encoding, repeated IDs/keys, an empty members array,
incompatible changes, nonfinite scores, invalid paths/names and bounds before
mutation. State checks then validate the active epoch and expected table
revision, every expected member revision and source placement, every field
condition, all destination types and all required permissions. A requested
epoch behind the active one refuses `STALE`; one ahead refuses `EPOCHAHEAD`.
Record/set agreement, including no duplicate hidden owned placement, is
checked before creating or relocating a member. Every guard is evaluated
against the same pre-state; an update made in this batch cannot satisfy another
guard.

The batch commits through the staged-write path every ordinary verb uses
(`T.finish`), inside this one server invocation. After all validation, it stages
cells, reverse indexes, member fields and revisions, the operation record, the table
revision and the receipt together. An accepted
batch increments the table revision once; every changed member increments its own
revision once. An accepted no-op batch has a recorded result and one noop receipt, and it
advances the table revision once like any accepted batch, so every other prepared
manifest that expects the earlier revision is stale; a refused batch changes no key,
revision, operation record or receipt.

Redis scripts do not provide rollback of writes after a runtime command error, so
every staged command's type, bounds, permissions and other preventable failure
conditions are checked before the first write; tests inject failure at each and
compare the complete store image. Redis process loss and durability guarantees are
those of the configured store; a transport error is uncertain, not evidence of
rollback. No stronger crash guarantee is asserted because the function is one FCALL.

Operation identity is table plus epoch plus operation_id. The record holds the
canonical request bytes and the result, with the request's digest as evidence. A
caller-provided digest is not enough to establish identical requests: the bytes are
compared. An identical recorded request returns the original receipt/result without writes, even when its old
expected revisions no longer match; operation lookup precedes those checks. A
changed request with an existing operation ID refuses. An unrecorded operation at
a stale epoch refuses. Original result epoch/revisions remain visible, so retry
cannot masquerade as a new current-epoch action. No retry loop in transport may
silently invent a new operation ID.

The library and the command judge a request before they send it, so a request that the
current rules refuse never reaches the store, and they guarantee a replay only for a
request the current rules accept. A request that was applied under looser rules and is
sent again is refused by the library and the command with a refusal that says it was
checked before sending, that this call changed nothing and that it says nothing about an
earlier call with the same operation id; the store, asked directly, returns the recorded
receipt.

Operation records do not expire. A table's operation records, of every epoch, are
one hash, `table:<t>:ops`, whose fields are `<epoch>:<operation id>` and whose values
are the records (the request bytes, at most 1 MiB, and the result, which holds the
receipt). The record holds the result's delta escaped a second time inside the record's
own encoding, and the change event holds the actor twice (its own field and inside the
delta), so both exceed the receipt bound. Measured at the largest batches the bounds
allow: the change event at most 2 MiB (2,096,969 bytes, an actor of 1,048,232 bytes),
the record at most 5 MiB (5,242,119 bytes, an actor of 524,116 `/` characters, which
JSON escapes; 4,175,647 bytes for 128 members each setting 26 fields of 64 `/`
characters). Records do not expire, so a record holds that much for as long as the
table exists. One key keeps the
work of removing them bounded. `drop <table>` and `drop <table> --definition` treat
them alike: each removes the whole hash in the same atomic call as the drop, so a table
created again under the name is a new table and no operation of the old one replays
against it; the two verbs differ only in what they always differed in, the saved
column definition. `clear <table>` removes no record and does not itself
advance the configured external epoch. If that epoch advances separately, an
earlier operation still replays with its original receipt, epoch and revisions.
Epoch snapshots that a drop keeps readable are not operation records and stay. A
replay is guaranteed for as long as the table exists; nothing else removes a record.
The layout is new with the batch, so there is no earlier layout to migrate or to sweep.

The ordinary --fence/--idem fields remain receipt metadata on existing table verbs;
they do not acquire false historical deduplication semantics through this extension.
The batch entry point's dedicated operation record supplies its replay contract.

### Receipt and refusals

The receipt extends an ordinary verb's receipt with a batch delta; an ordinary
verb's receipt is unchanged. One receipt identifies operation/request digest, table,
epoch, before/after table revision, actor, changed/noop result and all affected
members' before/after placements, scores, revisions and application-field changes.
For each affected application field it holds the before and after values, absence
distinguished from a present empty string. An absent placement has no score.
Set/unset instructions alone do not supply the before values. The receipt carries
explicit guard and selection counts; a missing member is not silently omitted.
A receipt is bounded by two rules that hold for every batch, whatever the store holds.
A field value of at most 64 bytes (`ReceiptValueBytes`; `T.receipt_value_bytes` in the
server) is recorded in full; a longer one, whether it is a before-value read from the
store or a value the manifest sets, is recorded as its length and its SHA-1
(`before_bytes`, `before_sha1`, `after_bytes`, `after_sha1`, with the value's own side
null; a null side with no length is an absent field), in the receipt, in the change
event's `batch_delta` and in the operation record's result alike, never in full. The
record's request is the manifest as sent, in full, because replay compares bytes; a
value a manifest sets is in the manifest, so the record holds it in full there. And the
receipt's size, the byte length of its encoded batch delta, is at most `receipt bytes`,
which is the manifest bound, 1 MiB. The size is computed before the first write, with
each score the call reads back after its writes counted at its longest form (24 bytes),
so it is never less than the size the receipt has. A batch whose receipt would exceed the
bound is refused as `LIMIT` before anything changes: the refusal names `receipt bytes`,
the bound and the computed size, and says `changed=no`; the caller changes fewer members
or fewer fields in one manifest and sends the rest as another transaction with its own
operation id. Because every changed field is in the receipt, a manifest under 1 MiB can
still be refused for its receipt. The digest is SHA-1 because it is the only digest a
script has natively (`sha1hex`; a SHA-256 in script code would cost seconds on a few
megabytes); it identifies a value for evidence, it is not a security boundary, and no
program decides that two values are equal from it: the server compares bytes, and the CLI
lists two long values with their digests instead of judging them equal.
`fields_set` lists only the set instructions whose values are recorded in full;
every changed field is in `fields`. A batch that unsets 128 fields of 64 KiB leaves
a receipt of tens of kilobytes, and a replay returns it unchanged. A consumer
that needs the full historical bytes of values above 64 bytes must retain
separate evidence; a length and SHA-1 cannot reconstruct those bytes.
A score in a receipt, a change event, a read set or the CLI is the exact decimal
string the store holds, as the ordinary verbs write it (`0.30000000000000004`,
not `0.3`); two different scores never render alike. A batch's change event has
the fields an ordinary verb's has for the same change, plus `batch_delta`. One
batch receipt maps to one model action. Returning the original result on retry
must return the same receipt identity.

Required refusals include stale epoch, table/member revision mismatch, failed field
guard, existing/placed member on create, duplicate manifest member, invalid/missing
record, reverse-index or set drift, unknown row/column, bound cell, reserved-field
write, invalid score/counter, operation-ID conflict and over-limit input. Each names
operation, member or batch, expected/observed state, changed=no and the next usable
command. A refusal is a code and a sentence: `...: <sentence>; code=NOTMEMBER; changed=no;
run: <command>`. The sentence says what was expected against what was found, in
words (never the code, never a Go type), and a field value in it over 64 bytes is
its first 32 and its length. The next command is one that runs when pasted; for a
member it is `nova-table member read <table> <id>`, for a limit the sentence says
which part of the request to narrow, and for a conflicting operation id it says to
use a new one. A drift says whether a record exists: an owned set that holds a member
no record places there is not described as a record's claim; the next command lists that cell
(`nova-table cell members <table> <row> <col>`), and `nova-table check` finds every
such disagreement (nova-table has no repair verb).
A request for an epoch behind the active one refuses `STALE`; one for an epoch
ahead of it refuses `EPOCHAHEAD`, naming the requested and the active epoch and
suggesting `nova-table show <table>`, which prints the active epoch. Existence
comes first: a table that does not exist refuses as missing whatever epoch is
asked for. A transport failure reports changed=unknown and operation reconciliation,
never changed=no without evidence: the message says to send the same manifest
again with the same operation id, which returns the original receipt if the batch
was applied and applies it if it was not. The caller's epoch is always the
"requested" epoch and the store's the "active" one.

### CLI batch verb (`nova-table batch`)

`nova-table batch (<manifest-file> | - | '<json>') [--redis <addr> | --seat <name>] [--epoch <n>] [--actor <name>] [--receipt=true|false] [--json]`
executes an atomic conditional mutation manifest against one table in a single Redis call (`ns_table_apply`).
The manifest is a file path, `-` for stdin, or inline JSON that starts with `{`. A path that cannot be
read is refused with the path and the operating system's error, never as a JSON error.

#### Manifest structure

The manifest is a JSON document containing `schema`, `table`, `epoch`, `expected_table_revision`, `operation_id`, optional `actor`, and `members` (an array of member mutation and guard objects). The manifest states its epoch. `--epoch <n>` given on the command must equal it, and a difference is refused, naming both, before the store is asked; `--actor <name>` given on the command must equal the manifest's actor when the manifest names one, and fills it when the manifest names none. `create` needs a `score`.

#### Worked example

From an empty store: a table with one row, a member created by a first batch, then a batch that guards that member and moves it and creates another. The lines after each command are what the last command of the block prints.

```sh
nova-table create demo --columns ready,working,done
nova-table row add demo build
cat > seed.json <<'EOF'
{
  "schema": 1,
  "table": "demo",
  "epoch": "0",
  "expected_table_revision": "2",
  "operation_id": "seed",
  "actor": "coordinator",
  "members": [
    {
      "id": "m1",
      "expect": {"absent": true},
      "create": {"row": "build", "col": "ready", "score": 1},
      "set": {"role": "builder"}
    }
  ]
}
EOF
nova-table batch seed.json
```

```text
TABLE BATCH table=demo operation=seed epoch=0 table_revision=2->3 outcome=changed selected=1 guards=0 changed=1 replay=no trips=1
TABLE RECEIPT event=1727570000000-0 epoch=0 before=2 after=3 outcome=changed
MEMBER m1 place=-->build:ready score=-->1 member_revision=0->1 fields={"role":[null,"builder"]}
```

```sh
cat > manifest.json <<'EOF'
{
  "schema": 1,
  "table": "demo",
  "epoch": "0",
  "expected_table_revision": "3",
  "operation_id": "op-42",
  "actor": "coordinator",
  "members": [
    {
      "id": "m1",
      "expect": {
        "revision": "1",
        "place": {"row": "build", "col": "ready"},
        "fields": {"role": {"equals": "builder"}}
      },
      "move": {"row": "build", "col": "working"},
      "set": {"status": "in_progress"}
    },
    {
      "id": "m2",
      "expect": {"absent": true},
      "create": {"row": "build", "col": "ready", "score": 10},
      "set": {"role": "tester"}
    }
  ]
}
EOF
nova-table batch manifest.json
```

```text
TABLE BATCH table=demo operation=op-42 epoch=0 table_revision=3->4 outcome=changed selected=2 guards=0 changed=2 replay=no trips=1
TABLE RECEIPT event=1727570000000-0 epoch=0 before=3 after=4 outcome=changed
MEMBER m1 place=build:ready->build:working score=1->1 member_revision=1->2 fields={"status":[null,"in_progress"]}
MEMBER m2 place=-->build:ready score=-->10 member_revision=0->1 fields={"role":[null,"tester"]}
```

Running the same manifest again applies nothing and returns the original receipt, marked as a replay:

```sh
nova-table batch manifest.json
```

```text
TABLE BATCH table=demo operation=op-42 epoch=0 table_revision=3->4 outcome=changed selected=2 guards=0 changed=2 replay=yes trips=1
TABLE RECEIPT event=1727570000000-0 epoch=0 before=3 after=4 outcome=changed
MEMBER m1 place=build:ready->build:working score=1->1 member_revision=1->2 fields={"status":[null,"in_progress"]}
MEMBER m2 place=-->build:ready score=-->10 member_revision=0->1 fields={"role":[null,"tester"]}
```

`member read` shows the members as they are now, and a member that does not exist:

```sh
nova-table member read demo m1 m2 zz
```

```text
TABLE READ table=demo epoch=0 table_revision=4 members=2 missing=1 trips=1
MEMBER m1 place=build:working score=1 member_revision=2 fields={"role":"builder","status":"in_progress"}
MEMBER m2 place=build:ready score=10 member_revision=1 fields={"role":"tester"}
MISSING zz
```

#### Output format

On success `batch` prints a summary line with the table, operation, epoch, the table revision before and after (`table_revision=<before>-><after>`), outcome, the selected, guard-only and changed entry counts, `replay=yes` when the receipt is the one recorded for an operation already applied (`replay=no` otherwise) and the trip count; the commit receipt (`TABLE RECEIPT`, whose `before` and `after` are table revisions, as on every write verb); and one `MEMBER` line per member in the manifest with its place, score and `member_revision` before and after, and its changed application fields as one JSON object of `[before, after]` pairs (`null` is absent). `-` is an unplaced member or an absent score. A score is the exact decimal string the store holds.

A revision is always labelled for what it counts: `table_revision` is the table's counter, `member_revision` a member's. `--receipt=false` suppresses the `TABLE RECEIPT` line. A request that changes nothing (`changed=0`) is a no-op batch: it prints `outcome=noop` and, like any accepted batch, advances the table revision by one.

`--json` prints the same receipt as one line of JSON for a program: `table`, `operation_id`, `epoch`, `table_revision` (`{"before", "after"}`), `outcome`, `selected`, `guards`, `changed`, `event`, `replay` (a boolean), `trips`, and `members`, each with `id`, `place`, `score`, `member_revision` (each a `{"before", "after"}` pair; `null` is none) and `fields` (name to `[before, after]`). Revisions and scores are decimal strings.

#### Reading members

`nova-table member read <table> <id>... | <table> --cell <row:col>... [--at-epoch <n>] [--json]` is the read set as a verb: one exchange, one consistent snapshot. It prints a summary line (`TABLE READ` with the table, epoch, `table_revision`, and the counts of members found and missing), one `MEMBER` line per member found with its place, score, `member_revision` and fields, and one `MISSING` line per id that does not exist; `place=-` and `score=-` are an unplaced member. `--cell` reads every member of a cell (repeatable). `--json` prints one object with `table`, `epoch`, `table_revision`, `members` (`id`, `place`, `score`, `member_revision`, `fields`), `missing` and `trips`. The next command a refusal suggests for a member's state is this verb. `nova-table member find <table> <id>` reports only where a member is, with the table's revision as `table_revision`.

`nova-table show` prints a cell that did not come back as `?`, never as a false 0, and says which: a warning line on stderr names the row, the column, the key and the type found, and `show` exits 1 (`render` and `watch` keep drawing the `?` and exit 0).

#### Error handling and exit codes

- `0` (`done`): The batch commits successfully, or an identical request replays without additional side effects.
- `1` (`refused`): A manifest that reads as one and is refused by any rule (a bound, a repeated member id, a field both set and unset, a reserved field, a create with a move, an absent with a revision, an empty `members` array), and any refusal by the store (a precondition, epoch check, revision check, field guard). `batch` prints the operation, the member at fault, the state expected against the state found, `code=<CODE>`, `changed=no` and a next command on stderr. The table store is unchanged.
- `2` (`usage` or `connection`): A manifest that cannot be parsed or read (a missing or unreadable file, not JSON, an unknown key, a value of the wrong type, named by its place in the manifest: `score must be a JSON number, found a string at members[0].create.score`), `--epoch` or `--actor` differing from the manifest, and a store that cannot be reached or did not answer. When the store did not confirm a batch the message says `changed=unknown` and to run the same manifest again with the same operation id.

`-h` prints the complete usage banner to stdout at exit 0 with empty stderr:

```text
usage: nova-table batch (<manifest-file> | - | '<json>')

example:
  nova-table batch manifest.json
  nova-table batch - < manifest.json

flags:
  --json  print the receipt as one JSON object instead of the lines

connection:
  --redis <string>  the Redis address (else NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, then the seat's)
  --seat <name>  dial as this seat: its seats.tsv row, else the nova-secrets seat of that name

write epoch and receipt:
  --actor <string>  actor recorded with the change; it must equal the manifest's actor when the manifest names one
  --epoch <uint>  the epoch this write observed; it must equal the manifest's epoch
  --receipt  print the committed event ID, epoch and revision

exit codes: 0 done, 1 refused, 2 usage
```

### Model

No model of the batch action is in this tree. `tla/EpochMemberTable.tla` models the
per-verb member/epoch table over the one-place and epoch definitions; it has no batch
action, member revisions, field guards, operation records or receipts. What checks the
batch here is the tests.

The tests: trip and commit tests with complete unchanged-store refusal tests; bounded
randomized batches on owned Redis (16 fixed seeds, 128 steps each) with runtime checks;
Go receipt replay with one batch receipt per action; N=1 and the maximum configured N
separately, including a late invalid entry, interacting moves and a replay; and the
ordinary callers' regression suite.
