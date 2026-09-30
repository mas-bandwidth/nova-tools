# DirtyTick: the sprint machine's tick, the owner's shape of 2026-09-30

`DirtyTick.tla` is the tick of the sprint machine as the owner shaped it on 2026-09-30, written before the tick is built. `MCDirtyTick.tla` holds the small instances, each `MCDirtyTick*.cfg` is one case of `CASES.tsv`, and `dirtytick-bench/MCDirtyTickFull.cfg` is the larger run outside the plan. Rule names are those of `design/EVENT-DRIVEN-TICK-v2.1.md` sections 2 and 3 (R2, R6, R8, R9, R10).

## The shape

- Each table gets one update in turn per tick: 1. work, 2. readers, 3. merge, 4. fleet (the first pass).
- Every table has a queue; the dirty bit is the number of entries in it. A step that changes a table appends an entry to that table's queue. An update drains its whole queue in one plan over every row that needs it.
- A queue that is not empty is drained at once, at any point after the pump; the tick ends only when the queues of readers, merge and fleet are empty.
- Only the pump writes the work table, and it runs once per tick, first. Readers, merge and fleet write their own tables and queue the work table's changes (a finish, a read done, a broken read, a landing, a card returned, room freed); the next tick's pump applies the whole queue, lands sentinels, releases, and deals from ready. Nothing moves from waiting to ready to working but in that pump.
- A placement on a machine, a reader or a stream starts at a uint64 counter modulo the full ordered name count and advances past every skipped name plus the selected name; the counters persist across ticks.
- A machine has a width; a read takes room on its reader's host.
- The coordinator is woken once, at the tick's end, with the count of what the tick addressed to it (a sentinel landed, a card at its bound, no fleet member up, a machine lost with work on it), and not at all when the count is 0.

## What is modelled

| variable | what it holds |
|---|---|
| `col`, `att`, `bnd` | the work table: a card's column (none, waiting, ready, working, review, merging, landed), its attempt, at its bound |
| `rd`, `askw` | the readers table: the reader holding a card's read; a card waiting for a reader |
| `mq` | the merge table: the cards queued to merge |
| `stat`, `mc`, `mr` | the fleet table: a machine's status as the fleet knows it, the cards dealt to it, the reads it hosts |
| `noUp` | the judgment "no fleet member is up" |
| `Q` | the four queues |
| `mctr`, `rctr`, `sctr` | the placement counters, modelled mod 2 (2 divides 2^64 and every candidate count an instance has) |
| `live`, `acts`, `ext` | the outside: a machine beating, outside actions used, an outside entry since the last tick began |
| `phase`, `pumps`, `sub`, `addr`, `notes`, `wake`, `act`, `plc`, `brk`, `dealt`, `always` | the tick and its ghosts |

| action | what it is |
|---|---|
| `TickStart` | a tick begins when some queue is not empty (the loop wakes on the log) |
| `PumpWork` | the work table's one update: apply the queue in order; land every sentinel with every card before it landed; release every waiting card with no unlanded sentinel before it; deal: streams take turns by the stream counter over the streams with a dealable card, the lowest position within the stream, the machine by the machine counter over the up machines with room; "no fleet member is up" when cards are dealable and none is |
| `Pass(t)` | the first pass's update of readers, merge, fleet, in turn |
| `Drain(t)` | a queue of readers, merge or fleet not empty, drained at once |
| `TickEnd` | the three queues empty; one note iff the tick addressed anything |
| `Add`, `Finish`, `Report`, `Merge`, `Beat`, `Lapse` | the outside, between ticks; each appends one entry to one queue and writes no table |

The updates: readers take `ask` (from the pump), `rep` (a reader's report) and `unread` (from the fleet), then place every card in askwait by the reader counter over the readers whose host is up and has room. Merge takes `queue` (from the pump) and `merged` (the merger's verb), and queues `landed` to work and to fleet. Fleet takes `beat`, `lapse`, `dealt`, `fin`, `readon`, `readoff` and `landed`: a lapse returns the machine's cards to the work queue and its reads to the readers queue; room freed or a machine up is a `room` entry to work (and to readers when a card waits for one).

## Properties

| property | kind | holds on the shape with both repairs |
|---|---|---|
| `WorkChangesOnlyInPump` | action | yes: no step but the pump changes `col`, `att` or `bnd` |
| `WorkAdvancesOnlyInThePump` | action | yes: no card leaves waiting or ready, or enters working, but in the pump |
| `WorkPumpedOnce`, `WorkPumpedEveryTick` | invariant, action | yes |
| `QueueDrainedByPump`, `WorkQueueDrainedOnlyByPump` | action | yes: the pump empties the work queue; every other step only appends to it |
| `ThreeQueuesEmptyAtTickEnd` | action | yes |
| `NothingLost` | invariant | yes: every card's token is in exactly one place (a table row or one entry on its way) for working, for its read, for its merge and for its read's host; the attempts equal the broken reports applied |
| `EveryRowWithWorkMoves` | action | yes: after the pump, no card it could land, release or deal is left; at the tick's end, no card waits for a reader that could take it |
| `OneWakePerTick`, `NoWakeIfNothing` | invariant, action | yes, and the note carries the tick's count |
| `PlacementsRound` | action | yes: each placement starts at the counter modulo the full count and takes the first eligible candidate in ring order; each placement advances its counter past skipped candidates plus the selected candidate; nothing else moves a counter |
| `WidthRespected` | invariant | yes, only with `pendingroom` (G2) |
| `StreamFairness` | action | yes: two streams with a dealable card left after every pump so far are dealt within one of each other |
| `TickBounded` | invariant | yes (at most 12 steps a tick), only with `seefleet` (G1); the longest tick in the full instance is 11 steps |
| `Terminates` | temporal | yes on the small scenarios, only with `seefleet` (G1Live) |
| `WorkNotStranded` | temporal | yes on the small scenarios: work queued at a tick's end is pumped by a later tick |

Why the tick ends: only the pump writes the work table, and it runs once, so nothing the other updates queue to work is acted on inside the tick. Among readers, merge and fleet, merge's entries go to work and fleet only, and fleet's to work and readers; the one cycle is readers and fleet (`readon`, `unread`, `room`). It ends because a read is placed only on a host the fleet table says is up with room, and the fleet table's statuses change only on outside entries, which arrive between ticks. `TickBounded` checks the bound; in every non-idle state a tick step is enabled by construction (a pass needs only its phase; at `drain`, a queue not empty enables `Drain`, all empty enable `TickEnd`), so the bound gives termination under the fairness of the tick's steps. `Terminates` checks the liveness form directly on the small scenarios. The liveness properties need ENABLED of the whole tick action, which costs about 30 s on 3,407 states, so the larger instances check safety only.

## Findings

Each is a counterexample TLC gave, checked by hand against the shape.

- **G1: a placement that does not read the fleet's status never ends the tick.** Readers place a read on a reader whose host the fleet table has as down; the fleet refuses the `readon` and queues `unread`; readers place it again; forever (`TickBounded` in 115 states; `Terminates` in 140). The trace: c1 in review, its only reader's host m1 down; TickStart, PumpWork, Pass(readers) places the read on r1, then the fleet and the readers trade `readon` and `unread` until the step count passes 12. The repair (`seefleet`): every placement reads the candidates' status from the fleet table. The shape as stated says nothing about what a placement reads; the model says it must.
- **G2: room read from a fleet table whose queue is not drained over-fills a machine.** The pump deals c1 to m1 (width 1), which queues `dealt` to the fleet; the readers pass comes before the fleet's and reads m1's load as 0, so it places c2's read on m1's reader; the fleet then applies both, and m1 holds 2 (`WidthRespected` in 8 states). The repair (`pendingroom`): a placement counts the placements already queued to the fleet, or only the fleet update places. This holds only if a read takes room on its host ("the readers dealing work might dirty the fleet table"); if reads take no room, there is no hole.
- **G3: the rules of v2.1 write the work table in their own steps.** R10 as section 2.3 writes it (the next attempt dealt at once) is one such rule, and it breaks `WorkChangesOnlyInPump` in 4 states. So do R2 (a down member's cards returned), R8 and R9 (review to merging), R3 (release) and R6 (deal): under this shape each becomes an entry in the work queue, applied by the next pump.

Two more points the witnesses show, which the shape must say:

- A queue entry is a change. An update that queues an entry each time it runs, whether or not it changed anything, never ends the tick (W13). An entry is appended only by a write that changed a row.
- The tick's own entries to the work queue must start the next tick. A loop woken only by outside lines strands work queued at a tick's end: a landing recorded by merge waits for the next outside event (W12, `WorkNotStranded`).

One latency, not a hole: a finish reaches the work table two ticks after it is queued (tick N's fleet update moves it to the work queue; tick N + 1's pump applies it and asks for a read, which the same tick's readers place).

One observation about the counter rule: it is exact while the candidate set is fixed. When the set changes (a stream empties and refills, a machine fills), the same counter modulo a new count can give one candidate two turns in a row. `StreamFairness` holds on the instances here; a long run whose streams empty and refill often is not checked.

## Reversed witnesses, goals and probes

Each witness turns on one broken rule (`Broken`); its control is the unbroken configuration of the same scenario, which passes with every property. Group times are from `tlacheck run` on a Linux bench (32 CPUs, TLC at 1 or 2 workers, 110 s budget per group): `dirtytick` 11 s, `dirtytick-small` 12 s, `dirtytick-three` 14 s, `dirtytick-witnesses` 14 s, `dirtytick-goals` 6 s.

| case | the rule broken | fails | states |
|---|---|---|---|
| W1 | the old per-row tick: the pump deals one card | `EveryRowWithWorkMoves` | 5 |
| W2 | a second work pump inside the tick | `WorkPumpedOnce` | 6 |
| W3 | a queue left for the next tick (a deferred dirty bit) | `ThreeQueuesEmptyAtTickEnd` | 18 |
| W4 | placement in name order | `PlacementsRound` | 31 |
| W5 | a wake per addressed card | `OneWakePerTick` | 3 |
| W6 | the fleet deals from ready to a machine that came up | `WorkChangesOnlyInPump` | 10 |
| W7, W7b | the counters reset every tick | `PlacementsRound`; with it alone, `StreamFairness` | 16, 31 |
| W8 | merge writes landed into the work table | `WorkChangesOnlyInPump` | 5 |
| W9 | the pump applies a broken entry twice | `NothingLost` | 23 |
| W10 | the fleet drops a finish | `NothingLost` | 5 |
| W11 | the pump plans release and deal on the state as read | `EveryRowWithWorkMoves` | 5 |
| W12 | only an outside entry starts a tick | `WorkNotStranded` | 21 |
| W13 | readers and fleet queue an entry to each other on every run | `TickBounded` | 118 |
| W14 | a note at every tick end | `NoWakeIfNothing` | 16 |
| W15 | the deal ignores width | `WidthRespected` | 128 |
| G1, G1Live | the shape without `seefleet` | `TickBounded`, `Terminates` | 115, 140 |
| G2 | the shape without `pendingroom` | `WidthRespected` | 8 |
| G3 | R10 as v2.1 writes it | `WorkChangesOnlyInPump` | 4 |

The controls: `MCDirtyTick` (two cards in two streams, two machines, two readers, one beat or lapse, the whole life with rework to the bound: 368,708 states), `MCDirtyTickThree` (three cards, no beat or lapse: 1,608,320), and nine small scenarios (sentinels, turns, a blind reader, width, a cold fleet, a landing, a rework, a finish, a lapse), the six smallest with the liveness properties. The probes, expected to fail, show the base reaches what the properties speak of: every card landed (`ProbeLanded`), a card at its bound (`ProbeBound`), a queue drained after the first pass (`ProbeLateDrain`).

The bench run, outside the plan: `dirtytick-bench/MCDirtyTickFull.cfg` (three cards, two machines, two readers, one beat or lapse), every invariant and action property: 9,884,260 states, no error, 49 s at 32 workers. The same instance with `MaxSub` lowered: 10 fails `TickBounded`, 11 holds over every state.

## What is not modelled

Clear and epochs (the counters' reset); two reads per attempt (one read each); the coordinator's accept (a read ok is the machine's accept, R9); take (a finish needs none); verbs other than add, finish, report, merge, beat and lapse; outside actions during a tick (they run between ticks: a verb's entry mid-tick lengthens the drain but is bounded by the verbs); byte and step budgets; two sentinels in one stream; the fleet redealing a working card itself (a lost machine's cards go back to the work queue, and the next pump deals them); the log (a queue entry stands for its line).
