RESULT tools22-pre-2133-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2133 at head 0734b481de1e: lifecycle: durable attempt ledger for launch

PREREAD 2133 claims=8 proven=8 unproven=0 defects=0 high=0

PR 2133
HEAD 0734b481de1ec7575fd1420cea4e5e5f8fcf850f
BASE dev
MERGE-BASE 86abcf23dd6cd95668ae1a865e11e29556996b03
BEHIND 2
FILES 8 production, 1 test
LINES +2285 -0

CLAIMS
1. Claim linearizes READY->CLAIMED by compare-and-swapping the card's current rev and minting an unguessable attempt
   PROVEN-BY internal/lifecycle/spec_test.go:725 TestDurableLaunch1_TwoDealersRaceOneReadyCard

2. ApplyUnknown records UNKNOWN with raised=true and retains stdout, stderr, and exit when no acknowledgement arrives
   PROVEN-BY internal/lifecycle/spec_test.go:823 TestDurableLaunch2_NoAcknowledgementBecomesUnknown

3. ApplyStarted accepts only a typed, fully bound acknowledgement matching the identities already on the attempt
   PROVEN-BY internal/lifecycle/spec_test.go:882 TestDurableLaunch3_BoundAcknowledgementMakesStarted

4. After STARTING, missing job or lease files leave UNKNOWN reserved; matching exit_attest/nonce reconciles
   PROVEN-BY internal/lifecycle/spec_test.go:958 TestDurableLaunch4_MissingLeaseAndJobDirLeaveUnknownReserved

5. Crash after CLAIM reconstructs CLAIMED not READY; crash after STARTING reconstructs STARTING not READY
   PROVEN-BY internal/lifecycle/spec_test.go:1024 TestDurableLaunch7_CrashReconstructsClaimedNotReady

6. AdmitStart consumes a bounded start token and records CLAIMED->STARTING inside WithCoordinator
   PROVEN-BY internal/lifecycle/spec_test.go:1095 TestDurableLaunch8_PauseRacingLaunchTwoLegalHistories

7. Replay refuses illegal CLAIMED->READY transitions
   PROVEN-BY internal/lifecycle/spec_test.go:1210 TestReplayRefusesClaimedToReady

8. ApplyStarted is idempotent for identical bound STARTED receipts
   PROVEN-BY internal/lifecycle/spec_test.go:1248 TestApplyStartedIdenticalRetryIsIdempotent

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. Why is MaxAttempts set to 2 rather than configurable per-card or via environment?
2. What is the relationship between this lifecycle package and the "Pulse" coordinator mentioned in comments?
3. Why does the store use a JSON event log with separate projection files instead of a single append-only log?
4. Is the 10-second non-Unix lock timeout sufficient for high-contention swarm workloads?

Left owed
None - all 8 production files and the test file were read in full.

git status --short
git rev-parse HEAD
0734b481de1ec7575fd1420cea4e5e5f8fcf850f===FILE=== card-tools22-pre-2133-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2133-r1	1	2026-09-20T19:27:01Z	2026-09-20T19:34:08Z	0	inception	mercury-2.5	668751	702	0	245331	4887	0.0286
