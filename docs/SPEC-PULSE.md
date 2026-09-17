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
     relayed as `PULSE REFUSED` with the swarm's reason and nothing is queued.
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
HYGIENE <host> slots=<n> reaped=<n> jobs-deleted=<n> slots-deleted=<n> cache=<kept|dropped>(<size>G) free <a> -> <b>
```

`host` is `hostname -s`; `slots` the slot directories the run saw; `reaped` the slot `data`
homes, `tmp` dirs and scratch it deleted; `jobs-deleted` the jobs it deleted whole;
`slots-deleted` the empty slots it removed; `cache` is `kept` or `dropped` with the build
cache's size in GB; `free <a> -> <b>` is disk free space before and after. `--dry-run` walks
the same trees and prints one `WOULD rm -rf <path>` line per deletion it would make, deleting
nothing, so a bench's first run is read before it is felt.

The liveness rule decides every touch: a job whose `harness-output.log` is under fifteen
minutes old with no `RESULT.md`, or that any process names in its command line or its cwd, is
live and never touched, and a slot any live process's cwd sits in is live the same way. A
harvested job (a `.harvested` marker) is deleted whole; a finished job nobody read goes after
six hours; an empty slot goes; runner `_work/_temp` entries older than a day go; and the Go
build cache is dropped when disk free is below 25 GB or the cache itself is above 20 GB. A
live job is never reaped, however old its neighbours are.

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

## The verbs

```
nova-pulse pool    --sources <file> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--max <n>]
nova-pulse harvest --id <pulse id> --root <dir> --sources <file> --templates <dir> [--max-body-bytes <n>] [--max <n>]
nova-pulse beat    --queue <dir> --cairn <file> --title <text> [--resume <text>]
nova-pulse manager --policy <file> --queue <dir> --roots <dirs> --bus <clone> --as <name> --hours <n>
nova-pulse progress --queue <dir> --roots <dirs> [--day <d>]
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
```

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

## Progress

`nova-pulse progress --queue <dir> --roots <dirs> [--day <d>]` answers "how fast, at what cost,
how long" from records and never from a guess (Glenn, 2026-09-15, #536). It reads every job's
`usage.tsv` under `--roots` (start, end, rc, usd) for the day and the queue's rows, no model,
two lines:

```
PROGRESS cards=<n> rc0=<n> wall_p50_s=<n> wall_p90_s=<n> usd_per_card=<x.xxxx> span_h=<n.n> effective_parallelism=<n.n> cards_per_hour=<n.n>
ESTIMATE remaining_cards=<n> hours=<n.n>
```

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
nova-pulse cut --templates <dir> --out <dir> --root <dir> (--pool <pool.tsv> | --issue <repo>#<n> | --rows <file.tsv> | --branch-from <repo>#<n>) [--max <n>]
```

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
