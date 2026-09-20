RESULT tools22-pre-1615-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1615 at head 2c3748deab751ca2d73e7aa97033fb2bb445eb7b: nova-swarm: --tokens is required on every native launch (SPEC-SWARM rule 13d, #1545)
PREREAD 1615 claims=11 proven=10 unproven=1 defects=0 high=0

PR 1615
HEAD 2c3748deab751ca2d73e7aa97033fb2bb445eb7b
BASE dev
MERGE-BASE a78f3ea93b08a1ac5fd01608ecc61b1040f3a6f2
BEHIND 6
FILES 18 production, 13 test
LINES +722 -90

## CLAIMS

1. `nova-swarm native` requires `--tokens <number>` or `--tokens unmetered`; a missing word is exit 2 naming the flag, and `--tokens 0` is refused. PROVEN-BY cmd/nova-swarm/native_budget_test.go:71 TestNativeRefusesWithoutTheBudgetWord — asserts rc==2 for absent and zero, names flag on stderr, verifies no job/data/tmp directories created on either refusal.

2. A launch with `--tokens unmetered` prints `budget=unmetered` on the NATIVE OK line. PROVEN-BY cmd/nova-swarm/native_budget_test.go:107 TestNativeUnmeteredPrintsTheWordOnTheLine — launches under unmetered, asserts stdout contains ` budget=unmetered `.

3. A numeric budget prints `budget=<spent>/<n>`, `budget=<spent>+/<n>` for partial observation, or `budget=-/<n>` for no observation (not `unmetered`). PROVEN-BY cmd/nova-swarm/native_budget_test.go:126 TestNativeNumericBudgetPrintsAgainstTheNumber — asserts `/50000 ` present and `budget=unmetered` absent.

4. The `budget=` field sits immediately after `harness=` and before every optional tail, preserving a fixed-line grammar. PROVEN-BY cmd/nova-swarm/native_budget_test.go:145 TestNativeBudgetSitsWhereTheGrammarPutsIt — iterates NATIVE OK fields, finds harness= then asserts next field starts with budget=.

5. `batch --cards` requires `--tokens`; a batch without it exits 2 before any card starts (verified via argv record being absent). PROVEN-BY internal/swarm/batch_tokens_13d_test.go:35 TestBatchCardsRefusesWithoutTheBudgetWord — fixture records every `nova-swarm` child argv; absence proves no card started; exit 2 and stderr contain `--tokens`.

6. The batch puts `--tokens` verbatim (never parsed, never divided) into the local `native` argv under `--harness`. PROVEN-BY internal/swarm/batch_tokens_13d_test.go:63 TestBatchCardsPutsTheWordInTheLocalNativeArgv — parameterized on `"250000"` and `"unmetered"`, asserts the exact string `--tokens <word>` appears in recorded argv.

7. The same token word crosses verbatim to remote bench `native` over ssh. PROVEN-BY internal/swarm/batch_tokens_13d_test.go:120 TestBatchCardsPutsTheWordInTheRemoteNativeArgv — fake ssh log captured, asserts every `nova-swarm native` line contains `--tokens 175000`.

8. A `--runner` is handed the token word as its sixth positional argument (after label, slot, model, card path, root). PROVEN-BY internal/swarm/batch_tokens_13d_test.go:159 TestBatchCardsHandsTheRunnerTheWordAsItsSixthArgument — asserts exactly six args, index 5 equals `"90000"`.

9. The pool's `RUN DONE`/`RUN KILLED` and `native`'s `NATIVE OK` share ONE rendering path (`BudgetWord` in usage.go), preventing drift between the two routes. PROVEN-BY internal/swarm/usage.go:206 BudgetWord (extraction) — finish.go:528 now delegates to `BudgetWord()` instead of its own switch; comment block states the shared-rendering policy.

10. Pulse's `runBatch` passes the configured budget token through `--tokens` to the `nova-swarm batch` subprocess. PROVEN-BY-EXISTING internal/pulse/launch.go:361 — new block defaults empty tokens to `DefaultLaunchTokens` ("unmetered") then appends `--tokens <value>` to the exec.Command call.

11. CLI help text, TESTS.md examples, and the quickstart shell script are updated to reflect the new `--tokens` requirement. UNPROVEN — these are documentation files updated in-tree but not tested by any unit test in this diff.

## DEFECTS

DEFECTS none

## QUESTIONS FOR THE REVIEWER

1. The merge-base is `a78f3ea9` (integration-16am), which is 6 commits behind dev@`5298f6be`. Was this intentional (the PR is meant to land gated later), or should this have been rebased first?

2. `internal/swarm/usage.go` introduces `BudgetWord` as the single shared rendering path for both the pool route and native route. Does this function need to be exported (capital B) or should it be lowercase since it's only called internally from `finish.go` and nowhere else currently?

3. The fake runner (`testdata/fakerunner`) treats 7 args as a runner and anything different as native. A runner written before this PR that only reads args[1]–args[5] will silently ignore arg[6]. Is there a plan to audit third-party runners, or is that intentionally left to the natural failure mode of hitting `native`'s own refusal?

4. The NATIVE output line inserts `budget=` between `harness=` and the optional tails (`fenceSuffix`, `usageSuffix`, `termSuffix`). If any tool already parses the NATIVE line expecting `harness=` followed immediately by optional fields, this shift could misalign field positions. Were downstream consumers of this output identified?

Left owed — I did not run `go build` or `go vet` due to lack of accessible mirror. I skimmed but did not deeply review the following diffs beyond surface assertion checking: cmd/nova-swarm/native_test.go (17 call-site updates), cmd/nova-swarm/fence_test.go, cmd/nova-swarm/native_slot_lease_test.go, cmd/nova-swarm/native_verdict_test.go, cmd/nova-swarm/native_deadline_test.go, internal/swarm/batch_route_cap_test.go, internal/swarm/batch_test.go, internal/swarm/batch_issue618_test.go, internal/swarm/batch_native_636_test.go, internal/swarm/batch_reliability_test.go, internal/swarm/batch_tree_640_test.go, internal/swarm/batch_usage_reaped_test.go, internal/swarm/bench_test.go, internal/swarm/benchpull_test.go, internal/swarm/publicbatch_test.go, internal/swarm/cardbudget_test.go, internal/swarm/fakerunner_test.go, internal/pulse/launch.go (the new tokens-default block and exec.Command line), and the three docs files. The core behavioral claims in claims 1–9 are fully covered by the two new test files I read in full.

a78f3ea9 integration-16am: #1669 node lease, #1842 review/check dogfood fixes, #1856 journal identity, #1969 cut --kind, #1943 live-seat keep — gated on hulk (#1985)
HEAD a78f3ea93b08a1ac5fd01608ecc61b1040f3a6f2

git status --short
(empty)
git rev-parse HEAD
5298f6be12eaa0f7e6622334d2b6a1eb427649e3
