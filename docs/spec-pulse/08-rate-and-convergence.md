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
   never by a person noticing. **The gate is read at the moment of PLACEMENT, on every road
   into a bench** (`internal/pulse/placement.go`): `fill` and `run`'s launcher both hold a
   card against it, the forge is asked once per distinct pull request per tick, and a gate
   whose answer cannot be had stays shut. `manager` takes the `AFTER:` line off the card the
   cycle it sees the merge, before its refill, so a card whose dependency landed is ready in
   that same cycle. Until 2026-09-18 the line was written and counted and nothing read it, and
   a card gated on a pull request that did not exist went out on a bench.
   Replay: `gated-card-launches-on-merge`.
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
10. **One writer per queue.** `<queue>/.lock` is taken by every verb that writes the queue —
    `run`, `fill`, `manager` and `loop` — and carries the holder's pid, the kernel's start
    stamp for that pid, a nonce, the verb and when it started. A second writer refuses at
    exit 2 and NAMES the holder; a lock whose holder is not running is taken over once, with
    no wait, because a `SIGKILL`ed loop would otherwise stop the bench until somebody noticed
    a file. **`O_EXCL` alone is not enough**: it creates an empty file and the content
    arrives after, and a reader in that window deletes a live owner's lock. The record is
    written to a temp file and HARD-LINKED onto the lock name, so creation and content are
    one step; release unlinks only a record whose nonce is still the handle's; stale recovery
    is serialized through `<queue>/.lock.take` and re-reads the holder under it; and
    reentrancy is by nonce, never by pid, because a recycled pid is not the same process.
    Replay: `second-writer-refuses-naming-the-holder`.
11. **One verb is the loop.** `nova-pulse loop` is one tick of `run`, then `fill`, then
    `manager`, under one lock, then the launch-dead probe, then one `LOOP TICK` line. Every
    placement on either road is held against the same `--machines` registry and `--lanes`
    table, so a card refused on one road is refused on the other. A launched card whose job
    directory never appeared inside the launch grace is given back to `pending` with its
    marker, which releases its lane. **Every table is read before the first tick** — a path
    that is merely non-empty is not a registry — and **a step that failed is not a quiet
    day**: its exit code is counted under `failed=`, a failed run tick stops the fill, the
    manager and the probe, and the loop's own exit is non-zero. Replays:
    `loop-tick-is-run-fill-manager-in-order`, `launch-dead-releases-the-lane`,
    `loop-stops-dependent-steps-and-exits-non-zero`.
12. **A read-only mode is whole or it is refused.** `fill --dry-run` and `manager --dry-run`
    change nothing and say so; the run tick's six seams have no read-only mode, so
    `loop --dry-run` is REFUSED rather than half-kept (nova-tools #1441). A dry run that
    still harvests, merges, reaps, cuts and launches is worse than no flag: it is a flag a
    person points at a live queue BECAUSE they were promised nothing would move. The proof is
    a byte-for-byte snapshot of the whole queue, before and after, through the real command
    line — never a library call with the step under test stubbed out.
    Replay: `dry-run-touches-nothing-through-the-cli`.

The exit of a pit stop is a trust batch: the fix cards of the stop rerun as one batch and every
one scores `done` with its red line quoted, before the queue widens again
([PIT-STOP.md](PIT-STOP.md), **The exit gate**).
