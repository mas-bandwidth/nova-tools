# Cold-Read Audit: PR #4865 (Functional tier green: whole-fleet deterministic, webhook no-poll allowlist)

**Date:** 2026-09-30  
**Auditor:** Emma Antigravity (Child Worker 133) <emma@mas-bandwidth.com>  
**Branch under audit:** `origin/rowan/functional-green`  
**Head commit:** `09a8f1f91bf3648d7b738a0a30c262c3d0c61d5e`  
**Commits in PR:**
- `955bdb92935e6026417bf049942cf949a8a6ac36` ("nsprint/webhook: the nova-ci cost files name a job-listing path and poll nothing")
- `d4d4059f676c8fba2822216116e4a7f8f2ed62c4` ("nova-sprint: the whole-fleet test alternates the machine's tick and the world's, and asserts what the deal guarantees")
- `52b249fdad3f7132253708159c95912dfef2d25a` ("nsprint/fleetbuild: the no-ssh-secret class test stops scanning a file that moved under deprecated/")
- `54b6d798579fc70557997ef07be3ffae268be934` ("Merge remote-tracking branch 'origin/sprint/foundation' into rowan/functional-green")
- `09a8f1f91bf3648d7b738a0a30c262c3d0c61d5e` ("tests: the member drive's briefs pass the card lint; the card-rules table is the typed-parser allowlist's")
**Target branch:** `origin/sprint/foundation` (`06418a6fd0f6fdf7c1a84f509e51c869fb8bc766`)  
**Diff against target:** 5 files changed, 102 insertions(+), 21 deletions(-)  
**Verdict:** **GREEN (ACCEPT)**

---

## 1. Executive Summary

PR #4865 serves as the functional-green landing gate for promoting `sprint/foundation` to `dev` (PR #4863). Rowan opened PR #4865 to resolve test regressions and non-deterministic functional test failures across the foundation tier:

1. **Deterministic Whole-Fleet Deal Testing (`cmd/nova-sprint/whole_fleet_functional_test.go`):**
   - **Problem:** `TestTheWholeFleetMovesInOneTickOnTheStore` previously executed the machine loop in an uncoordinated background goroutine against a simulated world running on a 10 ms timer (`play --every 10ms --ticks 40`). On a loaded bench, thread preemption caused deals to commit between steps of the world before member queues had been drained, resulting in deal ticks that only serviced members with room (e.g., 7 of 8 members), triggering false test failures.
   - **Solution:** Converted the test into an alternating turn-based execution model: 16 discrete rounds where the machine executes exactly 1 tick (`runLoop` with `ticks=1`) followed by the world executing exactly 1 tick (`play --every 1ms --ticks 1`). Zero sleeps, zero background goroutines, completely deterministic.
   - **Assertions Verified:**
     - Every dealing tick reaches every member in the fleet (`slices.Equal(set, members)`).
     - Ready cards are immediately taken by the world in the step after the deal (`fleetReady(...) == 0`).
     - Fairness guarantee: difference between most dealt member and least dealt member across the run is at most 1 card (`most - least <= 1`).
     - Deal index property (`PropDealIndex`) in the Redis store increases monotonically as a counter.

2. **Webhook No-Poll Allowlist (`internal/nsprint/webhook/nopoll_test.go`):**
   - **Problem:** `TestNoPollingPathsRemain` enforces that no production source code polls GitHub check-runs or workflow runs via REST API. The scan regex `/check-runs\b|actions/runs|/rerun` tripped on `actions/runs` references in `cmd/nova-ci/cost.go`, `cmd/nova-ci/main.go`, and `internal/cicost/cicost.go`.
   - **Solution:** Audited and allowlisted the three files. None of these files dial the GitHub API or poll. They reference `repos/<owner>/<name>/actions/runs/<id>/jobs` strictly in doc comments, CLI usage strings, and error refusal messages to describe the expected JSON schema supplied on `stdin`. The `nova-ci cost` verb is an offline, pure computation over `stdin` that calculates run cost and spin without network interaction.

3. **Fleetbuild Secret Scan Adjustment (`internal/nsprint/fleetbuild/release_test.go`):**
   - Fixed `TestNoReleaseCodeReadsASecretOverSsh` to stop scanning `cmd/nova-sprint/fleet_release.go`, which PR #4493 moved to `deprecated/cmd/nova-sprint/`. Deprecated code is reference-only.

4. **Integration with Recent Foundation Commits:**
   - Merged `sprint/foundation` (`06418a6fd`), updating `cmd/nova-swarm/member_functional_test.go` brief formatting to comply with the child card lint (`swarm.ChildRulesParagraph()`), and adding an allowlist row in `internal/typedrec/oneparser_test.go` for prose mentions of `PR` and `PATHS` in `CardChildRules`.

---

## 2. Commit & Diff Analysis

### 2.1 File Changes against `origin/sprint/foundation`

```
 cmd/nova-sprint/whole_fleet_functional_test.go | 90 +++++++++++++++++++++-----
 cmd/nova-swarm/member_functional_test.go       | 16 ++++-
 internal/nsprint/fleetbuild/release_test.go    |  8 +--
 internal/nsprint/webhook/nopoll_test.go        |  7 ++
 internal/typedrec/oneparser_test.go            |  2 +
 5 files changed, 102 insertions(+), 21 deletions(-)
```

### 2.2 Deep Dive: `whole_fleet_functional_test.go`

The heart of the change eliminates timing jitter between the store machine and the simulated fleet:

```go
// Each round is one machine tick and one world tick; the world takes
// (and, the round after, finishes) every card the tick dealt, so the next
// deal that has cards to give has the whole fleet's room. A round is
// 1 ms of world time, and the rounds are bounded, never timed.
const rounds = 16
dealing := 0
for i := 0; i < rounds; i++ {
    loop.runLoop(ctx, st, 0, 1, &out, &errb)
    dealt := len(loopDeals(out.String())) > dealing
    dealing = len(loopDeals(out.String()))
    var pout, perr bytes.Buffer
    if code := world.run([]string{"play", "--every", "1ms", "--ticks", "1"}, &pout, &perr); code != 0 {
        t.Fatalf("play: %d %s%s", code, pout.String(), perr.String())
    }
    if !dealt {
        continue
    }
    // the tick dealt: the world's tick took every member's ready cards,
    // none left behind for a tick after
    for _, m := range members {
        if n := fleetReady(t, do, m); n != 0 {
            t.Fatalf("round %d: %s has %d ready cards after the tick that followed the deal: every member up takes in that tick\n%s", i+1, m, n, out.String())
        }
    }
}
```

Key invariant audits:
- **Zero Sleeps / Zero Background Tasks:** `runLoop` with `n=1` exits immediately after 1 tick. `world.run` with `--ticks 1` exits immediately after 1 step. Execution is completely serial and synchronous.
- **Fairness & Spread Invariant:** Post-loop verification aggregates deals per member:
  ```go
  least, most := dealt, 0
  for _, m := range members {
      least, most = min(least, perMember[m]), max(most, perMember[m])
  }
  if most-least > 1 {
      t.Fatalf("members were dealt from %d to %d cards over the run (%v): the deal goes round the fleet a card a member, so no member is more than one card ahead", least, most, perMember)
  }
  ```
  Verified: In our execution, 3,000 cards were dealt across 6 dealing ticks. Exactly 375 cards were allocated to each of the 8 members (`m1` through `m8`). Spread `most - least == 0 <= 1`.

### 2.3 Deep Dive: `nopoll_test.go` Allowlist

The allowlist entries in `internal/nsprint/webhook/nopoll_test.go` are:
```go
	// `nova-ci cost` names the forge's job-listing path in its usage and in
	// its refusals, to say what stdin holds: it reads the listing from stdin
	// (one run's body, handed to it), never dials the forge and never loops,
	// so it is a one-shot read of a run's cost, not a check-state poll.
	"cmd/nova-ci/cost.go":       "names the job-listing path in its refusal text; reads stdin, never the forge",
	"cmd/nova-ci/main.go":       "names the job-listing path in the usage of the cost verb; reads nothing",
	"internal/cicost/cicost.go": "names the job-listing path in a comment on the body it parses; reads nothing",
```

Audit of the target locations:
- `cmd/nova-ci/cost.go:5`: Comment explaining stdin schema: `// stdin (the body of repos/<owner>/<name>/actions/runs/<id>/jobs: one JSON...`
- `cmd/nova-ci/cost.go:104,108`: Refusal error messages: `stdin is empty; it wants the run's job listing (repos/<owner>/<name>/actions/runs/<id>/jobs)` and `stdin is not the forge's job listing...`
- `cmd/nova-ci/main.go:94`: CLI usage description of `nova-ci cost`.
- `internal/cicost/cicost.go:15`: Package documentation comment.

None of these files invoke network requests. Furthermore, `nopoll_test.go` enforces that every allowlist key must match a live scan hit; if any file stops matching `pollRx`, the test fails, preventing allowlist rot.

---

## 3. Test Verification Results

All tests executed cleanly on the local Darwin ARM64 test bench:

1. **`internal/nsprint/webhook/...`:**
   ```
   === CONT  TestNoPollingPathsRemain
   --- PASS: TestNoPollingPathsRemain (0.74s)
   PASS
   ok      github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook    0.977s
   ```

2. **`cmd/nova-sprint/...` (Race detector enabled):**
   ```
   PASS
   ok      github.com/mas-bandwidth/nova-tools/cmd/nova-sprint    6.255s
   ```

3. **`whole_fleet_functional_test.go` (Deterministic functional test, `-tags functional`, race detector enabled):**
   ```
   === RUN   TestTheWholeFleetMovesInOneTickOnTheStore
   === PAUSE TestTheWholeFleetMovesInOneTickOnTheStore
   === CONT  TestTheWholeFleetMovesInOneTickOnTheStore
       whole_fleet_functional_test.go:188: tick 1: 512 dealt to [m1 m2 m3 m4 m5 m6 m7 m8]
       whole_fleet_functional_test.go:188: tick 2: 512 dealt to [m1 m2 m3 m4 m5 m6 m7 m8]
       whole_fleet_functional_test.go:188: tick 3: 512 dealt to [m1 m2 m3 m4 m5 m6 m7 m8]
       whole_fleet_functional_test.go:188: tick 4: 512 dealt to [m1 m2 m3 m4 m5 m6 m7 m8]
       whole_fleet_functional_test.go:188: tick 5: 512 dealt to [m1 m2 m3 m4 m5 m6 m7 m8]
       whole_fleet_functional_test.go:188: tick 6: 440 dealt to [m1 m2 m3 m4 m5 m6 m7 m8]
       whole_fleet_functional_test.go:210: deal_index 3000 after 3000 cards dealt in 6 ticks
   --- PASS: TestTheWholeFleetMovesInOneTickOnTheStore (134.44s)
   PASS
   ok      github.com/mas-bandwidth/nova-tools/cmd/nova-sprint    135.738s
   ```

4. **`internal/ci/...` (Full test & class suite):**
   ```
   PASS
   ok      github.com/mas-bandwidth/nova-tools/internal/ci            7.129s
   ok      github.com/mas-bandwidth/nova-tools/internal/ci/allowlist  0.134s
   ok      github.com/mas-bandwidth/nova-tools/internal/ci/functional 0.142s
   ok      github.com/mas-bandwidth/nova-tools/internal/ci/slowtests  0.139s
   ok      github.com/mas-bandwidth/nova-tools/internal/ci/timing     0.802s
   ```

5. **`internal/nsprint/fleetbuild/...` & `internal/typedrec/...`:**
   ```
   PASS
   ok      github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild 1.250s
   ok      github.com/mas-bandwidth/nova-tools/internal/typedrec           1.489s
   ```

6. **`cmd/nova-swarm/...` & functional member drive test:**
   ```
   PASS
   ok      github.com/mas-bandwidth/nova-tools/cmd/nova-swarm    13.625s
   --- PASS: TestMemberDrivesASprintFromReadyToLandedOnAStore (7.75s)
   PASS
   ok      github.com/mas-bandwidth/nova-tools/cmd/nova-swarm    12.804s
   ```

---

## 4. Cold-Read Audit Checklist

| Requirement | Status | Cold-Read Finding |
|---|---|---|
| Deterministic loop in whole-fleet test | PASS | Alternates single machine tick (`runLoop(..., 1)`) with single world step (`play --ticks 1`). No sleeps, zero background goroutines. |
| Deal reaches whole fleet | PASS | Every dealing tick verifies `slices.Equal(set, members)` for all 8 members. |
| Immediate card processing | PASS | Asserts `fleetReady == 0` for all members immediately after dealing tick. |
| Fairness / spread guarantee | PASS | Asserts `most - least <= 1` across all members over the entire run. |
| Monotonic deal index | PASS | Store snapshot `PropDealIndex` validated as monotonically increasing counter (`>= dealt`). |
| Webhook no-poll allowlist | PASS | Three `nova-ci cost` files allowlisted with exact rationale; zero API dials or polling in source. |
| Deprecated file scan removal | PASS | `fleet_release.go` correctly excluded from `fleetbuild` active scan. |
| Clean merge into `sprint/foundation` | PASS | Fast-forward / merge tree verified with 0 conflicts. |

---

## 5. Conclusion & Recommendation

PR #4865 is sound, thoroughly tested, and delivers high-reliability determinism to the whole-fleet test while cleanly fixing the static checks.

**Recommendation:** Proceed with immediate merge of PR #4865 into `sprint/foundation`, unblocking the promotion of `sprint/foundation` to `dev` (PR #4863).
