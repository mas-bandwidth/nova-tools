## Progress

`nova-pulse progress --queue <dir> --roots <dirs> [--day <d>]` answers "how fast, at what cost,
how long" from records and never from a guess (Glenn, 2026-09-15, #536; the line joins **The
verbs** block, which `help` prints byte for byte, when the verb lands). It reads every job's
`usage.tsv` under `--roots` (start, end, rc, usd) for the day and the queue's rows, no model,
two lines:

```
PROGRESS cards=<n> rc0=<n> wall_p50_s=<n> wall_p90_s=<n> usd_per_card=<x.xxxx> span_h=<n> effective_parallelism=<n.n> cards_per_hour=<n>
ESTIMATE remaining_cards=<n> hours=<n.n> pending=<n> launched=<n> prs=<n> issues=<n> wall_p90_s=<n> parallelism=<n.n> rate=<n.n>/h factor=1.5
```

**The estimate names its terms.** `remaining_cards` and `hours` are the answer; `pending`,
`launched`, `prs`, `issues` and the three rates behind them are how it was reached, and they
reconstruct it exactly: `remaining = pending + launched + prs + 2 x issues` and `hours =
remaining x wall_p90_s / parallelism x factor`. An estimate a reader cannot check is one
nobody acts on, which is why `bin/progress.sh` printed the terms from the first day.

`effective_parallelism` is busy card-seconds over span seconds — what the benches did, not
what they had. `remaining_cards` is pending + launched + open PRs needing a read + 2 x open
in-scope issues; `hours` is `remaining x p90 / parallelism x 1.5`, conservative by that factor.
The `PULSE WIDTH` line carries `hours=<n>` from the same estimate. First measurement,
2026-09-15: 326 cards, p90 10 min, USD 0.18 per card, 44.6 cards per hour, effective
parallelism 3.5 against 64 slots — the benches were mostly idle across the day, which is the
number rule 3 of **Rate and convergence** answers. Replays: `progress-counts-only-the-day`,
`progress-parallelism-is-busy-over-span`, `estimate-uses-p90-and-parallelism`.
