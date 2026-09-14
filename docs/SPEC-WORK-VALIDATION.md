# nova-work: preservation and recovery acceptance

Proposed release gates for [the main spec](SPEC-WORK.md) and
[resident/roadmap requirements](SPEC-WORK-PILOT.md). Tests demonstrate specific
failure protections; they cannot promise that every possible defect is absent.

## Independent oracle and retained evidence

Use immutable source captures and an independently implemented semantic comparator,
not only the production serializer to check itself. Preserve raw provider records
and a manifest of source IDs, revisions, counts and content hashes alongside
normalized work. Store originals byte-for-byte where the API supplies raw bytes;
API-normalized values must retain their declared semantics and provenance.

For every test retain input fixtures, deterministic seed, engine/client/schema
versions, fault point, invocation, captured revision, expected/actual reconciliation
and result. Distinguish fixture validation from a real read-only pilot. Unknown,
inaccessible, truncated or unsupported source fields must be visible; no silent
success by dropping them. Provider APIs may not expose deleted or private records;
claim completeness only for the declared observable inventory and capture scope.

## Required test suites

| Suite | Required cases and pass condition |
| --- | --- |
| Source inventory | Open/closed issues, comments, identities, labels, relationships, attachments and pagination. Every captured source record maps to a preserved original plus normalized mapping, or an explicit unresolved entry. Same counts with different IDs/content must fail reconciliation. |
| Read-only intake | Dry-run capture/plan and normal initial import cannot call source mutation endpoints. Use a recording adapter that fails on POST/PATCH/DELETE or equivalent mutation; compare remote inventory before/after. Applying a plan changes only the destination after revalidation. |
| Import replay | Repeat batches and interleave retries, overlapping pages, reordered records, interruption and resume. Stable source IDs produce exactly one mapping; no duplicate canonical work or lost comments. |
| Moving source | Edit a body, add/delete a visible comment, change labels/state and reopen during capture. Preserve captured versions, mark mixed/incomplete capture, reconcile newer observations. Never claim a consistent provider snapshot if unavailable. |
| Archive completeness | Missing attachment, unavailable comment, unsupported fields, size truncation, rate limit and mid-page failure remain explicit gaps and prohibit absorption. Mixed/external/unknown authors retain source issues. |
| Full data round trip | Export captured revision, load in a fresh isolated engine, export again. Compare all semantic fields, stable IDs, Unicode and literal text, order where meaningful, links, evidence, roles, CONFIG, ACTIVE observations, model/rate records, O/C history, roadmaps and accounting provenance. Derived caches rebuild to equivalent values. |
| Format determinism | Same state/schema produces identical canonical serialized bytes; preserve null/absent/empty distinctions, large integers, timestamps, escaping, multiline text and Unicode normalization differences. Do not require arbitrary provider JSON key order to be meaningful. |
| Old history | Export includes the full explicitly selected archive, including records outside the resident 24-hour window. Scope/omissions are declared. Restore an old completed roadmap and retrieve its exact proof without loading all C. A recent-only export cannot be labelled a full backup. |
| Referential integrity | Duplicate IDs, dangling references, cycles, conflicting parents, invalid cells, duplicate ownership and mismatched manifests fail before publication. A scoped export includes its dependency closure or explicit unresolved external references, never a falsely complete backup. |
| Atomic mutation | Inject failure before/during/after journal append, durable sync, apply, checkpoint write, rename and reply. Every acknowledged mutation survives process restart under the declared storage assumptions; torn unaccepted tails are diagnosed. No partial envelope or count/evidence split is admitted. |
| Retry/protocol | Lost reply, fragmented frames, disconnect, repeated ID/body, same ID/different body, invalid UTF-8/types/versions, oversized frames and deadlines. No duplicate accepted mutation or executable payload; outcome can be retrieved after uncertainty. |
| Async operations | Status/wait/cancel under busy import/export/clip, bounded queues and output, restart with pending operations, stale staged result and uncertain external effect. No double launch, false cancellation success or stalled control plane behind network I/O. |
| Batches and pipelines | Compare sequential commands with each batch mode under its declared revision semantics. Read bundles remain on one snapshot. Inject a bad middle command, changed revision, fragmented frames, lost replies, disconnect and crash at each acceptance boundary: atomic batches publish all or none; independent batches preserve exact accepted prefixes and mark unattempted entries. Same-ID retries never duplicate mutations; changed content refuses. Exercise per-entry errors, output limits, oversized atomic rejection, backpressure and control-plane responsiveness under bulk load. Verify O/W/counter consistency and prohibit external side effects within atomic batches. Measure matched operational token totals after adoption, not implementation cost. |
| Single writer | Two local processes, alias paths, stale socket, partitioned benches, lease expiry, delayed old owner and handoff crash. Existing fencing rules prevent stale mutation authority, not only stale Git pushes. Preserve exported unshared journal work. |
| Indexes and counters | Random legal verb sequences compared after each step with an independent full reconstruction. O counts and friend indexes agree; closure/reopen/reparent/shared references do not double-count. Instrument required constant-time queries and bounded historical paging. |
| Materialized W | Hold W fixed while growing O and C; repeated membership/count queries and the first read after mutations visit zero unrelated tasks, and listing visits only the selected W page. Compare W, its count and friend indexes with an independent reconstruction after take/renew/release/expiry, completion/cancellation/reassignment, duplicate attempts, undo and crash/replay. Fake-clock expiry uses the deadline index; expose delayed watermark instead of claiming freshness. Verify expiry retains uncertain remote executions and recovery does not advertise stale leases as live. |
| Roadmap proof | Full fixed-table prototype parity, optional axes, partial/stale evidence, shared prerequisites, newly discovered scope and closed members. Render chat/file identically; marker edits preserve every unrelated byte and refuse ambiguity. |
| Undo/redo | Reverse reversible edits, preserve history, redo only against valid preconditions. Exercise dependent later edits, changed criteria, close/reopen, decomposition, accounting receipts and uncertain external actions. Conflict is explicit and non-mutating. |
| Recovery | Restore newest valid checkpoint plus journal, reject corrupt checkpoints, recover from a prior checkpoint without silent loss, and compare an isolated old restore to current state. Missing tail or unavailable remote backup is reported as a recovery gap. |
| Schema evolution | Supported old schemas migrate losslessly using golden fixtures and semantic comparisons. Unsupported versions refuse while preserving originals; migration never rewrites the only source copy. |
| Hostile data and limits | Disable reader evaluation, enforce pre-parse depth/byte/node limits, reject path traversal and escaping archive paths; imported prose cannot execute commands or alter authority. Test deep/high-fanout inputs without quadratic copying. |

## Checkpoints, undo and redo

Every accepted mutation is locally journaled durably before success acknowledgment.
Create validated atomic local snapshots periodically by configured elapsed time
and accepted-event count. A snapshot names its schema, revision, journal boundary
and content manifest. Periodic clip/commit/push supplies separately observable
shared checkpoints; local success is not reported as shared backup success.
Expose checkpoint age, local/shared revisions, unshared work and failed backup
attempts. Keep the last known-good checkpoint while writing its replacement.

Retention and journal compaction must never remove the only recoverable copy of
accepted work or historical evidence. Pruning checkpoints is separate from
retaining C. A remote outage may leave new work only on this bench: make that
recovery exposure explicit. Machine/storage loss requires an independent verified
copy; a local journal alone does not protect against it.

Provide checkpoint list/create/verify and isolated restore/compare operations.
Restoration first opens a read-only or isolated non-dispatching recovery session.
It must not inherit current coordinator ownership, reanimate old assignments,
replay bus messages or duplicate external side effects. Promote selected repairs
only through a fenced, validated reconciliation with current state. Never reset
shared Git history as the normal undo mechanism.

Provide undo-plan/undo and redo-plan/redo against named accepted request IDs.
Each reversible verb records enough preimage/provenance to construct a typed
compensating envelope. Preserve the original event and add reversal lineage;
redo reapplies intent after current preconditions, not by deleting the undo.
Plans show affected nodes, dependencies, counters, verification and assignment
effects. Check expected revisions and descendant/dependency changes; stale or
conflicting plans refuse atomically or require explicit reconciliation.

Accepted evidence and source/accounting receipts are historical facts: undo may
supersede their current applicability but cannot erase what happened. Sent
messages, paid execution, publishing and source deletion cannot be undone by
rewinding local state. Report such external effects separately and require their
own supported compensating workflow; cancellation remains a request until its
outcome is known. Reject generic undo of irreversible or uncertain operations.

## Build from verified foundations

Implementation proceeds through small gates, each with successful, refused,
interrupted and replayed cases before dependent behavior is admitted:

1. Restricted parser, typed IDs/schema, canonical encoding and independent
   round-trip comparison; malformed inputs leave the destination unchanged.
2. Atomic envelopes, journal/checkpoint durability, crash recovery and fencing;
   prove accepted work survives and stale owners cannot write.
3. Core structure/state verbs, invariant validation, indexed counts and guarded
   undo/redo against an independent model after generated mutation sequences.
4. Client/socket protocol and asynchronous operations, including disconnect,
   retry, cancellation, backpressure and diagnostic attribution.
5. Non-destructive issue capture/import/export and roadmap projections; reconcile
   full source content and prove fresh-engine restore before live adoption.
6. Assignment/adapter integration and measured real coordinator dogfooding;
   preserve uncertain external outcomes and verify no duplicate execution.

Independent pieces may be built in parallel against pinned contracts, but no
unverified dependency is represented as a passed gate. Keep the first live pilot
small and reversible, with originals retained and a tested recovery path. A
successful happy-path demo is never enough to advance a preservation gate.
Record failures and minimize reproductions; repair and rerun the affected gate
before proceeding. Correctness evidence determines readiness, not schedule pressure.

## Staged verification and release

1. Run unit, generated/property, golden and independent-comparator suites. Add
   mutation tests proving important assertions fail when preservation is broken.
2. Run process-level fault injection and restart/replay against temporary Git
   remotes and fake providers, including two-process fencing and interrupted I/O.
3. Use an authorized real repository for read-only capture and dry-run plans;
   reconcile full observable IDs/content against captured GitHub issue records.
4. Import into a disposable destination with originals untouched, export and load
   in a fresh engine, independently compare, repeat/resume, and inspect all gaps.
5. Dogfood reversible coordinator workflows with periodic snapshots, tested
   undo/redo and an actual isolated restore. Only then admit ordinary live work.
   Destructive absorption remains a separately gated feature, never a pilot step.

Fast per-change checks target one minute and must finish within two. Put exhaustive
fault/scale/generated matrices in explicit pre-release/manual or nightly runs,
not in a slow every-change gate. Full preservation/recovery gates still must pass
at the release revision; changing affected code invalidates the relevant receipt.
Before lock, map each suite to named scenarios, assertions, owner, command and CI
lane. Before release, attach exact-revision results; a list of intended tests is
not evidence that the software has passed them.
