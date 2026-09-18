## Status

`nova-pulse status --queue <dir> --roots <dirs> [--day <d>]` is the one-verb answer to the
all-day questions — what runs where, how wide, at max throughput, what landed and who adopted
it, what is in flight and what remains and how long, what new work appeared, are we converging.
It prints, no model, at most 20 lines, eight line kinds, each one line, counts not lists:

- `WIDTH <bench>`, one per bench in scope — `running` jobs, `slots`, `load`, `headroom`.
- `QUEUE` — `pending`, `gated` (waiting on a merge or a hold), `launched`, `done`, `failed`.
- `RATE` — `cards_per_hour`, `p50_s`, `p90_s`, `usd_per_card`, `parallelism`, from `usage.tsv`; with no measured usage in the window every metric reads `-` — a cost or latency never measured is unknown, never zero.
- `REMAINING` — `queue` rows, `unread_prs`, `dirty_prs`, `uncarded_issues`, `hours`, in scope only.
- `CONTRACTION` — cards `cut/done`, prs `opened/merged`, issues `filed/closed`, for the last hour and the day, counted, never from a report body, with one verdict.
- `ADOPTION <friend>`, one per friend, coordinator included — `version`, `receipt`, `edges`, from the ADOPT files and bus receipts.
- `OPEN` — `dogfood`, `holds`, `escalations`.
- `TOOLS` — `merged_since_adoption` and the names, from `gh` cached per tick.

Sources: the queue, `usage.tsv`, the ADOPT files, bus receipts, `gh` (cached per tick). In
nova-work, `check` carries the same lines from the tree and the journal (nodes minted per node
closed, remaining by depth, completion by epic) once the tree is the pool (#500). Prototype:
`bin/status.sh`. The grammar, one line each, counts not lists:

```
STATUS WIDTH <bench> running=<n> slots=<n> load=<n> headroom=<n>
STATUS QUEUE pending=<n> gated=<n> launched=<n> done=<n> failed=<n>
STATUS RATE cards_per_hour=<n|-> p50_s=<n|-> p90_s=<n|-> usd_per_card=<x.xxxx|-> parallelism=<n.n|->
STATUS REMAINING queue=<n> unread_prs=<n> dirty_prs=<n> uncarded_issues=<n> hours=<n>
STATUS CONTRACTION hour cards=<cut/done> prs=<opened/merged> issues=<filed/closed> verdict=<CONVERGING|EXPANDING>
STATUS CONTRACTION day cards=<cut/done> prs=<opened/merged> issues=<filed/closed> verdict=<CONVERGING|EXPANDING>
STATUS ADOPTION <friend> version=<v> receipt=<n> edges=<n>
STATUS OPEN dogfood=<n> holds=<n> escalations=<n>
STATUS TOOLS merged_since_adoption=<n> <names>
STATUS SWARM first_attempt=<x.xx|-> done=<n> abstain=<n> batches=<n> hedge=<none|opus-on-critical-path|->
STATUS FAULT reason=<token> count=<n>
STATUS PIT-STOP reason=<token> count=<n> remedy=fix the machinery before more cards
```

**The swarm's own health is a flag, never a guess** (`--batches <dir>`, `bin/status.sh`'s
last two lines). With `--batches` status folds the newest 60 `batch-*.out` outputs in that
directory, by modification time with the name as the tie-break: `done=` and `abstain=` summed
over their `BATCH` lines give `first_attempt`, and every `<label> slot=<n>: ABSTAIN
reason=<token>` line is counted by reason. `hedge` is `opus-on-critical-path` below 0.90 —
the coordinator's model stays on the critical path until the rate holds above it for a day
(Glenn, 2026-09-15) — and `none` at or above it. One `STATUS FAULT` line per reason prints
loudest first, capped by `--max`, and a reason recurring five times or more in the window is
`STATUS PIT-STOP`: when one small fault recurs, fixing it beats continuing (Glenn,
2026-09-16), because more cards through a broken machine is the most expensive thing the
fleet does. Without `--batches` none of the three lines print — the verb claims nothing about
a swarm it was not shown — and a `--batches` that cannot be read is refused with one remedy
line rather than folded as a swarm with no faults. The `BATCH` line's own
`uniform-abstain=<reason>` field is a label and is never counted.

**The verdict is the health metric** (Glenn, 2026-09-15, #549, #553). Contraction is tracked
every tick, and the `CONTRACTION` line's `verdict` is `CONVERGING`, or `EXPANDING` when any one
stream's ratio — cards `cut/done`, prs `opened/merged`, issues `filed/closed` — has been above
1 for two consecutive hours. `EXPANDING` means the spec work up front was not done properly or
the engineering practice is lax, and the coordinator names which, on the record, before another
implementation card goes out. The same verdict belongs on nova-work `check`, from the journal,
under #500. Replay: `status-expanding-after-two-hours-above-one`.

### The fleet page

`nova-pulse status --html <out> --benches <file> [--queue <dir>] [--ssh <path>]
[--timeout <s|duration>] [--publish <host:dir>] [--self <name>] [--loop <label>=<pattern>]...
[--branch <name>] [--day-start <HH:MMZ>] [--gh-config <dir>]` is the fleet page as a verb
(`bin/status-page.sh`). It reads every bench in `--benches` over `ssh <target> bash -s`, in
parallel and bounded by `--timeout`, counting live cards from running card processes — a
process whose command line names a job directory, or whose cwd is under the slot, the
authoritative shape `bench-hygiene.sh`'s `live_slot` uses — and never from log age, which
called a silently dead card alive and a long card dead. It writes the page to `--html`,
appends one seven-column row to `metrics.tsv` beside it — `<RFC3339> live queue merged
opened launched free-disk-per-bench` — ships both where `--publish` says, and prints one
line:

```
STATUS HTML wrote=<path> live=<n> queue=<n> down=<n> merged=<n|-> published=<host:dir|->
```

**A count nobody took is a dash, never a zero.** This is the page's one rule and it has
been learned twice. A bench that does not answer is `DOWN (no answer over ssh)`, its disk is
a dash in the metrics row, it adds nothing to `live`, and it is counted in `down=`: a row of
zeros reads as a bench with nothing to do, which is how a fleet nobody could see looked
healthy on 2026-09-17. An answer that cannot be parsed is no answer and says DOWN too. With
no `<queue>/REPO` there is nobody to ask the forge, so `merged`, `opened` and the merge
queue read as a dash on the page, in the series and on the STATUS HTML line, and the chart
plots a gap rather than a flat line at zero — a flat line is a claim.

The rule was learned a third time by probing the real fleet: a `--benches` home that is not
a directory on the bench — a typo, a user renamed, a machine reinstalled — let `df` answer
nothing, and every row read `0 GB free, allowed 0` from benches that had answered perfectly.
So the home is checked first, before any of the work it would make pointless, and the row
says DOWN **with its reason** rather than a number. The reasons are prose and render as
prose: a run verdict of `completed cancelled` printed once as `completed\x20cancelled`,
because two words had gone through the one-token escaper.

**The rows, all of them.** The page a fleet reads is the whole page or it is not adopted:
an adoption attempt on 2026-09-17 matched four bench rows byte for byte and was still
correctly refused over twelve gaps.

- the branch tip and its CI run (`--branch`, `dev` by default) — a red tip is the one fact
  that makes every other number beside the point;
- the merge queue by state: running, waiting, unmergeable, from the forge's `mergeQueue`;
- pending and ready cards, what the fill loop launched and what capacity refused;
- hygiene actions in the last hour, summed over the benches from each `~/hygiene.log`,
  counted in the SAME round trip as the liveness numbers — a second ssh per bench per
  minute for one integer is the dumb waste the fleet audits for;
- one row per bench, and one for `--self`: the host running the verb is a bench too, with
  its own columns (CI runners, cores, load, free disk, orphans). The Studio drowned at load
  147 on 2026-09-17 while the page showed four Linux benches idling;
- the loops on the `--self` host, counted by the pattern the CALLER names. No loop name is
  baked into this tool: a verb carrying `harvest-loop.sh` in its source would freeze the
  scripts it exists to retire, and `--loop` without `--self` is refused because there is no
  host to count them on;
- the `metrics.tsv` time series it writes beside itself.

**`--limit` is not optional on a gh list.** gh's own default is thirty, so a fleet that
merges more than that reads as a fleet that merged thirty, every tick, for the rest of the
day. The page said 30 where the script said 273. Every list call here asks for 500.

**The day starts at 02:00Z** (`--day-start`), because `INSTALL-fleet.md`'s `rate_counter`
resets there and the script counted from there; before the boundary the window is still
yesterday's. A malformed boundary is refused rather than silently reset, and the page names
the boundary it counted from.

**`--publish <host:dir>`** ships `index.html` and `metrics.tsv` through the same ssh door
the benches are read through, as one child writing both files. A failed publish is loud and
exits 3: a page that quietly stopped shipping goes stale while everybody keeps reading it.
The local page is written first, so a publish that fails never costs the file.

**`--gh-config <dir>`** is `GH_CONFIG_DIR` for the gh children, and it overrides rather than
invents: without it the caller's own environment goes through untouched. gh answers as
whoever that directory says, so a page run from a service manager with a bare environment
must be given it.

**The verb is bounded and it is fast.** `--timeout` takes a whole number of seconds or a
duration (`90s`, `2m`), because the verb's own progress line prints a duration and a flag
that will not accept what the tool prints is a trap. The `/proc` walk happens once, before
the slot loop, and is capped: reading every pid's cwd once per slot is forty slots times a
few thousand processes, which is why a live bench read DOWN at `--timeout 10` and the page
took 24.6 s against the script's 13.8. This path fetches pull requests only — the page
shows no issue count, and the issue round trip was half its gh time.

**Counts only ever reach the page**: no card id, branch name or label, because it is served
to whoever can reach the host.
