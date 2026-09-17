# nova-jobs — the card system as a job system (draft 1)

Glenn, 2026-09-17: *"Think of all the different ways we can be more efficient dispatching work,
executing work, going more parallel, waking up and making decisions, making slots always be
efficiently used, not dead waiting. Perhaps slots should mechanically PULL work from some queue,
vs. us driving them. Research work queuing, multithreaded approaches, job systems, job stealing,
used when creating a job system for games. Batching: cards could stack, clone once, do a bunch of
work on that repo, clip in after each thing."*

This is that pull, written as the engine a game would ship. Today `nova-pulse launch` drives
`nova-swarm batch` from a window and pays a window turn per decision. The job system below
replaces the driver with a ready set, per-bench queues, pull workers on leases, affinity, lanes,
batches and events — every decision a file operation, every wait a blocking call. Facts it is
built on: 695 cards today, median 4 min and p90 15.7 min; the merge queue lands 10 to 15 PRs an
hour; each bench has a fetch-only mirror on a 5-minute timer; a bench's **capacity line** is
`min(cores*1.5 - load1, (free_gb-25)/2, memfree_gb/2)`; Jev answers one typed decision in 400 ms
behind a 0.9 confidence floor. SPEC.md's Conventions govern. Each section names the game-engine
form, the nova verb that carries it, the invariant, and the red tests that must be seen red
first. No code.

## 1. The job graph: needs, blocks, and a mechanical ready set

**Game engine.** A job graph carries typed edges: `needs` (cannot begin until that job
completes) and `blocks` (the reverse edge, written by the same insert). The scheduler never asks
a person what may run; it evaluates the ready set as the nodes whose count of unmet `needs` is
zero, and a completion is a join that decrements each dependent and promotes it at zero.

**Nova.** `nova-work dependencies` owns the graph (`:deps`, refused acyclic at seed by validator
rule 3) and `ready --node X` is the ready set: a node is ready only when every need is terminal
accepted, and every row that cannot proceed prints its exact blocker and its resolver. `nova-merge
queue` and `nova-pulse harvest` are joins: a merged, green need settles and re-evaluates its
dependents.

**Invariant.** Launch reads the ready set and nothing else; a card whose need is an open PR is
never on a slot, a `:deps` cycle is refused before publication, so the ready set is finite and the
graph can never deadlock.

**Red tests.** `launch-reads-the-ready-set-not-the-queue`;
`a-needs-cycle-refuses-at-seed`.

## 2. Per-bench queues with work stealing

**Game engine.** Each worker owns a deque; it pushes and pops its own end (LIFO, the hot cache),
and an idle worker steals from the opposite end of the *fullest* queue (FIFO), so ownership is
cheap and idle cores pull work to themselves. A steal is one compare-and-swap on the deque's top
or bottom index, never a lock around the whole queue.

**Nova.** Each bench holds `queue/` as a directory of card files; `nova-swarm pull` lists it,
takes one by `rename(<name>.card, taken/<worker>-<name>.card)` — atomic within the directory, so
two workers cannot take one card — and drains its own `taken/` before reaching for another bench.
When a bench's capacity line admits a worker and its queue is empty, it steals from the fullest
bench on the mirror's 5-minute timer.

**Invariant.** One card is on exactly one worker; the rename is the ownership record and there is
no central counter; a steal never empties the victim below its own capacity line.

**Red tests.** `two-workers-cannot-take-one-card`; `a-steal-never-starves-the-victim`.

## 3. Pull workers with leases and heartbeats

**Game engine.** A worker takes a work item together with a lease; it heartbeats the lease while
the item runs, and a lease whose heartbeat lapses is reclaimed by its own queue. No dispatcher
thread sits above the worker telling it when to start or stop, because the worker pulling is the
scheduling decision.

**Nova.** `nova-swarm pull` takes a `slots take` lease from the bench store (`<store>/shares.tsv`,
`capacity` and `reserve`) before it runs and renews it by `nova-work heartbeat` while the card
runs; a dead worker's lease past `until=` with a dead pid is reaped by the next `take` and its
card returns to `queue/`. The coordinator starts no slot and kills no slot it did not lease.

**Invariant.** No launch without a lease (rule 2 of **Bench slot leases**); a lease with a live
pid past `until=` is DRIFT, never reaped and never regranted; a card whose worker dies is
re-queueable and its partial `RESULT.md` is kept as evidence.

**Red tests.** `an-expired-lease-with-a-dead-pid-returns-the-card`;
`a-launch-without-a-lease-is-refused-by-the-puller`.

## 4. Affinity and warm state

**Game engine.** Tasks carry an affinity tag; the scheduler prefers a worker that already holds
the task's data in its cache or working set, because cache warmth is the difference between a
setup and no setup. A worker keeps its scratch between tasks and resets it per task, so the
expensive load is paid once and reused.

**Nova.** Every card carries a `kind` (`go`, `lisp`, `docs`, `schema-leg`) and a `repo`;
`nova-swarm pull` prefers the card whose `repo` it already has cloned in a kept worktree under
`<slot>/worktrees/<owner>/<name>`, falling back to a fetch from the bench mirror. The clone is
reused across cards, clipped after each by `nova-swarm batch`'s gather and `nova-work clip` —
commit the card's branch, harvest it, reset to base — so warmth is kept and history never leaks.

**Invariant.** A card never runs against another card's uncommitted state; a clip lands before
the next `pull` on that worktree, and the clone is reset to base rather than re-cloned.

**Red tests.** `pull-prefers-the-bench-that-holds-the-repo`; `a-clip-resets-the-worktree-to-base`.

## 5. Priority lanes

**Game engine.** A priority queue admits a small high-priority lane beside the bulk lane; work
that unblocks others or repairs a red is dequeued first, and small tasks run before large ones to
cut mean latency. When the rule cannot order two tasks, the scheduler asks a bounded typed score,
never a person.

**Nova.** `nova-pulse cut` writes `queue/lanes/{red,green,small,next}/`; `nova-swarm pull` drains
`red` (fixes to a red bench or a red PR), then `green` (small, already-approved PRs), then `small`
(the shortest step budget), then `next`. Ordering inside a lane is source order; a tie the rule
cannot break is asked of Jev as one typed decision in 400 ms behind the 0.9 floor, and a refusal
keeps source order.

**Invariant.** A lane is a directory, so it is visible and editable; no card is promoted by
editing prose, and a refusal by the scorer leaves the order deterministic.

**Red tests.** `red-lane-drains-before-green`; `a-scorer-refusal-keeps-source-order`.

## 6. Batching by shared clone

**Game engine.** Small jobs that touch one data set are packed into one worker turn: load the set
once, run each job, and reset between jobs, so setup is amortised and the worker's registers and
cache carry from one to the next.

**Nova.** `nova-swarm pull` takes up to `batch` cards of one `kind` and one `repo` in one turn on
one kept clone; `nova-pulse launch --batch` still carries the `nova-swarm batch` contract line per
card. After each card the clone is clipped (commit, harvest its `RESULT.md`, reset to base), so the
model's context carries from one card to the next without re-reading the repo.

**Invariant.** Every card keeps its own contract line and its own `RESULT.md`; a clip after each
means card n+1 never sees card n's uncommitted diff; a card over its `:effort` or turn budget
stops the batch and returns the remainder to `queue/`.

**Red tests.** `a-batch-clips-between-cards`; `a-card-over-effort-returns-the-remainder`.

## 7. Backpressure and idle

**Game engine.** A bounded queue refuses producers when full and parks idle consumers on a
condition variable; an idle core never spins, and admission is the count of free slots and the
measured load, not a hunch. The queue length is the backpressure signal, so the system settles at
its real throughput.

**Nova.** The capacity line `min(cores*1.5 - load1, (free_gb-25)/2, memfree_gb/2)` bounds how many
workers `nova-swarm pull` may start on a bench; a full bench stops pulling and leaves cards in
`queue/`. An idle slot whose queue is empty does not poll; it asks the coordinator for work by an
event (`nova-pulse watch`), and `nova-pulse width`/`status` print `STARVED` on the second
consecutive tick with pending work and free slots.

**Invariant.** Pulls never exceed the capacity line; a bench over it admits nothing new; `STARVED`
is a state the tool prints, never a note a person has to remember.

**Red tests.** `pull-never-exceeds-the-capacity-line`; `an-idle-slot-asks-on-an-event-not-a-poll`.

## 8. Events over polling

**Game engine.** Waiters block on typed events — job complete, data ready, timer expired — and the
completion path signals the one waiter that owns the join, so an idle system costs no cycles.
Polling is the fallback only where the platform has no event.

**Nova.** Four events wake a waiter: a queue change (`queue/` gained a card), a bus note, a job
done (a `RESULT.md` published by rename), and a CI verdict (a check conclusion or a merge). One
verb carries all four — `nova-pulse watch` (blocking, one return per change, never a per-minute
model turn), with `nova-wake watch` as its process shape outside a turn. `nova-pulse width` stays
the alarm; `watch` is the wait.

**Invariant.** Quiet time makes no model call and no subprocess poll; a watch that never changes
returns once at its deadline; a false wake from a non-round-tripping state value is a defect, not
a cadence.

**Red tests.** `watch-returns-once-per-change`; `quiet-time-makes-no-model-call`.

## Migration: push launcher to pull worker, in three steps

Each step shadows the last, so the old launcher can be restored until the numbers move, and each
number is read from `usage.tsv` and the queue — never from a report body.

1. **Shadow the queue.** Write `queue/lanes/` and a per-bench directory beside the existing pool,
   run `nova-swarm pull` with workers that are *not* yet exclusive (the old `launch` still fills
   free slots), and take each card by `rename`. Measure **cards per hour**; the target is the
   merge queue's 10 to 15 PRs an hour, and the first hurt is a slot idle while the window holds a
   card.
2. **Make the puller the only driver.** Give `nova-swarm pull` its bench lease and heartbeat, a
   kept worktree per repo with a clip after each card, and the capacity line from the mirror
   bench. Retire the push `launch` for admitted work. Measure **minutes per card** (median and
   p90 against 4 / 15.7 today) and **tokens per landed card** from the per-card cache reads the
   batch already sums.
3. **Lanes, batches and events.** Turn on the lane order, the shared-clone batch with its clip,
   and `nova-pulse watch` as the single wait. Measure all three again — **cards per hour**,
   **minutes per card**, **tokens per landed card** — against step 2, and keep the pull only
   where the three move together; a lane that raises cards per hour while lowering landed quality
   is a regression and the measurement says so.
