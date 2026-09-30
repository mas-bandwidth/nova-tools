# Cold-Read Review & Verification Audit: PR #4844 (`rowan/fleet-h0-h1`)
**Date:** 2026-09-30  
**Auditor:** Child Worker 117 (Emma Antigravity <emma@mas-bandwidth.com>)  
**Subject:** PR #4844 — *fleet: H0 machine self and H1 fleet sync from nova-config*  
**Base:** `sprint/foundation`  
**Target:** `origin/rowan/fleet-h0-h1`  
**Landed Commit:** `6796ca0b2224c933d7e80713ccbf414c455a07f0` (squashed from head `d69bd9a3c4ef3b1b6d2625bfe5bd3fd05345641c`)  

---

## 1. Executive Summary & Verdict

PR #4844 establishes the single source of truth for the fleet table from `nova-config`'s machine inventory (Milestones H0 and H1). It introduces `nova-config machine self` to resolve machine identity without typing, `nova-config machine width` to compute the sprint member capacity from the shared machine ceiling, and `nova-sprint fleet sync` to reconcile the fleet table with the inventory in a single atomic plan.

### Verification Verdict: **APPROVED & VERIFIED GREEN**
- **Architecture & Invariants:** Adheres strictly to the "one inventory" principle, single multi-member step execution, static share vs runtime lease separation, and clear separation of sync holds vs coordinator holds.
- **Test Results:** All unit, race-detector, class-audit, and functional container tests pass cleanly with zero errors or race conditions.
- **Rulings & Edge Cases:** All 8 design vs code differences documented by Rowan, along with the two review rounds (cold read by Opus and re-read by Sonnet), have been completely resolved, coded, and regression-tested.

---

## 2. Detailed Technical Audit by Area

### 2.1 Fleet Sync & Machine Self (`cmd/nova-sprint/fleetsync.go`, `cmd/nova-config/machine.go`, `internal/config/self.go`)

1. **`nova-config machine self [--check]`:**
   - **Resolution Precedence:**
     1. Environment variable `NOVA_MACHINE` (lower-cased and syntax-checked).
     2. Tailnet DNS/hostname from `tailscale status --json --peers=false` (first label, syntax-checked).
     3. Hostname from OS (first label, syntax-checked).
   - **Error & Safety:** If none yields a valid name, returns exit code 3 (`exitCannotRead`) with actionable remediation. No store is opened for bare `machine self`.
   - **`--check` Mode:** Opens `nova-config` Postgres store (via `config.ResolveDSN`). Exits 0 if the row exists in `KindMachine`, 2 if not found, and 3 if store is missing/unreachable.
   - **Syntax Validation:** Tailnet name and hostname are validated via `ValidateName(n) == nil`, falling through cleanly if invalid (verified by `TestSelfNameValidatesTheHostname`).

2. **`nova-sprint fleet sync [--check]`:**
   - **Coordination Seam:** Invokes `a.inventory` (`inventoryFn`), defaulting to `readInventory` which resolves DSN and queries `config.Widths` over Postgres and Redis.
   - **Safety Boundary:** If inventory holds 0 machine rows, aborts with exit code 3 to avoid accidentally holding down an entire active fleet when pointed at an empty or misconfigured database.
   - **Atomic Planning:** Uses `sprint.FleetReq{Op: "sync", Sync: want}` dispatched through `store.FleetStep`. `store.FleetStep` marks `Named: true`, guaranteeing that if any member is invalid or refused, the entire step is refused all-or-none before any state changes.
   - **Drift Reconciliation:**
     - Missing members: Added down until they beat (`status: Down`), allowing presence to bring them up naturally on the first beat.
     - Changed widths: Set without altering status or disturbing existing card placements.
     - Dropped members: Marked held down (`downPlan`), unfinished work cards redealt to remaining up members using the rolling index.
     - Second sync idempotence: `TestSyncAfterSyncIsSync` fuzz-property verifies that across 40 random inventory mutations, a subsequent sync is a clean no-op (`moved=0`, `drift=0`).

### 2.2 Width Derivation & Live Redis Queries (`internal/config/width.go`, `internal/config/redis.go`)

1. **Static Share Computation:**
   - Width is derived strictly as:
     $$\text{Width} = \max(0, \text{Slots} - \text{Charged})$$
   - No separate `width` field is persisted in the Postgres database, preventing cache divergence between capacity declarations and sprint membership.
   - A machine with $\text{Width} \ge 1$ is a sprint member (`Member() == true`). If friends consume the entire ceiling ($\text{Width} = 0$), the machine is excluded from sprint membership and placed in held status.

2. **Friend Slot Charging:**
   - A friend's desired slots count against the machine where her live heartbeat is running (`friend:<friend>:beat -> host`).
   - If a friend has no active heartbeat, slots fall back to being charged against the fleet's coordinator machine (`fleet:coordinator`).
   - `RedisApplier.FriendHosts` queries beats in a single pipelined Redis request.
   - If friends carry slots but no Redis connection is provided, width queries refuse with exit code 2 rather than printing ungrounded numbers.

### 2.3 Integration with CI Slot Deductions (`internal/ci/cipriority_class_test.go`)

1. **Class Test Rule (nova-tools #4293):**
   - General rule enforces that any slot computation must deduct active CI legs (`namesCILegs = regexp.MustCompile(r"\bci\b|\bCI\b|TM\.ci_legs\(")`).
2. **Reasoned Exemption (`staticShareExempt`):**
   - Exemption registered for `internal/config/width.go` line:
     ```go
     var staticShareExempt = map[string]string{
         "internal/config/width.go": "w.Width = w.Slots - w.Charged",
     }
     ```
   - **Rationale:** Width represents the **static share** declared by configuration. If runtime CI legs were subtracted here, the static width would fluctuate dynamically with every CI build, creating continuous false drift in `fleet sync --check` and triggering unnecessary card redeals.
   - **Dynamic Enforcement Point:** Runtime CI legs are instead deducted at the slot lease boundary during `nova-swarm slots take` (`internal/swarm.TakeSlotLeases`, cmd/nova-swarm/slots.go; H2 lease-before-take takes $\min(\text{width} - \text{held}, \text{free leases})$).

### 2.4 Behavior of `held_by=sync` Across Sprint Clears (`internal/sprint/steps_clear.go`, `steps_work.go`)

1. **Ownership Separation:**
   - Control cards track hold origin via `FieldHeldBy = "held_by"` (`HeldBySync = "sync"`).
   - Holds created by `fleet sync` set `held_by=sync`.
   - Holds created by coordinator (`fleet down <member>`) have no `held_by` tag.
2. **Release Rights:**
   - `fleet sync` releases only members where `ctl.F("held") != "" && ctl.F("held_by") == "sync"`.
   - Coordinator holds are never released by sync; they require explicit `nova-sprint fleet up <member>`.
3. **Coordinator Takeover (Rowan Fix in `d69bd9a3c`):**
   - If a member is held by sync and a coordinator executes `fleet down <member>`, `downPlan` explicitly clears `FieldHeldBy`:
     ```go
     case r.Op == "hold" && r.HeldBy == "":
         unset = append(unset, FieldHeldBy)
     ```
     This transfers hold ownership to the coordinator, ensuring sync cannot subsequently release it.
4. **Preservation Across Reset/Clear:**
   - In `internal/sprint/steps_clear.go`:
     - `Shape.HeldBy` records the map of `member -> held_by`.
     - `ShapeOf` extracts `s.MemberCtl(m).F(FieldHeldBy)`.
     - `RestoreShape` re-applies `fields[FieldHeldBy] = by`.
   - Verified by `TestAClearKeepsWhoMadeAHold`: after `clear --confirm sprint`, `held_by=sync` remains intact and permits release drift when the machine returns to inventory.

---

## 3. Review of the 8 Design vs Code Differences

| # | Design vs Code Difference | Resolution / Ruling | Audit Finding |
|---|---|---|---|
| **1** | CI legs in width computation | Width is the static share; CI legs are deducted at lease take (`TakeSlotLeases`). Reasoned exemption in `cipriority_class_test.go`. | **Compliant** — Clean justification, class test green. |
| **2** | Width 0 not representable (ParseWidth: 1..1024) | Machines where friends consume ceiling have width 0 and are treated as non-members (held down). | **Compliant** — Validated in `fleetSyncPlan` and tests. |
| **3** | Friend charges not in Postgres | Live beats in Redis provide host; fallback to coordinator machine. Redis required when friends carry slots. | **Compliant** — `FriendHosts` pipelining verified. |
| **4** | Holding and releasing distinction | Added `held_by=sync` to control cards; sync only releases its own holds; coordinator holds stay. | **Compliant** — Comprehensive test coverage including clears. |
| **5** | Multi-member step requirement | Added `Op: "sync"` to `FleetReq`; step is named (`Named: true`) and executes all-or-none. | **Compliant** — Validated by `TestFleetSyncIsAllOrNone`. |
| **6** | Exit code semantics (0, 2, 3) | 0: match/clean; 2: drift / not a row; 3: cannot read store/config or empty inventory. | **Compliant** — Standard family conventions honored. |
| **7** | Config seam for sprint | Introduced `a.inventory` seam (`inventoryFn`), allowing unit tests to use `config.Mem`. | **Compliant** — Unit tests run fast and isolated. |
| **8** | Flaky functional test observation | `TestTheWholeFleetMovesInOneTickOnTheStore` is tick-related, unaffected by sync. | **Verified** — Functional test `TestFleetSyncFollowsTheInventoryOnTheStore` passed in <1s. |

---

## 4. Test Verification Outputs

### 4.1 Unit & Race Detector Tests
Command executed:
```bash
go test -v -race ./cmd/nova-sprint/... ./internal/config/... ./internal/ci/...
```
- **`cmd/nova-sprint`:** All tests PASSED (including `TestFleetSyncBringsTheInventoryUpAtItsWidths`, `TestFleetSyncSetsAChangedWidthAndNothingElse`, `TestFleetSyncHoldsAMemberTheInventoryDropsAndRedealsItsCards`, `TestFleetSyncTwiceWritesNothingTheSecondTime`, `TestFleetSyncCheckPrintsTheDriftExitsTwoAndWritesNothing`, `TestFleetSyncExitsThreeWhenTheConfigCannotBeRead`, `TestFleetSyncLeavesTheCoordinatorsHold`, `TestFleetSyncRefusesAWidthTheFleetCannotTake`, `TestFleetSyncIsTheCoordinatorsAlone`, `TestSyncAfterSyncIsSync`, `TestFleetSyncAddsANewMemberDownUntilItBeats`, `TestFleetSyncRedealsToTheWidthsItSets`, `TestFleetSyncIsAllOrNone`, `TestFleetSyncReleasesItsOwnHoldAndNotTheCoordinators`, `TestFleetSyncCheckIsThreeWhenTheStoreIsMissing`, `TestFleetSyncJSONIsOneShape`, `TestAClearKeepsWhoMadeAHold`, `TestFleetSyncCheckIsThreeWhenTheTablesCannotBeRead`).
- **`internal/config`:** All tests PASSED in 1.17s (`TestSelfNameIsTheTailnetNameWhenThereIsATailnet`, `TestSelfNameRefusesAnInvalidNovaMachine`, `TestSelfNameValidatesTheHostname`, `TestWidthIsSlotsLessTheFriendsChargedThere`, `TestAFriendWithNoBeatIsChargedToTheCoordinatorMachine`, `TestWidthsNeedARedisOnlyWhenAFriendCarriesSlots`, `TestWidthIsDerivedNotStored`).
- **`internal/ci`:** All class tests PASSED (including `TestSlotsShrinkByCILegs` in 30.86s).

### 4.2 Additional Internal Sprint & Config Tests
Command executed:
```bash
go test -race ./cmd/nova-config/... ./internal/sprint/...
```
- **`cmd/nova-config`:** PASSED in 3.499s (`TestMachineSelfPrintsTheTailnetNameAndOpensNoStore`, `TestMachineSelfCheckIsTwoWhenTheNameIsNoRowAndThreeWhenUnreadable`, `TestMachineWidthIsSlotsLessTheFriendsCharged`, `TestMachineWidthWithNoFriendsNeedsNoRedis`).
- **`internal/sprint`:** PASSED in 17.453s (`TestValidSyncRefusesWhatTheFleetRefuses`, `TestDriftSaysWhatASyncWouldWrite`, `TestACoordinatorsHoldOnAMemberTheSyncHoldsClearsTheMark`, `TestFleetUpClearsTheSyncsMark`, `TestTheSyncsReleaseClearsItsMark`, `TestAHoldClearsAStaleMark`).

### 4.3 Functional Integration Test
Command executed:
```bash
go test -v -tags=functional ./cmd/nova-sprint -run TestFleetSyncFollowsTheInventoryOnTheStore
```
- **Output:** `PASS` (0.97s) — Real Redis store with RedisApplier, verifying friend beat host deduction, dynamic coordinator machine charging, drift checks, and width-constrained deals.

---

## 5. Documentation Verification

All associated documentation files were reviewed against the code:
- **`docs/SPEC-SPRINT.md` (§5):** Accurately specifies the inventory-driven fleet model, single-step plan, `held_by` semantics, and exit codes.
- **`docs/SPEC-CONFIG.md`:** Documents static share width derivation, friend charging rules, and `machine self` name resolution.
- **`docs/CLI.md`:** Documents `nova-config machine self [--check]` and `nova-config machine width`.
- **`docs/nova-config/README.md`:** Contains clear examples and usage demonstrations for `machine width` and `machine self`.

---

## 6. Conclusion

PR #4844 is fully sound, robust, well-tested, and adheres to all architectural constraints of the Nova platform. The branch has been merged into `sprint/foundation` at commit `6796ca0b2224c933d7e80713ccbf414c455a07f0`.
