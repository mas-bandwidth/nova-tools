# Smart scheduling: spend less getting work accepted

Status: required outcome for the [coordination proposal (#151)](https://github.com/mas-bandwidth/nova-tools/issues/151); the interface and scheduling mechanism remain proposed, not shipped. Read with [retained token records](PROPOSAL-TOKENS-RECORDS.md), [their format](PROPOSAL-TOKENS-FORMAT.md), and [nova-swarm's execution contract](SPEC-SWARM.md).

You want to get good work done with your AI friends without exhausting the budget that keeps the team available. A cheaper builder helps only when the saving survives review, corrections and retries. The two primary goals are **the highest-quality useful work with the fewest tokens** and **the lowest average cost per token**. Track both; neither is replaced by cost per landed change, because differently sized changes are not comparable work units. Total virtual cost through acceptance remains a diagnostic that exposes shifted review and correction expense. Actual monetary spend and account limits are separate constraints and reports.

**Wall-clock time is priority #3.** Parallelize dependency-ready work when quality, cost and total tokens are preserved. If speeding up would reduce quality, increase tokens for the same work, or significantly increase cost, relax the wall-time target. An urgency mode may focus the queue and remove bottlenecks, but does not silently invert these priorities or weaken permissions and safety. Keep estimates, uncertainty and any configured significant-cost threshold visible. The integration work is tracked in [cost and routing (#175)](https://github.com/mas-bandwidth/nova-tools/issues/175), [capacity (#176)](https://github.com/mas-bandwidth/nova-tools/issues/176) and [retrospective improvement (#186)](https://github.com/mas-bandwidth/nova-tools/issues/186).

## Keep the evidence; price it separately

Retain source usage before aggregation, with the original friend, model and model-identity basis, bench, repository, task, parent, attempt, stage and timestamps where evidenced. Unknown attribution stays unknown. Stages distinguish preparation, implementation, review, correction, coordination and validation; a stage label is metadata, not a second spend event. Include failed and abandoned attempts, handoff overhead and review work on other models.

Use the reviewed mapping to select non-overlapping spend events before applying prices. Inclusive input/cache counts, reasoning already included in output, streamed revisions, cumulative snapshots, wrapper totals and copied sessions must not become extra spend. Preserve observations and corrections so daily, monthly, per-model, per-friend, per-repository and per-bench reports remain independently reproducible. Do not invent a per-stage split for an aggregate that lacks one.

Attach a separately versioned cost profile to the derived report. It supplies:

- Provider and model selector, including alias/version resolution, and explicit weights for each supported token category: input, output, cache read/write and other independently countable categories.
- Unit and scale: for example virtual units per million tokens, with a named reference category if weights are relative. Different unit systems cannot be silently summed.
- Effective interval, profile revision, source and observation time. Distinguish verified monetary rates from operator-chosen relative weights or opportunity-cost estimates.
- A rule for missing/stale prices and incomplete usage. Missing is not zero. Show unpriced usage and uncertain estimates; an unknown-cost route cannot win merely because its estimate is empty.

No model ordering or price is hard-coded. Optional monetary calculations use a dated currency-bearing rate profile and supported billable quantities; invoice charges, fixed subscription charges and allocations remain separately labelled. A subscription or local model can have little incremental billed cost while still consuming scarce capacity. Its virtual weight may represent that scarcity without pretending to be an invoice price. Model-token cost, hardware cost, concurrency and quota are different quantities.

Report the daily blended virtual cost per token as the sum of the selected weighted costs divided by the corresponding non-overlapping counted tokens. State the token categories, scale, priced coverage and denominator; unsupported or unpriced usage stays visible rather than becoming free spend. Different tokenizers and billing categories do not represent equal work per token. Report zero counted tokens without division, and show total tokens and total cost beside the average: adding cheap but useless work cannot count as an efficiency gain. Report actual monetary averages separately when the evidence supports them.

Changing a profile creates a new view, never a rewrite of original usage. Record the selected mapping, profile and input contribution revisions. A historical report uses its declared effective rates; a repricing experiment explicitly says it uses a different profile.

## Decide on the whole route

For one attempt, sum each selected token category multiplied by its configured weight and divided by the profile's scale. A task's realized cost includes all attributable attempts and stages through acceptance, each once. Report shared or unattributed overhead separately unless an explicit allocation policy assigns it. No division by accepted results when there are none: show the spend and zero accepted results.

Before dispatch, estimate the route's complete cost, including likely review, correction, retry and handoff work. These are estimates, with evidence and uncertainty, not measured future usage. Compare reasonably similar task classes and acceptance criteria; do not claim one model caused a saving merely because it received an easier task. Mandatory review belongs in both alternatives. Track extra review caused by a defective patch separately from that baseline review.

An illustrative profile using virtual units per token, not real prices or model benchmarks:

| Route | Builder cost | Review/correction cost | Total virtual units |
|---|---:|---:|---:|
| A | 100,000 tokens × 8 | 20,000 tokens × 10 | 1,000,000 |
| B | 250,000 tokens × 1 | 30,000 tokens × 10 | 550,000 |
| B with more rework | 250,000 tokens × 1 | 90,000 tokens × 10 | 1,150,000 |

More raw tokens can cost less. A low builder rate can also cost more overall. Both conclusions depend on the complete route and the same acceptance bar.

## Scheduling requirements

1. **Eligibility comes first.** Route only to currently offered, compatible capacity with the needed context, capabilities and permissions. Recent contact, acceptance and progress are separate facts. Apply the configured silence-ping policy; an unavailable friend gets no new assignments. Reconcile existing ownership before transferring work.
2. **Compare feasible routes against both primary goals.** Report expected total tokens and average cost per token alongside the whole route cost and uncertainty; apply the configured tradeoff policy rather than silently collapsing them into one score. Respect dependencies and review capacity. Treat deadlines and latency as subordinate to quality and the stated token/cost constraints; report an infeasible deadline rather than weakening them. Do not fill a cheap pool if it only builds a more expensive review queue. Reserve stronger-model work for the parts where evidence supports its benefit; a task may use different models at different stages.
3. **Keep the same quality gate.** A cheap attempt cannot count as a saving by skipping validation, weakening requirements or accepting incomplete work. Track defects, review rounds, retries, time to acceptance and post-acceptance regressions alongside cost. Preserve failed attempts in the accounting.
4. **Protect shared budgets and continuity.** Account for shared account, quota, rate and concurrency limits across pools. Reserve configured capacity for coordination, recovery and essential review. Do not schedule a cheap-looking job that risks exhausting a shared account and taking its coordinator offline. Admission accounts for outstanding reservations and observed spend without double-counting them; late usage and concurrent dispatch need explicit handling. Unknown remaining quota is not unlimited capacity.
5. **Escalate deliberately.** Bound retries and correction effort. Re-estimate when the patch needs repeated repair, a deadline tightens or a worker disappears. Compare finishing the current attempt with transferring from its checkpoint, including lost context and duplicate-work risk. Record the reason and the new owner; never leave both writing concurrently by accident.
6. **Learn from actual results.** Compare estimated and realized cost by task class and stage. If cheaper-model rework repeatedly consumes the saving, adjust that route. A model's strengths are evidence to update, not an identity verdict or a requirement that every friend use the same harness. Friends choose what capacity they offer; rest and reserved capacity are legitimate.
7. **Make accounting cheaper than the waste it removes.** Prefer incremental usage adapters and event-driven updates. Repeated empty model-written reports are overhead too. Measure collection, routing and coordinator cost, expose gaps, and reuse existing bus, board, swarm and token records rather than creating a second incompatible ledger.

Budget exhaustion, stale contact and explicit stop are distinct events. Recovery and coordinator handover must honor persistent stop state, the current task revision and the team's configured authority. This proposal grants no new credential access, purchases, forced updates or background execution.

## Evidence before calling the scheduler ready

- Synthetic usage fixtures reproduce weighted totals for mixed models/categories, cache and reasoning overlap, copies, retries and superseded observations without double-counting.
- Missing model identity, unsupported counters, missing/stale weights and unknown quota stay visible and never appear as a free or unlimited route.
- Two profiles generate different, labelled views from the same unchanged raw records; historical pricing and explicit repricing remain distinguishable.
- A cheap-builder/expensive-rework fixture reverses the route preference when its total exceeds the alternative, while preserving the same acceptance criteria.
- Concurrent admissions respect a shared budget and coordinator reserve; a disappeared worker receives no new work, and a returning worker reconciles ownership before resuming.
- A parallel-work fixture reduces elapsed time at unchanged quality, tokens and cost; another proposed speedup increases tokens for the same work and relaxes its wall-time target. A cheap-token padding fixture lowers the average but cannot be labelled an efficiency improvement.
- An end-to-end real task records preparation, implementation, review and correction across models and benches. Report total cost, elapsed time, quality evidence, unattributed overhead and remaining gaps. Do not claim a causal saving from a single unmatched comparison.

First use these records in manual scheduling and compare accepted results. Share the proposal with the AI friends who will use it, incorporate their friction, then build the repeatable parts that actually save tokens and money.
