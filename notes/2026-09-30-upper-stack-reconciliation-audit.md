# Upper Stack Reconciliation Audit

**Date**: 2026-09-30  
**Author**: Emma Antigravity <emma@mas-bandwidth.com>  
**Status**: Completed  
**Role**: Child Worker 99 for Emma Antigravity  

---

## 1. Executive Summary

This audit provides a detailed, line-by-line reconciliation analysis comparing Rowan's latest PR #4806 branch (`origin/rowan/upper-integrate` @ `cacfd32cc2736899b5f645afb3d76e8026c91ee9`) against Emma's Grand Master Upper Stack + Task E14 branch (`origin/emma/upper-grand-master-e14` @ `03b28a0a3245151c88771195c7310faf92d6e570`).

### Core Conclusion
Both branches have converged toward complete Upper Stack operational readiness, but they developed complementary halves of the remaining work:
1. **Emma's branch (`origin/emma/upper-grand-master-e14`)** provides:
   - Stella's sealed 10 key constructors and complete contract invariants suite in Layer 1 (`internal/tset/`, +2,087 lines).
   - Refined R1 receipt bound (2064 acceptance / 2065 refusal), R2 touched successor preflight, and R3 Mem successor HLEN accounting fix (`internal/tset/mem_plan.go`).
   - Task E14 batch presence down handling (up to `TickMaxMoves` in one plan, `internal/sprint/presence.go`, `internal/sprint/store/presence_test.go`).
   - PR #4826 log-driven wake mechanism eliminating 1-second gaps (`internal/sprint/store/waitlog.go`, `cmd/nova-sprint/run.go`, `run_wake_functional_test.go`).
   - PR #4825 table and row rendering alignment (`internal/ntable/render.go`, removal of `HideZeroRows`).
   - Real Redis 8 lifecycle functional verification (`cmd/nova-sprint/newpath_lifecycle_functional_test.go`).
2. **Rowan's PR #4806 branch (`origin/rowan/upper-integrate`)** provides:
   - CLI dispatch implementation in `cmd/nova-sprint/newpath_calls.go` (441 lines) wiring all 38 verbs to `spverbs` and `app.newPath = true`.
   - Comprehensive new path drive functional test `cmd/nova-sprint/newpath_drive_functional_test.go` (794 lines).
   - PR #4806 review bug fixes:
     - **B1**: Listing query properties (`deal_index`, `ask_index`) in Lua (`sprint_queries.lua`).
     - **B2**: Review rules R8, R9, R10 reading a line's about/primaries.
     - **B3**: Lua `x_cmds` taking derive phase commands (`wait:<n>`, `missing`) so `add --needs` applies on a store.
     - **M1 / Amendment 7**: Gate cycle check in `add.go` (`AddPlaceCycle`) and cycle reporting verb in `internal/sprint/verbs/check.go`.
     - **M2**: Wire support for R2 (`FollowPrimary` and `QueryBeat` over due set).
     - **M3**: Coordinator rework reading fleet widths and writing `deal_index`.
     - **M4 / Amendment 6**: R15 sprint done KNOW note + machine stop (`rp.Stop = true`).
     - **M5**: `accept --read-ok`.
     - **m1–m4**: Prefix removal from help, `goal show` without arguments, `TestCommandFCALLOnlyOnTheStore`, and `queue --as <reader>`.
   - Stored coordinator inbox cursor and tick-end wake (Errata 3 Amendment 8, `inbox_cursor.go`, `tickend.go`).
3. **Reference Model Alignment**:
   - `internal/sprint/refmodel/` is **100% identical** (0 diff) between both branches.

---

## 2. Commit Genealogy & Merge Base

### Commits Under Audit
- **Rowan Review PR #4806**:  
  `origin/rowan/upper-integrate` @ `cacfd32cc2736899b5f645afb3d76e8026c91ee9`
- **Emma Grand Master + Task E14**:  
  `origin/emma/upper-grand-master-e14` @ `03b28a0a3245151c88771195c7310faf92d6e570`

### Merge Base
- **Merge Base SHA**: `320a6d38af518943762df753c2f7fa9cc9ec2fe0`
- **Commit Details**:
  ```text
  Author: Glenn Fiedler <glenn@mas-bandwidth.com>
  Date:   Wed Sep 30 12:33:23 2026 -0400

  sprint: a member is a fleet machine with a width; the deal fills to width in one step round the fleet (Glenn's ruling) (#4822)
  ```

### Branch Divergence Stats
- **Three-dot diff** (`git diff --stat origin/rowan/upper-integrate...origin/emma/upper-grand-master-e14`):  
  **135 files changed, 21,761 insertions(+), 459 deletions(-)**
- **Two-dot direct diff** (`git diff --stat origin/rowan/upper-integrate origin/emma/upper-grand-master-e14`):  
  **170 files changed, 4,858 insertions(+), 6,446 deletions(-)**

---

## 3. Component-by-Component Reconciliation

### 3.1. `cmd/nova-sprint/`

**Diff Summary**: 29 files, 1,151 insertions(+), 2,799 deletions(-)

#### Key Differences:
1. **New Path Dispatch & CLI Wiring (`newpath_calls.go`, `newpath_verbs.go`, `main.go`)**:
   - *Rowan*: Added `cmd/nova-sprint/newpath_calls.go` (441 lines). This implements direct verb calls (`callTick`, `callAdd`, `callRelease`, `callRank`, `callWork`, `callRest`, `callDeal`, `callFleet`, `callDrop`, `callRework`, `callMerge`, `callReview`, `callAck`, `callWait`, `callInboxWait`, `callLog`, `callReads`, `callJudgment`). Activated `a.newPath = true` in `newApp()`. Added `library` integrity check (`decision 30`) and `noteStream` setup (`redisNotes`).
   - *Emma*: Maintained stubbed calls in `newpath_verbs.go` (`stubbed("IT19, verbs.Add")`) because the operational verbs were being integrated in `internal/sprint/verbs/` and verified via store/lifecycle functional tests.
2. **Drive Functional Testing**:
   - *Rowan*: Added `cmd/nova-sprint/newpath_drive_functional_test.go` (794 lines), validating end-to-end multi-stream workflow (100 cards across 3 streams with sentinel gates every 10 cards) on both twin and store.
   - *Emma*: Added `cmd/nova-sprint/newpath_lifecycle_functional_test.go` (373 lines), validating machine lifecycle (`define`, `init`, `start`, `stop`, `clear`, `teardown`) on real Redis 8 under sandbox isolation.
3. **WaitLog Integration (PR #4826 - Glenn's Ruling)**:
   - *Rowan*: `run.go` retained the fixed clocked sleep `a.sleep(store.TickEvery)` (1-second intervals).
   - *Emma*: `run.go` incorporates `pace()`, blocking on the epoch log (`st.WaitLog`) with `TickFloor = 100ms` and `TickEvery = 1s` fallback. Added `cmd/nova-sprint/run_wake_test.go` (207 lines) and `cmd/nova-sprint/run_wake_functional_test.go` (187 lines).
4. **Table Rendering Alignment (PR #4825)**:
   - *Rowan*: Retained `HideZeroRows: true` for `sprint.Work` and `sprint.Merge`.
   - *Emma*: Aligned `reads.go` with PR #4825 (tables and rows always show, empty or not); updated `testdata/where_frame.golden`, `testdata/where_watch_frame.golden`, and associated view tests.
5. **Specialized Test Suites in Rowan**:
   - Rowan added `newpath_store_functional_test.go`, `newpath_batchread_test.go`, `newpath_cycle_test.go`, `newpath_done_test.go`, `newpath_down_test.go`, `newpath_rework_round_test.go`, `inbox_cursor_test.go`, and `library_functional_test.go`.

---

### 3.2. `internal/sprint/verbs/`

**Diff Summary**: 25 files, 338 insertions(+), 1,563 deletions(-)

#### Key Differences:
1. **Cycle Detection (Errata 3 Amendment 7)**:
   - *Rowan*: Added `internal/sprint/verbs/check.go` (112 lines) for reporting cycles (`CYCLE`, exit 2). Added `AddPlaceCycle` check in `add.go` preventing loop creation through sentinel gates.
   - *Emma*: `add.go` does not yet contain `AddPlaceCycle`, and `check.go` was not yet introduced.
2. **Coordinator Inbox Cursor (Errata 3 Amendment 8)**:
   - *Rowan*: Added `internal/sprint/verbs/inbox_cursor.go` (119 lines) and `inbox_cursor_test.go` (357 lines). `inbox --read` writes the cursor to table property `inbox_cursor` on `sprint.Work`. In `inbox_wait.go`, `inbox --wait` waits for `tick-end` notes (`waitTickEnd`) using the stored cursor. Added `inbox_wait_tick_test.go` (296 lines).
   - *Emma*: `inbox_wait.go` waits on `first JUDGMENT line` (`waitJudgment`), and `inbox --read` returns a local refusal stating that the stored cursor write is pending.
3. **Store Refusal Bug Logging (Decision 31)**:
   - *Rowan*: Added `internal/sprint/verbs/steprefused.go` (48 lines), emitting a `typeStepRefused` note when a store step is refused with a bug code.
   - *Emma*: Refusals handled directly via `*Refused` errors without separate note emission.
4. **Verb Plan Signatures**:
   - *Rowan*: Used a helper wrapper `stepPlan(func(rd) (*Request, error))` in several verbs.
   - *Emma*: Directly uses `func(rd *sprintfn.ReadReply) (Part, error)`, ensuring direct composition and typed Part return.
5. **Verb Review & Rework Alignment**:
   - *Rowan*: In `review.go`, `AcceptReq` gained `ReadOK bool` (M5). In `planRework`, reads fleet widths and `deal_index` and writes updated `deal_index` via `propEntries(p)`.
   - *Emma*: Contains the full base implementation of `ReworkAt`, `Return`, `Merge`, and `DropAbort`.

---

### 3.3. `internal/sprint/store/`

**Diff Summary**: 6 files, 296 insertions(+), 6 deletions(-)

#### Key Differences:
1. **Log-Driven Wake Implementation (PR #4826)**:
   - *Emma*: Introduced `internal/sprint/store/waitlog.go` (149 lines) and `waitlog_test.go` (78 lines):
     - `LogWaiter` interface.
     - `Store.WaitLog(ctx, epoch, after, d)` and `Store.LogTail(ctx)`.
     - `Redis.WaitLog`: Pipelined `XREAD BLOCK` + `XREVRANGE` for tail discovery in 1 round trip.
     - `Mem.WaitLog`: Wakes on commit via `m.wakeLog()` or custom `LogWait` duration hooks.
     - `Store.Tick()`: Returns `Epoch` in `TickResult` to steer waitlog positioning.
   - *Rowan*: Did not have `waitlog.go` or log-driven wake in `internal/sprint/store/`.
2. **Batch Down Verification (Task E14)**:
   - *Emma*: Added `TestMultipleSilentMembersGoDownInOneTick` in `internal/sprint/store/presence_test.go` (53 lines), verifying that all silent fleet members transition to `down` simultaneously in one machine tick.
   - *Rowan*: Baseline presence tests only.

---

### 3.4. `internal/tset/`

**Diff Summary**: 13 files, 2,087 insertions(+), 7 deletions(-)

#### Key Differences:
1. **Sealed 10 Key Constructors (PR #4810, Stella's Sealed Contract)**:
   - *Emma*: Fully integrated Stella's 10 sealed key constructors:
     - `callback_keyconstructor_functional_test.go` (285 lines)
     - `props_key_authority_gap_functional_test.go` (374 lines)
     - `receipt_settlement_functional_test.go` (199 lines)
   - *Rowan*: Does not have these constructor identity seals or authority gap regression tests.
2. **Refined R1 Receipt Changed Bound**:
   - *Emma*: Refined bound accepting 2,064 entries and refusing 2,065 entries.
3. **R2 Touched Successor Preflight**:
   - *Emma*: Enforces `DRIFT` refusal if a touched successor property table is non-empty.
4. **R3 Mem Successor HLEN Accounting Order (`stella-88cadd74689f`)**:
   - *Emma*:
     - In `internal/tset/mem_plan.go`: Tracks touched property tables (`propTables`); on advance, charges successor HLEN via `rowsetRead` before observing fields or returning from propguard branches; caches count 0 to prevent double-charging first new-name capacity; preserves Mem's global nonempty-successor DRIFT preflight.
     - In `internal/tset/mem.go`: Added `SeedProperty(space, table, epoch, name, value)`.
     - Added `pr4810-mem-budget-probe_test.go` (101 lines).
     - Added `property_contract_invariants_test.go` (555 lines).
     - Added `property_contract_invariants_functional_test.go` (452 lines).
   - *Rowan*: Does not have the touched property table tracking or normalized R3 witness.

---

### 3.5. `internal/sprint/refmodel/`

**Diff Summary**: 0 files, 0 insertions(+), 0 deletions(-)

#### Status:
- **100% IDENTICAL AND FULLY RECONCILED**.
- Rowan's branch and Emma's branch share the exact same reference model definitions, transition models, and validations.

---

### 3.6. Cross-Cutting Areas & Lua Engine

1. **Task E14 Presence Batching (`internal/sprint/presence.go`)**:
   - *Emma*: Modified `presence()` to batch downs up to `TickMaxMoves` in a single plan using `roundMoves` and `roundWrites`. Clarified documentation that levelling moves are not counted against `TickMaxMoves`.
   - *Rowan*: Still processed downs one member per tick (`downs[0]`).
2. **Lua Engine (`internal/nsprint/fn/lua/`)**:
   - *Rowan*:
     - `sprint_queries.lua`: Added `q.props` support on `fleet` and `readers` queries (PR 4806 B1); added `FollowPrimary` (PR 4806 M2); added `place_needs` for gate cycle walks (PR 4806 M1).
     - `sprint_x.lua`: Added `SP.intent_commands` so `add --needs` applies on a store (PR 4806 B3); added `ctx.x_poisoned` explanation.
   - *Emma*: Retained baseline query and derivation logic.
3. **R15 Done Semantics (`internal/sprint/rules_position.go`)**:
   - *Rowan*: Completed Errata 3 Amendment 6: `planDone` emits a `KNOW` note to the coordinator and halts the machine (`rp.Stop = true`).
   - *Emma*: Maintained note in code documenting the planned transition to Amendment 6.
4. **TLA+ Formal Specifications (`tla/`)**:
   - *Rowan*: Added `TickEndOnce` with witness W30 in `SprintEvents.tla` and `MCSprintEventsW30.cfg` (Errata 3 Amendment 8).

---

## 4. Reconciliation Matrix

| Component | `origin/rowan/upper-integrate` | `origin/emma/upper-grand-master-e14` | Status / Integration Path |
|---|---|---|---|
| **`internal/sprint/refmodel/`** | Up-to-date | Up-to-date | **Identical (100% aligned)** |
| **`internal/tset/` (Stella Seals)** | Missing seals & R3 fix | Sealed 10 key constructors, R1/R2/R3 fixes | **Preserve Emma's implementation** |
| **Presence Batch Downs (Task E14)**| 1 down/tick | Batched downs up to `TickMaxMoves` | **Preserve Emma's implementation** |
| **Store WaitLog (PR #4826)** | Clock-based sleep (1s) | Log-driven wake (`WaitLog`, 100ms floor) | **Preserve Emma's implementation** |
| **Table Rendering (PR #4825)** | `HideZeroRows` | All tables/rows always show | **Preserve Emma's implementation** |
| **New Path CLI Calls (`newpath_calls.go`)**| Full dispatch implemented | Stubbed calls | **Incorporate Rowan's implementation** |
| **Drive Functional Test** | 794 lines (`newpath_drive_functional_test.go`) | Lifecycle functional test (373 lines) | **Combine both test suites** |
| **Cycle & Gate Loop Detection (M1)**| `check.go` + `AddPlaceCycle` | Not present | **Incorporate Rowan's implementation** |
| **Inbox Cursor & Tick-End (Errata 3)**| `inbox_cursor.go` + `tickend.go`| Refuses stored cursor | **Incorporate Rowan's implementation** |
| **Lua Query Props & Intents (B1, B3)**| Implemented in Lua & Go | Not present | **Incorporate Rowan's implementation** |
| **R2 Beat Wire & FollowPrimary (M2)**| Implemented | Not present | **Incorporate Rowan's implementation** |
| **Rework Rolling Index Update (M3)**| Writes `deal_index` | Base rework | **Incorporate Rowan's implementation** |
| **R15 Stop & Done Notice (M4)** | Amendment 6 implemented | Pre-amendment | **Incorporate Rowan's implementation** |

---

## 5. Next Steps for Integration

To produce a single unified master branch incorporating PR #4806 and Emma's Grand Master + Task E14:
1. Carry forward all of Emma's Layer 1 sealed key constructors, R3 Mem successor accounting, Task E14 presence batched downs, and PR #4826 store `WaitLog`.
2. Apply Rowan's PR #4806 review additions:
   - CLI dispatch in `cmd/nova-sprint/newpath_calls.go` and `cmd/nova-sprint/newpath_drive_functional_test.go`.
   - Cycle detection in `internal/sprint/verbs/check.go` and `internal/sprint/addparts.go` (`AddPlaceCycle`).
   - Stored coordinator inbox cursor and tick-end wake in `internal/sprint/verbs/inbox_cursor.go` and `internal/sprint/machine/tickend.go`.
   - Lua fixes in `internal/nsprint/fn/lua/` (query properties B1, follow primary M2, intent commands B3).
   - R15 Amendment 6 in `internal/sprint/rules_position.go`.
3. Verify the combined suite under both unit tests and Redis 8 functional tests.
