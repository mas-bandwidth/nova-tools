# Nova Tools 1.1.0 — DRAFT, not released

This draft describes the audited source at `62a5fa35e58547224fe310761130a0a74f256ccc`. It is not a release announcement or a certification result.

Nova Tools 1.1.0 brings the sprint tick onto a dirty-driven path and gives a fleet a declared way to run its tools and loops. The sprint processes work changes, readers, merge and fleet activity, then settles bounded dirty tables. Returned work and a primary whose current head has red CI stay held until their conditions change; a redeal counts only after a take ends. The old event-driven rule path and parked Layer 1/2 table path are removed. `nova-swarm` retains `native` and `member`; the former `batch` and `route` commands are removed.

Work cards now carry a frame with staging, branch, attempt and review context; `native` uses it to stage the checkout and present `JOB.md`. Claude's `gh` shim records a finish, while the plain flow asks the child to write a shaped `RESULT.md`. The member has one path for judging a work card's finish. Other named model families currently use the plain fallback.

`nova-config` now declares loops with a machine, command and either a periodic interval or keepalive, and applies a revisioned loop view to Redis. Its inventory reads applied state and supplies machines, roles, beat facts and typed loops to the fleet plays. The tools, Redis and loop plays install verified tools, converge ACL users, and manage marked service units; check mode reports intended changes. A missing loop revision is refused instead of being read as an empty set of units. `nova-redis acl render|check|apply` derives role permissions from key families and callable Redis Functions; apply changes only differing users, preserves passwords and supports `--dry-run`.

The source tree is smaller. At the audited tip, tracked Go files whose names do not end in `_test.go` total **189,941 physical lines**, with **276,670 lines in `_test.go` files** counted separately. Non-test Go is down **53,137 lines** from the 243,078-line baseline at the start of the final contraction window. An earlier 346,902-line baseline spans a longer period and is not an overnight reduction. The count includes blanks, comments, `deprecated/` and any tracked Go fixture files; non-Go files are excluded. Testify is adopted with shared test helpers, while counted migration debt remains; the tests are not yet all converted.

## Upgrading

Run `nova-redis fn load` against each store when upgrading to 1.1.0. The smaller `nova_sprint` Redis Function library has a different digest; until the load, `nova-redis fn check` reports `STALE` and names the load command as its remedy.

## Validation still needed

The 3,000-card dirty-tick drive asserts bounded reads and can enforce a one-second tick ceiling on its configured gate runner. An author-reported bench run records 56 ticks, a 150 ms mean and a 613 ms maximum; a completed certification tick-gate receipt for this source tip is not established here. The card-contract model has a committed bounded-run record, but diagnostic functional probes are red: a cached foreign commit can be pushed, a Claude shim finish is not accepted by `native`, and a scripted reader can end without a delivered verdict. Card-header provenance is also unresolved in the source. These cases need repair and an integrated rerun before release. The fleet changes have targeted unit, functional and fixture evidence, including a repaired live capacity-function ACL probe; a complete post-fix fleet rollout is not established. Reconcile these results and the source tip before publishing these notes.
