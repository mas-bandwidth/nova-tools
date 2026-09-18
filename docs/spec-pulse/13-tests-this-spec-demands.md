## Tests this spec demands

Acceptance replays, one per rule that can be made red, each proven able to fail by a mutation
first. Every `gh`, `git` and `nova-swarm` is a fixture on `PATH` that records its argv;
tripwires: outside the docs, no `api.github.com`, no `os.UserHomeDir`, no `/tmp`, no
`exec.Command("sh"`, `"-c"`, and no `nova-merge` except the manager tier's `nova-merge add`
handoff (rule **The manager tier**).

1. `pool-reads-issue-label`: a fixture `gh issue list` answering three open issues, two
   labelled `card` and one dogfood-shaped without the label, yields `candidates=3 issues=3`
   and three `pool.tsv` rows in issue order; a fourth issue with neither is nowhere.
2. `pool-reads-audit-lines`: one audit issue whose body carries two `MISSING:` lines and one
   `DRIFT` line yields `audits=3` with ids `<issue>#1..3`, template `drift`.
3. `pool-reads-bus-slices-and-roadmap-cells`: a bus checkout with one note carrying a
   `slices:` block of four, and a roadmap file with two cells naming `:card fix` and one
   naming none, yield `slices=4 roadmap=2`; a body with a `slices:` block under the `issues`
   source is `plan=1` and not pooled.
4. `pool-refuses-unreadable-source`: a `gh` exiting 1, a `bus` path that is not a checkout,
   and a roadmap file that does not parse are each one `POOL REFUSED` naming the source and a
   remedy, exit 2, and no `pool.tsv` exists afterwards — the mutation that matters: a run
   that wrote a shorter pool and said `OK`.
5. `pool-skips-seen`: a `seen.tsv` with one candidate `carded` and one `retry` yields the
   `retry` one in `pool.tsv` and the `carded` one in `seen=1`, not in the pool.
6. `cut-line1-is-contract`: every card written has line 1 `RESULT <label> sha=<sha12>`, the
   hash equal to SHA-256 of the bytes below line 1, and `STEP 1` carrying `mkdir -p scratch`,
   an `https://` clone and `checkout -b`; a template whose rendered line 1 is prose, or
   whose `STEP 1` clones `git@`, is `CUT REFUSED` naming the rule, no card written.
7. `cut-text-template-forbids-build`: `read`, `text` and `tone` cards each carry the no-build
   line; a `text.md` fixture lacking it is `CUT REFUSED template=text`; `fix`, `replay` and
   `drift` cards carry the red-then-green row rule; no template mentions `../scratch`.
8. `cut-routes-by-capability-and-cost`: six candidates, one per kind, against a cost table
   whose zero-cost local covers `read|text`, whose flat route covers `code` and whose metered
   route covers `replay`, yield `cards.tsv` rows naming those models — the cheapest capable
   one per kind, never a route chosen by kind alone — and `zero=3 flat=2 metered=1` on
   `CUT OK`; a candidate with template `probe` is `skipped=1`, one `CUT SKIPPED` line,
   a `skipped.tsv` row, and exit 1.
9. `launch-fills-every-free-slot`: eight admitted cards and four free slots admit exactly
   four — every free slot filled, none left idle, exit 0 and no `UNDER-SLOTS` anywhere in
   either stream; the mutation that matters: a launch that refuses instead of filling.
10. `launch-queues-remainder`: the same run queues the rest with no flag asked for:
    `nova-swarm batch` runs once, in the card form, with a `--cards` TSV holding exactly the
    first four cards in `cards.tsv` order, `queued=4`,
    `queue.tsv` holding the other four, `pulses/<id>.tsv` naming the batch.
11. `launch-slot-free-from-lock-files`: a slot whose lock file is `state=free` is free, one
    `state=busy` is not, a missing lock file is free; `free-before` counts the free ones and
    no `native.log` age or 120 s rule appears anywhere — the mutation that matters: a slot
    freed by a log age instead of the swarm's lock file.
12. `launch-every-card-through-batch`: any admitted cards run exactly one `nova-swarm
     batch` invocation and zero `nova-swarm add`, in the card form — `--id <pulse>`, a
     `--cards` TSV holding exactly the admitted cards in order, `--deadline`, a `--runner`,
     `--root`, and a `--then` whose argv begins `nova-pulse harvest --id <id>`; a `BATCH
     REFUSED` fixture reply is `PULSE REFUSED` with that reason, `queue.tsv` unchanged.
13. `harvest-pushes-only-on-line1-match`: three done cards — line 1 equal, line 1 differing
    by one byte, line 1 equal with `BRANCH main` — push exactly one, by `git push <https>
    <branch>:<branch>`, open exactly one draft PR whose body is the `RESULT.md` lines, and
    print `pushed=1 prs=1 mismatch=2`, exit 1; a bare `git push` in the fixture's argv log is
    the mutation that matters.
14. `harvest-abstain-goes-to-retry`: one `ABSTAIN reason=idle=300` row and one card with no
    `RESULT.md`, each on its first attempt, yield `abstain=2` and two requeues under new
    numbers on the other bench; the same two abstaining again yield `retry=2`, two
    `retry.tsv` rows each carrying the card path and the harness log's last
    `permission`/`refused` line, no push, no PR, no third batch argv, `seen.tsv` rows `retry`.
15. `harvest-cuts-read-card-per-pr`: every PR opened yields one row in `next.tsv` with
    template `read` (or `tone` for a seed page), carrying the bench cost table's cheapest
    model that can hold it, and the next `pool` counts it in `next=<n>` after `queue.tsv`.
16. `harvest-relaunches-queue-first`: `queue.tsv` with three rows, `next.tsv` with one, a
    source with two new items, five free slots: the next batch's `--cards` TSV holds the
    three queued cards first, then the read card, then one source card; `queued=1`; the
    `HARVEST OK` line precedes the `PULSE OK` line and both are printed.
17. `harvest-usd-is-the-batch-line`: `usd=` on `HARVEST OK` equals the swarm packet's
    `BATCH … usd=<sum>` byte for byte, `-` when the packet says `-`; no verb's argv log
    shows a model id outside `nova-swarm batch --model`.
18. `width-under-width-exits-2`: `pool.tsv` with two rows, two free slots, nothing in
    flight: `PULSE UNDER-WIDTH pool=2 free=2: launch`, exit 2, and the fixture `nova-swarm`
    records zero runs — `width` launches nothing.
19. `width-pool-empty-says-so`: an empty pool and an empty queue with three cards in flight:
    `PULSE POOL EMPTY in-flight=3`, exit 0; `harvest` at the same state prints it as its last
    line and runs no `batch`; the word `padding` and any invented candidate is nowhere.
20. `then-gated-on-verdict`: a done card whose PR the fixture `gh` reports as not mergeable
    and whose hosted check is red is still pushed, still `prs=1`, and `nova-merge` appears in
    no argv log; a card with line 2 `BLOCKED head=…` is `mismatch` on the count and not pushed.
21. `output-bounded-at-the-largest-plausible-state`: 200 done cards and 60 abstains print at
    most `2 * --max + 6` lines over both streams with a `MORE` line per capped kind; every
    value is one token; `--max 0` prints all; `--max -1` is exit 2.
22. `every-refusal-names-its-remedy`: every refusal in the package lives in one table the
    test walks; each ends in a parenthesised remedy, and removing one turns the test red.
23. `pool-reads-work-nodes`: a `work` source with one open, unleased, unblocked bug node and
    one open, unleased, unblocked item node yields `work=2`, two `pool.tsv` rows whose `id` is
    the node id, and `candidates=2`; a node that is leased or blocked is nowhere; the id is
    carried on the card's line 1 so `harvest` records the attempt on the node.
24. `cut-picks-cheapest-capable-route`: a benches table with a zero-cost local, a flat Go and
    a metered Zen, and one card per capability class, yields `route=<model> reason=<class>`
    on `CUT ROUTE` for the cheapest capable model — the mutation that matters: a pick that
    ignores cost and takes a route by kind.
25. `retry-moves-one-class-up`: a card rewritten from `retry.tsv` after an abstain routes one
    capability class above the first attempt's, so `CUT ROUTE` names a stronger class and a
    `reason` that reflects it.
26. `handoff-writes-record-and-note`: `handoff --to <name>` ends the shift — `SHIFT END` on
    stdout, the loop stopped — writes `<root>/queue/OWNER` freed and `<root>/queue/HANDOFF`
    with the last width, in-flight by bench, pending, escalations, benches and state, and the
    fixture bus records one note to the successor carrying the record; `HANDOFF OK
    to=<name> inflight=<n> pending=<n> escalations=<n>`.
27. `handoff-refuses-asleep-successor`: the fixture `nova-wake awake <name>` exits non-zero —
    `HANDOFF REFUSED` naming the remedy, exit 2, no record, no note, `OWNER` unchanged.
28. `handoff-finishes-harvest-first`: a harvest in progress is finished before the handoff
    proceeds; a fixture mid-harvest is refused with the remedy and the harvest's line is
    still printed.
29. `takeover-refuses-live-owner`: `OWNER` names a live process on a reachable host —
    `TAKEOVER REFUSED owner=<name> pid=<n> host=<h>`, exit 2, nothing taken.
30. `takeover-takes-stale-lock-with-note`: `OWNER` names a dead process or an unreachable
    host — the lock is taken, one `NOTE` line says so, and the loop and a manager shift start on
    the same queue.
31. `takeover-inherits-queue`: the taken `HANDOFF` record's inflight, pending and
    escalations are inherited and printed as `TAKEOVER OK from=<name>
    inherited=<inflight/pending/escalations>`.
32. `status-is-bounded`: at the largest plausible state — every bench and every friend in
    scope — `status` prints at most `--max` lines over both streams with one `MORE` line per
    capped kind; every value is one `internal/oneline` token.
33. `status-contraction-from-counts`: the `CONTRACTION` hour and day lines equal the counted
    cards `cut/done`, prs `opened/merged` and issues `filed/closed` from the queue,
    `usage.tsv` and `gh`; a line built from a list or a report body is the mutation that
    matters.
34. `status-adoption-includes-coordinator`: the `ADOPTION` lines name every friend, the
    coordinator included, one line each with `version`, `receipt` and `edges`; a missing
    coordinator line is red.
35. `status-remaining-counts-in-scope-only`: `REMAINING` counts queue rows, unread PRs, dirty
    PRs and uncarded issues in scope only — a bench or friend outside `--roots <dirs>` is
    nowhere on the line.
36. `help-is-the-verbs-block-byte-for-byte`: `nova-pulse help` prints this file's **The
    verbs** block byte for byte — the test reads the fenced block out of
    `docs/SPEC-PULSE.md` and compares, so a flag added here and not there is red, and so is
    a flag added there and not here; the mutation that matters: a verb line edited on one
    side only.
37. `status-expanding-after-two-hours-above-one`: a queue whose `cut/done` is above 1 for two
    consecutive hours prints `verdict=EXPANDING`; one hour, or a ratio at 1, is `CONVERGING`;
    the verdict is computed from the counts and not from any word in a note.
38. `progress-counts-only-the-day`: `usage.tsv` rows from two days under `--roots` yield
    `cards=` equal to the `--day` rows alone; `rc0` counts the rows with `rc=0`.
39. `progress-parallelism-is-busy-over-span`: four cards of 600 s each inside one 1200 s span
    print `effective_parallelism=2.0`; the slot count is nowhere in the arithmetic.
40. `estimate-uses-p90-and-parallelism`: `hours` equals `remaining x p90 / parallelism x 1.5`
    to one decimal, and `remaining_cards` counts pending, launched, unread PRs and twice the
    in-scope open issues — an out-of-scope issue moves nothing.
41. `tick-never-delays-a-card`: a card cut by a requeue inside a cycle appears in that cycle's
    `nova-swarm batch` argv, not the next cycle's.
42. `pool-refills-to-floor`: a queue at `floor - 3` with five candidates in the sources gains
    exactly three rows in one cycle, deduplicated on PR number, issue number and contract line.
43. `gated-card-launches-on-merge`: a card carrying `AFTER: PR7 merged` is `gated=1` while the
    fixture `gh` reports PR 7 open and is in the batch argv of the first cycle after the
    fixture reports it merged, with no other input. Both halves:
    `TestGatedCardLaunchesOnMerge` (the placement road, `internal/pulse/cardgate_spec_test.go`)
    and `TestManagerReleasesTheGateWhenItSeesTheMerge` (the release,
    `internal/pulse/manager_gate_test.go`).
44. `contraction-phase-cards-bugs-only`: with `verdict=EXPANDING`, a candidate whose issue
    carries the label `next-push` is `skipped` on the `CUT` line and never cut; a fix candidate
    with a `red:` line is cut.
45. `admit-refuses-over-spend`: a shift whose `--spend-max` a `metered` route card's class
    would cross yields `ADMIT REFUSED card=<label> gate=spend <usd> (<remedy>)`, the card
    queued with `spend` on its `queue.tsv` row, the run still exit 0; `zero` and `flat` route
    cards count zero.
46. `admit-refuses-third-attempt`: a contract line with two prior attempts across the queue is
    refused at the default `--max-attempts 2` — `ADMIT REFUSED card=<label> gate=attempts 2`;
    a first attempt is admitted.
47. `admit-refuses-out-of-scope`: a card whose source is not in `--scope` is
    `ADMIT REFUSED card=<label> gate=scope <source>`; with no `--scope` the pool sources are
    the scope and all are admitted.
48. `under-width-counts-admitted-only`: a pool of one admitted and one gate-refused card with
    free slots is `PULSE UNDER-WIDTH pool=1 free=<f>: launch`, exit 2 — the refused card is
    not counted.
49. `cut-step-one-sets-no-tmpdir`: a template whose `STEP 1` only mkdirs, clones over
    `https://` and checks out is cut with no `TMPDIR` on the line, and one whose `STEP 1`
    sets a `TMPDIR` is `CUT REFUSED` naming the rule, no card written — the runner exports
    `TMPDIR` outside every repo (#460), and a card that set its own put its temp dir inside
    the job's repo, the red cards 247, 266 and 353 reported and did not cause.
50. `pool-reads-open-non-draft-prs`: a `prs` source with two open non-draft PRs and one open
    draft yields `prs=2` on the `POOL` line and two `pool.tsv` rows of kind `read`, template
    `read`, one candidate per PR — the draft is nowhere, and the read candidate is the same
    shape harvest's own read card has (rule 13).
51. `second-writer-refuses-naming-the-holder`: with one writer holding `<queue>/.lock`, a
    second `fill`, `run`, `manager` or `loop` on that queue exits 2 and its line carries the
    holder's `pid=`, `verb=` and `since=`; a lock whose holder is not running is taken over
    once, with no wait. `TestSecondWriterRefusesNamingTheHolder`,
    `TestSecondFillOnALockedQueueExitsTwo` and `TestStaleLockIsTakenOver`
    (`internal/pulse/queuelock_test.go`).
52. `loop-tick-is-run-fill-manager-in-order`: one `nova-pulse loop --once` calls `run`, `fill`
    and `manager` once each in that order, under one lock, and answers exactly one
    `LOOP TICK n=<i> ran=… filled=… harvested=… held=… refused=… dead=…` line on the console
    with each verb's own line in `<queue>/pulse.log`. `TestLoopTickIsRunFillManagerInOrder`
    (`internal/pulse/loop_test.go`).
53. `launch-dead-releases-the-lane`: a card under `launched` whose marker is older than the
    launch grace and whose job directory never appeared is moved back to `pending` with its
    marker removed, so its lane is free; a card whose job directory is there, and one still
    inside its grace, are untouched. `TestLoopLaunchDeadRequeuesAndReleasesTheLane`
    (`internal/pulse/loop_test.go`).
54. `loop-stops-dependent-steps-and-exits-non-zero`: a tick whose run step exits non-zero runs
    neither fill, manager nor the launch-dead probe, counts `failed=1` on its line, names the
    skip in `pulse.log`, and the loop exits non-zero; a failed fill is counted and the manager
    still runs. `TestLoopStopsDependentStepsAndExitsNonZero`,
    `TestLoopCountsAFailedFillAndStillRunsTheManager` (`internal/pulse/loop_test.go`).
55. `dry-run-touches-nothing-through-the-cli`: `fill --dry-run` and `manager --dry-run`, run
    through the real command line against an isolated queue, leave every file in it
    byte-for-byte identical and never reach the launcher; `loop --dry-run` is refused, exit 2,
    naming the two verbs that do have a read-only mode; and the same snapshot helper DOES
    catch the same fill without the flag, so the guarantee is not vacuous.
    `TestFillDryRunTouchesNothingThroughTheCLI`,
    `TestManagerDryRunTouchesNothingThroughTheCLI`, `TestLoopRefusesDryRunThroughTheCLI`,
    `TestTheSnapshotCatchesARealRun` (`cmd/nova-pulse/dryrun_test.go`).
56. `lock-is-atomic-and-identity-checked`: an empty lock file is never handed out half-written,
    an old owner's release never deletes its replacement's lock, two writers never recover one
    stale lock at once, and a lock is never entered on a pid match alone.
    `TestPausedPublisherIsNeverRobbed`, `TestOldOwnerReleaseDoesNotDeleteItsReplacement`,
    `TestCompetingTakeoverIsSerialized`, `TestReentrancyIsByNonceNotByPid`
    (`internal/pulse/queuelock_test.go`).
