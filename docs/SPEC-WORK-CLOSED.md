# nova-work: closed history that can keep growing

Status: proposed amendment to [SPEC-WORK.md](SPEC-WORK.md), against draft 24
`e79847fbd79fc647bfecce0e70c5849da44698d7`. This is a storage and query contract,
not an implemented runtime or a claim of measured savings. Circulate with the
existing nova-work review; do not create a second work-set owner.

Glenn asks that C be partitioned by day because closed history grows without
bound. A friend asking what finished in the last few hours should read those
hours of history, not reload everything the team has ever done.

## What remains the same

O is open work. W is the leased working view inside O. C preserves closed history.
Stable item IDs, required-set membership, acceptance evidence, source mappings
and the existing single-coordinator journal remain authoritative. Closing a fix
does not close a separately represented release task. Closed does not imply
verified. Reopening preserves earlier history and returns the same item ID to O.

C's historical records and O may mention the same ID after a reopen; their
**current membership views** partition item IDs. Historical occurrence and
current membership must never be conflated in validation or counts.

## Daily partitions and bounded indexes

Partition closure records by the event's recorded closure day in UTC. For example,
`closed/2026/09/14/` names a day, with bounded immutable segments within it.
This layout is illustrative, not a second public command interface. Each record
has a stable event ID, item ID, event revision and recorded timestamp; repeated
close/reopen cycles create distinct events for the same item.

A reopen or correction is appended at its own event time and references the
original identity. Do not rewrite an old closure event, migrate it to today or
delete it because the item is open again. A dated manifest names the immutable
segments, their revision/time bounds, hashes and record counts.

The clip's small root names a versioned on-disk index root. Index pages locate
partitions by day and records by stable item/event ID and repository. Pages and
segments have explicit byte/record bounds. A busy day may have multiple segments;
"one file per day" is not permission for one unbounded read.

The resident session loads O and a bounded index cache. It must **not** load one
row for every historical closed item, nor all evidence attached to every such
item, at startup. No unbounded in-memory directory or all-ID map may silently
replace the unbounded closed snapshot. The index is rebuildable from retained
canonical events and records; Redis, if used, is only an optional cache.

Request deduplication needs the same treatment. Retain durable request-ID,
payload-digest and accepted-revision lookup on disk, with bounded index pages
and a bounded resident cache. Moving C out of memory while retaining every past
request in one resident dedup map does not meet this contract. A retry after day
rollover, clipping or coordinator handoff cannot execute again; a reused ID with
a different payload is still refused. An unavailable dedup page is a refusal to
admit that request, never evidence that it is new. Inherit SPEC-WORK draft24
lines221–260: one request-ID namespace across mutation kinds and its canonical
SHA-256 payload serialization/collision rules. The committed root names this
dedup state, not a machine-local ledger that disappears on handoff.

## Queries say which question they answer

The default closed-history window is the **rolling last 24 hours**, not the last
24 calendar dates or all of yesterday plus today. At startup and for a default
closed-history query, use `[now - 24h, now)` and open at most the two intersecting
UTC day partitions (today and yesterday). At exactly midnight only yesterday
intersects the half-open interval. This deliberately replaces draft24
lines1024–1030 requiring explicit --from/--to on every closed/root listing:
omit both for the default, provide both to select an explicit interval, and
refuse a single endpoint. With --at, default now is the selected revision's
recorded timestamp rather than the current wall clock; the reply names both.
Older history stays on disk and is accessed
only by an explicitly broader historical query or a required indexed lookup.

The time range is bounded; volume within those hours can still grow. Keep byte,
record and page limits as well: read the default window in bounded pages rather
than promise to keep arbitrarily many recent events resident. Report truncation
and continuation explicitly. Ongoing sessions advance the rolling window and
evict expired history pages from the bounded cache; this does not delete history.

Time intervals are half-open UTC `[from, to)`. Capture the query revision and
`now` once. Select intersecting day partitions through the date index, then apply
exact timestamp bounds. Do not list all historical files and filter afterward.
A query crossing midnight reads both relevant days. Recorded event time chooses
the partition; revision remains the authoritative event ordering. Clock skew or
backdated supplied timestamps do not permit an index to assume time is monotonic
with revision. Only events at or before the captured revision are eligible;
exact recorded timestamps determine interval inclusion. The existing event
clock/provenance rules still apply, normalized to UTC. An explicitly backdated
or future event is not retimed silently; it appears only in a matching explicit
interval at a revision that includes it.

Keep two explicit views, using the existing query surface where possible:

- **Closure activity:** what closure events occurred in the interval? Show each
  closure event, including an item later reopened. Report event count and distinct
  item count separately. This is useful for retrospective work and cost analysis.
- **Item state:** select distinct item IDs with closure events in the interval,
  then ask their latest state as of the captured revision, considering transitions
  before the interval end. Count each item once, using its event history as of that
  point. A later reopen must not change an earlier historical answer.

A latest-row-only index cannot answer the second question for a time before that
row. Retain versioned per-item index entries or perform bounded indexed lookups of
its earlier transitions. A query needing missing history reports a coverage gap
or refuses; it never fabricates an earlier state from the latest row.

Document both the selected-row denominator and scope totals. Existing root-wide
aggregate counts may use revision-qualified materialized aggregates updated at
mutation time; do not rescan all C just to print `closed=` on a recent listing.
Changing a policy that requires a full rebuild must report that operation and its
cost. Keep pending release tasks visible in O while their fixes appear in C.

Activity rows order by `(event revision, event ID)` across day partitions;
item-state rows order by stable item ID. Event ID breaks ties within an atomic
envelope. A continuation binds the committed root/manifest identity, captured
revision, interval, query kind/scope and last ordering key. A changed binding
is a refusal; the wire encoding is a pilot decision, not an optional field.

Pagination is pinned to the captured revision, filter and ordering; a continuation
cannot drift because another item closed between pages. A stale/unavailable page
snapshot returns an explicit refusal, not skipped or repeated rows. Bound emitted
rows, bytes and index/body reads. Large complete reports may require multiple
bounded pages; never imply constant cost for output proportional to history.
Each invocation has a finite row, emitted-byte and scanned-byte budget. Reaching
one returns partial coverage plus a continuation, never an automatic loop
through further pages. Resuming is another explicit bounded invocation. This
permits requested full reports without one unbounded process response.

An absent day within a complete manifested range can mean no events. A manifest
or segment expected by the committed root but missing/corrupt means a coverage
gap. Preserve that distinction. Cached summaries may answer independent queries
with their provenance; they cannot make missing evidence become verified.

## One durable boundary, no lost closed work

One accepted mutation envelope carries the existing state, settle/revive and
lease effects. Append it durably before acknowledgment. Recovery replays the
unclipped journal overlay into O, historical lookup and aggregate/index state
before serving a current answer.

A clip publishes one revision naming O, immutable closed segments, historical
indexes, request dedup state and their manifests together. Stage new immutable
files, verify their referenced hashes, then commit the root and referenced files
in the same Git checkpoint. A failed push leaves locally durable pending work;
a missing acknowledgment is reconciled against the exact remote commit before
retrying publication. Do not advance a local shared-boundary receipt merely
because the commit exists locally. Inherit draft24
lines271–282's durable journal clip-boundary record and idempotent replay: a
crash after remote publication but before recording that boundary must reconcile
the exact committed root before replay, rotation or a second publication.

Closing/reopening must be all-or-none with index updates from the reader's
perspective, including after a crash before acknowledgment, after acknowledgment
before clip, and during publication. No item disappears from both current
membership views; no request is applied twice. Old committed roots remain
readable. No automatic history deletion or retention cutoff is introduced.

Roadmap references and required sets keep their stable IDs when members close.
Use revision-qualified indexed facts or bounded lookup for those dependencies;
if required facts are unavailable, return incomplete/unknown or refuse the
relevant validation. Do not claim every rule is valid with arbitrary historical
storage missing. Unrelated active queries can continue when their complete
inputs remain available and the result identifies its revision/coverage.

## Acceptance and an operational experiment

Correctness replays must cover:

1. The default rolling24h window at midnight, early morning and midday touches
   at most two UTC days; older partitions are not opened for the default listing.
   Closure either side of UTC midnight; exact interval endpoints; many segments
   in one day; empty day versus a missing expected segment.
2. Close, reopen and close the same ID on different days; unchanged past answers;
   distinct closure-event and latest-item counts; stable roadmap denominator.
3. Concurrent new events between query pages, with no missing or duplicated rows
   in the captured-revision result.
4. Crash at the journal/acknowledgment/clip boundaries; no lost item or duplicate
   request; retry an old ID after clip and successor handoff, including a payload
   collision and an unavailable dedup page.
5. An open roadmap referring to closed children; matching full reconstruction and
   incremental results; explicit unavailable-evidence behavior.
6. Increasing old history while holding O, recent-window volume and page size
   fixed: instrument startup resident bytes, index pages read, segment bytes read,
   parses, full replays and emitted bytes. No whole-C load, directory scan or
   whole-history dedup load on the ordinary path. A cold index lookup can depend
   on index depth; the requirement is bounded indexed access, not magic O(1).

Use the real security closeout report as an initial workload: findings across
repositories joined to fixes, releases, friends, models, benches and retained
usage pointers. Baseline the actual manual workflow and compare it with an
adopted pilot on equivalent report work. Retain native operational usage for the
coordinator, reviews, corrections and retries. Correctness tests or fewer bytes
alone do not establish a token-saving win. Implementation tokens are sunk cost
and excluded from the before/after success comparison. Unknown usage remains
unknown. Report verified savings, inconclusive evidence or a failed hypothesis
honestly; do not invent a percentage.

## Integration into draft 24

Before whole-spec approval, replace the conflicting clauses rather than keeping
two alternatives: the whole-history resident snapshot/index requirements in
Retention, the root's loaded closed index, the latest-row-only historical query
contract, the seventh loaded index in Cost, and unconditional absent-archive
validation claims. Keep the existing append-only settle/revive and one-journal
contracts. The day/index storage encoding is a bounded pilot decision; these
access and recovery properties are mandatory acceptance criteria.
