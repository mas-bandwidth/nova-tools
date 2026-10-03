# Nova Tools 1.1.0 — DRAFT, not released

This draft describes the audited source at `e6ce41a4a72daa64510a1104ad8a4c2c53b01d2d`. Version 1.1.0 is not shipped; this is neither a release announcement nor a certification result.

Nova Tools 1.1.0 brings the sprint tick onto a dirty-driven path and gives a fleet a declared way to run its tools and loops. The sprint processes work changes, readers, merge and fleet activity, then settles bounded dirty tables. Returned work and a primary whose current head has red CI stay held until their conditions change; a redeal counts only after a take ends. Routes assign a card's model, token budget and deadline at deal time; rework carries the reader's finding, and unbegun reads assigned to unavailable readers can be reassigned. The old event-driven rule path and parked Layer 1/2 table path are removed. `nova-swarm` retains `native` and `member`; the former `batch` and `route` commands are removed.

Work cards now carry a frame with staging, branch, attempt and review context; `native` uses it to stage the checkout and present `JOB.md`. A card header can name local recipe files to stage into the job before launch. Claude's `gh` shim records a finish that `native` recognizes as a result; the plain flow asks the child to write a shaped `RESULT.md`. The member has one path for judging a work card's finish. Other named model families currently use the plain fallback.

`nova-config` now declares loops with a machine, command and either a periodic interval or keepalive, and applies a revisioned loop view to Redis. Its inventory reads applied state and supplies machines, roles, beat facts and typed loops to the fleet plays. The tools, Redis and loop plays install verified tools, converge ACL users, and manage marked service units; check mode reports intended changes. A missing loop revision is refused instead of being read as an empty set of units. `nova-redis acl render|check|apply` derives role permissions from key families and callable Redis Functions; apply changes only differing users, preserves passwords and supports `--dry-run`.

The source tree is smaller. At the audited tip, tracked Go files whose names do not end in `_test.go` total **192,303 physical lines**, with **280,171 lines in `_test.go` files** counted separately. Non-test Go is down **50,775 lines** from the 243,078-line baseline at the start of the final contraction window. The count includes blanks, comments, `deprecated/` and any tracked Go fixture files; non-Go files are excluded. Testify is adopted with shared test helpers, while counted migration debt remains; the tests are not yet all converted.

## Upgrading

Run `nova-redis fn load` against each store when upgrading to 1.1.0. The smaller `nova_sprint` Redis Function library has a different digest; until the load, `nova-redis fn check` reports `STALE` and names the load command as its remedy.

## Evidence and open gates

- **Recorded:** The card-contract model has a committed bounded-run record. The 3,000-card dirty-tick drive checks its read invariants and can enforce a one-second ceiling on a configured gate runner; an earlier bench run reports 56 ticks, a 150 ms mean and a 613 ms maximum. These records do not establish a certification result for this source tip.
- **Earlier red evidence:** Follow-up probes on an earlier tip reproduced a recipe copied through a linked parent outside its root and a retry accepting an old head through a retained ref from the same launch. The retry probe seeded retained state rather than causing a crash. A prior race run was reported red in `internal/sprint/store` for `TestTheTickAndAVerbRaceSafely`, `TestSentinelsByPositionStoreNothing`, `TestSixtyPrimariesLandDrivenOnlyByTheTick` and `TestCRTwoRunLoopsAtOnce`. These results are historical, not verdicts on this tip.
- **Not established:** The latest recipe and retry probe receipt is **pending**. The current race rerun, certification tick-gate, walled child end-to-end CI run, Redis-backed CI class-test run, deadline-from-deal check and complete post-fix fleet rollout also lack a receipt here. The route, rework-finding, reader-away and Redis CI class-test source changes have landed; their current integrated gate remains to be checked.

*Editor's note: reconcile these gates and the source pin before publishing release notes.*
