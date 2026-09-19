# Criteria: the who-reads question

version: 2026-09-19.2

Question file: `questions-reader.json`, which names this file and its version. The pair is loaded
together and the criteria below are carried INTO the request above the state, so every asker asks
the same question with the same criteria and a change to either is one versioned edit rather than
many drifting copies. `internal/decide` refuses the pair when the versions disagree, and refuses a
state that does not carry a declared typed field, before any provider call is made.

**The answer is a role, never a person.** The binding from a role to a reader is local, is not in
this file, and is never sent. This house's binding, and the day it was trialled, are in
`examples/2026-09-19-local-trial.md`, which is an example and not part of the contract.

## What this answer is, and what it is not

It names **who reads first**. It removes no read the evidence already requires, it lifts no hold,
it replaces no holder, and it does not select the person before the friends have been asked. Those
four are machinery, in `internal/decide/readers.go`, and they hold whatever the answer is and at
whatever confidence — a settled security designation is taken with no provider asked at all.

A needless read costs a reader minutes. A read that should have happened and did not lands a
defect. The two are not the same size, so where two roles are arguable the answer is the heavier
one. **No floor for this reading is set here.** The 2026-09-19 rows in
`internal/decide/testdata/` are observations against a previous version of this question and a
previous state shape; they are not adjudicated truth and they tune nothing.

## The state fields the asker computes first

| field | how the asker gets it | why the question cannot be answered without it |
| --- | --- | --- |
| `security_shaped_package` | the changed paths against the security-shaped list: anything that execs, pushes, reads the environment or a secret, the sandbox, sudo, deploy keys, the network, the reaper, the launcher, the merge machinery | it is a settled designation the machinery takes without asking |
| `design_defaults_taken` | the count of defaults the card took that no ruling covers, which the card itself names | it is the whole of the design-authority answer, and prose beside the record is not a count |
| `normative_spec_moved` | whether the diff changes normative specification text rather than prose | the same |
| `holder_of_the_area` | the role that already has an open read or hold on these files | that role keeps its read whatever the answer says |
| `hold_is_open` | whether that holder's hold is unresolved | no answer lifts a hold |
| `kind`, `files`, `packages`, `lines_changed` | the diff | size separates a mechanical change from one wanting a cold read |
| `attempt` | the card's receipt | a second attempt is evidence the work was not a procedure |
| `notes` | optional: anything the card said about itself that no field holds | it is beside the record and no criterion turns on it |
| `test_added`, `pre_existing_tests_repaired` | the gate | a card that repaired pre-existing tests changed something the suite had encoded |

## The roles

* **`security-designate`** — the designated security read. Anything that execs, pushes, reads the
  environment or a secret, or lands in the reaper, the launcher or the merge machinery. Settled by
  the machinery, not asked.
* **`design-authority`** — a design default with no ruling, normative text, scope, kernel
  semantics.
* **`lane-owner`** — ordinary correctness, tests, documents against binaries, in one owned lane
  whose design is settled.
* **`child-review`** — the configured default: a self-contained mechanical change inside one
  package, red test and one-edit control, no specification question, no design default, not
  security-shaped.
* **`second-child-review`** — a second independent reading of the same small mechanical card.
* **`coordinator`** — only a coordination call: landing order, sequencing. Not the code.
