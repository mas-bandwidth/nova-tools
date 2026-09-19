# nova-pulse — specification, draft 1

Glenn, 2026-09-15: *"We work in parallel by default. If you have work that can be done in
parallel, push it all out now in a pulse. When work comes back in, see if there is more work
that can be done again, push it all out, max, in a pulse. Repeat this over and over. Only real
work; if real work runs out, say so and never pad. Scatter, gather, maximum throughput."* And
the ask: a tool, so the behaviour is locked in and never drifts.

One tool, five verbs, all mechanical. **The tool makes no model call**: it enumerates work,
cuts cards from templates, admits them through `nova-swarm batch`, and folds what comes back.
Every token spent is a card's, and the card's usage is already summed on the swarm's `BATCH`
line ([SPEC-SWARM.md](../SPEC-SWARM.md), **Batch: scatter, wait, gather**). The coordinator's
window reads one line per cycle.

- `nova-pulse pool` enumerates bounded open work from declared sources into `pool.tsv`.
- `nova-pulse cut` writes one card per candidate from a typed template, in the practice-17
  shape ([WORKER-CARDS.md](../WORKER-CARDS.md) 17), and a `cards.tsv` with the model by kind.
- `nova-pulse launch` allocates free slots, admits the cards as one batch with a `--then`
  that names `harvest`, and queues what did not fit.
- `nova-pulse harvest` pushes and opens a PR for every card whose `RESULT.md` line 1 is its
  contract line, sends every abstain to `retry.tsv` with the harness's last refusal line,
  cuts a read card per PR, and pulses again — queue first.
- `nova-pulse width` is the drift alarm: one line, and a non-zero exit when there is pool
  and there are free slots and nothing was launched.

SPEC.md's **Conventions** govern — exit codes, the one-line grammar, the field law,
`internal/oneline`, `internal/bounded`, no guessed paths — and this file says only what is
more. Merges are not this tool's: `nova-merge` and a person hold the gates
([SPEC-MERGE.md](../SPEC-MERGE.md), **The merge condition**).
