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

### Events, not ticks

The four events above cross the process boundary on local Redis pub/sub, so no merge-path
verb waits for a tick. The bridge is two verbs and one internal edge:

- `nova-work events --redis <addr> [--repo <owner>/<name>] [--base <branch>]
  [--gh-poll 60s] (--once | --deadline <duration>)` is the producer. It reads the
  `cards:done` stream with the consumer group `events` and republishes each entry as
  `card-done`. Because GitHub webhooks are not wired here yet, it also polls `gh` every
  `--gh-poll` for check-suite completions on open `rowan/*` pull requests and for the base
  branch's head, publishing `pr-checks-done {number, head, conclusion}` and
  `dev-moved {sha}` **only on change**. The poll is the fallback heartbeat the principle
  allows; a webhook later replaces it without moving the line between producer and
  reactor. A quiet poll publishes nothing.
- `nova-merge react --redis <addr> [--lane <dir>] (--once | --deadline <seconds>)` is the
  subscriber. On `pr-checks-done` success and not in the skip set (`enqueue:skip`, a Redis
  set) and not under `enqueue:hold` (a TTL'd key), it enqueues the PR once. On `dev-moved`
  it lists the `rowan/*` pull requests the move made DIRTY and publishes
  `rebase-wanted {number, head}`, which the rebase verb consumes. On `card-done` it
  publishes nothing, because the recorder and the harvester read the stream directly. The
  reactor holds no timer: it blocks on the subscription and returns once at its deadline,
  so an idle reactor makes no model call and no subprocess poll.

The gh edge is an interface with a fake; the bus is a real Redis addressed by `--redis`
and, under test, miniredis. Every loop has a `--deadline`. One action prints one line, in
the same grammar the sweep verb already prints.

**Red tests.** `producer-publishes-card-done-from-the-stream`;
`producer-publishes-pr-checks-done-only-on-change`;
`producer-publishes-dev-moved-only-on-change`; `reactor-enqueues-a-green-pr`;
`reactor-skips-the-skip-set`; `reactor-holds-on-the-hold-key`;
`reactor-publishes-rebase-wanted-for-dirty-prs`; `reactor-card-done-publishes-nothing`.

## 9. Resource vectors, writes and no barriers (Amendment 1, 2026-09-18)

The scheduling side of docs/SPEC-WORKLANG.md's **Amendment 1**. That part fixes the grammar and
lands the reader; this section fixes what the scheduler does with it. Rule numbers below are the
amendment's A1 to A14, and every red test named here is the kernel's, none of them implemented by
the reader slice.

**Game engine.** A job system does not admit a job into a slot; it admits a job against independent
reservations — cpu, memory, disk, network, gpu — held by one allocator per machine. Nothing in a
well-built one waits on a phase boundary: a job runs the moment its dependencies are done and its
reservations are granted, and a group that must finish together is expressed as a dependency, never
as a barrier every worker parks on. A nested scheduler is handed a sub-budget by the allocator above
it; it never counts the same cores twice.

**Nova.** A unit carries `:resources` (A5), and a slot becomes the degenerate one-dimensional case
of that vector. Admission is atomic across the vector's dimensions — all or nothing, never a partial
grant a unit then waits inside (A8). A **lane** is one dimension with capacity 1 over an area of the
tree, `queue/control/lanes.tsv` the map, so `nova-pulse fill` keeps one live card per lane and
unrelated lanes scatter and gather (A6). Two units whose `:writes` intersect serialize **even when
their lanes differ** (A7), because an area of the tree and a file are not the same grain. Every
other scheduler on a machine — the CI runners, an external engine such as Patrick's Tandem — holds a
delegated sub-budget from the one allocator, and a second independent count of the same cores is a
refusal, not a wait (A8). The day's evidence for all of it: load average 147 on the Studio with four
independent counters over 32 cores, and a darwin CI leg that was the queue's clock while 61 Linux
runners idled — a resource **kind** no slot count can see.

**Uncertainty is not idleness.** An attempt is a record with a termination proof (A3); a unit whose
last attempt cannot prove it stopped is `uncertain` (A4) and **keeps its reservation**. A lease past
its expiry with a live pid stays DRIFT and is never re-granted — section 3's rule, now written into
the work language rather than left to a reaper's judgment. The evidence is the same day's 55 orphan
harness processes hours past their cap, whose capacity had already been handed to someone else.

**No barriers.** There is no phase, wave or round in this scheduler. A unit goes when its own needs
are closed and its own resources are granted (A9); a work set's `:done-when` is a report of its
finish line and never a gate on its members. The pit-stop set of 2026-09-17 is the argument: 54 of
79 units name needs and the rest are independent, so a barrier idles most of the fleet behind the
slowest member of a group it has nothing to do with.

**Warmth and collections.** Warm state is retained and accounted apart from active resources (A12),
so a kept worktree, a toolchain image or a loaded compiler is never charged as running capacity and
never freed because it is merely warm — section 4's affinity, now with an account. Outputs whose
members cannot be listed before the run are `:collects` collections (A11): named before, bound to the
unit's revision at harvest. A unit's `:tools` name the verbs it needs at a version, with an optional
semantic key that invalidates its outputs when the tool's behaviour moves without its version (A10).

**Owners.** `:owner` is a mind — a friend, a child rung, a swarm, or `all` (A13). `nova-work ask`
(#1338) and the pull worker read that one form: the ask answers for the owner it finds, and the
worker takes only what its own mind owns or what no mind claims. One form, two readers.

**Invariant.** One authority per physical capacity; admission is atomic over the vector or refused;
a lane admits one live unit and intersecting writes serialize across lanes; an uncertain outcome
keeps its reservation until termination is proved or a fence is written; no unit ever waits on a
barrier, only on its own needs and its own resources.

**Red tests.** `jobs-admission-is-atomic-no-partial-grant`;
`jobs-a-nested-grant-draws-from-its-parent`; `jobs-double-reservation-is-a-refusal-not-a-wait`;
`jobs-a-download-asks-for-network-and-no-cpu`; `jobs-one-live-unit-per-lane`;
`jobs-unrelated-lanes-scatter`; `jobs-intersecting-writes-serialize-across-lanes`;
`jobs-uncertain-keeps-its-resources`;
`jobs-an-uncertain-attempt-is-never-re-granted-on-expiry`;
`jobs-a-ready-unit-goes-with-no-global-barrier`; `jobs-done-when-is-a-report-not-a-gate`;
`jobs-retained-warm-state-is-not-charged-as-active`;
`jobs-a-collection-binds-its-members-at-harvest`;
`jobs-a-tool-key-move-invalidates-the-unit`;
`jobs-a-unit-refuses-on-a-tool-below-its-version`;
`jobs-a-unit-without-acceptance-is-refused-at-load`;
`work-ask-reads-the-same-set-as-the-pull-worker`.

### What the kernel slice implements (internal/jobs, 2026-09-18)

`internal/jobs.Admission` is the authority: a `Grant` over a resource vector, a `Release`
and a `Snapshot` for the status line. It is deterministic, in-memory and single-writer --
one goroutine owns the state, like redis -- and it holds no condition variable, no queue
of waiters and no timeout, because A9 says a request is granted NOW or refused NOW, and a
waiter inside admission would BE the barrier A9 removes. Only `Release` frees a
reservation: no clock does, because an expiry is unknown until termination is proved.
`internal/worklang`'s `Unit.Request` is the one place a unit's form becomes a request, so
`nova-work set check --ready`, the pull worker and the kernel cannot drift apart.

Green here, each named as this section names it: `jobs-admission-is-atomic-no-partial-grant`,
`jobs-a-nested-grant-draws-from-its-parent`, `jobs-intersecting-writes-serialize-across-lanes`,
`jobs-a-ready-unit-goes-with-no-global-barrier`, `jobs-double-reservation-is-a-refusal-not-a-wait`,
`jobs-one-live-unit-per-lane`, `jobs-unrelated-lanes-scatter`,
`jobs-a-download-asks-for-network-and-no-cpu`. The rest of the list above -- the lease,
the uncertain reservation, the tool key, the collection harvest, acceptance at load and
the executor seam -- are still red and still unimplemented.

The pinned reading is the real set of 2026-09-18 (20 units, `work/pitstop-2026-09-18-units.lisp`,
copied verbatim into `internal/worklang/testdata`): 11 units have their needs closed and
8 of those may go, the other 3 held by A6 on the merge, pulse and ci lanes. On that file
the lane rule dominates and the writes intersection never fires among ready units,
because every pair that shares a path also shares a lane -- which is a reading about the
set, not about the rule: `certify:verb` and `certify:launchd` share
`fleet/launchd/com.rowan.fleet-certify.plist` and serialize on it with their lanes forced
apart.

### The executor seam

An assignment may name an external execution engine for its validation graph: the engine owns action
dependencies, artifact validity and supervision *inside* the sub-budget one allocator granted it, and
its results arrive as evidence bound to the unit's revision and its `:acceptance` criteria. The
boundary carries two things in both directions — a grant (drawn from the parent's reservation,
returned on release) and an outcome (with its termination proof, or `uncertain`). Neither side may
assume a disconnected process stopped. Reuse over invention applies: we do not grow our own build
graph engine, and the seam stays a proposal until an engine on the other side of it has incremental
records and runs on the platforms the fleet uses (ideas #783).

**Red tests.** `jobs-an-executor-draws-its-budget-from-the-parent-grant`;
`jobs-an-executor-result-binds-to-the-units-revision`;
`jobs-a-disconnected-executor-is-uncertain-not-stopped`.

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
