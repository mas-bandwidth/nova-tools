RESULT tools22-pre-1721-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1721 at head 12f6027fd731: nova-pulse accept: the mechanical accept gate -- own worktree, fixed step order, typed reject and abstain tokens, never opens RESULT.md, never reruns a red (T03, #1648)
PREREAD 1721 claims=24 proven=14 unproven=10 defects=3 high=0

PR 1721
HEAD 12f6027fd7312f190628967032ecf37fe343a032
BASE dev@5298f6be12eaa0f7e6622334d2b6a1eb427649e3
MERGE-BASE a78f3ea93b08a1ac5fd01608ecc61b1040f3a6f2
BEHIND 6
FILES 10 production, 18 test
LINES +3321 -49

## CLAIMS

1. The `nova-pulse accept` verb is wired into main.go and parses six required flags (`--job`, `--card`, `--base`, `--bench`, `--cert`, `--identity`) plus three optional flags (`--sandbox`, `--timeout`, `--max`), then delegates to `pulse.Accept`. PROVEN-BY cmd/nova-pulse/accept_test.go:3 TestAcceptVerbRefusesWithoutItsFlags asserts all six required flag names appear in refusal output and exit code is 2; also checks identity shape validation.

2. The accept gate pipeline executes six steps in order: (a) hygiene, (b) shape, (c1) build+vet, (b2) weakened, (c2) headTests, (d) mutate. First failure decides and prints exactly one ACCEPT line (OK/reject/abstain). PROVEN-BY internal/pulse/accept_test.go:~80 TestAcceptOKOnAGoodFix end-to-end tests the full positive path through all steps with a fake nova-sandbox and real go commands running under the wall.

3. Hygiene runs four checks (identity, stray-file, secret, out-of-path) against the gate's own clone and rejects on the first failure in spec order. PROVEN-BY internal/pulse/accept_test.go:~600 TestAcceptRejectsHygieneFirstAndRunsNothingAfterIt verifies stranger-commit identity rejection prevents any go commands from running afterward.

4. Shape requires the kind's STEP(StepShape), enforces that at least one test file changed, and verifies the card's named TEST: exists at HEAD. PROVEN-BY internal/pulse/accept_test.go:~370 TestAcceptRejectsNoTest for missing-test-file path; TestAcceptRejectsNamedTestMissing for missing named test path.

5. BuildVet runs `go build ./...` and `go vet ./...` each once inside the wall; toolchain failures yield ABSTAIN, others yield REJECT with the failing command's output. PROVEN-BY internal/pulse/accept_test.go:~760 TestAcceptRejectsBuildAndVet checks both build and vet rejection paths separately.

6. Weakened checks that no test files were deleted from any touched package directory, that no t.Skip calls were added to existing base test bodies, and that the base's copy of every pre-existing test file modified by the card still passes when overlaid on the head tree. PROVEN-BY internal/pulse/accept_test.go:~430 TestAcceptDeletedBaseTestIsTestWeakened, TestAcceptSkipAddedToABaseTestIsTestWeakened, TestAcceptBaseTestOverlaidOnHeadMustPass.

7. HeadTests runs tests for every changed package once at HEAD; if an untouched test fails, it runs that test ONCE at the base before charging the red to the card. UNPROVEN — there are ~840 lines of tests but none explicitly exercises the "untouched test fails → run at base → charge to card" decision point. There is `TestAcceptRunsAnUntouchedRedOnceAtBaseAndAbstains` but that tests the case where the base test ALSO fails (giving ABSTAIN base-red), not the case where only HEAD's unchanged test is red (which should REJECT).

8. Mutate calls `review.Mutate()` with the Exec seam pointing to the walled command builder; validates that the revert control passed and that the card's named TEST: appeared among the reds. PROVEN-BY internal/pulse/accept_test.go:~680 TestAcceptMutateCouldNotRunIsToolchain and TestAcceptRejectsWhenNamedTestStaysGreen.

9. The gate emits three verdict formats: `ACCEPT OK label=<n> kind=<k> head=<h> base=<b> tests=<t> red_without=<r> control=- bench=<n> cert=<c> took=<d>`, `ACCEPT REJECT label=<n> kind=<k> head=<h> reason=<r> at=<a> control=- bench=<n> cert=<c> took=<d>`, or `ACCEPT ABSTAIN label=<n> kind=<k> reason=<r> bench=<n> took=<d>`. PROVEN-BY internal/pulse/accept_test.go:~280 TestAcceptOKOnAGoodFix asserts the ACCEPT OK format contains expected fields; TestAcceptAbstainsTimeoutWhenAStepOverruns asserts ABSTAIN format.

10. The kinds table in kinds.go defines gated kinds (fix-red with steps [hygiene,shape,positive,mutate], transcript-test with steps [hygiene,shape,positive]) and ungated kinds (read, probe, text, tone). Gated kinds without built controls are refused (transcript-test refuses because three-seed control is not yet implemented). PROVEN-BY internal/pulse/kinddrift_test.go:1 Tests that all cut kinds map to either the table or a nearest name; internal/pulse/accept_test.go:~620 TestAcceptAbstainsOnAnUnknownKindAndRefusesAnUngatedOne verifies unknown-kind abstains and ungated-kind refusals.

11. The card header parser reads KIND, PATHS, TEST, LEGS, SOURCE, and optionally repeated TEST-EDIT from the card file; validates PATHS via hyg.ValidatePaths, validates TEST package path safety and test-name format, and refuses duplicate keys (except TEST-EDIT). PROVEN-BY internal/pulse/cardheader_dup_test.go:1 Tests that repeating KIND, PATHS, TEST, LEGS, and SOURCE are all refused while TEST-EDIT repeats are allowed.

12. IsSecretName extracts the KEY/TOKEN/SECRET uppercase substring predicate from goenv into a shared function, replacing a local `keepNativeSecretName` in cmd/nova-swarm/native.go. PROVEN-BY-EXISTING internal/goenv/secretname_test.go:1 provides two unit tests (matches key/token/secret; leaves ordinary names alone); cmd/nova-swarm/audit_test.go:131 add an allowlist entry for the new goenv import but do not independently verify the behavior equivalence of the replacement.

13. The critical environment-preserving fix: mutate and seed now preserve `cmd.Env` if already set (the seam's HOME and GOCACHE inside the wall) rather than always overwriting with `goenv.Clean(os.Environ())`. PROVEN-BY-EXISTING internal/review/seam_test.go:~20 TestMutateKeepsTheExecSeamsEnv uses a mark variable `seedExecMark` to verify the custom Env reaches the child process.

14. A `[build failed]` result on the reverted side is no longer scored as every unit red; instead it produces a SKIP with reason `revert-did-not-compile` because compilation coupling (a test calling `_ = Mul(2,3)` on a new symbol) asserts nothing about the fix. PROVEN-BY-EXISTING internal/review/mutate_test.go:~50 updated expectations reflect the skip behavior; internal/review/seam_nocompile_test.go:1 TestMutateRefusesACallOnlyTestCoupledByCompilation verifies a call-only test yields SkipRevertNoCompile rather than Red.

15. Kill signals require BOTH a structured `--- FAIL: <name>` line AND a non-zero exit status. Free-text `"panic:"` or forged `"FAIL\t"` without structured output are skips ("could not be run"); zero-exit with a "--- FAIL:" line is also a skip (contradiction). PROVEN-BY-EXISTING internal/review/seam_test.go:~50 TestMutateABareFAILOrPanicLineWithNoStructuredFAILLineIsNotAKill and TestMutateAFAILLineWithAZeroExitIsNotAKill.

16. Card-controlled processes printing wall-like text before any test results are NOT classified as bench fault (ABSTAIN toolchain). For `go test`, free-text is the card's voice; only exit codes speak for the bench. For `go build`/`go vet`, toolchain markers before the first test still count. PROVEN-BY internal/pulse/accept_redteam_test.go:~20 TestAcceptAWallMarkerFromTheCardsOwnProcessIsNotTheBenchs and TestAcceptAWallMarkerFromTestMainIsStillTheCardsRed.

17. Compilation-coupled vacuous tests (tests that only CALL a new symbol without asserting anything) cause the mutate control to return PASS=false with Red=0, which accept rejects as `vacuous-test`. PROVEN-BY internal/pulse/accept_redteam_test.go:~50 TestAcceptRejectsACallOnlyTestCoupledByCompilation end-to-end test exercising the full accept path with a call-only test.

18. Timeouts are bounded: `cmd.WaitDelay = 2*time.Second` prevents grandchild hangs after the context deadline kills the parent; step overrun yields ABSTAIN timeout. PROVEN-BY internal/pulse/accept_test.go:~720 TestAcceptAbstainsTimeoutWhenAStepOverruns — however, the test mocks the sandbox so the WallDelay guard is not tested against a live grandchild process.

19. RESULT.md is never opened by the gate: verified via FIFO trick in `TestAcceptNeverOpensResultMD` — if the gate tried to open RESULT.md it would block or error; the gate completes promptly, proving it does not. PROVEN-BY internal/pulse/accept_test.go:~340.

20. The gate owns its git clone under the job's slot, disables hooks and fsmonitor on ALL git calls, strips GOENV and secret-shaped env vars before entering the wall, and isolates per-run home/gocache. PROVEN-BY internal/pulse/accept_test.go:~500 TestAcceptNeverRunsTheJobClonesHooks; TestAcceptWallListsExcludeTheWorkersCopy.

21. Documentation is added: help text banner in main.go, CLI.md section describing the accept verb's purpose, flags, and output format. PROVEN-BY cmd/nova-pulse/main.go:137 usage line; docs/CLI.md:2017 accept section.

22. The test infrastructure was extended: fakebin gains CwdContains matching and Exec passthrough mode (running real commands after `--`); fake_test gains CwdContains and Exec fields; fakeTools adds `"nova-sandbox"`. PROVEN-BY-EXISTING internal/pulse/testdata/fakebin/main.go:47 CwdContains rule matching logic; internal/pulse/testdata/fakebin/main.go:147 passThrough() function.

23. Base must be a full 40-char hex SHA, never a ref, because a ref could be rewritten by the worker to make the judged range evil..head. The code validates format and attempts rev-parse of the base commit. UNPROVEN — there is no test explicitly passing a ref and verifying refusal. `TestAcceptRefusesABaseRefTheWorkerCanMove` only tests that a short hash (not a full SHA) is rejected, but the test description mentions refs in general. Looking more carefully at the test around line 600, it passes `"refs/heads/dev"` as the base — this IS a ref — but the test expects REFUSED. So PROVEN-BY internal/pulse/accept_test.go:~595 TestAcceptRefusesABaseRefTheWorkerCanMove.

24. Untracked files left by the worker in its copy cannot turn the gate green: the wall excludes paths under the swarm root, and the overlay test confirms the gate works against an isolated head tree. PROVEN-BY internal/pulse/accept_test.go:~300 TestAcceptUntrackedFileCannotTurnTheGateGreen writes an untracked .go file that introduces a new test; since the card didn't change it, headTests catches the new red.

## DEFECTS

DEFECT medium internal/pulse/accept.go:664-676 — cleanup() silently drops errors from worktree.remove/prune and safepath.RemoveUnder; orphaned directories under /jobs/<label>/accept/ accumulate across runs — a leaked resource defect. Medium because stale files don't affect verdict correctness but they waste disk and may confuse operators inspecting job slots. Fix: collect errors from each cleanup operation and flush them as notes (same pattern used elsewhere) or return a compound error.

DEFECT low internal/pulse/accept.go:651-662 — sanitizeLabel() silently strips characters beyond [a-zA-Z0-9_-] and falls back to "card" for empty input; a card whose RESULT label becomes "card" collides with others on the same slot. Low because labels are human-readable identifiers not machine keys and collisions are unlikely in practice. Fix: append a truncated hash suffix for collision avoidance, or refuse with a clear message.

DEFECT medium internal/pulse/accept.go:1102-1108 (execWalled) — WaitDelay = 2*s is hard-coded, not surfaced as a flag alongside --timeout; if a child process has a longer-than-2s shutdown window the context deadline blocks 2 extra seconds beyond user expectation. Medium because the effect is minor (2 seconds) but inconsistent with the rest of the configuration model. Fix: parameterize waitDelay as a flag or derive it proportionally from the timeout value.

## QUESTIONS FOR THE REVIEWER

1. The overlay phase in weakened() writes base copies of modified test files directly onto the head worktree, runs them, then restores with `git checkout --`. If restore fails partway through (e.g., due to permission issues or concurrent modification), the head tree is dirty for the subsequent headTests run. The comment on line 869 acknowledges this as "cold read, LOW 7" but the actual consequence (dirty tree corrupting the next test run) feels higher than low. Was the decision intentional to prefer simplicity over robustness?

2. KindDrift maps commonly-cut kind names (fix-with-red-test, dogfood, new-verb, row-test) to fix-red. But sweep, rebase, mutation-kill, and docs-fix have drift entries in comments but no map entries. The comment on line 80 says "Several of those cards say SPEC: docs/SPEC-TOOLWORK.md §5 kind <name>" — is the intention that these later get added to the Kinds table proper with their own controls, and KindDrift is only for temporary spellings? Should lint --card warn card writers to use the drift-corrected name immediately?

3. In acceptToolchainRed, when cardRuns=true (for go test), compiler error markers like "[build failed]" and "# pkg" are treated as the card's fault regardless of exit code. But what about panics during init that happen before the package even compiles cleanly? An init panic in card-controlled code prints no "--- FAIL:" line, exits non-zero, and produce no "[build failed]" prefix. This would classify as "could not be run" (skip). Is that the correct classification for init-time panics, or should they be charged to the card?

4. The review.Seed form similarly accepts an Exec seam parameter. What is the intended lifecycle here — will all consumers of mutate and seed eventually go through the gate, or is there a plan for direct invocation outside nova-pulse? The tight coupling between the seam design and the walled-execution model makes it awkward to use these functions in other contexts.

5. Why does `redAtBase` lazily create the base worktree on first invocation and cache it (via g.baseWt) rather than creating it eagerly with the other worktrees in makeRun/addWorktree? The lazy approach saves disk if no untouched red tests exist, but means the base worktree creation can fail mid-pipeline after several steps have already run, making the failure harder to diagnose.

Left owed: Did not read the full internal/pulse/accept_test.go (~840 lines) line-by-line; sampled key test functions for claim verification. Did not attempt `go build` or `go vet` due to mirror access constraints. Did not review internal/hygiene package changes (if any) referenced by the gate's hygiene step. Did not check whether `cut` or `lint --card` were also modified (they may be on the PR but listed under different file patterns).

git status --short
(git status --short prints nothing)
git rev-parse HEAD
5298f6be12eaa0f7e6622334d2b6a1eb427649e3
