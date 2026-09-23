# nova-state — live state in Redis, the durable record in git, GitHub and `cards:done` (draft 2, 2026-09-22)

**Revised 2026-09-22 (#2597, #2623).** Draft 1 (2026-09-17) put the durable record in Postgres. It
was adopted, measured for a day, and retired: the record is git, GitHub and the `cards:done`
stream, and the SQLite fold of that stream (`internal/events`, `nova-pulse fold`) is a view that
`fold --rebuild` recomputes, never a second source of truth. The one row the stream lacked, the
decision log, is now a `decide` event on it. Glenn's ruling: try, then remove fully.

Glenn, 2026-09-17: *"What other technology could we adopt that is useful? Redis I think is another
thing I really love."* The rule that admits it: *"If something already
exists and is suitable, we use that. We only invent when it is radically new."* Glenn's design law
for the kernel is Redis's own: **one writer, single thread — or it corrupts** (SPEC-WORK, *The
single-writer kernel*). This file splits the family's state in two and maps every hand-built piece
it retires. **Nothing here invents a thing Redis, SQLite or git already does.** Related:
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

**The durable record: git, GitHub and the `cards:done` stream.** The kernel's index of nodes and
receipts, the token ledger, the capability inventory, CI and PR outcomes, the per-card timeline,
the decisions, and the hygiene and mirror logs are questions asked of the past that must answer
the same tomorrow. The facts live in git (files, notes, the journal, the day TSVs), in GitHub (PRs,
checks, merges) and as events on `cards:done`; the answers come from the **SQLite fold**, a view
of the stream with one writer, which a rebuild recomputes.

**What stays in git.** The **bus notes** stay the human-readable record — a note is prose a person
reads, diffs, addresses and cites, and a receipt is a line appended to it, so a note belongs where
prose already lives and where `git log` is the index of who said what. The **nova-work journal**
stays the replayable truth: the single command thread journals every accepted mutation in the same
order it applies it, and replaying that order rebuilds O node by node (SPEC-WORK, *The
single-writer kernel*). So a `nodes` view is a **projection of the journal**, not its
authority: it can be dropped and rebuilt, and a disagreement is a bug in the projection. Git is
what survives an instance, a bench and a vendor; Redis and the fold are how the family reads it
without a fetch and a turn.

## Part 2 — Redis

One local instance holds the live state. Every key obeys SPEC-REDIS's two rules that make this
safe: `<owner>:<name>` with a mandatory TTL, and **nothing in Redis is the only copy of anything**.

### The work queue — one Stream per bench kind, one consumer group per stream

Where SPEC-JOBS puts `queue/lanes/{red,green,small,next}/` and takes a card by `rename`, Redis
gives the same queue with the lease and the heartbeat built in. The stream is
`nova:queue:<kind>:<lane>` (`kind` one of the six card classes, `lane` one of the four), added by
the kernel's expander or `nova-pulse cut`:

```text
XADD nova:queue:<kind>:<lane> * card <id> repo <owner/name> base <ref> branch <rule>
     budget <minutes/tokens/floor> affinity <bench/route> inputs <named>
XGROUP CREATE nova:queue:<kind>:<lane> workers $ MKSTREAM
```

**One consumer group per stream, not per bench.** The group is `workers`, shared by every bench;
each bench reads as a distinct consumer inside it and never the raw tail:

```text
XREADGROUP GROUP workers <bench> COUNT <batch> BLOCK <ms>
     STREAMS nova:queue:<kind>:<lane> >
```

Redis delivers a card to exactly one consumer in a group, so a card reaches exactly one bench.
Per-bench affinity is expressed by **separate streams per kind** — a bench reads the streams it is
for — and never by a separate group over one stream, because a second group over the same stream
gets its own copy of every card and duplicates the whole queue. A bench that groups by itself gets
every card twice; the group name is the work's, the consumer name is the bench's.

It `XACK`s a card **on clip** — after `nova-work clip` commits and `nova-swarm batch` gathers the
`RESULT.md` — so an acked card is a landed card and only a landed card is safe to forget. A worker
that dies mid-card leaves the card in the group's pending list; the next puller reclaims it with
`XAUTOCLAIM nova:queue:<kind>:<lane> workers <worker> <min-idle> 0`, which is SPEC-JOBS's
"lease whose heartbeat lapses is reclaimed" with no reaper to write. Lanes are read `red`, then
`green`, then `small`, then `next`, so priority is the stream suffix and the puller's read order,
never a scorer by default.

### Slot leases — one key, one fenced token, renewed only by its owner

`nova-swarm slots take` has a file with a pid and an `until=`; Redis has the lease. A lease is one
key, `swarm:lease:<store>:<slot>`, and the value is a **random fencing token** minted at take time,
not an owner name: the token is what proves the holder is still the holder:

```text
SET swarm:lease:<store>:<slot> <token> NX PX <lease-ms>
```

`SET NX` means a lease appears only if it won the key, and **PX** puts the life on the key in
milliseconds. Renewal and release **only ever run as a Lua script that compares the stored token
to the caller's** and is a no-op on a mismatch:

```text
-- renew: KEYS[1] is the key, ARGV[1] the token, ARGV[2] the new life in ms
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
-- release: the same token check, then DEL
```

A bare `EXPIRE` (or `DEL`) without the token check is not allowed: it would let an old owner revive
or drop a lease it no longer holds. With the check, a stale holder can neither renew nor release,
so **two owners are impossible by construction** — the fenced token, not the TTL, is what fences
the slot. **The TTL is the reaper**: a dead worker simply stops renewing, the key lapses, and the
slot is free. A live worker past `until=` is still DRIFT and is still printed by name and never
regranted (SPEC-SWARM rule 5); Redis removes only what stopped breathing. `slots list` is
`SCAN swarm:lease:<store>:*` with the values, one line per lease.

**Every write a lease guards is fenced, not only the slot key.** The token is not only what proves
the holder holds the slot; it is what proves a write is still the holder's to make. Each guarded
write carries the caller's fencing token and runs as a **Lua check-then-write** that compares the
stored token to the caller's and **refuses the write when they differ** — so a paused old worker
whose lease lapsed is refused, its renew, its release and its work all. The section names each
guarded write:

- **the clip** — `nova-work clip` accepting the node, gated by the lease the worker took;
- **the `XACK`** — acknowledging the card on the queue stream;
- **the `RESULT` publish** — the `RESULT.md` landing and its `nova:events:job` `PUBLISH`;
- **the harvest push** — `nova-pulse harvest` appending the card's timeline row and pushing the
  gathered result.

The guarded Lua is one shape. Where the target is Redis the check and the write are the same atomic
script; where the target is a file or the fold the fence gates the write, and the write is
idempotent so a fence that passed is safe to repeat:

```text
-- fenced write: KEYS[1] the lease key, ARGV[1] the token, ARGV[2..] the write
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0                          -- not the current holder: the write is refused
end
-- ... perform the guarded write: clip / XACK / RESULT publish / harvest push
return 1
```

A worker that lost its lease to `XAUTOCLAIM` cannot clip, cannot `XACK`, cannot publish a
`RESULT.md` and cannot push a harvest, because every one of those writes reads the same token
first. The fence covers the work, not only the slot.

**One mode per bench, recorded at start, and drained before a restart.** A bench takes leases from
Redis or from the file fallback, never both at once: it chooses a mode when it starts, records that
mode, and every lease verb follows it until the bench restarts in the other mode. The file fallback
is never active at the same time as the Redis lease, because a slot granted twice — once by each
store — is a fence no token can see across. **A restart in the other mode drains survivors first**:
a bench switching mode refuses to start the new mode while any card from the old mode is live — in
the old store's queue, pending list or slot files — and the old mode's cards are reclaimed or
completed before the switch. Only when the old mode holds no live card does the new mode start, and
the drain is written to the bench's log as `mode restart: <old> drained (<n> reclaimed, <m>
completed) -> <new>`, so a mode switch is never a silent drop of the work the old mode still held.

### In-flight caps — one atomic reservation over provider, model and key

Every in-flight call is counted in three sorted sets at once — `swarm:cap:provider:<provider>`,
`swarm:cap:model:<model>` and `swarm:cap:key:<key>` — and a call is admitted only when **all three**
scopes have room. Admission is **one atomic Lua script over the three sets together**, never a
`ZCARD` read followed by separate `ZADD` writes, and never three independent admissions that could
reserve two scopes and then refuse the third:

```text
-- reserve: KEYS = provider set, model set, key set
--          ARGV = cap-provider, cap-model, cap-key, bound, call-id, now
for i = 1, 3 do
  redis.call('ZREMRANGEBYSCORE', KEYS[i], '-inf', '(' .. ARGV[6])  -- reap past the bound only
  if redis.call('ZCARD', KEYS[i]) >= tonumber(ARGV[i]) then
    return 0                                       -- refused: one scope full, none reserved
  end
end
for i = 1, 3 do
  redis.call('ZADD', KEYS[i], tonumber(ARGV[4]), ARGV[5])   -- bound is the score
end
return 1                                           -- admitted: all three reserved together
-- release: KEYS = the same three sets, ARGV[1] the call-id
for i = 1, 3 do
  redis.call('ZREM', KEYS[i], ARGV[1])             -- all three released together
end
return 1
```

The script runs single-threaded inside Redis, so no two reservations interleave between the counts
and the adds, and the three sets are reserved **all or nothing in one script**: the 41st Muse call
is refused inside the script because provider, model or key has no room, and a partial reservation
is impossible. The three scopes are released together by the mirror script, so a seat never lingers
in one set after it left the others. **The score is a hard bound, not the deadline** (below), so a
call is not reaped the moment its deadline lapses. `nova-swarm caps` prints the counts; reservation
is the script, not a spreadsheet.

### In-flight caps — one atomic admission script per provider/model/key

The per-key in-flight counter is a sorted set scored by each call's own deadline,
`swarm:cap:<provider>:<model>`, but admission is **one atomic Lua script — a Lua INCR-with-limit
that returns admitted or refused — never a `ZCARD` read followed by a separate `ZADD` write**:

```text
-- admit: KEYS[1] the cap set, ARGV = cap, deadline, call-id, now
ZREMRANGEBYSCORE KEYS[1] -inf (now)                 -- a lapsed call frees its seat
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[1]) then
  return 0                                          -- refused
end
redis.call('ZADD', KEYS[1], tonumber(ARGV[2]), ARGV[3])
return 1                                            -- admitted
```

The script runs single-threaded inside Redis, so no two admissions interleave between the count and
the add: the 41st in-flight Muse call is refused **inside** the script, and a check-then-add split
across two round trips cannot admit it. **The score is the expiry**, so a call whose worker dies
drops out on the next admission with no reap pass; the same script serves the per provider cap and
the per key cap by naming them. `nova-swarm caps` prints the counts; admission is the script, not a
spreadsheet.

### An expired remote call is `outcome=unknown`, never failed or succeeded

A model call can outlive its own lease or deadline while the provider has not answered. That call
is **not** a failure and **not** a success: it is recorded with `outcome=unknown` in the durable
timeline, and it stays unknown until the provider answers or a bound passes. The cap seat it holds
is released **only** on one of those two events — when the provider's answer lands (the call is
resolved to `succeeded` or `failed` and its reservation is released) or when the hard bound passes
(the seat is reaped and the call remains `unknown`). A lapsed deadline alone frees nothing: the
deadline is when we stop waiting, not when the provider stops owing us an answer. The unknown count
is visible in `nova-pulse status`, which prints the live unknown calls beside the cap counts, so
neither the seat nor the uncertainty is silently dropped.

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

## Part 3 — the fold

The durable answers are views of the `cards:done` stream, folded by one consumer into one SQLite
file on space (`nova-pulse fold`, `internal/events`). Every table's primary key is the stream
entry id, so a redelivery or a replay is a no-op and `fold --rebuild` into a fresh file produces
the same rows as the incremental fold. No bench writes the file: a writer XADDs an event, and the
fold is the table's one writer.

**What it holds today.** `attempts` (a card's own transitions), `reads`, `landings`, and
`decisions` (the Jev routing decisions, one `decide` event each, under the column names the
retired `decide_log` table used), with the views `by_model_route`, `by_bench`, `by_day`,
`by_label`, `totals` and `decisions_by_kind`. An absent cost, count or reason is NULL, never 0 or
`''`.

**What draft 1 also listed** — `nodes`/`receipts`, `token_ledger`, `capability_inventory`,
`ci_pr_outcomes`, `card_timeline`, `hygiene_log`, `mirror_log` — are questions the fold answers
the same way when their writers put their events on the stream: a new event kind and a view, never
a second store with a second writer. Until then git (the journal, the day TSVs, the notes) and
GitHub (checks and merges) answer them.

## The record: card results

Card results were scraped from job directories on the benches by a harvest loop over ssh; it
re-harvested old jobs and could force-push stale commits (review #1263 F09, F11). A card's end is
now an `ok` or `fail` event on `cards:done`, and the fold's `attempts` table and its views are the
record read by a verb (`nova-pulse fold --report`) rather than a walk over the `job*` directories.

## Part 4 — the verbs that change

- **`nova-pulse watch`** subscribes to `nova:events:*` instead of polling the queue, the bus and
  the job dirs; one return per change, one re-read.
- **`nova-pulse status --fleet`** reads the `nodes`/`receipts` projection and the counters, not
  every job file, so the answer is a query, and prints the live unknown-call count beside them.
- **`nova-merge queue`** writes CI and PR verdicts into `ci_pr_outcomes` as it decides them.
- **`nova-tokens report`** is a query over `token_ledger`; the fold still writes the day TSVs.
- **`nova-swarm caps`** reads the in-flight sorted sets across provider, model and key;
  reservation is the one atomic script and the admission is the counter, not a note.
- **the pull worker** reads `nova:queue:*` with `XREADGROUP` and `XACK`s on clip, replacing the
  directory scan and the `taken/` rename.
- **`nova-pulse fold`** reads the `cards:done` stream into the fold, replacing the harvest loop
  over `job*` directories and its stale force-pushes; `nova-decide route --store` writes each
  decision onto the same stream.

## Part 5 — migration in four slices, and the red tests

Each slice shadows the last and is measured, so the old path is restorable until the number moves.

1. **The smallest exclusive-mode slice: one bench in Redis mode only.** Run one bench in Redis mode
   only — the streams, leases, counters, presence and channels — with the **directory queue disabled
   on that bench**, so the bench has exactly one mode and no slot can be granted twice. Drain the
   bench's directory-mode cards before the switch (Part 2, *One mode per bench*), then measure
   **polling turns per hour**, which should fall toward zero on a quiet bench.
2. **The fold beside the files, on one bench.** Fold the stream's events into the SQLite views
   while the files in git stay the record. Measure **hand steps removed**: the inventory
   spreadsheet and the per-question `gh` calls stop being a bench's job.
3. **Redis beside the directory queue, on space.** Run the streams, leases, counters, presence and
   channels beside `queue/` and the slot files; the fallback is the proof. Measure **polling turns
   per hour**, which should fall toward zero on a quiet bench.
4. **Cut the verbs over.** `watch` subscribes, `status --fleet` queries, the puller reads the
   stream, caps read the counters, `report` queries. Measure **seconds to answer "how wide are
   we"**, from a `gh`-and-files sweep to one `status` line.

**The red tests** (one per promise, seen red first):

- `a-stream-consumer-that-dies-mid-card-has-its-card-reclaimed` — kill a puller mid-card;
  `XAUTOCLAIM` hands the card to the next puller, its partial `RESULT.md` kept as evidence.
- `a-cap-counter-refuses-the-41st-in-flight-muse-call` — the 40th Muse call admits, the 41st is
  refused; in the slice-1 script a lapsed call frees its seat with no reap pass, and a call past
  its **hard bound** frees its seat with no reap pass too (a lapsed deadline alone does not; see
  the unknown-outcome test).
- `a-watch-subscriber-wakes-on-a-job-done-event-within-a-second` — publish `nova:events:job` after
  a `RESULT.md` lands; the subscriber returns once, inside a second, and re-reads the file.
- `the-monthly-token-report-from-the-fold-equals-the-folded-tsv-to-the-token` — `report` over
  the fold's ledger view equals the folded day TSVs, every type and every `(day, model, repo)`.
- `one-card-is-delivered-to-exactly-one-consumer` — with the `workers` group shared by two benches,
  `XREADGROUP` hands a card to one bench and not the other; a second group over the same stream
  would hand both a copy, so the test fails unless the group is per stream, never per bench.
- `a-stale-lease-token-cannot-renew-or-release-a-slot` — a token that lost the `SET NX` renews and
  releases as a no-op through the Lua script, no bare `EXPIRE` moves the key, and a bench that chose
  the file mode never touches the Redis key (nor the reverse), so one slot never has two modes.
- `a-cap-admission-is-one-atomic-script-that-refuses-the-41st` — concurrent admissions run through
  the single three-set script; exactly 40 hold provider, model and key together, no partial
  reservation is ever visible, and no `ZCARD`-then-`ZADD` interleaving lets a 41st in; the same
  single script holds the key, and exactly 40 hold the key.
- `a-paused-old-worker-cannot-write-after-its-lease-lapsed` — take a lease, let it lapse, give the
  slot to a new token, then run the old worker's clip, `XACK`, `RESULT` publish and harvest push
  through their fenced Lua; every one returns 0 and no clip, ack, event or timeline row lands,
  while the current holder's identical writes all return 1.
- `a-mode-restart-refuses-to-start-until-the-old-modes-cards-are-drained` — a bench with a live
  directory-mode card refuses to start Redis mode; the card is reclaimed or completed, the drain is
  written to the bench's log, and only then does the new mode start.
- `a-cap-reservation-over-provider-model-and-key-is-one-atomic-script` — with the key or the model
  scope full, a call that would fit the provider scope alone is refused with none of the three sets
  written; an admitted call reserves all three together and releases all three together.
- `an-expired-remote-call-is-unknown-and-keeps-its-seat-until-an-answer-or-a-bound` — let a model
  call pass its deadline with no provider answer; it is recorded `outcome=unknown`, never failed or
  succeeded, its provider/model/key seats stay held, `nova-pulse status` shows the unknown count,
  and the seats release only when the provider answers or the hard bound passes.

**Invent nothing.** If Redis, SQLite or git already does it — streams, consumer groups,
`XAUTOCLAIM`, `SET NX`, TTL, `ZREMRANGEBYSCORE`, pub/sub, a `GROUP BY`, a unique key, WAL — the
design uses it and does not build a second one.

## Tests this spec demands

The slice-1 tests run against fakes: **miniredis** stands in for the Redis instance, a `record.FakeStore` stands in for Postgres, every path is a `t.TempDir()`, nothing reaches the network, and each test is shown red first by mutation before it is trusted. The one real-Postgres half is the soak behind `RECORD_TEST_PG`, skipped when the env var is unset.

1. `TestOneCardIsDeliveredToExactlyOneConsumer` — with the `workers` group shared by two benches, `XREADGROUP` hands a card to one bench and not the other; the group is per stream, never per bench.
2. `TestAStreamConsumerThatDiesMidCardHasItsCardReclaimed` — a puller that dies mid-card has its card handed to the next puller by `XAUTOCLAIM`, its partial `RESULT.md` kept as evidence.
3. `TestAStreamConsumerThatDiesMidCardHasItsCardReclaimed` — an acked card is a landed card and only a landed card is safe to forget (nothing left pending after `XACK`).
4. `TestLanesAreReadInPriorityOrder` — lanes are read `red`, then `green`, then `small`, then `next`; priority is the stream suffix and the puller's read order.
5. `TestACapCounterRefusesThe41stInFlightMuseCall` — the 40th Muse call admits, the 41st is refused.
6. `TestACapCounterRefusesThe41stInFlightMuseCall` — a lapsed call frees its seat on the next admission with no reap pass (the score is the expiry/deadline).
7. `TestACapAdmissionIsOneAtomicScriptThatRefusesThe41st` — concurrent admissions run through the one script; exactly the cap hold, no `ZCARD`-then-`ZADD` interleave lets a 41st in.
8. `TestAStaleLeaseTokenCannotRenewOrReleaseASlot` — a token that lost the `SET NX` renews and releases as a no-op through the Lua script; no bare `EXPIRE` moves the key.
9. `TestAStaleLeaseTokenCannotRenewOrReleaseASlot` — a bench in file mode never touches the Redis key (nor the reverse), so one slot never has two modes.
10. `TestAPausedOldWorkerCannotWriteAfterItsLeaseLapsed` — a paused old worker whose lease lapsed is refused its clip, `XACK`, `RESULT` publish and harvest push: every guarded write carries the token and compares before it writes.
11. `TestAModeRestartRefusesToStartUntilTheOldModesCardsAreDrained` — a bench switching mode refuses to start the new mode while any old-mode card is live, and writes `mode restart` to its log once drained.
12. `TestACapReservationOverProviderModelAndKeyIsOneAtomicScript` — with the key or model scope full, a call that would fit the provider alone is refused with none of the three sets written; an admitted call reserves and releases all three together.
13. `TestAnExpiredRemoteCallIsUnknownAndKeepsItsSeatUntilAnAnswerOrABound` — a model call past its deadline with no answer is `outcome=unknown`, never failed or succeeded, and keeps its seat until the provider answers or the hard bound passes.
14. `TestStatusPrintsTheLiveUnknownCallCount` — `nova-pulse status` prints the live unknown-call count beside the cap counts.
15. `TestAWatchSubscriberWakesOnAJobDoneEventWithinASecond` — publishing `nova:events:job` after a `RESULT.md` lands returns the subscriber once, inside a second, and it re-reads the file.
16. `TestPresenceKeyExpiresAndLeavesNoTombstone` — `wake:presence:<bench>` holds `{as, until}`, the beat renews it, and a crashed line ages out with no tombstone.
17. `TestTheMonthlyTokenReportFromPostgresEqualsTheFoldedTsv` — `nova-tokens report` over `token_ledger` equals the folded day TSVs, every type and every `(day, model, repo)`.
18. `TestRowFromMessageCarriesEveryField` — a `card_results` row carries every field: stream_id, label, bench, exit, result_line, job_path, commit, branch, pr, pushed_at, done_at, recorded_at.
19. `TestRowFromMessageAcceptsTheStreamsAliasNames` — `pr`, `pushed_at` and `done_at` stay unknown (SQL NULL) when the result did not carry them.
20. `TestRowFromMessageRefusesAResultWithNoLabel` — a result with no label is refused; a row needs a card.
21. `TestInsertIsIdempotentOnTheStreamID` — a redelivered or replayed result is an `ON CONFLICT (stream_id) DO NOTHING` no-op, never a second row.
22. `TestMigrateIsIdempotent` — `record --migrate` applies the schema idempotently, versioned by `schema_version`.
23. `TestConsumeDoesNotAckWhenTheCommitFailsThenRecovers` — `record` commits each row and `XACK`s only after the commit; a crash between leaves the entry pending for the next start.
24. `TestListFiltersSinceBenchAndFailed` — `results` filters by `--since`, `--bench` and `--failed`.
25. `TestResultsPrintsOneLinePerRowAndAMoreLine` — `results` prints one line per row, newest first, capped at `--max` (default 20), with a `MORE` line naming the rest.
26. `TestNodesIsAProjectionOfTheJournal` — the Postgres `nodes` table is a projection of the journal, not its authority: it can be dropped and rebuilt, and a disagreement is a bug in the projection.
27. `TestStatusFleetReadsTheProjectionNotJobFiles` — `nova-pulse status --fleet` reads the `nodes`/`receipts` projection and the counters, not every job file.
28. `TestDirectoryFallbackDeliversTheSameContract` — a bench cut off from Redis falls back to the directory queue, slot files and bus presence through the same contract, so an outage is a latency regression and never a lost card.
