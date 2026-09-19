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
   outside every repo ([SPEC-SWARM.md](../SPEC-SWARM.md), #460), and a card that sets its own
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
   form of `nova-swarm batch`, which takes the TSV whole and wants no file or token budget
   (rule 10).
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
     --runner <cmd> --root <root> --then "nova-pulse harvest --id <id> --root <root>"`, with
     `<admitted.tsv>` the admitted cards written under `<root>/cards/<id>/cards.tsv` (the
     cards that fit the free slots, never the queued remainder) and `<cmd>` the deployment's
     native runner on PATH, `nova-native-runner.sh`. The card form is the only form that runs
     a card: the swarm's pool form (`--pool --tasks --label`) wants `--files` and `--tokens`,
     which no launch flag can supply (issue #630), so `launch` never calls it. The one
     admission is recorded in `<root>/pulses/<id>.tsv` (`batch id`, `n`). The `--then` argv
     is `nova-swarm batch`'s (card 269): it runs when the batch's wait ends — every card
     ended or the deadline — and never earlier. A `BATCH REFUSED` line from the swarm is
     relayed as `PULSE REFUSED` with the swarm's reason and nothing is queued.
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
    ([WORKER-CARDS.md](../WORKER-CARDS.md), practice 17's holder: *fix the prompt*).
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
