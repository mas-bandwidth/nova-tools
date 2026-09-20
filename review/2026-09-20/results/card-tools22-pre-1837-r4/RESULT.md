RESULT tools22-pre-1837-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1837 at head ea66ec34f874: bus wait, wake serve: --no-beat, so a waker beside a line does not beat for it (#1517)
PREREAD 1837 claims=8 proven=6 unproven=2 defects=1 high=0

PR 1837
HEAD ea66ec34f874bd6abd15add8c1696a6e4df49ea7
BASE rowan/tools12-c7-1141-bus-wait-decide
MERGE-BASE 60039cfd37f0ce7843fad592102801e740912138
BEHIND 0
FILES 2 production, 2 test
LINES +207 -16

CLAIMS
1. `nova-bus wait --no-beat` polls to WAIT TIMEOUT but writes no BEAT file and commits no BEAT.
   PROVEN-BY cmd/nova-bus/issue1517_test.go:44-50 TestIssue1517WaitWithNoBeatWritesNoBeat — a wait with the flag runs to timeout, then from-ada/BEAT is asserted absent on disk and `git log -- from-ada/BEAT` empty.
2. `nova-bus wait` without `--no-beat` still writes and commits its BEAT, so presence is unchanged for a normal wait.
   PROVEN-BY cmd/nova-bus/issue1517_test.go:54-60 TestIssue1517WaitWithNoBeatWritesNoBeat (the control) — the same poll without the flag leaves BEAT on disk and a BEAT commit in the log.
3. `wait --no-beat` is byte-for-byte the same call otherwise: same exit code and the same `cursor=` on the WAIT TIMEOUT line.
   PROVEN-BY cmd/nova-bus/issue1517_test.go:64-71 TestIssue1517WaitWithNoBeatWritesNoBeat — compares quiet.code/control.code and the cursor field of both WAIT TIMEOUT lines.
4. `wait --no-beat` refuses to run together with `--beat` or `--beat-lease`.
   UNPROVEN — the refusal is real (cmd/nova-bus/main.go:2884-2886) but no test in this diff or the tree invokes `wait --no-beat --beat <d>` to assert it; every test here uses only the default (unset) beat flags.
5. Under `wait --no-beat --advance` the cursor commit does not name the BEAT path.
   UNPROVEN — cmd/nova-bus/main.go:2244-2246 excludes BeatPath from the advance commit, but no test in this diff runs `wait --no-beat --advance` (waitFlags carries no `--advance`, and the wake-side test uses a fake nova-bus that never advances).
6. `nova-wake serve --no-beat` appends `--no-beat` to the argv of every `nova-bus wait` poll it issues.
   PROVEN-BY cmd/nova-wake/issue1517_test.go:60-62 TestIssue1517ServeNoBeatReachesTheBusArgv — every recorded `wait ...` call from a serve started with `--no-beat` carries the token.
7. A `serve` without `--no-beat` carries no `--no-beat` in its polls, so the argv is exactly what it was before the flag existed.
   PROVEN-BY cmd/nova-wake/issue1517_test.go:67-70 TestIssue1517ServeNoBeatReachesTheBusArgv (the control) — no recorded wait call from the default serve carries the token.
8. A waker beside a line does not beat for it, so the two writers never collide on from-<name>/BEAT.
   PROVEN-BY the pair cmd/nova-bus/issue1517_test.go:44-50 + cmd/nova-wake/issue1517_test.go:60-62 — the bus test proves wait --no-beat writes nothing, the wake test proves serve carries the flag, and the composition is witnessed by both; there is no single end-to-end test running a real serve against a real wait while a line's own harness also beats.

DEFECT low cmd/nova-bus/main.go:2843 and cmd/nova-wake/serve.go:85 — the new `--no-beat` flag has no entry in docs/CLI.md (neither the `wait` section nor the `serve` section), and the flag lists in docs/SPEC.md:2715-2720 and docs/SPEC-WAKE.md:315-316 are not updated to name it — a caller reading the door docs cannot learn the flag exists, and the repo's own convention documents every other wait/serve flag there (docs/CLI.md:424 for --quiet-beats, docs/CLI.md:426 for --max-commits) — a reader cannot tell that the read-only poll is the sanctioned fix for the serve-beside-a-line collision — what would fix it: one sentence in each of the wait and serve sections of docs/CLI.md, and the flag added to the two usage blocks.

QUESTIONS
1. The whole point of the change is that a `serve` beside a line must not beat for it — but `--no-beat` is opt-in on serve: a serve started without it still collides forever. Why is the default not no-beat for serve, or why is the flag not required when serve runs beside a line somebody else is driving?
2. The wake-side test proves only that the flag reaches the fake nova-bus's argv; the bus-side test proves only that a real wait honors it. Was there a reason not to run the two together — a real serve against the real wait while a second process beats the same lane — to witness claim 8 end to end?
3. Claim 4's refusal (no-beat + beat/beat-lease) and claim 5 (no-beat + advance excludes the beat from the cursor commit) are the two spots a reviewer might want pinned. Are they deliberately left to existing refusal-test coverage, or is a test for each still owed?
4. The merge-base of this PR and its own rowan base is the rowan tip 60039cfd (the PR sits one commit directly on it) and the head is 0 behind that branch, so the diff is exactly this PR's one commit — but dev@5298f6be (the card's BASE line) is neither an ancestor nor a descendant of 60039cfd. Is the rowan branch the intended landing target, or is dev the real one?

Left owed — everything in the diff was read in full (both new tests, and the changed hunks of cmd/nova-bus/main.go and cmd/nova-wake/serve.go in context at the PR head via `git show`; the working tree in the clone is on main, not the PR head, so files were read from refs/tmp/pr1837). The two new tests were run (go test -run TestIssue1517 in both packages: pass) and go build/vet of both packages pass. Not read: the rest of the two large production files outside the changed regions, and the docs/SPEC*.md beyond the usage blocks quoted above.===FILE=== card-tools22-pre-1837-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1837-r2	1	2026-09-20T19:23:30Z	2026-09-20T19:47:15Z	0	opencode	deepseek-v4-flash	126242	90766	0	11739648	0	0.3718
