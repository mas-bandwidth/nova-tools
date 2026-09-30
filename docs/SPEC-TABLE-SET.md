# Table-set contract

This page introduces the `tset/1` table-set layer and its Layer 2 boundary for
readers new to the design. It summarizes the normative interface; it is not an
implementation report.

**Contract status:** Layer 1 contract revision 3 is a review-pending candidate.
Rowan's accepted authorization for implementation against revision 2 plus
amendments A2–A12 remains binding. Revision 3 consolidates those accepted
amendments for review; publishing this summary does not mean revision 3 has
been accepted, nor does it withdraw any accepted amendment. Required tests,
size gates, model checks, and implementation results below remain unmeasured
unless supported by separate execution evidence.

## Purpose and ownership

The layer changes a bounded set of table members in one Redis Function call.
A member is identified by its stored ID and occupies at most one cell per
table. A cell is an ordered set; each member has a score, and each table has an
ordered list of rows. A step can combine member changes, row changes, and
guards across at most four configured tables. Callers submit sets of members
through `Step` or `Steps`; there is no per-card API and no need to make one
call per ID.

Layer 1 owns table records, cells, row order, epoch/catalog markers, and retry
receipts. Layer 2 owns the append-only event log, per-primary histories,
sequence allocation, and `last`, `lines`, and `cardlines` reads. The two
planners share observations and budgets, then submit their prepared commands
to one executor. Layer 2 cannot commit independently or mutate the table plan.

## Atomic write path

Every predictable error and the complete reply must be found before the first
write. The path is:

1. `S.open` bounds and decodes the exact request, validates static shape and
   server configuration, then checks the original receipt. A matching receipt
   returns immediately.
2. An optional enclosing planner may call `S.before(ctx, table, ids, fields)`
   to read a cached, ID-keyed before-state projection. It may derive and append
   entries and notes before `S.plan`; it cannot change epoch, space, operation
   identity, result, or introduce/alter `advance`.
3. `S.plan` revalidates the complete staged request and builds the table plan.
   Layer 2 plans the log from that table plan. All planners use checked read,
   budget, and command-descriptor helpers; planning never writes.
4. `S.prepare` validates the combined command list, access declarations,
   budgets, receipts, and already encoded success reply. One `S.commit` then
   executes that frozen list; it performs no reads, state-dependent branches,
   validation, splitting, or JSON encoding.

A refusal leaves the call's keys unchanged. A transport loss, cancellation
after dispatch, or unexpected runtime error has an unknown outcome. The caller
must retain the exact request bytes and retry with the same receipt identity;
the client never invents a new identity to retry.

The foundation calls are `FCALL ns_tset_step 0 tset/1 <request-json>` and
`FCALL_RO ns_tset_read 0 tset/1 <read-plan-json>`. The Layer 1-only profile
exists only as a test fixture with an empty log plan. The composed foundation
profile always invokes Layer 2. The Sprint profile exposes the Sprint writer
and readers and omits `ns_tset_step`. A request cannot select a weaker
profile.

`Steps` pipelines independent, fully encoded steps in one flush and returns
one aligned result per input. A refusal in one slot does not refuse its
siblings; atomicity is per step, not across the pipeline. Use a standalone
Redis client with automatic retries disabled: construct go-redis with
`MaxRetries: -1`, and verify the effective retry setting before dispatch. A
retry-enabled, Cluster/Ring, or unverifiable client is refused as
`UNSUPPORTEDSTORE` before sending a command.

## A batched write

This example adds a row and creates two members in that row in one step. Cell
references are `<row>:<column>`.

```json
{
  "epoch": "7",
  "space": "demo",
  "op": "seed-batch-01",
  "intent": "{\"verb\":\"seed\",\"part\":\"01\",\"ids\":[\"task-a\",\"task-b\"]}",
  "entries": [
    {"kind": "rows", "t": "work", "add": ["ready"]},
    {
      "kind": "create", "t": "work", "to": "ready:queued",
      "ids": ["task-a", "task-b"], "scores": ["10", "20"],
      "about": ["task-a", "task-b"]
    }
  ]
}
```

`op` and `intent` appear together or neither appears. Intent is immutable,
caller-owned UTF-8 text describing the semantic operation and stable part;
the receipt digest covers those exact intent bytes, not a newly observed
request. In the composed profile, each member-changing entry supplies an
`about` primary ID aligned with its member ID. Duplicate member intent is
combined before submission; a member cannot occur twice in entries for the
same table.

Entries include `create`, `move`, `remove`, `guard`, `count`, `rcount`, `rows`,
and `advance`. `create` requires an absent record. `move` checks the expected
source and optional revision; an omitted or identical destination means stay.
`remove` removes placement but retains the record and fields. Guards and counts
observe pre-state. Row deletion is allowed only when the row is empty after
the whole step; a deleted row cannot receive a member in that step. `advance`
must be first and carries the complete new-epoch restoration work. Scores are
decimal strings accepted by the target Redis `ZADD` score parser; invalid,
non-finite, whitespace-padded, or nonzero-underflowing values refuse
`REQUEST` before writes.

## Epochs, catalog, and receipts

Epoch and revision values are canonical decimal strings over the full uint64
range. A stale write refuses `STALE`; an epoch above active refuses
`EPOCHAHEAD`. A matching original receipt is checked first, so a completed
operation can replay after later advances. An advance receipt remains in its
request epoch while its table/log writes use the successor epoch. Live log
sequence metadata has the stricter exact ceiling `9007199254740991` and
refuses `OVERFLOW` before table mutation if the next sequence would exceed it.

The active marker is `{space}sprint:epoch` and carries `engine`, active `n`,
and `tables`. Each retained epoch has `{space}sprint:epoch@<e>` with the same
catalog, including epoch zero. `tables` is the JSON array of all configured
definitions: zero to four unique table names, at most 2,048 encoded bytes.
It is not limited to tables mentioned by the current request. Advance loads
and snapshots every definition in that catalog, including when no restoration
entry names it; a bad catalog refuses `CONFIG` before writes.
Layer 2 log, primary-history, and receipt keys always include the epoch suffix:
`{space}sprint:log@<epoch>`, `{space}sprint:cl:<about>@<epoch>`, and
`{space}sprint:done@<epoch>`. Epoch zero therefore uses `@0` too.
The event log is append-only within a retained epoch; it is not trimmed or
expired in place.

The receipt identity is `(space, request_epoch, op)`. The stored receipt keeps
only the intent digest, status, before/after epochs, sequence bounds, total
changed count, and caller result. A matching replay returns the compact stable
schema:

```json
{
  "status": "ok", "epoch_before": "7", "epoch_after": "7",
  "changed": 2, "first_seq": "41", "last_seq": "41",
  "result": "", "replay": true
}
```

Replay omits `guarded`, `changed_per_entry`, `lines`, and `counters`; it does
not rerun preplanning, table/log planning, TIME, or current guards. Fresh
success has those fresh-call fields. Receipts remain across clear until
authorized teardown; there is no silent expiry or trim. Resume logic keeps
stable per-part identities and checks the set of done identities before
replanning unfinished parts. Multi-step resume is not a transaction.

## Reads and pagination

`Read` sends one bounded read plan of up to 1,024 queries. Layer 1 query kinds
include `range`, `count`, `rcount`, `ids`, `rows`, and `done`; Layer 2 handles
`last`, `lines`, and `cardlines`. Atomic mode returns all answers or one
refusal with no partial answers. Every successful read includes the active
epoch observed in that call, even when reading retained historical data. A
`rows` answer contains the ordered rows and exact decimal ranks, for example:

```json
{"kind":"rows","rows":[{"row":"ready","rank":"0"}]}
```

Only `lines` and `cardlines` support page mode. First-page high-water marks
remain fixed for that cursor chain; a cursor advances only after complete
items are included. A nonempty page must advance at least one position or
refuse `BUDGET`; it cannot return a successful non-progressing cursor. For
`cardlines`, each primary has its own list-index high-water. `through_index`
may be `-1` only for an empty history; supported list indices are exact
integers from 0 through `2^53-1`. Log sequence values remain decimal strings.
Each projected primary-history item is capped at 512 KiB; the complete read
reply, including its envelope, is capped at 8 MiB. No field/effect is
truncated to fit.

Range reads may address a table cell or an authorized sorted-set key. A raw
key must start with the configured **literal space prefix** and pass client
ACL; the prefix is not automatically delimited or rewritten. Configure
adjacent namespaces so their prefixes are disjoint when required. Supported
sorted sets, including upper-layer indexes, must be maintained by the
supported writer and contain valid UTF-8 member identifiers of at most 256
bytes. That raw-index bound is a supported-state invariant used to reserve
fetch budgets; detecting an oversized externally mutated member after fetch
can only report `DRIFT`, not prevent the allocation. A raw range read does not
claim table-record or placement invariants for that set. There is no arbitrary
write-key API.

## Bounds and refusals

These are per-call hard ceilings, not recommended production chunk sizes.
MiB is 1,048,576 bytes. The builder must measure down from the 2,000-candidate
admission ceiling to meet the composed latency gate.

| Resource | Contract bound |
| --- | ---: |
| Encoded write/read request | 4 MiB each, before JSON decode; combined preplan write also 4 MiB |
| JSON nesting / configured tables / entries / queries | 16 / 4 / 256 / 1,024 |
| Mutation candidates / additional guard-only members | 2,000 / 4,000 |
| IDs per entry / distinct table-row pairs, normal / with advance | 2,000 / 100 / 1,024 |
| Columns per table / fields stored per member | 32 / 128 |
| ID, row, column, field, operation name | 256 UTF-8 bytes each |
| Effective set/each fields, unset names, before_fields | 128 each per member/projection |
| Field value / caller result / intent | 64 KiB / 4 KiB / 64 KiB |
| Notes / aligned about IDs before dedup | 100 / 4,000 |
| Planned commands / encoded argv bytes | 65,536 / 8 MiB |
| Raw fetched payload / encoded read reply | 8 MiB / 8 MiB including envelope |
| Cell/key probes per read or write | 20,000 |
| Field observations, read / write | 1,280,000 / 768,000 |
| Records returned / range IDs returned | 10,000 / 20,000 |
| Log lines / IDs in lines / IDs per line | 5,000 / 200,000 / 2,000 |
| Encoded line / primary histories or done identities per query | 1 MiB / 2,000 / 2,000 |
| Cardlines projected item | 512 KiB |
| Encoded receipt | 32 KiB |

Read reservations and fetched bytes share one budget across Layer 1 and Layer
2. Reserve the validated worst-case payload before a typed read. Fetch the
requested projection directly when that reservation fits; otherwise use
bounded length/presence probes and fetch only values that fit. Empty text and
absence are distinct. Type probes are for keys to be written without a
preceding typed read. `LIMIT` means a write or requested maximum exceeds a
hard bound; `BUDGET` means a read cannot fetch or encode a complete answer or
next item; `REQUEST` means malformed or unsupported input. Predictable
refusals are returned before commit. Unexpected runtime/transport failures
are `OUTCOMEUNKNOWN`, not a refusal promising no change.

## Profiles and evidence

`table.lua` remains unchanged. The production tset library profile must filter
registrations: expose new tset/Sprint writers and only the required legacy
read-only callbacks. Legacy table read, `read_set`, check, member/list, and
view-read callbacks used by watch/check must be marked no-writes; old mutating
callbacks must be absent from the installed function set. The legacy
read-only adapter and exact allow-list remain an implementation gap to verify;
unchanged `table.lua` alone does not establish compatibility. In particular,
old table/member/view mutators must not be callable against the new-engine
store. The normal old-tool library remains on its existing server.

Required correctness, Lua/twin agreement, whole-store, Redis 8.10.2, memory,
and latency gates have not been shown as passing by this contract. The tracer's
Redis 7.4.11 measurements are not proof for 8.10.2; the 1,000,000-card
composed gate, TLA+ model/TLC result, actual-table replay measurement, and
legacy adapter allow-list are still unmeasured. Treat every named test as a
required gate, not as a completed result. A Layer 1-only test pass cannot
stand in for the composed Layer 1/Layer 2 gates.
