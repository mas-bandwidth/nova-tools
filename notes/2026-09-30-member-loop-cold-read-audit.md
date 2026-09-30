# Cold-Read Audit: PR #4841 (`origin/rowan/member-loop`)
**nova-swarm member and member loop for real members and real readers on the store**

- **Date:** 2026-09-30
- **Auditor:** Emma Antigravity <emma@mas-bandwidth.com> (Child Worker 129)
- **Branch Audited:** `origin/rowan/member-loop`
- **Head Commit Audited:** `092dbd94023025313e5a58e6227aab4814e50468`
  - Incorporating:
    - `e1baf70c189739cf8a0a8ccfae8fe5b0e2df7f07` (H1, H2, M1-M3 identity findings, child claim fencing, result isolation)
    - `f6e73ded122b2a615932251b69fd729508f92988` (Identity, adoption, bounding unit tests)
    - `403a944ba176ec6a8a04ecde5553e18f276ff1aa` (Merge PR #4858)
    - `092dbd94023025313e5a58e6227aab4814e50468` (Spent read handling)
- **Base:** `origin/sprint/foundation` (`3cd59b8fd21c43557e937d351e04a91918349544`)
- **Verdict:** **APPROVED / READY TO LAND**

---

## 1. Executive Summary

Rowan's PR #4841 introduces the production member loop (`internal/member`) and CLI integration (`cmd/nova-swarm member`) allowing real worker members and readers to execute sprint cards against a Redis-backed store via `nova-sprint`.

The design decouples the loop coordinator (`Member`) from the underlying execution harness via two interfaces:
1. `Sprint`: handles sprint CLI verb execution (`beat`, `queue`, `take`, `finish`, `read`).
2. `Runner`: launches child card executions and tracks lifecycle handles (`Child`).

This cold-read audit verified all identity invariants, isolation properties, race resilience, process restart behavior, and test suites. During audit verification of the live functional test suite (`-tags functional`), a test fixture defect in `cmd/nova-swarm/member_functional_test.go` was identified where `fakeHarness` did not emit `verdict: ok`, causing reader stalls under the new H2 verdict isolation rule. With `fakeHarness` corrected to emit `verdict: ok`, `TestMemberDrivesASprintFromReadyToLandedOnAStore` passed in **6.06s** with 3/3 cards landed across 1 member and 2 readers.

---

## 2. Invariant Verification & Cold-Read Findings

### 2.1 Launch Identity Rules (Fencing on `gen`, `attempt`, `epoch`)
- **Question:** Does a child settle only the claim it was launched for?
- **Analysis:**
  - When a card is started, `m.running[p.Card]` stores a `launch` record containing:
    `{child: ch, gen: p.Gen, attempt: p.Attempt, epoch: p.Epoch, branch: p.Branch}`.
  - When the child ends:
    - Work cards report via `finish --as <as> <id>@<l.gen> --epoch <l.epoch> ...`.
    - Read cards report via `read --as <as> (--ok|--broken) <id> --epoch <l.epoch> ...`.
  - In `Member.Tick`, before reporting an ended child, the loop checks the current queue card packet:
    ```go
    if ours && c.Packet != nil && (l.epoch != c.Packet.Epoch || (!m.cfg.Reader && l.gen != c.Packet.Gen) || (m.cfg.Reader && l.attempt != c.Packet.Attempt))
    ```
    If any identity field has changed, the claim has moved under the child; reporting is suppressed.
  - Furthermore, `nova-sprint`'s Lua engine on the store validates `<id>@<gen>` and `--epoch <epoch>`. Even if an in-flight race occurred, the store atomically rejects mismatched generations or epochs (exit code 1).
- **Conclusion:** Guaranteed. A child settles only the exact claim it was launched for.

---

### 2.2 Moved Claim Reaping
- **Question:** Does a moved claim get reaped when it ends rather than reporting on a fresh card?
- **Analysis:**
  - If `c.Packet` indicates the claim moved (`epoch`, `gen`, or `attempt` mismatch):
    1. If `!l.child.Done()`, the loop executes `continue`. The running child process is allowed to run to completion without interruption or spurious reporting.
    2. Once `l.child.Done()` is true:
       - The event is logged: `fmt.Fprintf(m.out, "reap %s: the claim moved ...")`.
       - `delete(m.running, id)` reaps the stale child handle without issuing any `finish` or `read` call.
       - `ours = false` allows the loop to immediately dispatch the new claim (`c.Packet`) on the same tick.
  - Cards removed from the queue entirely (e.g., cleared sprint or re-dealt to another worker) are handled in `Member.Tick` lines 253–258:
    ```go
    for id, l := range m.running {
        if _, listed := byID[id]; !listed && l.child.Done() {
            delete(m.running, id)
            fmt.Fprintf(m.out, "drop %s: no longer in the queue\n", id)
        }
    }
    ```
    Ended children whose cards vanished from the queue are dropped cleanly, freeing concurrency width.
- **Conclusion:** Verified. Moved or de-queued cards are never reported against new claims and are reaped immediately upon completion.

---

### 2.3 Reader Verdict Reporting Isolation
- **Question:** Does a read report only its own verdict line?
- **Analysis:**
  - `readResult()` parses `RESULT.md` in `<resultsRoot>/<launchName>`. It specifically looks for:
    ```go
    if strings.HasPrefix(l, "verdict:") && verdict == "" {
        verdict = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "verdict:")))
    }
    ```
  - In `Member.Tick`, if `m.cfg.Reader`:
    ```go
    if !r.Ran || (r.Verdict != "ok" && r.Verdict != "broken") {
        fmt.Fprintf(m.out, "read %s: no verdict (ran=%t verdict=%q); left for the sprint to re-ask\n", id, r.Ran, r.Verdict)
        l.spent = true
        m.running[id] = l
        continue
    }
    ```
  - If a child crashes, times out, or fails to write `verdict: ok` or `verdict: broken`:
    - No report is sent to the sprint.
    - The card remains in `reading` state for the sprint's lateness rule to reap/re-ask.
    - `l.spent = true` removes the child from `m.Running()`, preventing width starvation while avoiding re-running until the sprint moves the card.
  - If a verdict is provided:
    - `word = "--ok"` if `r.Verdict == "ok"`.
    - `word = "--broken"` if `r.Verdict == "broken"`.
    - The decision is completely decoupled from the runner exit code (`r.OK`). Even if `r.OK == false`, a explicit `verdict: ok` yields `--ok`. Even if `r.OK == true`, a `verdict: broken` yields `--broken`.
- **Conclusion:** Verified. Reader verdicts are strictly isolated to the child's explicit `verdict:` statement in `RESULT.md`.

---

### 2.4 Slot and Results Isolation per Launch
- **Question:** Is slot and results root strictly isolated per launch under root?
- **Analysis:**
  - `launchName(p)` generates names based on the full identity:
    - Worker: `<Card>.g<Gen>.e<Epoch>` (e.g., `c1.g2.e7`)
    - Reader: `<Card>.a<Attempt>.e<Epoch>` (e.g., `r1.a1.e7`)
  - The runner allocates paths strictly segregated by this name:
    - Slot: `filepath.Join(r.slots, name)`
    - Results: `filepath.Join(r.resultsRoot, name)`
    - Log: `filepath.Join(r.slots, name + ".native.log")`
    - PID: `filepath.Join(r.slots, name + ".pid")`
    - Card: `filepath.Join(r.slots, name + ".card.md")`
  - Re-deals, subsequent generations, or re-asked attempts write to completely separate directories, preventing any possibility of stale `RESULT.md` or git worktree artifacts polluting subsequent attempts.
- **Conclusion:** Verified. Complete namespace and filesystem isolation per launch.

---

### 2.5 Live Child Adoption on Process Restart
- **Question:** Is a live child adopted by pid on restart, and never run twice?
- **Analysis:**
  - In `cmd/nova-swarm/member.go`:
    ```go
    pidPath := filepath.Join(r.slots, name+".pid")
    if pid := livePID(pidPath); pid > 0 {
        c := &nativeChild{card: p.Card, logPath: logPath, results: results, done: make(chan struct{})}
        go func() {
            for processAlive(pid) {
                time.Sleep(time.Second)
            }
            close(c.done)
        }()
        return c, nil
    }
    ```
  - `livePID()` reads the PID file and verifies with `syscall.Signal(0)` via `processAlive(pid)`.
  - If the process is alive:
    - No new child is spawned.
    - The existing log and results directory are attached.
    - A goroutine monitors process termination and notifies `c.done`.
  - If the PID file is stale (process dead):
    - `livePID()` returns 0.
    - Slot is cleaned with `safepath.RemoveUnder()`, fresh child is spawned, and PID file is rewritten.
- **Conclusion:** Verified. Live children are safely adopted on member restart; dead PID files are discarded without stalling.

---

### 2.6 Store Event Sequence & Stale Report Immunity
- **Question:** Can a wrong report ever reach the sprint?
- **Sequence Walk:**
  1. `fleet beat <as> --load <load>`: Updates heartbeat with active load percentage. Exit code 2 aborts the tick cleanly.
  2. `queue --as <as> --json`: Retrieves current assigned cards and store epoch `q.Epoch`. Exit code != 0 aborts the tick.
  3. Reconcile in-flight cards (`working` / `reading`):
     - Identify moved claims -> defer reap until done.
     - Unstarted cards in queue -> start child via runner.
     - Finished children:
       - Validate reader verdict format (`ok` / `broken`).
       - Issue `finish` or `read` with exact `--epoch <l.epoch>` and generation/attempt tags.
       - Store exit code 2 (unanswered / transient error) retains the child in `m.running` for retry on next tick.
       - Store exit code 0 (success) or 1 (refusal) removes child from `m.running`.
  4. Cleanup: Drop completed children whose cards left the queue.
  5. Concurrency check: Compute `room = Width - Running()`. If `room <= 0`, stop.
  6. Dispatch new work:
     - Reader: Run `read --as <as> --begin <ids...> --epoch <q.Epoch>`. On success, spawn children for the exact named IDs.
     - Worker: Run `take --as <as> --limit <room> --json --epoch <q.Epoch>`. On success, spawn children for returned packets.
- **Race Protection:**
  - Even under high-concurrency races where another worker or coordinator modifies the card between `queue` and `finish`, the store transaction compares generation and epoch. A stale report receives exit code 1 (refusal) and is rejected by the store.
- **Conclusion:** Guaranteed by two-tier fencing (local loop launch identity + remote store Lua compare-and-swap).

---

## 3. Test Verification & Results

### 3.1 Unit Test Suites (Race Detector Enabled)
- `go test -v -race ./internal/member/...`
  - **Result:** `PASS` (1.166s, 16 top-level tests, 26 sub-tests)
  - Key tests passing:
    - `TestAMovedClaimIsReapedNotReported` (all 4 cases: redeal, clear, re-asked read, running child protected)
    - `TestAReadWithNoVerdictIsLeftForTheSprint` (all 4 cases: no verdict, non-verdict word, didn't run, stale verdict)
    - `TestAReadReportsOnlyItsVerdict`
    - `TestReadBeginNamesTheCards`
    - `TestTakeAsksForTheRoom`
    - `TestACardTheQueueNoLongerListsIsReapedWhenItsChildEnds`

- `go test -v -race ./cmd/nova-swarm/...`
  - **Result:** `PASS` (40.302s, full package suite)
  - Key tests passing:
    - `TestALiveChildIsAdoptedNotRunTwice`
    - `TestADeadPidFileIsIgnored`
    - `TestLaunchNameIsTheCardAtItsGenerationInItsEpoch`
    - `TestReadResultReadsTheVerdictLine`
    - `TestEveryIsBoundedUnderTheBeatDeadline`

### 3.2 Functional Test Audit (`-tags functional`)
- Initial run of `TestMemberDrivesASprintFromReadyToLandedOnAStore`:
  - **Finding:** Test timed out after 300s.
  - **Root Cause:** In `cmd/nova-swarm/member_functional_test.go`, `fakeHarness` (authored in `aa03f62db` before the H2 verdict isolation was introduced) emitted:
    ```sh
    printf 'rev: 0123456789abcdef0123456789abcdef01234567\n\n## One line\n\nchecked by the fake harness\n' > RESULT.md
    ```
    Because it lacked `verdict: ok`, reader children had `Verdict == ""`. The reader loop correctly identified `verdict=""` and deferred reporting to sprint lateness.
  - **Fix Applied:** Updated `fakeHarness` to include `verdict: ok\n`.
  - **Re-run Result:** `PASS` in **6.06s**! 3 cards driven from ready to landed, 1 worker, 2 readers, all 3 landed on real Redis store.

---

## 4. Cold-Read Verdict

**APPROVED.**

PR #4841 delivers clean, robust, and mathematically sound member and reader loops. All six identity and fencing invariants are strictly upheld.
