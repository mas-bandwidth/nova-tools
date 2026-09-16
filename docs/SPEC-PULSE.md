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
 │  candidates     │  every free slot is filled with admitted work;│  done: push, PR, read card
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
   a tab-separated file, one source per line: `kind`, `locator`, `template`. Six kinds:
   `issues` (`owner/repo` — open issues carrying label `card`, or the dogfood shape: tool,
   command, verbatim output, expected, smallest fix), `audits` (`owner/repo` — open issues
   whose body has a `MISSING:` or `DRIFT` line, one candidate per such line), `bus` (a
   nova-bus checkout — open notes whose body has a `slices:` block, one candidate per slice),
   `roadmap` (a lisp file under `docs/roadmaps/` — every cell whose `:card` names a template),
   `prs` (`owner/repo` — open, non-draft pull requests, one read candidate per PR),
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
   `kind`, `title`, `template`. `id` is the issue number, the PR number, the audit line's
   `<issue>#<n>`, the note id and slice ordinal, the roadmap cell name, or the work node id. `template` is
   the source's unless
   the item names one: an issue body line `template: <name>`, a slice's `template:` word, or
   the cell's `:card`. A candidate whose (`source`,`id`) is already in `<root>/seen.tsv` with
   state `carded`, `running` or `pr` is not re-pooled; a `retry` state is, so a rewritten
   card can go out (rule 14). Priority is source order and nothing else.
3. **Bounded work only.** A candidate is one issue, one PR, one audit line, one slice, one
   cell: one bounded item, one card. `pool` never splits an item and never merges two; an issue
   whose body says it is a plan (no `expected`, no `smallest fix`, a `slices:` block of its
   own) is counted in `plan=<n>` on the `POOL` line and not pooled — a plan is the bus's, and
   its slices arrive by the `bus` source.
4. **Six typed templates, in one directory, named by a flag.** `--templates <dir>` holds
   `read.md`, `fix.md`, `text.md`, `replay.md`, `drift.md`, `tone.md` and the cost table
   rule 7 reads (`benches.tsv`, or `routes.tsv`, whichever is there). A candidate whose
   template is not a
   file there is `skipped`, counted on the `CUT` line, and its (`source`,`id`) goes to
   `skipped.tsv` with `no template <name>`; `cut` never falls back to another template.
5. **Every card is the practice-17 shape, and the template guarantees it.** Line 1 is the
   `RESULT` contract line, `RESULT <label> sha=<sha12 of the card text below line 1>`; line 2
   is the role and the wall (the job directory, the `TMPDIR` the runner already exported and
   a card never sets, never `/tmp`, `~` or `..`, no stdlib or toolchain source, the deadline
   held by the machinery). `STEP 1` is `mkdir -p scratch`, the https clone, `git checkout -b
   <branch>`, and no `TMPDIR` of its own: the runner exports `TMPDIR=<slot>/tmp/<label>`,
   outside every repo ([SPEC-SWARM.md](SPEC-SWARM.md), #460), and a card that sets its own
   puts its temp dir inside the job's repo; every step is numbered with one check; the final
   step writes `RESULT.md` with line 1 equal to the card's line 1, line 2 the verdict, then
   a `BRANCH <name>` line and a `REPO <owner>/<name>` line. Scratch notes live in the repo
   directory, never `../scratch`. `cut` refuses a template whose rendered card violates any
   of these — `CUT REFUSED template=<name>: <which>` — because a card that drifts here is a
   card that stalls.
6. **Text-only templates forbid the build.** `read`, `text` and `tone` carry the line `Do not
   run go build, go test or any toolchain; read and write only`, and `cut` refuses a text
   template that lacks it; `fix`, `replay` and `drift` carry rule 4 of WORKER-CARDS: red
   line then green line, one row per item.
7. **The route is the cheapest capable, from a cost table, never a hand.** `cut` reads the
   cost table `--benches <file>` (default `benches.tsv` beside `--templates`; a `routes.tsv`
   beside it is read the same way): one column per model, two rows — `cost`, a class
   `zero|flat|metered` with a `usd per Mtok`, and `capability`, `read|text|code|replay`. Per
   card class a model is capable when its capability covers the kind's (`read`, `text` and
   `tone` need `read|text|replay`; `fix` and `drift` need `code`; `replay` needs `replay`),
   and `cut` picks the capable model with the lowest average cost per token — `zero` beats
   `flat` beats `metered`, ties broken by `usd per Mtok` — so routing is mechanical: local is
   zero, Go is flat, Zen is metered. It prints `route=<model> reason=<class>` on its
   `CUT ROUTE` line, `class` the cost class. A retry after an abstain (rule 14) moves the pick
   one capability class up (`read` → `text` → `code` → `replay`), so a rewritten card routes
   to a stronger class. There is no `--model` flag on any verb: the table is the whole
   policy, in git, edited once. `cards.tsv` is five fields per line: `label`, `slot`, `model`,
   `tokens`, `card` (the card's path); `slot` is `-` until `launch` allocates, and `tokens` is
   the card's admission token bound, which rule 10 sums for the batch's `--tokens`.
8. **A slot is free when the swarm's slot lock files say so.** `launch` reads the swarm pool
   under `--root`: a slot is free when its slot lock file `<pool>/slots/<n>.json` is absent
   or `state=free` — the swarm's own lock files (issue #457), never a log age and never a
   120 s rule: a slot freed under a writer is the hurt of SPEC-SWARM rule 17. `free-before=<n>`
   on the `PULSE` line is that count before admission; `--slots <n>` caps it.
9. **Launch fills every free slot on every bench, and only with admitted work.** Maximum
   available throughput is the default, Glenn: every free slot rule 8 counts — under the
   per-tick headroom of **Rate and convergence** rule 3 — is filled in `cards.tsv` order and
   the rest appended to `<root>/queue.tsv`, `queued=<n>` says how many. A card is admitted
   when (a) the shift's spend ceiling would not be crossed by its route's cost class —
   `--spend-max <usd>` per shift, a `zero` or `flat` route counting zero and a `metered` one
   its `usd per Mtok` (rule 7); (b) its contract line has fewer than `--max-attempts`
   (default 2, rule 14's one requeue and no more, the manager policy's key of the same name)
   prior attempts across the queue; (c) its source is in the approved scope list
   — `--scope <file>`, one source per line, the pool sources by default. A refused card
   prints `ADMIT REFUSED card=<label> gate=<spend|attempts|scope> <value> (<remedy>)`, and
   its `queue.tsv` row carries the gate that refused it, so `width` can tell pending work
   from gated work (rule 16). An admission refusal is not a failed run: `launch` fills the
   slots it can and exits 0. There is no `UNDER-SLOTS` refusal any more — a pulse never
   refuses for being wider than the bench; the remainder queues, always.
10. **Every card goes through `batch`, never a single `add`.** `launch` runs exactly one
    `nova-swarm batch --pool <root>/pool --tasks <dir> --label pulse-<id> --deadline <s>
    --files <n> --tokens <n> --then "nova-pulse harvest --id <id> --root <root>"` per model
    route present, so at most two admissions, both under one pulse id recorded in
    `<root>/pulses/<id>.tsv` (`batch id`, `model`, `n`). `<n>` on `--files` and `--tokens` is
    that route's card count and summed token bound taken from its `cards.tsv` columns, so the
    admission is bounded exactly as the cards say. The `--then` argv is `nova-swarm batch`'s
    (card 269): it runs when the batch's wait ends — every card ended or the deadline — and
    never earlier. A `BATCH REFUSED` line from the swarm is relayed as `PULSE REFUSED` with
    the swarm's reason and nothing is queued.
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
13. **Every PR gets a read card in the next pool, routed local-first.** On open, `harvest`
    appends (`pr`, `<repo>#<n>`, `read`, `<title>`, `read`) to `<root>/next.tsv` — template
    `read`, or `tone` for a seed page — which the next `pool` reads after `queue.tsv` and
    before the sources. The read card's model comes from the bench cost table, cheapest
    route that can hold it (the local model first, per the cost-row issue), so reads stay
    off the paid routes. Reads go by verb, through cards, like everything else; a PR nobody
    is asked to read is a PR that waits.
14. **An abstain is requeued at most once, under a new card number, then escalated.** For
    every `ABSTAIN` row and every card with no `RESULT.md`, `harvest` counts it in
    `abstain=<n>` and triages it by its reason token as the manager does (#587): a first
    abstain is requeued once under a new number on the other bench, the attempt riding on
    the card; a second abstain of the same item is one escalation and a row in
    `<root>/retry.tsv` — `label`, `card path`, and the last line of `<job>/harness.log` that
    carries `permission`, `denied`, `refused` or `REFUSED` (else the log's last line),
    bounded to one line — never a third send. `retry.tsv` is a person's inbox: the card is
    rewritten, the row's `seen.tsv` state becomes `retry`, and only then does `pool` pick
    the item up again (rule 2). An abstain is a prompt defect
    ([WORKER-CARDS.md](WORKER-CARDS.md), practice 17's holder: *fix the prompt*).
15. **Harvest pulses again, queue first.** After the counts, `harvest` runs `pool`, `cut`
    and `launch` in that order, with `queue.tsv` rows first, then `next.tsv`, then
    the sources, and prints the next `PULSE` line as its own last line. When the pool and the
    queue are both empty it prints `PULSE POOL EMPTY in-flight=<n>` and exits 0: real work
    ran out, and the tool says so. It invents nothing to keep the slots warm.
16. **`width` is the alarm, and it is one line.** `PULSE WIDTH in-flight=<n> free=<n>
    pool=<n> queued=<n>` when the state is consistent; `PULSE UNDER-WIDTH pool=<n> free=<n>:
    launch` with exit 2 when `pool + queued > 0` and `free > 0`, where `pool` counts only
    admitted pending work — work is waiting and slots are idle, which is the drift Glenn
    named; `PULSE POOL EMPTY in-flight=<n>` when `pool = 0` and `queued = 0`. Admitted
    pending work is the `queue.tsv` rows with no gate recorded on them (rule 9) plus the
    `pool.tsv` rows not yet cut; `width` reads `pool.tsv`, `queue.tsv` and the slot files,
    takes no gate flag of its own, and launches nothing and writes nothing. nova-wake may run it on a clock; this tool has none.
17. **The cost model.** `nova-pulse` costs no model tokens: no verb calls a model, reads a
    report body, or summarises anything. Each card's usage is on the swarm's `CARD` line and
    summed on its `BATCH` line; `harvest` copies `usd=<sum>` onto `HARVEST OK` and adds
    nothing. The coordinator's cost per cycle is one `PULSE` line read and one `HARVEST`
    line read, so fewer turns in the window (Glenn 2026-09-14: input tokens are the win).
18. **Bounded output, per SPEC.md.** Every verb prints at most `--max` (default 20) event
    lines per kind — `HARVEST PR`, `HARVEST RETRY`, `CUT SKIPPED`, `ADMIT REFUSED` — then
    one `<TOKEN> MORE
    kind=<k> shown=<n> total=<t> <remedy>`; the count line prints on failure as well as
    success; every value is one `internal/oneline` token. No verb prints a list of titles.
    On `pool` and `cut`, `--max` also bounds the work, never just the print: `pool` admits at
    most `--max` candidates and `cut` writes at most `--max` cards, in pool order, so `CUT OK
    cards=<n>` is the bound when one was named (`--max 6` cuts six cards), and `0` means no
    bound. The rest of the pool is left for the next call — never dropped, never counted
    skipped.
19. **State is files under `--root`, all of them tab-separated, all of them in the open.**
    `pool.tsv`, `cards.tsv`, `queue.tsv`, `next.tsv`, `retry.tsv`, `skipped.tsv`, `seen.tsv`,
    `pulses/<id>.tsv`, and the cards under `cards/<pulse id>/`. No database, no lock of this
    tool's own: the swarm's `slots.lock` is the only lock touched, and only through `nova-swarm`.
    A missing `--root` is `refusing to guess`, exit 2.

## The verbs

```
nova-pulse pool    --sources <file> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--max <n>]
nova-pulse harvest --id <pulse id> --root <dir> --sources <file> --templates <dir> [--max-body-bytes <n>] [--max <n>]
nova-pulse manager --policy <file> --queue <dir> --roots <dirs> --bus <clone> --as <name> --hours <n>
nova-pulse width   --root <dir> --pool <pool.tsv>
nova-pulse version
nova-pulse help
```

Those lines are the string `nova-pulse help` must print, byte for byte — the parity is a
demand on `internal/pulse/cli.go`'s `pulseVerbs`, which carries the same claim in a comment,
and a replay walks it (replay 36). The rules name verbs and flags the shipped block does not
yet offer — `handoff`, `takeover`, `status`, rule 9's admission gates, `--work`, `--benches`
and `--timeout` on every spawning verb — and each of those gaps is named in the open
questions, not listed here, so a reader who types a line in this block never gets a flag
error. `--timeout <s>` (default 120) bounds every `gh`, `git`, `nova-bus`, `nova-wake` and
`nova-swarm` child, for SPEC-MERGE's reason (SPEC-MERGE.md:458); `pool` is the one verb that
carries it today, and the rest take it when they land. `harvest` takes `--sources` and
`--templates` because its last act is `pool` and `cut` again (rule 15). There is no
`--model`, no `--priority`, no `--retry`.

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
- `RATE` — `cards_per_hour`, `p50_s`, `p90_s`, `usd_per_card`, `parallelism`, from `usage.tsv`; with no measured usage in the window every metric reads `-` — a cost or latency never measured is unknown, never zero.
- `REMAINING` — `queue` rows, `unread_prs`, `dirty_prs`, `uncarded_issues`, `hours`, in scope only.
- `CONTRACTION` — cards `cut/done`, prs `opened/merged`, issues `filed/closed`, for the last hour and the day, counted, never from a report body, with one verdict.
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
STATUS RATE cards_per_hour=<n|-> p50_s=<n|-> p90_s=<n|-> usd_per_card=<x.xxxx|-> parallelism=<n.n|->
STATUS REMAINING queue=<n> unread_prs=<n> dirty_prs=<n> uncarded_issues=<n> hours=<n>
STATUS CONTRACTION hour cards=<cut/done> prs=<opened/merged> issues=<filed/closed> verdict=<CONVERGING|EXPANDING>
STATUS CONTRACTION day cards=<cut/done> prs=<opened/merged> issues=<filed/closed> verdict=<CONVERGING|EXPANDING>
STATUS ADOPTION <friend> version=<v> receipt=<n> edges=<n>
STATUS OPEN dogfood=<n> holds=<n> escalations=<n>
STATUS TOOLS merged_since_adoption=<n> <names>
```

**The verdict is the health metric** (Glenn, 2026-09-15, #549, #553). Contraction is tracked
every tick, and the `CONTRACTION` line's `verdict` is `CONVERGING`, or `EXPANDING` when any one
stream's ratio — cards `cut/done`, prs `opened/merged`, issues `filed/closed` — has been above
1 for two consecutive hours. `EXPANDING` means the spec work up front was not done properly or
the engineering practice is lax, and the coordinator names which, on the record, before another
implementation card goes out. The same verdict belongs on nova-work `check`, from the journal,
under #500. Replay: `status-expanding-after-two-hours-above-one`.

## Progress

`nova-pulse progress --queue <dir> --roots <dirs> [--day <d>]` answers "how fast, at what cost,
how long" from records and never from a guess (Glenn, 2026-09-15, #536; the line joins **The
verbs** block, which `help` prints byte for byte, when the verb lands). It reads every job's
`usage.tsv` under `--roots` (start, end, rc, usd) for the day and the queue's rows, no model,
two lines:

```
PROGRESS cards=<n> rc0=<n> wall_p50_s=<n> wall_p90_s=<n> usd_per_card=<x.xxxx> span_h=<n> effective_parallelism=<n.n> cards_per_hour=<n>
ESTIMATE remaining_cards=<n> hours=<n>
```

`effective_parallelism` is busy card-seconds over span seconds — what the benches did, not
what they had. `remaining_cards` is pending + launched + open PRs needing a read + 2 x open
in-scope issues; `hours` is `remaining x p90 / parallelism x 1.5`, conservative by that factor.
The `PULSE WIDTH` line carries `hours=<n>` from the same estimate. First measurement,
2026-09-15: 326 cards, p90 10 min, USD 0.18 per card, 44.6 cards per hour, effective
parallelism 3.5 against 64 slots — the benches were mostly idle across the day, which is the
number rule 3 of **Rate and convergence** answers. Replays: `progress-counts-only-the-day`,
`progress-parallelism-is-busy-over-span`, `estimate-uses-p90-and-parallelism`.

## Rate and convergence

The rules measured on 2026-09-15 and 2026-09-16 (#553), each one sentence here and its
mechanism where it lives. Rate is concurrency over wall per card; concurrency comes from pool
depth and headroom, never from slot count alone.

1. **The tick is the manager's, and it is 10 s.** The manager cycle's `wait-timeout` defaults
   to 10 s; every other verb is clockless. A card never waits for a tick to start: a card
   cut or requeued inside a cycle is launched in that cycle. Replay: `tick-never-delays-a-card`.
2. **The pool has a floor and fixed refill sources.** Every cycle the manager refills the
   queue to the policy's `floor` from the policy's `sources` (**The manager tier**, step 6,
   #587); the coordinator writes queues ahead, so the loop is never starved by the window.
   Replay: `pool-refills-to-floor`.
3. **Fill the machine, never oversaturate it.** Per tick, `launch` starts at most
   `cores x 1.5 - load` cards on a bench, never more than `cores` in one tick and never more
   than the bench's width, a measured power of two (SPEC-SWARM, **Benches**, `bench size`,
   #528; 1.25 was the first setting, Glenn raised it: be aggressive); a loaded or less
   capable bench drops down by its load without changing its width. `PULSE WIDTH` gains `headroom=<n>`
   per bench.
4. **A gated card launches itself.** A card carrying `AFTER: PR<n> merged` stays `gated` on
   the `QUEUE` line and is launched by the first cycle in which `gh` reports that PR merged —
   never by a person noticing. Replay: `gated-card-launches-on-merge`.
5. **No card pays a clone.** Where the policy names `mirror`, `launch` pre-clones the job's
   `repo/` from that bench-local mirror, refreshed by the upgrade loop, and the card's `STEP 1`
   tolerates an existing checkout (`git fetch` and `checkout <head>` when `.git` exists,
   the clone otherwise). Open in #553; until it lands `STEP 1` clones as **The card** shows.
6. **A refusal never takes neighbours down.** Admission, scoring and locks are per card, every
   abstain names one reason token — SPEC-SWARM, **scatter** and **gather**, landed in #577.
7. **A contraction phase is bugs only.** While the `CONTRACTION` verdict is `EXPANDING`, or a
   pit stop is open ([PIT-STOP.md](PIT-STOP.md)), the policy's `scope-regex` admits fixes with
   reproducing tests, reads and rebases; anything expansionary is labelled `next-push` in its
   issue and never carded. We choose to be done. Replay: `contraction-phase-cards-bugs-only`.
8. **The coordinator is a friend.** Every broadcast includes it; adoption is a mechanical step
   with a receipt; `status` names it on its own `ADOPTION` line (replay 34).
9. **Parallelism and time remaining are printed, never guessed** — `progress`, above.

The exit of a pit stop is a trust batch: the fix cards of the stop rerun as one batch and every
one scores `done` with its red line quoted, before the queue widens again
([PIT-STOP.md](PIT-STOP.md), **The exit gate**).

## Exit codes and the output grammar

Per SPEC.md: **0** the verb ran and the state it reports is consistent — a pool written, cards
cut, a batch admitted, a harvest folded, a width that is not under; **1** the tool saying NO —
a harvest with `mismatch > 0` or `abstain > 0`, a cut with `skipped > 0`; **2** could not run
— every refusal the rules name and `UNDER-WIDTH` (rule 16: the alarm is a state the
coordinator must act on, and it exits like a refusal so a wake fires on it). An
`ADMIT REFUSED` line is an event, not a verdict: it leaves the exit code alone (rule 9).

```
POOL OK sources=<n> candidates=<n> issues=<n> audits=<n> slices=<n> roadmap=<n> prs=<n> next=<n> plan=<n> seen=<n> took=<d> out=<path>
POOL REFUSED source=<kind>:<locator>: <reason> (<remedy>)
CUT OK cards=<n> skipped=<n> zero=<n> flat=<n> metered=<n> out=<dir>
CUT ROUTE route=<model> reason=<class>
CUT SKIPPED source=<kind> id=<id> template=<name>: no template
CUT REFUSED template=<name>: <which rule> (<remedy>)
PULSE OK id=<id> n=<n> free-before=<n> queued=<n> batches=<n> deadline=<s>
ADMIT REFUSED card=<label> gate=<spend|attempts|scope> <value> (<remedy>)
PULSE REFUSED: <reason> (<remedy>)
HARVEST PR repo=<owner/name> pr=<n> label=<label> branch=<name>
HARVEST RETRY label=<label> card=<path>: <last permission or refusal line, escaped>
HARVEST OK id=<id> done=<n> pushed=<n> prs=<n> abstain=<n> mismatch=<n> retry=<n> usd=<sum|-> took=<d>
HARVEST REFUSED id=<id>: <reason> (<remedy>)
PULSE WIDTH in-flight=<n> free=<n> pool=<n> queued=<n> headroom=<n> hours=<n>
PULSE UNDER-WIDTH pool=<n> free=<n>: launch
PULSE POOL EMPTY in-flight=<n>
HANDOFF OK to=<name> inflight=<n> pending=<n> escalations=<n>
HANDOFF REFUSED: <reason> (<remedy>)
TAKEOVER OK from=<name> inherited=<inflight/pending/escalations>
TAKEOVER REFUSED owner=<name> pid=<n> host=<h> (wait, or clear the stale lock)
PROGRESS cards=<n> rc0=<n> wall_p50_s=<n> wall_p90_s=<n> usd_per_card=<x.xxxx> span_h=<n> effective_parallelism=<n.n> cards_per_hour=<n>
ESTIMATE remaining_cards=<n> hours=<n>
<TOKEN> MORE kind=<k> shown=<n> total=<t> <remedy>
<TOKEN> NOTE <something true about this run that is not a finding>
```

`POOL`, `CUT`, `ADMIT`, `PULSE`, `HARVEST`, `HANDOFF` and `TAKEOVER` are the first tokens;
`OK`, `REFUSED`, `WIDTH`, `UNDER-WIDTH` and `POOL EMPTY` the verdicts and the **last** line of
a verb — except `ADMIT REFUSED`, which is an event line `launch` prints per gated card and
never its last; `harvest`'s last line is the `PULSE` line of the pulse it launched, or
`PULSE POOL EMPTY`. `OK` and `WIDTH` lines go to stdout, `REFUSED` and `UNDER-WIDTH` to
stderr. Every line is one line; a count stands where a list would be; every refusal carries
one remedy in parentheses.

## The card, as `cut` writes it

```
RESULT <label> sha=<sha12>
You are a worker. Job directory only; the TMPDIR the runner already exported, never one of your own; never /tmp, ~ or ..; no stdlib or toolchain source; the deadline is the machinery's: <s> s.
STEP 1. mkdir -p scratch && git clone -q https://github.com/<owner>/<name>.git . && git checkout -b <branch>
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

- **No model routing beyond the cost table.** One table in git, read by `cut`: the capable
  model with the lowest average cost per token, and one capability class up after an
  abstain. No fallback provider, no per-card override, no hand on a route; the table is the
  whole policy, and changing it is a diff somebody read.
- **No merging.** It opens draft PRs and cuts read cards. `nova-merge` and a person hold the
  lane, the gate and the read condition; the scatter/gather verbs never run `nova-merge`,
  never label, never mark ready — the manager tier hands approved PRs to the lane (it merges
  nothing itself; **The manager tier**).
- **No priority beyond source order.** `queue.tsv` first, `next.tsv` second, then the
  sources in file order, then the items in each source's own order. A person who wants an
  item first moves its source line up.
- **No cross-repo dependency graph.** Every card is one bounded item on one repo at one head;
  a card that needs another card's PR merged first is a card that is not bounded yet, and it
  waits in an issue until it is.
- **No third try.** A card abstains, is requeued once under a new number (rule 14, #587),
  and a second abstain is a person's: `retry.tsv` names it and the harness's last refusal, a
  person rewrites it, and the rewritten card is a new candidate.
- **No model call, no summary, no judgement.** It never reads a report body, never opens a
  transcript, never scores a finding. The swarm's packet is the whole read.
- **No clock of its own.** No daemon, no `--loop`, no `--watch`. The chain is `--then`;
  the alarm is `width`, run by nova-wake or a person. The one clock is the manager tier's
  cycle (#587), and its tick is **Rate and convergence** rule 1.

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

## Tests this spec demands

Acceptance replays, one per rule that can be made red, each proven able to fail by a mutation
first. Every `gh`, `git` and `nova-swarm` is a fixture on `PATH` that records its argv;
tripwires: outside the docs, no `api.github.com`, no `os.UserHomeDir`, no `/tmp`, no
`exec.Command("sh"`, `"-c"`, and no `nova-merge` except the manager tier's `nova-merge add`
handoff (rule **The manager tier**).

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
   an `https://` clone and `checkout -b`; a template whose rendered line 1 is prose, or
   whose `STEP 1` clones `git@`, is `CUT REFUSED` naming the rule, no card written.
7. `cut-text-template-forbids-build`: `read`, `text` and `tone` cards each carry the no-build
   line; a `text.md` fixture lacking it is `CUT REFUSED template=text`; `fix`, `replay` and
   `drift` cards carry the red-then-green row rule; no template mentions `../scratch`.
8. `cut-routes-by-capability-and-cost`: six candidates, one per kind, against a cost table
   whose zero-cost local covers `read|text`, whose flat route covers `code` and whose metered
   route covers `replay`, yield `cards.tsv` rows naming those models — the cheapest capable
   one per kind, never a route chosen by kind alone — and `zero=3 flat=2 metered=1` on
   `CUT OK`; a candidate with template `probe` is `skipped=1`, one `CUT SKIPPED` line,
   a `skipped.tsv` row, and exit 1.
9. `launch-fills-every-free-slot`: eight admitted cards and four free slots admit exactly
   four — every free slot filled, none left idle, exit 0 and no `UNDER-SLOTS` anywhere in
   either stream; the mutation that matters: a launch that refuses instead of filling.
10. `launch-queues-remainder`: the same run queues the rest with no flag asked for:
    `nova-swarm batch` runs with a
    `--tasks` dir holding exactly the first four cards in `cards.tsv` order, `queued=4`,
    `queue.tsv` holding the other four, `pulses/<id>.tsv` naming the batch.
11. `launch-slot-free-from-lock-files`: a slot whose lock file is `state=free` is free, one
    `state=busy` is not, a missing lock file is free; `free-before` counts the free ones and
    no `native.log` age or 120 s rule appears anywhere — the mutation that matters: a slot
    freed by a log age instead of the swarm's lock file.
12. `launch-every-card-through-batch`: two model routes present run exactly two `nova-swarm
    batch` invocations and zero `nova-swarm add`, each with `--label pulse-<id>`, a `--files`
    equal to that route's card count, a `--tokens` equal to the sum of that route's `tokens`
    column in `cards.tsv`, and a
    `--then` whose argv begins `nova-pulse harvest --id <id>`; a `BATCH REFUSED` fixture
    reply is `PULSE REFUSED` with that reason, `queue.tsv` unchanged.
13. `harvest-pushes-only-on-line1-match`: three done cards — line 1 equal, line 1 differing
    by one byte, line 1 equal with `BRANCH main` — push exactly one, by `git push <https>
    <branch>:<branch>`, open exactly one draft PR whose body is the `RESULT.md` lines, and
    print `pushed=1 prs=1 mismatch=2`, exit 1; a bare `git push` in the fixture's argv log is
    the mutation that matters.
14. `harvest-abstain-goes-to-retry`: one `ABSTAIN reason=idle=300` row and one card with no
    `RESULT.md`, each on its first attempt, yield `abstain=2` and two requeues under new
    numbers on the other bench; the same two abstaining again yield `retry=2`, two
    `retry.tsv` rows each carrying the card path and the harness log's last
    `permission`/`refused` line, no push, no PR, no third batch argv, `seen.tsv` rows `retry`.
15. `harvest-cuts-read-card-per-pr`: every PR opened yields one row in `next.tsv` with
    template `read` (or `tone` for a seed page), carrying the bench cost table's cheapest
    model that can hold it, and the next `pool` counts it in `next=<n>` after `queue.tsv`.
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
24. `cut-picks-cheapest-capable-route`: a benches table with a zero-cost local, a flat Go and
    a metered Zen, and one card per capability class, yields `route=<model> reason=<class>`
    on `CUT ROUTE` for the cheapest capable model — the mutation that matters: a pick that
    ignores cost and takes a route by kind.
25. `retry-moves-one-class-up`: a card rewritten from `retry.tsv` after an abstain routes one
    capability class above the first attempt's, so `CUT ROUTE` names a stronger class and a
    `reason` that reflects it.
26. `handoff-writes-record-and-note`: `handoff --to <name>` ends the shift — `SHIFT END` on
    stdout, the loop stopped — writes `<root>/queue/OWNER` freed and `<root>/queue/HANDOFF`
    with the last width, in-flight by bench, pending, escalations, benches and state, and the
    fixture bus records one note to the successor carrying the record; `HANDOFF OK
    to=<name> inflight=<n> pending=<n> escalations=<n>`.
27. `handoff-refuses-asleep-successor`: the fixture `nova-wake awake <name>` exits non-zero —
    `HANDOFF REFUSED` naming the remedy, exit 2, no record, no note, `OWNER` unchanged.
28. `handoff-finishes-harvest-first`: a harvest in progress is finished before the handoff
    proceeds; a fixture mid-harvest is refused with the remedy and the harvest's line is
    still printed.
29. `takeover-refuses-live-owner`: `OWNER` names a live process on a reachable host —
    `TAKEOVER REFUSED owner=<name> pid=<n> host=<h>`, exit 2, nothing taken.
30. `takeover-takes-stale-lock-with-note`: `OWNER` names a dead process or an unreachable
    host — the lock is taken, one `NOTE` line says so, and the loop and a manager shift start on
    the same queue.
31. `takeover-inherits-queue`: the taken `HANDOFF` record's inflight, pending and
    escalations are inherited and printed as `TAKEOVER OK from=<name>
    inherited=<inflight/pending/escalations>`.
32. `status-is-bounded`: at the largest plausible state — every bench and every friend in
    scope — `status` prints at most `--max` lines over both streams with one `MORE` line per
    capped kind; every value is one `internal/oneline` token.
33. `status-contraction-from-counts`: the `CONTRACTION` hour and day lines equal the counted
    cards `cut/done`, prs `opened/merged` and issues `filed/closed` from the queue,
    `usage.tsv` and `gh`; a line built from a list or a report body is the mutation that
    matters.
34. `status-adoption-includes-coordinator`: the `ADOPTION` lines name every friend, the
    coordinator included, one line each with `version`, `receipt` and `edges`; a missing
    coordinator line is red.
35. `status-remaining-counts-in-scope-only`: `REMAINING` counts queue rows, unread PRs, dirty
    PRs and uncarded issues in scope only — a bench or friend outside `--roots <dirs>` is
    nowhere on the line.
36. `help-is-the-verbs-block-byte-for-byte`: `nova-pulse help` prints this file's **The
    verbs** block byte for byte — the test reads the fenced block out of
    `docs/SPEC-PULSE.md` and compares, so a flag added here and not there is red, and so is
    a flag added there and not here; the mutation that matters: a verb line edited on one
    side only.
37. `status-expanding-after-two-hours-above-one`: a queue whose `cut/done` is above 1 for two
    consecutive hours prints `verdict=EXPANDING`; one hour, or a ratio at 1, is `CONVERGING`;
    the verdict is computed from the counts and not from any word in a note.
38. `progress-counts-only-the-day`: `usage.tsv` rows from two days under `--roots` yield
    `cards=` equal to the `--day` rows alone; `rc0` counts the rows with `rc=0`.
39. `progress-parallelism-is-busy-over-span`: four cards of 600 s each inside one 1200 s span
    print `effective_parallelism=2.0`; the slot count is nowhere in the arithmetic.
40. `estimate-uses-p90-and-parallelism`: `hours` equals `remaining x p90 / parallelism x 1.5`
    to one decimal, and `remaining_cards` counts pending, launched, unread PRs and twice the
    in-scope open issues — an out-of-scope issue moves nothing.
41. `tick-never-delays-a-card`: a card cut by a requeue inside a cycle appears in that cycle's
    `nova-swarm batch` argv, not the next cycle's.
42. `pool-refills-to-floor`: a queue at `floor - 3` with five candidates in the sources gains
    exactly three rows in one cycle, deduplicated on PR number, issue number and contract line.
43. `gated-card-launches-on-merge`: a card carrying `AFTER: PR7 merged` is `gated=1` while the
    fixture `gh` reports PR 7 open and is in the batch argv of the first cycle after the
    fixture reports it merged, with no other input.
44. `contraction-phase-cards-bugs-only`: with `verdict=EXPANDING`, a candidate whose issue
    carries the label `next-push` is `skipped` on the `CUT` line and never cut; a fix candidate
    with a `red:` line is cut.
45. `admit-refuses-over-spend`: a shift whose `--spend-max` a `metered` route card's class
    would cross yields `ADMIT REFUSED card=<label> gate=spend <usd> (<remedy>)`, the card
    queued with `spend` on its `queue.tsv` row, the run still exit 0; `zero` and `flat` route
    cards count zero.
46. `admit-refuses-third-attempt`: a contract line with two prior attempts across the queue is
    refused at the default `--max-attempts 2` — `ADMIT REFUSED card=<label> gate=attempts 2`;
    a first attempt is admitted.
47. `admit-refuses-out-of-scope`: a card whose source is not in `--scope` is
    `ADMIT REFUSED card=<label> gate=scope <source>`; with no `--scope` the pool sources are
    the scope and all are admitted.
48. `under-width-counts-admitted-only`: a pool of one admitted and one gate-refused card with
    free slots is `PULSE UNDER-WIDTH pool=1 free=<f>: launch`, exit 2 — the refused card is
    not counted.
49. `cut-step-one-sets-no-tmpdir`: a template whose `STEP 1` only mkdirs, clones over
    `https://` and checks out is cut with no `TMPDIR` on the line, and one whose `STEP 1`
    sets a `TMPDIR` is `CUT REFUSED` naming the rule, no card written — the runner exports
    `TMPDIR` outside every repo (#460), and a card that set its own put its temp dir inside
    the job's repo, the red cards 247, 266 and 353 reported and did not cause.
50. `pool-reads-open-non-draft-prs`: a `prs` source with two open non-draft PRs and one open
    draft yields `prs=2` on the `POOL` line and two `pool.tsv` rows of kind `read`, template
    `read`, one candidate per PR — the draft is nowhere, and the read candidate is the same
    shape harvest's own read card has (rule 13).

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
5. **What the shipped tool is behind on, named rather than assumed.** This is a draft, and
   `## Tests this spec demands` says so: the replays are demanded of the implementation, not
   read off it. Three deltas are open against `internal/pulse` at this draft's head, each one
   card's work: `pulseVerbs` lacks `--work` on `pool`, the `handoff`, `takeover` and `status`
   lines, and this draft's `--benches` and `--timeout`; `cut.go` prints `flash=<n> pro=<n>` on
   `CUT OK` where rule 7's cost table gives `zero=<n> flat=<n> metered=<n>`, and writes
   `cards.tsv` with four fields where rule 7 gives five; and `TestCutModelByKind` pins the
   routing replay 8 replaces. A fourth is rule 9's: `internal/pulse/launch.go` still writes
   `PULSE REFUSED UNDER-SLOTS` and `launch_test.go` pins it, `pulseVerbs` still offers
   `[--queue]` and carries none of the three gate flags — one card retires the refusal, turns
   that test into replay 9, drops `[--queue]` and adds the gates, in that order, so the red is
   the removal and never a silent behaviour change. Default: the spec leads, the cards follow,
   and no rule here is softened to match code that has not been written. Rejected: documenting the code as it is,
   which is how a draft stops being a design.
