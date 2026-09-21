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
   pit stop is open ([PIT-STOP.md](../PIT-STOP.md)), the policy's `scope-regex` admits fixes with
   reproducing tests, reads and rebases; anything expansionary is labelled `next-push` in its
   issue and never carded. We choose to be done. Replay: `contraction-phase-cards-bugs-only`.
8. **The coordinator is a friend.** Every broadcast includes it; adoption is a mechanical step
   with a receipt; `status` names it on its own `ADOPTION` line (replay 34).
9. **Parallelism and time remaining are printed, never guessed** — `progress`, above.

The exit of a pit stop is a trust batch: the fix cards of the stop rerun as one batch and every
one scores `done` with its red line quoted, before the queue widens again
([PIT-STOP.md](../PIT-STOP.md), **The exit gate**).
