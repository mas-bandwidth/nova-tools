RESULT tools22-pre-1869-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1869 at head 040ba8f81934: bus wait, wake serve: --no-beat, so a waker beside a line does not beat for it (#1517) — #1837 
PREREAD 1869 claims=7 proven=5 unproven=2 defects=1 high=0

PR 1869
HEAD 040ba8f81934c9137561ea72738b7659597216ef
BASE dev
MERGE-BASE 0f0e46c0e26bfbab7fc84c0dfead4f551fb1c503
BEHIND 21
FILES 2 production, 3 test
LINES +208 -17

CLAIMS

1. `nova-bus wait --no-beat` writes and pushes no BEAT at all — no entry beat, no tick beat, no beat file, no beat commit — so a poll beside a line is read-only for the line's presence.
   PROVEN-BY cmd/nova-bus/issue1517_test.go:44 TestIssue1517WaitWithNoBeatWritesNoBeat — runs `wait --no-beat` on a settled bus and asserts from-ada/BEAT does not appear on disk and no from-ada/BEAT commit exists.

2. The default `wait` (no `--no-beat`) still writes and commits its BEAT, so presence for everyone else is unchanged — the flag does not quietly disable beating everywhere.
   PROVEN-BY cmd/nova-bus/issue1517_test.go:53 same test's control — a default `wait` on the same bus is asserted to leave from-ada/BEAT on disk and a from-ada/BEAT commit in the log.

3. `--no-beat` changes nothing else about the wait: the exit code and the WAIT TIMEOUT line's `cursor=` are identical with and without the flag.
   PROVEN-BY cmd/nova-bus/issue1517_test.go:65 same test — compares the two runs' exit codes and the `cursor=` field of their WAIT TIMEOUT lines.

4. `wait --no-beat` given together with `--beat` or `--beat-lease` is refused (exit 2, naming the two flags).
   UNPROVEN — the refusal at cmd/nova-bus/main.go:2922 is not exercised by any test in the diff or the tree.

5. `wait --no-beat --advance` advances the cursor while its commit does not name the BEAT (advanceCursorTo drops BeatPath from the commit and the allow list).
   UNPROVEN — no test calls advanceCursorTo/advanceCursor with noBeat=true; only `TestAnAdvanceWithNoLaneWritesNothing` (cmd/nova-bus/walk_test.go:312) passes the new parameter, and it passes `false`.

6. `nova-wake serve --no-beat` passes `--no-beat` through to every `nova-bus wait` argv its polls spawn.
   PROVEN-BY cmd/nova-wake/issue1517_test.go:60 TestIssue1517ServeNoBeatReachesTheBusArgv — for a `serve --no-beat` run, every recorded fake `wait` invocation is asserted to carry the token `--no-beat`.

7. `serve` without `--no-beat` leaves the poll argv exactly as it was before the flag existed.
   PROVEN-BY cmd/nova-wake/issue1517_test.go:66 same test's control — for a `serve` run without the flag, no recorded wait invocation carries `--no-beat`.

DEFECTS

DEFECT low cmd/nova-bus/main.go:2886 — the new `--no-beat` flag on `nova-bus wait` and on `nova-wake serve` (cmd/nova-wake/serve.go:85) has no docs/CLI.md entry: the wait section documents its flags in prose (it names --quiet-beats and --max-commits) and the serve section bullet-lists every flag, and neither names --no-beat — the command reference a person reads first cannot find the very flag that is this PR's fix, so a serve operator beside a line has no documented path to it — add a sentence to the `wait` section and a bullet to the `serve` flag list in docs/CLI.md.

QUESTIONS

1. `serve --no-beat` is opt-in and the serve test (cmd/nova-wake/issue1517_test.go:66) deliberately pins that the default argv carries no `--no-beat`. Since the reported bug is precisely that a serve beside a line collides on from-<name>/BEAT, is keeping the old beating default on purpose (so existing serves byte-for-byte unchanged), or should a serve stop beating by default?

2. `wait --no-beat --advance` is allowed but untested, and its behavior in the exact #1517 scenario looks sharp: when a line's own harness shares the checkout and holds a dirty from-<lane>/BEAT, advanceCursorTo's checkoutReady (main.go:2290) now refuses because the BEAT is no longer in the allow list — a refusal where the old code would have folded the harness's BEAT into the cursor commit. Is that refusal the intended outcome for `--no-beat --advance`, or should the combination be refused outright?

3. The new bus test runs two real ~300ms waits with no `testing.Short()` skip, while the package's other wall-clock wait tests (e.g. TestWaitTimesOutQuietlyAndCountsItsPolls) skip under Short. Is ~0.6s of real wait plus its git overhead inside cmd/nova-bus's per-package budget on the CI benches?

Left owed — I read the full diff plus the changed regions of cmd/nova-bus/main.go (cmdWait, waitLoop, waitPoll, advanceCursorTo/advanceCursor) and cmd/nova-wake/serve.go (cmdServe, busArgs) in full at the PR head, and the bus helpers StagePaths/EnsureClean and the test helpers the new tests call. I did not read the full 3859-line main.go, 711-line serve.go, or internal/bus/cursor.go beyond the referenced functions; nothing in the diff touches code outside the wait/advance/serve paths, so nothing there can bear on a claim.

git status --short:
git rev-parse HEAD: 040ba8f81934c9137561ea72738b7659597216ef===FILE=== card-tools22-pre-1869-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1869-r1	1	2026-09-20T19:06:07Z	2026-09-20T19:14:21Z	0	opencode	deepseek-v4-flash	98668	38943	0	5439488	0	0.1770
