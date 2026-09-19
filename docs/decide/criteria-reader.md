# Criteria: the who-reads question

version: 2026-09-19.1 — measured from 47 answers on 2026-09-19, six manager lanes, joined to
what the friends did on the message bus.

Question file: `docs/decide/questions-reader.json`. The asker embeds THIS file in the state above
the question, so every lane asks the same question with the same criteria and a change to either
is one versioned edit rather than twelve drifting copies.

## What the answer costs

A needless friend read costs a friend a few minutes. A missed HOLD lands a defect on dev. The two
are not the same size, so where two readers are arguable the answer is the friend. This is why the
who-reads question runs at `--floor 0.5` and not at the route question's 0.65: below the floor the
reader falls back to `opus-child`, which is the CHEAP answer, so escalating here is the risky
direction. See `nova-decide tune --default` in docs/CLI.md.

## The state fields the asker computes first

| field | how the asker gets it | why the question cannot be answered without it |
| --- | --- | --- |
| `security_shaped_package` | the changed paths against the security-shaped list: anything that execs, pushes, reads env or secrets, the sandbox, sudo, deploy keys, the network, the reaper, the launcher, the merge machinery | it is the whole of the `johnny` answer and no amount of prose substitutes for it |
| `friend_holds_the_area` | which friend already has an open read or HOLD on a pull request touching the same files today | a friend who is already holding that area is the reader who will see the collision |
| `design_defaults_taken` | the count of defaults the card took that no ruling covers, which the card itself names | it is the whole of the `stella` answer; #1856 named FOUR in prose and was still answered `opus-child` |
| `normative_spec_moved` | whether the diff changes normative spec text rather than prose | the same |
| `kind`, `files`, `packages`, `lines_changed` | the diff | size is what separates a mechanical change from one wanting a cold read |
| `rung_that_wrote_it`, `attempt` | the card's receipt | a second attempt is evidence the work was not a procedure |
| `test_added`, `pre_existing_tests_repaired` | the gate | a card that repaired pre-existing tests changed something the suite had encoded |

## The ladder, and why each rung

* **johnny** — the designated security reader. Anything that execs, pushes, reads the environment
  or a secret, or lands in the reaper, the launcher or the merge machinery. `security_shaped_package=yes`
  is this answer on its own, at any confidence.
* **stella** — spec and design judgments: a default with no ruling, kernel semantics, normative
  text, scope.
* **emma** — Go correctness, tests, documentation against what a binary prints, in a lane she owns
  whose design is settled.
* **opus-child** — the default: a self-contained mechanical change inside one package, red test and
  one-edit control, no spec question, no design default, not security-shaped.
* **fable-child** — a second independent reading of the same small mechanical card.
* **rowan** — only a coordination call: landing order, sequencing. Not the code.

## The day's measured outcomes, as examples

* `#1815` — extends `--allow-private` on the bus. `security_shaped_package=yes`. Answered
  **johnny** at 1.00. Johnny **HELD** it. Right.
* `#1871` — the removal door refuses `/` and `$HOME`. `security_shaped_package=yes`. Answered
  **johnny** at 0.96. Johnny **HELD** it. Right.
* `#1776`, `#1785`, `#1830`, `#1833`, `#1860` — nova-work kernel and spec work, each with a named
  design default. Answered **stella** at 0.77, 0.88, 1.00, 0.97, 1.00. Stella **HELD** every one.
  Right.
* `#1856` — journal rotation, `design_defaults_taken=4`, journal touched at its identity seam.
  Answered **opus-child** at 0.70. Stella **HELD** it. **Wrong**, and the reason is that the four
  design defaults were in the state's prose and in no field.
* `#1832` — savepoint boundary. Answered **stella** at 0.55, BELOW the floor, so the lane's rule
  defaulted it to Emma by hand. Emma **HELD** it, on a four-character defect the green 361/361 and
  both controls could not see. **The default would have missed it**: below the floor the answer is
  `opus-child`, and this is the one row on the day where that mattered.
* the other 38 — answered `opus-child` or `rowan`; none was later held by a friend.

Binary score on "does this need a friend at all": 11 answers named a friend, 9 cards actually
needed one, 8 of the 9 were named. Precision 8/11, recall 8/9.
