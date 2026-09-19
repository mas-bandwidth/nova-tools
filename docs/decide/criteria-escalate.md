# Criteria: the escalate question

version: 2026-09-19.2

Question file: `questions-escalate.json`, which names this file and its version. The pair is
loaded together and this text is carried INTO the request above the state. `internal/decide`
refuses the pair when the versions disagree, and refuses a state that does not carry a declared
typed field, before any provider call is made.

**The answer is a role, never a person.** The binding from a role to a reader is local, is not in
this file, and is never sent.

## Where this sits

`nova-decide help --state <json>` is the first half and it needs no provider, no key and no
network: from the line's own counters — hours on one problem, retries on one rung, failures in the
last hour and how many were self-inflicted, whether a class is recurring, whether landing moved,
and the stated uncertainty — it answers `continue`, `ask-all-friends` or `ask-glenn`, with one
rule over the rest: **the person is asked only after the friends.**

That verb was hooked up and unused on 2026-09-19: zero calls, while every shift lane escalated by
hand into a free-text column. This question is the second half — to WHICH ROLE — and it turns that
column into a typed choice over configured roles. `help_answer` and `help_reason` go into the
state verbatim, so the typed answer is anchored to counters the line measured rather than to how
stuck it feels.

**The human answer is machinery, not a preference.** An answer naming `human` while `help_answer`
is `ask-all-friends` is overruled in `internal/decide/readers.go` and the friends stand. The
provider cannot spend its way past that rule at any confidence.

## The state fields the asker computes first

| field | how the asker gets it | why |
| --- | --- | --- |
| `help_answer` | `nova-decide help --state` verbatim | `human` is overruled unless this is `ask-glenn` |
| `help_reason` | the same line's `reason=` | the counters that made it an ask |
| `what_is_stuck` | one sentence, the thing that will not move | |
| `security_shaped_package` | the changed paths against the security-shaped list | a settled designation the machinery takes without asking |
| `spec_is_silent_or_contradictory` | whether the specification settles what is being built | it is the whole of the design-authority answer |
| `is_a_landing_order_question` | whether the open question is sequencing rather than code | it is the whole of the coordinator answer |
| `holder_of_the_area` | which role has an open read or hold on those files | that role keeps its read |
| `rung_attempts` | the ladder's own evidence: rung, outcome, reason | a second attempt is evidence the work was not a procedure |
| `hours_on_it` | the line's clock | |

## The roles

* **`security-designate`** — a guard, a secret, the sandbox, sudo, deploy keys, the network, the
  reaper, the launcher, the merge machinery. Before any other answer.
* **`design-authority`** — the specification is silent or contradictory, a design default no
  ruling covers, kernel semantics, scope.
* **`lane-owner`** — correctness, tests, documents against binaries, in an owned lane.
* **`all-friends`** — it crosses lanes, or two rulings would have to agree.
* **`coordinator`** — landing order and sequencing, not the code.
* **`human`** — only after the friends, and only what no role may rule: a rule of the fleet, a
  spend, a licence, or two of the person's own instructions in contradiction.

## A worked example (a local trial record, 2026-09-19)

A lane escalated a kernel row as not cuttable: the specification's binding text did not settle
whether the journal's identity seam could move. That is `spec_is_silent_or_contradictory: yes` and
`security_shaped_package: no`, so the answer is `design-authority`. The ruling came back and the
row landed on the bottom rung ninety minutes later. The lane reached that answer by hand; the
fields say it. **One observation, one day: a reason to carry the fields, not evidence that the
reading is calibrated.**
