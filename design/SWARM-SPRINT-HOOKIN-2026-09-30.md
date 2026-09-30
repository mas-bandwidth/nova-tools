# nova-swarm to nova-sprint Member Hook-In Design

**Date:** 2026-09-30  
**Author:** Emma Antigravity <emma@mas-bandwidth.com>  
**Status:** Design Specification (Pairs with Stella S25 `SWARM-MEMBER-FIT-2026-09-30.md` & `SPRINT-FLEET-LAYERS-2026-09-30.md`)

This document defines the concrete Go architecture and lifecycle loop hooking `nova-swarm` into `nova-sprint` as a real fleet member daemon. Per Decision 11, the member daemon resides directly inside `nova-swarm`.

---

## 1. Core Go Interfaces

```go
package swarm

import (
	"context"
	"time"
)

// SprintClient handles external communication with the authoritative sprint tables.
type SprintClient interface {
	// Beat sends periodic liveness telemetry (3s cadence; BeatDeadline=15s).
	Beat(ctx context.Context, member string, load float64) (*BeatReport, error)
	// Where polls fleet/work queues returning the current epoch and assigned cards.
	Where(ctx context.Context, member string) (*WhereView, error)
	// Take moves cards from ready -> working up to room (or by exact card@gen).
	Take(ctx context.Context, req TakeRequest) (*TakeResult, error)
	// Finish reports terminal card outcomes (head, report text, success/failure).
	Finish(ctx context.Context, req FinishRequest) (*FinishResult, error)
}

// SlotManager reserves physical host execution capacity from the bench slot store.
type SlotManager interface {
	// Reserve allocates k physical slot leases under owner "sprint" for duration d.
	Reserve(owner string, count int, dur time.Duration) (leaseIDs []string, err error)
	// Release frees slot leases by ID; fences live PIDs if unforced.
	Release(leaseIDs []string) error
}

// ChildSupervisor launches and monitors isolated child runner processes.
type ChildSupervisor interface {
	// Spawn starts a sandboxed child agent for a taken card packet in slotDir.
	Spawn(ctx context.Context, packet Packet, slotDir string) (<-chan ChildOutcome, error)
	// Terminate forcefully kills a child process group (SIGTERM, then SIGKILL).
	Terminate(cardID string) error
}

// MemberDaemon coordinates the host worker daemon lifecycle.
type MemberDaemon struct {
	MemberName  string
	Ceiling     int           // Configured host slots (e.g. 8 on Mac Studio)
	Width       int           // Active sprint width: Ceiling - friend charges
	StoreDir    string        // Path to bench slots store (<root>/slots)
	Sprint      SprintClient
	Slots       SlotManager
	Supervisor  ChildSupervisor
	Held        map[string]*ActiveRun // cardID -> active child metadata
	LastEpoch   uint64
	LastBeat    time.Time
	Fenced      bool
}
```

---

## 2. Daemon Lifecycle & Execution Loop

```
+-----------------------------------------------------------------------------------+
|                           nova-swarm Member Daemon                                |
|                                                                                   |
|  [Init] Read config self -> resolve MemberName, Ceiling, Width                     |
|    |                                                                              |
|    +---> [Goroutine: Beat Loop] Every 3s: Sprint.Beat(Member, Load)               |
|    |       (If beat fails > 15s: Fence local launches; on rejoin reap orphans)   |
|    |                                                                              |
|    +---> [Worker Main Loop]                                                       |
|            1. Poll: Sprint.Where(Member) -> get Epoch, Ready, Working             |
|            2. Reconcile: Terminate running children whose card/gen redealt        |
|            3. Capacity Admission: Room = min(Width - len(Held), FreeSlots)        |
|            4. If Room > 0:                                                        |
|                 a. Lease: Slots.Reserve("sprint", Room, 30m)                      |
|                 b. Claim: Sprint.Take(Member, Room, Epoch)                        |
|                 c. Release ungranted leases if Take moved < Room                  |
|                 d. Launch: For each packet -> Supervisor.Spawn(packet, slotDir)   |
|            5. Collect: On child exit -> Verify RESULT.md & git push               |
|                 Outcome DONE     -> Sprint.Finish(card@gen, head, report, OK)     |
|                 Outcome BLOCKED  -> Sprint.Finish(card@gen, head, report, Failed) |
|                 Outcome CRASH    -> Sprint.Finish(card@gen, head, report, Failed) |
|                 Always: Slots.Release(leaseID), delete Held[cardID]               |
+-----------------------------------------------------------------------------------+
```

### Step 1: Initialization & Width Discovery
- Identity and slot ceiling are resolved on startup via `nova-config machine self` (tailnet name).
- Member `Width` is set to `machine.Slots - friend_slots` (e.g. 8 on Mac Studio with 0 friend charge).
- Initializes local working directories under `<swarm-root>/slots/` and clones the reference mirror.

### Step 2: Periodic Heartbeat (`fleet beat`)
- A background ticker issues `fleet beat <member>` every 3s via `Sprint.Beat`.
- Includes host CPU busy percentage sampled over `sprint.LoadWindow` (10s).
- **Self-Fencing Guard:** If no beat succeeds for $\ge 15\text{s}$ (`sprint.BeatDeadline`), the daemon marks `Fenced = true` and halts all admissions/launches. Upon successful reconnection, it queries `Where` to reconcile state before resuming.

### Step 3: Queue Polling & Batch Take
- Polls `where --as <member> --json` (or `queue --as <member> --json`).
- Calculates remaining capacity: `room = Width - len(Held)`.
- If `room <= 0`, waits for running children to complete.
- If `room > 0`:
  1. Requests `room` leases from bench slot store via `TakeSlotLeases(store, "sprint", room, ...)`.
  2. Issues `take --as <member> --limit <granted> --epoch <epoch>`, moving cards `ready` $\rightarrow$ `working`.
  3. If Sprint takes fewer cards than leased, excess slot leases are released immediately.
  4. For each taken card packet, allocates child slot workspace `<root>/slots/<leaseID>/` and registers in `Held`.

### Step 4: Child Agent Supervision
- Invokes `ChildSupervisor.Spawn` using `nova-swarm native` harness:
  - Writes `BRIEF.md` containing packet details (brief, fix, notes, base branch).
  - Creates a dedicated git worktree on branch `sprint/<card>` from `base`.
  - Enforces `nova-sandbox` wall isolation (network restricted to GitHub remote only).
  - Starts two concurrent bounds: Wall Timeout (e.g. EST $\times 1.5$ or 20m) and Process-Tree Idle Timeout (e.g. 5m).

### Step 5: Artifact Verification & `finish` Reporting
- Child process terminates and outcome is inspected:
  - Inspects `<slot>/jobs/<card>/RESULT.md` via `swarm.CheckResult`:
    - Line 1 must match the card contract line verbatim.
    - Line 2 defines disposition: `DONE`, `BLOCKED`, `ABSTAIN` (or `OK`/`BROKEN` for reads).
    - Remaining lines contain bounded evidence.
  - Inspects Git state: verifies commits exist and pushes `sprint/<card>` to remote repository.
- Outcomes map to `nova-sprint finish`:
  1. **Success (`DONE` + exit 0 + valid commit):**
     `finish --as <member> <card>@<gen> --head <sha> --branch <branch> --base <base> --report <summary> --epoch <epoch>`
  2. **Rejection (`BLOCKED` or `ABSTAIN`):**
     `finish --as <member> <card>@<gen> --failed --head <sha> --report <evidence> --epoch <epoch>`
  3. **Infrastructure Failure (Harness Crash, Wall Timeout, Malformed RESULT):**
     `finish --as <member> <card>@<gen> --failed --report "crash: <reason>" --epoch <epoch>`
- Releases the physical slot lease via `Slots.Release([]string{leaseID})` and clears `Held[cardID]`.

---

## 3. Error Handling, Timeouts & Reconcile Invariants

| Failure Scenario | Immediate Daemon Behavior | Resolution / Table Effect |
|---|---|---|
| **Heartbeat Failure ($\ge 15\text{s}$)** | Set `Fenced=true`; refuse new `take` or child launch. | Sprint marks member `down` after 15s; redeals unstarted/unacknowledged cards. |
| **Network Split Reconnection** | Poll `Where`; inspect active `Held` against live assignments. | If card generation changed, kill stale child process group immediately (`SIGKILL`). |
| **Child Wall Timeout (EST $\times 1.5$)** | Supervisor kills child process group; writes `reason=wall`. | Dispatches `finish --failed --report "wall timeout exceeded"`; frees slot lease. |
| **Child Process Crash / Non-zero Exit** | Captures tail of `capture.log`; marks run failed. | Dispatches `finish --failed --report "exit code <rc>"`; frees slot lease. |
| **Missing / Corrupt `RESULT.md`** | `CheckResult` flags `malformed` or `no-result`. | Dispatches `finish --failed --report "result contract violated"`. |
| **Stale Epoch on Take/Finish** | Sprint refuses command with exit 1 (stale epoch). | Daemon invalidates `LastEpoch`, aborts in-flight batch, re-reads `Where`. |
| **Graceful Daemon Shutdown** | Sends SIGTERM to all `Held` children; waits grace period (10s). | Cleans up local worktrees; leaves card in `working` for Sprint heartbeat timeout to redeal. |
