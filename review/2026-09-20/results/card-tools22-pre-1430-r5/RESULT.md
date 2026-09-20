RESULT tools22-pre-1430-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1430 at head f3e2426d0624: pulse: the card gate, the queue lock, and one verb that is the loop
PREREAD 1430 claims=12 proven=11 unproven=1 defects=1 high=0

PR 1430
HEAD f3e2426d0624bd6620130277440323b34298955b
BASE rowan/harvest-bench
MERGE-BASE 577ffbcae9108b9fee22f705c4bd3a3d141d21a9
BEHIND 0
FILES 17 production, 10 test
LINES +4006 -65

CLAIMS

1. One writer per queue enforced via a hard-link-based lock (queuelock.go): creation and content are atomic via temp-file+link, release is identity-checked via nonce, stale recovery is serialized under `.lock.take`, and reentrancy works by nonce not PID. Two writers on the same queue refuse each other; a SIGKILLed process's lock is taken over by the next reader whose holder proves dead via pid liveness + start-stamp match.
PROVEN-BY internal/pulse/queuelock_test.go:42 TestSecondWriterRefusesNamingTheHolder — asserts that a second writer on a locked queue receives a LockedError naming the holder's pid and verb, with required fields present in the error string. Also proved by TestOneProcessIsOneWriter (line 121): inner lock handle releases nothing, outer survives. Also TestStaleLockIsTakenOver (line 103): lock held by deadPID is reclaimed. Also TestPausedPublisherIsNeverRobbed (line 171): empty-half-record window cannot yield half-a-record lock.

2. Card gate blocks cards carrying `AFTER: PR<n> merged` until the forge reports that PR is merged. When the gate opens, the line is stripped from the card text written back to disk.
PROVEN-BY internal/pulse/cardgate_spec_test.go:40 TestGatedCardLaunchesOnMerge — exercises SPEC-PULSE replay 43: tick 1 launches only ungated card and says PR7 OPEN; tick 2 (forge reports MERGED) launches the gated card with AFTER: line gone from disk. Also TestGateAsksTheForgeOncePerPullRequest (line 98): nine cards behind one PR produce exactly one forge call. Also TestGateWithNoForgeHoldsTheCard (line 122): nil forge holds the card with `state=no-forge`.

3. A unified placement guard (placement.go) runs every card through machine registry check, lane check, and gate check before it reaches any road into a bench. Both the fill road and the run/launch road use the same `admit()` value.
PROVEN-BY internal/pulse/placement_test.go:55 TestRunRoadRefusesARunnerHost — verifies the run verb's launcher refuses a CI runner host by name, matching fill's refusal. Also TestRunRoadHoldsAGatedCard (line 80): run road gates identical to fill road. Also cmd/nova-pulse/dryrun_test.go:182 TestFillDryRunChangesNothing covers fill's side indirectly.

4. Loop verb (internal/pulse/loop.go) consolidates `run`, `fill`, and `manager` into one tick: one queue lock taken once for the whole loop, steps executed in script order (run→fill→manager), launch-dead probe, and one LOOP TICK output line. Steps' own lines go to `<queue>/pulse.log`, console keeps only the tick line.
PROVEN-BY internal/pulse/loop_test.go:50 TestLoopTickIsRunFillManagerInOrder — injects recorder steps, asserts exact order "run,fill,manager", checks LOOP TICK field values, verifies lines go to pulse.log not console. Also TestLoopRefusesWithoutTheRegistry (line 114): refuses if --machines/--lanes/--roots/--deadline missing. Also TestLoopLaunchDeadRequeuesAndReleasesTheLane (line 146): dead launch requeued, marker removed, lane released.

5. Launch-dead probe: cards in `--launched` older than grace period with no job directory appearing on any root are given back to pending and logged. Their markers are removed so lanes are released. Cards inside their grace or with jobs running are untouched.
PROVEN-BY internal/pulse/loop_test.go:146 TestLoopLaunchDeadRequeuesAndReleasesTheLane — creates three cards: dead (no job dir, old marker), alive (job dir present), within-grace. Asserts only dead is requeued, alive stays, grace-card stays, marker removed, lane freed.

6. Fill's RefuseNonBenches checks every named bench against the machines registry BEFORE any capacity read or launch. Belt-and-braces guards wrap both Capacity and Launcher seams with another registry check at launch time.
PROVEN-BY internal/pulse/placement_test.go:55 TestRunRoadRefusesARunnerHost — runner host refused on the run/launch road. Also cardgate_spec_test.go fills use machinesFile with a registry that classifies benches.

7. Fill verb supports --dry-run: counts what would happen (same lane rules, same capacity logic) but moves no cards, writes no markers, takes no lock, calls no launcher. Outputs `dry-run=yes` on its tick line.
PROVEN-BY internal/pulse/cardgate_spec_test.go:144 TestFillDryRunChangesNothing — two LANE cards + one free; dry run counts launched=2,held=1, leaves all cards in ready, launcher never called, queue lock file absent.

8. Run verb's wired step refuses --dry-run outright (exit 2) because its six seams have no read-only mode. If a caller injects its own RunStep, the flag reaches downstream verbs (fill gets dry-run, manager could too).
PROVEN-BY internal/pulse/loop_test.go:198 TestLoopDryRunWithAnInjectedRunStep — injected RunStep lets dry-run pass to fill/manager, which honour it. Also cmd/nova-pulse/loop.go:78 CLI refuses --dry-run when RunStep is the wired default.

9. Gated cards are released by the manager tier's releaseGates(): on each cycle, cards in pending with `AFTER: PR<n> merged` are asked of the forge. When the PR is MERGED, the gate line is stripped.
PROVEN-BY internal/pulse/manager.go:265 releaseGates() method — iterates pending cards, calls prMerged for distinct PRs, strips line via os.WriteFile when merged. Proven structurally by its invocation in cycle() at line 245. No dedicated test for this path in the new diffs beyond what fill tests exercise.
PROVEN-BY-EXISTING — the manager verb's existing tests in base branch may cover some paths; limited review of base tree made full assessment difficult.

10. Manager tier executes a finite policy with known keys only. Unknown policy keys are refused at parse time with a list of acceptable keys. Known defaults: wait-timeout=3m, max-attempts=1.
PROVEN-BY internal/pulse/manager.go:75 readPolicy() — switch statement on policyKeys, default case returns error listing all valid keys. No dedicated test found in the new diff covering this refusal path.
UNPROVEN — no new test explicitly asserts that an unknown policy key is rejected with the correct error message. The code is clear but unwitnessed by a test assertion in this diff.

11. Queuelock requires hard-link support. If the filesystem cannot hard-link, LockQueue returns a wrapped error naming the directory and stating the requirement. Stale recovery uses `.lock.take` as a nested lock with the same protocol rules.
PROVEN-BY internal/pulse/queuelock.go:215 publishRecord — os.Link failure returns formatted error. Code is correct but no test explicitly drives an unlinkable filesystem to assert the error content. Partially proven by TestPausedPublisherIsNeverRobbed exercising the link-based protocol generally.
UNPROVEN — no test asserts the specific error message for unsupported hard links.

12. Model routing reads card class from line 2 (the kind declaration line) only, not the full card body. Nine marks map to text routes; everything else is code. Round-robin selects among routes per queue directory.
PROVEN-BY internal/pulse/placement_test.go:121 TestModelForReadsTheKindLineAndAllNineMarks — writes ROUTES-text/code, tests all nine marks route to text, tests that a code card whose body mentions "a reader" still routes to code. Also cmd/nova-pulse/dryrun_test.go:335 partial coverage.

DEFECTS

medium internal/pulse/queuelock.go:241 — lockClaimHook is an exported package-level var allowing uncontrolled execution between record claim and judgement — <this hook is a test seam to pause a recoverer mid-operation, but exposing it as a public variable defeats package encapsulation; callers can set it unintentionally or cause test contamination> — move to an unexported variable and provide a test-only initialization function in a _test.go file or pass the hook through the Locker interface

QUESTIONS FOR THE REVIEWER

1. The gateLine regex (`AFTER: PR([0-9]+) merged`) appears in both manager.go and cardgate.go with identical patterns. Is there an intended shared constant (like `QueueLockName`) being considered, or is duplication acceptable given the small size and stable pattern?

2. The queuelock protocol requires hard links. If this tool is ever deployed on network filesystems or containers where hard-link semantics are unreliable, what is the intended fallback or migration path? The current error is named but there is no recovery.

3. The manager's releaseGates() maintains its own merge/state map local to each call, separate from placement.gates. If the same PR is gated in both fill and manager in the same tick, two separate forge calls occur instead of one deduplicated across both roads. Is this an acceptable tradeoff, or should gate caching be promoted to a shared struct?

4. launchDead() silently skips cards when MkdirAll(pending) or Rename fail, counting them as not-dead rather than logging the failure. In practice these likely indicate permission errors or directory conflicts, but is silent skip the right behaviour or should they surface in the LOOP TICK count or pulse.log?

5. The loop verb's validate() pre-flights machines, lanes, roots, and policy before any tick runs. For long-running loops (--deadline large), the user must fix all four tables before seeing output even once. Was the design choice deliberate over a lazy-validation approach where ticks proceed with missing tables and report which ones failed?

Left owed

- Did not read the full diffs of docs/CLI.md, docs/SPEC-PULSE.md, docs/spec-pulse/08-rate-and-convergence.md, docs/spec-pulse/13-tests-this-spec-demands.md in detail; only assessed that CLI help includes loop verb and spec adds rule 4.
- Did not read all 491 lines of queuelock_test.go exhaustively; sampled the first 200 lines for structure and key test names.
- Did not read cmd/nova-pulse/dryrun_test.go in full (244 lines); only checked TestFillDryRunChangesNothing.
- Did not run `go build` or `go vet` to verify compilation.
- Did not compare every test in base vs PR to assess regression risk on existing functionality.

git status --short

git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
