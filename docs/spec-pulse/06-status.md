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
```

**The verdict is the health metric** (Glenn, 2026-09-15, #549, #553). Contraction is tracked
every tick, and the `CONTRACTION` line's `verdict` is `CONVERGING`, or `EXPANDING` when any one
stream's ratio — cards `cut/done`, prs `opened/merged`, issues `filed/closed` — has been above
1 for two consecutive hours. `EXPANDING` means the spec work up front was not done properly or
the engineering practice is lax, and the coordinator names which, on the record, before another
implementation card goes out. The same verdict belongs on nova-work `check`, from the journal,
under #500. Replay: `status-expanding-after-two-hours-above-one`.
