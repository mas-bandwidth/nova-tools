RESULT tools22-pre-868-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#868 at head aca5763705c4: nova-pulse rules, routes, brief: POLICY prose becomes rows, config and a rendered page (#828 cl
PREREAD 868 claims=27 proven=25 unproven=2 defects=7 high=0

PR 868
HEAD aca5763705c4b4aa5eb188fca79f9d4629875726
BASE dev
MERGE-BASE ce1144159cb53e8b6efe6300e9576bce6fe45cb9
BEHIND 364
FILES 37 production, 29 test
LINES +12569 -16

CLAIMS

1. `nova-pulse rules` prints the rule table as one line (`RULES file=... rows=<n> kinds=...`).
   PROVEN-BY internal/pulse/rules_test.go:121 TestRulesWithNoTableIsOneLine — a queue with no table prints exactly one line carrying rows=0.
2. `rules --check` refuses a row no verb can execute — an unknown kind, an empty condition or verdict, a missing source — naming each bad row and counting them, exit 2.
   PROVEN-BY internal/pulse/rules_test.go:98 TestRulesCheckRefusesABadRow — asserts "row 2: kind weather", "row 3: no condition" and "RULES REFUSED: 2 of 3 rows".
3. `rules --seed-from <POLICY.md>` turns every dated line of the prose into a row, kind guessed from its words, source the line's own date, and a line that records rather than rules becomes kind=record, while an undated line is not a row.
   PROVEN-BY internal/pulse/rules_test.go:39 TestRulesSeedFromPolicyGuessesTheKinds — six dated lines give six rows with kinds stop/revert/requeue/route/slots/record, a source on every row, and the undated line excluded.
4. Seeding today's live POLICY.md yields exactly 36 rows in the distribution stop 8, read 6, rule 4, slots 3, hold 2, requeue 2, admission 1, record 10.
   UNPROVEN — the figure depends on the bench's live POLICY.md, which is not in the repo; nothing in the diff or the tree witnesses the count.
5. `routes` prints the live rotation per class — the class list minus what a probe benched — and the route the next card of that class takes, in one line.
   PROVEN-BY internal/pulse/routes_test.go:45 TestRoutesBenchUnbenchRoundTrip — after benching kimi the live code list is flash alone and next.text=flash appears on the ROUTES line.
6. `routes --bench` and `--unbench` write pulse.toml in place keeping comments and every other key, and each refuses a second bench/unbench and benching a class's last live route.
   PROVEN-BY internal/pulse/routes_test.go:45 TestRoutesBenchUnbenchRoundTrip — the comment and `studio = 4` survive, and "no live route", "is not benched" and the second-bench refusals all exit 2.
7. `brief` renders the one-pulse brief from the configuration and the rule table — the tool's own commands, only the decidable rows (kinds stop, hold, admission), the configured width and routes — with no line over 200 bytes and the log read by `tail -n`, never by a clock.
   PROVEN-BY internal/pulse/brief_test.go:43 TestBriefCarriesEveryDecidableRuleRow — every decidable row is on the page, the record row is not, tail -n is present and every line is within briefLineMax.
8. `brief --friend` renders the friend shape — wake, the one note, the one thing, the reply — and no line over the bound.
   PROVEN-BY internal/pulse/brief_test.go:85 TestBriefFriendShape — asserts wake/--as, packet --pr, the PULSE reply line and the line ceiling.
9. `cut --kind read` requires `--base` and writes the diff against the PR's own base branch (`git fetch origin <base> && git diff origin/<base>...<head>`), never main by default.
   PROVEN-BY internal/pulse/cutkind_test.go:149 TestCutKindReadDiffsAgainstThePRBase — the card carries BASE: dev and `git diff origin/dev...5f544272a1b0`, and a cut with no base is refused.
10. `cut` rewrites a card's MODEL: line to the queue's configured `routes.rewrite_model_to` and says so on its CUT line.
    PROVEN-BY internal/pulse/cutkind_test.go:186 TestCutKindRewritesTheModelLine — the rewritten card carries MODEL: flash, none of kimi, rewrote=flash on the line, and the stamp still matches.
11. RULES.tsv is written by one writer and read by one reader, and the older 3-, 4- and 5-column shapes still read.
    UNPROVEN — ParseRuleRow (internal/pulse/rules.go) accepts the shapes, but no test in this diff feeds a 3- or 4-column row through LoadRules; every fixture writes the six-column shape.
12. `cut --kind` is the only numberer: the card number comes only from the state file's next_card under the queue's lock, and `--number` is refused naming why.
    PROVEN-BY internal/pulse/number_test.go:29 TestFiftyConcurrentCutsNeverShareANumber — fifty concurrent cuts take 1..50 exactly once with no duplicate.
13. `gate` writes STOP whose line 1 names the red's sha, run, job and test and whose line 2 is the admission name (the issue the failing job's log names, else the failing test), changes nothing on a cancelled or in-progress run, and a green lifts only the STOP the gate wrote.
    PROVEN-BY internal/pulse/gate_test.go:52 TestGateWritesStopWithTheIssueAsAdmissionName; internal/pulse/gate_test.go:108 TestGateHoldsOnCancelledAndInProgress; internal/pulse/gate_test.go:156 TestGateGreenClearsOnlyTheGatesOwnStop.
14. While a STOP stands, launch admits only the card whose line 1 names the red, and an admission refusal is not a failed run.
    PROVEN-BY internal/pulse/admission_test.go:77 TestLaunchRefusesEveryCardTheStopDoesNotAdmit — exit 0, five ADMIT REFUSED lines and one MORE, nothing reached nova-swarm.
15. launch refuses a card carrying no valid cut stamp, and a card edited after it was cut; `--unstamped-ok` admits unsigned cards only and says so on its own line.
    PROVEN-BY internal/pulse/stamp_test.go:15 TestLaunchRefusesAnUnstampedCard — unstamped refused, stamped admitted, edited-after refused, unstamped-ok admits unsigned only.
16. `cut` refuses a card whose steps say "whole" or "read <file> entirely", naming `--spec-lines` or `nova-review packet --rule spec:n`, and admits a card naming a line range.
    PROVEN-BY internal/pulse/stamp_test.go:91 TestCutRefusesWhole — the "whole" and "entirely" cards exit 2 with the range/rule remedy, the L1-L2 card exits 0.
17. A failed card is disposed by its cause: the harness log's signature picks exactly one action (fence→recut, a bench signature→probe, orphan→recut, nosha→recut, unknown→triage), and a second failure of the same cause is failed with a triage packet, never re-cut.
    PROVEN-BY internal/pulse/cause_test.go:17 TestCauseMapsEachSignatureToOneAction; internal/pulse/cause_test.go:111 TestSecondFailureOfTheSameKindFails; internal/pulse/cause_test.go:261 TestReapFailsTheSecondFailureOfACause.
18. The same card text is never launched twice: a re-cut takes a new number, carries the cause line, and writes the superseded body's sha8 into RECUT.tsv, which launch refuses by name.
    PROVEN-BY internal/pulse/cause_test.go:131 TestRecutTakesANewNumberAndCarriesTheCause — card-900 written, Prior attempt line above the steps, CardGate refuses the old text naming card-900.
19. An approval is a ledger row, not a moment: the sweep enqueues an APPROVE row when its checks are green — never while QUEUED, never twice — and marks a moved head stale and a merged or closed PR closed.
    PROVEN-BY internal/pulse/ledger_test.go:76 TestSweepEnqueuesWhenChecksTurnGreenAndNeverTwice; internal/pulse/ledger_test.go:124 TestSweepMarksStaleHeadAndClosesMergedRows.
20. The sweep writes the day's MERGED record that `status --oneline` counts, for a merged PR and never a closed one, and a second sweep does not double-count it.
    PROVEN-BY internal/pulse/merged_test.go:16 TestSweepWritesTheMergedRecordStatusReads — one MERGED line for the merged PR, none for the closed one, countDay returns 1.
21. `run` wires the tick's six seams to the shipped verbs — one PULSE WIDTH line per tick, each verb's own line in <queue>/pulse.log — and calls a person once per undecided (case, ref), never once per tick.
    PROVEN-BY internal/pulse/wire_test.go:71 TestWiredOnceTickRunsEverySeam — WIDTH + RUN OK on stdout, GATE GREEN/SWEEP/REAP/REFILL/PULSE OK in the log, no seam= line; internal/pulse/run_test.go:161 TestRunSimulatedDayCostsUnderTwentyNotes — a 60-tick day costs exactly 7 notes.
22. `run` re-reads pulse.toml every tick, names a changed value on one CONFIG line, and carries its counters across a restart in pulse.state.
    PROVEN-BY internal/pulse/wire_test.go:175 TestWiredRunRereadsTheConfigAndSaysWhatChanged — the second tick prints CONFIG OK keys=slots.studio and runs as tick=2.
23. A red branch stops the tick's work, writes one STOP and one note per red, and green clears the loop's own STOP.
    PROVEN-BY internal/pulse/run_test.go:217 TestRunEveryRedIsOneStopAndOneNote — two MAIN-RED and two RESUMED lines, two red notes, tick 5 stop=yes harvested=0, tick 6 stop=no.
24. `status --oneline` prints the whole day in one line under 400 bytes — pool, cards done/failed, the day's reds and merges, spend, per-bench width, the pit-stop note — and benches that do not fit are counted, never dropped.
    PROVEN-BY internal/pulse/statusline_test.go:92 TestStatusLineIsOneLineUnderFourHundredBytes; internal/pulse/statusline_test.go:139 TestStatusLineHoldsTheCeilingAtEveryBench — benches_more= counts the overflow.
25. `nova-tokens session` sums one Claude Code session jsonl per turn, deduplicated on the message id so a streamed message counts once, prints one SESSION line with the weighted fresh-input equivalent, and folds the coordinator into the day file as claude-fable-5-1/coordinator, keeping every other row.
    PROVEN-BY internal/tokens/claude_session_test.go:67 TestReadClaudeSessionMatchesTheHandCount; cmd/nova-tokens/session_test.go:41 TestSessionFoldsIntoTheDayFileAndKeepsTheOtherRows — the emma row survives and a second fold writes no second coordinator row.
26. Every tool files its own edges: a refusal appends one row to <queue>/EDGES.tsv deduplicated by (tool, verb, line), `nova-check edges` opens one issue per distinct open row and marks each filed so a second run is silent, and recording never creates the queue directory.
    PROVEN-BY internal/edges/edges_test.go:44 TestRecordIsDeduplicatedByLine; internal/edges/edges_test.go:97 TestReportOpensOneIssuePerDistinctRow; internal/edges/edges_test.go:220 TestRecordNeverCreatesTheQueueDirectory; cmd/nova-check/edges_test.go:21 TestEdgesVerbFilesOneIssuePerDistinctRow.
27. `reap` restarts a self-hosted runner busy with no in-progress run for more than five minutes (rule E2) — never a first sight, never a runner with a run in progress — and `--dry-run` prints the same count and restarts nothing.
    PROVEN-BY internal/pulse/runners_test.go:66 TestReapRestartsTheStuckRunnerAndOnlyThatOne; internal/pulse/runners_test.go:110 TestReapNeverRestartsWhileRunsAreInProgress; internal/pulse/runners_test.go:142 TestReapDryRunCountsTheStuckRunnerAndRestartsNothing.

DEFECTS

DEFECT medium internal/pulse/wire.go:530 — the wired Launch moves pending cards into launched/ before Launch applies STOP admission, and only rolls them back when Launch returns a non-zero code — while an all-refused admission returns 0, so every non-red card moved during a red is stranded in launched/ with no job behind it — the reaper later disposes those cards by an unknown cause (failed + triage packet), losing work that merely sat in the queue while the bench was red, and the WIDTH line counts them launched — check the STOP (or admission) before moving cards, or move the refused ones back to pending on an admitted==0 result.
DEFECT medium internal/pulse/reap.go:319 — cutTriagePacket writes the evidence to one <queue>/UNDECIDED/<kind>.txt per kind, and Triage's default reads the same per-kind file, so two cards disposed with the same cause in one tick overwrite each other's evidence and the second packet's card is cut from the wrong card's log — a triage decision can be made on the wrong card's evidence — key the evidence file by card name (or append), not by kind alone.
DEFECT medium internal/pulse/rules.go:212 — `rules --seed-from` writes the seeded rows over RULES.tsv whole, silently dropping any rows already in the table, including the pending rows `triage` writes from NEW verdicts — re-seeding a live queue loses policy someone already added — merge the seeded rows into the existing table, or refuse when the table is not empty.
DEFECT medium internal/pulse/run.go:434 — note() records a (case, ref) in NOTED and marks it told before the bus/file send runs, so a send that fails is never retried and the loop believes a person was told when they were not — an escalation that failed to send is lost for good across restarts — append NOTED and set noted only after a successful Note.
DEFECT medium docs/CLI.md — the PR ships eight new verbs/flags — nova-pulse run, gate, sweep, reap, triage, cut --kind, status --oneline and nova-tokens session — and CLI.md documents none of them; only the new rules/routes/brief section and the nova-check edges line were added — a reader of the one doc that is supposed to list every verb cannot find most of this PR's surface — add the missing entries in the same shape as the rules/routes/brief block.
DEFECT low internal/edges/edges.go:230 — Report on a nonexistent --queue directory reads as "no ledger" and returns success with rows=0, where SPEC.md's edges section says the verb says NO (exit 2) when the ledger cannot be read — a typo'd queue path looks like a clean bench — stat the queue directory and refuse when it is not a directory.
DEFECT low internal/pulse/reap.go:377 — reapProcesses matches a process's argv with strings.Contains(args, root), a substring test on the user-supplied --roots value, so a root that is a path prefix of another directory would make the reaper kill processes outside its own subtree — a wrong kill is worse than a leak — compare on a path-boundary (the root followed by a separator or end).

QUESTIONS

1. `rules --seed-from` replaces RULES.tsv whole (rules.go:212). Is re-seeding meant to be a one-time migration only, or should it merge with rows the triage has since written as pending? The commit message calls it "the migration" but nothing stops a second run.
2. The 36-row claim for seeding today's POLICY.md (stop 8, read 6, ...) — is that a live-bench figure somebody expects to keep in sync, and should there be a fixture pinned to it, or is it deliberately unverifiable here?
3. The wired launcher moves cards into launched/ before the STOP admission runs (wire.go:530). Was the launcher designed to let refused cards sit in launched for the reaper, or was the admission check supposed to run before the move?
4. Why do rules/routes/brief and nova-check edges get CLI.md entries while run/gate/sweep/reap/triage/cut --kind and nova-tokens session do not — is CLI.md no longer the verb list for nova-pulse?
5. The reaper's evidence file is keyed by triage case, not by card (reap.go:319) — is one evidence file per kind per tick a deliberate ceiling (the packet's RULES rows are the decision), or an accident of the kind being the only name the packet carries?

Left owed — the diff (13,176 lines) was read in full, production and test files alike. I did not read in full the pre-existing files the new code calls but that this PR does not change beyond a hunk: internal/pulse/harvest.go, internal/pulse/cut.go, internal/pulse/status.go, the internal/oneline and internal/bounded packages, and the existing launch.go context beyond the diff. No test run was performed (none required); `go build ./...` and `go vet` on the touched packages pass. The working tree was left at origin/main; the reading above is of refs/tmp/pr868.

git status --short:
(empty)
git rev-parse HEAD:
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-868-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-868-r1	1	2026-09-20T19:28:42Z	2026-09-20T19:44:27Z	0	opencode	deepseek-v4-flash	265727	46602	0	9498880	0	0.3162
