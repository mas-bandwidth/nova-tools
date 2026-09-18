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

`nova-pulse status --html <out> --benches <file> [--queue <dir>] [--ssh <path>] [--timeout <s>]`
is the fleet page as a verb (`bin/status-page.sh`). It reads every bench in `--benches` over
`ssh <target> bash -s`, in parallel and bounded by `--timeout`, counting live cards from
running card processes — a process whose command line names a job directory, or whose cwd is
under the slot, the authoritative shape `bench-hygiene.sh`'s `live_slot` uses — and never from
log age, which called a silently dead card alive and a long card dead. It writes the page to
`--html`, appends one seven-column row to `metrics.tsv` beside it — `<RFC3339> live queue
merged opened launched free-disk-per-bench` — and prints one line:

```
STATUS HTML wrote=<path> live=<n> queue=<n> down=<n>
```

**A bench that does not answer says DOWN.** Its row is `DOWN (no answer over ssh)`, its disk
is a dash in the metrics row, it adds nothing to `live`, and it is counted in `down=`. A row
of zeros reads as a bench with nothing to do, which is how a fleet nobody could see looked
healthy on 2026-09-17; an answer that cannot be parsed is no answer and says DOWN too. The
page draws the `metrics.tsv` series it writes beside itself, so a reader sees the fleet
widening or stalling rather than only this instant. **Counts only ever reach the page**: no
card id, branch name or label, because the page is served to whoever can reach the host. The
verb writes locally and ships nothing; the caller copies the file where it is served.
