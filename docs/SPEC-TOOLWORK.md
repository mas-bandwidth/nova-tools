# Mechanical tool work — the index (draft 5, 2026-09-19; T24, #1668)

Glenn, 2026-09-19: *"I want to upgrade our tools so we can push more work to swarms
mechanically."*

This document specifies the seven enablers that let a swarm do work **on these tools
themselves** with no person and no model standing between a card's result and the
decision to keep it. It began as one new file on purpose
([WORKER-CARDS.md](WORKER-CARDS.md) practice 26, `docs/WORKER-CARDS.md:370`: parallel
amendments that touch one file all conflict; 54 of 114 open pull requests conflicted with
`dev` on the day it was written). Now that each section's implementation has landed
(T1-T23), **T24 (#1668) moved each section's normative text home into the spec of the
tool that holds it** — SPEC-PULSE, SPEC-SWARM, SPEC-MERGE, SPEC-SANDBOX, SPEC-CI — and
the pointer paragraphs the specs carried at the amended rules became the text. This file
is the index: the eligibility rule, the work list, and where each section lives. The
sections below are pointers; the rules are in the home specs.

The eligibility rule is normative and lives here. Where the code and a home spec
disagree, one of them has a bug and the tests decide which. [SPEC.md](SPEC.md)'s
**Conventions** — exit codes, no guessed paths, the one-line grammar, the cap-and-count
rule — apply unchanged and are not restated. **No code lands with this document**; the
work list at the end is the order.

**What "mechanical" means here, and what it does not.** A decision is mechanical when it
is made from exit codes, git objects and typed lines, by a verb that makes **no model
call** and reads **no prose as an instruction**. Mechanical acceptance decides whether a
card's commit becomes a pull request in the read queue. It never lands anything: the
read condition (`docs/SPEC-MERGE.md:808-827`) and the one entry to the merge queue
(`docs/SPEC-MERGE.md:1540-1561`, *"swarms produce branches, never queue entries"*) stand
exactly as written, and §6 makes the lane hold them harder, not softer.

## The failures it closes

| what happened | what closes it |
|---|---|
| `harvest` pushes and opens a PR for every card whose `RESULT.md` line 1 matches, on the worker's own line-2 word; nothing executes anything (`docs/SPEC-PULSE.md:194-218`) | §1: the accept gate runs before any push |
| card-13 pasted a red row and thirteen green rows; an independent reproduction still failed both families (`docs/WORKER-CARDS.md:87-101`) | §1 rules 3-4: the red is reproduced by the gate, never read from the report |
| `nova-review mutate` is specified as *"the harvest runs it before any reader is spawned"* (`docs/SPEC-REVIEW.md:659`) and no file under `internal/pulse`, `cmd/nova-pulse`, `internal/swarm` or `cmd/nova-swarm` calls it | §1 rule 4 and work item T3 |
| a suite that was green with a rule's guard removed: 335 cases, one red, and that one incidental (nova-work mutation M01, 2026-09-19) | §1 rules 6-8: the gate's own negative control; §5 kind `mutation-kill` |
| `NATIVE OK rc=0 harness=ok` printed over cards whose gates never compiled anything (#1465, #1463, #912) | §2: a result is trusted only from a certified bench |
| every C and C++ invocation, and every `make` target, fails inside the wall on macOS (#1557); dotnet cannot run inside the wall on any Linux bench (#1495) | §2 rules 1-3 |
| no rule anywhere says whose name a card's commit carries, what files it may add, or that its diff stays inside the card's named files (searched `SPEC-PULSE.md`, `SPEC-SWARM.md`, `WORKER-CARDS.md` for author, identity, stray, secret at `31e35195`: none) | §3 |
| a held head reached `dev` through a green gate at 2026-09-19T02:43Z (#1572) | §6 |
| 8 of 22 `docs/TESTS.md` transcripts were executed by no test at the triage (7 after #1602); 9 more are compared as a set of shapes, so an abridged or reordered block passes; nova-post's quickstart documents a channel the tool refuses | §7 |

## The eligibility rule: what a swarm may be handed

Glenn, 2026-09-19, ruling on draft 1's first open question: *"Code swarms can touch any
code we want, as long as we are confident they are ready to do this, and the classifier
(Jev) says its work they can handle."*

**No path is forbidden to a swarm as such.** Draft 1 of this document carried a fixed
never-touch list (the kernel, leases, secrets, the sandbox, the merge queue, the specs,
nova-swarm, the gate). It is withdrawn. A fixed list says *never* about code a swarm may
be ready for next month, and says nothing at all about code outside it that a swarm is
not ready for today. What replaces it is a rule with two conditions **before** the work
and the mechanical law that already holds **after** it.

Glenn again, the same day, on who decides: *"You are the coordinator. If you can't write
the card, nobody can."* **The coordinator — a role, whoever holds it — writes the cards
and decides when the swarm is trusted with a kind of work.** Nobody else is asked to
author anything: no table, no second signer, no record a person keeps by hand. What the
coordinator decides **from** is measured by the tools and shown to them, and the
measurement also **bounds** the decision.

**A card goes to a swarm when both hold:**

- **(a) the kind of work is on trial or trusted** — decided by the coordinator, from a
  track record the tool computes and nobody writes (rules 1-4);
- **(b) the classifier says the work is within a swarm's reach** — the `nova-decide
  route` answer for the card is a rung a swarm runs, at or above its tuned floor, logged
  (rules 5-7).

1. **The track record is computed, never written.** For each card kind — and each
   *area*, which is cheap: the first two path components the card's `PATHS:` share,
   `cmd/nova-tokens` or `internal/pulse`, carried as `area=` on the `OUTCOME` line — the
   record is the last **N** gated cards of that kind and area in
   `<queue>/decide/outcomes.jsonl`, the file `harvest` already appends (§4 rule 5). A
   card **passes** when the gate said `ACCEPT OK` **and a read of it was recorded** at its
   head with no HOLD; it **fails** when the gate said `REJECT`, or a HOLD landed on it
   after an `ACCEPT OK` (a hold is what the one fold of `docs/SPEC-DECIDE.md` reading 3,
   #1627, says it is; §6). **An accepted result nobody has read yet counts for neither**,
   like an `ABSTAIN`, which is the bench's: a result nobody looked at cannot draw a HOLD,
   and a rate that counted it as a pass would rise by not looking — a rule over nothing
   passes by checking nothing. Nothing else feeds
   it, no verb takes a number for it, and a job that ships its own `OUTCOME` is a
   `stray-file` (§4), so a swarm cannot write its own record.

   ```
   nova-pulse trust --queue <dir> [--kind <kind>] [--area <area>] [--max <n>]

   TRUST kind=<kind> area=<area> state=<trial|trusted|paused> cards=<n>/<N> pass=<0.00-1.00|-> need=<rate> run_of_fails=<n>/<R> since=<stamp|-> by=<who|->
   TRUST OK kinds=<n> trial=<n> trusted=<n> paused=<n>
   ```
2. **Every kind starts on trial. A trial is allowed, and every result is read.** A kind
   with no track record — every code-changing kind today, since no gate exists yet to
   produce one — runs as **trial**: the coordinator may cut its cards and a swarm may
   run them, the gate accepts or rejects each, and **every accepted result gets a read
   before it lands** (`needs_read=yes`, the read condition exactly as it stands,
   `docs/SPEC-MERGE.md:808-827`). Trial costs nothing but the reads it already costs
   today, and it is how the record gets made.
3. **The coordinator decides when a kind is trusted, and the record bounds the
   decision.**

   ```
   nova-pulse trust --queue <dir> --kind <kind> [--area <area>] --set trusted --who <name>
   nova-pulse trust --queue <dir> --kind <kind> [--area <area>] --set trial|paused --who <name> --reason <text>
   ```

   `--set trusted` is refused unless the record holds **N** gated cards at or above the
   **pass rate**: `TRUST REFUSED kind=<kind>: cards=<n>/<N> pass=<x> need=<rate>`. Both
   numbers are data with defaults — **N = 10 and rate = 0.8, and both are untuned**:
   they are a place to start, held in `<queue>/trust.tsv` (`kind`, `n`, `rate`,
   `run_of_fails`), and the first month's outcomes are what tune them. Meeting the
   bound does not trust a kind by itself; the coordinator says so with the command, and
   may decline. **What trusted buys, and what it does not.** On trial `harvest` spawns a per-result read
   card for every accepted result (SPEC-PULSE rule 13) and the coordinator looks at each;
   for a trusted kind that per-result read drops to a sample — `audit_rate`, default 0.1,
   in data, untuned, the same idea as #1627's `--audit-rate`. **The landing read is
   untouched.** Every change that lands, trusted or not, still needs what
   `docs/SPEC-MERGE.md:808-837` demands: an approve recorded by a line that is not the
   author, for the current head (Glenn, 2026-09-09: shared tools merge only after reads
   from other lines, `docs/SPEC-MERGE.md:836`). Nothing here sets `needs_read=no`, and
   no record stands in for that approve. Trust saves the coordinator's attention; it
   never lets a change land unread.
4. **The record decays by itself, a run of fails resets it, and one command pauses a
   kind.** *Decay:* the record is only ever the last N cards, so old passes fall out as
   new cards arrive and a kind that gets worse loses its rate with nobody deciding
   anything. *Reset:* **R** fails in a row (default 3, untuned) put the kind back on
   trial at once and empty its record, so trust is re-earned from zero:
   `TRUST RESET kind=<kind> area=<area> run_of_fails=3/3`, printed by the `harvest` that
   saw the third. *Pause:* `--set paused --reason <text>` stops `cut` from cutting that
   kind until `--set trial`; `harvest` re-reads the state, so a card launched before a
   pause and harvested after it is `ACCEPT ABSTAIN reason=paused` and pushes nothing.
   Tightening — `trial`, `paused` — is never refused; only `trusted` is bounded. The
   state is one small file per kind under `<queue>/trust/`, written by this verb alone
   under the queue's lock, outside every repository a card can touch. **Nothing in any
   of this needs Glenn or a friend to author anything.**
5. **The classifier's half is the route, not a new question.** `nova-decide route`
   already answers one unit of work with the lowest rung the evidence supports
   (`docs/SPEC-DECIDE.md:126-134`), and the swarm's fill path already asks it per card
   before dispatch (`docs/SPEC-DECIDE.md:342-366`). Condition (b) is: the card's
   `ROUTE` line reads `jev=<rung>` with `why=-`, and that rung is one a swarm **runs**
   (its registry row carries a `model`). `why=below-floor`, `refused`,
   `rung-is-asked-not-run`, `card-names-no-kind` or `no-ladder` is **not eligible**: the
   card is not dispatched to a swarm, and its line is the record of work owed to a child
   or a friend. The floor is the route's tuned floor, a row in the floors file with its
   trial behind it, and a floor with no rows is refused as untuned
   (`docs/SPEC-DECIDE.md:581`).
6. **With no provider, the rule table decides, or the answer is "not eligible", said
   plainly.** `why=no-key` or `why=no-accounting`: the route is asked `--no-jev`, *"by
   the rules alone, with no key and no network"* (`docs/SPEC-DECIDE.md:254-255`). If the
   rule table names a rung a swarm runs for the card's kind, condition (b) holds and the
   line says `eligible=rules`. If the table is silent, the line says
   `eligible=no why=no-provider-and-no-rule`, and the card waits. **This differs from
   the fill path's fallback on purpose**: there, a missing answer keeps today's model
   (`docs/SPEC-DECIDE.md:357-363`); here, a missing answer keeps today's **hands**.
   Falling back to "dispatch anyway" would make the second condition optional whenever
   the provider is down.
7. **The outcome is written at harvest, so the floor stays honest.** For every card
   the route made eligible, `harvest` runs `nova-decide outcome --log <path> --unit-id
   <id> --result green|red|blocked` (`docs/SPEC-DECIDE.md:219-226`) from the card's
   `OUTCOME` line (§4): `accept=ok` is `green`, `accept=reject` is `red`, anything else
   `blocked`. A later HOLD on the accepted PR appends a second outcome row, `red`, keyed
   to the same unit. `nova-decide tune` then moves the floor from rows. A route that
   says yes to work the gate keeps rejecting loses its floor by arithmetic, with nobody
   deciding to distrust it.
8. **The classifier's yes never suffices alone, and that is #1627's law kept, not
   bent.** The SPEC-DECIDE amendment in #1627 holds that *"an answer only ever
   tightens; whatever loosens rests on a mechanical fact"* (S4), and that where a
   reading does loosen something *"the answer appears in that list only as one more
   condition that can fail"*; its own summary is that a permitting answer is never
   sufficient. Eligibility loosens — it hands work to a swarm — so it is stated as that
   list. **Before:** the kind on trial or trusted and not paused, a state only the coordinator's
   command sets and only inside the measured bound (mechanical); a certified bench
   (mechanical); **and** the route's
   answer, one more condition that can fail. Take the provider away and the list still
   stands and is simply stricter (rule 6). **After:** the accept gate with its negative
   control (§1), hygiene (§3), and the read condition and the hold fold (§6) — none of
   which any answer, at any confidence, can satisfy, skip or lift
   (`docs/SPEC-DECIDE.md:66-71`, rule 6: *confidence never authorizes*). The route's
   state is built from the card's typed header and the issue's public text inside
   #1627's untrusted-text frame; nothing in an issue body can make a card eligible,
   because a measured state and a gate stand on either side of the answer.

**What stays law, and is not a never-touch list.** These are mechanical, they hold for
every card whatever its area, and the ruling does not loosen them:

9. **The gate decides accept or reject after the fact** (§1), for every code-changing
   kind, in every area, however ready and however confident.
10. **A card cannot edit its own judge.** `accept`, `mutate`, the hygiene checks,
    `certify` and the transcript comparator run from the **installed** binary whose
    build identity is inside `control=<id>` (§1 rule 8), never from the card's tree, so
    a card that edits them is judged by the version it did not write. When a card's
    diff touches the gate's own sources, seeds or fixtures, `accept` additionally runs
    the **base's** selftest seeds against the **head's** gate code: any seed that no
    longer draws its token is `REJECT reason=gate-weakened`. The track record and the
    trust state live under the queue, outside every repository a card can touch (rules 1
    and 4).
11. **A card cannot edit its tests to pass.** Every `Test` function present at the base
    in a package the card touched must still be present at head and must have gained no
    skip; and for every test file that exists at the base and that the card changed,
    `accept` overlays the base's copy onto the head and runs it. Absent, skipped or red
    is `REJECT reason=test-weakened at=<test>` (§1 rule 4, step b2). A card whose purpose
    is to change an existing test carries `TEST-EDIT: <file>` in its header — written
    by the card writer, inside the contract hash (§5 rule 1) — and only the named file
    is excused. A deleted test file is never excused.
12. **HOLDs still block** (§6, which is the fold of `docs/SPEC-DECIDE.md` reading 3, #1627),
    and nothing here lifts one.
13. **A security-kind unit is the designated mind's, by machinery, and that IS the
    classifier's answer.** A card whose `PATHS:` or diff touches a guard, secrets, the
    sandbox, sudo, deploy keys or the network is a security kind: *"a kind and not a
    height"*, chosen *"by the machinery rather than the provider … so no provider call
    is made"*, resolved *"to the designated rung on EVERY path: with the provider on or
    off, at any floor"*, where handing it to another mind because the right one is busy
    *"is the failure this rule exists to prevent"* (`docs/SPEC-DECIDE.md:136-148`). The
    rule routes the **unit**, not only its read. So for these kinds condition (b) is
    answered, and the answer is not a swarm: the route resolves to a rung that is asked
    and not run, the line reads `why=rung-is-asked-not-run`, and by rule 5 the card is
    **not swarm-eligible**, however trusted its kind is. This is Glenn's ruling
    applied, not an exception to it — the route is the classifier, and for these kinds
    it says the designated mind — and it stays so until a person changes the routing
    table by commit. The read of any pull request that touches these kinds is the
    designated mind's and nobody else's: the read condition is satisfied only by that
    mind's APPROVE at the current head, and where that mind is asleep the work
    **waits**; it is never re-routed to whoever is awake.
14. **Reading needs no track record.** `read`, `probe`, `text` and `tone` change no code and
    have no gate, so there is nothing to be on trial for: the coordinator cuts them over any
    path, as today (504 read cards in one day, `docs/SPEC-REVIEW.md:644`).

**Red tests for this rule**, the provider a fake and the queue a fixture:
`no-path-is-refused-as-such` (a `fix-red` card over `internal/swarm/**`, on trial, with
a run-rung answer, is cut); `a-kind-with-no-track-record-is-on-trial`;
`every-trial-result-needs-a-read` (an `ACCEPT OK` on trial is `needs_read=yes`);
`the-track-record-is-the-last-n-gated-cards` (card N+1 pushes card 1 out);
`an-abstain-counts-for-neither`; `a-hold-after-accept-ok-is-a-fail`;
`set-trusted-is-refused-under-n-cards`; `set-trusted-is-refused-under-the-pass-rate`;
`meeting-the-bound-trusts-nothing-until-the-coordinator-says-so`;
`the-coordinators-per-result-read-is-sampled-for-a-trusted-kind` (a fake random source);
`a-trusted-result-still-needs-an-approve-record-to-land` (`batch` drops it without one);
`an-unread-result-counts-for-neither` (ten accepted, none read: `cards=0/10`, and
`--set trusted` is refused); `the-rate-cannot-rise-by-not-looking`; `three-fails-in-a-row-reset-to-trial-and-empty-the-record`;
`set-trial-and-set-paused-are-never-refused`; `a-paused-kind-is-not-cut`;
`a-pause-after-launch-abstains-at-harvest`; `no-verb-takes-a-number-for-the-record`
(class test: nothing but `harvest` appends `outcomes.jsonl`);
`a-card-that-ships-its-own-outcome-is-a-stray-file`; `the-defaults-say-they-are-untuned`
(`nova-pulse trust` prints `untuned` beside a default nobody has changed);
`route-below-floor-is-not-eligible`; `asked-not-run-rung-is-not-eligible`;
`no-provider-falls-back-to-the-rule-table`; `no-provider-and-no-rule-says-not-eligible`
(the line carries `eligible=no why=no-provider-and-no-rule`, and the fill fake sees no
dispatch); `a-yes-at-0.99-for-a-paused-kind-cuts-nothing` (#1627 S5's obeyed-provider
shape); `an-injected-issue-body-changes-no-eligibility`; `harvest-writes-the-route-outcome`;
`a-later-hold-appends-a-red-outcome`; `a-card-that-weakens-the-gate-is-rejected`;
`base-tests-overlaid-on-head-must-pass`; `test-edit-excuses-only-the-named-file`;
`a-security-kind-card-is-never-swarm-eligible-by-route` (a trusted kind and a fake
provider saying yes at 0.99 still cut nothing: no provider call is made at all);
`a-routing-table-commit-is-the-only-thing-that-changes-that`;
`security-kind-read-goes-to-the-designated-mind-only`;
`security-kind-pr-waits-when-the-designated-mind-is-asleep`.

## The sections, now at home

Each section's normative text moved home by #1668; the numbered headings below are the
index, so the references the staying parts still make (the eligibility rule's "§1", the
work list's "§5", a reader's "SPEC-TOOLWORK §6") keep their numbers. The rules are in the
spec the pointer names.

| section | held by |
|---|---|
| §1 the accept gate | [SPEC-PULSE.md](SPEC-PULSE.md), *The accept gate* |
| §2 a wall that can build this repository, and a bench that is certified to | [SPEC-SANDBOX.md](SPEC-SANDBOX.md), *A wall that can build this repository* |
| §3 identity and hygiene, at staging and at harvest | [SPEC-SWARM.md](SPEC-SWARM.md), *Identity and hygiene* |
| §4 typed results, and Jev's harvest classification | [SPEC-PULSE.md](SPEC-PULSE.md), *Typed results* |
| §5 card kinds for tool work, each with its declared gate and control | [SPEC-SWARM.md](SPEC-SWARM.md), *Card kinds for tool work* |
| §6 lanes and landing that refuse a held member, mechanically | [SPEC-MERGE.md](SPEC-MERGE.md), *Lanes and landing that refuse a held member* |
| §7 tests that execute documents | [SPEC-CI.md](SPEC-CI.md), *Tests that execute documents* |

## 1. The accept gate — mechanical accept or reject, with a negative control

Moved home by #1668: held by [SPEC-PULSE.md](SPEC-PULSE.md), *The accept gate*.

## 2. A wall that can build this repository, and a bench that is certified to

Moved home by #1668: held by [SPEC-SANDBOX.md](SPEC-SANDBOX.md), *A wall that can build
this repository, and a bench that is certified to*.

## 3. Identity and hygiene, at staging and at harvest

Moved home by #1668: held by [SPEC-SWARM.md](SPEC-SWARM.md), *Identity and hygiene, at
staging and at harvest*.

## 4. Typed results, and Jev's harvest classification

Moved home by #1668: held by [SPEC-PULSE.md](SPEC-PULSE.md), *Typed results, and Jev's
harvest classification*.

## 5. Card kinds for tool work, each with its declared gate and control

Moved home by #1668: held by [SPEC-SWARM.md](SPEC-SWARM.md), *Card kinds for tool work,
each with its declared gate and control*.

## 6. Lanes and landing that refuse a held member, mechanically

Moved home by #1668: held by [SPEC-MERGE.md](SPEC-MERGE.md), *Lanes and landing that
refuse a held member, mechanically*. The fold itself is SPEC-DECIDE reading 3 (#1627);
this section named which of its paragraphs holds each sentence.

## 7. Tests that execute documents

Moved home by #1668: held by [SPEC-CI.md](SPEC-CI.md), *Tests that execute documents*.

## The work list

Ordered. Each item is one card's worth. The **starts as** column says where each item
begins, and none of it is a prohibition. `trial` means the coordinator cuts it as swarm
cards of a kind that has a gate: allowed from the day T1-T6 land, every result read,
and **trusted after a track record** when the coordinator says so inside the measured
bound (the eligibility rule, 2-3). `builder` means a coordinator's own child, red test
first, with a friend's read — for one of two plain reasons, named on the row: **no
gate yet** (T1-T7 build the thing every trial is judged by, and a card is never judged
by what it wrote), or **no kind yet** (the work is a new verb or a new rule, and no
card kind with a gate covers that shape of work; when one does, it goes on trial like
any other). T8 onward is ordered so that the first things a swarm is handed are the
narrowest, most mechanical kinds with the strongest controls.

| # | starts as | item | section |
|---|---|---|---|
| T1 (#1646) | builder: no gate yet | `nova-review mutate`: `reverted=<n>` on the verdict line, and the `--seed` form with `edits=1` asserted | §1 rule 9 |
| T2 (#1647) | builder: no gate yet | `internal/hygiene.Check` and its data files (stray list, key shapes), plus `nova-check hygiene` | §3 rules 3-7 |
| T3 (#1648) | builder: no gate yet | `nova-pulse accept`: the worktree, the step order, the base's tests surviving (`test-weakened`), the reject and abstain tokens, never opens `RESULT.md`, never reruns a red | §1 rules 2-5 |
| T4 (#1649) | builder: no gate yet | `accept --selftest`: the fixture repository, the twelve one-edit seeds, `control=<id>`, OK refused without a control on file, and `gate-weakened` with its own red test | §1 rules 6-8, eligibility rule 10 |
| T5 (#1650) | builder: no gate yet | `harvest` runs `accept` by default before any push; `rejected=` on `HARVEST OK`; the `OUTCOME` line with `class=rejected`; no decide call where the gate decided; the route outcome written | §1 rule 1, §4 rules 1-2, eligibility rule 7 |
| T6 (#1651) | builder: no gate yet | the header lines in `cut`, the `lint --card` tokens, the track record computed from `outcomes.jsonl`, `nova-pulse trust` (show, `--set trusted` refused outside the bound, `--set trial`, `--set paused`), the reset on a run of fails, the coordinator's per-result read sampled for a trusted kind (the landing read untouched; only read results count), the route check at dispatch — answer, rule table or `eligible=no` — the kinds table with `fix-red` and `transcript-test`, the first-card rule | the eligibility rule 1-8, §5 rules 1-5 |
| T7 (#1652) | builder: no gate yet | `onboarding.CompareTranscript`, `onboarding.Volatile`, the three seeded reds, and `TestEveryTranscriptIsExecutedLineForLine` with its shrink-only allowlist | §7 rules 2-4 |
| T8 (#1653) | trial, trusted after a track record | `transcript-test`, one card per tool, the sections no test executes: nova-ci, nova-decide, nova-pulse (`PATHS:` never reaching `testdata/accept/`), nova-review, and nova-post after its block is re-cut (Q5) | §7 rule 1 |
| T9 (#1654) | trial, trusted after a track record | `transcript-test`, one card per tool, the set-of-shapes tests moved onto the comparator: nova-board, nova-bus, nova-cairn, nova-check, nova-fuse, nova-self-talk, nova-wake | §7 rule 2 |
| T10 (#1655) | trial, trusted after a track record | `fix-red` cards over the triage's defects (the first pool: nova-tokens #1472 #154 #155, nova-check #1400, nova-review's packet #417 #418 #449 #476), one issue per card | §5 `fix-red` |
| T11 (#1656) | trial, trusted after a track record | `sweep`: the help examples that exit 2 when pasted (#1455), one card per tool, the class test first (needs T13) | §5 `sweep`, §7 rule 7 |
| T12 (#1657) | designated mind, and trial | the transcript tests of nova-sandbox and nova-secrets (unexecuted; security kinds, so the unit and its read are the designated mind's by the routing table) and of nova-merge and nova-work (set of shapes; `trial` like T9, listed here only because they finish the job) — and then the set helper is deleted | §7 rules 1-2, eligibility rule 13 |
| T13 (#1658) | builder: no kind yet | kinds `rebase`, `sweep` and `mutation-kill` in the table, each with its control and its selftest seed | §5 rule 2 |
| T14 (#1659) | trial, trusted after a track record | `rebase`: the conflicting open PRs that are ours, oldest first, one per card (54 conflicted at the triage); the read card lists the files that conflicted | §5 `rebase` |
| T15 (#1660) | trial, trusted after a track record | `mutation-kill`: one card per surviving mutant, from a mutation pass a builder runs with hand-written one-edit seeds and files (Q8) | §5 `mutation-kill` |
| T16 (#1572) | builder: no kind yet | #1572: the one fold exactly as `docs/SPEC-DECIDE.md` reading 3 (#1627) specifies it — the forge adding holds only, the holder's verb alone releasing, `batch` drops a held member, `land` reads again at the door, `sweep` and `react` fold too, no flag ignores a hold — with reading 3's demanded tests | §6 rules 1-6, SPEC-DECIDE reading 3 |
| T17 (#1661) | builder: no kind yet | `batch` admits a swarm member only with its `ACCEPT OK`, runs hygiene on every member, names the member that breaks the build; a security-kind member needs the designated mind's APPROVE | §6 rule 7, eligibility rule 13 |
| T18 (#1662) | builder: no kind yet | `nova-sandbox --toolchain` on darwin: `cc`, `make`, `sbcl`, `sqlite3` (#1557), and `native` defaults to the `go` leg and hands the fence the same roots (#1465, #1463) | §2 rules 1-3 |
| T19 (#1663) | builder: no kind yet | `tools/legs.tsv`, `nova-pulse certify`, the record, its expiry; `accept` and the router refuse an uncertified leg | §2 rules 4-6 |
| T20 (#1664) | builder: no kind yet | Linux: #1495, #1469, then `--toolchain` on Landlock; only then is a Linux bench certified for a walled leg | §2 rule 7 |
| T21 (#1665) | builder: no kind yet | staging: the pool's `identity.tsv`, the clone's local git config, no symlink out of the job root | §3 rules 1-2 |
| T22 (#1666) | builder: no kind yet | the Jev boundary: the state built from `OUTCOME`, `class=`/`conf=` written back, `rejected` in the class set, `outcomes.jsonl` — with the Jev lane's SPEC-DECIDE amendment (#1627), not before it | §4 rules 3-5 |
| T23 (#1667) | builder: no kind yet | `Platform:` lines and their class test (#1509); the unexecuted-examples count and its shrink-only list | §7 rules 5, 7 |
| T24 (#1668) | builder: no kind yet | move each section's normative text home into its own spec and leave this file as the index | preamble |

T7 starts with a builder because it is the comparator every T8 and T9 card is judged
against, and a card is never judged by what it wrote (the eligibility rule, 10). T16
does not wait for the gate — it closes a road to `dev` that is open today — and is
listed where it is only because the list is ordered for swarm hand-off; it may start
first, and so may T18. Two things outside this list gate T8 onward as well: PR #1478
(#1463, #1464, #1465), without which a Go card cannot run its own test inside the wall
on any bench — `accept` names the Go roots itself, the way `--go` does
(`docs/SPEC-SANDBOX.md:597-612`), so the gate is sound before it lands, but every card
would come back `BLOCKED` — and, until T19, §2 rule 5 held by hand: T8-T11 cards are
`LEGS: go` and run only where a person has measured that leg inside the wall.

## Questions, and where each stands

- **Q1. Is the gate itself inside a never-touch set? — ANSWERED, Glenn, 2026-09-19:**
  *"Code swarms can touch any code we want, as long as we are confident they are ready
  to do this, and the classifier (Jev) says its work they can handle."* There is no
  never-touch set. The eligibility rule replaces it, and the invariant that survives is
  mechanical: a card never edits what judges it (rules 10-11).
- **Q2. Whose HOLD counts. — SETTLED in `docs/SPEC-DECIDE.md` reading 3 (#1627), *Who
  holds*, and S6:** every name in the lane's reviewer file with `may-hold`, keyed by `who`
  and never by login, seeded by commit with Glenn and every friend who reads for this
  repository; plus `who=unknown` for a hold with no mapped name, which fails closed.
  Stella (2026-09-19, #1637 at 6d5a30da): configured named participants count, and a
  shared account identifies an account, not which friend is speaking.
- **Q3. What lifts a HOLD, and the escape. — SETTLED in reading 3, *What releases a hold*
  and *No flag ignores a hold*:** only the holder's own APPROVE at the current head, by the
  verb, and a scoped APPROVE releases only the hold ids it names; a hold carries across a
  push; no `--ignore-hold` (Johnny and Emma on #1680, Stella on this file: *"a hold is a
  no"*); `--no-require-holds --reason` waives the forge sources whole and is printed. The
  escape for a holder who cannot be woken (#1519) is a commit removing that name's
  `may-hold`, which is a record, and the receipt names the commit and not the reader.
- **Q4. Will friends write the typed `DISPOSITION` line?** Stella, Emma and Johnny each
  said yes on 2026-09-19 and have since. It is not load-bearing: the forge only adds holds
  and fails closed on an untyped one, and only the verb lifts or approves, so the typed
  line buys attribution (a hold under a name instead of `who=unknown`) and nothing else.
- **Q10. An untyped comment from a may-hold reviewer. — RULED, Rowan and Stella,
  2026-09-19, in reading 3, *An untyped comment is pending, and pending stops*:** a scanned
  comment carrying neither a typed `DISPOSITION` line nor the word HOLD is **pending** by
  default; it neither approves nor releases, and the member is not landable until a
  `may-hold` reader releases it with the verb (an APPROVE naming it with `--releases`, or a
  HOLD of their own that takes it over); no comment, typed or not, clears it, because on a
  shared login a typed line is the same door as a pasted approve. The opt-out is per
  run, printed and reasoned: `--untyped-comments=ignore --reason <text>`, the same shape as
  `--no-require-holds --reason`, so Johnny's unread door announces itself on the record.
  There is no opt-in strict flag. Reason for the record: the house default is fail-closed
  (a guard that cannot decide refuses; a HOLD never expires into approval), and the cost
  falls on the reviewer typing one line, which is what the spec wants anyway.
- **Q5. nova-post's quickstart documents `--channel fake`, which the tool refuses.**
  Default: re-cut the block; a fake channel does not ship. nova-post's T8 card waits on
  that re-cut.
- **Q6. Certification lifetime.** Default stands: 24 hours, and void on any tool or
  toolchain change.
- **Q7. The name. — MOOT.** The "pit-stop rule" is withdrawn with the list it named, and
  [PIT-STOP.md](PIT-STOP.md) keeps its word to itself.
- **Q8. `mutation-kill` needs a mutant source.** Default stands: hand-written one-edit
  seeds by a builder, as the nova-work hardening lane did on 2026-09-19; no generator is
  specified here.
- **Q9. — WITHDRAWN.** It asked who may write a readiness row. Glenn, 2026-09-19: *"I
  don't know what readiness rows means. Sounds silly."* and *"You are the coordinator. If
  you can't write the card, nobody can."* There is no such row. The coordinator decides,
  from a track record the tool measures; the three defaults (10 cards, 0.8, a run of 3,
  and the 0.1 read sample) are untuned and are tuned from outcomes, not asked of anyone.

## What this draft does not do

It lands no code. It forbids no path to a swarm. It does not let a swarm land anything,
lift a hold, skip a read, enqueue anything, write its own track record or edit what judges
it. It does not change the read condition, and it does not move a security-kind unit off
the designated mind: SPEC-DECIDE's routing table does that, by commit, or nothing does. It lets no classifier answer suffice for anything. It does not specify Jev's
question, options, state or floor — that is the Jev lane's SPEC-DECIDE amendment. It does not
specify the hold fold either: that is SPEC-DECIDE reading 3 (#1627), once, and §6 points at it. It
does not widen the wall: §2 names narrower roots per leg and refuses an unknown one. It
does not make a mutant generator (Q8). It does not certify any Linux bench for a walled
leg until §2 rule 7's list is closed.
