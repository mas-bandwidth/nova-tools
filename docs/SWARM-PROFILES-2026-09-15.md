# SWARM PROFILES — 2026-09-15

Bounded measurement only: the new `nova-tokens profiles --swarm-root <dir>` verb folds every
card's `usage.tsv` under a root and prints, per model, the card count, the median
`tokens_out`, and the overshoot — the count of cards whose `tokens_out` exceeded the card's
own `YOUR TOKEN BUDGET IS <n>` line (read from the sibling `PROMPT.md`). No profile policy,
no provider route, no setting is changed; a card with no numeric budget line (or `unmetered`)
is an absence, never a zero that would read as a free ceiling.

Output shape, one line per model plus the total, sorted by model:

```
PROFILES MODEL model=<model> cards=<n> median_out=<m> overshoot=<n>
PROFILES OK models=<n> cards=<n> overshoot=<n>
```

Today's observed card (the bounded contract review from the issue, one attempt): model
`deepseek-flash`, output 1480, budget line 18000 — `tokens_out` did not exceed the budget
line, so `overshoot=0` under this verb's definition. The raw accounting spend 35639
(reasoning 25202 + input 8957 + output 1480) against the 18000 runner budget is the
threshold the issue flags as an *observed accounting* cap, not a hard provider generation
cap, and is not this measurement's overshoot column. Re-run against the real roots
(`stella-tools/.local/workers/runs/`) to fill per-model cards and medians; this repo holds
only the instrument and its fixtures.
