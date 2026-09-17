# nova-state — live state in Redis, the durable record in Postgres (draft 1, 2026-09-17)

Glenn, 2026-09-17: *"What other technology could we adopt that is useful? Redis I think is another
thing I really love. Postgres is another."* The rule that admits them both: *"If something already
exists and is suitable, we use that. We only invent when it is radically new."* Glenn's design law
for the kernel is Redis's own: **one writer, single thread — or it corrupts** (SPEC-WORK, *The
single-writer kernel*). This file splits the family's state in two and maps every hand-built piece
it retires. **Nothing here invents a thing Redis or Postgres already does.** Related:
[SPEC-REDIS.md](SPEC-REDIS.md) (the instance, its owner prefix and TTL, its file fallbacks),
the job spec `docs/SPEC-JOBS.md` (on `rowan/spec-jobs`) and the work-language spec
`docs/SPEC-WORKLANG.md` (on `rowan/spec-worklang`), [SPEC-BUS-DELIVERY.md](SPEC-BUS-DELIVERY.md)
and [SPEC-WAKE.md](SPEC-WAKE.md) (notes, cursors, wakes), [SPEC-SWARM.md](SPEC-SWARM.md) (slots,
leases, caps), [SPEC-TOKENS.md](SPEC-TOKENS.md) (the ledger), [SPEC-MERGE.md](SPEC-MERGE.md) (CI
and PR verdicts).

## Part 1 — the split

**Live state: lost is fine, because the record rebuilds it.** Who is awake, which card is on which
worker, which slot is leased, how many calls a provider has in flight right now, and the signal
that something changed — all of it is a read of the present that the next beat recomputes. Losing
it costs one rebuild and never a work item, because the work item is already a file and a card id.
It goes to **Redis**.

**The durable record: Postgres.** The kernel's index of nodes and receipts, the token ledger, the
capability inventory, CI and PR outcomes, the per-card timeline, and the hygiene and mirror logs
are questions asked of the past that must answer the same tomorrow. They go to **Postgres**, one
writer per table, with the tables' own history of stamps.

**What stays in git.** The **bus notes** stay the human-readable record — a note is prose a person
reads, diffs, addresses and cites, and a receipt is a line appended to it, so a note belongs where
prose already lives and where `git log` is the index of who said what. The **nova-work journal**
stays the replayable truth: the single command thread journals every accepted mutation in the same
order it applies it, and replaying that order rebuilds O node by node (SPEC-WORK, *The
single-writer kernel*). So the Postgres `nodes` table is a **projection of the journal**, not its
authority: it can be dropped and rebuilt, and a disagreement is a bug in the projection. Git is
what survives an instance, a bench and a vendor; Redis and Postgres are how the family reads it
without a fetch and a turn.

## Part 2 — Redis

One local instance holds the live state. Every key obeys SPEC-REDIS's two rules that make this
safe: `<owner>:<name>` with a mandatory TTL, and **nothing in Redis is the only copy of anything**.

### The work queue — one Stream per bench kind, one consumer group per bench

Where SPEC-JOBS puts `queue/lanes/{red,green,small,next}/` and takes a card by `rename`, Redis
gives the same queue with the lease and the heartbeat built in. The stream is
`nova:queue:<kind>:<lane>` (`kind` one of the six card classes, `lane` one of the four), added by
the kernel's expander or `nova-pulse cut`:

```text
XADD nova:queue:<kind>:<lane> * card <id> repo <owner/name> base <ref> branch <rule>
     budget <minutes/tokens/floor> affinity <bench/route> inputs <named>
XGROUP CREATE nova:queue:<kind>:<lane> bench:<bench> $ MKSTREAM
```

The pull worker reads its own group and never the raw tail:

```text
XREADGROUP GROUP bench:<bench> <worker> COUNT <batch> BLOCK <ms>
     STREAMS nova:queue:<kind>:<lane> >
```

It `XACK`s a card **on clip** — after `nova-work clip` commits and `nova-swarm batch` gathers the
`RESULT.md` — so an acked card is a landed card and only a landed card is safe to forget. A worker
that dies mid-card leaves the card in the group's pending list; the next puller reclaims it with
`XAUTOCLAIM nova:queue:<kind>:<lane> bench:<bench> <worker> <min-idle> 0`, which is SPEC-JOBS's
"lease whose heartbeat lapses is reclaimed" with no reaper to write. Lanes are read `red`, then
`green`, then `small`, then `next`, so priority is the stream suffix and the puller's read order,
never a scorer by default.

### Slot leases — keys with TTL, renewed by the worker

`nova-swarm slots take` has a file with a pid and an `until=`; Redis has the lease. A lease is one
key, `swarm:lease:<store>:<slot>`, whose value names the owner and the card, and whose TTL is the
lease's life:

```text
SET swarm:lease:<store>:<slot> <owner>/<card> NX EX <lease-seconds>
```

The worker renews it with `nova-work heartbeat` (`EXPIRE`), and a lease appears only if `SET NX`
won it, so two workers cannot take one slot. **The TTL is the reaper**: a dead worker simply stops
renewing, the key lapses, and the slot is free. A live worker past `until=` is still DRIFT and is
still printed by name and never regranted (SPEC-SWARM rule 5); Redis removes only what stopped
breathing. `slots list` is `SCAN swarm:lease:<store>:*` with the values, one line per lease.

### In-flight caps — counters per provider/model/key

The per-key in-flight counters run as a sorted set scored by each call's own deadline,
`swarm:cap:<provider>:<model>`:

```text
ZADD swarm:cap:<provider>:<model> <deadline-unix> <call-id>
ZREMRANGEBYSCORE swarm:cap:<provider>:<model> -inf <now>   # a lapsed call frees its seat
ZCARD swarm:cap:<provider>:<model>                        # the live count
```

Admission is `ZCARD` below the cap, then `ZADD`. **The score is the expiry**, so a call whose
worker dies drops out on the next admission with no reap pass; the same shape serves the per
provider cap and the per key cap by naming them. `nova-swarm caps` prints the counts; the 41st
Muse call of the day is refused by the counter, not by a spreadsheet.

### Presence — keys with TTL

`wake:presence:<bench>` holds `{as, until}` and expires on its own; the beat renews it. A crashed
line ages out with no tombstone, which is exactly what `nova-wake awake` reads from the
`from-<name>/BEAT` file and its `until=` lease today. The bus **cursor commit stays in git** — it
is the record of where a line has read to — and the live key is only the "I am here now" signal.

### Events — pub/sub channels

Four channels replace polling (SPEC-JOBS §8):

```text
nova:events:queue:<kind>   a card was added
nova:events:note           a bus note landed
nova:events:job            a RESULT.md was published
nova:events:ci             a check conclusion or a merge
```

The writer `PUBLISH`es after the file lands; `nova-pulse watch` `SUBSCRIBE`s and returns once per
change, then re-reads the file, the note or the stream once. **Pub/sub is a doorbell, never a
record**: a missed publish is a missed wake and not a lost event, because the file was the event
and the wake only says to read it. An empty minute costs a subprocess and no model turn.

### Where it lives, and the fallback

One Redis lives on **space**, the 440 GB bench, bound to localhost and the tailnet with auth from
nova-secrets and persistence off (SPEC-REDIS). A bench that cannot reach it **falls back to the
directory queue it has today** — `queue/` taken by atomic `rename`, slot files on disk, the
presence keys read from the bus — through the same contract, so an outage is a latency regression
and never a lost card. Slice 1 runs this fallback beside the instance on one bench before any
other bench depends on it.

## Part 3 — Postgres

One Postgres holds the durable record, on space beside Redis, one writer per table. No bench runs
SQL by hand: a timer or a verb calls `nova-pulse`, and the tool is the table's writer.

**`nodes` and `receipts` — the kernel's index.** `nodes(node_id, kind, state, owner, repo,
base_ref, branch, ready, revision, updated_at)`; `node_needs(node_id, need_id)` for the dependency
edge; `receipts(request_id, node_id, verb, revision, author, stamp, payload_sha)`. This is the
projection the journal rebuilds, and it is what `nova-pulse status --fleet` and the views read
instead of opening every job file. **Writer: the kernel's command thread** (the same thread that
journals the mutation), so index and journal are one order.

**`token_ledger` — per card, model, repo, day.** `token_ledger(day, card, model, repo, provider,
input_tokens, output_tokens, cache_read, cache_write, reasoning, rough, sources)`, keyed exactly
`(day, card, model, repo)`, the five token types apart. The monthly report is one `GROUP BY`
query. **Writer: `nova-tokens`**, which keeps folding the day TSVs in git unchanged and writes
each day to the table as its index; the red test below proves the two agree to the token.

**`capability_inventory` — tool, verb, revision, machine, state.** `capability_inventory(tool,
verb, revision, machine, state, checked_at, evidence)`. The spreadsheet becomes a table a verb
queries. **Writer: the survey** (`nova-pulse fleet`/`nova-version`), which records what it
observed, never what it hoped.

**`ci_pr_outcomes` — run, group, conclusion, failing test, poison verdicts.**
`ci_pr_outcomes(run_id, pr, repo, head_sha, group_name, conclusion, failing_test, poison,
started_at, finished_at)`. GitHub is the record; this is the index that answers "is this branch
green" without a `gh` call. **Writer: `nova-merge`**, one row per run it observes, including the
poison verdicts.

**`card_timeline` — from `usage.tsv`.** `card_timeline(card, at, event, actor, detail, slot,
bench)`, the pool's `usage/*.tsv` rows appended in order. **Writer: `nova-pulse harvest`**, at the
same moment it gathers the `RESULT.md`.

**`hygiene_log` and `mirror_log`.** `hygiene_log(machine, at, kind, verdict, detail)` and
`mirror_log(machine, at, repo, verdict, detail)`, the bench timers' outcomes. **Writer:
`nova-pulse`** on the timer's behalf (`nova-pulse beat`), so a bench writes through the tool's one
writer and never opens a connection of its own.

## Part 4 — the verbs that change

- **`nova-pulse watch`** subscribes to `nova:events:*` instead of polling the queue, the bus and
  the job dirs; one return per change, one re-read.
- **`nova-pulse status --fleet`** reads the `nodes`/`receipts` projection and the counters, not
  every job file, so the answer is a query.
- **`nova-merge queue`** writes CI and PR verdicts into `ci_pr_outcomes` as it decides them.
- **`nova-tokens report`** is a query over `token_ledger`; the fold still writes the day TSVs.
- **`nova-swarm caps`** reads the in-flight sorted sets; admission is the counter, not a note.
- **the pull worker** reads `nova:queue:*` with `XREADGROUP` and `XACK`s on clip, replacing the
  directory scan and the `taken/` rename.

## Part 5 — migration in three slices, and the red tests

Each slice shadows the last and is measured, so the old path is restorable until the number moves.

1. **Postgres beside the files, on one bench.** Write the projection, the ledger, the inventory
   and the outcomes to Postgres while the files in git stay the record. Measure **hand steps
   removed**: the inventory spreadsheet and the per-question `gh` calls stop being a bench's job.
2. **Redis beside the directory queue, on space.** Run the streams, leases, counters, presence and
   channels beside `queue/` and the slot files; the fallback is the proof. Measure **polling turns
   per hour**, which should fall toward zero on a quiet bench.
3. **Cut the verbs over.** `watch` subscribes, `status --fleet` queries, the puller reads the
   stream, caps read the counters, `report` queries. Measure **seconds to answer "how wide are
   we"**, from a `gh`-and-files sweep to one `status` line.

**The red tests** (one per promise, seen red first):

- `a-stream-consumer-that-dies-mid-card-has-its-card-reclaimed` — kill a puller mid-card;
  `XAUTOCLAIM` hands the card to the next puller, its partial `RESULT.md` kept as evidence.
- `a-cap-counter-refuses-the-41st-in-flight-muse-call` — the 40th Muse call admits, the 41st is
  refused, and a lapsed call frees its seat with no reap pass.
- `a-watch-subscriber-wakes-on-a-job-done-event-within-a-second` — publish `nova:events:job` after
  a `RESULT.md` lands; the subscriber returns once, inside a second, and re-reads the file.
- `the-monthly-token-report-from-postgres-equals-the-folded-tsv-to-the-token` — `report` over
  `token_ledger` equals the folded day TSVs, every type and every `(day, model, repo)`.

**Invent nothing.** If Redis or Postgres already does it — streams, consumer groups,
`XAUTOCLAIM`, `SET NX`, TTL, `ZREMRANGEBYSCORE`, pub/sub, a `GROUP BY`, a unique key, WAL — the
design uses it and does not build a second one.
