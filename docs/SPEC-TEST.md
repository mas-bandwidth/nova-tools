# nova-test — specification (draft 1, 2026-09-17)

`nova-test` is a proposed binary at the **validation layer** (name
provisional, nova-tools #247). It makes repository validation reproducible
across local and hosted runs: a versioned manifest names what is validated
and how, an equivalent prior run is reused instead of dispatched twice, and
a failure arrives as a compact receipt rather than a log to reread. It is
distinct from `nova-check`'s record checks, `nova-swarm`'s workers and
`nova-release`'s publication; it does not start a declared instance (that
name belongs to the proposed `nova-run`).

This spec is normative. If the code and this document disagree, one of them
has a bug, and the tests decide which. It stands beside [SPEC.md](SPEC.md),
whose **Conventions** section — exit codes, no guessed paths, the one-line
output grammar, the cap-and-count rule, `internal/oneline` and
`internal/bounded` — applies here unchanged and is not restated.

This draft covers the first slice only: read-only `plan`, run lookup,
`status --since` and `failures`/`receipt` extraction for one repository.
Execution (dispatch of a new run) comes later, after the receipt contract
below is reviewed. Nothing here is a new gate for current release or Fixed
Tables work. Related: #185 (over-budget CI samples, tier-routing repair),
#83 #183 #229, specs #236/#237.

The second slice (#561, from proposal #247) fixes the test and CI rules of
2026-09-15 as the five verbs **The local and CI verbs** below. Where this
section and the first-slice text or the original proposal of #247 disagree,
this section supersedes them.

## Verbs

- `plan` reads a versioned repository validation manifest — source,
  dependency and environment identity, affected scope, required fast checks
  and explicitly requested/full/nightly tiers — and prints the concrete plan
  before execution. Configured recipes do not expand caller authority.
- `run` finds an equivalent running or completed local or hosted run before
  dispatch and reuses it. Equivalence includes source, dependencies,
  workflow/recipe revision, environment, policy, event/trust context and
  required coverage; matching SHA alone is insufficient.
- `status` surveys recent runs (`status --since`): queue, cancellation drain,
  execution and end-to-end latency per run, with attempt identities and
  prior failures preserved. Implemented (nova-tools #2207) over a run store of
  one `<run>.json` per run: `--store` and `--since` (an RFC 3339 instant or a
  duration back from the clock) are required, one `STATUS RUN` line per run
  queued at or after the boundary, then one `STATUS OK` summary with the count
  left out as `older=`.
- `failures` lists the bounded failing steps of a run with source-backed
  excerpts; full logs remain retrievable. Successful checks are
  distinguished from unexecuted, skipped or missing ones.
- `receipt` prints the compact failure receipt for one run: identity,
  equivalence key, bounded failing steps and excerpts, and latency.

## Rules

1. The manifest is versioned; `plan` names its revision in the first line.
2. Reuse requires full equivalence (verb `run`); a SHA match with a changed
   dependency, recipe revision, environment, policy, event/trust context or
   required coverage is not equivalent and must not reuse.
3. Receipts are bounded: at most the failing steps plus excerpts, one MORE
   line naming the remedy (the log holding the whole list).
4. Per-change CI belongs in fast tiers (one minute ideal, two minutes
   maximum); longer work belongs to explicit/nightly certification without
   discarding coverage.
5. Deadlines bound every subprocess; bounded output, supervisor receipts
   and safe ambiguous-dispatch reconciliation reuse the swarm execution
   substrate rather than creating another supervisor.

## Tests

Duplicate request reuses the run; stale green does not cover a newer
commit; a newer failed attempt supersedes; a partial rerun covers only its
scope; a changed dependency breaks equivalence; a timeout, a missing job
and a superseded cancellation each read as their own receipt. Measure
coordinator turns and duplicate execution with unchanged coverage.

## The local and CI verbs

The test and CI rules of 2026-09-15 as verbs. Each runs a suite or a
workflow; none dispatches a new validation run (that stays the first
slice's `run`).

- `fast` runs the per-package suite under the one-minute budget, one line
  per package with its seconds, and fails on a budget breach.
  `docs/TEST-DURATIONS.md` is its record.
- `slow` runs the slow-tagged suite; it is the nightly job, never the
  per-change path.
- `ci` generates and validates the workflow matrix from the package list.
  Pull requests run on self-hosted runners in parallel with fail-fast off,
  a fork guard and a concurrency group; hosted runners run only on main
  and nightly; `ci-ok` aggregates the tier; every action is pinned by SHA;
  the YAML is never hand-edited.
- `runners` mints the registration token, registers N runners per bench
  with platform labels and core pinning, and lists status. Any machine a
  person can ssh to may be a runner.
- `local` runs the fast suite: what a card or a read runs before a PR, so
  branch checks are local by verb.

### Rules it encodes

1. Tests answer inside the two-minute rule: one minute per package, two at
   most (#516).
2. The CI rule of 2026-09-15, memory
   `ci-on-main-only-self-hosted-parallel`: pull requests run in parallel
   on self-hosted runners, and hosted runners run only on main and nightly.
3. Rule of the day: slow CI is a crawl forever, so the slow tier is
   explicitly requested and nightly, never the per-change path.

### Replays

`fast-fails-on-budget-breach`, `slow-runs-only-with-tag`,
`ci-matrix-matches-package-list`, `ci-prs-never-use-hosted-runners`,
`ci-fork-guard-present`, `runners-register-with-pinning`,
`local-runs-fast-suite`.

## Tests this spec demands

These tests run against fake manifests, a temp-dir run store and a fake swarm/runner
substrate; nothing reaches a network, a real runner or a real secret, and each test is
proven able to fail (seen red) before it is trusted. `cmd/nova-test` implements `status`
only (15-18, in `cmd/nova-test/status_test.go`); every other behaviour below is the work
that turns the two document-only tests in `internal/docs` (which assert that
`docs/SPEC-TEST.md` *mentions* strings, not that anything behaves) into real coverage.

1. `TestPlanPrintsConcretePlanBeforeExecution` — `plan` reads a versioned repository validation manifest and prints the concrete plan before execution (it never dispatches).
2. `TestPlanNamesManifestIdentity` — the printed plan names source, dependency and environment identity.
3. `TestPlanNamesAffectedScope` — the printed plan names affected scope.
4. `TestPlanNamesCheckAndTierList` — the printed plan names required fast checks and explicitly requested/full/nightly tiers.
5. `TestConfiguredRecipeDoesNotExpandAuthority` — configured recipes do not expand caller authority.
6. `TestEquivalentRunIsReusedBeforeDispatch` — `run` finds an equivalent running or completed local or hosted run before dispatch and reuses it.
7. `TestChangedSourceBreaksEquivalence` — a changed source breaks equivalence and must not reuse.
8. `TestChangedDependencyBreaksEquivalence` — a changed dependency breaks equivalence.
9. `TestChangedRecipeRevisionBreaksEquivalence` — a changed workflow/recipe revision breaks equivalence.
10. `TestChangedEnvironmentBreaksEquivalence` — a changed environment breaks equivalence.
11. `TestChangedPolicyBreaksEquivalence` — a changed policy breaks equivalence.
12. `TestChangedEventTrustContextBreaksEquivalence` — a changed event/trust context breaks equivalence.
13. `TestChangedRequiredCoverageBreaksEquivalence` — a changed required coverage breaks equivalence.
14. `TestSHAAloneDoesNotReuse` — matching SHA alone is insufficient: a SHA match with any changed factor above must not reuse.
15. `TestStatusSinceSurveysRecentRuns` — `status --since` surveys recent runs.
16. `TestStatusReportsQueueAndCancellationDrain` — status reports queue and cancellation drain per run.
17. `TestStatusReportsLatency` — status reports execution and end-to-end latency per run.
18. `TestStatusPreservesAttemptsAndPriorFailures` — attempt identities and prior failures are preserved.
19. `TestFailuresListsBoundedStepsWithExcerpts` — `failures` lists the bounded failing steps of a run with source-backed excerpts.
20. `TestFullLogsRemainRetrievable` — full logs remain retrievable.
21. `TestFailuresDistinguishUnexecutedSkippedMissing` — successful checks are distinguished from unexecuted, skipped or missing ones.
22. `TestReceiptPrintsIdentity` — `receipt` prints the run identity.
23. `TestReceiptPrintsEquivalenceKey` — receipt prints the equivalence key.
24. `TestReceiptPrintsBoundedStepsAndExcerpts` — receipt prints bounded failing steps and excerpts.
25. `TestReceiptPrintsLatency` — receipt prints latency.
26. `TestPlanNamesManifestRevisionOnFirstLine` — the manifest is versioned; `plan` names its revision in the first line.
27. `TestReceiptIsBoundedToFailingStepsAndExcerpts` — a receipt holds at most the failing steps plus excerpts.
28. `TestReceiptNamesRemedyLogLine` — the receipt carries one MORE line naming the remedy (the log holding the whole list).
29. `TestPerChangeCIInFastTierUnderTwoMinutes` — per-change CI belongs in fast tiers (one minute ideal, two minutes maximum).
30. `TestLongerWorkGoesToNightlyWithoutDiscardingCoverage` — longer work belongs to explicit/nightly certification without discarding coverage.
31. `TestEverySubprocessHasADeadline` — deadlines bound every subprocess.
32. `TestReusesSwarmSubstrateNotANewSupervisor` — bounded output, supervisor receipts and safe ambiguous-dispatch reconciliation reuse the swarm execution substrate rather than creating another supervisor.
33. `TestDuplicateRequestReusesTheRun` — a duplicate request reuses the run.
34. `TestStaleGreenDoesNotCoverNewerCommit` — stale green does not cover a newer commit.
35. `TestNewerFailedAttemptSupersedes` — a newer failed attempt supersedes.
36. `TestPartialRerunCoversOnlyItsScope` — a partial rerun covers only its scope.
37. `TestTimeoutReadsAsItsOwnReceipt` — a timeout reads as its own receipt.
38. `TestMissingJobReadsAsItsOwnReceipt` — a missing job reads as its own receipt.
39. `TestSupersededCancellationReadsAsItsOwnReceipt` — a superseded cancellation reads as its own receipt.
40. `TestCoordinatorTurnsAndDuplicateExecutionMeasured` — coordinator turns are measured and duplicate execution is not paid with unchanged coverage.
41. `TestFastRunsSuiteUnderOneMinuteBudget` — `fast` runs the per-package suite under the one-minute budget.
42. `TestFastPrintsOneLinePerPackageWithSeconds` — `fast` prints one line per package with its seconds.
43. `TestFastFailsOnBudgetBreach` — `fast` fails on a budget breach (`fast-fails-on-budget-breach`).
44. `TestFastRecordsDurationsFile` — `docs/TEST-DURATIONS.md` is `fast`'s record.
45. `TestSlowRunsSlowTaggedSuite` — `slow` runs the slow-tagged suite (`slow-runs-only-with-tag`).
46. `TestSlowIsNightlyNotPerChange` — `slow` is the nightly job, never the per-change path.
47. `TestCiGeneratesMatrixFromPackageList` — `ci` generates and validates the workflow matrix from the package list (`ci-matrix-matches-package-list`).
48. `TestCiPRsRunSelfHostedParallelFailFastOff` — pull requests run on self-hosted runners in parallel with fail-fast off.
49. `TestCiForkGuardPresent` — the workflow carries a fork guard (`ci-fork-guard-present`).
50. `TestCiConcurrencyGroupPresent` — the workflow carries a concurrency group.
51. `TestHostedRunnersOnlyMainAndNightly` — hosted runners run only on main and nightly (`ci-prs-never-use-hosted-runners`).
52. `TestCiOkAggregatesTier` — `ci-ok` aggregates the tier.
53. `TestEveryActionPinnedBySHA` — every action is pinned by SHA.
54. `TestCiYAMLNeverHandEdited` — the YAML is never hand-edited.
55. `TestRunnersMintsRegistrationToken` — `runners` mints the registration token (`runners-register-with-pinning`).
56. `TestRunnersRegisterWithPlatformLabelsAndCorePinning` — `runners` registers N runners per bench with platform labels and core pinning.
57. `TestRunnersListsStatus` — `runners` lists status.
58. `TestAnySSHMachineMayBeARunner` — any machine a person can ssh to may be a runner.
59. `TestLocalRunsFastSuite` — `local` runs the fast suite (`local-runs-fast-suite`).
60. `TestTestsAnswerWithinTwoMinuteRule` — tests answer inside the two-minute rule: one minute per package, two at most (#516).
