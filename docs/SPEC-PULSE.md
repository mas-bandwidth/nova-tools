# nova-pulse — specification, draft 1

Glenn, 2026-09-15: *"We work in parallel by default. If you have work that can be done in
parallel, push it all out now in a pulse. When work comes back in, see if there is more work
that can be done again, push it all out, max, in a pulse. Repeat this over and over. Only real
work; if real work runs out, say so and never pad. Scatter, gather, maximum throughput."* And
the ask: a tool, so the behaviour is locked in and never drifts.

One tool, five verbs, all mechanical. **The tool makes no model call**: it enumerates work,
cuts cards from templates, admits them through `nova-swarm batch`, and folds what comes back.
Every token spent is a card's, and the card's usage is already summed on the swarm's `BATCH`
line ([SPEC-SWARM.md](SPEC-SWARM.md), **Batch: scatter, wait, gather**). The coordinator's
window reads one line per cycle.

- `nova-pulse pool` enumerates bounded open work from declared sources into `pool.tsv`.
- `nova-pulse cut` writes one card per candidate from a typed template, in the practice-17
  shape ([WORKER-CARDS.md](WORKER-CARDS.md) 17), and a `cards.tsv` with the model by kind.
- `nova-pulse launch` allocates free slots, admits the cards as one batch with a `--then`
  that names `harvest`, and queues what did not fit.
- `nova-pulse harvest` pushes and opens a PR for every card whose `RESULT.md` line 1 is its
  contract line, sends every abstain to `retry.tsv` with the harness's last refusal line,
  cuts a read card per PR, and pulses again — queue first.
- `nova-pulse width` is the drift alarm: one line, and a non-zero exit when there is pool
  and there are free slots and nothing was launched.

SPEC.md's **Conventions** govern — exit codes, the one-line grammar, the field law,
`internal/oneline`, `internal/bounded`, no guessed paths — and this file says only what is
more. Merges are not this tool's: `nova-merge` and a person hold the gates
([SPEC-MERGE.md](SPEC-MERGE.md), **The merge condition**).

## The loop, in words

```
pool ──► cut ──► launch ══(nova-swarm batch, --then harvest)══► harvest ──► pool ──► …
 │                 │                                              │
 │  candidates     │  cards that fit the free slots go out now;    │  done: push, PR, read card
 │  from sources   │  the rest wait in queue.tsv, never dropped    │  abstain: retry.tsv, no push
 │                 │                                              │  then pool, cut, launch again
 └─ empty? say so: PULSE POOL EMPTY. Never pad. ◄─────────────────┘
```

**Scatter** is `launch`: every card that can run now runs now, one slot each, one batch id,
one deadline. **Gather** is `harvest`: it reads the swarm's packet, never a report body, and
disposes each card by its own two lines. **Scatter again** is the last thing `harvest` does.
The loop ends only when the pool and the queue are both empty, and then it says so.

## The rules, numbered

1. **Sources are declared in one file, named by a flag, kept in git.** `--sources <file>` is
   a tab-separated file, one source per line: `kind`, `locator`, `template`. Four kinds:
   `issues` (`owner/repo` — open issues carrying label `card`, or the dogfood shape: tool,
   command, verbatim output, expected, smallest fix), `audits` (`owner/repo` — open issues
   whose body has a `MISSING:` or `DRIFT` line, one candidate per such line), `bus` (a
   nova-bus checkout — open notes whose body has a `slices:` block, one candidate per slice),
   `roadmap` (a lisp file under `docs/roadmaps/` — every cell whose `:card` names a template),
   `work` (a nova-work checkout — bug nodes and item nodes that are open, unleased and
   unblocked, one candidate per node). A `work` node is unleased when no `launch` currently
   holds it and unblocked when it is not waiting on a merge; the node id rides on the card's
   line 1 (rule 5) so `harvest` can record the attempt on the node (rule 11).
   A missing `--sources` is `refusing to guess`, exit 2. A source that cannot be read — a
   `gh` non-zero, a bus checkout that is not one, a roadmap file that does not parse — is one
   `POOL REFUSED` line naming the source and the remedy, exit 2, and no `pool.tsv` is written:
   a pool with a source silently missing would read as *no work* (the same law as nova-update
   rule 7: a dead source is never green).
2. **`pool.tsv` is five fields, one candidate per line, in source order.** `source`, `id`,
   `kind`, `title`, `template`. `id` is the issue number, the audit line's `<issue>#<n>`,
   the note id and slice ordinal, the roadmap cell name, or the work node id. `template` is
   the source's unless
   the item names one: an issue body line `template: <name>`, a slice's `template:` word, or
   the cell's `:card`. A candidate whose (`source`,`id`) is already in `<root>/seen.tsv` with
   state `carded`, `running` or `pr` is not re-pooled; a `retry` state is, so a rewritten
   card can go out (rule 14). Priority is source order and nothing else.
3. **Bounded work only.** A candidate is one issue, one audit line, one slice, one cell:
   one bounded item, one card. `pool` never splits an item and never merges two; an issue
   whose body says it is a plan (no `expected`, no `smallest fix`, a `slices:` block of its
   own) is counted in `plan=<n>` on the `POOL` line and not pooled — a plan is the bus's, and
   its slices arrive by the `bus` source.
4. **Six typed templates, in one directory, named by a flag.** `--templates <dir>` holds
   `read.md`, `fix.md`, `text.md`, `replay.md`, `drift.md`, `tone.md` and `models.tsv`
   (two lines: `flash <model id>`, `pro <model id>`). A candidate whose template is not a
   file there is `skipped`, counted on the `CUT` line, and its (`source`,`id`) goes to
   `skipped.tsv` with `no template <name>`; `cut` never falls back to another template.
5. **Every card is the practice-17 shape, and the template guarantees it.** Line 1 is the
   `RESULT` contract line, `RESULT <label> sha=<sha12 of the card text below line 1>`; line 2
   is the role and the wall (the job directory, `./scratch` as `TMPDIR`, never `/tmp`, `~`
   or `..`, no stdlib or toolchain source, the deadline held by the machinery). `STEP 1` is
   `mkdir -p scratch`, `export TMPDIR=$PWD/scratch`, the https clone, `git checkout -b
   <branch>`; every step is numbered with one check; the final step writes `RESULT.md` with
   line 1 equal to the card's line 1, line 2 the verdict, then a `BRANCH <name>` line and a
   `REPO <owner>/<name>` line. Scratch notes live in the repo directory, never `../scratch`.
   `cut` refuses a template whose rendered card violates any of these — `CUT REFUSED
   template=<name>: <which>` — because a card that drifts here is a card that stalls.
6. **Text-only templates forbid the build.** `read`, `text` and `tone` carry the line `Do not
   run go build, go test or any toolchain; read and write only`, and `cut` refuses a text
   template that lacks it; `fix`, `replay` and `drift` carry rule 4 of WORKER-CARDS: red
   line then green line, one row per item.
7. **The model is decided by the kind and nowhere else.** `read`, `text`, `tone` → `flash`;
   `fix`, `replay`, `drift` → `pro`; the ids come from `models.tsv`. `cards.tsv` is four
   fields per line: `label`, `slot`, `model`, `card` (the card's path). `slot` is `-` until
   `launch` allocates. There is no `--model` flag on any verb: a person who wants another
   route edits `models.tsv`, in git, once.
8. **A slot is free when no job holds it and its `native.log` is quiet.** `launch` reads the
   swarm pool under `--root`: a slot is free when `<pool>/slots/<n>.json` is absent or
   `state=free` **and** `<slot>/native.log` has not been written for 120 s. Both, never
   one: a slot file freed under a writer is the hurt of SPEC-SWARM rule 17. `free-before=<n>`
   on the `PULSE` line is that count before admission; `--slots <n>` caps it.
9. **Cards beyond the free slots are refused, or queued by choice.** `n` cards over `f` free
   slots with no `--queue` is `PULSE REFUSED UNDER-SLOTS cards=<n> free=<f> (pass --queue, or
   wait)`, exit 2, and nothing is admitted — half a pulse admitted by surprise is the
   coordinator counting by hand. With `--queue` the first `f` cards in `cards.tsv` order go
   out and the rest are appended to `<root>/queue.tsv`, `queued=<n>` says how many.
10. **Every card goes through `batch`, never a single `add`.** `launch` runs exactly one
    `nova-swarm batch --pool <root>/pool --tasks <dir> --label pulse-<id> --deadline <s>
    --then "nova-pulse harvest --id <id> --root <root>"` per model route present, so at
    most two admissions, both under one pulse id recorded in `<root>/pulses/<id>.tsv`
    (`batch id`, `model`, `n`). The `--then` argv is `nova-swarm batch`'s (card 269): it
    runs when the batch's wait ends — every card ended or the deadline — and never earlier.
    A `BATCH REFUSED` line from the swarm is relayed as `PULSE REFUSED` with the swarm's
    reason and nothing is queued.
11. **`--then` is gated on the verdict, never on mergeability.** `harvest` disposes a card by
    its own two lines — line 1 the contract, line 2 the verdict — and by the `BRANCH` line.
    It never asks `nova-merge` whether the PR can merge, never reads a hosted check, never
    waits for a read. A chain that waits on mergeability waits on a person, and the pulse
    stalls behind the one thing this tool was built not to wait for (the chain-gate lesson,
    cairn 0b737f92).
12. **Line 1 must match, or nothing is pushed.** For every done card `harvest` runs
    `nova-swarm verify --result <job>/RESULT.md --contract <card line 1> --label <label>`;
    a mismatch is `mismatch=<n>` on the `HARVEST` line, the card's row in `seen.tsv` is
    `mismatch`, and no push, no PR. A match with a `BRANCH` line and a `REPO` line is pushed
    by explicit refspec — `git push <https url> <branch>:<branch>` from the job's clone,
    never `git push` bare, never to `main` (a `BRANCH main` line is `mismatch`) — and a
    draft PR is opened with `gh pr create --draft` whose body is the `RESULT.md` lines, capped
    at `--max-body-bytes` (default 4096). One `HARVEST PR` line per PR.
13. **Every PR gets a read card in the next pool.** `harvest` appends (`pr`, `<repo>#<n>`,
    `read`, `<title>`, `read`) to `<root>/next.tsv`, which the next `pool` reads after
    `queue.tsv` and before the sources. Reads go by verb, through cards, like everything
    else; a PR nobody is asked to read is a PR that waits.
14. **An abstain is a prompt defect; it is written down and never retried as is.** For every
    `ABSTAIN` row and every card with no `RESULT.md`, `harvest` files nothing, counts it
    in `abstain=<n>`, and appends to `<root>/retry.tsv`: `label`, `card path`, and the last
    line of `<job>/harness.log` that carries `permission`, `denied`, `refused` or `REFUSED`
    (else the log's last line), bounded to one line. `retry.tsv` is a person's inbox: the
    card is rewritten, the row's `seen.tsv` state becomes `retry`, and only then does `pool`
    pick the item up again (rule 2). No automatic requeue, no backoff, no second try of the
    same bytes ([WORKER-CARDS.md](WORKER-CARDS.md), practice 17's holder: *fix the prompt*).
15. **Harvest pulses again, queue first.** After the counts, `harvest` runs `pool`, `cut`
    and `launch --queue` in that order, with `queue.tsv` rows first, then `next.tsv`, then
    the sources, and prints the next `PULSE` line as its own last line. When the pool and the
    queue are both empty it prints `PULSE POOL EMPTY in-flight=<n>` and exits 0: real work
    ran out, and the tool says so. It invents nothing to keep the slots warm.
16. **`width` is the alarm, and it is one line.** `PULSE WIDTH in-flight=<n> free=<n>
    pool=<n> queued=<n>` when the state is consistent; `PULSE UNDER-WIDTH pool=<n> free=<n>:
    launch` with exit 2 when `pool + queued > 0` and `free > 0` — work is waiting and slots
    are idle, which is the drift Glenn named; `PULSE POOL EMPTY in-flight=<n>` when `pool =
    0` and `queued = 0`. `width` reads `pool.tsv`, `queue.tsv` and the slot files; it
    launches nothing and writes nothing. nova-wake may run it on a clock; this tool has none.
17. **The cost model.** `nova-pulse` costs no model tokens: no verb calls a model, reads a
    report body, or summarises anything. Each card's usage is on the swarm's `CARD` line and
    summed on its `BATCH` line; `harvest` copies `usd=<sum>` onto `HARVEST OK` and adds
    nothing. The coordinator's cost per cycle is one `PULSE` line read and one `HARVEST`
    line read, so fewer turns in the window (Glenn 2026-09-14: input tokens are the win).
18. **Bounded output, per SPEC.md.** Every verb prints at most `--max` (default 20) event
    lines per kind — `HARVEST PR`, `HARVEST RETRY`, `CUT SKIPPED` — then one `<TOKEN> MORE
    kind=<k> shown=<n> total=<t> <remedy>`; the count line prints on failure as well as
    success; every value is one `internal/oneline` token. No verb prints a list of titles.
19. **State is files under `--root`, all of them tab-separated, all of them in the open.**
    `pool.tsv`, `cards.tsv`, `queue.tsv`, `next.tsv`, `retry.tsv`, `skipped.tsv`, `seen.tsv`,
    `pulses/<id>.tsv`, and the cards under `cards/<pulse id>/`. No database, no lock of this
    tool's own: the swarm's `slots.lock` is the only lock touched, and only through `nova-swarm`.
    A missing `--root` is `refusing to guess`, exit 2.

## The verbs

```
nova-pulse pool    --sources <file> --work <nova-work root> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--max <n>]
nova-pulse harvest --id <pulse id> --root <dir> --sources <file> --templates <dir> [--max-body-bytes <n>] [--max <n>]
nova-pulse handoff --to <name> --root <dir>
nova-pulse takeover --as <name> --root <dir> --sources <file> --templates <dir> [--max <n>]
nova-pulse manager --policy <file> --queue <dir> --roots <dirs> --bus <clone> --as <name> --hours <n>
nova-pulse width   --root <dir> --pool <pool.tsv>
nova-pulse status  --queue <dir> --roots <dirs> [--day <d>]
nova-pulse version
nova-pulse help
```

Those lines are the string `nova-pulse help` prints, byte for byte. `--timeout <s>` (default
120) bounds every `gh`, `git` and `nova-swarm` child, for SPEC-MERGE's reason
(SPEC-MERGE.md:458). `harvest` takes `--sources` and `--templates` because its last act is
`pool` and `cut` again (rule 15). There is no `--model`, no `--priority`, no `--retry`.

## Handoff

The manager shift is the coordinator's turn on a queue, and it ends by handoff and begins again
by takeover. `handoff --to <name>` ends the shift: it writes the `SHIFT END` line, stops the
loop, releases the `OWNER` lock (name, host, pid, since), writes a `HANDOFF` record (last
`WIDTH`, in-flight cards by bench, pending, escalations open, benches and state) and posts
one bus note to the successor carrying the record. It refuses mid-harvest — it finishes the
harvest first — and refuses when the successor is asleep by `nova-wake awake`, and then
prints `HANDOFF OK`. `takeover --as <name>` refuses when `OWNER` names a live process on a
reachable host (`TAKEOVER REFUSED owner=<name> pid=<n> host=<h>`); a stale lock is taken with
one `NOTE` line, then the loop and a manager shift start on the same queue and it prints
`TAKEOVER OK`. When nova-work is open, `handoff` also moves the coordinator ownership record
in the tree — generation, token, fencing, the `:handoff` event SPEC-WORK names.

The `OWNER` lock and the `HANDOFF` record live under `<root>/queue/` as files, both
tab-separated. `OWNER` is `name`, `host`, `pid`, `since`. `HANDOFF` is `to`, `from`,
`width` (the last `PULSE WIDTH` line), `in-flight` (cards by bench), `pending`, `escalations`,
`benches` and `state`.

## Status

`nova-pulse status --queue <dir> --roots <dirs> [--day <d>]` is the one-verb answer to the
all-day questions — what runs where, how wide, at max throughput, what landed and who adopted
it, what is in flight and what remains and how long, what new work appeared, are we converging.
It prints, no model, at most 20 lines, eight line kinds, each one line, counts not lists:

- `WIDTH <bench>`, one per bench in scope — `running` jobs, `slots`, `load`, `headroom`.
- `QUEUE` — `pending`, `gated` (waiting on a merge or a hold), `launched`, `done`, `failed`.
- `RATE` — `cards_per_hour`, `p50_s`, `p90_s`, `usd_per_card`, `parallelism`, from `usage.tsv`.
- `REMAINING` — `queue` rows, `unread_prs`, `dirty_prs`, `uncarded_issues`, `hours`, in scope only.
- `CONTRACTION` — cards `cut/done`, prs `opened/merged`, issues `filed/closed`, for the last hour and the day, counted, never from a report body.
- `ADOPTION <friend>`, one per friend, coordinator included — `version`, `receipt`, `edges`, from the ADOPT files and bus receipts.
- `OPEN` — `dogfood`, `holds`, `escalations`.
- `TOOLS` — `merged_since_adoption` and the names, from `gh` cached per tick.

Sources: the queue, `usage.tsv`, the ADOPT files, bus receipts, `gh` (cached per tick). In
nova-work, `check` carries the same lines from the tree and the journal (nodes minted per node
closed, remaining by depth, completion by epic) once the tree is the pool (#500). Prototype:
`bin/status.sh`. The grammar, one line each, counts not lists:

```
STATUS WIDTH <bench> running=<n> slots=<n> load=<n> headroom=<n>
STATUS QUEUE pending=<n> gated=<n> launched=<n> done=<n> failed=<n>
STATUS RATE cards_per_hour=<n> p50_s=<n> p90_s=<n> usd_per_card=<n> parallelism=<n>
STATUS REMAINING queue=<n> unread_prs=<n> dirty_prs=<n> uncarded_issues=<n> hours=<n>
STATUS CONTRACTION hour cards=<cut/done> prs=<opened/merged> issues=<filed/closed>
STATUS CONTRACTION day cards=<cut/done> prs=<opened/merged> issues=<filed/closed>
STATUS ADOPTION <friend> version=<v> receipt=<n> edges=<n>
STATUS OPEN dogfood=<n> holds=<n> escalations=<n>
STATUS TOOLS merged_since_adoption=<n> <names>
```

## Exit codes and the output grammar

Per SPEC.md: **0** the verb ran and the state it reports is consistent — a pool written, cards
cut, a batch admitted, a harvest folded, a width that is not under; **1** the tool saying NO —
a harvest with `mismatch > 0` or `abstain > 0`, a cut with `skipped > 0`; **2** could not run
— every refusal the rules name, `UNDER-SLOTS`, and `UNDER-WIDTH` (rule 16: the alarm is a
state the coordinator must act on, and it exits like a refusal so a wake fires on it).

```
POOL OK sources=<n> candidates=<n> issues=<n> audits=<n> slices=<n> roadmap=<n> work=<n> next=<n> plan=<n> seen=<n> took=<d> out=<path>
POOL REFUSED source=<kind>:<locator>: <reason> (<remedy>)
CUT OK cards=<n> skipped=<n> flash=<n> pro=<n> out=<dir>
CUT SKIPPED source=<kind> id=<id> template=<name>: no template
CUT REFUSED template=<name>: <which rule> (<remedy>)
PULSE OK id=<id> n=<n> free-before=<n> queued=<n> batches=<n> deadline=<s>
PULSE REFUSED UNDER-SLOTS cards=<n> free=<n> (pass --queue, or wait)
PULSE REFUSED: <reason> (<remedy>)
HARVEST PR repo=<owner/name> pr=<n> label=<label> branch=<name>
HARVEST RETRY label=<label> card=<path>: <last permission or refusal line, escaped>
HARVEST OK id=<id> done=<n> pushed=<n> prs=<n> abstain=<n> mismatch=<n> retry=<n> usd=<sum|-> took=<d>
HARVEST REFUSED id=<id>: <reason> (<remedy>)
PULSE WIDTH in-flight=<n> free=<n> pool=<n> queued=<n>
PULSE UNDER-WIDTH pool=<n> free=<n>: launch
PULSE POOL EMPTY in-flight=<n>
HANDOFF OK to=<name> inflight=<n> pending=<n> escalations=<n>
HANDOFF REFUSED: <reason> (<remedy>)
TAKEOVER OK from=<name> inherited=<inflight/pending/escalations>
TAKEOVER REFUSED owner=<name> pid=<n> host=<h> (wait, or clear the stale lock)
<TOKEN> MORE kind=<k> shown=<n> total=<t> <remedy>
<TOKEN> NOTE <something true about this run that is not a finding>
```

`POOL`, `CUT`, `PULSE`, `HARVEST`, `HANDOFF` and `TAKEOVER` are the first tokens; `OK`,
`REFUSED`, `WIDTH`, `UNDER-WIDTH` and `POOL EMPTY` the verdicts and the **last** line of a
verb; `harvest`'s last line is the `PULSE` line of the pulse it launched, or
`PULSE POOL EMPTY`. `OK` and `WIDTH` lines go to stdout, `REFUSED` and `UNDER-WIDTH` to
stderr. Every line is one line; a count stands where a list would be; every refusal carries
one remedy in parentheses.

## The card, as `cut` writes it

```
RESULT <label> sha=<sha12>
You are a worker. Job directory only; ./scratch is TMPDIR; never /tmp, ~ or ..; no stdlib or toolchain source; the deadline is the machinery's: <s> s.
STEP 1. mkdir -p scratch && export TMPDIR=$PWD/scratch && git clone -q https://github.com/<owner>/<name>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints <head>. Else write RESULT.md with line 2 BLOCKED head=<yours> and stop.
STEP 2..n. <the template's numbered steps, one command per line, one check each>
STEP last. Write RESULT.md: line 1 exactly the line 1 of this card; line 2 one of DONE, ABSTAIN <why>, BLOCKED <why>; then BRANCH <branch>; REPO <owner>/<name>; then the RESULT template's sections.
```

`<sha12>` is the first twelve hex of the SHA-256 of everything below line 1, so the contract
line binds the card it heads; the swarm records the same hash at admission and refuses a
`RESULT.md` whose line 1 differs (SPEC-SWARM, **gather**). Text templates add rule 6's line
after line 2. `<head>` is the repo's default-branch head at cut time, read once per repo per
`cut` run; a candidate whose repo cannot be read is `skipped` with the reason, never cut blind.

## What this draft does not do

- **No model routing beyond the kind→model table.** Two routes, six kinds, one file in git.
  No cost-aware choice, no fallback provider, no per-card override; `models.tsv` is the
  whole policy, and changing it is a diff somebody read.
- **No merging.** It opens draft PRs and cuts read cards. `nova-merge` and a person hold the
  lane, the gate and the read condition; this tool never runs `nova-merge`, never labels,
  never marks ready.
- **No priority beyond source order.** `queue.tsv` first, `next.tsv` second, then the
  sources in file order, then the items in each source's own order. A person who wants an
  item first moves its source line up.
- **No cross-repo dependency graph.** Every card is one bounded item on one repo at one head;
  a card that needs another card's PR merged first is a card that is not bounded yet, and it
  waits in an issue until it is.
- **No retry without a rewritten card.** A stalled or abstained card is a prompt defect
  (rule 14): `retry.tsv` names it and the harness's last refusal, a person rewrites it, and
  the rewritten card is a new candidate. The same bytes never go out twice.
- **No model call, no summary, no judgement.** It never reads a report body, never opens a
  transcript, never scores a finding. The swarm's packet is the whole read.
- **No clock of its own.** No daemon, no `--loop`, no `--watch`. The chain is `--then`;
  the alarm is `width`, run by nova-wake or a person.

## The manager tier

Stella's answer to Glenn's *"I want the intelligence; I don't want to spend it sending out jobs
and reading results"* is a third tier between planning and work. **Planning** is a person and
the strong model: decisions, specs, rules; its output is cards and notes. **Manager** is a bounded
controller on the cheapest qualified model: it owns the bus wait, harvests, triages abstains
and HOLD reads by rewriting cards from templates, files dogfood issues, cuts fix cards, merges
non-draft PRs on an approving read plus green CI, and escalates a decision as one line.
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
on the other bench and escalating the second; merge a non-draft PR whose read said `APPROVE`
once the head revalidates and every check is `SUCCESS`, never on `HOLD`; refill the queue
from the policy's sources to its floor, deduplicated on PR number, issue number and the
contract sentence, in the policy's scope, leaving `AFTER: PR<n> merged` gates gated; and
write one `MANAGER` line to `<queue>/MANAGER.log`. The policy is key=value lines —
`wait-timeout`, `floor`, `scope-regex`, `sources`, `known-flakes`, `max-attempts` — and an
unknown key is a refusal, exit 2, because a policy the tool half-understands is a policy
nobody approved.

Replays: `manager-never-expands-policy`, `manager-quiet-time-makes-no-call`,
`manager-dedups-on-contract-line`, `manager-revalidates-head-before-merge`,
`manager-never-merges-draft`, `manager-shift-ends-with-handoff`,
`manager-requeues-once-then-escalates`, `manager-refuses-fix-pr-without-test`.

## Tests this spec demands

Acceptance replays, one per rule that can be made red, each proven able to fail by a mutation
first. Every `gh`, `git` and `nova-swarm` is a fixture on `PATH` that records its argv;
tripwires: outside the docs, no `api.github.com`, no `os.UserHomeDir`, no `/tmp`, no
`exec.Command("sh"`, `"-c"`, and no `nova-merge`.

1. `pool-reads-issue-label`: a fixture `gh issue list` answering three open issues, two
   labelled `card` and one dogfood-shaped without the label, yields `candidates=3 issues=3`
   and three `pool.tsv` rows in issue order; a fourth issue with neither is nowhere.
2. `pool-reads-audit-lines`: one audit issue whose body carries two `MISSING:` lines and one
   `DRIFT` line yields `audits=3` with ids `<issue>#1..3`, template `drift`.
3. `pool-reads-bus-slices-and-roadmap-cells`: a bus checkout with one note carrying a
   `slices:` block of four, and a roadmap file with two cells naming `:card fix` and one
   naming none, yield `slices=4 roadmap=2`; a body with a `slices:` block under the `issues`
   source is `plan=1` and not pooled.
4. `pool-refuses-unreadable-source`: a `gh` exiting 1, a `bus` path that is not a checkout,
   and a roadmap file that does not parse are each one `POOL REFUSED` naming the source and a
   remedy, exit 2, and no `pool.tsv` exists afterwards — the mutation that matters: a run
   that wrote a shorter pool and said `OK`.
5. `pool-skips-seen`: a `seen.tsv` with one candidate `carded` and one `retry` yields the
   `retry` one in `pool.tsv` and the `carded` one in `seen=1`, not in the pool.
6. `cut-line1-is-contract`: every card written has line 1 `RESULT <label> sha=<sha12>`, the
   hash equal to SHA-256 of the bytes below line 1, and `STEP 1` carrying `mkdir -p scratch`,
   `TMPDIR`, an `https://` clone and `checkout -b`; a template whose rendered line 1 is
   prose, or whose `STEP 1` clones `git@`, is `CUT REFUSED` naming the rule, no card written.
7. `cut-text-template-forbids-build`: `read`, `text` and `tone` cards each carry the no-build
   line; a `text.md` fixture lacking it is `CUT REFUSED template=text`; `fix`, `replay` and
   `drift` cards carry the red-then-green row rule; no template mentions `../scratch`.
8. `cut-model-by-kind`: six candidates, one per kind, yield `cards.tsv` with `flash` on
   `read`, `text`, `tone` and `pro` on `fix`, `replay`, `drift`, ids from `models.tsv`;
   `flash=3 pro=3`; a candidate with template `probe` is `skipped=1`, one `CUT SKIPPED` line,
   a `skipped.tsv` row, and exit 1.
9. `launch-refuses-under-slots`: eight cards, four free slots, no `--queue`: `PULSE REFUSED
   UNDER-SLOTS cards=8 free=4`, exit 2, the fixture `nova-swarm` recording zero runs.
10. `launch-queues-remainder`: the same with `--queue`: `nova-swarm batch` runs with a
    `--tasks` dir holding exactly the first four cards in `cards.tsv` order, `queued=4`,
    `queue.tsv` holding the other four, `pulses/<id>.tsv` naming the batch.
11. `launch-slot-free-needs-both`: a slot file `state=free` whose `native.log` was written
    30 s ago is not free; a slot file absent whose `native.log` is 130 s old is; `free-before`
    counts only the second — the mutation that matters: a slot freed on the file alone.
12. `launch-every-card-through-batch`: two model routes present run exactly two `nova-swarm
    batch` invocations and zero `nova-swarm add`, each with `--label pulse-<id>` and a
    `--then` whose argv begins `nova-pulse harvest --id <id>`; a `BATCH REFUSED` fixture
    reply is `PULSE REFUSED` with that reason, `queue.tsv` unchanged.
13. `harvest-pushes-only-on-line1-match`: three done cards — line 1 equal, line 1 differing
    by one byte, line 1 equal with `BRANCH main` — push exactly one, by `git push <https>
    <branch>:<branch>`, open exactly one draft PR whose body is the `RESULT.md` lines, and
    print `pushed=1 prs=1 mismatch=2`, exit 1; a bare `git push` in the fixture's argv log is
    the mutation that matters.
14. `harvest-abstain-goes-to-retry`: one `ABSTAIN -- idle 300s` row and one card with no
    `RESULT.md` yield `abstain=2 retry=2`, two `retry.tsv` rows each carrying the card path
    and the harness log's last `permission`/`refused` line, no push, no PR, no
    `nova-swarm requeue` in the argv log, `seen.tsv` rows `retry`.
15. `harvest-cuts-read-card-per-pr`: every PR opened yields one row in `next.tsv` with
    template `read`, and the next `pool` counts it in `next=<n>` after `queue.tsv` rows.
16. `harvest-relaunches-queue-first`: `queue.tsv` with three rows, `next.tsv` with one, a
    source with two new items, five free slots: the next batch's `--tasks` dir holds the
    three queued cards first, then the read card, then one source card; `queued=1`; the
    `HARVEST OK` line precedes the `PULSE OK` line and both are printed.
17. `harvest-usd-is-the-batch-line`: `usd=` on `HARVEST OK` equals the swarm packet's
    `BATCH … usd=<sum>` byte for byte, `-` when the packet says `-`; no verb's argv log
    shows a model id outside `nova-swarm batch --model`.
18. `width-under-width-exits-2`: `pool.tsv` with two rows, two free slots, nothing in
    flight: `PULSE UNDER-WIDTH pool=2 free=2: launch`, exit 2, and the fixture `nova-swarm`
    records zero runs — `width` launches nothing.
19. `width-pool-empty-says-so`: an empty pool and an empty queue with three cards in flight:
    `PULSE POOL EMPTY in-flight=3`, exit 0; `harvest` at the same state prints it as its last
    line and runs no `batch`; the word `padding` and any invented candidate is nowhere.
20. `then-gated-on-verdict`: a done card whose PR the fixture `gh` reports as not mergeable
    and whose hosted check is red is still pushed, still `prs=1`, and `nova-merge` appears in
    no argv log; a card with line 2 `BLOCKED head=…` is `mismatch` on the count and not pushed.
21. `output-bounded-at-the-largest-plausible-state`: 200 done cards and 60 abstains print at
    most `2 * --max + 6` lines over both streams with a `MORE` line per capped kind; every
    value is one token; `--max 0` prints all; `--max -1` is exit 2.
22. `every-refusal-names-its-remedy`: every refusal in the package lives in one table the
    test walks; each ends in a parenthesised remedy, and removing one turns the test red.
23. `pool-reads-work-nodes`: a `work` source with one open, unleased, unblocked bug node and
    one open, unleased, unblocked item node yields `work=2`, two `pool.tsv` rows whose `id` is
    the node id, and `candidates=2`; a node that is leased or blocked is nowhere; the id is
    carried on the card's line 1 so `harvest` records the attempt on the node.
24. `handoff-writes-record-and-note`: `handoff --to <name>` ends the shift — `SHIFT END` on
    stdout, the loop stopped — writes `<root>/queue/OWNER` freed and `<root>/queue/HANDOFF`
    with the last width, in-flight by bench, pending, escalations, benches and state, and the
    fixture bus records one note to the successor carrying the record; `HANDOFF OK
    to=<name> inflight=<n> pending=<n> escalations=<n>`.
25. `handoff-refuses-asleep-successor`: the fixture `nova-wake awake <name>` exits non-zero —
    `HANDOFF REFUSED` naming the remedy, exit 2, no record, no note, `OWNER` unchanged.
26. `handoff-finishes-harvest-first`: a harvest in progress is finished before the handoff
    proceeds; a fixture mid-harvest is refused with the remedy and the harvest's line is
    still printed.
27. `takeover-refuses-live-owner`: `OWNER` names a live process on a reachable host —
    `TAKEOVER REFUSED owner=<name> pid=<n> host=<h>`, exit 2, nothing taken.
28. `takeover-takes-stale-lock-with-note`: `OWNER` names a dead process or an unreachable
    host — the lock is taken, one `NOTE` line says so, and the loop and a manager shift start on
    the same queue.
29. `takeover-inherits-queue`: the taken `HANDOFF` record's inflight, pending and
    escalations are inherited and printed as `TAKEOVER OK from=<name>
    inherited=<inflight/pending/escalations>`.
30. `status-is-bounded`: at the largest plausible state — every bench and every friend in
    scope — `status` prints at most `--max` lines over both streams with one `MORE` line per
    capped kind; every value is one `internal/oneline` token.
31. `status-contraction-from-counts`: the `CONTRACTION` hour and day lines equal the counted
    cards `cut/done`, prs `opened/merged` and issues `filed/closed` from the queue,
    `usage.tsv` and `gh`; a line built from a list or a report body is the mutation that
    matters.
32. `status-adoption-includes-coordinator`: the `ADOPTION` lines name every friend, the
    coordinator included, one line each with `version`, `receipt` and `edges`; a missing
    coordinator line is red.
33. `status-remaining-counts-in-scope-only`: `REMAINING` counts queue rows, unread PRs, dirty
    PRs and uncarded issues in scope only — a bench or friend outside `--roots <dirs>` is
    nowhere on the line.

## Open questions — each with a default, and the default stands unless Glenn says otherwise

1. **One pulse id over two batches.** `nova-swarm batch` takes one `--model`, so a pulse
   with both routes is two admissions. Default: one pulse id, `pulses/<id>.tsv` naming each
   batch, and `harvest --id` taking the pulse id; `batches=<n>` on the `PULSE` line says
   how many. Rejected: a per-card model in the swarm's sidecar, which is a swarm change.
2. **`UNDER-WIDTH` at exit 2.** SPEC.md's table reads a failed check as exit 1. Default: 2,
   as asked, because the alarm is a state the coordinator must act on and nova-wake fires
   on refusals; if the estate reads 1 as the right code, it is one constant and one replay.
3. **The dogfood issue shape without the label.** Default: pooled, template `fix`, because
   the shape is the contract (tool, command, verbatim output, expected, smallest fix) and a
   label is a hand's afterthought. Rejected: label only, which loses first-run stumbles.
4. **This spec needs `nova-swarm batch --then` (card 269) first.** Until it lands, `launch`
   admits and prints its line, and `harvest` is run by a person after `nova-swarm wait`;
   nothing else in this draft depends on it.
