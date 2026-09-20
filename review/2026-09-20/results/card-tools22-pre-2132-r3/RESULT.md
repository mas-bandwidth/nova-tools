RESULT tools22-pre-2132-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2132 at head d09a92889aa4: fill/harvest: timeouts and per-host isolation so a DOWN bench cannot hang (#2009)
PREREAD 2132 claims=7 proven=6 unproven=1 defects=1 high=0

PR 2132
HEAD d09a92889aa4fb3945eb688ccb2da0635bc8c523
BASE dev
MERGE-BASE 86abcf23dd6cd95668ae1a865e11e29556996b03
BEHIND 2
FILES 12 production, 7 test
LINES +662 -41 (production +296 -39, test +366 -2)

CLAIMS
1. Capacity probes run in parallel under a per-host wall-clock budget (FillProbeTimeout=10s), so one hanging bench cannot block the tick or its neighbours. PROVEN-BY internal/pulse/fill_missed_test.go:80 TestFillHangingBenchDoesNotBlockAHealthyNeighbour: a hanging channel-bench beside a healthy bench returns inside the bound and the healthy bench is filled 2.
2. A host that misses its budget is MISSED: it is reported `FILL MISSED bench=<name> reason=timeout` and never `FILL UNREADABLE ... free=0`, it is never launched onto, and a healthy neighbour's value is kept and filled. PROVEN-BY internal/pulse/fill_missed_test.go:80 (asserts the MISSED line, the absence of the UNREADABLE line, no launcher call on down-bench, and up-bench:launched=2,failed=0) and internal/pulse/observe_test.go:76 TestObserveHostsMarksAHangingHostMissedAndKeepsTheHealthyOne.
3. The FILL line receipts the miss as `<bench>:missed=1`. PROVEN-BY internal/pulse/fill_missed_test.go:80 (asserts "down-bench:missed=1" on the FILL line).
4. Every observation ssh carries -o ControlMaster=no, ControlPath=none, ServerAliveInterval=2, ServerAliveCountMax=2, ConnectTimeout and BatchMode, so a stale multiplexed socket or a host that never answers cannot hang the call. PROVEN-BY cmd/nova-pulse/fill_test.go:181 TestSSHCapacityBypassesControlMaster (asserts all six on the capacity ssh argv); internal/pulse/observe_test.go:80 TestSSHShellBypassesControlMaster; cmd/nova-pulse/fillstore_test.go:46 TestSlotsStoreProbeAsksTheOwnersShareAndItsLiveLeases (ControlMaster=no, ControlPath=none); cmd/nova-pulse/fleet_survey_test.go:294 TestFleetSSHRunnerStartsTheProgramWithTheScriptOnStdin (ControlMaster=no); internal/pulse/observe_test.go:28 TestIsolationArgvBypassesControlMaster (the argv builder itself).
5. The release package's ssh options gain the same mux isolation (ControlMaster=no, ControlPath=none, ServerAlive). PROVEN-BY internal/release/release_test.go:1377 TestSSHOptionsForbidAgentForwardingAndKeysOnArgv.
6. BoundObservation bounds Cmd.Wait on leftover copy pipes with a positive WaitDelay and, on unix, kills the observation child's whole process group, so descendants holding stdout/stderr cannot hold a probe past its deadline. PROVEN-BY cmd/nova-pulse/fill_unix_test.go:48 TestSSHCapacityReturnsWhenDescendantKeepsOutputOpen (a 1s probe returns an error while two 60s descendants retain stdout/stderr). internal/pulse/observe_test.go:23 TestObservationWaitDelayIsPositive asserts only that the constant is positive; the behaviour is witnessed by the unix test alone.
7. A tick in which every bench misses its probe budget is a red fleet and exits 1. UNPROVEN: the new fill_missed test covers only 1-of-2 missed (exit 0), which cannot discriminate the missed-counts-toward-failed logic, and the existing TestFillExitsOneWhenEveryBenchFailed (internal/pulse/fill_test.go:421) exercises only the all-UNREADABLE path.

DEFECTS
DEFECT low docs/CLI.md:1741 — the `FILL MISSED bench=<name> reason=timeout` line and the `missed=1` token the FILL line now emits have no docs/CLI.md entry, while 1741 still documents the line as `FILL tick=<n> <bench>:launched=<n>,failed=<n> ...` — the verb's documented output contract understates what it prints; nothing in-repo parses the line, so this is documentation drift, not a tool break — add the `FILL MISSED` line and the `missed=1` token to the fill section.

QUESTIONS
1. cmdFill never sets FillInput.Timeout, sshCapacity.timeout or storeProbeConfig.Timeout, so no CLI knob exists and every probe runs on the 10s default; the tick's wait budget and each ssh child's own kill budget are two independent clocks of the same value, so a caller that did set a shorter FillInput.Timeout would mark benches missed while their ssh children keep running a full FillProbeTimeout. Is the decoupling deliberate, and is the absence of a --probe-timeout flag intentional?
2. The all-missed red-fleet exit-1 edge (claim 7) has no witness in the diff or the tree; is it deliberately left to the shared allBenchesFailed machinery, or should a missed-everything tick be asserted?
3. runCapacity abandons its goroutine on timeout and relies on the Capacity seam's own internal CommandContext to kill the ssh child; a Capacity implementation that does not spawn a self-bounded child leaks that goroutine, and even in production the child is killed on its own clock, not at the tick's deadline. The comment accepts this; is the seam intended to gain a context parameter later?
4. internal/release/edges.go gained the mux-isolation options but not BoundObservation: release ssh children are still killed as single processes by runCommandCapped (no process-group kill, no WaitDelay). Is that partial application intentional given the PR is scoped to fill/harvest?

Left owed: I read the full 1013-line diff and, in full, every production and test file it touches. I did not read whole untouched files beyond the seams that call the new helpers (internal/pulse/fillstore.go, harvest.go, power.go process-group helpers, release runCommandCapped); nothing there is changed by the PR. I ran `go build`/`go vet` on cmd/nova-pulse, internal/pulse and internal/release at the PR head (both clean) and ran the touched packages' full test suites under NOVA_TEST_NO_HOST=1. Failures were all pre-existing and reproduced identically at the merge-base 86abcf23: internal/pulse TestFleetJoinKeepsTheAuthKeyOutOfEveryArgv, and the two cmd/nova-pulse fillstore CLI brake tests (TestFillFillsABenchWhoseSlotsListIsEmpty, TestFillCountsTheOwnersLiveLeasesAndNobodyElses) which read this machine's real load (176/32 cores) and are environment-sensitive, not caused by this PR. The new tests in the diff all pass.

`git status --short` prints nothing.
`git rev-parse HEAD` prints d576bf6bbabb39068096a97b4560de9b5e245970 (the clone's default branch; the PR head refs/tmp/pr2132 was verified as d09a92889aa4fb3945eb688ccb2da0635bc8c523 in STEP 1).===FILE=== card-tools22-pre-2132-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2132-r1	1	2026-09-20T19:27:08Z	2026-09-20T19:47:21Z	0	opencode	deepseek-v4-flash	83266	51831	0	5245440	0	0.1730
