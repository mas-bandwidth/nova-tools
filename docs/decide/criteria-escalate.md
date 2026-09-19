# Criteria: the escalate question

version: 2026-09-19.1

Question file: `docs/decide/questions-escalate.json`. The asker embeds THIS file in the state.

## Where this sits

`nova-decide help --state <json>` is the first half and it needs no provider, no key and no
network: from the line's own counters — hours on one problem, retries on one rung, failures in the
last hour and how many were self-inflicted, whether a class is recurring, whether landing moved,
and the stated uncertainty — it answers `continue`, `ask-all-friends` or `ask-glenn`, with one
rule over the rest: **Glenn is asked only after the friends are.**

That verb was hooked up and unused on 2026-09-19: zero calls, while twelve manager lanes each
escalated by hand into an `ESCALATE.tsv` with a free-text `what-Rowan-must-decide` column. This
question is the second half — to WHOM — and it turns that column into a typed choice over the
ladder. `help_answer` and `help_reason` go into the state verbatim, so the typed answer is
anchored to counters the line actually measured rather than to how stuck it feels.

## The state fields the asker computes first

| field | how the asker gets it | why |
| --- | --- | --- |
| `help_answer` | `nova-decide help --state` verbatim | `glenn` is refused unless this is `ask-glenn` |
| `help_reason` | the same line's `reason=` | the counters that made it an ask |
| `what_is_stuck` | one sentence, the thing that will not move | |
| `security_shaped_package` | the changed paths against the security-shaped list | it is the whole of the `johnny` answer |
| `spec_is_silent_or_contradictory` | whether the spec settles what is being built | it is the whole of the `stella` answer |
| `friend_holds_the_area` | which friend has an open read or HOLD on those files today | |
| `is_a_landing_order_question` | whether the open question is sequencing rather than code | it is the whole of the `rowan` answer |
| `rung_attempts` | the ladder's own evidence: rung, outcome, reason | a second attempt is evidence the work was not a procedure |
| `hours_on_it` | the line's clock | |

## The six answers

* **johnny** — a guard, a secret, the sandbox, sudo, deploy keys, the network, the reaper, the
  launcher, the merge machinery. Before any other answer, at any height, at any floor.
* **stella** — the spec is silent or contradictory, a design default no ruling covers, kernel
  semantics, scope.
* **emma** — Go correctness, tests, documents against binaries, in a lane she owns.
* **all-friends** — it crosses lanes, or two rulings would have to agree.
* **rowan** — landing order and sequencing, not the code.
* **glenn** — only after the friends, and only what no friend may rule: a rule of the fleet, a
  spend, a licence, or two of his own instructions in contradiction.

## The day's worked example

`work-swarm` escalated AUDIT row E05.6 at 17:22Z as not cuttable: the spec's binding text at
`docs/SPEC-WORK.md:7176-7182` did not settle whether the journal's identity seam could move. That
is `spec_is_silent_or_contradictory=yes` and `security_shaped_package=no`, so the answer is
**stella**. She ruled on both defaults, and the row landed in one card on the bottom rung ninety
minutes later as #1856. The lane reached that answer by hand; the fields say it.
