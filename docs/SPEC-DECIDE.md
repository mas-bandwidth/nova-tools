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

### nova-bus inbox --decide (subject-line triage)

`nova-bus inbox --decide` asks one `choice` question per new note, named
`class`, whose options are `act-now`, `read-later` and `receipt-only`; the state
is the subject line, the From name, the addr (`to` or `cc`) and whether the
subject starts with `ADOPTED`, `HOLD`, `APPROVE`, `STOP`, `GREEN` or a PR
number, and never the body, which is private. Each INBOX NOTE line prints
`class=<kind> conf=<c>`; with `--only-act-now`, above the floor only the
act-now notes print and a `wait` wakes only for them. `STOP` and `HOLD` are
act-now by deterministic machinery and are never sent. The floor is 0.9: below
it the note prints `class=unknown` with `below=class` and today's listing runs
unchanged, so the decision advises and the machinery decides.

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
