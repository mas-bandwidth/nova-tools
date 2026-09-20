RESULT tools22-pre-2152-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2152 at head 9699b2715d33: swarm: guard unguarded production so reverting it goes red
PREREAD 2152 claims=14 proven=14 unproven=0 defects=0 high=0
PR 2152
HEAD 9699b2715d33bc5f4458cb3ce5084771e05db397
BASE dev
MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730
BEHIND 1
FILES 0 production, 6 test
LINES +311 -0

CLAIMS
1. PROVEN-BY cmd/nova-swarm/batch_flag_wiring_test.go:14 TestBatchAcceptsTheGatherFlagsThatLandedUnguarded — asserts --max-inflight, --stall-after, --harness, --auth are wired on batch command with exit code 2
2. PROVEN-BY internal/swarm/decide_export_test.go:12 TestDecideFinishedReturnsTheTypedDecision — asserts DecideFinished returns typed decision with correct task, label, reason, confidence, needs human fields
3. PROVEN-BY internal/swarm/decide_export_test.go:34 TestRequeueOnceRetriesAFinishedTaskAndRefusesASecondTime — asserts RequeueOnce retries finished task once and refuses second requeue
4. PROVEN-BY internal/swarm/hostguard_test.go:29 TestHostGuardCleanupRestoresCachedState — asserts environment is restored to NOVA_TEST_NO_HOST=1 and host guard cache remains enabled after subtest cleanup
5. PROVEN-BY internal/swarm/hostguard_test.go:55 TestSSHRunPanicsUnderTheGuard — asserts sshRun panics with guard message when called under host guard
6. PROVEN-BY internal/swarm/hostguard_test.go:60 TestSSHOutputPanicsUnderTheGuard — asserts sshOutput panics with guard message when called under host guard
7. PROVEN-BY internal/swarm/hostguard_test.go:65 TestSCPFilePanicsUnderTheGuard — asserts scpFile panics with guard message when called under host guard
8. PROVEN-BY internal/swarm/hostguard_test.go:73 TestCopyCardToBenchPanicsUnderTheGuard — asserts copyCardToBench panics with guard message when called under host guard
9. PROVEN-BY internal/swarm/native_verdict_line_test.go:20 TestGatherReadsHarnessSilentOnNativeIncomplete — asserts cardHarnessSilent returns true for NATIVE INCOMPLETE line with harness=silent
10. PROVEN-BY internal/swarm/native_verdict_line_test.go:29 TestGatherReadsFenceRejectedOnNativeIncomplete — asserts cardFenceRejected returns path from NATIVE INCOMPLETE line with fence=rejected
11. PROVEN-BY internal/swarm/native_verdict_line_test.go:39 TestGatherDoesNotTreatANonVerdictLineAsNative — asserts cardHarnessSilent and cardFenceRejected both reject NATIVE NOTE as non-verdict line
12. PROVEN-BY internal/swarm/route_core_test.go:18 TestParseRoutesAndPickRoute — asserts ParseRoutes parses routes.tsv and PickRoute selects correct card based on kind, type, confidence
13. PROVEN-BY internal/swarm/route_core_test.go:41 TestParseRoutesRefusesAnEmptyTable — asserts ParseRoutes returns error for empty routes table
14. PROVEN-BY internal/swarm/slot_holdings_test.go:15 TestSlotHoldingsCountsTheOwnerAndNamesTheShare — asserts SlotHoldings returns correct held and share counts for each owner

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. The merge base a7611c8189d33979923064f4d10eb07fb9957730 is older than the card's base 5298f6be12ea; was this PR cut from a stale branch or was dev rebased?
2. Why are all negative control tests landing together in one PR rather than with their respective production commits?
3. The armHostGuard helper in hostguard_test.go uses t.Cleanup(testguard.Reload) before t.Setenv; should the cleanup order be documented to explain why it must run last?

Left owed
All production files touched by the referenced production commits (batch_flag_wiring, decide.go, bench.go, benchpull.go, native_verdict_line, route.go, slots.go) were not read because this PR contains only tests. The referenced commits in comments (2d99edbf, 021e9e4b, 037d8222, 47d81e9c, 6ac72b6a, 37ae3125, b1c69694) were not inspected.

git status --short
git rev-parse HEAD
9699b2715d33bc5f4458cb3ce5084771e05db397===FILE=== card-tools22-pre-2152-r4/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2152-r4	1	2026-09-20T19:56:34Z	2026-09-20T19:57:32Z	0	inception	mercury-2.5	159318	318	0	38102	3739	0.0071
