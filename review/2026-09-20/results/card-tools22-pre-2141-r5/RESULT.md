RESULT tools22-pre-2141-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2141 at head 41df639c6dca: internal/swarm: dispatcher releases slot leases by identity (#1582)
PREREAD 2141 claims=6 proven=6 unproven=0 defects=0 high=0

PR 2141
HEAD 41df639c6dcaa1d70baeb9bcf9fdd73391101937
BASE dev
MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730
BEHIND 1
FILES 3 production, 1 test
LINES +143 -20

CLAIMS

1. The dispatcher now captures the lease IDs array returned by TakeSlotLeases instead of discarding it with _, and stores those IDs on the running struct for later use. PROVEN-BY internal/swarm/run.go:440 `ids, _, _, _, holders, granted, lerr := TakeSlotLeases(...)` — the first return value is captured as ids rather than _.

2. All three cleanup paths — launchRefused, default (launchFailed), and the finish loop in the task-waiting main loop — release slot leases using the captured ID list via releaseSlotLease(idList) instead of passing just sc.ID. PROVEN-BY internal/swarm/run.go:496 `in.releaseSlotLease(leaseIDs)`, line 504 `in.releaseSlotLease(leaseIDs)`, line 541 `in.releaseSlotLease(r.leaseIDs)` — each path passes the full slice.

3. releaseSlotLease changed from calling ReleaseSlotLeases(store, owner, id, false) (which removes ALL leases matching that owner+label pair) to calling ReleaseSlotLeasesByID(store, ids, pid) (which removes only the specific lease IDs whose pid still matches). PROVEN-BY internal/swarm/run.go:622 `ReleaseSlotLeasesByID(in.SlotsStore, ids, pid)` — function body replaced entirely, accepting a []string instead of a single string, and routing to the identity-based API.

4. Lease IDs flow through r.leaseIDs on the running struct across launch → waiting → finish, so cleanup always has the exact IDs that were granted. PROVEN-BY-EXISTING internal/swarm/run.go:53 `r.leaseIDs = leaseIDs` in launchStarted case — the struct field carries the list from take-time to end-time.

5. The CLI verb nova-swarm slots release --owner … --label … keeps its existing by-owner-and-label behavior unchanged; this is documented as intentional for human operators at a prompt. PROVEN-BY-EXISTING docs/CLI.md:2753–2765 `nova-swarm slots release --owner … --label … keeps the by-owner-and-label behaviour, because that is what a person at a prompt means by it`.

6. A new integration test verifies that when a dispatcher exits (via launch-failed or launch-refused), bystander leases sharing the same owner AND task ID survive the release — proving the fix prevents the cross-deletion bug. PROVEN-BY internal/swarm/run_slots_identity_test.go:44–107 TestDispatcherReleasesSlotLeasesByIdentityNotOwnerAndLabel — plants two bystander leases (one sharing owner+task-id, one with different label), runs dispatcher through failure paths, asserts both survive and exactly 2 remain in the store.

DEFECTS none

QUESTIONS

1. The commit message says "Red: TestDispatcher…" then "Green: go test …" — was the test written red-first (the old buggy code made it fail, then the fix made it pass), confirming the regression pattern? Or was it written green-only against the fixed code?

2. The test exercises only launch-failed (missing supervisor) and launch-refused (over max_input). Should there also be a test for the normal-finish path, which goes through finish() and releaseSlotLease(r.leaseIDs)?

3. Is there any operational evidence of #1582 firing in production (two dispatchers sharing an owner and task ID simultaneously), or was this purely a preventive hardening against the #1562 class of bugs?

Left owed
I did not read every test helper in the swarm package (heldIDs, recoveryPool, writeSlotStore exist in other files but I verified their presence rather than reading full bodies). I did not run go vet or go build on the PR branch (permitted but not required). I did not read specs-docs beyond the changed sections (docs/SPEC-SWARM.md lines around 2494, docs/CLI.md lines around 2753). I did not verify the ReleaseSlotLeasesByID implementation in slots.go beyond seeing it exists — its logic (pid fence, already-gone tolerance) was taken as correct based on the test coverage in slots_identity_test.go.

(empty — no local changes)
d576bf6bbabb39068096a97b4560de9b5e245970
