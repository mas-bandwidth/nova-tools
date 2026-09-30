# Table-set contract

This page introduces the `tset/1` table-set layer and its Layer 2 boundary for
readers new to the design. It summarizes the normative interface; it is not an
implementation report.

**Contract status:** This is an explanatory guide to the published Layer 1
revision-4 contract in the message bus (`design/L1-CONTRACT.md`) at SHA-256
`e055d64d0f6fc6e4d9bc587bf3a6789c1cacb28cbd6d4fda62a760459907973e`,
not the normative contract or a G0 acceptance. Revision 4 resolves rowset
guarding, explicit fences, and the checked read-extension seam. Rowan confirmed
this amended pin, and Johnny renewed its unchanged nine Layer 2 alignments.
Layer 2 revision 2 and its remaining wire choices are pending. Source at
`72177fe33` is a historical work-in-progress checkpoint; this page does not
establish required tests, size gates, model checks, or composed behavior.

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

1. `S.open` bounds and decodes the exact original request, validates static
   shape and server configuration, then checks the original receipt. A
   matching receipt returns immediately. It seals the caller's fence bit,
   rowset prefix, and advance position and source epoch before preplanning.
2. An original `fence:true` request takes the receipt-only
   `S.fence_prepare` → `S.commit` path after replay lookup. It does not enter
   an enclosing preplan, table planner, or Layer 2 planner.
3. For an ordinary step, an optional enclosing planner may call
   `S.before(ctx, table, ids, fields)`
   to read a cached, ID-keyed before-state projection. It may derive and append
   entries and notes from the observed state before `S.plan`; it cannot change
   epoch, space, operation identity, result, the original rowset prefix, fence,
   or advance.
4. `S.plan` revalidates the complete staged request, including its combined
   effective bytes, and builds the table plan.
   Layer 2 plans the log from that table plan. All planners use checked read,
   budget, and command-descriptor helpers; planning never writes.
5. `S.prepare` validates the combined command list, access declarations,
   budgets, receipts, and already encoded success reply. One `S.commit` then
   executes that frozen list; it performs no reads, state-dependent branches,
   validation, splitting, or JSON encoding.

The current Lua foundation exposes `S.run_context(fn, ...)` as a terminal
registered-callback wrapper. The Layer 1 write and read callbacks use it to
bound one invocation's private sealed context and read ledger: it clears them
on entry and exit, including when the callback throws, and refuses a nested
terminal scope. An upper composed writer wraps its whole invocation once;
an upper reader calls `S.read`, which already supplies that scope, without
adding another wrapper. Direct users may explicitly release an active context
through `S.release_context(ctx)`. This is source lifecycle plumbing for composition,
not a separate planning or commit path; composed runtime validation remains
pending.

A refusal leaves the call's keys unchanged. A transport loss, cancellation
after dispatch, or unexpected runtime error has an unknown outcome. The caller
must retain the exact request bytes and retry with the same receipt identity;
the client never invents a new identity to retry.

The foundation calls are `FCALL ns_tset_step 0 tset/1 <request-json>` and
`FCALL_RO ns_tset_read 0 tset/1 <read-plan-json>`. The Layer 1-only profile is
for isolated tests and uses an empty log plan. A complete composed foundation
profile invokes Layer 2 for each fresh ordinary step. The proposed Sprint
profile would expose its own writer and readers while omitting
`ns_tset_step`; that production surface is not present at source `72177fe33`.
The server-selected profile cannot be weakened by a request.

`Steps` pipelines independent, fully encoded steps in one flush and returns
one aligned result per input. A refusal in one slot does not refuse its
siblings; atomicity is per step, not across the pipeline. The Go entry point
is `tset.NewRedis(address, username, passwordEnvVar, options...)`, followed by
`Step`, `Steps`, or `Read`. It creates and owns a standalone go-redis client
with built-in retries disabled and no caller-installed hooks; it never accepts
a raw client or raw go-redis options. An empty `passwordEnvVar` means no
password, while a named but unset variable is a constructor error. Call
`RedisStore.Close()` when finished. The only optional connection metadata is
`tset.WithClientName(name)`, which cannot change retries or install hooks.
Construction neither contacts Redis nor loads a Function library.
`fn.LoadTSet(ctx, client, profile)` is a separate isolated-server setup call;
`fn.TSetStandalone` selects the Layer 1-only test profile, while
`fn.TSetComposed` requires the Layer 2 source. `tset.NewMem()` supplies the
in-memory Layer 1 counterpart for tests and planners.

## A batched write

This example assumes a configured `work` table at active epoch `7`. It adds
a row and creates two members in that row in one step. Cell references are
`<row>:<column>`.

```json
{
  "epoch": "7",
  "space": "demo:",
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

For ordinary steps, `op` and `intent` appear together or neither appears;
every `advance` requires both so its clear outcome can be settled after a lost
reply. A caller-supplied nonempty `notes` array also requires both. A trusted Lua preplan may append notes
derived from real state to an op-less step; those notes are checked with the
entire effective request and are never resent as caller input. Intent is
immutable, caller-owned UTF-8 text describing the semantic operation and
stable part; the receipt digest covers those exact intent bytes, not a newly
observed request. In the composed profile, each member-changing entry
supplies an `about` primary ID aligned with its member ID. Duplicate member intent is
combined before submission; a member cannot occur twice in entries for the
same table.

Entries include `create`, `move`, `remove`, `guard`, `count`, `rcount`, `rows`,
`rowset`, and `advance`. `create` requires an absent record. `move` checks the
expected source and optional revision; an omitted or identical destination
means stay.
`remove` removes placement but retains the record and fields. Guards and counts
observe pre-state. Row deletion is allowed only when the row is empty after
the whole step; a deleted row cannot receive a member in that step. A leading
`rowset` entry asserts a table's complete named rows and exact rank strings
in the **request epoch**. Ranks are canonical nonnegative decimal strings at
most `9007199254740991`. A mismatch refuses `ROWSET` without writes. An
empty `rows:[]` assertion checks emptiness without enumerating rows. For a
topology-preserving clear, guard every configured table, including empty ones:
one `rowset` per table, followed by `advance` as the first non-rowset entry,
then successor-epoch restoration. Guarding and restoring the same `(table,row)`
pair counts once toward the 1,024-pair advance limit. Duplicate row names,
a second rowset for one table, or a rowset without/after advance refuse
`REQUEST`. A rowset guard itself adds no write or log line; the surrounding
named step may still write a receipt. Scores are decimal strings accepted by
the target Redis `ZADD` score parser; invalid,
non-finite, whitespace-padded, or nonzero-underflowing values refuse
`REQUEST` before writes.

For example, these entries require the old epoch's `work` rows to be exactly
`ready` at rank `0`, then create epoch `8` and restore `ready` there:

```json
[
  {"kind":"rowset","t":"work","rows":[{"row":"ready","rank":"0"}]},
  {"kind":"advance","from":"7"},
  {"kind":"rows","t":"work","add":["ready"]}
]
```

An explicit fence settles an uncertain **named** part as not applied if its
original receipt has not won. It sends the same original epoch, `op`, and
`intent`, with `fence:true`, `entries:[]`, no notes, and no result or enclosing
effects:

```json
{"epoch":"7","space":"demo:","op":"part-01","intent":"stable arguments","fence":true,"entries":[]}
```

The flag must be literal `true` in the original request; an ordinary empty
named step is still an ordinary `ok` step. If the original completed first,
the fence replays its saved `ok`. If the fence wins, it stores and returns
`fenced`; a later copy of the original replays `fenced` and cannot apply its
effects. A `done:absent` read after an unknown dispatch does not prove that the
first copy has stopped: resend the same identity or fence it before choosing
a new operation. `STALE` and `OPCONFLICT` definitively settle identity; a
fence refused with another code writes nothing and settles nothing.

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
replanning unfinished parts. After a crash, an unfinished part counts as
possibly dispatched unless a durable resume manifest recorded that it was
never sent. `done` preserves whether a receipt is `ok` or `fenced`;
`fenced` is an explicit not-applied outcome, never a claim that requested
effects succeeded. Multi-step resume is not a transaction.

## In-memory twin observation

`tset.NewMem()` supplies the Layer 1 table and receipt twin. For a fresh
ordinary successful step, its Go `Reply.MemPlan *MemPlan` exposes the
normalized combined entry order, effective row changes, and aligned before
and after member records. `MemPlan.Before` also includes observed guards and
no-op members. This is for upper-layer tests of derivation against the actual
Layer 1 plan; it is tagged `json:"-"`, never stored in a receipt, and never
appears in a Redis/Lua reply. Replay and fence replies do not synthesize one.
It is not a Layer 2 log twin, a Sprint `Twin` type, or a G0 condition.

The published S15 nonwire signatures are `Mem.Plan(step Step) (*MemPlan, *Refusal)`
and `Mem.Commit(plan *MemPlan) (Reply, *Refusal)`. They are Mem-only and add
no Redis/Lua wire field. `MemPlan.Replay` observes the captured reply; the
private prepared plan remains authoritative. A composed twin commits that
plan immediately on replay or an original fence, before downstream planners.
Its three focused unit witnesses passed in the frozen 119-test selection;
composed atomicity validation remains pending.

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

Revision 4 also defines one checked read extension for an enclosing Sprint
reader: `S.read(version, raw, L.read, {kinds, validate, read})`. The registry
is a dense list of unique query kinds; every query has a `kind` and every
answer aligns with its query. Its pure `validate` callback checks all
extension queries before TIME or store reads. Its `read` callback uses the
same requested epoch, sampled TIME, refusal envelope, and shared budgets as
Layer 1 and Layer 2. The Sprint registry includes `related`, `front`,
`waiters`, `streams`, `fleet`, `readers`, `needchain`, and `jnote`; it may also
register bounded Sprint-key kinds for clock, lease, tick, heartbeat and other
upper-layer indexes. Those upper query meanings are not defined by this page.

The extension uses `S.ensure_read_table` for a table discovered inside a
query, `S.read_record` for a checked record projection, `S.read_probe` for
reply-size-bounded scalar or named-field probes, `S.read_range_head` for
bounded sorted-set heads, and `S.emit_read_item` to charge nested output
before appending it. Repeated record and selected-field **occurrences** count
even if the same payload is cached. A retained-epoch record uses that epoch's
definition snapshot; a missing snapshot refuses `EPOCHGONE`. Collection
enumeration cannot enter through a generic probe. Layer 2's proposed
`L.read_line_at(ctx, seq, query_index)` must validate the exact sequence and
charge the same log/fetch/probe budgets. These Lua helpers are present in the
Layer 1 work tree, but no Sprint Function or Go Sprint query API is exported
there. The exact Layer 2 cursor/body wire remains an acceptance gate.

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
| Distinct write-record observations across preplan and table plan | 6,000 `(table, stored ID)` pairs |
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
preceding typed read. TIME counts store work and fetched bytes but not a
cell/key probe. The 20,000 probe cap counts actual cell, structural, TYPE and
log-key probes; record/field fetches still count commands and their own
observation units. `LIMIT` means a write or requested maximum exceeds a
hard bound; `BUDGET` means a read cannot fetch or encode a complete answer or
next item; `REQUEST` means malformed or unsupported input. Predictable
refusals are returned before commit. Unexpected runtime/transport failures
are `OUTCOMEUNKNOWN`, not a refusal promising no change.

## Profiles and evidence

The revision-4 contract keeps `table.lua` on **old** tables and requires the
new tset profile to register only tset callbacks: no legacy read or write
callback may address the new-engine store. The eventual Sprint profile adds
its own registered functions after G0. At source `72177fe33`, the WIP
`fn.TSetSource` filter still admits eight named legacy read callbacks from
unchanged `table.lua` (`ns_table_check`, `ns_table_list`,
`ns_table_member_find`, `ns_table_members`, `ns_table_read`,
`ns_table_read_set`, `ns_view_get`, `ns_view_list`). Source inspection finds
matching registrations in `table.lua`; the exact installed surface needs its
own functional check and reconciliation with the revised contract. The
loader also refers to `lua/table_set_log.lua`, which is absent at this source
checkpoint, so its composed profile cannot yet assemble. The standalone
Layer 1 fixture and `tset.NewRedis`/`tset.NewMem` APIs do not close that gap.

The published `e055d64d` revision-4 pin is not G0. Rowan confirmed the
amendment at that exact hash; Johnny renewed the unchanged nine Layer 2
alignments against it. Layer 2 revision 2, including its open wire choices,
a real composed source, the revision-4 model/TLC evidence, and the required
composed comparison and size gates remain separate work. Neither confirmation
is a runtime, model, size, or Emma disposition pass. The saved
`observation-boundaries-validation-sol.md` report records 97
selected non-stub Layer 1 units passing, along with dependency graphs and
functional compilation. Later `lua-read-field-boundary-validation-sol.md`
and `lua-read-fetched-boundary-validation-sol.md` reports record passing
graphs and functional compilation. Each full tset functional run on pinned
Redis 8.10.2 remained red only for 17 diagnostics loading the absent
`lua/table_set_log.lua`; non-verbose output did not provide individual PASS
lines. The reports live in the Stella review dispatch's `l1-build-dispatch`
directory and predate later source-`72177fe33` edits, so they do not certify
those edits. The tracer's measurements on an earlier Redis version are not a
Redis 8.10.2 performance pass. No 1,000,000-card composed memory or latency measurement,
actual-table replay measurement, or revised model proof is established here.
A Layer 1-only pass cannot stand in for a composed Layer 1/Layer 2 gate.
