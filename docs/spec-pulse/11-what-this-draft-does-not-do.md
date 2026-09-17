## What this draft does not do

- **No model routing beyond the cost table.** One table in git, read by `cut`: the capable
  model with the lowest average cost per token, and one capability class up after an
  abstain. No fallback provider, no per-card override, no hand on a route; the table is the
  whole policy, and changing it is a diff somebody read.
- **No merging.** It opens draft PRs and cuts read cards. `nova-merge` and a person hold the
  lane, the gate and the read condition; the scatter/gather verbs never run `nova-merge`,
  never label, never mark ready — the manager tier hands approved PRs to the lane (it merges
  nothing itself; **The manager tier**).
- **No priority beyond source order.** `queue.tsv` first, `next.tsv` second, then the
  sources in file order, then the items in each source's own order. A person who wants an
  item first moves its source line up.
- **No cross-repo dependency graph.** Every card is one bounded item on one repo at one head;
  a card that needs another card's PR merged first is a card that is not bounded yet, and it
  waits in an issue until it is.
- **No third try.** A card abstains, is requeued once under a new number (rule 14, #587),
  and a second abstain is a person's: `retry.tsv` names it and the harness's last refusal, a
  person rewrites it, and the rewritten card is a new candidate.
- **No model call, no summary, no judgement.** It never reads a report body, never opens a
  transcript, never scores a finding. The swarm's packet is the whole read.
- **No clock of its own.** No daemon, no `--loop`, no `--watch`. The chain is `--then`;
  the alarm is `width`, run by nova-wake or a person. The one clock is the manager tier's
  cycle (#587), and its tick is **Rate and convergence** rule 1.
