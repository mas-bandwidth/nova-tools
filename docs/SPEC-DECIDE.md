# nova-decide — specification: the typed-decision route (Jev)

The typed-decision route answers small judgments with a system-one model and a
provider-reported confidence, beside deterministic machinery that keeps every
authority it already had. The confidence is the provider's number, not a
number of our own, and the floor (rule 5) is applied to that provider's
number. A decision advises; the machinery decides.

This spec is normative. If the code and this document disagree, one of them has
a bug, and the tests decide which. It stands beside [SPEC.md](SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line output
grammar, the cap-and-count rule, `internal/oneline` and `internal/bounded` —
applies here unchanged and is not restated.

## The rules, numbered

Every rule here is normative. Each has one line in **red tests** near the end.

1. **A decision is a typed judgment over bounded evidence.** a decision is a choice,
   a 0-N score or a yes/no over bounded evidence, answered by a system-one
   model with a provider-reported confidence. The three types are
   `choice` (one of named options), `score` (a float on a 0-N legend) and
   `noul` (a yes/no over one statement). The evidence is bounded: the caller
   names the state text and the questions, and nothing else is consulted.
   (The hurt: an untyped verdict — "looks good" — that no caller could gate
   on, so every consumer parsed prose and each parsed it differently.)
2. **The provider today is TypeSafe Jev.** The provider is TypeSafe Jev:
   `POST https://api.typesafe.ai/v1/systemone`, header `Authorization: Bearer
   <key>`, JSON body `{"state": <text>, "model": "jev-latest", "questions":
   {<name>: {"type": "choice", "instructions": <text>, "criteria": {<option>:
   <description>}} | {"type": "score", "instructions": <text>, "criteria":
   [<level 0 text>, <level 1 text>, ...]} | {"type": "noul", "instructions":
   <statement>}}}`; response `{"answers": {<name>: {"type":"choice","choice":
   <option>,"probabilities":{<option>:<p>},"confidence":<0-1>} |
   {"type":"score","score":<float>,"legend":{...},"confidence":<0-1>} |
   {"type":"noul","noul":<0-1>}}, "usage": {"input_tokens": n,
   "output_tokens": n}}`. Provider-reported latency 300-700 ms; the retained
   trials measure the real figures (about 400 ms in a 20-call trial on
   2026-09-17, no retained file). The key comes ONLY from the
   environment variable named by the caller (default JEV_API_KEY,
   TYPESAFE_API_KEY also accepted), never a file, never argv, never printed.
   (The hurt: a provider abstraction that hid which model answered, so a
   number measured on one model was quoted for another.)
3. **The key arrives sealed.** The key comes only from the environment by
   nova-secrets exec, never a file, argv or a log. The caller names the
   variable; the tool reads it from its own environment and passes it as the
   header value. It is never written to any file, never printed on any line,
   and never appears in a usage row.
   (The hurt: the leak that taught the swarm — a key in an argument is in the
   process table, in a log, and in a transcript somebody pastes.)
4. **The state sent is public or synthetic material only.** The state sent is
   public or synthetic material only: never a secret, never private repository
   content, never a private bus note (assume every provider trains on what it
   sees). A caller that wants a judgment over private material summarizes it
   into public-shaped features first, or does not call.
   (The hurt: a private note sent for triage that later surfaced verbatim in
   an unrelated completion.)
5. **Every decision line carries the confidence and the floor.** Every decision
   line carries the confidence and the floor, and a decision below the floor is a suggestion:
   the caller keeps today's behaviour as the fallback. The floor is per call
   site, stated beside the call, and the fallback is the code path that ran
   before the decision route existed — never "ask again" and never a wider
   permission.
   (The hurt: a 0.51 routed where a 0.99 was needed, because the number lived
   in a log nobody gated on.)
6. **Confidence never authorizes.** confidence never authorizes: permissions,
   STOPs, HOLDs, secrets, slot limits, ownership, budgets and leases stay
   deterministic machinery (Stella 2026-09-17). A decision may propose which
   queue a card joins; it may not lift a hold, spend a secret, widen a slot,
   transfer ownership, spend a budget or clear a lease.
   (The hurt: a confident "safe to land" beside a STOP the machinery had set —
   two authorities, one merge.)
7. **Decisions never write.** decisions never write: no card, no fix, no merge,
   no push is made by a decision alone. A decision returns answers; a caller
   acts on them through the same verbs, with the same evidence, it already
   had.
   (The hurt: a triage judgment that filed its own card, and the card it filed
   quoted a rule the repo had never held.)
8. **A decision is logged beside the outcome it predicted.** A decision is
   logged beside the outcome it predicted so the floor is re-tuned from data:
   the answers, the confidences, the floor, and what happened next, in one
   row. Floors are re-tuned from those rows, never from a feeling about the
   model.
   (The hurt: a floor of 0.9 nobody could defend, because no row joined a
   confidence to what followed.)
9. **Tests use an httptest fake, never the network.** Tests use an httptest fake,
   never the network: the fake speaks the Jev body-and-response shape of rule
   2 and asserts the header carries the key the environment gave. No test
   dials the provider, and no test needs a key on disk to run.
   (The hurt: a suite that passed on a fast network and failed on a train —
   and once, a suite that billed a key per run.)
10. **Every decision output carries an evidence pointer.** every decision output
    carries an evidence pointer — the task id, note id, run id or file:line the
    state came from — so a human can retrieve the original that fed the
    judgment. The pointer sits beside the answer, never folded into the model's
    state text.
    (The hurt: an answer nobody could reproduce, because the evidence that
    produced it had been dropped at the call site.)
11. **Unknowns are recorded as unknown.** unknowns are recorded as unknown: a
    below-floor answer is logged as `?` with its confidence, never dropped, and
    the original message or log is never replaced by the decision. The decision
    sits beside the original; abstention is evidence, not silence.
    (The hurt: a low-confidence guess that overwrote the only copy of the
    message it judged, so the original was gone and the guess looked certain.)
12. **Ask only on delivered work.** a decision is asked only on
    a delivered batch or a finished task, never on an empty wait or a pending
    one: no speculative provider calls against work that has not arrived. An
    empty wait is answered by waiting, not by a model.
    (The hurt: a triage loop that called the provider on an empty inbox each
    tick, spending a key and inventing work that had not been delivered.)

## Adoption

Each adoption is a call site with its floor and its measured evidence. The
fallback in every row is today's behaviour without the call.

### nova-swarm route

Route incoming work to a lane by kind. Measured: card routing 19/20 kinds
correct against a human label, at 937 input tokens, in the 2026-09-17 route
trial of 20 calls, retained at `mas-bandwidth/rowan-new
reports/jev/2026-09-17-card-route.tsv@21d27719 (model jev-latest, 22 rows)`;
latency about 400 ms in a 20-call trial on 2026-09-17, no retained file. The
floor is per lane; below it the card keeps today's routing.

### nova-decide route — the ladder of minds

Route one unit of work to one MIND, over a registry of rungs (Glenn 2026-09-18). The rungs are
Flash and Pro on the DeepSeek lineage, the child rungs Opus (Rowan's) and Sol (Stella's) at one
height in two lineages, each friend as a mind with an owned lane, Astra and Fable as the top pair,
then all friends at once, then Glenn. The answer is the lowest rung the evidence supports with
confidence that the FIRST attempt is right; below the floor it steps UP a rung, never down. A
failed attempt re-enters the decision with its evidence — the rung, the outcome, the reason — and
the answer is the next rung automatically, sideways before up: the ladder is the retry policy.

Two rungs are chosen by KIND and not by height, and by the machinery rather than the provider
(rule 6), so no provider call is made for either: security — a guard, secrets, the sandbox, sudo,
deploy keys, the network — is Johnny's always, and so is a fresh take, where the rungs below failed
in two lineages or a design has one author. Friends first: the DeepSeek rungs are eligible for
mechanical kinds only.

**Security never falls through.** A unit that touches a guard, secrets, the sandbox, sudo, deploy
keys or the network resolves to the designated rung on EVERY path: with the provider on or off, at
any floor, and after any prior attempt, including an attempt by the designated rung itself. It is a
kind and not a height, so the height rules — sideways, up, the floor, never down — do not apply to
it at all. Where no mind is designated, or every designated mind is asleep, the work WAITS for one
of them, and that is a refusal: handing a guard, a secret or a deploy key to another mind because
the right one is busy is the failure this rule exists to prevent.

**A timeout is not a death.** An attempt that timed out with no proof it terminated leaves its
expiry UNKNOWN (Stella's lease rule), and a rung whose attempt may still be running is not a rung
to step off: the answer is the SAME rung until there is termination proof, the floor does not move
it, and the provider is not asked — there is no choice to make while an attempt may be alive. Only
a CONFIRMED failure — `failed`, `abandoned`, or a `timeout` marked terminated — moves the ladder
on, and only confirmed failures are counted when the starting rung is regenerated.

**A wait is typed, and a wait is not permission.** The waiting is carried in a field of its own —
`wait=awaiting_termination` on the line, `wait` and `awaiting_termination` in the log row — never
as prose a reader has to parse; a decision that is not a wait carries `wait=-`, so the field is
always there to gate on. The same rung is an answer about WHO owns the work, not permission to
start it again: the caller's next move is to establish what happened to the attempt. The CLI says
so in its exit code, where only 0 is permission (SPEC.md): **1 is the verb running and saying NOT
YET**. The owner and the wait are two facts and neither hides the other — a security unit whose
designated rung timed out answers with that rung AND the wait.

The evidence is bounded and public (rules 1 and 4): kind — one of `rebase`, `stack`,
`fixture-retarget`, `fleet-chore`, `dogfood`, `row-test`, `fix-with-red-test`, `new-verb`, `spec`, `design`, `guard`,
`cause-to-find` — size (files, packages, lanes), lane
owner, prior attempts, platform need, what security it touches (`guard`, `secrets`, `sandbox`,
`sudo`, `deploy-keys`, `network`), and the deadline — metadata, never a body, never a secret.

**A row test is card work, and its size says so wrongly.** `row-test` is a kind of its own and it
starts at `pro`, not at the bottom: one row of a table-driven suite, on a leg whose card shape has
already been proven, is work a card rung does well and work the cheapest rung does not. The kind
exists because the two names it used to wear both answered wrong. Named `fixture-retarget`, the
ladder read its size — one file, one package — and answered `flash`, one rung under the rung that
actually landed it. Named `fix-with-red-test`, the ladder answered `opus`, two rungs over. Measured
2026-09-18: the schema campaign's row cards ran on the `pro` rung and came back green, at usd
0.03-0.04 and about 150 s each. The size term may raise a row test's rung and never lowers it, so
one file stays card work: one file is the shape of a trivial rebase AND of a subtle codegen fix,
and the count cannot tell them apart.

**A dogfood transcript diff is not a guard.** `dogfood` is a kind of its own and starts at the
bottom rung: reading a documented transcript against what the tool actually prints is a
comparison, not a judgement about safety. It exists because the work was being named `guard`, and
`guard` is a SECURITY kind — it resolves to the designated mind on every path, at any height, at
any floor (that is the rule, and nothing here touches it). So a documented-transcript diff was
being priced as a guard and handed to the mind the fleet reserves for guards, secrets, the
sandbox, sudo, deploy keys and the network. Measured 2026-09-18: 21 Flash cards over dogfood
transcripts found four real drifts for about 20 cents. Naming the work `dogfood` prices it at the
rung that does it; naming it `guard` was the mistake, and the security rule was doing exactly what
it says.

**A mechanical kind that failed on a card rung was not mechanical.** The DeepSeek rungs are
eligible for mechanical kinds only (friends first), and a mechanical kind is one whose answer is a
procedure rather than a judgement. A CONFIRMED failure on a card rung is the evidence that the
procedure was not given after all, so no card rung is eligible for that unit again: the answer
steps past the other card rung to the child rungs. The per-height sideways rule cannot reach this
case, because `flash` and `pro` are the only two minds of one lineage standing at two DIFFERENT
heights; every other lineage puts its minds far enough apart that a lineage which failed is a
lineage the height rule already excludes. The hurt: a fleet-chore a Flash card delivered as a
workflow edit needed a class test nobody had asked for, and the ladder's next answer was the other
card rung — the same mistake one rung up — when the work went to a child and finished
(nova-tools#1482, 2026-09-18).

**The floor is a number with rows behind it.** The default floor is **0.65**, and rule 8 is why it
is not 0.9 any more. Measured 2026-09-18, 39 real route calls over 13 units: every confidence the
provider returned fell between 0.61 and 0.91, and the same unit with the same evidence came back
0.78, 0.80, 0.81 and 0.82 on four separate calls. A floor of 0.9 therefore stepped up on 13 units
of 13 — the provider's answer never survived, and the route was the rules plus exactly one rung,
bought with a call. A floor of 0.8 sits inside the provider's own noise, so the same unit routed to
one mind on one call and another on the next. A floor of 0.95 sent an eight-file pull request a
child had landed green all the way to all-friends. 0.65 sits below the whole measured band, so a
step-up means the confidence actually collapsed rather than that it came back ordinary; 0.7 was
still inside it, and one rebase unit came back 0.68, 0.69 and 0.71 on three calls and routed two
ways on the strength of it. The number
moves again when the rows move: it is re-tuned from the log, never from a feeling about the model.

**The outcome is written by the verb that watched the work.** `nova-decide outcome --log <path>
--unit-id <id> --result green|red|blocked` appends the other half of rule 8's row: what happened to
a unit a decision routed. It is a row of its own, because the log is append-only and a row written
is never rewritten; it carries `source: outcome`, so the summary folds it into the rung it names
without counting a second decision. The kind and the rung are read from the last DECISION row for
that unit rather than retyped by the caller, and an outcome for a unit no decision routed is a
refusal. The hurt: on 2026-09-18 the log held 78 rows and 73 escalations and zero successes,
because nothing wrote the second half, so `log --summary` regenerated no starting rung from any of
them.

**The public-data boundary is explicit, and it is an allowlist.** What the provider sees is
typed and enumerated: a `Public()` projection of the unit and nothing else — a kind, size
buckets, whether the lane is one a
mind on the ladder OWNS (`none`, `owned`, `other` — never which lane), an attempt-count bucket, a
platform flag (`ordinary` or `named`, never which platform), a security flag and a deadline bucket
— one `field: value` line each, every value checked against the closed set its field allows before
anything is sent. A value that is not on the allowlist is a refusal, not a payload, so the boundary
fails closed.

**No registry string crosses that boundary either.** A registry is local configuration, and being
configured locally does not make a value public: a mind's name, its lineage and the lanes it owns
are as private as the project they came from. The rungs the provider chooses between are therefore
**opaque ids** — `rung-1`, `rung-2`, by position in the offered set — described only by our own
enumerations: the step above the lowest rung offered, a per-call lineage LABEL (so "the same
lineage" and "another lineage" survive without the lineage's name), whether that mind owns the
unit's lane, and how it is asked. The answer is mapped back to a mind here; an answer naming a mind
outright is an answer to a question this process never asked. The unit's id, its lane's spelling,
its platform's name, its deadline and every attempt reason stay in this process, so no title, path,
branch name or error text can ride out on a payload.

The provider is offered only the eligible rungs at the
supported height and the one above it, so a typed answer can advise sideways or up but never down;
an answer below the floor steps up, and a provider error or a rung nobody offered leaves the rules'
answer standing (rule 5). A floor that is not a number between 0 and 1 — NaN, an infinity, a
negative, anything above one — is refused with one remedy line, because NaN compares false against
every bound and would otherwise gate a decision on a number that is not one. `--no-jev` answers by
the rules alone, with no key and no network, so the loop runs where the API does not.

Every decision is logged (rule 8): the evidence, the rung tried, the confidence and floor, the
wait, the outcome and the rung that succeeded — and beside it `rowan_pick`, what the rules alone
would have chosen, which is how the route is measured against the coordinator's own hand.
`nova-decide log --summary` regenerates the starting rung per kind from those rows, and a kind with
no success keeps the rung it started from.

**What a call spent is kept, not dropped.** A routing decision that called the provider records its
usage: the call count and the tokens in the log row, and one row of the fleet's own usage TSV at
`--usage` — the same columns, written through the same appender, that a swarm card's usage is
written with, so `nova-tokens` reads a decision's spend the way it reads everything else. A call
that FAILED is a row too, with a non-zero `rc`: its cost is real and unmeasured. A decision that
made no call writes no row, because an empty row would be a claim that a call was made.

**Accounting is not optional.** Token spend reporting is an obligation and every decision is
logged (Glenn), so the route verb REFUSES to ask the provider at all unless it has been told where
both records go: `--usage` and `--log` are required whenever jev is asked, and the refusal comes
before the call, in one line naming the missing flag. A call nobody can account for is refused
rather than made and then forgotten. `--no-jev` makes no call and has nothing to account for, so
both stay optional there.

**A successful answer is not evidence of reported usage.** Presence is tracked PER COUNTER, from
the decoder outward: a 200 that carries a valid answer and no `usage` object, or one naming only
some of the counters, has said nothing about the rest — and nothing is not zero. An unreported
counter is written as the literal `-` in the TSV and is ABSENT from the log row; an explicitly
reported `0` is a measurement and is written as `0` (SPEC-TOKENS rule 14). Decoding the counters as
plain integers is what made every silent response look like a free one.

**A refusal cannot unspend a call.** Where a route ends in a refusal, the result comes back
POPULATED — the unit, the reason, and above all what a completed provider call spent — and carries
the refusal in `Refusal`. The caller persists the usage row and the log row, with that reason,
BEFORE it exits, and the exit code stays the refusal's own. The hurt: a provider answered, the
tokens were spent, the rung it picked had nothing above it to step up to, and the verb exited 2
before either file was written, so the call left no trace anywhere.

**An answer is checked against the question that asked it.** A typed decision is typed at BOTH
ends. A choice answer must name one of the options its own question offered: an answer the criteria
never held, an answer naming NOTHING AT ALL, an answer of the wrong type, and an answer to a
question nobody asked are PROVIDER ERRORS, refused at exit 2 in a line naming the answer and the
offered set — never decisions with a confidence on them, and the floor never sees one. The hurt:
`deepseek-flash` came back to a question offering `continue|ask-all-friends|ask-glenn` and printed
`HELP help=deepseek-flash conf=0.94 below=-` at exit 0, and `{"type":"choice","confidence":0.99}` —
an answer naming nothing — printed an empty value ABOVE the floor at exit 0. A decision that was
never made cannot be authorized by the number attached to it.

**The step-up signal names the rung above it, and can take it.** Exit 3 said only which question
fell below the floor, and stopped there: the reader was left to work out where the work goes next
from a ladder they cannot see, and nothing turned the signal into the step. So every route line
carries `next=<rung>` — the rung ABOVE the one answered, from the same registry, and a dash where
the answer is at or above the floor, where the work is waiting, where the answer is a designation,
or where there is nothing above. `--step-up` takes the step: below the floor it re-asks the SAME
question with that rung EXCLUDED from the criteria, up to `--max-steps` (default 3). Every step is
a decision in its own right — asked, answered, paid for — so each writes its own log row and its
own usage row, carrying its own reason, and the final line says how many it took in `steps=<n>`.
Nothing steps past an answer the floor accepts, past a wait (which is not permission to move at
all), past a designation (security is a KIND and not a height), or past a rung with nothing above
it. A step-up that never gets above the floor is still exit 3: a suggestion, never an
authorization.

**A sub-verb refuses an unknown flag by name.** `nova-decide help` with no arguments is the door
the onboarding standard names and prints the banner at exit 0; `nova-decide help` with a flag it
does not hold, or one given no value, is `REFUSED reason=bad-flags` at exit 2 naming the flag. A
flag silently swallowed is a caller who thinks they asked something and did not.

The second decision, `help`, answers continue | ask-all-friends | ask-glenn over hours on the same
problem, retries on one rung, failures in the last hour and how many were self-inflicted, a class
recurring, whether landing moved, and stated uncertainty. Ask-glenn only ever follows
ask-all-friends. Red tests: `a-failed-attempt-steps-sideways-before-up`,
`below-the-floor-steps-up-never-down`, `security-is-johnnys-by-kind-not-by-height`,
`a-designation-makes-no-provider-call`, `ask-glenn-only-after-ask-all-friends`,
`the-provider-sees-only-typed-enumerated-evidence`, `security-never-falls-through` (a table over
the provider on and off, every floor and every prior attempt), `a-timeout-does-not-advance-the-rung`,
`a-floor-that-is-not-a-number-is-refused`, `no-registry-string-reaches-the-provider` (a registry of
synthetic private markers, checked over the state AND the questions),
`an-opaque-choice-maps-back-to-its-mind`, `public-is-an-allowlist`,
`the-wait-is-typed-on-the-line`, `security-and-wait-together` and
`the-provider-usage-is-kept-including-a-failed-call`, `usage-presence-is-per-counter`,
`an-unreported-counter-is-a-dash-and-a-reported-zero-is-a-zero` and
`a-routing-refusal-still-persists-the-call` and `a-jev-call-with-no-accounting-is-refused-before-it-is-made`,
`a-choice-answer-outside-the-criteria-is-a-provider-error`,
`an-empty-choice-above-the-floor-is-a-provider-error`,
`the-line-names-the-next-rung-below-the-floor`,
`step-up-re-asks-with-the-below-floor-rung-excluded`,
`every-step-is-a-logged-decision` and `a-sub-verb-refuses-an-unknown-flag-by-name`,
all against a fake decider, with no network and no key on disk.

### manager abstain / needs_human

A manager judgment that abstains below its floor instead of guessing.
Measured: abstain reason 9 above a 0.9 floor consistent between runs and 21
below all flagged needs_human, in the 2026-09-17 abstain trial of 30 calls,
retained at `mas-bandwidth/rowan-new
reports/jev/2026-09-17-589-abstain-reason.tsv@4857e071 (model jev-latest, 31
rows)`. The fallback for `needs_human` is a person, never a second guess.

### nova-bus inbox triage (opt-in, public buses only)

Triage an inbox into carry / receipt / noise on public material. Opt-in per
bus, public buses only — rule 4 bars private bus notes from the state. Below
the floor the note stays unclassified and today's reader handles it.

### nova-pulse gate flaky-vs-real

Gate a failing pulse as flaky or real before paging. Below the floor the gate
answers real: a flaky test re-run is cheap, a missed page is not.

### nova-pulse harvest class

Classify each finished job's `RESULT.md` before any push: one choice named
`class` over {fixed, already-fixed, no-change, failed, off-branch}, asked over
the bounded public state of the `RESULT.md` first line, its `BRANCH` line, the
commits the branch carries past its base and its `files` line. Above the floor
`fixed` and `failed` push as today; `no-change` and `already-fixed` push nothing
and mark the job harvested, the `already-fixed` line naming the test the
`RESULT.md` `red:` line carries for the closer; `off-branch` pushes nothing and
prints the remedy. Below the floor the class is `unknown` (the line reads
`below=class`) and harvest runs the path that ran before the call.

### nova-merge classify merge-group failure

Classify one failed merge-group run by kind — flaky-under-load, own-change or
environment — from the `--- FAIL` test names of its failed jobs, the package each
test lives in, whether the pull request changed that package, and the runner name.
The floor is 0.9. Above it `flaky-under-load` and `environment` print `rerun=yes`
and `own-change` prints `park=yes`; below it the kind is `unknown`, `rerun=no`,
`park=no`, and the line names the raw answer as `below=<name>`, so the pass keeps
today's skip-and-rerun-versus-park behaviour.

### the space game intent layer

Classify a player's intent (ships danger, collision, maneuver) into the
game's existing response table. Measured: ships danger MAE 0.18, collision
60/60 correctness against a human label, and maneuver 51/60 low confidence,
all in the 2026-09-17 ships trial of 60 calls, retained at
`mas-bandwidth/rowan-new reports/jev/2026-09-17-ships.tsv@21d27719 (model
jev-latest, 62 rows)` — the maneuver band stays on the hand-written table
until its confidence earns the floor. Throughput measured: 22 calls/s at
concurrency 10, measured in the 2026-09-17 ships trial of 60 calls.

## Jev across the stack

Every layer below asks the same three-line shape: the question, the floor, the invariant. The
invariant is one sentence across all of them: **a decision advises, the machinery decides, the
inputs are public or synthetic, and a decision never carries a secret and never carries a private
bus body.** Each subsection ends with one red test, run against the httptest fake of rule 9, with
no network and no key on disk.

### Go tools — the `--decide` flags

The question: the small judgment each verb already makes in code, asked behind a flag — `route`,
`triage`, `gate`, `review packet`, `inbox`, `merge classify`, `queue order`, `harvest class`,
`card lint`. The floor: per flag, stated beside the call site; below it the tool runs the branch
it ran before the flag existed and prints the answer as a suggestion (rule 5). The invariant: the
flag returns a typed answer and the verb acts through the same verbs it already had (rules 6 and
7); the state text is the card's public summary, never a private bus body and never a secret (rule
4). Red test: `a-decide-flag-below-its-floor-runs-the-pre-flag-branch` — the fixture answers under
the floor, the verb prints its evidence pointer (rule 10), and no card, fix or push is written
(rule 7).

### The Lisp kernel — one protocol, one in-process fake

The question: only the choices the kernel cannot resolve itself — the order of a ready set whose
priorities tie, an abstain reason, a conflict between two nodes' claims. The floor: below it the
kernel keeps its own order, its own abstain or its own tie-break, and records the answer as a
suggestion. The invariant: the kernel asks through one `decide` protocol function with one
in-process fake (rule 9); every decision is journaled as an event carrying the question hash, so a
replay reproduces the answer without calling the provider, and the kernel's authority — a decisive
ready order, leases, holds, ownership — is untouched (rule 6). Red test:
`a-journaled-decision-replays-without-the-provider` — with the fake absent on replay, the event's
question hash yields the same answer and the kernel's decisive order is identical.

### Redis — the decision cache

The question: the pull worker asks once on an ambiguous card class, where the answer is a function
of a state the queue already holds. The floor: below it the worker takes the default lane and
source order; the cache stores the `?` (rule 11) so the miss is not re-bought. The invariant: the
cache is keyed by the question hash with a mandatory TTL and nothing in it is the only copy of
anything (SPEC-REDIS); the same question is never paid twice while the key lives, and the state was
public or synthetic before it was hashed (rule 4). Red test:
`the-second-identical-question-is-served-from-redis-with-no-provider-call` — the first call hits
the fake, the second is a cache hit before the TTL and a fresh call after it.

### Postgres — the decisions table

The question: what did we decide, at what confidence, above what floor, and what followed. The
answer, the provider confidence and the floor live in the row; the outcome is filled when known.
The invariant: a decisions table `(question_hash, kind, answer, provider_confidence, floor,
outcome)`, written by one writer, is the calibration record rule 8 owes; `nova-decide tune` reads
it and refuses a floor with no rows behind it (rule 8); the table is a projection of the log and
never an authority over the machinery. Red test:
`nova-decide-tune-refuses-a-floor-with-no-rows` — an untuned floor is refused, and a row joins its
confidence to the outcome that followed.

### Terraform and Kubernetes — the key, sealed

The question: a Job's admission may take one typed score, and nothing else in the cluster asks.
The floor: below it the Job keeps the admission the manifest already carried. The invariant: the
Jev key is a sealed secret injected as env only, never a file, never argv, never a log (rule 3);
a decision never changes resource limits, replica counts, quotas or a rollout — that is the
machinery's word (rule 6). Red test: `a-decision-cannot-change-a-resource-limit` — a typed score
admits a Job, and an answer proposing a limit, quota or replica change is refused and the manifest
stands.

### Git and GitHub — classify, order, risk; never a merge

The question: merge classify, queue order, and a review packet's risk and scope. The floor: below
it the queue keeps source order, the packet keeps its hand-written risk, and the merge gate runs
as before. The invariant: a decision never merges, never pushes, never approves a review and never
clears a branch protection (rule 7); it labels a PR the machinery already gated. Red test:
`a-merge-classification-never-merges` — the classifier's answer lands on the packet, and no merge
commit, push or approval exists after the run.

### The work language — one `:decide` field

The question: where a plan leaves a choice to a typed decision, one `:decide (:type :choice
:question Q :options (...))` on the node, with the fallback the field's floor names. The floor:
below it the node takes the plan's default and records the choice as `?` beside the answer. The
invariant: the field is data the bounded reader accepts beside `:inputs`, `:output` and `:budget`,
and the card blocks on `choice=` until the decision answers (SPEC-WORKLANG Part 3); a `:sweep`
never needs one, because the fact that selects the sweep is a rule and not a judgment. Red test:
`worklang-a-decide-field-blocks-its-card-until-an-answer-and-a-sweep-needs-none` — a node carrying
`:decide` is not pullable until the fake answers above the floor, and a `:derive` sweep expands
with no decision call.

### Outbound — never

The question: none. The floor, the invariant and the red test are one sentence: **no decision
approves a post, a mail or a message.** `nova-post` carries the outward channels, and an outward
receipt is Glenn's alone; a typed answer may rank a draft's risk, but the send is a person's
receipt, never a model's (rules 6 and 7). Red test:
`no-decision-approves-a-post-a-mail-or-a-message` — a decision-only run over an outbound draft
produces no post, no mail and no message.

**The cost line.** Measured in the retained trials: about 400 ms and under a thousand input tokens
per call (rule 2; the 2026-09-17 trials). **The one thing Jev must never be: an authority.**

## Red tests

One line per rule, then one per adoption. Each runs against the httptest fake
of rule 9, with no network, and each must be seen red before it is trusted.

**Trial report (measured, not normative).** These fake-provider tests validate
the interface contract — the request and response shapes, the confidence floor
and the refusal paths — and nothing more. They cannot reproduce a real model's
behaviour: real-model accuracy, latency and throughput are only what the
retained trials in `mas-bandwidth/rowan-new reports/jev/` show, named here by
path. Measured: card routing 19/20 kinds correct against a human label at ~937
input tokens in the 2026-09-17 route trial of 20 calls, retained at
`mas-bandwidth/rowan-new
reports/jev/2026-09-17-card-route.tsv@21d27719 (model jev-latest, 22 rows)`,
latency about 400 ms in a 20-call trial on 2026-09-17, no retained file;
abstain reason 9 above the 0.9 floor consistent between runs and 21 below all
flagged `needs_human` in the 2026-09-17 abstain trial of 30 calls, retained at
`mas-bandwidth/rowan-new
reports/jev/2026-09-17-589-abstain-reason.tsv@4857e071 (model jev-latest, 31
rows)`; ships danger MAE 0.18, collision 60/60 correctness against a human
label, maneuver 51/60 low confidence stays on the hand-written table, all in
the 2026-09-17 ships trial of 60 calls, retained at `mas-bandwidth/rowan-new
reports/jev/2026-09-17-ships.tsv@21d27719 (model jev-latest, 62 rows)`, with
throughput 22 calls/s at concurrency 10 measured in that trial; and the note
`2026-09-17-jev-note.pdf` (all 2026-09-17).

1. A question over bounded state returns the typed answer with a
   provider-reported confidence; an untyped reply is refused, not parsed.
2. The fake asserts path `/v1/systemone`, model `jev-latest`, the header
   value, and the three question shapes; a body missing `state` is refused; a
   key on argv or in a log is red; `TYPESAFE_API_KEY` is accepted where
   `JEV_API_KEY` is absent.
3. The key arrives only via the named environment variable under `nova-secrets
   exec`; it appears in no file, no argv and no log line.
4. A caller that offers a secret, private repository content or a private bus
   note as state is refused before any call; public and synthetic states pass.
5. A decision line prints `confidence=` and `floor=`; below the floor the
   caller runs the fallback path and marks the answer a suggestion.
6. A decision proposing to lift a permission, STOP, HOLD, secret, slot limit,
   ownership, budget or lease is refused; the machinery's word stands (Stella
   2026-09-17).
7. A decision alone writes nothing: no card, no fix, no merge, no push exists
   after a decision-only run.
8. Each decision appends one row joining answers, confidences, floor and
   outcome; a floor with no rows behind it is refused as untuned.
9. The suite runs with the network refused and no key on disk, green on the
   fake alone.
10. Every decision prints its evidence pointer — a task id, note id, run id or
    `file:line` — beside the answer, so the original state is retrievable.
11. A below-floor answer is logged as `?` with its confidence and is never
    dropped; the original message or log still exists after the decision.
12. A delivered batch or a finished task is decided; an empty wait or a pending
    task is not, and no provider call is made for it.
13. Route adoption: the route verb takes the fixture's above-floor answer and
    prints the chosen kind; below the floor it falls back to the default route
    and says so.
14. Abstain adoption: the abstain reason and `needs_human` come through with
    their confidence; a below-floor answer leaves the abstain as the
    machinery's own.
15. Inbox adoption: opt-in only, public buses only; a private bus is refused;
    below-floor notes stay unclassified.
16. Pulse adoption: below the floor the gate answers real; a forced-flaky
    mutation pages.
17. Game adoption: the game path uses the decision above the floor and falls
    back to the local rule below it.
