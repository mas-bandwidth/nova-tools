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
- `nova-pulse launch` allocates free slots, admits the cards as one batch, and queues what did
  not fit.
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
pool ──► cut ──► launch ══(nova-swarm batch: pool admission)══► harvest ──► pool ──► …
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
   SPEC-AHEAD: #466 — `work` (a nova-work checkout — bug nodes and item nodes that are
   open, unleased and unblocked, one candidate per node). A `work` node is unleased when no
   `launch` currently holds it and unblocked when it is not waiting on a merge; the node id
   rides on the card's line 1 (rule 5) so `harvest` can record the attempt on the node
   (rule 11).
   A `work` checkout carries its poolable nodes in `<root>/nodes.tsv`, one tab-separated
   row per node — `id`, `type` (`bug` or `item`), `state`, `lease` (empty when unleased),
   `blocked-by` (empty when unblocked), `title`, and an optional template override — and
   `pool` counts the source in `work=<n>` on the `POOL` line; a checkout with no readable
   nodes file is one `POOL REFUSED` like every other unreadable source.
   A missing `--sources` is `refusing to guess`, exit 2. A source that cannot be read — a
   `gh` non-zero, a bus checkout that is not one, a roadmap file that does not parse — is one
   `POOL REFUSED` line naming the source and the remedy, exit 2, and no `pool.tsv` is written:
   a pool with a source silently missing would read as *no work* (the same law as nova-update
   rule 7: a dead source is never green).
2. **`pool.tsv` is five fields, one candidate per line, in source order.** `source`, `id`,
   `kind`, `title`, `template`. `source` is the locator the sources line declares — an
   `owner/repo` for the `issues` kind, so a template's `<source>` renders the repo the card
   is about, never the source kind (#631: the card cloned `github.com/issues.git`). `id` is
   the issue number, the PR number, the audit line's
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
   beside it is read the same way): one column per model, three rows — a `model` row naming
   each model, then `cost`, a class `zero|flat|metered` with a `usd per Mtok`, and
   `capability`, `read|text|code|replay`. Per
   card class a model is capable when its capability covers the kind's (`read`, `text` and
   `tone` need `read|text|replay`; `fix` and `drift` need `code`; `replay` needs `replay`),
   and `cut` picks the capable model with the lowest average cost per token — `zero` beats
   `flat` beats `metered`, ties broken by `usd per Mtok` — so routing is mechanical: local is
   zero, Go is flat, Zen is metered. It prints `route=<model> reason=<class>` on its
   `CUT ROUTE` line, `class` the cost class. A retry after an abstain (rule 14) moves the pick
   one capability class up (`read` → `text` → `code` → `replay`), so a rewritten card routes
   to a stronger class. There is no `--model` flag on any verb: the table is the whole
   policy, in git, edited once. Until that cost table ships, the shipped `cut` reads
   `models.tsv` beside `--templates`, one line `flash <id>` and/or one line `pro <id>`; a
   table that names only one holds every card to that model, so `flash <id>` alone is the
   spend rule "flash only tonight" (issue #635). `cards.tsv` is four fields per line: `label`, `slot`, `model`,
   `card` (the card's path); `slot` is `-` until `launch` allocates. Admission is the card
   form of `nova-swarm batch`, which takes the TSV whole and carries the `[launch] files`
   file budget (default 40, rule 10).
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
 10. **Every card goes through `batch`'s card form, never a single `add`.** `launch` runs
     exactly one `nova-swarm batch --id <pulse> --cards <admitted.tsv> --deadline <s>
     --runner <cmd> --root <root> --files <n> --then "nova-pulse harvest --id <id> --root
     <root>"`, with `<admitted.tsv>` the admitted cards written under
     `<root>/cards/<id>/cards.tsv` (the cards that fit the free slots, never the queued
     remainder), `<cmd>` the deployment's native runner on PATH,
     `nova-native-runner.sh`, and `<n>` the `[launch] files` file budget (default 40,
     the shim's own number). `nova-swarm batch` refuses an admission that names no budget
     ("--files is required and is at least 1, got 0"), so the launch always carries one
     (issue #869). The card form is the only form that runs a card: the swarm's pool form
     (`--pool --tasks --label`) wants `--tokens` as well, which no launch flag can supply
     (issue #630), so `launch` never calls it. The one
     admission is recorded in `<root>/pulses/<id>.tsv` (`batch id`, `n`). The `--then` argv
     is `nova-swarm batch`'s (card 269): it runs when the batch's wait ends — every card
     ended or the deadline — and never earlier. A `BATCH REFUSED` line from the swarm is
     relayed as `PULSE REFUSED` with the swarm's reason and nothing is queued. `launch` takes
     an optional `--routes <routes.tsv>`: with it, every card's worker is a typed decision —
     the shared core behind `nova-swarm route`, [SPEC-DECIDE.md](SPEC-DECIDE.md) rule 8 —
     over the four questions, with `--floor` (default 0.9), `--key-env` (default
     `JEV_API_KEY`) and `--base-url`, and one batch runs per chosen worker description. Below
     the floor the card keeps its own model column as the default worker, and the line says
     so. Every decision appends one `ROUTE` line to `<queue>/ROUTES.log` beside the card's
     label and the time, so the floor is re-tuned from rows and never from a feeling. With no
     `--routes` the cards group by their model column exactly as before, and no `ROUTES.log`
     is written.
11. **`--then` is gated on the verdict, never on mergeability.** `harvest` disposes a card by
10. **Every card goes through `batch`, never a single `add`.** `launch` runs exactly one
    `nova-swarm batch --pool <root>/pool --tasks <dir> --label pulse-<id> --deadline <s>s
    --files <n> --tokens <n>` per model route present, so at most two admissions, both under
    one pulse id recorded in `<root>/pulses/<id>.tsv` (`batch id`, `model`, `n`). `<n>` on
    `--files` and `--tokens` is that route's card count and summed token bound taken from its
    `cards.tsv` columns, so the admission is bounded exactly as the cards say. Where
    `cards.tsv` carries no budget column, the budgets come from the configuration --
    `[launch] files` (default 40) and `[launch] tokens` (default the explicit word
    `unmetered`, which is what a native runner with no live token accounting has always
    meant). Neither is ever omitted: `nova-swarm batch` requires both and refuses to guess,
    so a launch that names none is refused before a card starts (issue #869). This is the
    pool/tasks admission mode: it enqueues work and returns, it does not wait and it holds no
    follow-on. `--then` is the gather mode's flag (`batch --cards --runner`), so `launch`
    never passes it; `launch`'s whole-seconds `--deadline` is spelled as the duration `<s>s`
    the swarm's `Sidecar.Deadline` parser requires, because a bare `120` parses as no deadline
    and silently falls back to the worker default (issue #534). A `BATCH REFUSED` line from
    the swarm is relayed as `PULSE REFUSED` with the swarm's reason and nothing is queued.
11. **`harvest` is gated on the verdict, never on mergeability.** `harvest` disposes a card by
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
    at `--max-body-bytes` (default 4096). One `HARVEST PR` line per PR. `harvest` reads the
    pool layout beside the slot layout: a card `launch` admitted into `<root>/pool` is folded
    from `pool/reports/<id>/RESULT.md`, with the task id mapped back to its card label through
    the task file's own label line (line 1, the card's `RESULT` contract line) or the sidecar's
    label, `done/` before `failed/`, latest task first — and a task in `failed/` whose
    `RESULT.md` exists with a first line equal to the card's `RESULT` line is harvested as a
    result, not a failure, because the contract decides, never the directory it sits in. When
    `--root` names a bare swarm root with no `cards.tsv` — the caller handed cards straight to
    `nova-swarm batch` — `harvest` folds every `<root>/<slot>/jobs/<label>/RESULT.md` under it,
    whoever put it there, with no push or PR withheld for want of a cards.tsv: with none to
    name the contract, the `RESULT.md`'s own line 1 is the contract, and the refusal stands
    only when neither the file nor a job dir is there.
    **Amended by `docs/SPEC-TOOLWORK.md` §1 (draft, 2026-09-19):** between this rule's
    verify and its push stands `nova-pulse accept` — the card's claim is executed, its test is seen
    red without its change, and a rejected card pushes nothing.
13. *SPEC-AHEAD: #467.* **Every PR gets a read card in the next pool, routed local-first.** On open, `harvest`
    appends (`pr`, `<repo>#<n>`, `read`, `<title>`, `read`) to `<root>/next.tsv` — template
    `read`, or `tone` for a seed page — which the next `pool` reads after `queue.tsv` and
    before the sources. The read card's model comes from the bench cost table, cheapest
    route that can hold it (the local model first, per the cost-row issue), so reads stay
    off the paid routes. Reads go by verb, through cards, like everything else; a PR nobody
    is asked to read is a PR that waits. The machinery is literal: a title naming a seed
    page cuts template `tone`, every other title cuts `read`, the kind staying `read`
    either way; the relaunch routes the read card to the cost table's cheapest capable
    route — `benches.tsv` beside `--templates`, or `routes.tsv` when that is what is
    there, one column per model with a `cost` row (`zero|flat|metered` plus `usd per
    Mtok`) and a `capability` row, `zero` beating `flat` beating `metered` and ties broken
    by `usd per Mtok` — so the zero-cost local model wins; with neither file present the
    relaunch keeps the kind-only route it has always written. Replay
    `harvest-cuts-read-card-per-pr` holds one card after #416.
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
    ([WORKER-CARDS.md](WORKER-CARDS.md), practice 17's holder: *fix the prompt*). When
    `nova-pulse run` is given `--decide`, each harvest makes one typed decision per newly
    finished task before it disposes of it: a task whose `needs_human` is at or above
    `--floor` is appended to `<queue>/HUMAN` as one line
    `HUMAN task=<id> reason=<r> conf=<c> card=<label>` and is not auto-retried, so a person
    reads the inbox instead of the loop retrying a task that needs them; a
    `provider_error` above the floor is requeued once by rule 14's own path; below the
    floor nothing changes and the pool's own class stands. The decision is the core of
    `nova-swarm triage --decide` (`internal/swarm.DecideFinished`), and the loop makes no
    model call of its own.
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
    **Glenn's token obligation, and its one hand.** `run` folds, once per tick after
    harvest and before sweep, every root's `<root>/pool/usage/*.tsv` into the queue's
    monthly ledger `<queue>/ledger-<YYYY-MM>.tsv` through `internal/tokens`'s fold-pool
    library (`FoldPool` and `WritePoolLedger`, SPEC-TOKENS **`fold-pool`**) -- a library
    call, never a child process. It prints one
    `FOLD rows=<n> tasks=<t> ledger=<path>` line for each root whose fold moved the ledger,
    and nothing when a root's fold changed nothing, because fold-pool upserts by
    `(day, provider, model, repo)`; the step is idempotent by construction, so a second tick
    over the same usage leaves the ledger byte-identical and the console quiet. The
    coordinator reads no ledger: the monthly report per model and per repo is folded with no
    hand on it, and a bench with no pool folds nothing and says nothing.
    Replay: `run-folds-pool-usage-into-the-monthly-ledger` holds it (#8361).
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
20. **The gate classifies a failing job by a typed decision behind a floor.** With
    `--decide [--floor 0.9] [--key-env JEV_API_KEY] [--base-url <url>]` the gate reads the
    failing job it already fetched and sends `internal/decide` one call: the state is the
    job's name, the last 60 lines of its log (escaped and capped as triage escapes evidence)
    and the open known-flaky patterns of `<queue>/FLAKY.txt`, one regex per line, and the two
    questions are `verdict` — a choice of `flaky` (a timing, network, runner or rate-limit
    failure unrelated to the change), `real` (a test or build failure caused by the code) or
    `unknown` (cannot tell from the tail) — and `same_class_as_known`, a noul. A `flaky`
    verdict at or above the floor, for a head with no `<queue>/RERUN-<sha>` marker, reruns the
    run's failed jobs once, writes the marker and prints `GATE RERUN sha=<s> job=<j>
    conf=<c>`; the job is never rerun twice for the same head. Anything else — `real`,
    `unknown`, an error, or any verdict below the floor — leaves the gate's STOP exactly as
    it is today and carries ` decide=<verdict> conf=<c>` on the `GATE RED` line, `decide=?`
    below the floor. A decision below the floor is a suggestion, never an authorization: the
    gate keeps today's behaviour as the fallback. Replays:
    `gate-reruns-a-flaky-job-once-per-head`, `gate-real-verdict-keeps-stop`,
    `gate-below-floor-changes-nothing`.

## Fleet

The fleet is the benches named in one file, kept in git. `--benches <file>`
is a tab-separated file, one bench per line: `name`, `ssh target`, `home`,
`mac` (the wake address; `-` when the bench never sleeps). A missing
`--benches` is `refusing to guess`, exit 2.

**The machines registry.** `--machines <file>` is the second file, and it says
what each machine IS: `name`, `ssh`, `os/arch`, `roles`, `seat`, `cores`,
`notes`, tab separated, where `roles` is a set from `{bench, runner,
coordination, services}`. THE LOCK (Glenn, 2026-09-18): **runner hosts are
CI-only** — no card, no probe and no load may be placed on a machine that
serves the merge group's shards. Every verb that puts work on a machine
resolves its `--bench` through the registry and refuses a machine whose roles
lack `bench`, by name and before any ssh:

```
FILL REFUSED bench=batman reason=runner-host remedy="..."
```

The reason is one of `runner-host`, `coordination-host`, `services-host`,
`not-a-bench`, `unknown-machine`. `nova-pulse fill`, the path a CARD takes,
**requires** `--machines`: without it the verb cannot tell a bench from a CI
runner host, and that is the one thing it may not guess. The fleet admin verbs
take it optionally and keep their older guard without it. A machine that is
both `runner` and `bench` must carry a dated exception in its notes,
`allow-shared=<YYYY-MM-DD> <why>`; hulk and vision carry one until the pull
worker runs cards in containers. `nova-pulse fleet registry` lists the file.

The fleet rule: every fleet verb prints one `FLEET <name>` line per bench,
runs the benches in parallel under `--timeout <s>` (default 120), exits
0/2/3 (0 ok, 2 drift-or-refused, 3 unreachable), takes ssh from `--ssh` so
tests fake it with a fixture on `PATH` and no test makes a network call to
any provider, and refuses `studio` for any admin act (`FLEET REFUSED
bench=studio`, exit 2) — the studio bench is never rebooted, suspended, or
secret-checked by this tool.

Verbs, one line each: `fleet survey` runs `tools/bench-standard.sh` on each
bench and folds the `STANDARD OK`/`DRIFT` lines; `fleet reboot` reboots each
bench and waits for the runners to listen again; `fleet secrets` runs the
seat check per bench (`nova-secrets check` passing, never a value printed);
`fleet suspend` sleeps the idle benches (solar: an idle bench sleeps instead
of burning the afternoon); `fleet wake` wakes by magic packet to the bench's
`mac`; `fleet standard` is the standard itself, run locally or over ssh,
whose checks are the contract below.

`fleet survey --benches <file> [--ssh <path>] [--timeout <s>] [--max <n>]` reads
the benches file, copies `tools/bench-standard.sh` over each bench's
`ssh <target> bash -s` stdin in parallel under `--timeout` (default 120), and
prints one `FLEET <name>` line per bench: every `DRIFT` line the standard emitted,
then its last line, both prefixed. It exits 0 when every bench says
`STANDARD OK`, 2 when any says `DRIFT`, and 3 when a bench is unreachable
(`FLEET <name> UNREACHABLE <error>`); `--max` caps the lines (0 lifts the cap)
and `--ssh` defaults to `ssh` on `PATH`, so the tests point it at a fake.

`fleet suspend` sleeps the idle benches: a bench holding a running card
(a lease or a job directory with a live pid under `~/rowan-swarm-root` or
`~/stella-swarm-root`) or a busy runner (a `Runner.Worker` process) is
`FLEET <name> BUSY <what>`, exit 2, and is never suspended unless `--force`;
`--if-idle` skips a busy bench instead of refusing it, and an idle one runs
`sudo systemctl suspend` and prints `FLEET <name> SUSPENDED`. `fleet wake`
builds the Wake-on-LAN magic packet in Go — six `0xFF` bytes then the bench's
`mac` sixteen times, to UDP broadcast port 9, no external tool — and then
polls ssh until the bench answers, printing `FLEET <name> AWAKE wall=<s>` or
`FLEET <name> WAKE TIMEOUT`, exit 3; a bench whose `mac` is `-` cannot be
woken and is `FLEET REFUSED bench=<name> no-mac`, exit 2.

`tools/bench-standard.sh` is the one admin entry for a Linux bench: it checks
the bench against the standard and prints `STANDARD OK ...`, or one
`DRIFT <what>` line per finding followed by `STANDARD DRIFT (see lines
above)`. It checks that each `$HOME/runner-nova-tools-<i>/` holds exactly one
listener process descending from `nova-runner-<i>.service` (runner checks run
only when `uname` is Linux), that each unit file carries `Environment=PATH`
with `go/bin` and `.local/bin`, `KillMode=control-group` and
`TimeoutStopSec=30s`, that `go version` equals `$NOVA_GO` (default
`go1.26.5`) with `sbcl` on `PATH` and the harness at
`$HOME/nova-bench/harness-<ver>/opencode`, that the 16 nova bins in
`$HOME/.local/bin` each report `$NOVA_WANT`, that exactly one `*.key` sits
under `$HOME/.config/nova-secrets` with `nova-secrets check` passing for it,
and that no plaintext key file (`$HOME/.local/share/opencode/auth.json`,
`$HOME/.config/deepseek/env`) and no literal `apiKey": "sk-` in
`$HOME/.config/opencode/*.json` survives; `--apply` kills stray runner
listeners not under their unit, nothing else destructive. The network probe
runs inside the real sandbox, never from the host, so what it reports is what
a card would see.

The same probe is the gate that admits a bench to the loop at all, and it is
the CI job that runs it: `fleet-probe` in `ci.yml`, dispatched with `bench` and
`slots`, one job per requested runner slot on that bench's Linux runners. It
builds `nova-sandbox` from the checkout and runs the network fetch inside it,
failing unless the fetch answers 200, so a bench enters the loop only after the
SANDBOXED probe is green. A host probe is never the evidence: #893 is the night
one passed while every sandboxed card died.

The sandbox network probe is Linux only and runs after the toolchain checks: it
makes a temp dir under `$HOME/nova-bench`, then runs `$HOME/.local/bin/nova-sandbox
--read $HOME/nova-bench --write <tmp> --cwd <tmp> -- curl -s -o /dev/null -w
'%{http_code}' https://models.opencode.ai/api.json` with `HOME=<tmp>/home`,
expecting `200`; any other code, including an empty reply, is `DRIFT
sandbox-network: curl inside nova-sandbox got http=<code> (want 200)`.
`NOVA_PROBE_URL` overrides the URL, so the test fakes the sandbox and the `curl`
behind it and no test touches the network.

The hurts, one line each: tonight's 97 ssh turns in the window is the cost
this section exists to remove; the bins drift Stella found is what `fleet
survey` catches before a card does; the resolver defect a host probe missed
is why the probe runs inside the real sandbox; the runner listener duplicates
are what one-listener-per-dir under its unit forbids.

Red tests, one per verb: `fleet-survey-folds-standard-lines` (a `DRIFT`
bench is exit 2 with its `FLEET <name>` line); `fleet-reboot-waits-for-runners`
(no `FLEET OK` before the listeners answer); `fleet-secrets-never-prints-a-value`
(the seat check passes and no key bytes appear on either stream);
`fleet-suspend-sleeps-only-idle` (a busy bench is `FLEET <name> busy`, never
suspended); `fleet-wake-sends-magic-packet` (the fixture ssh log shows one
magic packet to the bench's `mac`); `fleet-standard-lists-the-checks` (one
listener per runner dir under its unit, unit `PATH`/`KillMode`/
`TimeoutStopSec`, `go`/`sbcl`/harness, 16 bins at `NOVA_WANT`, one seat key
that opens, no plaintext keys, the sandbox network probe — remove one and the
test is red); `fleet-refuses-studio` (any admin verb with `studio` in
`--benches` is `FLEET REFUSED bench=studio`, exit 2, and the fixture ssh log
is empty).

`nova-pulse fleet reboot --benches <file> --bench <name>[,<name>] [--ssh <path>]
[--wait <duration, default 5m>] [--timeout <s>] [--max <n>]` boots each named
bench by `sudo systemctl reboot` (or `sudo reboot`) over ssh, then polls every
fifteen seconds until ssh answers and `nova-runner-1.service` is active in the
system or user scope, printing `FLEET <name> REBOOTED wall=<s> runners=<n>` or
`FLEET <name> REBOOT TIMEOUT after <wait>`. A bench not in the file is refused,
and `studio` is refused by name. Every fleet verb prints one `FLEET <name> ...`
line per bench, never more than `--max`, runs the benches in parallel under
`--timeout`, and takes ssh from `--ssh` so a test puts a fake on `PATH` and no
test reaches a machine.

`nova-pulse fleet secrets --benches <file> [--ssh <path>] [--timeout <s>]` runs the seat
check on every bench and prints one `FLEET <name>` line per bench: `FLEET <name> SEAT
<seat> check=OK head=<sha8> names=<n>`, `FLEET <name> SEAT <seat> check=REFUSED <reason>`
or `FLEET <name> NO-SEAT (found <n> keys)`. On a bench it runs `git -C
<home>/nova-bench/secrets pull --ff-only`, requires exactly one
`<home>/.config/nova-secrets/*.key`, and runs `nova-secrets check --store
<home>/nova-bench/secrets --as <seat> --key <path> --sops <home>/.local/bin/sops` for the
seat the key names. It never prints a value. The benches run in parallel under
`--timeout` (default 120); the verb exits 0 when every seat checked, 2 when any refused
or found no seat, and 3 when any bench was unreachable. ssh comes from `--ssh` (default
`ssh`), so a test fakes it and no test reaches a machine.

### The four bench scripts, retired

`fleet standard`, `fleet mirror`, `fleet join` and `fleet sleep` are the last
four hand-run bench scripts as verbs (#1142, "everything sketched becomes a
tool"). Each keeps the fleet rule: one bench per `--bench`, every path from a
flag with no default, ssh from `--ssh`, `studio` and an unknown name refused by
name before any ssh, exit 0/2/3, and one remedy line per refusal.

`nova-pulse fleet standard --benches <file> --bench <name> [--want <stamp>]
[--go <ver>] [--os linux|darwin] [--min-free <gb>] [--ssh <path>]
[--timeout <s>] [--max <n>]` holds one bench against the provisioning standard.
It prints one `STANDARD <bench> <check> OK got=<v>` or
`STANDARD <bench> <check> DRIFT want=<match>:<want> got=<v>` line per check and
then the verdict `FLEET <bench> STANDARD OK checks=<n>` or
`FLEET <bench> STANDARD DRIFT drift=<k>/<n>`. The checks are DATA, one table per
operating system, so the standard is read rather than traced through a shell
script: the Linux list is the Go toolchain at `--go` (default `go1.26.5`),
`sbcl`, the `safe-rm` helper, the nova stamp at `--want`, one seat key that
opens, and the free-space floor `--min-free` (default 25 GB); the darwin list is
the Mac bench standard — the Go SDK and `sbcl` under `~/sdk`, real git ahead of
the Xcode shim (`/usr/bin/git` is the shim, and the sandbox cannot read
`/var/db/xcode_select_link`), and every runner's `.path` carrying the real git
first — with the stamp, seat and space checks shared. `--os` names the list;
left out, the bench is asked with `uname -s`. The remote side prints
`CHECK<TAB>name<TAB>value` and nothing else: the verdict is decided in Go. This
retires `bench-standard.sh`, which refused to run anywhere but ON a Linux bench
and could say nothing at all about a Mac one.

`nova-pulse fleet mirror --benches <file> --bench <name> --repo <url>
--path <remote path> [--ssh <path>] [--timeout <s>]` creates the bare mirror a
card clones from (`git clone --reference`) or fetches the one already there, and
prints `FLEET <bench> MIRROR <path> created|refreshed head=<sha> size=<n>K`. It
deletes nothing. `--repo` must be an https remote and `--path` an absolute clean
path, both free of shell metacharacters, or the verb refuses before any ssh:
both are pasted into a remote command line. This retires `bench-mirror.sh`,
whose mirror root and repository list were hard-coded.

`nova-pulse fleet join --benches <file> --bench <name> --tailscale <path>
--authkey-env <NAME> [--ssh <path>] [--timeout <s>]` joins one bench to the
tailnet and prints `FLEET <bench> JOINED ip=<addr>`. The auth key is never a
flag value: it reaches the process ONLY through the environment variable
`--authkey-env` names, which `nova-secrets exec` fills for the length of the
call, and it reaches the bench on the remote shell's stdin, piped into
`tailscale up --auth-key=file:/dev/stdin --hostname=<bench>`, so it is in no
argv on either machine, and anything the bench says back is scrubbed of it
before a line is printed. An empty variable, a `--tailscale` that is not
absolute, and a bench the file does not carry are refusals before any ssh. This
retires `ts-join-one.sh`.

`nova-pulse fleet sleep --benches <file> --bench <name> [--ssh <path>]
[--if-idle] [--force] [--timeout <s>] [--max <n>]` puts ONE bench to sleep. It
is `fleet suspend` over one name, not a second implementation: one place decides
busy, so a lease or a job directory with a live pid under either swarm root, or
a `Runner.Worker`, is `FLEET <bench> BUSY <what>` and is never suspended. It
retires the Linux half of `fleet-sleep.sh` (the Mac half — `pmset` idle sleep —
is `nova-pulse sleep`, under "Mac bench power"): that script asked GitHub whether
the bench's RUNNERS were busy and knew nothing about the cards in flight on it,
so a bench working through nova-swarm looked idle and slept under its own work.

Red tests, one per verb: `fleet-standard-lists-the-checks` (the two lists as
data, and what both benches must carry in both); `fleet-standard-names-the-check-that-drifted`;
`fleet-mirror-creates-then-refreshes` (clone once, fetch after);
`fleet-join-keeps-the-auth-key-out-of-every-argv` (the key is on the remote
stdin and in no argv log, on neither stream); `fleet-sleep-refuses-a-bench-with-a-live-job`
(and the idle one is suspended through systemctl). Every one of them drives a
fake ssh on the test's own PATH that runs the remote script through `bash -s`
with fake sudo, git, tailscale and systemctl beside it, so no test reaches a
machine or opens a socket.

## Mac bench power

The Mac benches (batman, superman) draw 100 W each and the fleet runs on solar, so an idle
Mac sleeps and is woken on demand (Glenn 2026-09-17). `nova-pulse wake` and
`nova-pulse sleep` are the Go verbs that replace `scripts/coordination/fleet-wake.sh` and
`fleet-sleep.sh` (#1142); the shell's case table, its python heredoc over `ssh` and its
swallowed errors are not carried over. Both take the benches as a repeatable
`--bench <name>` or as bare arguments.

The verb lines, as `nova-pulse help` carries them:

```
nova-pulse wake    --bench <name>... --registry <file> [--timeout <duration, default 8m>]
nova-pulse sleep   --bench <name>... [--idle <duration, default 30m>]
```

`--registry` is one `name,mac,lan-bench` per line, blank lines and `#` comments skipped.
The file is validated whole before any ssh: a row with the wrong field count, an empty or
non-alias name, a mac that does not parse as six bytes, an empty `lan-bench`, or a duplicate
name refuses the verb, and an unknown `--bench` refuses the whole invocation rather than
waking the ones it knows.

To wake a bench the magic packet is built in Go -- six `0xFF` bytes then the six-byte mac
sixteen times -- and sent three times from the named `lan-bench` to the all-ones UDP
broadcast on port 9. Then ssh is polled for up to ninety seconds; a user-activity assertion
(`sudo -n pmset -a sleep 0; /usr/bin/caffeinate -u -t 5`) turns the dark wake a magic packet
gives (ssh answers, runners stay offline) into a full one; and the bench is awake only when
the runners whose names start with the bench show `online` in GitHub inside `--timeout`. The
line is `WAKE <bench> up after <s>s runners=<n>` or
`WAKE FAIL <bench> <stage> <reason>`, the stage one of `registry`, `packet`, `ssh`,
`assert`, `runners`; a bench a test already sees online prints `up after 0s`.

`sleep` refuses while any runner of the bench is busy (`SLEEP REFUSED <bench> busy=<n>`;
`busy=unknown` when GitHub cannot be read, refusing rather than guessing), and otherwise
sets idle sleep with `sudo -n pmset -a sleep <m> displaysleep 1 womp 1`, printing
`SLEEP <bench> idle=<m>`. A failure to reach the bench is `SLEEP FAIL <bench> <reason>`.

Red tests, one per behaviour: `power-wake-registry-refused-before-ssh` and
`power-wake-unknown-bench-refused-before-ssh` (no runner call before validation);
`power-wake-sends-packet-asserts-and-waits` (the packet goes from the `lan-bench` first,
then ssh, then the assertion, then the runner wait); `power-wake-fails-with-the-stage-that-did`
(no ssh inside ninety seconds is `WAKE FAIL ... ssh`); `power-wake-fails-when-no-runner-comes-online`;
`sleep-refuses-a-busy-bench` (a busy runner is never told to sleep);
`sleep-sets-idle-sleep`; the `power-registry-*` shape cases; and
`power-runner-timeout-kills-the-group` (a cancelled ssh child takes its whole process group
with it). Every remote step goes through one runner interface (`ssh <target> bash -s`, the
script on stdin) and the GitHub runner list is a second interface, so every test drives
fakes and no test opens a socket or reaches a machine.

## Fleet hygiene

`nova-pulse fleet hygiene --install` starts mechanical clean-as-we-work on the bench it runs
on (Glenn 2026-09-17, #1139): it writes the hygiene script and a ten-minute systemd timer
that runs it, so a bench never fills its disk while the loop sleeps. It is an admin act and
obeys the fleet rule: one `HYGIENE` line per run, one `FLEET <name>` line per bench, the
benches in parallel under `--timeout` (default 120), ssh from `--ssh` so a test fakes the
bench, exit 0 ok / 2 refused / 3 unreachable, and `studio` refused by name.

The verb line, as `nova-pulse help` will carry it:

```
nova-pulse fleet hygiene --install [--root <dir>] [--timeout <s>] [--max <n>]
nova-pulse fleet hygiene --dry-run [--root <dir>] [--timeout <s>] [--max <n>]
nova-pulse fleet hygiene --status --benches <file> [--ssh <path>] [--timeout <s>] [--max <n>]
```

It reads the bench's own trees and writes nothing outside them. Under
`$HOME/rowan-swarm-root` and `$HOME/rowan-working/tmp` a job is a slot's `jobs/<label>` or a
`working/tmp` `<guid>-<label>`, and the verb reads that job's `harness-output.log`, its
`RESULT.md` and its `.harvested` marker; it reads `_work/_temp` entry mtimes under
`$HOME/runner-nova-tools-*/`, `df` free space, the size of `$HOME/.cache/go-build`, and the
last `HYGIENE` line a previous run wrote to its log. `--install` writes
`$HOME/nova-bench/bench-hygiene.sh` and a system-scope `bench-hygiene.timer` (ten minutes),
both idempotent: a second `--install` rewrites nothing that already matches.

One line per run, every field named:

```
HYGIENE <host> slots=<n> reaped=<n> jobs-deleted=<n> slots-deleted=<n> dead=<n> diag-deleted=<n> diag-freed=<bytes> cache=<kept|kept-lease|dropped>(<size>G) free <a> -> <b>
```

`host` is `hostname -s`; `slots` the directories the run RECOGNISED as slots; `reaped` the
slot `data` homes, `tmp` dirs and scratch it deleted; `jobs-deleted` the jobs it deleted
whole; `slots-deleted` the empty slots it removed; `dead` the unleased jobs it took that had
left no `RESULT.md`, which is a different fact from a job somebody read; `diag-deleted` and
`diag-freed` the runner `_diag` files it took and the bytes they held; `cache` is `kept`,
`kept-lease` (a lease was live) or `dropped`, with the build cache's size in GB;
`free <a> -> <b>` is disk free space before and after. `--dry-run` walks the same trees and
prints one `WOULD <verb> <path>` line per deletion it would make, deleting nothing, so a
bench's first run is read before it is felt.

**The reaper deletes on a lease and an age, never on a shape** (issue #1499, after it ate a
certify tree, corpus and all, on two benches mid-pass; and issue #1512, which is the same
three rules reaching the Go verb that replaces the script). `scripts/bench-hygiene.sh` -- the
script the benches run today, and the one `--install` writes -- and `nova-pulse hygiene run`
both ask three questions, in this order. **Is it a slot?** A slot is the shape the launcher
makes, `<slot>/jobs`; a directory under either root without one is somebody's work and is
skipped whole, at any age, and is not even counted as a slot. **Is it leased?** `nova-swarm native` writes `<job>/.lease` before the child starts,
carrying its pid and a heartbeat it bumps every 30 s while the child runs, and removes it
when the run ends; a job whose lease names a live pid or whose heartbeat is under ten minutes
old is live and is never touched, and neither are the slot's `data` (the card's HOME) and
`tmp` (its TMPDIR) around it. `pgrep` and a process cwd stay as a second reason to KEEP; no
silence is ever a reason to delete, because one long model call and one long compile are both
silent. **Is it old?** An unleased job goes when it is harvested, or when nothing in it has
changed for six hours; an emptied slot goes only once it too has been quiet six hours, read
before the pass deletes anything under it. The build cache is never dropped while any lease
is live (`cache=kept-lease`). Replay: `scripts/bench-hygiene_test.sh`, and the same class
ported onto the Go verb in `cmd/nova-pulse/hygiene_reaper_class_test.go` (#1512).

**The Go verb reads the launcher's own lease record, not a second spelling of it.**
`nova-pulse hygiene run` asks `internal/swarm`'s `ReadJobLease` and `JobLease.Live`, which is
the judgement `nova-swarm native` itself makes about another run's lease: the pid wherever
this kernel can be asked, and the heartbeat against `swarm.JobLeaseStale` where it cannot. A
reaper that re-spells that rule is a reaper that can disagree with the launcher, and the way
it disagrees is by deleting a running card's directory. A `.lease` this process cannot read
at all -- a directory, or unreadable -- names an owner it cannot establish, so the job is
KEPT.

**The runner `_diag` prune is two rules, and one of them is a size cap.** Each
`$HOME/runner-*/_diag` is bounded by an age window, `--diag-days` (**two** days by default),
and then by a per-runner-directory cap, `--diag-max-bytes` (**2 GiB** by default), which
deletes the oldest file until the directory is at or below it. Only regular files directly
inside `_diag` count: a symlink is skipped by the `Lstat`, never followed, never counted and
never removed, and the newest file of a runner is never deleted by either rule, because the
runner process holds it open. Each deletion is `delete-diag <path>` in the action log and goes
through `internal/safepath` below that runner's own `_diag`.

The mistake that rule removes, measured on hulk on 2026-09-18: **24 runner directories held
3.5 GB of `_diag`, 18,296 files, and the oldest file on the whole bench was two days old.** A
runner rolls its own diagnostics at a rate nobody chose, so the **seven-day window the bench
ran with took nothing, ever**, and neither would three days. The two rules divide the job. On
a busy bench the window is what bites day by day: a `--dry-run` on hulk at `--diag-days 1`
selects 10,391 files and 1.95 GB. The cap is the backstop for a burst, or for a runner that
starts writing faster than anyone watches: a `--dry-run` at `--diag-max-bytes 104857600`
selects 6,374 files and 1.198 GB, oldest first, from the oldest file on the bench. The shipped
2 GiB takes nothing on hulk today, because no runner directory there is over 220 MB, and that
is what a ceiling is for.

The general lesson, and the one to carry to the next cleaner written: *a window over a tree
that rotates itself is not a bound. Set the window from the rate the tree actually rotates at,
and put a size ceiling behind it for the day the rate changes.*

Refusals are exit 2, one remedy line each: without root, `FLEET REFUSED bench=<name> no-sudo
(run it under sudo, or install the script by hand)`; without systemd, `FLEET REFUSED
bench=<name> no-systemd (install the timer by hand)`; `studio` from any hygiene act,
`FLEET REFUSED bench=studio (the reference bench is never cleaned by this tool)`; `--status`
with no `--benches`, `refusing to guess`; a bench that answers no `HYGIENE` line,
`FLEET <name> NO-HYGIENE (run fleet hygiene --install)`, exit 2, never a green line for a
bench nobody cleaned.

`--status --benches <file>` reads each bench's last `HYGIENE` line and its free space now,
one `FLEET <name> HYGIENE slots=<n> reaped=<n> jobs-deleted=<n> slots-deleted=<n>
cache=<kept|dropped>(<size>G) free=<a>G` per bench, so the coordinator sees a bench filling
before it fills.

The mistake it removes: two benches reached zero free in one night — 5 to 7 GB of slot data
home and a 30 GB build cache each, grown while nobody watched.

Red tests, one card writes them first; each fakes what the test cannot have:

1. `hygiene-install-writes-script-and-timer`: a fake `ssh` bench with a fake `systemctl` on `PATH` records the script and the ten-minute timer written once, and a second `--install` writes neither again.
2. `hygiene-install-refuses-without-sudo-or-systemd`: a fake `sudo` failing, and a bench whose fake `systemctl` reports no systemd, are each `FLEET REFUSED bench=<name>`, exit 2, and no script or timer is written.
3. `hygiene-dry-run-prints-would-and-deletes-nothing`: a fixture root holding a stale job, a harvested job and an empty slot prints one `WOULD rm -rf` line each and every fixture file survives.
4. `hygiene-never-touches-a-live-job`: with a fake clock, a job whose harness log is five minutes old and has no `RESULT.md`, and a job a fake `pgrep -f` names, are both untouched.
5. `hygiene-deletes-a-harvested-job-whole`: a job carrying `.harvested` is removed in one piece and counted in `jobs-deleted`.
6. `hygiene-deletes-an-unread-finished-job-after-six-hours`: with a fake clock, a job finished seven hours ago is deleted while one finished five hours ago stays.
7. `hygiene-deletes-empty-slots`: a slot with no `jobs` entry is deleted and counted in `slots-deleted`, and one holding a live job is kept.
8. `hygiene-prunes-runner-temp-older-than-a-day`: with a fake clock, a fixture `_work/_temp` entry older than a day goes and a younger one stays.
9. `hygiene-drops-the-go-cache-below-25g-free-or-above-20g`: a fake `df` reporting 20 GB free, and a fake `du` reporting a 25 GB cache, each drop `$HOME/.cache/go-build`, while 40 GB free and a 10 GB cache keep it.
10. `hygiene-status-prints-the-last-line-and-free-space-per-bench`: a fake `ssh` answering a stored `HYGIENE` line and a `df` output yields one `FLEET <name>` line per bench with the last counts and free space now.
11. `hygiene-refuses-studio`: any hygiene act with `studio` in `--benches` is `FLEET REFUSED bench=studio`, exit 2, and the fake ssh log is empty.
12. `hygiene-status-without-benches-refuses`: `--status` with no `--benches` is `refusing to guess`, exit 2, and no bench is contacted.
13. `hygiene-prunes-diag-older-than-two-days`: with a fake clock, a `_diag` log three days old goes, one a day old stays, and a runner whose whole directory is stale still keeps its newest file. The counts land in `diag-deleted` and `diag-freed`.
14. `hygiene-diag-size-cap-deletes-oldest-first`: four logs written today, every one inside the window, against `--diag-max-bytes 250`: the two oldest go, the run stops at or below the cap, and it takes no more than it has to.
15. `hygiene-diag-cap-is-per-runner-directory`: two runner directories each under the cap and together over it lose nothing, because the cap bounds a runner and not a bench.
16. `hygiene-diag-dry-run-prints-would-and-deletes-nothing`: `--dry-run` names the file it would take as `WOULD delete-diag <path>`, every byte survives and no action log is written.
17. `hygiene-diag-never-follows-a-symlink-out`: a symlink inside `_diag` pointing at a file outside the runner directory survives with its target, while a cap of one byte proves the prune did delete something.
18. `hygiene-diag-flags-refuse-nonsense`: `--diag-days` and `--diag-max-bytes` each refuse a non-number and a negative, exit 2, naming the flag; a prune never runs on a guess.
19. `hygiene-diag-default-cap-is-two-gibibytes`: the shipped defaults are pinned at two days and 2 GiB, so a change to either is a change to this test.

`fleet add <bench>` is the only door into the loop, and rule R (pit stop 4,
2026-09-16: nothing enters the loop untested) is why. It reads the fleet-probe
record — the `fleet-probe` job in `ci.yml` (#872), dispatched with
`bench=<label> slots=<n>`, one job per runner slot, read back by `runner_name` —
and refuses the bench unless every runner name on the record is green: the line
is `FLEET REFUSED bench=<name> runner=<first-not-green> run=<id>:
the fleet-probe is not all green (green=<n> of <n>)`, exit 2, and a record with
no jobs is refused the same way because a probe that ran nothing is not a probe.
A green record prints `FLEET ADD bench=<name> run=<id> runners=<n>` and writes
two values that no other verb and no hand writes: `<queue>/PULSE_ROOTS`, the
roots the loop holds, and `<queue>/runner-labels.tsv`, one admitted runner per
line. The red test is `fleet-add-refuses-a-bench-with-a-failed-probe-job`: a
fixture record with one failed job and one green job is refused with the failed
runner name and the run id on the line.

## The verbs

```
nova-pulse pool    --sources <file> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--benches <file>] [--bench <names>] [--routes <routes.tsv>] [--floor <f>] [--key-env <name>] [--base-url <url>] [--max <n>]
nova-pulse harvest --id <pulse id> --root <dir> [--sources <file>] [--templates <dir>] [--launched <dir>] [--done <dir>] [--failed <dir>] [--max-body-bytes <n>] [--max <n>] [--decide] [--floor 0.9] [--key-env JEV_API_KEY] [--base-url <url>]
nova-pulse harvest --bench <name> --root <bench root>[,<root>] --clone [<o/n>=]<dir>... [--session <id>] [--branch-prefix rowan/] [--base <branch>] [--since <d>] [--launched <dir>] [--done <dir>] [--failed <dir>] [--ssh <path>] [--max <n>]
nova-pulse beat    --queue <dir> --cairn <file> --title <text> [--resume <text>]
nova-pulse manager --policy <file> --queue <dir> --roots <dirs> --bus <clone> --as <name> --hours <n>
nova-pulse progress --queue <dir> --roots <dirs> [--day <d>]
nova-pulse wake    --bench <name>... --registry <file> [--timeout <duration, default 8m>]
nova-pulse sleep   --bench <name>... [--idle <duration, default 30m>]
nova-pulse width   --root <dir> --pool <pool.tsv>  (not yet implemented)
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
`--model`, no `--priority`, no `--retry`. `version` takes no flags and no arguments and
prints the one build-identity line every binary prints (SPEC.md, **Every binary says
which build it is**): `nova-pulse <build identity> <goos>/<goarch> <go version>`, four
tokens, exit 0.

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

`nova-pulse status --queue <dir> --roots <dirs> [--day <d>] [--timeout <s>] [--max <n>]
[--expanding-hours <n>]` is the one-verb answer to the
all-day questions — what runs where, how wide, at max throughput, what landed and who adopted
it, what is in flight and what remains and how long, what new work appeared, are we converging.
It prints, no model, at most 20 lines, eight line kinds, each one line, counts not lists:

- `WIDTH <bench>`, one per bench in scope — `running` jobs, `slots`, `load`, `headroom`.
- `QUEUE` — `pending`, `gated` (waiting on a merge or a hold), `launched`, `done`, `failed`.
- `RATE` — `cards_per_hour`, `p50_s`, `p90_s`, `usd_per_card`, `parallelism`, from `usage.tsv`; with no measured usage in the window every metric reads `-` — a cost or latency never measured is unknown, never zero.
- `REMAINING` — `queue` rows, `unread_prs`, `dirty_prs`, `uncarded_issues`, `hours`, in scope only.
- `CONTRACTION` — cards `cut/done`, prs `opened/merged`, issues `filed/closed`, for the last hour and the day, counted, never from a report body, with one verdict on both lines, and the sustained window and the threshold that produced it beside it (`window=<n>h above=1`).
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
STATUS CONTRACTION hour cards=<cut/done> prs=<opened/merged> issues=<filed/closed> verdict=<CONVERGING|EXPANDING> window=<n>h above=1
STATUS CONTRACTION day cards=<cut/done> prs=<opened/merged> issues=<filed/closed> verdict=<CONVERGING|EXPANDING> window=<n>h above=1
STATUS STREAM <name> opened=<n> closed=<n> ratio=<x.xx>
STATUS EXPANDING stream=<name> hours=<n>
STATUS ADOPTION <friend> version=<v> receipt=<n> edges=<n>
STATUS OPEN dogfood=<n> holds=<n> escalations=<n>
STATUS TOOLS merged_since_adoption=<n> <names>
STATUS SLOTS bench=<name> capacity=<n> reserve=<n> held=<n> free=<n> owners=<owner:held/share,...>
STATUS STARVED bench=<name> free=<n> pending=<n>
STATUS SWARM first_attempt=<x.xx|-> done=<n> abstain=<n> batches=<n> hedge=<none|opus-on-critical-path|->
STATUS FAULT reason=<token> count=<n>
STATUS PIT-STOP reason=<token> count=<n> remedy=fix the machinery before more cards
```

**The swarm's own health is a flag, never a guess** (`--batches <dir>`, `bin/status.sh`'s
last two lines). With `--batches` status folds the newest 60 `batch-*.out` outputs in that
directory, by modification time with the name as the tie-break: `done=` and `abstain=` summed
over their `BATCH` lines give `first_attempt`, and every `<label> slot=<n>: ABSTAIN
reason=<token>` line is counted by reason. `hedge` is `opus-on-critical-path` below 0.90 —
the coordinator's model stays on the critical path until the rate holds above it for a day
(Glenn, 2026-09-15) — and `none` at or above it. One `STATUS FAULT` line per reason prints
loudest first, capped by `--max`, and a reason recurring five times or more in the window is
`STATUS PIT-STOP`: when one small fault recurs, fixing it beats continuing (Glenn,
2026-09-16), because more cards through a broken machine is the most expensive thing the
fleet does. Without `--batches` none of the three lines print — the verb claims nothing about
a swarm it was not shown — and a `--batches` that cannot be read is refused with one remedy
line rather than folded as a swarm with no faults. The `BATCH` line's own
`uniform-abstain=<reason>` field is a label and is never counted.

**Utilisation is per bench, every slot working all the time** (Glenn). With
`--slots-store <dir>[,<dir>]` status reads each bench slot-lease store
(`shares.tsv` plus the lease directories) and prints one `STATUS SLOTS` line
per store: `bench` is the store directory's base name, `capacity` and
`reserve` are the store's rows, `held` is the leases held, `free` is
`capacity-reserve-held`, and `owners` names each owner's `held/share` in name
order. When the queue holds pending cards and a bench has `free > 0` on two
consecutive ticks — the first tick leaves a `STARVED-<bench>` marker file in
the queue, the second prints `STATUS STARVED` — status exits 2, so a wake
fires on idle slots with waiting work; one tick alone never starves, and a
tick with no pending or no free slots clears the marker.

**status reads a per-root index, not every job file** (#1088). A root holding a full day —
2,000 finished jobs across 40 slot dirs plus a pool, ~1,100 queue entries — made `status
--oneline` walk every root and open every job's `usage.tsv` on every tick, over two minutes
on a real root. `<root>/status-index.tsv` remembers, per job, a class, a token count, the
usage rows the two readers fold, and the key that says they hold: the mtimes of the three
files a job writes as it finishes — `usage.tsv`, its harness store
`data/opencode/opencode.db` and `RESULT.md`. `status` stats only those three files per
indexed job and re-reads only the jobs one of them moved for, so a finished job whose
harness log or store wal keeps moving is never re-opened for it, and a warm tick opens no
job file and starts no `sqlite3` at all. The class is `RESULT.md`'s verdict (done, abstain,
blocked), falling back to the usage row's return code when the job wrote none. `run` and
`harvest` append a finished job to its root's index as they
fold it, so a job is indexed before the next tick needs it; a root with no index is walked
once and the index written, and a root that sat still is answered from the index alone. The
RATE arithmetic still takes the first measured row of each file and a day's spend still sums
every row, both now from the index. Replay:
`status-oneline-two-thousand-job-root-is-fast`.

**The verdict is the health metric** (Glenn, 2026-09-15, #549, #553). Contraction is tracked
every tick, and the `CONTRACTION` line's `verdict` is `CONVERGING`, or `EXPANDING` when any one
stream's ratio — cards `cut/done`, prs `opened/merged`, issues `filed/closed` — has been above
the threshold for the sustained window's consecutive sampled hours. The verdict is computed once
per run from the hourly samples and printed on both the hour and the day line, and the line
carries the window and the threshold that produced it — `window=<n>h above=1` — because a
divergence signal whose window and threshold a reader has to infer is a signal nobody can check
(nova-tools #177: *avoid reacting to one arbitrary sampling instant; configurable windows and
thresholds must stay visible*). `--expanding-hours <n>` (default 2, whole hours, refused below
1) is that window; the run of consecutive above-threshold hours is remembered between runs in
the queue's `EXPANDING` marker — `<hour> <run>`, and a marker in the old shape, one bare hour
key from before the run was counted, is a run of one — so one sampling instant never decides
the verdict and a balanced window never erases the trend. The threshold stays 1: *faster than
completion* is #177's own definition of divergence, and a threshold a coordinator could raise
would be the signal tuned into silence. `EXPANDING` means the spec work up front was not done
properly or the engineering practice is lax, and the coordinator names which, on the record,
before another implementation card goes out. The same verdict belongs on nova-work `check`,
from the journal, under #500. Replays: `status-expanding-after-two-hours-above-one`,
`status-contraction-window-and-threshold-stay-visible`.

**The contraction ratio is per stream, every tick.** A stream is the grouping the queue already
has (a queue subdirectory) or, failing that, the card label's prefix before the first `-`:
`card-8381.md` is stream `card`. `run` appends one `TICKS` line per tick per stream —
`at=<RFC3339> stream=<name> opened=<n> closed=<n>` — where `opened` is the cards cut/refilled
that tick and `closed` the cards done; a bench whose `run` does not write it yet gets it written
here. `status` folds the rolling two-hour window into `<queue>/CONVERGENCE.tsv` and prints one
`STATUS STREAM <name> opened=<n> closed=<n> ratio=<x.xx>` per stream, the ratio being the window's
opened over closed. When a stream's ratio has been above 1 for every tick in two hours, `status`
prints `STATUS EXPANDING stream=<name> hours=<n>` and exits 2 — the alarm is a state the
coordinator must act on, like `PULSE UNDER-WIDTH`.

### The fleet page

`nova-pulse status --html <out> --machines <registry> [--benches <file>, retired]
[--queue <dir>] [--ssh <path>] [--timeout <s|duration>] [--publish <host:dir>]
[--self <name>] [--loop <label>=<pattern>]... [--branch <name>] [--day-start <HH:MMZ>]
[--gh-config <dir>]` is the fleet page as a verb
(`bin/status-page.sh`). It reads every bench over `ssh <target> bash -s`, in
parallel and bounded by `--timeout`, counting live cards from running card processes — a
process whose command line names a job directory, or whose cwd is under the slot, the
authoritative shape `bench-hygiene.sh`'s `live_slot` uses — and never from log age, which
called a silently dead card alive and a long card dead. It writes the page to `--html`,
appends one seven-column row to `metrics.tsv` beside it — `<RFC3339> live queue merged
opened launched free-disk-per-bench` — ships both where `--publish` says, and prints one
line:

```
STATUS HTML wrote=<path> live=<n> queue=<n> down=<n> merged=<n|-> published=<host:dir|->
```

**A count nobody took is a dash, never a zero.** This is the page's one rule and it has
been learned twice. A bench that does not answer is `DOWN (no answer over ssh)`, its disk is
a dash in the metrics row, it adds nothing to `live`, and it is counted in `down=`: a row of
zeros reads as a bench with nothing to do, which is how a fleet nobody could see looked
healthy on 2026-09-17. An answer that cannot be parsed is no answer and says DOWN too. With
no `<queue>/REPO` **and no origin on the clone the queue sits in** there is nobody to ask the
forge, so `merged`, `opened` and the merge queue read as a dash on the page, in the series
and on the STATUS HTML line, and the chart plots a gap rather than a flat line at zero — a
flat line is a claim. "Nobody to ask" and "asked and got nothing" are DIFFERENT facts and
the page says which: a queue that named a repo and got no answer is a forge to go and look
at, not a fleet file to go and edit.

**The fleet comes from the machines registry.** `--machines <file>` is the registry of
**The machines registry** above — seven columns and roles, the file `fleet registry` prints
and every fill refusal names. The benches read over ssh are its rows carrying the role
`bench`, in file order; a registry no machine of which carries `bench` is a refusal, because
a fleet with nowhere to put a card is not a page of nothing. The page also carries a
**machines** table — name, os/arch, roles, cores, notes — so it answers the question people
were answering by hand ("can I fill batman?") and shows each machine's own word about
itself: the Air is a fleet machine WHILE UP, and a bare `DOWN` row every time a laptop is
shut is a page people learn to ignore. A DOWN bench's row carries its registry note beside
the ssh reason for the same reason.

The registry carries no home column and needs none: with no home the liveness script leaves
`HOME` alone and uses the login home, which is the home ssh lands in.

`--benches` is the retired four-column file (`name`, ssh target, home, mac). It reads for
one more release and every run of it prints one `STATUS NOTE` line naming `--machines`.
Given both, the registry decides and the run names the file it did not read: two files that
disagree about what the fleet IS is how a runner host quietly becomes a bench. Replays:
`status-machines-reads-the-registry`, `status-machines-shows-the-runners`,
`status-machines-shows-the-air-while-up-note`,
`status-machines-refuses-a-registry-with-no-bench`,
`status-benches-is-deprecated-for-one-release`, `status-machines-wins-over-benches`,
`status-derives-the-repo-from-the-queue-origin`, `status-repo-file-wins-over-the-origin`.

The rule was learned a third time by probing the real fleet: a fleet-file home that is not
a directory on the bench — a typo, a user renamed, a machine reinstalled — let `df` answer
nothing, and every row read `0 GB free, allowed 0` from benches that had answered perfectly.
So the home is checked first, before any of the work it would make pointless, and the row
says DOWN **with its reason** rather than a number. The reasons are prose and render as
prose: a run verdict of `completed cancelled` printed once as `completed\x20cancelled`,
because two words had gone through the one-token escaper.

**The rows, all of them.** The page a fleet reads is the whole page or it is not adopted:
an adoption attempt on 2026-09-17 matched four bench rows byte for byte and was still
correctly refused over twelve gaps.

- the branch tip and its CI run (`--branch`, `dev` by default) — a red tip is the one fact
  that makes every other number beside the point;
- the merge queue by state: running, waiting, unmergeable, from the forge's `mergeQueue`;
- pending and ready cards, what the fill loop launched and what capacity refused;
- hygiene actions in the last hour, summed over the benches from each `~/hygiene.log`,
  counted in the SAME round trip as the liveness numbers — a second ssh per bench per
  minute for one integer is the dumb waste the fleet audits for;
- one row per bench, and one for `--self`: the host running the verb is a bench too, with
  its own columns (CI runners, cores, load, free disk, orphans). The Studio drowned at load
  147 on 2026-09-17 while the page showed four Linux benches idling;
- the loops on the `--self` host, counted by the pattern the CALLER names. No loop name is
  baked into this tool: a verb carrying `harvest-loop.sh` in its source would freeze the
  scripts it exists to retire, and `--loop` without `--self` is refused because there is no
  host to count them on;
- the `metrics.tsv` time series it writes beside itself.

**`--limit` is not optional on a gh list.** gh's own default is thirty, so a fleet that
merges more than that reads as a fleet that merged thirty, every tick, for the rest of the
day. The page said 30 where the script said 273. Every list call here asks for 500.

**The day starts at 02:00Z** (`--day-start`), because `INSTALL-fleet.md`'s `rate_counter`
resets there and the script counted from there; before the boundary the window is still
yesterday's. A malformed boundary is refused rather than silently reset, and the page names
the boundary it counted from.

**`--publish <host:dir>`** ships `index.html` and `metrics.tsv` through the same ssh door
the benches are read through, as one child writing both files. A failed publish is loud and
exits 3: a page that quietly stopped shipping goes stale while everybody keeps reading it.
The local page is written first, so a publish that fails never costs the file.

**`--gh-config <dir>`** is `GH_CONFIG_DIR` for the gh children, and it overrides rather than
invents: without it the caller's own environment goes through untouched. gh answers as
whoever that directory says, so a page run from a service manager with a bare environment
must be given it.

**The verb is bounded and it is fast.** `--timeout` takes a whole number of seconds or a
duration (`90s`, `2m`), because the verb's own progress line prints a duration and a flag
that will not accept what the tool prints is a trap. The `/proc` walk happens once, before
the slot loop, and is capped: reading every pid's cwd once per slot is forty slots times a
few thousand processes, which is why a live bench read DOWN at `--timeout 10` and the page
took 24.6 s against the script's 13.8. This path fetches pull requests only — the page
shows no issue count, and the issue round trip was half its gh time.

**Counts only ever reach the page**: no card id, branch name or label, because it is served
to whoever can reach the host.

## Progress

`nova-pulse progress --queue <dir> --roots <dirs> [--day <d>]` answers "how fast, at what cost,
how long" from records and never from a guess (Glenn, 2026-09-15, #536). It reads every job's
`usage.tsv` under `--roots` (start, end, rc, usd) for the day and the queue's rows, no model,
two lines:

```
PROGRESS cards=<n> rc0=<n> wall_p50_s=<n> wall_p90_s=<n> usd_per_card=<x.xxxx> span_h=<n.n> effective_parallelism=<n.n> cards_per_hour=<n.n>
ESTIMATE remaining_cards=<n> hours=<n.n> pending=<n> launched=<n> prs=<n> issues=<n> wall_p90_s=<n> parallelism=<n.n> rate=<n.n>/h factor=1.5
```

**The estimate names its terms.** `remaining_cards` and `hours` are the answer; `pending`,
`launched`, `prs`, `issues` and the three rates behind them are how it was reached, and they
reconstruct it exactly: `remaining = pending + launched + prs + 2 x issues` and `hours =
remaining x wall_p90_s / parallelism x factor`. An estimate a reader cannot check is one
nobody acts on, which is why `bin/progress.sh` printed the terms from the first day.

`effective_parallelism` is busy card-seconds over span seconds — what the benches did, not
what they had. `remaining_cards` is pending + launched + open PRs needing a read + 2 x open
in-scope issues; `hours` is `remaining x p90 / parallelism x 1.5`, conservative by that factor.
`span_h`, `effective_parallelism`, `cards_per_hour` and `hours` print one decimal; the verb
reads `usage.tsv` columns by header name, so column order never matters, and without a
`POLICY` naming the repo the PR and issue terms are zero and the estimate counts the queue
alone. The `PULSE WIDTH` line carries `hours=<n>` from the same estimate. First measurement,
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
   per bench. Headroom is a RATIO of cores, so `[headroom] studio = 2.5` is a value a person
   writes and the loader reads it as 2.5; the cap is the whole cards that fit under it, since
   half a card is not a card (issue #869).
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

## The pit stop

A **pit stop** is the coordinator's decision to stop widening the queue and spend
the machine on the faults that make every card slower, until one trust batch
proves them gone ([PIT-STOP.md](PIT-STOP.md)). The activity it names is bugs
only: fixes with a reproducing test and its red line quoted, reads and rebases,
one PR per fault, while the policy's `scope-regex` admits nothing else and
anything expansionary is labelled `next-push` in its issue and never carded
(rule 7 of **Rate and convergence**). A pit stop is finished when the fix cards
of the stop rerun as one trust batch on the fixed tools and every card scores
`done` with its red line quoted — no abstain, no `harness-silent`, no `idle`,
no false red in any package — and only then does the queue widen again
([PIT-STOP.md](PIT-STOP.md), **The exit gate**).

## Exit codes and the output grammar

Per SPEC.md: **0** the verb ran and the state it reports is consistent — a pool written, cards
cut, a batch admitted, a harvest folded, a width that is not under; **1** the tool saying NO —
a harvest with `mismatch > 0` or `abstain > 0`, a cut with `skipped > 0`; **2** could not run
— every refusal the rules name and `UNDER-WIDTH` (rule 16: the alarm is a state the
coordinator must act on, and it exits like a refusal so a wake fires on it). An
`ADMIT REFUSED` line is an event, not a verdict: it leaves the exit code alone (rule 9).

```
POOL OK sources=<n> candidates=<n> issues=<n> audits=<n> slices=<n> roadmap=<n> prs=<n> work=<n> next=<n> plan=<n> seen=<n> took=<d> out=<path>
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
PROGRESS cards=<n> rc0=<n> wall_p50_s=<n> wall_p90_s=<n> usd_per_card=<x.xxxx> span_h=<n.n> effective_parallelism=<n.n> cards_per_hour=<n.n>
ESTIMATE remaining_cards=<n> hours=<n.n>
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

## Cut, from a validated template

Glenn, 2026-09-17 (#1142): *"every script and hand step sketched on the bench becomes an
official verb; fold every friend's scripts in"*. `cut` grows three inputs beside `--pool` —
an issue (`--issue <repo>#<n>`, title and body verbatim), a table of rows (`--rows
<file.tsv>`, one card per group), and an existing PR's exact head branch (`--branch-from
<repo>#<n>`) — through a template with named slots, so the source a friend's script scraped
by hand becomes a flag and the script retires. The verb line, as help will print it:

```
nova-pulse cut --templates <dir> --out <dir> --root <dir> --pool <pool.tsv> [--max <n>]
nova-pulse cut --templates <dir> --out <dir> --repo <clone> (--issue <repo>#<n> | --rows <file.tsv> | --branch-from <repo>#<n>) [--base <branch>] [--cards <file.tsv>] [--max <n>]
```

**Amended 2026-09-18, from a non-author's dogfood run.** Four things the section above
did not say, and the verb now does. `--repo <clone>` is required of the three validated
forms and every `git` call runs with `-C <repo>`: a command never depends on the working
directory. `--root` is the pool form's alone — the validated forms wrote nothing under it
and asked for it anyway. The branch check begins with `git check-ref-format --branch`
semantics applied in process, because `rowan/has a space` was `CUT OK`. Every card is
written as `card-<label>.md`, the one filename contract the queue directories keep and the
one `nova-pulse fill` globs, and a second cut into the same `--out` appends to `cards.tsv`
rather than overwriting it. One example of each template ships at
`cmd/nova-pulse/testdata/templates/{issue,rows,branch-from}.md`, and a test cuts a card
from each.

**Amended again the same day, from the schema dogfood loop (rows 9601-9603).** A label
never carries the queue's `card-` prefix — it belongs to the filename, and carrying it in
both rendered `CARD-card-9601` into a RESULT line and from there into a PR title. A
`--rows` table carries two more columns, `lane` (field 6, filling the `<lane>` slot) and
`template` (field 7, naming another `<templates>/<name>.md`), so one cut fills as many
lanes as it has rows and one table cuts N different tasks. `--base <branch>` is the
default base for a source that names none, because `dev` was hardcoded and a repo without
a `dev` branch had to spell it in every row. `--cards <file.tsv>` names where the
`cards.tsv` goes, because `--out` is a queue directory in real use and the table is not a
card.

**It reads one source and one template, and writes only cards.** `--issue` reads title and
body verbatim through `gh issue view --json title,body`; `--rows` reads a tab-separated file,
one group per line; `--branch-from` reads the PR's head ref through `gh pr view --json
headRefName,headRefOid`. The template declares named slots — `<issue>`, `<title>`, `<body>`,
`<branch>`, `<base>`, `<row>`, `<replay>` — and `cut` fills every one from that input; an
unfilled slot is `CUT REFUSED check=slot slot=<name> (named slot with no value: fill it, or
drop it from the template)`. It writes the cards under `--out` and their rows to `cards.tsv`,
rule 7's four fields, and nothing else.

**Before a byte is written it runs five checks in order and stops at the first that fails.**
(1) the branch is derived, `rowan/issue-<n>-<slug>` from the issue number and the title slug
(or `--branch-from`'s exact head ref) and does not exist on origin, read with a fixture `git
ls-remote`, unless `--branch-from` names it. (2) every file path and every replay name a row
names exists at the base revision, `git cat-file -e <base>:<path>` against the clone at cut
time, never assumed. (3) `STEP 1` parses as one shell line, in-process, no shell run. (4) no
row is a table separator (`|---|`) or a header row naming the columns. (5) the rendered
`RESULT` line is one line. The checks are the contract: the network and the repo are fakes in
tests, the parser is the tool's own.

**One line of output, every field named.** `CUT OK cards=<n> from=<pool|issue|rows|branch-from>
skipped=<n> out=<dir>` — `cards` the cards written, `from` the input read, `skipped` a
bounded count of rows nothing cut (rule 18's `--max` bounds the cards, `0` lifts it), `out`
the directory they landed in. Refusals are exit 2, one line each, remedy in parentheses, the
first failing check named:

```
CUT OK cards=<n> from=<pool|issue|rows|branch-from> skipped=<n> out=<dir>
CUT REFUSED check=branch branch=<name> (pass --branch-from <pr>, or rename the issue)
CUT REFUSED check=base path=<p> not at <base> (fix the row, or add the file)
CUT REFUSED check=step1 (STEP 1 is not one shell line: fix the template)
CUT REFUSED check=row row=<n> is a separator or a header (drop it)
CUT REFUSED check=result (the RESULT line is more than one line: fix the template)
CUT REFUSED check=slot slot=<name> (named slot with no value: fill it, or drop it)
```

**The mistake it removes, one sentence.** Six broken cards in one night came out of `sed` and
`awk` over pasted text — a glob that picked a pre-session branch, a header row leaked in as a
fake replay, a doubled branch name, a docs card naming files that do not exist — and this verb
refuses each of the four before a card exists.

**Red tests, each written first, each with its fake.**

1. `cut-issue-title-and-body-are-verbatim`: a fixture `gh` answering an issue whose title and
   body carry tabs, backticks and a newline cuts a card carrying them byte for byte.
2. `cut-rows-one-card-per-group`: a fixture `--rows` file with two groups cuts two cards and
   no third.
3. `cut-branch-derived-and-absent`: a fixture `git ls-remote` reporting `rowan/issue-<n>-<slug>`
   absent passes, reporting it present is `CUT REFUSED check=branch`, and the same branch is
   admitted when `--branch-from` names it.
4. `cut-names-exist-at-base`: a fixture `git cat-file` passing one path and failing another
   refuses the row naming the missing path, exit 2, no card written.
5. `cut-step-one-is-one-shell-line`: a template whose `STEP 1` carries `&&` and a newline is
   refused by the in-process parser (no shell is run, no clock is read); a one-line `STEP 1`
   cuts.
6. `cut-separator-and-header-are-not-cards`: a `--rows` file whose first row is a header and
   whose second is `|---|` cuts no card for either and still cuts the rows below.
7. `cut-result-is-one-line`: a template whose `<title>` slot renders two lines is `CUT REFUSED
   check=result`; a one-line render cuts.
8. `cut-refuses-the-first-failing-check`: a bad branch and a missing path at once name
   `check=branch` and no path check runs — the order is the contract.

## The cost of a card (#855), 2026-09-16

Measured 2026-09-16 over 1,068 jobs, all benches, from `usage.tsv`: input 62.1M, output
7.2M, cache_write 4.0M, **cache_read 1,434.6M**, reasoning 8.7M. Per job: reads 44.7k input
against 5.2k output; fixes 66k/7.3k; replays 86k/8.9k; implementations 144k/12.4k. The bill
is context x harness turns: the cache reads are 23x the input, because every tool call in a
card re-sends the whole context. A 45k-token read that takes 30 harness turns bills about
1.35M cache-read tokens. The lever is turns per card, not the model. And reasoning is 8.7M
against 7.2M visible output — 121% — so a read that thinks at full effort pays for a chain
of thought no read needs.

The rules:

1. **`cut-steps-are-the-turn-budget`.** `cut` emits cards whose numbered step count is the
   turn budget: a read-family card (`read`, `text`, `tone`) takes at most 8 turns, a
   writing-family card (`fix`, `replay`, `drift`) at most 20 turns. A template whose
   rendered card exceeds its budget is `CUT REFUSED template=<name>: <which>` naming the
   rule, and no card is written. The hurt that made it: the 1,434.6M cache-read tokens
   above, where a read card that greps around for thirty turns re-bills a 45k-token context
   thirty times. Steps name the exact file and line range, one check each, no exploration;
   the budget is the step count, so the count is the contract.
   Red test: `cut-steps-are-the-turn-budget`.

2. **`harvest-records-turns-per-card`.** `harvest` records the turns each card took from
   its `<job>/harness.log` and marks a card over its kind's budget as one template finding,
   counted on the `HARVEST` line and written to `retry.tsv` the way an abstain is (rule 14),
   so a template that cannot fit the budget is rewritten, never re-sent. The hurt that made
   it: without a measured turn count the budget is a hope, and a card over budget is spent
   again on the next pool.
   Red test: `harvest-records-turns-per-card`.

3. **`cut-read-card-names-low-reasoning`.** A read-family card's route carries the
   provider's low reasoning setting — the OpenCode variant, or DeepSeek `reasoning_effort`
   where the route supports it — while fix, replay and drift cards keep the default. The
   hurt that made it: reasoning is 8.7M against 7.2M visible output, 121% of it, and a read
   does not need chain-of-thought at full effort. Measure per kind daily.
   Red test: `cut-read-card-names-low-reasoning`.

The wall clock agrees: the mean job directory lifetime is 480 s against a 436 s wall, so
clone and copy pay about 44 s and the model loop pays the tail — the same lever.

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
- **No clock of its own.** No daemon, no `--loop`, no `--watch`. The chain is `harvest` and
  the manager tier's tick; the alarm is `width`, run by nova-wake or a person. The one clock
  is the manager tier's cycle (#587), and its tick is **Rate and convergence** rule 1.

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

## The loop as a tool (pit stop 3)

Pit stop 3 (#828, Glenn 2026-09-16 17:00Z: *"It still feels like there are a lot of bugs in
your process ... stop, analyze the bugs, and then create new tools and fix existing tools so
these bug classes go away"*). Thirteen bugs inside seventy minutes of one afternoon, seven
classes, one rule each. The coordination loop around the swarm and the merge queue was a
263-line bash prototype — `pulse-loop.sh`, `harvest.sh`, `refill.sh`, `mk-read-card.sh`,
`run-batch*.sh` — patched by hand while it ran, and nova-pulse never absorbed what it learned.
These seven rules are that absorption: each one quotes the row it retires, names the verb or
the file that holds it, and names the test that proves it. The bash shims retire verb by verb
as each lands, each retirement a receipt line in `queue/POLICY.md`.

Two things this section does not do. It names `run`, `gate`, `sweep`, `reap`, `cut --kind` and
`launch --card <n>` — verbs the shipped tool does not offer yet — and deliberately does not add
them to **The verbs** block, so `nova-pulse help` stays byte for byte what that block says
(replay 36) until each one lands. And `run` is a loop with a tick, which amends **What this
draft does not do**'s *"No clock of its own"* exactly as far as the manager tier's cycle
already did and no further: the law that does not move is rule 17, no verb calls a model.

1. **A. State in env vars and restarts.** Row 2: *"Loop restarted for a headroom change forgot
   `PULSE_STUDIO_SLOTS=8`; the Studio launched 11 cards at load 26"*. Row 3: *"The loop must be
   restarted to take any change and loses its counters (`gt`) each time; a running bash script
   edited in place is undefined"*. Row 11: *"Space's launch gate (load-based, k=2.5) held 45
   free slots idle while the load was CI compiles and the cards wait on APIs"* — tuning in an
   env var is tuning nobody can change without killing the loop.
   **The rule:** the loop's configuration is ONE file in the queue directory, `<queue>/pulse.toml`
   — slots per bench, headroom per bench, tick, refill cadence, integration branches, runner
   count per machine — and its counters are one state file beside it, `<queue>/state.tsv`,
   tab-separated like every other file rule 19 names. A restart reads both. Changing a value
   never needs a restart: `run` re-reads the config each tick and prints one `CONFIG` line
   naming what changed. No verb reads a `PULSE_*` environment variable for a value the config
   holds.
   **Held by:** `nova-pulse run`, `<queue>/pulse.toml`, `<queue>/state.tsv`.
   **Proved by:** `config-change-needs-no-restart`, `counters-survive-a-restart`.

2. **B. One fact written twice.** Row 8: *"dev inherited main's cancellation bug: concurrency
   keyed dev on the ref, the next merge cancelled dev's run, the gate read cancelled as red and
   froze the bench"*. Row 9: *"Fair-share divisor stayed 4 when runners went to 8 per machine;
   Space load 39 on 15 cores from CI alone"*. Row 1: *"Replay cards hand-numbered 8130-8134
   collided with five launched cards of the same numbers"*. Row 5: *"The sweep then enqueued
   seven red-test PRs the policy holds; dequeued by hand"*. Row 13: *"PR CI ran the full
   24-shard matrix per PR; a burst of 8 PRs queued 128 studio legs and held the merge group
   16 min"*.
   **The rule:** integration branches are one list, read by every rule that treats those
   branches differently and by the tier rule that decides how wide a PR's matrix is; runners per
   machine is one number the runner service exports (`NOVA_RUNNERS_PER_MACHINE`) and the
   workflow reads, never a divisor written into a workflow; card numbers come only from the
   state file under the lock, so `nova-pulse cut` is the only cutter and a hand-numbered card
   does not exist; holds — red-test PRs, drafts, `AFTER:` gates — are labels or lines the code
   reads, never prose in `queue/POLICY.md` that only a person can apply.
   **Held by:** `.github/workflows/ci.yml` (the one `fromJSON` list of integration refs, and
   the fair-share step reading `NOVA_RUNNERS_PER_MACHINE`), `internal/ci`, `nova-pulse cut`
   under `<queue>/state.tsv`'s lock, and the hold labels `sweep` reads (rule 4 below).
   **Proved by:** `integration-branches-are-one-list`, `runners-per-machine-is-one-number`,
   `cut-is-the-only-cutter-of-card-numbers`, `holds-are-labels-the-code-reads`.

3. **C. All-or-nothing STOP.** Row 6: *"STOP halts launches, so the red's own fix card could not
   launch; launched by hand, wrong runner path then a held slot, third try landed"*. A stop that
   also stops the fix is a stop that has to be worked around by hand, three times.
   **The rule:** STOP is a state with an admission list. While red, `run` launches only cards
   whose line 1 names the red's issue or test — the name `gate` wrote into the STOP file when it
   went red — harvests everything, and enqueues nothing. RESUME is written by the gate alone: the
   gate's STOP is lifted by the gate, never by a hand, and no other card slips past one.
   **Held by:** `nova-pulse gate` (writes the STOP naming the red, and writes RESUME),
   `nova-pulse run` and `launch` (admit against it), the STOP file under the root.
   **Proved by:** `stop-admits-only-the-reds-own-fix-card`, `resume-is-the-gates-alone`.

4. **D. Point-in-time verdicts.** Row 4: *"Enqueue only on APPROVE **and** green at that
   instant; 51 approved PRs with checks still queued were logged `(not merged)` and never
   revisited"*. Fifty-one decisions taken once, each correct at the instant it was taken, none
   of them true ten minutes later.
   **The rule:** an approval is a ledger row — PR, head, card, verdict, time — and every tick
   `sweep` walks the open rows: green and not draft and not held → enqueue once; head moved →
   a new read card; merged or closed → the row is closed. Nothing is decided once and forgotten,
   and nothing is enqueued twice.
   **Held by:** `nova-pulse sweep`, `<queue>/approvals.tsv`.
   **Proved by:** `sweep-revisits-every-open-approval-row`,
   `sweep-cuts-a-read-card-when-the-head-moves`.

5. **E. No reaper.** Row 10: *"Six `nova-swarm-swarmtest supervise` processes leaked by
   TestTheLaunchIsATransaction lived up to 14 h on Space; a 23-hour-old card ran on the Studio;
   three stale cards sat in launched/"*. Row 7, classed B + E: *"refill's dedup treated
   done/failed cards as a block; 22 admitted issues sat uncut"* — a finished card that nothing
   sweeps is a card that still counts.
   **The rule:** a `reap` step every tick. Processes under a swarm root older than the batch
   deadline are killed and logged; a launched card whose job directory is gone is requeued once
   and then failed; a batch lock whose pid is dead is removed; test binaries under
   `/tmp/*swarmtest*` older than 30 minutes die. The counts ride on the `PULSE WIDTH` line, so a
   leak is visible in the one line the coordinator already reads.
   **Held by:** `nova-pulse reap`, called by `run` each tick; `PULSE WIDTH`.
   **Proved by:** `reap-kills-past-the-batch-deadline`,
   `reap-requeues-a-launched-card-whose-job-dir-is-gone`.

6. **F. The coordinator's hands.** Row 12: *"The coordinator's own shell: zsh globs aborting
   probes (`no matches found`), a wrong runner path, a held slot, `pkill` killing its own ssh"*.
   Row 1 again — hand-numbered cards — and row 6 again: the hand launch that took three tries.
   **The rule:** every coordinator action has a verb — `nova-pulse cut`, `launch --card <n>`,
   `gate`, `sweep`, `reap`, `status` — each printing one bounded line, and the loop calls the
   same verbs a hand does. A hand launch is `nova-pulse launch --card <n>` and nothing else.
   `bash -c` for any glob the coordinator types is not a rule; it is the reason the verbs exist.
   **Held by:** the verbs themselves, and `nova-pulse run`, which composes them and adds no path
   of its own.
   **Proved by:** `hand-launch-is-launch-card-n`, `the-loop-calls-the-same-verbs-a-hand-does`.

7. **G. The coordinator's own tokens.** Glenn, 2026-09-16 17:40Z: *"This seems like a lot. How
   can we make the coordinator more efficient? How can we make the manager more efficient?"*
   Measured that session: *"4,800 turns, average context 551k, cache reads 2,630M, cache writes
   14.8M, output 5.6M"* — about 310M fresh-input-equivalent tokens weighted, against 55M tokens
   at $238 for the whole swarm's day. The coordinator is five to six swarms, and *"85% of it is
   context × turns, most turns being polls (log tails, run lists, width lines)"*. The manager
   tier that answers it: routine is not an LLM (G1), triage is a packet on the cheapest route
   (G2, about $0.003 a decision), and the coordinator is reached only when the rule table has no
   row — and each such case becomes a row.
   - **G1. One turn per decision, never per tick.** `nova-pulse run` holds the loop — gate,
     harvest, sweep, reap, refill, launch, each tick — with zero model calls, and writes ONE bus
     note when a rule cannot decide. The coordinator's turns are the decisions, not the ticks.
     **Held by:** `nova-pulse run`. **Proved by:**
     `run-a-simulated-day-writes-under-twenty-notes`.
   - **G2. Triage is a fresh bounded packet on the cheapest model.** When `run` cannot decide —
     a REFUSED reason, a HOLD line, a failed card's cause, a read that quotes a line — it cuts a
     triage card: the decision packet (the `RESULT` lines and the rule table's candidate rows,
     under 5k tokens) to the text route. The verdict is one line,
     `TRIAGE <case> <verdict> <rule-row-or-NEW>`, and a `NEW` verdict becomes a rule row the
     same day. **Held by:** `nova-pulse run` cutting through `cut --kind`, and the rule table in
     `queue/POLICY.md`. **Proved by:** `triage-packet-decides-every-refusal-kind`.
   - **G3. Bounded output is the coordinator's rule too.** Every verb prints one line by
     default; a list above five items is a count with `--max` to widen; the coordinator's tool
     results are what the verbs print, never a `tail -40`. This is rule 18 aimed at the window
     instead of at the log. **Held by:** every verb, `internal/oneline`, `internal/bounded`.
     **Proved by:** `every-verb-prints-at-most-three-lines-by-default`.
   - **G4. State lives in files, the window restarts at beats.** `nova-pulse status`
     reconstructs the day from the `queue/` files in one line, so a fresh window needs `status`
     and `POLICY` and not the transcript. **Held by:** `nova-pulse status`, `<queue>/` files.
     **Proved by:** `status-reconstructs-the-day-in-one-line`.
   - **G5. Measure it.** `nova-tokens` folds the coordinator's own session — the Claude Code
     jsonl: input, cache write, cache read, output per turn — into the daily ledger as its own
     model line with the weighted equivalent, and the ledger prints tokens per decision and per
     merged PR. A cost nobody measures is the one that grows. **Held by:** `nova-tokens`, the
     daily ledger. **Proved by:** `tokens-folds-the-coordinator-session`.

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
 8b. `cut-source-is-locator-not-kind`: an `issues` source whose line names
    `mas-bandwidth/nova-tools` yields a `pool.tsv` field 1 of `mas-bandwidth/nova-tools`, and
    the card `cut` writes clones `https://github.com/mas-bandwidth/nova-tools.git` on its
    `STEP 1` — never `https://github.com/issues.git` (#631, the red dogfood line).
 9. `launch-fills-every-free-slot`: eight admitted cards and four free slots admit exactly
   four — every free slot filled, none left idle, exit 0 and no `UNDER-SLOTS` anywhere in
   either stream; the mutation that matters: a launch that refuses instead of filling.
10. `launch-queues-remainder`: the same run queues the rest with no flag asked for:
    `nova-swarm batch` runs once, in the card form, with a `--cards` TSV holding exactly the
    first four cards in `cards.tsv` order, `queued=4`,
    `queue.tsv` holding the other four, `pulses/<id>.tsv` naming the batch.
11. `launch-slot-free-from-lock-files`: a slot whose lock file is `state=free` is free, one
    `state=busy` is not, a missing lock file is free; `free-before` counts the free ones and
    no `native.log` age or 120 s rule appears anywhere — the mutation that matters: a slot
    freed by a log age instead of the swarm's lock file.
12. `launch-every-card-through-batch`: any admitted cards run exactly one `nova-swarm
     batch` invocation and zero `nova-swarm add`, in the card form — `--id <pulse>`, a
     `--cards` TSV holding exactly the admitted cards in order, `--deadline`, a `--runner`,
     `--root`, and a `--then` whose argv begins `nova-pulse harvest --id <id>`; a `BATCH
     REFUSED` fixture reply is `PULSE REFUSED` with that reason, `queue.tsv` unchanged.
12. `launch-every-card-through-batch`: two model routes present run exactly two `nova-swarm
    batch` invocations and zero `nova-swarm add`, each with `--label pulse-<id>`, a `--files`
    equal to that route's card count, a `--tokens` equal to the sum of that route's `tokens`
    column in `cards.tsv`, a duration-form `--deadline <s>s`, and no `--then` (the gather
    mode's flag, which this admission does not carry); a `BATCH REFUSED` fixture
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
    source with two new items, five free slots: the next batch's `--cards` TSV holds the
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
23. SPEC-AHEAD: #466 — `pool-reads-work-nodes`: a `work` source with one open, unleased,
    unblocked bug node and one open, unleased, unblocked item node yields `work=2`, two
    `pool.tsv` rows whose `id` is the node id, and `candidates=2`; a node that is leased or
    blocked is nowhere; the id is carried on the card's line 1 so `harvest` records the
    attempt on the node.
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
51. `status-contraction-window-and-threshold-stay-visible` (#177: *avoid reacting to one
    arbitrary sampling instant; configurable windows and thresholds must stay visible*): every
    `CONTRACTION` line names the sustained window and the threshold that produced its verdict
    (`window=2h above=1` by default); a ratio at 1 and a single above-threshold hour are
    `CONVERGING`; two consecutive above-threshold ticks — the run remembered between them by the
    marker the tool itself writes, and a marker in the old shape, one bare hour key, reading as
    a run of one — print `EXPANDING` on the hour line and on the day line alike, the day line
    carrying its own counts and the same sustained verdict; `--expanding-hours 3` names
    `window=3h` on both lines, and `--expanding-hours 0` is refused at exit 2.
52. `config-change-needs-no-restart`: a `<queue>/pulse.toml` whose `headroom` is edited between
    two ticks is read by the second tick — one `CONFIG` line naming the key, the new value used
    in that tick's admission — with no restart and no `PULSE_*` environment variable anywhere in
    the argv log; the mutation that matters: a config read once at startup (rule A, rows 2, 3
    and 11).
53. `counters-survive-a-restart`: a `run` killed mid-shift and started again on the same
    `<queue>` continues its counters from `<queue>/state.tsv` — cards cut, launched, done and
    the card numbers — and cuts no number it cut before; a fresh directory starts at the file's
    initial row and never at a number a hand chose.
54. `integration-branches-are-one-list` (internal/ci, `TestIntegrationBranchesAreOneList`): the
    integration refs are named once in `.github/workflows/ci.yml`; any `refs/heads/main` or
    `refs/heads/dev` literal outside that one list is red, `cancel-in-progress` names no branch,
    and the push triggers of ci.yml and certification.yml — which take no expression, so they
    cannot read it — are held to the list (row 8, the bug that froze the bench).
55. `runners-per-machine-is-one-number` (internal/ci, `TestRunnersPerMachineIsOneNumber`): the
    fair-share step divides the core count by `${NOVA_RUNNERS_PER_MACHINE:-8}`, the runner
    service's own fact, prints the count it used, and carries no literal divisor — the mutation
    that matters: the divisor written back into the workflow, which is row 9 exactly.
56. `cut-is-the-only-cutter-of-card-numbers`: two `cut` runs against one `<queue>` under
    concurrent load take their numbers from `<queue>/state.tsv` under its lock and collide on
    none; a card whose number was written by anything else has no state row, and `launch --card`
    refuses it naming the remedy (row 1: five hand-numbered replay cards overwrote five launched
    ones).
57. `holds-are-labels-the-code-reads`: a red-test PR, a draft and a card carrying
    `AFTER: PR<n> merged` are each held by a label or a line the code reads, so `sweep` enqueues
    none of them with no prose consulted; the mutation that matters: a hold that exists only as
    a sentence in `queue/POLICY.md` and is enforced by whoever remembers it (row 5: seven
    red-test PRs enqueued, dequeued by hand).
58. `stop-admits-only-the-reds-own-fix-card`: with a STOP naming an issue and a test, `run`
    launches the card whose line 1 names either, launches no other card, harvests every job that
    finished, and enqueues nothing; exit 0 — the mutation that matters: a STOP that also stops
    the fix, which is row 6 and three hand launches.
59. `resume-is-the-gates-alone`: RESUME written by `gate` when the red clears lifts the
    admission list; a RESUME written by anything else is refused with the remedy and the STOP
    stands, and no verb clears a STOP as a side effect.
60. `sweep-revisits-every-open-approval-row`: an `approvals.tsv` of fifty-one rows approved
    while their checks were `QUEUED` is walked every tick, each row enqueued exactly once when
    its checks go green and it is neither draft nor held, and closed when the PR merges or
    closes; the mutation that matters: a verdict taken at the instant of approval and never
    revisited, logged `(not merged)` — row 4, all fifty-one of them.
61. `sweep-cuts-a-read-card-when-the-head-moves`: an open row whose PR head changed yields one
    new read card in `next.tsv` and no enqueue, and the row carries the new head; the old card is
    not re-sent and the row is not closed.
62. `reap-kills-past-the-batch-deadline`: on a day-sized fixture, a supervise process under a
    swarm root older than its batch deadline, a batch lock whose pid is dead and a
    `/tmp/*swarmtest*` binary older than 30 minutes are killed, removed and deleted, each logged
    one line, with the counts on the `PULSE WIDTH` line — the mutation that matters: a loop with
    no reaper, which is fourteen hours of leaked supervisors and a 23-hour-old card (row 10).
63. `reap-requeues-a-launched-card-whose-job-dir-is-gone`: a card in `launched/` whose job
    directory has vanished is requeued exactly once and failed on the second reap, its state row
    written both times, and a done or failed card never blocks the next `refill` from cutting its
    item (row 7: 22 admitted issues sat uncut).
64. `hand-launch-is-launch-card-n`: `nova-pulse launch --card <n>` launches that one card and
    prints one bounded line; the same card launched twice is refused by its state row naming the
    remedy; no verb in the package shells out through `bash -c`, no verb globs a path, and
    `pkill` appears nowhere — row 12, the coordinator's own shell.
65. `the-loop-calls-the-same-verbs-a-hand-does`: `run`'s tick appears in the argv log as `gate`,
    `harvest`, `sweep`, `reap`, `refill`, `launch` and nothing else — no inline path, no second
    implementation of a verb's rule — so a fix to a verb is a fix to the loop.
66. `run-a-simulated-day-writes-under-twenty-notes`: a full simulated day — the day-sized
    fixture, 200 cards, 40 PRs, two reds and one flake — runs every tick with zero model calls in
    any argv log and writes fewer than 20 bus notes, one per undecidable rule; the mutation that
    matters: a note, a poll or a model call per tick, which is 4,800 turns (rule G1).
67. `triage-packet-decides-every-refusal-kind`: each of the day's refusal kinds — signature,
    scope, docs-only, NOSHA, orphan, fence — is decided by one triage card whose packet is under
    5k tokens and whose verdict is one `TRIAGE <case> <verdict> <rule-row-or-NEW>` line, with no
    coordinator turn in the trace; a `NEW` verdict writes a rule row the same day (rule G2).
68. `every-verb-prints-at-most-three-lines-by-default`: on the day-sized fixture every verb's
    default output is three lines or fewer, a list above five items is a count with `--max` to
    widen, and every value is one `internal/oneline` token — rule 18 aimed at the window (rule
    G3).
69. `status-reconstructs-the-day-in-one-line`: `status` on the day-sized fixture answers width,
    pool, reds, merges and spend in one line under 400 bytes, read from the `queue/` files
    alone — no transcript, no report body — so a fresh window needs `status` and `POLICY` and
    nothing else (rule G4).
70. `tokens-folds-the-coordinator-session`: `nova-tokens` folding a fixture Claude Code jsonl —
    input, cache write, cache read and output per turn — adds one model line for the coordinator
    to the daily ledger whose numbers match a hand count exactly, with the weighted equivalent,
    and the ledger prints tokens per decision and per merged PR (rule G5).

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
4. **`--then` rides the card form.** `launch` passes `--then "nova-pulse harvest --id <id>
   --root <root>"` to `nova-swarm batch`'s card form, which runs it when every card is done
   and never earlier (rule 10); the pool form's `--then` (card 269) is no longer on launch's
   path. Nothing else in this draft depends on it.
4. **What batches the chain.** `nova-swarm batch`'s pool/tasks admission mode enqueues work
   and returns; `--then` belongs to its `--cards --runner` gather mode only. Default: `launch`
   admits and prints its line, and `harvest` is run by the manager tier's tick (or a person),
   never chained from the admission batch itself.
5. **What the shipped tool is behind on, named rather than assumed.** This is a draft, and
   `## Tests this spec demands` says so: the replays are demanded of the implementation, not
   read off it. Three deltas are open against `internal/pulse` at this draft's head, each one
   card's work: `pulseVerbs` lacks `--work` on `pool`, the `handoff`, `takeover` and `status`
   lines, and this draft's `--benches` and `--timeout`; `cut.go` prints `flash=<n> pro=<n>` on
   `CUT OK` where rule 7's cost table gives `zero=<n> flat=<n> metered=<n>`; and
   `TestCutModelByKind` pins the routing replay 8 replaces. A fourth is rule 9's: `internal/pulse/launch.go` still writes
   `zero=<n> flat=<n> metered=<n>` on `CUT OK` (rule 7's cost table) and `TestCutModelByKind`
   asserts the table's routes, while `cards.tsv` still carries four fields where rule 7 gives
   five; and rule 9's: `internal/pulse/launch.go` still writes
   `PULSE REFUSED UNDER-SLOTS` and `launch_test.go` pins it, `pulseVerbs` still offers
   `[--queue]` and carries none of the three gate flags — one card retires the refusal, turns
   that test into replay 9, drops `[--queue]` and adds the gates, in that order, so the red is
   the removal and never a silent behaviour change. Default: the spec leads, the cards follow,
   and no rule here is softened to match code that has not been written. Rejected: documenting the code as it is,
   which is how a draft stops being a design.

## CI wall

Glenn 2026-09-17 (#888): the gate watches CI runtime and pit-stops when it is over two
minutes. It records each run's wall, `updated_at` minus `created_at` from the runs API it
already reads, for the branch tip and each PR it enqueues; a wall over 120 s writes one line
to `<queue>/REDS` of the form `CI-WALL <run id> <wall s> long-pole=<job name> <job wall s>`
naming the job with the largest wall from the jobs API the gate already fetches (`-` when
unknown), and the gate's status line carries `STOP: ci-wall`, while a wall under 120 s
changes nothing.
## Layout

One file per spec section so parallel PRs stop conflicting (nova-tools #560):
each `## ` section of this file lives in its own file under `docs/spec-pulse/`
and this top file includes them — every section file's body appears here
verbatim, and `internal/pulse/layout560_test.go` checks both directions, that
this file names each section file and carries its body. An amendment edits one
section file (and this top file's copy of that section with it, keeping the
two identical), never two sections at once. The replay slices follow the same
rule on the Lisp side: `lisp/nova-work/tests/acceptance/<slice>.lisp`, loaded
by `lisp/nova-work/tests/acceptance.lisp` (WORKER-CARDS.md practice 26).

- [preamble](spec-pulse/00-preamble.md)
- [The loop, in words](spec-pulse/01-the-loop-in-words.md)
- [The rules, numbered](spec-pulse/02-the-rules-numbered.md)
- [Fleet](spec-pulse/03-fleet.md)
- [The verbs](spec-pulse/04-the-verbs.md)
- [Handoff](spec-pulse/05-handoff.md)
- [Status](spec-pulse/06-status.md)
- [Progress](spec-pulse/07-progress.md)
- [Rate and convergence](spec-pulse/08-rate-and-convergence.md)
- [Exit codes and the output grammar](spec-pulse/09-exit-codes-and-the-output-grammar.md)
- [The card, as `cut` writes it](spec-pulse/10-the-card-as-cut-writes-it.md)
- [What this draft does not do](spec-pulse/11-what-this-draft-does-not-do.md)
- [The manager tier](spec-pulse/12-the-manager-tier.md)
- [Tests this spec demands](spec-pulse/13-tests-this-spec-demands.md)
- [Open questions](spec-pulse/14-open-questions-each-with-a-default-and-the-default-stands-un.md)
- [CI wall](spec-pulse/15-ci-wall.md)
- [Layout](spec-pulse/16-layout.md)
- [The beat](spec-pulse/17-the-beat.md)
- [The learned admission checklist](spec-pulse/18-learned-admission-checklist.md)

## The beat

The coordinator window restarts thin at every beat (token item, cairn 8386). `nova-pulse
beat --queue <dir> --cairn <file> --title <text> [--resume <text>]` appends one section to
the cairn file — `## <UTC time> <title>`, then one line per queue fact: the pending,
running, done and failed counts from `status`, the `REDS` line count, whether `STOP`
stands, the `HUMAN` line count, and the last merged PR from `<queue>/MERGED` — and then the
`Resume rule:` line: `--resume`, or `read this section, run nova-pulse status, act on
REDS/STOP/HUMAN first` when the flag names none. It reads only the queue and makes no model
call. When the cairn file is in a git repo it commits it (`git add` and one commit, never a
push) and otherwise leaves it uncommitted, and either way it prints `BEAT OK cairn=<file>
lines=<n>`, where `<n>` is the cairn's line count, and then the line a fresh window restarts
from: `RESTART: exit this window; the next window boots from <cairn>`. Replays:
`beat-appends-the-queue-section`, `beat-appends-a-second-section`,
`beat-without-git-still-says-ok`.

## The learned admission checklist

Ikrima's synthesis for Glenn, "Let tomorrow's questions shape today's memory" (2026-09-16),
reviewed by Rowan. The organizing question: what may Rowan forget while still able to learn,
decide and act correctly tomorrow? The answer from today's record is the **abstain reason** —
the recurring failure class the machinery actually produced. The checklist below is cut from
that history, not from imagination, and every rule names the hurt that made it and the red
test that holds it. `nova-pulse cut --probe` runs it against each new card before launch.

1. **The checklist is cut from the abstain history, never invented.** The 2026-09-15 baseline
   over 60 batches: first-attempt 0.58, and the classes that recur are `no-result` (26),
   `wall refusal`, `idle kill`, `admission` and `line1-mismatch`. **Hurt:** a rule written
   after each fault did not transfer to the next unfamiliar card, so the same class abstained
   again; a checklist with no history behind it is a new opinion, not memory. **Red test:**
   `probe-reads-the-abstain-history` — a check whose class is absent from the history is
   skipped, and the same check refuses the card when the class is present.

2. **`cut --probe` runs the checklist against each new card before launch.** **Hurt:** the
   classes were discovered only after a card was cut, launched and abstained, at the price of
   the whole run; the probe pays a read instead. **Red test:** `probe-refuses-before-launch` —
   a card carrying a known class is refused with no card written and exit 1.

3. **The five checks, each named for the class it was learned from:** paths outside the job;
   quoted forbidden words; a missing red-test step; the budget against the card's size; the
   result location. **Hurt:** `admission` and `wall refusal` came from cards that left their
   job, `line1-mismatch` from a card with no red-test step, `idle kill` from a card over its
   file budget, `no-result` from a card that never named where `RESULT.md` goes. **Red test:**
   `probe-names-the-five-checks` — each class names its one check.

4. **A probe refusal names the known class and its remedy.** **Hurt:** an abstain reason that
   arrived without its class was triaged as new and cost a fresh rule; the probe says
   `class=` so the per-class count stays comparable to the history. **Red test:**
   `probe-refusal-names-the-class`.

5. **Measure repeated abstains of a known class on unfamiliar cards (retained transfer).**
   The measure is abstains per class per day, and total cost with reads and probes included;
   the adoption round and Glenn's judgment stay the external standard. **Hurt:** counting only
   total abstains hid that the classes had moved to new cards. **Red test:**
   `probe-counts-by-class-per-day`.

6. **A compression is legal only if the next decision survives it.** Apply it to the manager's
   `SHIFT END` and `HANDOFF` record: a successor must be able to make the same next decision
   from the record alone. **Hurt:** a handoff that carried what the shift produced, not what
   the successor needs, sent a fresh window to a different first move. **Red test:**
   `handoff-record-supports-next-decision`.

7. **A cairn compaction carries what the next decision needs, not what the last produced.**
   **Hurt:** a session summary that preserved the last turns lost the one fact the next window
   acted on. **Red test:** `cairn-preserves-next-decision`.

8. **A read RESULT states its scope: `not checked: <list>`.** **Hurt** (his PR 300 point): a
   "review passed" line lost the kind and scope of the evidence it stood on, so a reader took
   a pass over one axis for a pass over all — "review passed" is not a verdict. The template
   change lands in `nova-pulse cut` and in SPEC-REVIEW. **Red test:**
   `read-result-states-not-checked`.

9. **The objective is future capability per unit of total cost, verification included.** The
   ledger adds the read and probe cost to each card's row, so the probe is never free by
   construction and a cheaper route that cannot hold the card is named. **Hurt:** a route
   chosen on card price alone bought abstains that cost more than the card. **Red test:**
   `ledger-counts-read-and-probe`.

## Watch

Glenn, 2026-09-17 (#1142): every script and hand step sketched on the bench becomes an
official verb, and every friend's scripts fold in. The verb line, as it will appear in help:

```
nova-pulse watch --queue <dir> --bus <dir> --jobs <root> --until <event> --cap <duration>
```

**One process, three waits.** `watch` waits on the merge queue (`--queue`: entry rows, the
`MERGED` line and removal rows), the bus (`--bus`: a new `To:` note from anyone but the caller —
the caller is the name on the queue's `OWNER` row, and a note whose `From:` is that name is
skipped), and the job roots (`--jobs`: a `RESULT.md` appearing under any
`<root>/<slot>/jobs/<label>/`). It reads those three directories and the caller's clock, writes
no state of its own, holds its last snapshot in memory and starts from the state of its first
poll — it reports changes, never a backlog. `--until '<event>'` names the event that ends it —
`pr=<n> merged`, `note-from=<name>`, or `job=<label> done` — and `--cap <duration>` is the wall
it never runs past. It is not the daemon **What this draft does not do** forbids: it is bounded
by its cap and exits, and it adds no tick to the loop.

**One line per change, with a stable prefix, and silence when nothing changed.** Every change
prints exactly one line: `QUEUE <change> pr=<n> at=<utc>` where `<change>` is `enqueued`, `merged`
or `removed`; `NOTE from=<name> to=<name> id=<id>` for a new addressed note; and `JOB
label=<label> state=<done|abstain|blocked> path=<path>` for a `RESULT.md` that appeared since the
last poll, the state being its line 2 verdict. It polls no faster than every 30 s and prints no
change line on a poll that found none. A named `--until` event ends it at the poll that sees it,
before the cap, and then it prints `WATCH OK` and exits 0; the cap prints `WATCH CAP` and exits 3.

```
QUEUE <change> pr=<n> at=<utc>
NOTE from=<name> to=<name> id=<id>
JOB label=<label> state=<done|abstain|blocked> path=<path>
WATCH OK until=<event> changes=<n> polls=<n> wall=<s>
WATCH CAP until=<event> changes=<n> polls=<n> cap=<duration>
WATCH REFUSED: <reason> (<remedy>)
```

**Every refusal is exit 2 with one remedy in parentheses.** A missing `--queue`, `--bus`,
`--jobs`, `--until` or `--cap` is `WATCH REFUSED: refusing to guess (<flag> is required)`. An
`--until` that is not one of the three events is `WATCH REFUSED until=<event> (name one of
pr=<n> merged, note-from=<name>, job=<label> done)`. A `--cap` below the 30 s poll floor is
`WATCH REFUSED cap=<duration> (the cap is at least the 30s poll floor)`. A `--bus` that is not a
nova-bus checkout or a `--jobs` root that is not readable is one `WATCH REFUSED <flag>=<path>`
naming a readable one.

**The mistake it removes:** a coordinator spending most of its turns polling, with a dozen
hand-written waiters in one night.

Red tests, one per rule, each with a fake where the real thing is a network, a bench or a clock:

1. `watch-prints-one-line-per-queue-change`: a fake queue dir whose entry, merge and removal each
   appear between two polls yields exactly one `QUEUE` line each and no other line.
2. `watch-prints-a-new-note-once`: a fake bus checkout holding one `To:` note from a friend
   yields one `NOTE` line on the next poll and none after; a note `From:` the `OWNER` name is
   skipped.
3. `watch-prints-a-result-md-appearance`: a fake job root where a `RESULT.md` appears yields one
   `JOB` line carrying the label and its line 2 verdict.
4. `watch-polls-no-faster-than-30s`: a fake clock logs every poll, and no two polls are closer
   than 30 s.
5. `watch-prints-nothing-when-nothing-changes`: a fake queue, bus and job root unchanged across
   three ticks yields no change line at all.
6. `watch-exits-0-on-the-until-event`: `--until 'pr=7 merged'` against a fake queue that records
   the merge returns `WATCH OK` and exit 0 at the poll that sees it.
7. `watch-exits-3-at-the-cap`: a fake clock driven past `--cap` with no event returns `WATCH CAP`
   and exit 3.
8. `watch-refuses-a-bad-until-or-cap`: an unknown `--until` and a `--cap` below 30 s are each one
   `WATCH REFUSED` with a remedy, exit 2, and no poll is made.
9. `watch-refuses-missing-paths`: a missing `--bus` and a missing `--jobs` are each one `WATCH
   REFUSED` with a remedy, exit 2.

## Harvest on the working layout

Glenn, 2026-09-17 (#1142): every script and hand step sketched on a bench becomes an official verb. `harvest` gains a second layout — the jobs a bench leaves under `<working>/tmp/<guid>-<label>/jobs/<label>`, beside the swarm roots it already folds — and the `--timer` that runs it with no coordinator's loop. The line this section adds, for `nova-pulse help`:

```
nova-pulse harvest --working <dir> [--roots <dirs>] [--base <ref>] [--since <stamp>] [--timer install] [--max <n>]
```

It is documented here and not in **The verbs** until the implementation card lands, so `nova-pulse help` stays byte for byte that block (replay 36).

1. **Every path comes from a flag.** `--working <dir>` names the working root and `--roots <dirs>` the swarm roots; it reads `<working>/tmp/<guid>-<label>/jobs/<label>` and each `<root>/<slot>/jobs/<label>`, so the old layout is folded too and no `$HOME`, `~/rowan-working` or `/tmp` is assumed. `--base <ref>` (default the default-branch head, read once per run) and `--since <stamp>` (the session window) are the caller's.

2. **What it reads and what it writes.** Per job it reads the `RESULT.md` first line and `BRANCH` line, the clone's `git log -1 --format=%H %cI <base>..HEAD`, `git ls-remote` and the hosting CLI's open PRs. It writes one `<job>/.harvested` (empty, atomic), one `HARVEST JOB` line per job and one `HARVEST OK` summary, and edits no job, branch, PR or report body.

3. **One `HARVEST JOB` line per job, every field named.** `label` is the job directory's label (the text after `<guid>-`); `class` is `fixed|already-fixed|no-change|failed|off-branch` (rule 4); `branch` is the job's `BRANCH` name (`-` when it wrote none); `pr` is the open PR whose head ref equals `branch` exactly (`-` when none); `commit` is the sha12 head of `<base>..HEAD`; `base` is the sha12 the no-commit test and the lease used; `took` is the job's milliseconds. The grammar:

```
HARVEST JOB label=<label> class=<fixed|already-fixed|no-change|failed|off-branch> branch=<name|-> pr=<n|-> commit=<sha12|-> base=<sha12> took=<ms>
HARVEST OK jobs=<n> fixed=<n> already-fixed=<n> no-change=<n> off-branch=<n> failed=<n> pushed=<n> prs=<n> took=<ms>
```

4. **The five classes are the typed decision behind the floor (card 8336).** `fixed`: a commit past `base` was pushed and its PR opened or updated; `already-fixed`: an open PR's head already equals the job's head, so nothing is pushed; `no-change`: no commit past `base` (rule 5); `failed`: the push or the read could not complete; `off-branch`: rule 6. The class is the whole disposition, never inferred from a log.

5. **No commit past the base is the NO-COMMIT line, and nothing is pushed.** An empty `git log <base>..HEAD` prints `HARVEST JOB label=<label> class=no-change base=<sha12> branch=- pr=- commit=-` — the NO-COMMIT line — and marks the job `.harvested`, the git fixture's argv log holding no push. This is the empty branch that was pushed.

6. **A branch off `rowan/*`, or one before the session window, is off-branch.** A `BRANCH` name that is not `rowan/*`, or whose head commit date precedes `--since`, prints `HARVEST JOB label=<label> class=off-branch branch=<name> reason=<not-rowan|before-session>`, is never pushed or matched to a PR, and is `.harvested` all the same. This is the 149 stale branches.

7. **The PR is matched by exact head branch, and the push's lease comes from `ls-remote`.** It reads `git ls-remote <url> refs/heads/<branch>` and pushes `git push <url> refs/heads/<branch> --force-with-lease=refs/heads/<branch>:<ls-remote sha>`, never a bare push, never to `main`. A lease that moved, or a branch whose PR the hosting CLI reports merged, is `failed`, not pushed. This is the push onto a merged PR's branch.

8. **`.harvested` is the marker and the only state.** The disposition is followed by one empty `<job>/.harvested`; a job already carrying it is skipped with no line and no read, so a second harvest in the same window is a no-op.

9. **`--timer install` gives the harvest a clock the bench owns.** On Linux it writes `~/.config/systemd/user/nova-pulse-harvest.service` and `.timer` with the harvest's own flags, runs `systemctl --user daemon-reload` and `systemctl --user enable --now nova-pulse-harvest.timer`, and appends one line per run to `<working>/harvest.log`. `--timer` takes `install` or nothing: any other action is `HARVEST REFUSED timer=<action> (only install)`, exit 2. A reaper silenced for hours by an `ssh -n` in a Studio loop is gone, because no coordinator loop is its clock.

10. **Refusals are exit 2, one remedy line each.** A missing `--working` with no `--roots` is `HARVEST REFUSED: refusing to guess (name --working or --roots)`; an unreadable `--working` is `HARVEST REFUSED working=<dir>: <reason> (fix the path or the permissions)`; an unparsable `--since` is `HARVEST REFUSED since=<stamp>: not a timestamp (use RFC3339)`; an unknown `--timer` action is rule 9's. No refusal writes a `.harvested` or a `HARVEST JOB` line.

11. **Bounded output, per SPEC.md.** Per-job lines are capped at `--max` (default 20), followed by one `HARVEST MORE kind=<class> shown=<n> total=<t> --max <n>`; the `HARVEST OK` count line prints on failure as well as success; every value is one `internal/oneline` token.

**The mistake it removes.** A bench job no coordinator folded — a reaper silenced for hours by an `ssh -n` in a Studio loop — is now one `HARVEST JOB` line per job on a timer the bench owns, so 149 stale branches, an empty branch and a push onto a merged PR's branch are each a class, not a surprise.

Red tests, one per rule, each faking the network, the bench or the clock:

1. `harvest-working-reads-the-guid-layout` — a fixture working root of `<guid>-<label>` jobs plus a fixture old root yields one `HARVEST JOB` line per job (fixture `gh` and `git`).
2. `harvest-working-refuses-without-a-flag` — no `--working` and no `--roots` is `refusing to guess`, exit 2, and the fixture `git` log is empty.
3. `harvest-working-no-commit-is-skipped` — a fixture job whose HEAD equals `base` prints the NO-COMMIT line as class `no-change`, and the fixture `git` log records no push.
4. `harvest-working-lease-comes-from-ls-remote` — the fixture `git` log records `ls-remote` then `push --force-with-lease=...:<sha>`, and a fixture remote whose sha moved is `failed`, no push.
5. `harvest-working-off-branch-and-before-session` — fixture jobs on `feature/x` and on a commit before `--since` (fixture clock) are class `off-branch`, with a `reason` and no push.
6. `harvest-working-matches-pr-by-exact-head` — a fixture `gh` with two similar-titled PRs matches `pr=<n>` only on the exact head branch and leaves the other `pr=-`.
7. `harvest-working-marks-harvested` — after a run every fixture job holds `.harvested` and a second run prints no line for it (fixture clock and fixture `git`).
8. `harvest-working-classes-are-the-five` — one fixture job per class prints exactly the five classes and an `HARVEST OK` whose counts sum, through fixture `gh`, `git` and clock.
9. `harvest-working-timer-install` — a fixture `systemctl` records `daemon-reload` and `enable --now`, both unit files carry the harvest flags, and an unknown action is exit 2 with one remedy.
10. `harvest-working-output-is-bounded` — 200 fixture jobs print at most `--max` lines plus one `HARVEST MORE`, every value one token, with fixture `gh`, `git` and `systemctl`.

## Harvest, from a bench

Glenn, 2026-09-17: *"every script and hand step sketched on the bench becomes an official
verb"*. `~/rowan-working/bin/harvest-bench.sh` was the working reference on 2026-09-18 — one
`ssh` loop over a bench's job directories, a push from the Studio, a PR per job — and a
dogfood loop of three schema cards on hulk found six edges in it and in the local harvest
beside it. This section is what shipped; the **Harvest on the working layout** section above
is the wider layout the same verb grows into. The lines, as `nova-pulse help` prints them:

```
nova-pulse harvest --bench <name> --root <bench root>[,<root>] --clone [<o/n>=]<dir>... [--session <id>] [--branch-prefix rowan/] [--base <branch>] [--since <d>] [--launched <dir>] [--done <dir>] [--failed <dir>] [--ssh <path>] [--max <n>]
```

1. **`--bench` reads the jobs where they are.** The verb lists every
   `<root>/<slot>/jobs/<label>` on the bench over the ssh seam and reads each `RESULT.md`
   from there. Without it, harvest read those paths *locally*, found nothing, and reported a
   card that had finished green with a committed branch as `retry=1` — a success reported as
   a retry. The one remote script lists directories and copies `RESULT.md` bodies; every
   decision — the session, the prefix, the base, the age — is taken here, because half the
   defects of the hand loop were the shell itself.

2. **A card with no job directory under the root ran somewhere else, and is not an
   abstain.** The local fold counts it under `elsewhere=<n>`, writes no `retry.tsv` row and
   rewrites no card, and one `HARVEST NOTE` names the remedy: harvest it from its bench.

3. **A bench harvest wants no `--id`, no `--sources` and no `--templates`.** There is no
   pulse packet to name and no relaunch to feed, and a `cut --rows` produces none of the
   three. It wants `--clone`, because the branch is pushed from here.

4. **The branch is pushed from the coordinator, never from the bench.** The job's clone is
   fetched over `ssh://<bench><job>/repo` into `refs/harvest/<branch>` in the local clone
   `--clone` names and pushed from there by explicit refspec. A bench holds no forge
   credential and never will (*Secrets never copied between machines*). `--clone <dir>` is
   the clone for any repo; `--clone <owner>/<name>=<dir>` binds one, which is the two-repo
   table the script hardcoded.

5. **The filter is the session and the branch prefix, never an age alone.** `--session <id>`
   takes only jobs whose `RESULT.md` names that session, `--branch-prefix` (default
   `rowan/`) only branches under it, and `--since` is an optional extra bound rather than
   the whole filter. The script took *every* `rowan/*` job under six hours, whoever cut it.
   A job passed over is one line naming its reason: `session`, `branch-prefix`, `age`,
   `no-repo`, `no-clone`.

6. **The base is the card's, in both places it is used.** The `RESULT.md`'s `BASE` line,
   else `--base`, else `dev`. The no-commit guard is `git rev-list --count
   origin/<base>..refs/harvest/<branch>` — the script hardcoded `origin/dev` — and a job
   that committed nothing is **not pushed**, is marked `.harvested` and prints
   `HARVEST NO-COMMIT`. The PR's base is that same base; the script hardcoded schema's to
   `main`.

7. **The PR title is cut at a word boundary and keeps its suffix.** The whole title,
   ` (<label>, <bench>)` included, is at most 110 bytes; the head is cut back to the last
   space and closed with `...`. The script cut the `RESULT` line at 110 mid-word and then
   appended the suffix on top of it, so the title lost a word *and* ran past the cap.

8. **`harvest` drains `--launched`, so a lane is released by the verb that folds it.**
   Nothing but `manager` drained it, and a lane taken by a card that finished hours ago
   stayed occupied forever. A launched card whose job is done (a `RESULT.md`) moves to
   `--done`, one whose job directory is gone moves to `--failed`, each with a marker naming
   the lane, the bench and why; one still running is left alone. The lane comes from the
   `<card>.launched` marker `fill` writes beside the card — `lane`, `bench`, `label`,
   `session`, `at` — so the release is by lane name and never by parsing the card again.

9. **The launched directory is SHARED, and the drain touches only what is the caller's,
   finished, and within `--max`.** #1950: one `harvest --bench vision --max 1` emptied a
   live 151-card queue in 733 ms — `jobs=0 ... drained=151` on one line — marked every card
   `failed`, deleted every `.launched` marker and took five cards of other lanes whose jobs
   were running on other benches; 149 markers had to be rebuilt by hand. Seven manager
   lanes drop cards into one `ready/` and the resident fill loops move them into one
   `launched/`, so a verb that empties that directory for everybody is a fleet-wide
   foot-gun. Four clauses, each read from the card's own launch record:
   - **(a) the record must be the caller's, and the caller must SAY who that is.**
     `--session <id>` is **required for any drain**: with no `--session` nothing is
     drained at all — every card is left, counted `no-session`, with the remedy on stderr
     (Johnny, #1984: *"`--launched` without `--session` must not drain"*). The session is
     never inferred — not from the job's `RESULT.md`, not from the lane, not from the
     bench. With one, only a card whose marker names exactly that session; a marker naming
     none is not provably the caller's either.
   - **(b) it must match `--bench`.** A card whose marker names another bench is left where
     it is — the drain printed `bench=captainamerica` under `--bench vision` and moved the
     card anyway. Without `--bench` (the local form) only a marker naming no bench qualifies.
   - **(c) it must be finished, or PROVEN dead.** `done` is a job THIS run folded to a
     durable end — a PR, a `NO-COMMIT`, or an earlier `.harvested`. A job whose fetch, push
     or forge call failed, or which a filter passed over, keeps its card: **a launch record
     is removed only after its result has been harvested.** `job-dir-gone` is a verdict on
     EVIDENCE, and a sibling job being listed is not evidence about this card:
     - the traversal must be **complete per root**. The listing answers
       `ROOT <path> ok|missing|incomplete`, holding every root, every slot and every `jobs`
       directory against `-r` and `-x`, because a glob is silent about the difference
       between *nothing is here* and *I was not allowed to look*: a live job under a `jobs`
       directory at mode 000 makes the script print nothing and exit 0. One `incomplete`
       root costs the whole run its absence claims (never its harvest), and the cards it
       cannot judge are left and counted `incomplete-listing`.
     - the card's OWN job directory must be **probed by name**: one bounded
       `PROBE <label> present|absent|unknown` call for every candidate, where `present`
       beats `unknown` beats `absent`, so no permission failure is ever read as a dead job.
       Only `absent` is absence. An empty listing proves nothing either.
   - **(d) `--max` bounds what is CONSUMED, not just what is printed.** `--max 1` printed
     one line and drained 151.
   A drained card's `.launched` marker **moves with it** into `--done` or `--failed` and is
   never deleted: it is the only record of which bench the job is on, and a card without it
   cannot be harvested by anyone. The two move as a **PAIR or not at all**: the destination
   is checked for a collision first (this verb overwrites no evidence it did not write), the
   MARKER moves first, and a card move that fails rolls the marker back. A drain that cannot
   complete is **never counted in `drained`**, prints `HARVEST DRAIN-FAIL` with its reason —
   `destination-exists`, `marker-move`, `card-move`, `rollback` — and makes the verb exit 1.
   `drained=<n> left=<m>` on the summary is the whole receipt, with one bounded
   `HARVEST LEFT reason=<r> cards=<n>` line per reason and never one per card. And a
   `--root` that does not resolve on the bench is `HARVEST REFUSED` before any state
   changes, never a silent `jobs=0`; a quoted `'~/...'` is not expanded by this verb.

```
HARVEST JOB bench=<name> label=<label> branch=<name> sha=<sha> base=<branch> pr=<repo>#<n>
HARVEST NO-COMMIT bench=<name> label=<label> branch=<name> base=<branch> (nothing was committed; not pushed)
HARVEST SKIP bench=<name> label=<label> reason=<session|branch-prefix|age|no-repo|no-clone|no-count> <detail>
HARVEST ROOT-INCOMPLETE bench=<name> root=<path> (<why>)
HARVEST DRAIN card=<card-<n>.md> lane=<name> state=<done|failed> bench=<name> why=<result|job-dir-gone>
HARVEST DRAIN-FAIL card=<card-<n>.md> lane=<name> bench=<name> reason=<destination-exists|marker-move|card-move|rollback> <detail>
HARVEST LEFT reason=<running|unharvested|no-session|other-session|other-bench|probe-present|probe-unknown|unprobed|unproven|incomplete-listing|max> cards=<n>
HARVEST BENCH <OK|RED> bench=<name> jobs=<n> done=<n> pushed=<n> prs=<n> no-commit=<n> skipped=<n> drained=<n> left=<n> took=<d>
HARVEST REFUSED: --root <path> does not exist on <bench> (<remedy>)
```

Exit 0, 1 when a fetch, a push or the forge failed for any job or a drain could not
complete, 2 on a refusal that never started. Red tests, one per edge, each against the fake shell, the fake forge and the fake
git on PATH — no test opens a connection:

1. `TestHarvestBenchReadsResultsOverTheShellSeamAndOpensThePR` — rules 1, 3 and 4.
2. `TestHarvestDoesNotCountAMissingJobDirAsARetry` — rule 2.
3. `TestHarvestBenchFiltersBySessionAndBranchPrefix` — rule 5.
4. `TestHarvestBenchMarksANoCommitJobAgainstTheCardsBase` — rule 6, the guard.
5. `TestHarvestBenchOpensThePRAgainstTheCardsBase` — rule 6, the PR.
6. `TestHarvestPRTitleCutsAtAWordBoundaryAndKeepsTheSuffix` and
   `TestHarvestBenchHandsTheForgeTheCutTitle` — rule 7.
7. `TestHarvestReleasesTheLaneOfAFinishedCard` — rule 8.
8. `TestFillWritesTheLaunchedMarkerCarryingTheLaneAndSession`,
   `TestFillReadsTheLiveLaneFromTheMarkerNotTheCard` and
   `TestFillRemovesTheLaunchedMarkerWhenTheLauncherFails` — rule 8's marker.
9. `TestHarvestConsumesOnlyItsOwnFinishedCardOnASharedQueue` — rule 9, all four clauses in
   one run, asserted on a before/after listing of the shared directory with a sha256 per
   file; `TestHarvestDrainsNothingWhenTheBenchListedNoJobs` — rule 9(c), the 733 ms
   receipt; `TestHarvestLeavesACardWhoseJobFailedToFetch` — rule 9(c), durability;
   `TestHarvestRefusesARootThatDoesNotResolveOnTheBench` and
   `TestBenchListScriptAsksWhetherEachRootIsThere` — the root refusal.
10. The pair, the session and the proof, one witness each (#1984):
    `TestHarvestNeverSplitsACardFromItsMarkerWhenTheMarkerCannotMove`,
    `TestHarvestRollsTheMarkerBackWhenTheCardCannotMove` and
    `TestHarvestRefusesToDrainOntoExistingEvidence` — the paired move, each with an
    injected failing rename and a sha256 listing showing no split pair anywhere;
    `TestHarvestWithNoSessionDrainsNothing` and
    `TestHarvestWithASessionStillRefusesAMismatchedOrUnstampedMarker` — clause (a);
    `TestHarvestInfersNoAbsenceFromARootItCouldNotReadWhole` and
    `TestBenchListScriptReportsAnUnreadableRootAsIncomplete` — per-root completeness, the
    generated script run under `/bin/sh` against a two-root fixture whose second `jobs`
    directory is mode 000 (skipped as root, which mode 000 does not refuse);
    `TestHarvestProvesJobDirGonePerLabelAndNeverFromASibling`,
    `TestHarvestLeavesACardTheProbeCouldNotAnswerFor` and
    `TestBenchProbeScriptAsksAboutEachLabelByName` — the per-label probe.
