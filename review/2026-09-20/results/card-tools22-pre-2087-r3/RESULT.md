RESULT tools22-pre-2087-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2087 at head f4417f76c049: swarm: multi-tier bounded execution timeouts and two-phase reaping (#2062)
PREREAD 2087 claims=12 proven=10 unproven=2 defects=3 high=0

## PR 2087
HEAD f4417f76c04949c1691f07a7c9ba72c18f9e20ee
BASE dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
MERGE-BASE 5298f6be12ea
BEHIND 0
FILES 2 production, 1 test
LINES +717 -10

## 1. `ClampTimeout` clamps any `time.Duration` to `[MinExecutionTimeout, MaxExecutionTimeout]` = `[5s, 2h]`.

PROVEN-BY internal/swarm/timeout_test.go:436 TestClampTimeout — 12 sub-cases covering zero, negative, below-min, boundary-preserving, above-max, and far-above inputs with exact equality assertions against expected clamped outputs.

## 2. `ClampTimeoutWithDefault` substitutes a default when raw duration ≤ 0, then clamps the result; otherwise clamps the raw value.

PROVEN-BY internal/swarm/timeout_test.go:467 TestClampTimeoutWithDefault — 6 sub-cases testing zero/negative→default with subsequent bounds clamping, plus positive-value override and below-min clamp of positive input.

## 3. `NewTimeoutHierarchy` builds a three-tier hierarchy (`Card ≥ Step ≥ Git`) applying all clamping rules for auto-derived and explicit parameters.

PROVEN-BY internal/swarm/timeout_test.go:494 TestTimeoutHierarchyConstruction — asserts invariants via `.Validate()`, checks exact default values, step clamping to card, git cap at MaxGitTimeout, card clamp at 2h, and automatic derivation for unspecified step/git.

## 4. `StepBudget(remainingCard)` returns 0 when exhausted, otherwise `min(h.StepTimeout, remainingCard)`.

PROVEN-BY internal/swarm/timeout_test.go:570 TestTimeoutHierarchyBudgets·StepBudget — asserts zero return on 0/negative input, min(5m, 20m)=5m, min(5m, 2m)=2m.

## 5. `GitBudget(remainingStep)` applies the 1/3 allocation rule, caps at MaxGitTimeout (1m), floors at MinGitTimeout (5s), never exceeds remainingStep.

PROVEN-BY internal/swarm/timeout_test.go:586 TestTimeoutHierarchyBudgets·GitBudget — tests 0→0, 30s→10s, 6m→1m(capped), 6s→5s(floored), 3s→3s(floor blocked by remaining).

## 6. `CardContext`, `StepContext`, `GitContext` create contexts whose deadlines reflect the computed budget.

PROVEN-BY internal/swarm/timeout_test.go:610 TestTimeoutHierarchyContexts — extracts `Deadline()` from created contexts and asserts remaining time falls within tight tolerances (~10s for card, ~5s for step).

## 7. `TwoPhaseReap` delegates to `Reap(pgid, started, TerminateGrace)` performing SIGTERM → 3s grace polling → SIGKILL fallback → confirmation.

PROVEN-BY internal/swarm/timeout_test.go:685 TestTwoPhaseReapLifecycle — spawns `sleep 60`, verifies alive before reap, calls TwoPhaseReap, asserts fast completion (< Grace+1s) and death after. Also PROVEN-BY internal/swarm/timeout_test.go:725 TestTwoPhaseReapUnresponsiveChildKilledBySIGKILL — spawns Python process ignoring SIGTERM, confirms Phase 2 SIGKILL fires within custom grace window.

## 8. `TaskkillArgs(pid, force)` generates `[ "/PID", "<pid>", "/T" ]` or `[ "/PID", "<pid>", "/T", "/F" ]`.

PROVEN-BY internal/swarm/timeout_test.go:637 TestTaskkillArgs — DeepEqual checks against both graceful and forceful arg lists; also tested in TestWindowsKillStrategyComparison:668–681 and TestWindowsTreeReapFallback:777–781 with multiple PID values.

## 9. `WindowsKillStrategy` enum has `String()` returning `"taskkill"` / `"syscall"` / `"strategy(<n>)"`.

PROVEN-BY internal/swarm/timeout_test.go:656 TestWindowsKillStrategyComparison — direct equality assertions on `.String()` for both constants.

## 10. `KnownStamp(started)` rejects empty string and `Dash`, accepts anything else.

PROVEN-BY internal/swarm/timeout_test.go:764 TestWindowsTreeReapFallback — explicit false assertions for `""` and `Dash`, true for `"123456789"`.

## 11. Windows kill operations (`TerminateGroup`, `KillGroup`) delegate through strategy-aware functions (`TerminateGroupWithStrategy`, `KillGroupWithStrategy`) and use `DefaultKillStrategy = StrategyTaskkill` with taskkill-first, syscall-fallback behavior.

UNPROVEN — no test in this diff runs on Windows or exercises `TerminateGroupWithStrategy`/`KillGroupWithStrategy` directly. Both lifecycle tests skip on Windows (`runtime.GOOS == "windows"` → t.Skip). The taskkill→syscall fallback path is only reachable in comments and cross-compilation verification.

## 12. `DefaultKillStrategy` equals `StrategyTaskkill`.

UNPROVEN — var declaration at `internal/swarm/timeout.go:32` (`var DefaultKillStrategy = StrategyTaskkill`), but no test would fail if changed to `StrategySyscall`. Assertion-by-inspection only.

---

## DEFECTS

DEFECT medium internal/swarm/timeout.go:120 `NewTimeoutHierarchy` does not enforce `MinGitTimeout` floor on explicitly-provided (positive) git parameter — only auto-derived git values receive the floor check — contradicting the documented contract that git is bounded to `[MinGitTimeout, min(StepTimeout, MaxGitTimeout)]` — calling `NewTimeoutHierarchy(card, step, 3s)` produces `GitTimeout=3s` which violates the stated minimum; compounds downstream because `GitBudget` will propagate this undersized value into dynamic allocations — adding `if g < MinGitTimeout { g = MinGitTimeout }` after the explicit-parameter branch closes the gap

DEFECT low internal/swarm/timeout.go:266 `Validate()` lacks a `GitTimeout >= MinExecutionTimeout` check — allows a manually-constructed hierarchy with `GitTimeout = 0` or negative to pass validation, relying entirely on constructors to enforce the invariant — adding an explicit guard alongside existing `MaxGitTimeout` check would make Validate complete

DEFECT low internal/swarm/timeout.go:270 `Validate()` step timeout check `if h.StepTimeout < MinExecutionTimeout && h.StepTimeout != h.CardTimeout` contains a dead-code branch — since CardTimeout is always clamped to ≥ MinExecutionTimeout, `StepTimeout == CardTimeout` implies `StepTimeout >= MinExecutionTimeout`, so the exception case is unreachable — removing `&& h.StepTimeout != h.CardTimeout` would clarify intent

---

## QUESTIONS FOR THE REVIEWER

1. `TwoPhaseReap` delegates to `Reap(pgid, started, TerminateGrace)` which lives outside this diff — is Reap platform-generic, and was its implementation audited for correctness alongside these two-phase wrappers?

2. The docstring for `NewTimeoutHierarchy` says auto-derived git is `"floored at MinGitTimeout (or StepTimeout if StepTimeout < MinGitTimeout)"` but the code floors unconditionally at MinGitTimeout without the StepTimeout exception — is this doc discrepancy intentional given the invariant constraints, or should the code match the doc more precisely?

3. Why does the PR introduce `KnownStamp` in timeout.go when `known` already exists in proc_windows.go? Is this about API exposure, or an oversight that leaves `known` and `KnownStamp` doing the same thing?

4. On Windows, `killPidWithStrategy` falls back to `killPidSyscall(pid)` silently when taskkill fails — `_ = killPidSyscall(pid)` discards even the syscall error. Should there be any logging or alternative fallback to alert that both termination methods failed?

---

Left owed: Could not run `git merge-base`, `git rev-list --count`, or local `go vet` due to network restrictions preventing `git clone`; merge-base inferred as `5298f6be12ea` (dev tip, consistent with single-commit PR), BEHIND inferred as 0; the garbled curl progress output in the diff may have obscured minor lines in NewTimeoutHierarchy but raw-file fetch confirmed correct source. Did not inspect `Reap` function implementation (outside PR scope) or other existing swarm files beyond what the diff references.

git status --short
N/A - repo not cloned (network restrictions prevented git clone)
git rev-parse HEAD
N/A - repo not cloned
