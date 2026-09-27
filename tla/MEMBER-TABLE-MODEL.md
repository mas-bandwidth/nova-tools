# Proposed member placement and epoch protocol

This is the **proposed replacement**, separate from `tla/TableMachine.tla`, which still models nova-tools `f77458853af46fdbbafd6881a4b46006431f266f` and still fails its strict gate. The proposal follows SPEC-COORDINATOR section 7 at `599f786b9` (the wording cleanup at `00f6f18` does not change that contract). It has not yet been implemented or independently reviewed. Do not describe these green design checks as proof about deployed Lua.

## State and contract

`MemberTable.tla` reuses the baseline's owned cells, bound views, shape operations and scores, adding `place[member][table]`. A missing placement is `NoPlace`. `RecordSetLink` checks that the actual owned locations equal that record's singleton place, or the empty set. This proves at most one owned location and exactly one when the record declares a placement. Card lifecycle status, primary/copy relationships and the requirement that a live card be placed in particular dimensions remain the card machine's obligations; no card-liveness guarantee is inferred from this index alone.

The added protocol is explicit:

- Add refuses when the member already has a place in that table, including adding again to the same cell. A successful add writes the set and record together.
- Move requires that the source is the recorded place, both cells are owned, and both belong to the same table. It preserves the source score and changes the record and set together. The proposed enumerated action includes different rows; today's public move only accepts columns of the same row.
- Remove clears the matching set member and placement together. Removing a member from a different cell changes neither. Explicit row deletion, table drop and successful ordinary table clear clear affected placements with the deleted sets.
- Bind and row-add refuse any shape that would delete or hide an occupied owned cell. Empty cells can be rebound. Bindings must target external storage, never any potential table-owned key, including one not created yet. This is checked in the operation, not just assumed by the invariant. An explicitly destructive row-delete remains separate from lossless reshape.
- Ordinary table clear keeps today's whole-operation refusal when any cell is bound; successful clear removes that table's owned rows and placements and retains its definition. Drop leaves external sets intact.

`NoOwnedAlias`, `NoHiddenOwned`, `RecordSetLink`, the score-preservation check and the baseline bound/clear/drop/external checks all run together. The large workload offers at most one new external binding per call. The small fixed-point workloads also offer table-owned aliases, which the new guard must refuse. External sets may appear in several bound views; those views do not count as owned placement. External-owner writes themselves are outside this model.

## Epoch composition

`EpochMemberTable.tla` composes the same operations in one abstract epoch domain. A physical table is `<<logicalTable, epoch>>`. `MemberEpoch[member]` is immutable. The real record's `place:<table>` is represented by `place[member][<<table, MemberEpoch[member]>>]`; all entries for other epochs must be absent. This redundant map makes the link to physical storage directly checkable. It is not a proposal to put one field per historical epoch on a real record.

Every mutation checks both the active namespace and the caller's observed epoch. Member operations also check the member's immutable epoch. A writer must refresh its observed epoch after advancement. These are epoch checks, **not** the backend leader lease/fence or actor authorization protocol.

`Advance` increments the active epoch, retaining all historical data, placements and shape. The new active cells start empty; definitions for the finite physical-table universe are abstractly available, while future epochs initially have no rows. `ReadEpoch` refreshes a writer without changing user data. The active-view set contains only current-epoch owned entries. `NoEpochLeak`, `RecordEpochLink` and `OnePlacePerDimension` check that records cannot cross generations or acquire a second placement. The action properties check that stale writes leave user state unchanged, advancing retains history, the new view starts empty, and old generations subsequently remain frozen.

This separates coordinator/sprint epoch clear from ordinary one-table clear. The concrete generic epoch-domain API, physical key encoding, epoch-zero compatibility and fenced migration are implementation choices still to settle with Rowan. The model's first epoch is 1 and its last is 2; this represents two consecutive generations, not a requirement that production start at 1. Member creation and ID reuse are not modeled: members and their immutable epochs are assigned in the finite initial universe. An implementation must define creation atomically and reject accidental reuse of a historical ID.

## Scope, bounds and execution

These are finite-instance **safety** checks of serialized successful transitions. They do not establish liveness, arbitrary-size correctness, the handling of every Redis command error, an implementation refinement, or atomicity across Redis and Postgres. The record and its set are assumed to share one validated atomic commit. Redis function execution is serialized but does not roll back preceding writes on a later error; type, ACL and other preflight/refusal tests remain required against the implementation. Concurrent writers appear as all serialized orders. Partial execution, timeouts, retries/idempotency, corruption introduced outside this API, raw Redis writers, leases, receipts and schema changes are not in the state space.

The model uses the baseline's fixed column set and one-new-binding-per-shape-call input abstraction. Its small fixed-point cases offer all finite states reachable by arbitrarily many calls, including failed calls and repeated epoch reads; the large cases stop after three calls from a populated seed. In particular, the large epoch case's three-call bound does not by itself cover advance, refresh, row creation and insertion into that new row. The smaller unbounded-call instance supplies that coverage.

| Check | Finite universe and depth |
| --- | --- |
| `MCMemberTable` | 2 tables, 2 rows, 3 columns, 3 members, 2 writers, 2 scores; 3 calls |
| `MCMemberFixedPoint` | 1 table, 1 row, 2 columns, 1 member, 2 writers, 2 scores; fixed point; alias attempts included |
| `MCEpochMemberTable` | 2 logical tables × 2 epochs, 2 rows, 3 columns, 3 members, 2 writers, 2 scores; 3 calls |
| `MCEpochMemberFixedPoint` | 1 logical table × 2 epochs, 1 row, 2 columns, 2 members (one per epoch), 2 writers, 2 scores; fixed point; alias attempts included |

The mutation controls must fail with the named property and TLC exit code. Forgetting the move's record write violates `RecordSetLink`; omitting alias rejection violates `NoOwnedAlias`; omitting the member-epoch check violates `NoEpochLeak`; allowing an old writer to add after advancement violates the action property `StaleWritesRefuse`. An unrelated parser or runtime error is a failure of the suite, not a successful control.

Run with a locally installed Java and `tla2tools.jar` (validated with release v1.7.4 / TLC 2.19):

```sh
python3 tla/check_member.py --jar /absolute/path/to/tla2tools.jar --out /tmp/member-results
```

The runner has a **single 120-second wall-clock budget**, including all selected checks; timeout is exit 124, never green. It defaults to four TLC workers for the positive cases and one for counterexamples. `--suite member`, `--suite epoch` and `--suite small` select diagnostic subsets and retain the same total cap. Deadlock checking is enabled; bounded cases have an explicit terminal stutter. No download, server, production Redis or external account is used.

## Implementation acceptance still owed

Keep every baseline Lua witness. Reverse the desired-contract outcomes against the corrected Lua: duplicate add, lossy bind/row-add and owned aliases must refuse without changing any affected key; permitted cross-table placement still succeeds. Add real-Redis record/set checks for all destructive paths, stale callers after epoch advancement, retained historical placements, score preservation, legacy/epoch-zero behavior, and wrong-type/ACL refusal before mutation. The one-exchange tests and existing caller tests remain gates. A green candidate model does not substitute for these implementation tests or Rowan's independent review.

## Recorded local result (2026-09-27, Studio)

The final default suite completed in **83.96 seconds**, all queues exhausted in the positive cases: member large 11,372,299 generated / 372,127 distinct states; member fixed point 15,518 / 263; epoch large 15,971,793 / 198,223; epoch fixed point 3,323,874 / 17,397. All four deliberately broken controls failed for the expected named property. The run used four workers for positive cases, one for controls, Java 27 and tla2tools v1.7.4 (TLC 2.19). Counts are evidence for these exact instances, not a scale-independent proof. Rowan's independent candidate-model read is still pending.
