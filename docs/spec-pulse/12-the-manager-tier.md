## The manager tier

Stella's answer to Glenn's *"I want the intelligence; I don't want to spend it sending out jobs
and reading results"* is a third tier between planning and work. **Planning** is a person and
the strong model: decisions, specs, rules; its output is cards and notes. **Manager** is a bounded
controller on the cheapest qualified model: it owns the bus wait, harvests, triages abstains
and HOLD reads by rewriting cards from templates, files dogfood issues, cuts fix cards, hands
non-draft PRs on an approving read plus green CI to nova-merge's lane, and escalates a
decision as one line.
**Work** is swarms and local models. Manager is where the intelligence is spent once and the
scatter-gather is spent never.

Manager executes an approved finite policy and never expands it; it is the single owner of the bus
wait; it keeps one card per work item, deduplicated on the contract line; it revalidates the PR
head before any side effect; it runs an explicit shift length and ends with a handoff line; quiet
time makes no model call and sends no status note; state is published mechanically (the `WIDTH`
line); a note goes out only on a meaningful change; and it never merges a draft spec, never edits
a spec, never addresses the person. Measurement is cost per accepted decision across all tiers
(provider-priced input, output, cache, plus review and retry), with wrong or missed decisions
and recovery latency as gates.

The escalation line, one line, one decision not in the policy:

```
ESCALATE <stamp> <kind> <ref>: <one line>
```

The handoff at the end of a shift:

```
SHIFT END cycles=<n> decisions=<n> escalations=<n>
```

The tier is a verb, and the verb makes no model call:

```
nova-pulse manager --policy <file> --queue <dir> --roots <dirs> --bus <clone> --as <name> --hours <n>
```

One cycle is: `nova-bus wait` in the foreground with the policy's timeout (the one call a
quiet cycle makes); receipt every `START` and `DONE` note and append every other note to
`<queue>/ESCALATE` as one line, composing no reply; harvest every card whose job holds a
`RESULT.md` — push its branch by explicit refspec, open or update its PR, cut its read card
from `<queue>/templates`, and refuse a fix PR carrying neither a `red:` line nor a test file
in its diff; triage each abstain by its reason token, requeueing it once under a new number
on the other bench and escalating the second; hand a non-draft PR whose read said `APPROVE`
to the lane once the head revalidates and every check is `SUCCESS` — `nova-merge add
--lane <dir> --pr <n>`, never `gh pr merge`, never on `HOLD`; refill the queue
from the policy's sources to its floor, deduplicated on PR number, issue number and the
contract sentence, in the policy's scope, leaving `AFTER: PR<n> merged` gates gated; and
write one `MANAGER` line to `<queue>/MANAGER.log`. The policy is key=value lines —
`wait-timeout`, `floor`, `scope-regex`, `sources`, `known-flakes`, `max-attempts`, `lane` — and an
unknown key is a refusal, exit 2, because a policy the tool half-understands is a policy
nobody approved. `lane` is the nova-merge lane directory the manager hands approved PRs to
(rule 20 of SPEC-MERGE: a lane `init` has made); with no `lane` the approval stays on file
and nothing is merged. `mirror` (rule 5 of **Rate and convergence**) is the one key proposed and
not landed (#553).

Replays: `manager-never-expands-policy`, `manager-quiet-time-makes-no-call`,
`manager-dedups-on-contract-line`, `manager-revalidates-head-before-merge`,
`manager-never-merges-draft`, `manager-shift-ends-with-handoff`,
`manager-requeues-once-then-escalates`, `manager-refuses-fix-pr-without-test`,
`manager-hands-merge-to-lane`.
