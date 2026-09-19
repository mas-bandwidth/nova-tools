# Mechanical tool work — specification (draft 6, 2026-09-19: five corrections the day measured)

Glenn, 2026-09-19: *"I want to upgrade our tools so we can push more work to swarms
mechanically."*

This document specifies the seven enablers that let a swarm do work **on these tools
themselves** with no person and no model standing between a card's result and the
decision to keep it. It is an **amendment document**: every section names the existing
rule it builds on by `file:line` at `dev@31e35195` (the cited files are byte-identical at `11aa07a7`, where the triage this builds on was cut), says what that rule does not yet
hold, and adds the rules that close the gap. It is one new file on purpose
([WORKER-CARDS.md](WORKER-CARDS.md) practice 26, `docs/WORKER-CARDS.md:370`: parallel
amendments that touch one file all conflict; 54 of 114 open pull requests conflicted with
`dev` on the day this was written). Each amended spec carries a short pointer here at
the amended rule; the implementation card for a section moves the normative text home
into that spec when it lands, and this file shrinks to the index.

This spec is normative. Where the code and this document disagree, one of them has a bug
and the tests decide which. [SPEC.md](SPEC.md)'s **Conventions** — exit codes, no guessed
paths, the one-line grammar, the cap-and-count rule — apply unchanged and are not
restated. **No code lands with this document**; the work list at the end is the order.

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

## 1. The accept gate — mechanical accept or reject, with a negative control

**Builds on.** `harvest` is gated on the verdict and never on mergeability
(`docs/SPEC-PULSE.md:194-199`); line 1 must match or nothing is pushed
(`docs/SPEC-PULSE.md:200-218`); `gather` scores a card `done` on line 1 alone, whatever
the exit code (`docs/SPEC-SWARM.md:1470`), which is why line 1 never carries the answer
(`docs/WORKER-CARDS.md:310-321`); a fix card names its reproducing test and the manager
refuses a fix PR that carries neither the `red:` line nor the test file
(`docs/WORKER-CARDS.md:323-340`); `nova-review mutate` reverts the change, keeps the
tests and demands they go red (`docs/SPEC-REVIEW.md:643-660`, grammar `:818-824`, exits
`:787-789`); the card is a three-call pipeline whose tools the harness runs
(`docs/SPEC-SWARM.md:771-838`).

**What those rules do not hold.** Every one of them reads what the worker **said**. The
contract line proves which card this is. Line 2 is the worker's own verdict. The `red:`
line is a pasted string. Between the worker's word and an open pull request there is no
step that executes the claim, so the first execution of a swarm's claim is a friend's
read — the most expensive reader in the house doing the most mechanical check in it.

**The verb.** `nova-pulse gate` is taken (it is the dev-red STOP gate,
`docs/SPEC-PULSE.md:1443-1446`), so this is `accept`:

```
nova-pulse accept --job <dir> --card <path> --base <ref> --bench <name> --cert <path> [--timeout <seconds>] [--max <n>]
nova-pulse accept --selftest --fixtures <dir> --bench <name> --cert <path> [--timeout <seconds>]
```

```
ACCEPT OK      label=<label> kind=<kind> head=<sha12> base=<sha12> tests=<n> red_without=<n> edits=<n|-> control=<id> bench=<name> cert=<id> took=<d>
ACCEPT REJECT  label=<label> kind=<kind> head=<sha12|-> reason=<token> at=<path[:line]|test|-> control=<id> bench=<name> cert=<id> took=<d>
ACCEPT ABSTAIN label=<label> kind=<kind> reason=<bench-uncertified|paused|unknown-kind|toolchain|base-red|control-stale|control-red|timeout> bench=<name> took=<d>
ACCEPT SELFTEST control=<id> accepted=<n>/<n> rejected=<n>/<n> edits=1 build=<build identity> fixtures=<sha12> bench=<name> <PASS|FAIL>
ACCEPT SEED    name=<seed> edits=<n> want=<token> got=<token|ACCEPT> <ok|WRONG>
ACCEPT REFUSED: <reason> (<remedy>)
```

Exit 0 is `ACCEPT OK` or a passing selftest; 1 is the verb saying NO (`REJECT`, a
failing selftest); 2 is could-not-run, which includes `ABSTAIN` and `REFUSED`.

1. **The gate is `harvest`'s default step, before any push.** For every `done` card
   whose kind declares a gate (§5), `harvest` runs `accept` after rule 12's line-1
   verify and **before** the push of rule 12 and the read card of rule 13. `ACCEPT OK`
   pushes and opens the PR as today, with the `ACCEPT OK` line as the first line of the
   PR body. `REJECT` pushes nothing, writes the card's row in `seen.tsv` as `rejected`,
   counts `rejected=<n>` on `HARVEST OK`, and sends the card down rule 14's requeue-once
   path with the reason token as the requeue's evidence. `ABSTAIN` pushes nothing and
   requeues nothing: it is the bench's fault, and it is one line in `<root>/bench.tsv`
   for a person. The one abstain that is nobody's fault is `reason=paused`: the
   coordinator paused the kind after the card was launched (the eligibility rule, 4);
   `harvest` re-reads the trust state and produces it, it writes no `bench.tsv` row, it
   requeues nothing (the remedy is `trust --set trial`, not a rerun), and for the track
   record it counts as neither a pass nor a fail, like every `ABSTAIN` (the eligibility
   rule, 1). There is no `--no-gate`. A kind whose declared gate is `none` (reads,
   probes) skips the step and its `HARVEST` row says `gate=none`, so a green row never
   claims a check that did not run.
2. **The gate reads the card and the commit, never the report.** Its inputs are the
   card file `cut` wrote (whose typed header lines are the card writer's), the job's git
   objects, and `--base`. `RESULT.md` is data and is not opened by `accept` at all (its
   line 1 is `verify`'s, one step earlier). This keeps `docs/SPEC-SWARM.md:2470` true — *"`nova-swarm` does not
   read a worker's `RESULT.md` and act on it"* — and means a worker cannot name its own
   gate, widen its own paths or supply its own seed. The gate's commands come from the
   kind's declaration in the tool, parameterised only by the card's `TEST:` and
   `PATHS:` lines, each of which is validated as a test name or a repo-relative glob
   before use and never passed through a shell.
3. **The gate runs in its own tree, inside the wall, on a certified bench.** `accept`
   makes a throwaway worktree of the job's head under `--job`'s slot, runs every
   command through `nova-sandbox` with the read and write lists of
   `docs/SPEC-SWARM.md:2245-2275`, and removes the tree on every path. It never runs in
   the worker's own working copy: an untracked file the worker left behind must not be
   able to turn a test green. `--cert` names the bench's certification record (§2); a
   missing, stale or failing record is `ACCEPT ABSTAIN reason=bench-uncertified`.
4. **The order, and the first failure decides.** (a) §3's hygiene checks: `identity`,
   `stray-file`, `secret`, `out-of-path`. (b) The kind's shape check,
   where the kind declares one (§5): the named test exists at head
   (`named-test-missing`); the kind changed at least one test file (`no-test`). (b2)
   **The base's tests survive** (the eligibility rule, 11): every `Test` function present
   at the base in a package the card touched is still present at head and has gained no
   skip, and the base's copy of every pre-existing test file the card changed, overlaid
   on the head, passes — else `test-weakened at=<test>`. A pre-existing test body the
   card modified under `TEST-EDIT:` is listed on the PR for the reader, by name, because
   that is judgment the gate does not have. (c) Positive: build, vet, and the packages of every
   changed file test green at head (`build`, `vet`, `red-at-head`, each with the first
   failing line that is not a notice). (d) **Negative control for the card**, for the kinds that declare the range form
   (§5): `nova-review mutate --repo <tree> --base <base> --head <head>` must print `PASS`.
   A `MUTATE GREEN` line **for a test the card wrote or changed** is
   `reason=vacuous-test at=<test>`: a test that is green without the change it claims
   to cover proves nothing (`docs/SPEC-REVIEW.md:653-654`), and that is now a rejection
   and not a reader's finding. A pre-existing test the card did not touch is not
   charged: `mutate` runs every `Test` in a changed file, so such a test is `MUTATE
   GREEN` under the revert on every ordinary fix card, and the letter of draft 5
   ("a `MUTATE GREEN` line is `vacuous-test`") rejected the known-good fix (measured
   2026-09-19, gate lane PR #1721, negative control B: `vacuous-test at=TestSign` on the
   fixture's untouched test; `TestAcceptDoesNotCallAPreExistingGreenVacuous` pins the
   intent). The card's named `TEST:` must be among the tests that went red
   (`named-test-not-red`). (e) The kind's own control, where mutate's revert is not the
   right defect (§5: `transcript-test`, `mutation-kill`, `sweep`).
5. **A red test is a finding, never a rerun.** `accept` runs each command once. A test
   red at head is `REJECT reason=red-at-head`; a second run to see whether it goes
   green is forbidden, because a gate that reruns until green accepts every flaky fix.
   A test red at head that the card neither changed nor named is run once **at the base**:
   red there too is `ABSTAIN reason=base-red`, the base's fault and not the card's (a
   pre-existing failure is named by its failure set against the pinned base,
   `docs/WORKER-CARDS.md:103-118`).
   A red whose first non-notice line names a missing toolchain, a refused path
   (`SANDBOX DENIED`, `WALL`) or a full disk is the bench's:
   `ABSTAIN reason=toolchain`, and the certification record is marked stale (§2 rule 6).
6. **The gate's own negative control: it must be seen red before its green counts.**
   `accept --selftest` runs the gate over a fixture repository shipped in
   `cmd/nova-pulse/testdata/accept/`: one known-good fix commit, which must be
   `ACCEPT OK`, and one seeded defect per reject token of the common gate, each of which must be
   `ACCEPT REJECT` **with that token and no other** (twelve tokens, twelve seeds; the
   kind-specific tokens of §5 add their seeds with their kind; **`gate-weakened` is the
   one token the selftest does not prove** — it needs a second gate to weaken — and its
   own red test, `a-card-that-weakens-the-gate-is-rejected`, is what holds it):

   | seed | the one edit | want |
   |---|---|---|
   | `fix-reverted` | the fix's one changed line put back | `red-at-head` |
   | `vacuous` | the new test's assertion replaced by one that holds without the fix | `vacuous-test` |
   | `no-test` | the test hunk dropped | `no-test` |
   | `wrong-author` | the commit re-authored | `identity` |
   | `stray` | one untracked-then-added file outside `PATHS:` | `stray-file` |
   | `wide` | one line changed in a file outside `PATHS:` | `out-of-path` |
   | `secret` | one line carrying a key-shaped fixture string | `secret` |
   | `broken` | one line of the fix made a syntax error | `build` |
   | `vetted` | one format verb in the fix made wrong for its argument | `vet` |
   | `renamed` | the named test's `func` line renamed | `named-test-missing` |
   | `wrong-name` | the fixture card's `TEST:` line pointed at a test that passes without the fix | `named-test-not-red` |
   | `skipped` | one `t.Skip()` line added to a test that exists at the base | `test-weakened` |

   `ACCEPT SELFTEST … PASS` requires every row right. One wrong row is `FAIL`, exit 1,
   with its `ACCEPT SEED … WRONG` line.
7. **Every seed is one edit, and the count is asserted.** A seed is applied by
   `nova-review mutate --seed` (rule 9) and is refused unless it changes **exactly one
   line** — one `-` and one `+`, or one added line, or one removed line, or one line
   moved (the same text removed in one place and added in another); for the
   whole-object seeds exactly one object: one commit (`wrong-author`), one file (`stray`),
   one hunk (`no-test`). The
   count is printed as `edits=1` on the `ACCEPT SEED` line and asserted by the verb
   itself: a seed that changed nothing proves the gate red on nothing, and a seed that
   changed two things does not say which one the gate caught. `edits=0` and `edits>1`
   are `ACCEPT REFUSED: seed <name> made <n> edits, want exactly 1`, exit 2.
8. **The control is an id, and a green without it does not count.** `control=<id>` is
   the first twelve hex of the SHA-256 of the gate binary's build identity, the fixture
   tree's digest and the bench certification id. `accept` refuses to print `ACCEPT OK`
   unless a passing `ACCEPT SELFTEST` for the **same id** is on file under
   `<root>/accept/control/<id>`: it runs the selftest itself when none is
   (`control-red` if it fails). A new build of the tool, a changed fixture or a
   re-certified bench is a new id and a new selftest. `harvest` copies `control=` onto
   the PR body; `nova-merge batch` (§6) and a reader can ask for it; an `ACCEPT OK`
   with no control on file is `control-stale` and is treated as `ABSTAIN`.
9. **`nova-review mutate` grows the seed form, and says how much it reverted.**

   ```
   nova-review mutate --repo <dir> --head <ref> --seed <patch file> --tests <package>[,<package>...] [--timeout <seconds>]

   MUTATE <head8> seed=<sha8> edits=1 red=<n> green=<n> <PASS|FAIL>
   MUTATE REFUSED: seed makes <n> edits, want exactly 1
   ```

   The seed is a unified diff applied in the throwaway worktree with `git apply`; the
   verb counts changed lines from the patch it applied, never from the patch file's own
   header. The range form gains `reverted=<n>` (hunks put back) on its verdict line, so
   *"every non-test hunk"* (`docs/SPEC-REVIEW.md:647`) is a number a caller can gate on.
   `mutate` still records nothing and writes nothing into the repo it is pointed at.
10. **The gate makes no model call and no network call.** It reads no forge, asks no
    provider and never consults `--decide`: Jev's harvest classification (§4) runs
    **after** the gate, over its typed line, and can only route a result — it can never
    turn a `REJECT` into a push. (Floors: `docs/SPEC-DECIDE.md` rule 5, *"a decision
    below the floor is a suggestion"*; a decision above the floor is still not an
    acceptance.)

**Red tests this section demands**, each seen red first, fakes for the bench and the
clock, no network: `accept-runs-before-any-push` (the git fixture's argv log holds no
push for a rejected card); `accept-never-opens-result-md` (a `RESULT.md` that is a FIFO
does not hang the gate); `accept-rejects-a-vacuous-test`;
`a-pre-existing-green-test-is-not-vacuous` (rule 4(d): an untouched test `MUTATE GREEN`
under the revert on a known-good fix is not charged; PR #1721's
`TestAcceptDoesNotCallAPreExistingGreenVacuous`); `accept-rejects-when-named-test-stays-green`;
`accept-never-reruns-a-red`; `accept-abstains-on-a-bench-red` (a `WALL` line is the
bench's); `selftest-every-seed-is-one-edit` (a two-line seed is refused by count);
`selftest-wrong-token-is-a-fail`; `ok-without-a-control-on-file-is-refused`;
`control-id-changes-with-the-build`; `mutate-seed-refuses-two-edits`;
`mutate-prints-reverted-count`; `harvest-row-says-gate-none-for-a-read`;
`an-untracked-file-in-the-workers-copy-cannot-turn-the-gate-green` (rule 3);
`a-red-the-card-did-not-touch-is-run-once-at-base` and `base-red-is-an-abstain` (rule 5);
`accept-makes-no-network-call` (rule 10: the run is wrapped with the network denied and
still passes); `a-deleted-base-test-is-test-weakened`; `a-skip-added-to-a-base-test-is-test-weakened`;
`a-test-edit-body-is-listed-for-the-reader`.

## 2. A wall that can build this repository, and a bench that is certified to

**Builds on.** Every job runs inside `nova-sandbox`, argv built by the dispatcher and
never from the task text (`docs/SPEC-SWARM.md:2245-2275`); `--go` puts `GOROOT` and
`GOMODCACHE` in the read set as `go env` reports them (`docs/SPEC-SANDBOX.md:597-612`);
a toolchain under a user directory is named with `--read`, and *"a command that runs
outside the wall and dies inside it is missing a `--read`"*
(`docs/SPEC-SANDBOX.md:1195-1210`), which already names the `xcode_select_link` shim;
`tools/bench-standard.sh` checks a Linux bench's `go version` and `sbcl` on `PATH`
(`docs/SPEC-PULSE.md:384-392`); every launcher takes a bench slot lease
(`docs/SPEC-SWARM.md:2103-2147`).

**What those rules do not hold.** They say how to let a toolchain through the wall; none
says **which toolchains this repository's own gate needs**, none proves a bench can run
that gate **inside** the wall, and nothing ties a card's result to a bench that was ever
shown able to produce one. The receipts: `/usr/bin/cc` and `/usr/bin/c++` fail inside
the wall on macOS because the profile denies `/var/db/xcode_select_link`, which also
kills every `make` target at parse time (#1557); `native` shares the Go caches but never
reads the Go toolchain, so a Go card printed `rc=0 harness=ok` having compiled nothing
(#1465); the harness fence ignores `read_roots` (#1463); on Linux the wall refuses
`/tmp`, which dotnet hard-codes (#1495), `policy` prints the darwin profile under
`backend=landlock` (#1469), and the nova-sandbox and nova-swarm transcripts cannot be
reproduced on a Linux bench at all (#1509). **nova-sandbox on Linux is not clean, and
until rule 7's list is closed no Linux bench can be certified for a wall-dependent
leg.**

1. **The repository declares its legs, as data.** `tools/legs.tsv` — `leg`, `probe
   command`, `wants` — one row per toolchain this repository's gate uses:

   | leg | probe (run inside the wall, in a scratch clone) | wants |
   |---|---|---|
   | `go` | `go build ./... && go vet ./...` | exit 0; `go version` equals `go.mod`'s |
   | `go-cross` | `GOOS=windows go vet ./...` | exit 0 |
   | `cc` | compile and run a ten-line C file with `cc` | exit 0, the file's one line |
   | `make` | `make -n test` | exit 0 (parses; #1557's parse-time `$(CC)`) |
   | `sbcl` | `lisp/nova-work/run-tests.sh` on a fresh isolated ASDF cache | its `total=<n> pass=<n> fail=0` line |
   | `sqlite3` | open, write and read one row in the write set | the row |
   | `git` | clone from the bench mirror, commit, `git worktree add` | exit 0 |
   | `tmp` | `mkdtemp()` under `$TMPDIR` **and** under `/tmp` | both succeed inside the write set (#1495) |

   A leg is added by adding a row, and a class test asserts every toolchain `ci.yml`
   installs has a row.
2. **`nova-sandbox` grows a named toolchain set, not a wider wall.** `--toolchain
   <leg>[,<leg>...]` adds, per leg and per platform, the narrowest roots that leg was
   measured to need, asked of the toolchain the way `--go` asks `go env` and never
   guessed. **The roots, as measured** (2026-09-19, the wall lane, on this Studio: macOS
   26.6.2, `go1.27.1 darwin/arm64`, `backend=sandbox-exec`; logs `t18-measure.log`,
   `t18-legs.log`, `t18-fix.log`, `t18-fix2.log`, `t18-narrow.log` in that lane's
   scratchpad, summarised in its HANDOFF):
   - `cc` on darwin: **draft 5's fix was not sufficient.** `DEVELOPER_DIR` set from
     `xcode-select -p` plus read on that directory still dies: `cc` fails on `couldn't
     stat Xcode's Info.plist (errno=Operation not permitted)` and dyld refuses
     `DVTSystemPrerequisites.framework … (blocked by sandbox)`, both **above** `Developer`
     in the `.app`'s `Contents/`; `cc_rc=71`. The narrowest roots that work are, in this
     order: `DEVELOPER_DIR=/Library/Developer/CommandLineTools` with `--read` on it, when
     the Command Line Tools are installed (silent, and far narrower than an `.app`); else
     `--read` on the whole `.app` that `xcode-select -p` names (noisy). `--toolchain cc`
     probes for the first and falls to the second, and prints which on `toolchain=`. The
     rule's earlier claim that the cargo workaround on #1557 proved the `Developer`-only
     read is withdrawn: it was not measured inside this wall.
   - `go`: `--read $(go env GOROOT)` and `--read-noexec $(go env GOMODCACHE)`, and no
     other root: on this Studio `GOROOT` is `/opt/homebrew/Cellar/go/1.27.1/libexec`
     (`t18-narrow.log:2`), so what is read under `/opt/homebrew` is `GOROOT` itself, asked
     of `go env`, and not the Cellar tree. With those two roots `go test
     ./internal/sandbox/` inside the wall is `ok` on a cold cache (`t18-measure.log`).
     What `dev` carries today is **wider**: `swarm.ToolchainRoots`
     (`internal/swarm/toolchain.go:150-165`) grants `~/sdk`, `/opt/homebrew/Cellar/go`,
     `/opt/homebrew/Cellar/sbcl`, `openjdk`, the JVMs and `dotnet`, all with exec, on
     every `native` run; T18 (#1662) narrows `native`'s default to the two measured roots.
   - `sbcl`: `--read` on its Cellar prefix alone (`/opt/homebrew/Cellar/sbcl/<version>`;
     `SBCL_HOME` and the core live under it).
   - `sqlite3`: no extra root at all.
   - `make`: **unmeasured under the working roots.** It dies with `cc` under the failing
     ones (`xcode-select: error: unable to read data link at '/var/db/xcode_select_link'`,
     `t18-fix.log`, #1557); whether it lives under `cc`'s working roots was not run, so
     `certify`'s `make` leg starts `absent` until it is.
   A wall widening is a judgment and not a measurement: the `cc` roots above are wider
   than draft 5 promised, and Stella's word on them is owed before T18 writes the flag.
   Each resolved root is printed on the `SANDBOX OK` line's `toolchain=` field so the
   wall a card ran behind is a fact on its record. An unknown leg is `SANDBOX REFUSED
   reason=bad_toolchain`. This is a sandbox change, a security kind: the unit and its read are the designated
   mind's (the eligibility rule, 13).
3. **`native` and `accept` pass the card's legs to the wall.** A card's `LEGS:` line
   (§5) names what its gate needs; the dispatcher turns it into `--toolchain`, and the
   harness fence is given the same roots (#1463) so the two walls agree. A card with no
   `LEGS:` line gets `go`, which closes #1465 as the default rather than as a flag
   somebody remembers.
4. **`certify` proves a bench, leg by leg, inside the wall.**

   ```
   nova-pulse certify --bench <name> --legs <tools/legs.tsv> --root <dir> --out <cert file> [--timeout <seconds>]

   CERTIFY LEG  bench=<name> leg=<leg> <ok|FAIL|absent> wall=<backend> took=<d> [first=<first line that is not a notice>]
   CERTIFY OK   bench=<name> cert=<id> legs=<ok list> failed=<list|none> absent=<list|none> wall=<backend> build=<build identity> at=<stamp> until=<stamp>
   ```

   Every probe runs through `nova-sandbox --toolchain <leg>` exactly as a card's gate
   will, so a leg is certified **inside the wall or not at all**. The record is one file,
   `cert=<id>` the first twelve hex of its SHA-256; it names the tool build, the wall
   backend, the host, and the legs that passed. `absent` (the toolchain is not
   installed) is not a failure of the bench, only a leg it cannot be routed.
5. **A result is trusted only from a bench certified for the card's legs.** The result
   that is trusted is the **gate's**, never the worker's (§1 rule 2), so the record that
   decides is the one for the bench `accept` runs on: `accept` refuses to gate, and
   `harvest` refuses to count, a card whose `LEGS:` are not all in the `legs=` list of a
   live certification for that bench — `ACCEPT ABSTAIN reason=bench-uncertified`.
   Routing reads the same records for the worker's side: a card is never sent to a bench
   not certified for its legs, because a worker that cannot run its own test comes back
   `BLOCKED` and the card was paid for nothing (the route's `outcome` records the
   refusal). *Measure a toolchain before routing a card there* becomes a file the router
   reads.
6. **A certification expires, and a bench red kills it.** `until=` is 24 hours. It is
   also void the moment the tool build changes (`nova-update adopt`), the host's
   toolchain versions change (`go version`, `sbcl --version`, `cc --version` are in the
   record), or any `accept` on that bench abstains with `reason=toolchain`. A void
   record is re-run, never edited.
7. **What Linux needs before any Linux bench is certified for a walled leg** — each an
   existing issue, in this order: #1495 (a private writable `/tmp` inside the Landlock
   policy, with a class test that `mkdtemp()`s under `/tmp` inside the wall); #1469
   (`policy` prints the Landlock rule set it will apply, not the darwin template);
   #1509 (the transcripts name their platform, §7 rule 5); then rule 2's `--toolchain`
   on Landlock, where read roots are inherited across `fork(2)` and cannot be widened
   after the wrap. Until then a Linux bench certifies with `wall=none` legs only, its
   record says so, and `accept` treats `wall=none` as uncertified for any card that
   changes code. UDP is unrestricted under Landlock at every ABI
   (`docs/SPEC-SANDBOX.md:1559`); `certify` prints that as a note and it is not a leg.

**Red tests:** `certify-runs-every-probe-inside-the-wall` (the sandbox fake's argv log
holds one wrap per leg); `certify-absent-is-not-failed`; `cert-voids-on-build-change`;
`accept-abstains-on-an-uncertified-leg`; `route-refuses-a-bench-without-the-leg`;
`toolchain-cc-prefers-command-line-tools-and-reads-nothing-wider` (darwin: with
`/Library/Developer/CommandLineTools` present the wall's argv holds `--read` on it and no
`.app`; `toolchain=` names it); `toolchain-cc-falls-back-to-the-app-and-prints-which` (darwin:
with the Command Line Tools absent the argv holds `--read` on the `.app` that `xcode-select
-p` names, and `toolchain=` says so); `native-reads-goroot-and-gomodcache-and-nothing-else`
(the argv holds exactly those two roots, neither with exec beyond `GOROOT`);
`legs-tsv-covers-every-toolchain-ci-installs`; `native-defaults-to-the-go-leg`;
`an-unknown-leg-is-bad-toolchain` (rule 2); `a-cert-past-until-is-void`,
`a-toolchain-abstain-voids-the-cert` and `a-changed-go-version-voids-the-cert` (rule 6);
`wall-none-is-uncertified-for-a-code-card` (rule 7).

## 3. Identity and hygiene, at staging and at harvest

**Builds on.** The card ends at the commit and publication is the launcher's
(`docs/WORKER-CARDS.md:129-135`); `harvest` pushes by explicit refspec, never bare and
never to `main` (`docs/SPEC-PULSE.md:203-206`), with a lease from `ls-remote` and a
refusal of any branch off `rowan/*` (`docs/SPEC-PULSE.md:2049-2051`); the key is in
neither of the wall's lists (`docs/SPEC-SWARM.md:2268`); the work is anchored to named
files (`files-named`, `docs/WORKER-CARDS.md:28`).

**What those rules do not hold.** Nothing says whose name a card's commit carries, so
it carries whatever the bench's git config held; nothing looks at **what** is in the
commit beyond its branch name; and `files-named` is checked on the card's text at lint
and never on the diff that comes back.

**Staging** is what the launcher puts in a job directory before the worker starts.
**Harvest** is what `accept` checks before anything leaves it. Both halves are
mechanical, and the harvest half never trusts that the staging half ran.

1. **Staging sets the identity; the worker never does.** The launcher writes the job
   clone's **local** git config — `user.name`, `user.email`, `commit.gpgsign=false`,
   `core.hooksPath=/dev/null` — from the pool's `identity.tsv` (`owner`, `name`,
   `email`), and exports `GIT_CONFIG_GLOBAL=/dev/null` and `GIT_CONFIG_NOSYSTEM=1` so a
   bench's own config cannot leak in. A pool with no identity row is refused at launch.
2. **Staging leaves no way out of the job root.** No absolute symlink and no symlink
   resolving outside the job root exists in a staged tree (#1557's `repo/dist ->
   /Users/…` cost four legs a toolchain); toolchains are real directories or
   copy-on-write clones. The launcher checks this before the first worker starts and
   refuses by path.
3. **`identity`.** Every commit in `<base>..<head>` has author **and** committer equal
   to the pool's identity row, and none is a merge commit. Anything else is
   `reason=identity at=<sha12>`.
4. **`out-of-path`: the diff is bounded to the card's declared paths.** The card's
   `PATHS:` line (§5) is a list of repo-relative globs, validated at `cut`: no `..`, no
   absolute path, no bare `**`, at most 8 entries. Every path in `git diff --name-only
   <base>..<head>` matches one. A rename counts on both sides.
5. **`stray-file`.** No added file matches the stray list — `RESULT.md`, `PROMPT.md`,
   `scratch/**`, `*.log`, `*.orig`, `*.rej`, `*.test`, `*.out`, `.DS_Store`, editor
   swap files, anything over 1 MiB, any file whose mode is not `100644` or `100755`,
   any symlink, any submodule — and the worktree after the gate's checkout holds no
   conflict marker in a changed file (`git diff --check`; card-16 left `<<<<<<< HEAD`
   in a fenced block, `docs/WORKER-CARDS.md:112-113`). The list is one data file, and
   an allowlisted exception names the card kind it is for.
6. **`secret`.** Every **added line** is matched against a list of key SHAPES held as one
   data file — PEM private-key headers, `AGE-SECRET-KEY-1`, the forge's token prefixes,
   the provider key prefixes this fleet holds keys for — and never against a key's
   value: the gate holds no key and reads none (no shape matcher exists in the tree at
   `31e35195`; `internal/swarm/key.go:118` redacts an error's text and is the nearest
   thing). A match is `reason=secret at=<path>:<line>` and **the matched
   text is never printed**, only its path, line and shape name. The job directory is
   then quarantined — moved to `<root>/quarantine/<label>`, not harvested, not deleted
   — and one `HUMAN` line is written, because a key in a worker's diff means a key
   reached a worker. The fixture strings the selftest seeds are generated at test time
   and are not valid keys for any provider.
7. **The checks are one package with one entry point**, `internal/hygiene.Check(repo,
   base, head, paths, identity) []Finding`, called by `accept`, by `nova-merge batch`
   on each member (§6 rule 7) and by a `nova-check hygiene` verb a person can run on a
   branch before asking for a read. One implementation, three callers, so the lane and
   the harvest cannot disagree about what clean means. `identity` is a set and
   `paths` may be absent: at harvest the set is the pool's one row and `paths` is the
   card's; at `batch` the set is the lane's `identities.tsv` — every friend who commits
   to this repository — and a member that is not a swarm's has no `PATHS:`, so
   `out-of-path` is skipped for it and says so (`paths=-`), while `identity`,
   `stray-file`, `secret` and the conflict-marker check run on every member.

**Red tests:** `launch-refuses-a-pool-with-no-identity`;
`staged-clone-ignores-the-bench-gitconfig`; `stage-refuses-a-symlink-out-of-the-job`;
`hygiene-rejects-a-foreign-committer`; `hygiene-rejects-a-merge-commit`;
`hygiene-counts-a-rename-on-both-sides`; `hygiene-rejects-result-md-in-the-diff`;
`hygiene-never-prints-the-secret` (the output is searched for the fixture string);
`secret-quarantines-and-never-deletes`; `paths-line-refuses-dotdot-and-bare-doublestar`;
`hygiene-rejects-a-conflict-marker`, `hygiene-rejects-mode-100600`,
`hygiene-rejects-a-file-over-one-mebibyte`, `hygiene-rejects-a-symlink-and-a-submodule`
(rule 5).

## 4. Typed results, and Jev's harvest classification

**Builds on.** Line 2 is one of `DONE`, `ABSTAIN <why>`, `BLOCKED <why>`
(`docs/spec-pulse/10-the-card-as-cut-writes-it.md:8`); every abstain names one reason
token so the packet is the whole read (`docs/SPEC-SWARM.md:1481-1503`); the working
layout's five harvest classes are *"the typed decision behind the floor"*
(`docs/SPEC-PULSE.md:2045`); SPEC-DECIDE's **nova-pulse harvest class** asks one
`choice` over {fixed, already-fixed, no-change, failed, off-branch} over bounded public
state (`docs/SPEC-DECIDE.md:407-417`).

**The Jev lane owns the SPEC-DECIDE amendment** (the 2026-09-19 Jev integration lane,
umbrella #896). This section does not restate or change it. It fixes only the
**boundary**: what is typed by the gate and therefore never asked of Jev, and what Jev
is handed.

1. **The result of a card is one typed line, written by the machinery.** After
   `accept`, `harvest` writes `<job>/OUTCOME` — one line, and the only thing any later
   step reads about the card:

   ```
   OUTCOME label=<label> kind=<kind> gather=<done|reason token> accept=<ok|reject|abstain|none> reason=<token|-> class=<class|-> conf=<0-1|-> head=<sha12|-> base=<sha12> bench=<name> cert=<id|-> control=<id|-> pr=<n|-> took=<ms>
   ```

   `gather=` is SPEC-SWARM's token, `accept=`/`reason=` are §1's, `class=`/`conf=` are
   rule 3's. The worker writes none of it.
2. **What the gate decides is never asked of a model.** `accept=ok` is `class=fixed`
   and `accept=reject` is the new class **`rejected`**, with `conf=-`, and no decision call is
   made. It is not `failed`: that word already means *"the push or the read could not
   complete"* (`docs/SPEC-PULSE.md:2045`) and SPEC-DECIDE says `fixed` and `failed` *"push as
   today"* (`docs/SPEC-DECIDE.md:413`); a rejected card pushes nothing, and an implementer
   reading either sentence must not be able to conclude otherwise. The Jev lane's amendment
   adds `rejected` to the class set as a member no provider is ever asked for:
   a typed judgment over evidence a verb already settled is a paid coin-flip over a
   known answer. `no-change`, `already-fixed` and `off-branch` stay the mechanical git
   reads they are in the working layout (`docs/SPEC-PULSE.md:2045-2049`).
3. **Jev classifies what is left: the cards with no gate verdict.** `accept=abstain`,
   `accept=none` with line 2 `BLOCKED` or `ABSTAIN`, and every `gather` abstain whose
   token does not already name its remedy. The question, its option set, the state sent
   and the floor are the Jev lane's amendment; this spec requires only that (a) the
   state is built from `OUTCOME` and the reason token's bounded field, never from
   `RESULT.md` prose, which also keeps SPEC-DECIDE rule 4 (public or synthetic state
   only) true by construction; (b) the answer lands in `class=`/`conf=` on `OUTCOME`
   and on the route log's `outcome`; (c) below the floor `class=unknown` and rule 14's
   requeue-once path runs as today.
4. **A classification routes; it never accepts.** No `class`, at any confidence, turns
   `accept=reject` or `accept=abstain` into a push, lifts a HOLD, or skips a read. The
   red test is SPEC-DECIDE's own shape, `a-merge-classification-never-merges`
   (`docs/SPEC-DECIDE.md:510`), restated for harvest:
   `a-harvest-class-never-pushes-a-rejected-card`.
5. **Every `OUTCOME` is a row for tuning.** `harvest` appends it to
   `<queue>/decide/outcomes.jsonl` beside the route log, so the Jev lane measures
   agreement between `class=` and what a person later did, from rows and not from a
   feeling (`docs/SPEC-PULSE.md`, rule 10's `ROUTES.log` sentence).

**Red tests:** `outcome-is-written-by-harvest-and-never-by-the-card` (a job that ships
its own `OUTCOME` is `stray-file`); `no-decide-call-when-accept-decided` (the provider
fake sees zero requests); `decide-state-is-built-from-outcome-only`;
`a-harvest-class-never-pushes-a-rejected-card`; `below-floor-is-unknown-and-requeues-once`;
`accept-reject-is-class-rejected-never-failed`; `every-outcome-is-one-appended-jsonl-row`
(rule 5).

## 5. Card kinds for tool work, each with its declared gate and control

**Builds on.** `cut` renders six typed templates — `read`, `fix`, `text`, `replay`,
`drift`, `tone` — and refuses a rendered card that breaks the practice-17 shape
(`docs/spec-pulse/02-the-rules-numbered.md:33-51`); text-only templates forbid the
build (`:52-55`); the contract line's `sha12` binds everything below line 1
(`docs/spec-pulse/10-the-card-as-cut-writes-it.md:12-14`); the fix-card shape is three
model calls (`docs/SPEC-SWARM.md:822-830`) and a fourth without `MODE: explore` is
refused at admission (`:808-813`); `nova-swarm lint --card` checks twelve rule tokens
(`docs/WORKER-CARDS.md:23-36`); *"a template is text and nothing else"*
(`docs/SPEC-SWARM.md:2470`).

**What those rules do not hold.** A template fixes what a card **says**. Nothing fixes
what **accepting** that kind of card means, so every kind is accepted the same way — by
its line 1. A kind is a template **plus** the gate that judges it and the control that
proves the gate: the first half is text for the worker, the second is code in the tool
the worker never sees and cannot edit.

1. **A seven-line header block, then `STEP 1`; everything below line 1 inside the hash.**
   The header is the contract line, the role line, and five typed lines, in this order,
   and `STEP 1` is the line after it:

   ```
   RESULT: <label> sha=<sha12>
   You are a worker. <the role line of docs/spec-pulse/10-the-card-as-cut-writes-it.md>
   KIND: <kind>
   PATHS: <glob>[, <glob>...]
   TEST: <package> <TestName>          (or `TEST: none` where the kind allows it)
   LEGS: <leg>[,<leg>...]
   SOURCE: <owner>/<repo>#<n> | <file:line at the pinned head>
   STEP 1. <clone or cd, as the spec-pulse document writes it>
   ```

   **Why seven and why `STEP 1` next**: the shipped admission check reads the first
   fifteen lines for a line beginning `STEP 1` (`internal/swarm/batch.go:1833-1844`,
   `hasStep1`, for every DeepSeek-family model; `docs/WORKER-CARDS.md` practice 17), and
   the first card cut in draft 5's shape, with a coordinator's `BASE`, `SPEC`, `ROUTE`,
   `ACCEPT`, `CERT`, `LANE` and RULES prose above the steps, was refused before any model,
   bench or provider was reached (`ADMIT REFUSED tools10-c1 card-shape: no 'STEP 1' line
   in the first 15 lines`, `in=0 out=0 usd=0.0000`; measured 2026-09-19 by the tools10
   shift, nova-tools#1728). Everything else a card carries — `LANE`, `ACCEPT`, `CERT`,
   rules prose, the `RESULT` template — sits **below `STEP 1`**, still inside the hash,
   because the hash is of everything below line 1 and the header lines' order is free.
   **A card with more than three model steps carries `MODE: explore` and `TURNS: <n>`**,
   as header lines directly under `SOURCE:` and inside the hash, so the block is nine
   lines and `STEP 1` is line 10, still inside the fifteen: the shipped pipeline rule
   admits three model calls with no mode word (`internal/swarm/cardpipeline.go:15`,
   `pipelineModelSteps = 3`) and refuses a fourth without it, and every gated kind's
   shape in rule 2 names more than three (a `fix-red` card is eight `STEP` lines). Two
   kinds carry one more typed line under `SOURCE:` — `sweep` a `FILES: <n>` line — or a
   block below `STEP 1` — `mutation-kill` a `SEED:` block holding its one-edit patch.
   The header lines are written by `cut` from the pool row, never by a model, and because
   they sit below line 1 the contract hash covers them: a card whose header was altered
   after admission is `line1-mismatch` at `gather`. `cut` refuses a gated kind missing
   any of them (`CUT REFUSED kind=<kind>: no <LINE>`) and refuses a rendered card that
   the shipped admission and pipeline checks would refuse, and a class test,
   `a-card-cut-renders-is-admitted`, runs `cut`'s output through the admission shape
   check and the pipeline rule — the test that would have caught #1728 before a card was
   launched. `lint --card` gains the tokens `kind-declared`, `paths-declared`, `paused`
   (the coordinator paused this kind; the remedy is the `trust --set trial` command),
   `mode-declared` (more than three model steps and no `MODE: explore` / `TURNS:`) and
   `test-named`.
2. **The kinds.** `gate` is the step list of §1 rule 4; `control` is what must be seen
   red before the gate's green counts for this card.

   | kind | the card does | `PATHS:` may hold | gate | control (seeded defect, `edits=1`) | kind-specific reject tokens |
   |---|---|---|---|---|---|
   | `fix-red` | fixes one defect, red test first (WORKER-CARDS 4 and 23; SPEC-SWARM P7) | the named source and test files | hygiene, shape, positive, mutate | the change reverted: `nova-review mutate` range form `PASS`, and `TEST:` is among the tests that went red | `no-test`, `vacuous-test`, `named-test-not-red` |
   | `transcript-test` | makes one `docs/TESTS.md` section executed line for line (§7) | `cmd/<tool>/firstrun_test.go`, `cmd/<tool>/testdata/firstrun/**` — **never `docs/TESTS.md`**, and never the rest of `testdata/` (for nova-pulse that holds the gate's own fixtures, `testdata/accept/`) | hygiene, shape, positive | three seeds applied to a **copy** of the tool's section, each one edit: one line dropped, one value altered, one line moved; the new test goes red on each | `doc-edited`, `transcript-not-read` (a seed stayed green: the test does not read the document) |
   | `rebase` | replays one of our own open PRs onto the base, changing nothing | the PR's own changed files, computed by `cut` from the PR at its pinned head | hygiene, positive, and **range equality**: `git range-diff` pairs every commit, and each pair's patch-id is equal except in files git reported conflicted during the replay; those files are exempt from equality, so they are **listed by name on the read card** and are what the reader reads | one line changed in a file that did **not** conflict: the gate rejects `rebase-drift` | `rebase-drift`, `commit-dropped`, `commit-added` |
   | `sweep` | applies one mechanical class fix at every site the class test names | up to 8 globs, plus `FILES: <n>`, the most files the diff may touch | hygiene, positive, and the seed form **only**: the class test landed first and the sweep changes no test file, so the shape check would say `no-test` and range `mutate` would exit 2 `no-tests-changed` — neither is declared for this kind | **two** seeds, each one edit: the first and the last changed site (path order) reverted alone; the class test in `TEST:` goes red **naming that site** — a class test that samples is found out | `site-not-seen`, `over-files` |
   | `mutation-kill` | writes the test that kills one surviving mutant | test files only | hygiene, shape, positive, and the diff is test-only | the card's own `SEED:` patch (the mutant, written by the card writer into the card, `edits=1` asserted at `cut`) applied with `mutate --seed`: the new test is red with it and green without | `mutant-survives`, `non-test-change` |
   | `read`, `probe`, `text`, `tone` | read and report | none: `PATHS: none` | `none` | none; the `HARVEST` row says `gate=none` | — |

3. **A kind's gate is declared in the tool, in one table, and printed.**
   `internal/pulse/kinds.go` holds the table above as data; `nova-pulse accept --kinds`
   prints it, one line per kind, and a class test asserts this section's table and
   that output name the same kinds, steps and tokens. A kind the table does not hold is
   refused by `cut` and abstained by `accept` with `reason=unknown-kind` (the token the
   gate lane found the §1 grammar had no name for, PR #1721 departure 5; like `paused`
   it is nobody's bench's fault, writes no `bench.tsv` row, requeues nothing and counts
   for the track record as neither); there is no default kind.
4. **What makes a card of a kind eligible for a swarm.** All of: both conditions of the
   eligibility rule hold for its kind over its `PATHS:`; its `SOURCE:` names an issue or a `file:line` the card writer opened at
   the pinned head (WORKER-CARDS 3); its `TEST:` either exists at the pinned head
   (`transcript-test`'s target section, `sweep`'s class test) or is a name the card
   fixes in advance; its `LEGS:` are certified on at least one bench (§2 rule 5); and it
   fits the three-call pipeline or says `MODE: explore` with a `TURNS:` budget, as header
   lines inside the hash (rule 1). `cut`
   checks the first, third and fourth; the second is the card writer's, by practice 3.
5. **One card of a new template runs alone before the batch widens.** `launch` refuses
   a batch wider than one for a `(kind, template sha12)` pair with no `ACCEPT OK` on
   file in `<root>/accept/first.tsv`: `PULSE REFUSED: no accepted first card for
   kind=<kind> template=<sha12> (launch one card first)`. One wrong template line goes
   out on every card; the first card is how it goes out on one. A first card that is
   `REJECT` or `ABSTAIN` leaves the pair unproven. This is about the template, and it
   holds for a trusted kind as much as for one on trial.
6. **A kind never widens itself.** The templates stay text (`docs/SPEC-SWARM.md:2470`);
   the gate is chosen by `KIND:` from the tool's table and by nothing the worker wrote.
   A `RESULT.md` that names a different kind, a different test or more paths is not
   read (§1 rule 2), so it changes nothing.
7. **The contract line has one form, `RESULT: <label> sha=<sha12>`, with the colon: it is
   SPEC-SWARM's, and the writers follow it.** Read on `dev@702b0133`, the two forms and who
   holds each. **Colon**: `docs/SPEC-SWARM.md:969,976` (the swarm's own law: *"line 1 starts
   with `RESULT: `"*); `internal/pulse/cutkind.go:189-201` (`cut --kind`, wired at
   `cmd/nova-pulse/cut_kind.go:48` and `wire.go:502`) and `internal/pulse/manager.go:739`,
   which write `RESULT: CARD-<n> …`; `nova-swarm lint --card`'s `result-first`
   (`cmd/nova-swarm/lint.go:55,140`); `internal/worklang/expand.go:227`. **No colon**: the
   plain `cut` templates (`internal/pulse/cut.go:472` writes, `:433` checks,
   `cut_test.go:110` asserts) and, before this draft, `docs/spec-pulse/10:4` and
   `docs/WORKER-CARDS.md:312`. **Both accepted**: `internal/pulse/harvestbench.go:349`,
   `worklang/expand.go:227`, and `gather`, which compares line 1 for equality with no prefix
   of its own. Ruled (Rowan, 2026-09-19, on the coordinator's cold read of #1759): **the
   colon form wins**, because it is SPEC-SWARM's and the majority of writers already write
   it; the no-colon rule this draft first proposed would have made every `cut --kind` and
   manager card a `result-first` drift. The one renderer that changes is the plain `cut`
   template path: **the follow-up card** rewrites `cut.go:472` and `:433`, `cut_test.go:110`,
   and adds the class test `every-writer-and-reader-agrees-on-the-result-line`, which reads
   every writer's prefix (`cut.go`, `cutkind.go`, `manager.go`, `expand.go`), every reader's
   (`lint.go`, `harvestbench.go`, `gather`) and both documents' examples, and fails when they
   are not one string. This draft fixes the two documents now (`spec-pulse/10:4`,
   `WORKER-CARDS.md:25,312`). **Until that card lands, readers accept both forms**
   (`harvestbench.go:349`'s loop is the shape; the lint's accept-both stopgap in PR #1733 is
   the same shape and is retired by the class test, not before). The six hand-written cards
   of 2026-09-19 that drew `result-first` (nova-tools#1741) were in the no-colon form and
   were wrong; the lint was right.

**Red tests:** `cut-refuses-a-gated-kind-without-paths`; `header-lines-are-inside-the-contract-hash`;
`a-card-cut-renders-is-admitted` (rule 1); `the-contract-line-has-one-form` (rule 7);
`kinds-table-matches-the-spec`; `transcript-test-rejects-an-edit-to-the-document`;
`rebase-rejects-one-changed-line-outside-a-conflict`; `sweep-control-names-the-reverted-site`;
`mutation-kill-rejects-a-test-the-mutant-survives`; `launch-refuses-a-wide-batch-with-no-accepted-first-card`;
`accept-abstains-on-an-unknown-kind`.

## 6. Lanes and landing that refuse a held member, mechanically

**Builds on.** *"A hold blocks, and nothing outvotes it"*; per `(who, head)` the newest
`at` wins and a tie folds hold-last (`docs/SPEC-MERGE.md:820-827`); a read is recorded
by the reader, with the verb, for the head the reader read, and a verdict whose head is
not the entry's current `oid` is kept, counted `stale` and authorizes nothing
(`docs/SPEC-MERGE.md:286-304`); an approve from the author is not a read, and `who` is
*"a line at a keyboard"*, never a host login (`docs/SPEC-MERGE.md:828-837`); nothing
reaches the merge queue but a batch, and `land` re-reads the PR from the forge
(`docs/SPEC-MERGE.md:1540-1561`); `batch` drops a member with no green `ci-ok` before
the merge (`docs/CLI.md:1213-1258`).

**What those rules do not hold — #1572.** The read condition is held by `run`, over the
lane's **records**. `batch` and `land` — the only road to `dev` since 2026-09-18 — read
`MERGEABLE` and `ci-ok` and **no disposition at all**, and on this repository a friend's
disposition is a pull-request **comment**, posted through one shared login, in prose
(`Stella … Exact head f3e2426d… **HOLD: live-owner exclusion is still lost.**`, PR
#1430). At 2026-09-19T02:34:25Z a scoped HOLD was posted on #1551 two minutes after
`BATCH OK`; at 02:43:03Z the held head was on `dev`, through a gate that checked
everything it was specified to check. Three more were one command from the same end on
the day of the triage: #1430 and #1588 (green, MERGEABLE, HOLD at the exact head) and
#1479 (green, MERGEABLE, approved, and does not compile on `dev`).

**The fold is specified once, in `docs/SPEC-DECIDE.md`, reading 3, *The hold check: no held
head is batched or landed* (#1627), and this section restates none of it.** Drafts 1 to 4 of
this file carried their own fold beside #1627's, and two implementations following their own
spec could have disagreed on a landing. There is now one contract, and where this file and that
reading seem to differ, that reading wins and this file has a bug. What follows names the
paragraphs of reading 3 that hold each sentence, so that an implementer of T16 opens one text.

1. **What the fold takes** is reading 3, *The inputs*: the lane's line-level APPROVE/HOLD read
   records (`nova-merge read`, and the `nova-review verdict --kind line` that writes the same
   record, `docs/SPEC-REVIEW.md:253-260`), the forge's `CHANGES_REQUESTED` reviews, and the
   comments from a login in the reviewer file. **An ABSTAIN record, a `child` record and a `card`
   record are not inputs.** The fold is over **unresolved holds**, each with its holder, its head
   and its release condition, and nothing newer masks one: a same-head or a later-head ABSTAIN
   by the same reader leaves a hold exactly where it was. The sources are a union, and a hold in
   any one of them stops the member.
2. **Whose hold it is** is reading 3, *Who holds*, and SPEC-DECIDE S6: `who` is a name in the
   lane's reviewer file (`who`, `logins`, `may-hold`) mapped through the typed `DISPOSITION who=`
   line, and `unknown` otherwise; a `who=unknown` hold holds; a login is evidence about an
   account and excuses nothing (draft 1 skipped the author's login, and where the friends and the
   author share one login that skipped every comment there was). The one comment not scanned is
   the entry author's own `verdict=NOTE` line.
3. **What adds a hold** is reading 3, *What adds a hold, by the rule table, with no call* and
   *What the decider may do, which is add*: a typed `DISPOSITION … verdict=HOLD` line, a
   first-line hold token, the word `HOLD` as a heading word or in bold, a `CHANGES_REQUESTED`
   review, a decided `hold` at any confidence; each binds to a head and **carries across a
   push**, with no time filter (SPEC-REVIEW rule 7, *"a HOLD never expires"*,
   `docs/SPEC-REVIEW.md:269`). The forge source is one-way: it only ever adds. A false drop costs
   one verb from the reviewer; a false admit costs a held head on `dev`.
4. **What releases a hold** is reading 3, *What releases a hold*: only its holder, only by the
   verb (`nova-merge read --verdict approve --head <sha40>`, or the `nova-review verdict` line
   APPROVE that writes it), only at the current head, and a **scoped** APPROVE releases only the
   hold ids it names with `--releases`, so a reviewer who held the parser and approved the
   documentation has not released the parser. A forge `APPROVED` review, the forge's dismissal
   of a review, a comment typed or untyped, a decider's answer and a push each release nothing.
   A `who=unknown` hold, and a pending comment, are released only by a `may-hold` reader's verb
   naming them with `--releases`, or taken over by that reader's own recorded HOLD. A scoped
   APPROVE record satisfies nothing in the read condition (`docs/SPEC-MERGE.md:808-816`); only an
   unscoped one does.
5. **What no flag does** is reading 3, *No flag ignores a hold* and *An untyped comment is
   pending, and pending stops*: there is no `--ignore-hold`. An untyped comment from a
   `may-hold` reviewer's login is **pending** by default and stops the member until a `may-hold`
   reader releases it with the verb; no comment, typed or not, clears it (Q10). Two per-run
   waivers exist, each printed on every line with its
   reason and neither touching the lane's records: `--no-require-holds --reason <text>` waives
   the forge sources whole, XOR `--reviewers`; `--untyped-comments=ignore --reason <text>` makes
   untyped comments not a hold for that run. The escape for a holder who
   cannot be woken is a commit removing that `who`'s `may-hold` in the reviewer file, and the
   receipt names the commit and not the reader.
6. **Where the fold runs** is reading 3, *The inputs* and *The lines*: at `batch`, at the point
   it reads each member's `ci-ok`, from the wire; **again** at `land`, over the receipt's
   `members=`, from the wire, immediately before `Enqueuer.Enqueue`, where one held member
   refuses the whole landing (the 02:34Z hold arrived between the two reads, and only the
   second one sees it); and at `queue sweep` and `react`, which never enqueue a held pull
   request. The `BATCH DROP`, `LAND REFUSED`, `holds=` and `dispositions=` grammar is reading
   3's. This file adds one line: `nova-pulse status` counts held PRs, so a lane standing behind a
   hold does not read as a lane with nothing to do.
7. **A swarm's member carries its gate's line and passes hygiene again.** A member
   whose head branch was pushed by `harvest` is admitted to a batch only if its PR
   body's first line is an `ACCEPT OK` whose `head=` is the member's current head and
   whose `control=` is on file; otherwise `BATCH DROP #<n> reason="no ACCEPT OK for head
   <sha12>"`. `batch` runs `internal/hygiene.Check` (§3 rule 7) over every member
   whatever its origin. **The merged tree must compile**: #1479 merged clean and left
   `undefined: useFakeForge`; `batch`'s `build` step already catches that for the batch
   — the rule added here is that the failing member is **named** by bisecting the
   members once (`BATCH DROP #<n> reason="build red with this member merged: <first
   line>"`) instead of failing the batch whole.
8. **A mechanical accept is never a read.** `ACCEPT OK` satisfies nothing in the read
   condition; `needs_read` stands for every swarm PR that changes code, on trial or trusted
   (the eligibility rule, 3), and the reader
   *"judges spec fit and nothing else"* (`docs/SPEC-REVIEW.md:659-660`) because the
   gate already did the rest. What the gate cannot judge it hands over by name: a
   pre-existing test body changed under `TEST-EDIT:`, and a rebase's conflicted files.
   And for a security-kind member the read that counts is the designated mind's and
   nobody else's (the eligibility rule, 13).

**Red tests.** The fold's tests are reading 3's *Demanded tests* in `docs/SPEC-DECIDE.md`
(#1627), by name, each a transition table over a fake forge, and T16 is not done until every one
of them is red then green; this file names the ones its reviewers asked for so that no reading of
this file can miss them: `an-abstain-record-is-not-an-input` (a recorded HOLD at H1, then the
same reader's ABSTAIN at H1: held; then a push to H2 and their ABSTAIN at H2: still held,
`carried=yes`); `child-and-card-records-are-not-inputs`;
`a-scoped-approve-releases-only-the-holds-it-names` (a parser HOLD and a docs HOLD by one
reader; their docs-scoped APPROVE leaves the parser held; their scoped APPROVE naming nothing
releases nothing; their unscoped APPROVE at the current head releases both);
`a-push-releases-nothing` (a typed line, a `CHANGES_REQUESTED` review and an untyped HOLD
comment, each posted at head A, then a push to B, with NO lane record at all: each still held);
`an-untyped-hold-from-the-shared-login-fails-closed` (author and reviewers on ONE login, the
fixture draft 1 failed open on); `the-authors-note-line-is-not-scanned-and-the-authors-login-skips-nothing`;
`a-comment-never-releases-anything` (a pasted `DISPOSITION … verdict=APPROVE` from the shared
login naming the current head; still held); `a-forge-approved-review-releases-nothing`;
`a-dismissal-releases-nothing`; `only-the-holder-releases`;
`an-unknown-hold-is-released-only-by-a-readers-verb-naming-it`; `two-names-one-login-fold-separately`;
`there-is-no-flag-that-ignores-one-hold`; `an-untyped-comment-from-a-may-hold-login-is-pending`;
`a-pending-comment-is-cleared-only-by-a-readers-verb`;
`a-scoped-approve-record-does-not-satisfy-needs-read`;
`untyped-comments-ignore-is-per-run-printed-and-carries-a-reason`;
`no-require-holds-waives-the-forge-sources-only-and-is-printed`;
`reviewers-xor-no-require-holds`; `removing-may-hold-by-commit-releases-and-the-receipt-names-the-commit`;
`land-refuses-a-hold-posted-after-batch-ok` (the receipt is #1572's timeline);
`sweep-and-react-never-enqueue-a-held-pr`. A class test, `toolwork-names-only-tests-reading-3-demands`,
asserts every test name in this paragraph appears in reading 3's list, so the two texts cannot
drift apart. This file's own tests, for rules 7 and 8: `batch-names-the-member-that-breaks-the-build`;
`swarm-member-without-accept-ok-is-dropped`; `status-counts-held-prs`.

## 7. Tests that execute documents

**Builds on.** Every command carries a `### First run` transcript in its `## <tool>`
section of `docs/TESTS.md`, asserted for every directory under `cmd/`
(`docs/SPEC-CI.md:1139-1163`); each `docs/TESTS.md` heading is written once, because
only the first is read (`docs/SPEC-CI.md:1200-1227`); `docs/TESTS.md:1-3` says every `$`
line *"is run by a test … and what the tool prints is compared with what is written
here by SHAPE"*; `onboarding.FirstRun`, `Transcript` and `Shape`
(`internal/onboarding/onboarding.go:71,153,93`).

**What those rules do not hold.** The class test asserts the section **exists**, not
that anything **runs** it. Measured at `11aa07a7` by the 2026-09-19 triage: 8 of 22
sections were executed by no test — nova-ci (its `firstrun_test.go` never opens the
document), nova-decide, nova-play, nova-pulse, nova-review, nova-secrets (no
`firstrun_test.go` at all), nova-post (asserts only that the section is non-empty) and
nova-sandbox (one line pinned by substring) — 7 at `31e35195`, after #1602 gave
nova-play the first line-for-line test. Nine more —
`cmd/nova-{board,bus,cairn,check,fuse,merge,self-talk,wake,work}/firstrun_test.go` —
collect what was printed into a `printed map[string]bool` and ask whether each
documented line is in it, so an abridged or reordered block passes. nova-post's section
runs `--channel fake` six times and the shipped tool refuses it
(`internal/post/post.go:111-118`). Every drift found that week was found by a person.

1. **Every transcript is executed, line for line, in order.** For every `## <tool>`
   section, a test in `cmd/<tool>` runs every `$` line of every fenced block under
   `### First run` and compares each command's **whole** output with the block under
   it: same number of lines, same lines, same order — #1602's shape, in-process
   `run()`, one temp directory for the sitting.
2. **One comparator, in one place.** `onboarding.CompareTranscript(doc, got, volatile)`
   is the only comparison a `firstrun_test.go` may make. Values are compared **as
   written**; the only values matched by shape are the fields in one shared table of
   run-owned values (`at=`, `took=`, `created=`, a temp path, a fresh sha) —
   `onboarding.Volatile` — and a test may name a field from that table and may not
   invent one. The set-of-shapes helper and every `printed map[string]bool` are
   deleted.
3. **The class test asserts execution, not existence.**
   `TestEveryTranscriptIsExecutedLineForLine` (`internal/ci`) walks `docs/TESTS.md`'s
   sections and fails for any tool whose package has no test calling
   `onboarding.CompareTranscript` on that tool's section, and for any
   `firstrun_test.go` that compares any other way. Its allowlist is the sections not
   yet converted, by name with the issue that owes each, **shrink-only in both
   directions** — the repository's own pattern
   (`internal/ci/testdata/prmerge_allowlist.txt`).
4. **The comparator is seen red three ways, and so is every test that uses it.** The
   comparator's own tests carry the three one-edit seeds — a line dropped, a value
   altered, a line moved — each red. Per tool, the `transcript-test` kind's control
   (§5) applies the same three seeds to a copy of that tool's **real** section and
   demands red, which proves the test reads the document and not a fixture of its own.
5. **A section that one platform cannot reproduce says which.** A `Platform:
   <goos>[,<goos>]` line under the `## <tool>` heading; elsewhere the test is a named
   skip, and the class test requires every platform named to be a leg `ci.yml` runs, so
   a skipped transcript is still executed somewhere (#1509: `backend=`, `hosts=`,
   `gpu=`, `used=`, `ancestors=`).
6. **A false transcript is a finding, never an edit to make a test pass.** A
   `transcript-test` card may not touch `docs/TESTS.md` (§5). When the document and the
   tool disagree the card's line 2 is `BLOCKED drift <file:line>` with the two lines,
   and which of the two is wrong is a person's decision and a `fix-red` or a `text`
   card afterwards — nova-post's `fake` channel is the first such decision (Q5).
7. **The other documents a stranger pastes from are counted, and the count only
   shrinks.** Every `$ ` line in a fenced block of `README.md`, `docs/USAGE.md`,
   `docs/CLI.md`'s `### First run` sections and `docs/nova-swarm-quickstart.md`, and
   every `example:` line of every `help` (#1455: 28 of 61 exit 2 when pasted), is
   either executed by a test through the same comparator or listed in
   `internal/ci/testdata/unexecuted_examples.txt` with its reason. The list is
   shrink-only; a new unexecuted example fails the class test on the PR that adds it.

**Red tests:** `compare-rejects-a-dropped-line`; `compare-rejects-a-moved-line`;
`compare-rejects-an-altered-value`; `volatile-field-outside-the-table-is-refused`;
`every-transcript-is-executed-line-for-line` (fails today, naming the seven and the
nine); `platform-line-must-name-a-ci-leg`; `unexecuted-examples-only-shrink`.

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
