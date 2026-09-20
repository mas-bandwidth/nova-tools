RESULT tools22-pre-1433-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1433 at head 6be42131a2af: wake: one eager build for the package's test programs — and the measured reason 62.9 s is not w
PREREAD 1433 claims=4 proven=0 unproven=4 defects=1 high=0

PR 1433, HEAD 6be42131a2af9343909120e830856fde0465b94d, BASE dev, MERGE-BASE 04bb4e1c7ee73537396bf436701f129233446432, BEHIND 71, FILES 0 production, 2 test, LINES +71 -72

CLAIMS

1. All five of the package's test programs (fakebus, fakegh, fakenote, fakegit, recordbus) are built once, eagerly in TestMain before any test runs, in a single `go build`. — UNPROVEN — it is structural; no test in the diff or the tree would go red if the build were lazy again or split back into two, so nothing witnesses the "once, eagerly, in TestMain" part.
2. No test's recorded duration is charged the toolchain any more, so the measured per-test seconds are the tests themselves (the 6.8 s test that is really a ~2 s test). — UNPROVEN — a measurement claim; no test can witness it and the diff asserts no recorded duration.
3. The recording nova-bus wrapper (recordbus) joins the four PATH fakes in that one build, and the separate per-test recordbus build behind its own sync.Once is gone. — UNPROVEN — structural; the advance tests (cmd/nova-wake/advance_test.go:235 etc.) assert behaviour of the real bus, not the shape of the build, and would not go red if recordbus were built separately again.
4. What survives the build is the bytes; no directory outlives TestMain's own. — UNPROVEN — a cleanup claim; nothing in the diff or the tree asserts it (and a leaked directory would not turn any test red).

DEFECTS

DEFECT low cmd/nova-wake/main_test.go:60 — the comment says the change removes "the last of it -- three separate `go build` invocations, each behind its own sync.Once, ... each CHARGED TO WHICHEVER TEST HAPPENED TO ASK FIRST", but it removes only two (fakeOnce, recordOnce); nova-bus's build behind busOnce (cmd/nova-wake/advance_test.go:41-90) remains, lazy and still charged to whichever advance test asks first — a reader is sent to believe no per-test-charged build is left, which is the wrong conclusion this very change exists to prevent, and the commit message itself contradicts the count by saying "nova-bus keeps its own build" — say "two builds become one eager build in TestMain; nova-bus keeps its own lazy build".

QUESTIONS

1. nova-bus's build stays lazy behind busOnce (advance_test.go:41) and its cost is still attributed to whichever advance test asks first — the exact misattribution this change exists to remove. Is the accepted trade that the remaining charge lands only on the slow real-bus tests that are skipped under -short, or was moving that build into TestMain too considered and rejected?
2. The change is justified by an Air measurement (6.8 s recorded vs ~2 s true for TestALineThatNeverSignedDoesNotFlip) that does not appear in the checked-in docs/TEST-DURATIONS.md (cmd/nova-wake 12.4 s, slowest test "-"). Is a regeneration of that file expected as part of this work or a follow-up, since it is the record the two-minute budget is enforced against?
3. buildFakes now runs on every invocation of this package's tests, including -short runs and a single non-fake test such as TestTheToolSaysWhichBuildItIs, which previously could build nothing. Is the fixed sub-second cost on every run accepted as the price of stable attribution rather than building only when the selected tests need the fakes?

Left owed: I read both changed files in full (cmd/nova-wake/main_test.go, 1432 lines; cmd/nova-wake/advance_test.go, 594 lines), the recordbus testdata program, and internal/goenv. I verified the single `go build -o <dir> <5 packages>` produces exactly the five binary names buildFakes reads back (fakebus, fakegh, fakegit, fakenote, recordbus), and `go vet ./cmd/nova-wake/` compiles the package clean. I did not run the test suite (none required; no test is expected on this card), so I did not observe a real timing/attribution measurement; the measurement claims are therefore UNPROVEN. Unrelated test files were not read beyond the parts touching install/fakes.

git status --short (must print nothing):
(empty)

git rev-parse HEAD:
6be42131a2af9343909120e830856fde0465b94d===FILE=== card-tools22-pre-1433-r4/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1433-r4	1	2026-09-20T19:27:25Z	2026-09-20T19:40:23Z	0	opencode	deepseek-v4-flash	58369	32748	0	1686528	0	0.0646
