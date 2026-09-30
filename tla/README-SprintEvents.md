# SprintEvents: the upper layers of nova-sprint as a TLA+ model

`SprintEvents.tla` is the state machine of layers 3 to 8 of nova-sprint, written from section 5 of `design/EVENT-DRIVEN-TICK-v2.1.md` ("the design" below, cited by section), with sections 1.1 to 1.6 and 2 as the text it models. It is item IT24 of the design. `MCSprintEvents.tla` holds the small instances (the scenarios), and each `MCSprintEvents*.cfg` is one configuration. The larger runs are in `tla/sprintevents-bench/`. The repairs are the decisions of errata 3 to version 2.1 (`design/EVENT-DRIVEN-TICK-v2.1-ERRATA-3.md`, as amended at 03:20 and by its amendment 2 at 04:35), each behind the `Fixes` constant.

The model stands on layers 1 and 2 as their own models prove them (`tla/SetTable.tla` and `tla/SetTableLog.tla`, on their own branches): one call at a time, a step all or nothing, guards checked at apply, one place per card and table, a revision that moves with every change of a record, a receipt that makes a part identity apply once, one line per change in the step's own commit, and a function that errors keeping the writes it made before the error. So a step here is one atomic action, a revision guard is "the record is as read", and a refusal writes nothing.

## What is modelled

**The state** (section 5's variables, as far as the model keeps them).

| variable | what it holds |
|---|---|
| `col`, `score`, `fld` | the work table: each card's column, score and the fields the rules read (open, refused, bound, attempt, redeals, rereads, avoid, result, waived; and `tries`, the untaken withdrawals since the last take, kept with `unplaced`, and with `stablesince` as a ghost) |
| `wk` | a primary's live work card: its places (a set, so that a card dealt twice is a state `OnePlace` sees), generation, taken, `untaken_replaced`, the member it was dealt to, and the timers of the two work-card deadlines, each as the card's field and as its due entry |
| `rd` | the read cards of a primary's attempt, by reader, with the timer of their one deadline (unbegun and unreported as one) |
| `mi` | a stream's merge-idle timer and entry |
| `status`, `beat`, `seen` | a member's control card (up, down, held), its `beat:m` entry, its `seen:m` entry |
| `stab`, `sw` | with the `stablesince` repair only: a member's `stable_since` as a timer (not up, up and not yet stable, stable), and R6's 30 s entry (unset, armed, due, fired) |
| `waitn`, `missing` | the indexes written by intents (`wait:n`, `missing`) |
| `J` | `jopen`: one record per (type, subject, cause), open or held, with the hold's entry |
| `agenda`, `parked` | the rule keys queued, the parked keys |
| `log`, `cur` | the lines not yet dropped, each with the keys ingest gives it and how often it was ingested; the cursor |
| `running`, `clk` | RUNNING or STOPPED; R17's fields (`due_since`, `stophold`, raised in this span), in wall time |
| `remind`, `pushes` | one person's remind entry; the pushes made (mod 2) |
| `dropping`, `cut`, `receipts`, `next` | the dropping marks, the cut entries (wall time), the part receipts with their continuations, the score counter |
| `lease`, `tk`, `vk` | the lease; the tick processes (their round trip, the page read, the plans, the pending requests, the halved keys); the verb processes (with a drop's fresh reads after a `freezefirst` refusal) |

The indexes that are functions of the fields (`sent`, `elig`, `fresh`, `again`) are operators, not variables: X derives them in the step that changes a card, from its real before-state (1.3.2), so they cannot disagree with a move that applied. A witness that breaks a definition (W9, W26) changes the operator.

**Time** is relative, in units of one deadline. Every timer holds what remains until it is due: 1, 0 (due), or -1 (unset). `Wall` advances time by one unit: running-time timers and entries move only while RUNNING, and the cut entries and R17's fields always. The state stays finite however long a behaviour runs, and a deadline passing is an event under fairness. Every timer is one unit long, so the model cannot order two timers inside a unit (15 s of beat freshness against 15 minutes of an untaken deadline, or two beat periods of `stable_since` against 15 s of beat freshness); that matters for H12 and H15 below.

**Actions.** Plan and apply are separate for every rule and every verb, R17 included.

| action | what it is |
|---|---|
| `RT1(t)` | the tick's first step: the lease part (take a free lease at gen + 1, or renew one's own), then the pop part: every due entry's key into the agenda first, then the entries removed (A1, D3) |
| `ReadEvents(t)` | RT1's page: the lines after the cursor, read with the cursor |
| `RT2(t)` | the ingest part, refused unless the gen is current and the cursor is where the page starts (INGESTAT); keys first, then the cursor. Then, RUNNING, every key of the agenda is planned on the present state and cut into requests by the builder (1.3.6), in the round robin's order (1.4.2). STOPPED, the look (1.4.5): the rules plan dry, R17 plans its step on the clock as read (its fields, and its judgment), and the cut clock's judgments are the only other rule steps sent. RT2 writes nothing but the ingest |
| `Apply(t)` | one request, atomic alone. LIMIT first (the size), then STALEGEN, STOPPED and the guards (a race writes nothing; the key stays). The error step that names a LIMIT carries the lease generation like every tick step (1.3.5): a stale loop's is refused STALEGEN and writes nothing. A plan that fits one request removes its key unless it requeues; a cut plan removes none (1.3.6). R17's step is one such request, guarded by the clock fields as read (2.3, R17), and with `stopinputs` by the cards as read |
| `ErrorRT1(t)`, `ErrorRT2(t)` | an error between the two writes of the pop or of the ingest: the first write kept, the second lost |
| `ParkOnBug(t)` | a request refused as a bug other than LIMIT: named, and its key parked, by an error step that carries the lease generation (a stale loop's writes nothing) |
| `TickCrash(t)` | a loop dies; what it held in memory is gone |
| `LeaseExpire` | the lease expires with no renewal (bounded by `MaxLease`) |
| `RunTwice(t)` | the probe of E7: a second plan and apply of a key right after a run that removed it or changed nothing, while the facts are as that run's plan read them |
| `VerbPlan(v)`, `VerbApply(v)` | a verb's read (its arguments and a snapshot of what its guards compare) and its step: add (named, a sentinel, an insertion at an odd score), take (with `unplaced`, it ends a card's count and restarts R6's 30 s entry), finish, read (begin, ok, broken), merge (land), release, drop, rank, rework, ack (with waive), wait, wait on the STOPPED judgment, fleet down, fleet up, start, stop |
| `PartPlan(v)`, `PartApply(v)`, `AbortApply(v)` | `drop --stream` in parts, under an op name with no receipt: part 1 marks the stream, each part removes the head of the current cell and moves the cut clock, the last part clears the mark and writes the request line; with `freezefirst`, a part refused because a card moved back to a passed cell returns the verb to read again, at most `PartRereads` (2) times; a resume reads the receipts; `--abort` |
| `VerbCrash(v)` | a verb's process dies between its read and its step, or between parts |
| `Beat(m)` | a member beats: `beat:m` to R + 15 s, and `seen:m` at R for a member that is down. Unbounded and unfair |
| `Wall` | one unit of time |

**The rules** modelled are R1 (seen), R2 (down), R3 (resolve), R4 (needs, made), R6 (deal), R8 (ask), R9 (accept), R10 (rework), R11 (late: untaken, unfinished, unread, merge-idle, the cut clock), R13 (hold), R14 (remind, phase 1), R17 (stopped) and R19 (pull back), each with its plan, its units' guards and its effects as 2.3 states them, and J's one per cause decided at apply.

## What is not modelled

The same list stands at the top of `SprintEvents.tla` and in the pull request's description.

- R5 (cross), R7 (level), R12 (overdue), R15 (done), R16 (held) and R18 (behind). R16's table is the invariant `NothingSilent` instead (with a row for R6's 30 s entry under `stablesince`); the held queue with its cap, and `HeldDrop`, are not kept.
- Quarantine: a refusal naming a card is a layer-1 bug, and layer 1's model shows it does not happen.
- Clear, epochs and remove, so `EpochSafe` is not checked; CI, return, merge stops and resume; reader add; add in parts and insertion's anchors (an insertion is an add at an odd score); `ask --another`; the coordinator's `accept` verb (R9, the machine's accept, is modelled).
- R11's `idle:s` kind (a notice, no judgment), and R1's strangers (a beat from a member with no fleet row).
- The `askwait` index, and I3 for it and for `fresh` above sigma: `HeadActionable` checks `elig` below sigma, deal's heads and `wait:n`.
- `NoLostWork`'s "parked (and named)": a parked key counts as owed without the check that its judgment is open. `Named` does check a parked key per card.
- Byte and read budgets other than the step bound; the tick's step budget (a tick applies every request it planned); R19's one step a tick.
- The rules' reads as separate snapshots: a tick plans every key on one snapshot, then applies request by request, with every outside action free to run between two requests.
- A note line that queues no key is not written.
- The order of two keys of one priority in the round robin is the one TLC's `CHOOSE` gives; the design leaves it open, and a trace that needs the other order needs another scenario.
- The row of "this card cannot be placed" (H15's judgment): its decisions are not decided; the model prints `drop` only, and the judgment is neither held nor acked.

## The instances

`MCSprintEvents.tla` names the scenarios. Every configuration uses 2 streams (`s1`, `s2`), 2 members (`m1`, `m2`), 1 reader (`r1`; 2 where the review path is checked), 1 verb process and 1 tick process (2 where two loops race), 1 op name (2 in every configuration with `drop --stream`), and some of the cards `p1` (s1), `p2` (s2), `p3` (s1) and the sentinel `g1` (s1). Scores are even integers from the counter; rank and insertion choose odd ones below it (U2). The constants the gated instances use: `StepChunk` 2, a step bound of 1 unit (2 with layer 1 accepting 1 in the LIMIT cases; 2 with layer 1 accepting 2 in `MCSprintEventsMulti`, where a release of two cards and a deal of two cards each apply as one request), `MaxAttempts` 2, `MaxRedeals` 2, `MaxRereads` 1, `MaxPlaceTries` 3 (1 in `MCSprintEventsGoalPlace` and `MCSprintEventsRepairPlace`), a ready cap of 1 (2 in three bench runs), generations mod 2 (3 in three bench runs), and 1 to 3 outside actions. Members that beat are either `Beaters` (they may beat, stop and beat again, unfairly) or `Steady` (they never stop). `ScnTurns` (`MCSprintEventsC28`, `MCSprintEventsW28`) is the instance of the deal's order: p1 and p3 ready in s1 (2, 4), p2 ready in s2 (6), both members up with room for two. It has two streams and s2 holds one card, so the "before in the streams' order" branch of `DealTakesTurns` is checked for one pair of streams only.

**The gap to section 5's instance.** Section 5 asks for 1 stream (2 for the cross rule, R15 and remove), 4 primaries and 1 sentinel, 2 members, 3 readers, a step bound of 3 entries (so a plan of 4 changes is cut into two requests), a budget of 3 keys, a held queue capped at 2, and `MaxActs` 6. No instance here reaches it: at most 3 primaries and the sentinel, 2 readers, a step bound of 2, no key budget and no held queue, and at most 3 outside actions (2 in every run with every repair). A request of two units applies in `MCSprintEventsMulti` (and its probe, `MCSprintEventsMultiReach`), but no plan of 4 changes is cut. **Owed:** a bench run of section 5's instance to completion with every repair, with symmetry sets or one smaller menu of verbs per run; the one attempted with every verb but the drops and every repair (`MCSprintEventsFull2`, 2 verbs) does not finish inside the 900 s cap (Results).

## Properties

| property | kind | section 5 |
|---|---|---|
| `OnePlace` | invariant | a card in at most one place in each table |
| `Lifecycle` | action | every change of a card's column is a row of `Moves` (an action property: it speaks of steps) |
| `IngestOnce` | invariant | E2: the lines at or before the cursor ingested once, the others not |
| `CursorSound` | invariant | E3 (with `TrackSeen`): every key of a line at or before the cursor is owed or quiet |
| `NoLostWork` | invariant | every rule whose plan on the present state is not empty is owed: its key queued or parked, on a line after the cursor, or an entry will queue it. R1's key is left out: a down member's return is owed only to a beat, which is unfair |
| `DueAgrees` | invariant | D1, holds included |
| `IndexAgrees`, `OpenExact` | invariant | I1 for `wait:n` and `missing`, I2 |
| `HeadActionable` | invariant | I3 for elig below sigma, deal's heads, and `wait:n` of a landed or removed need |
| `PositionHolds` | invariant | no primary past ready while a sentinel that was ahead of it at its first deal is unlanded and ahead; no sentinel landed with an open card before it |
| `NeedsHold` | invariant | a primary outside waiting has every need landed or waived; no missing need waived while it had a record |
| `NothingSilent` | invariant | every open card has a local holder, row by row of R16's table |
| `JudgmentOnce`, `WaitHolds` | invariant | one open judgment per cause; none while a hold on it is before its time |
| `Answerable` | invariant | every decision printed for an open judgment is accepted by its verb's guard now (the decisions of 2.2 whose verbs the model has, with their stated conditions) |
| `OpOnce` | invariant | V4 |
| `DropComplete` | action | V6: while an op of `drop --stream` is in flight on a stream (a part receipt with no final part and no abort), only the op's parts move its cards; when a drop records its last part, no card of the stream is open. Keyed on the receipts, not on the mark, so a drop without its mark (W13) is seen |
| `UniqueScores`, `ScoresBelowCounter` | invariant | U1, U2 |
| `HeldSticky` | action | a member held by fleet down stays held until fleet up |
| `StoppedJudgmentTrue` | invariant | the STOPPED judgment open only when a dry plan would change a card; raised at most once per span. It fails with every repair (H7: a verb empties the dry plans between two looks); `StoppedOnce` is its second half (once per span, and per wait with `spanreset`, per close with `stoprearm`), and `StoppedStaleCloses` and `StoppedRaiseFresh` below say what the repairs give |
| `StepWithinBounds` | invariant | every request fits the step bound |
| `DealTakesTurns` | invariant | errata 3, amendment 4 (R6): the plan the deal would make takes each stream's front in turn: within a stream its lowest by work order, and no stream is taken from more than once past a stream with a dealable card left (once, only if it is before it in the streams' order); stated from the counts of the plan, not from `TurnSorted`, which the plan uses |
| `LeaseSafe` | action | E4, T1: every tick write carries the current lease generation, the error steps of a LIMIT and of a bug refusal included |
| `ReplayNoop`, `QuietStaysQuiet` | action | a rule run again at once writes nothing |
| `ChunkProgress` | action | a requeue lowers the key's variant |
| `PlaceOnlyUp` | action | a card placed in a member's ready cell only while the member is up |
| `RedealsCounted` | action | R2 and R11: a work card that leaves working other than by a finish or a drop raises its primary's `redeals` by one, up to the bound (W7b's property: under bounded outside actions a take is an outside action, so an uncounted redeal cannot make an infinite behaviour, and `Progress` cannot see it) |
| `StoppedRaiseFresh` | action | R17 raises its judgment only on a dry plan that still changes a card when its step applies (the claim errata 3 makes for the R17 split; with `stopinputs` it holds for a verb that changes a card, and fails for one that changes the fleet: H17) |
| `UnplacedNamed` | invariant | H15's repair: a card dealt and withdrawn `MaxPlaceTries` times without a take is named until it is taken (the repair's own claim, with the count a ghost under `stablesince` alone; `Progress` in a flapping fleet is the bench's) |
| `Progress` | temporal | every open primary ends or is named, where a card that waits on a named card (an open need, the first sentinel, the cards before a sentinel, the cards filling every ready cell deal may fill: (d) of R16's table) is named through it, and a parked key names only the cards its rule could move |
| `ProgressScoped` | temporal | H6's decision: `Progress`, or the machine STOPPED with no move due (the owner's choice) |
| `HoldEnds` | temporal | a STOPPED hold ends (or the machine starts): the design's liveness argument rests on holds ending |
| `StoppedStaleCloses` | temporal | a STOPPED judgment left open with no move due is closed by a later look (or a move is due again, or the machine starts) |

`ProgressLiteral` is section 5's `Progress` with `Named` as written; `CursorSoundLiteral`, `IndexAgreesLiteral` and `DueAgreesLiteral` are E3, I1 and D1 as written. Each of these fails on the design (Findings). `ProbeTwoDrops`, `ProbeTwoMoves` and `ProbeReread` (in `MCSprintEvents.tla`) are reachability probes, expected to fail: two drops each reach their last part in one behaviour; one step moves two cards; a part of a drop applies after the verb read again on a `freezefirst` refusal.

Fairness is on each tick process's steps and on `Wall` only.

## Reversed witnesses

Each `MCSprintEventsW<n>.cfg` turns on one broken rule (`Broken = "W<n>"`) on a scenario where it matters, and `MCSprintEventsC<n>.cfg` is the same scenario on the design, checked with every safety property (and `Progress` for the liveness rows). Every control passes; five of them run with the repairs their scenario needs (C12, C13, C14, C21, C22), and W5Reach's control is `MCSprintEventsRepairRank`. Trace lengths are from the gated runs.

| witness | the rule broken | fails | trace |
|---|---|---|---|
| W1 | ingest without comparing the cursor (two loops) | nothing: passes, defence in depth (Findings) | |
| W2 | ingest moves the cursor before it adds the keys, with an error between | `NoLostWork` | 4 |
| W3 | due entries scored in wall time | `DueAgrees` | 4 |
| W4 | deal without the zguard on `sent:s` (a sentinel ranked ahead between plan and apply) | `PositionHolds` | 7 |
| W5 | reach, and the `release` verb, without the `rcount` guard on the open cells before sigma (the verb releases g1 with p1 before it) | `PositionHolds` | 3 |
| W5Reach | reach and unreach without that guard (the `release` verb keeps its own), with `rankclose` (H13 closed): a card ranked before g1 between reach's plan and its apply | `Answerable` (the `release` printed, refused by the verb's own guard) | 7 |
| W6 | a tick write without the lease generation (two loops) | `LeaseSafe` | 6 |
| W7a | an untaken card replaced without end | `Progress` | 17, lasso from 3 |
| W7b | a redeal not counted when a taken card leaves unfinished (with a take) | `RedealsCounted` | 9 |
| W8 | `ApplyNeeds` writes `open` = the value its plan read, less one (two needs landing in one tick) | `OpenExact` | 10 |
| W9 | a refused card left in its index | `HeadActionable` | 5 |
| W10 | a hold's expiry writes no line | `Progress` | 20, lasso from 18 |
| W11 | a sentinel line does not queue `deal` | `Progress` | 20, lasso from 18 |
| W12 | the STOPPED judgment's hold in running time | `HoldEnds` | 18, lasso from 16 |
| W13 | a drop in parts without the dropping mark, with C13's repairs | `DropComplete` (a withdrawal moves a card of the stream while the op is in flight) | 17 |
| W14 | a drop's selection resumed by offset | `DropComplete` | 4 |
| W15 | `needgone` leaves the waiter in `wait:n` | `ChunkProgress` | 7 |
| W16 | a wait's close queues its owner key, and J ignores the hold | `WaitHolds` | 11 |
| W17 | rank to a score above the counter without raising it | `ScoresBelowCounter` | 3 |
| W18 | deal, and the redeal of a withdrawn card, without memberup (fleet down between plan and apply) | `PlaceOnlyUp` | 7 |
| W19 | R2 sets a held member down | `HeldSticky` | 5 |
| W20 | R17 counts moves due from the backlog | `StoppedJudgmentTrue` | 12 |
| W21 | the late-work judgment offers rework | `Answerable` | 7 |
| W22 | waive of a missing need that has a record | `NeedsHold` | 5 |
| W23 | a step over the bound refused and its key requeued unchanged | `Progress` | 5, lasso from 2 |
| W24 | the pop removes its entries before it adds its keys, with an error between | `NoLostWork` | 3 |
| W25 | R14's phase 1 without its guard on the entry's score | nothing: passes, defence in depth (Findings) | |
| W26 | release reads waiting instead of `elig` | `NeedsHold` | 5 |
| W27 | the first request of a cut plan removes the keys (the second refused STOPPED) | `NoLostWork` | 11 |
| W28 | the deal takes the room lowest over the whole table (the design's body for R6), not each stream's front in turn (amendment 4) | `DealTakesTurns` | 1 (the instance's first state: p1 and p3 of s1 taken, p2 of s2 left) |

So 30 witness configurations: 28 fail with the property their row names, and W1 and W25 pass, because the design holds there with a second guard (the lease generation for both). Section 5's table claims W6 fails `OnePlace` "or a card dealt twice": it fails `LeaseSafe`, the rule itself, and `OnePlace` holds for the same second guards (place, revision and absence on every rule step); W7's row is two changes, split here, and W7b's claim of `Progress` cannot fail under bounded outside actions (its property is `RedealsCounted`).

The liveness witnesses and their controls use `Steady` members, so that a failure of `Progress` there comes from the broken rule and not from H12.

## Repairs

`Fixes` names the repairs, one per decided hole, each as errata 3 states it (amended at 03:20, and by amendment 2 at 04:35, which confirms the model's forms of H2, H3, H8 and H11 and decides H14, H15, H16 and the H13 gap). The design as written is `Fixes = {}`: every witness but W5Reach and W13 runs it, and so does every goal configuration of a hole in the design, except that GoalCut and GoalFreeze run the repairs of the other holes on their path (so that each fails for its own). The goal configurations of a hole in a repair run that repair and not the one decided for the hole: GoalStaleRaise and GoalRearm run `stopclose` (H14 fails the same way on `Fixes = {}`, a manual probe; H16 cannot, since nothing closes the judgment there, and GoalRearm passes on `Fixes = {}`, a manual probe); GoalStaleFleet runs `stopclose` and `stopinputs`; GoalPlace runs `seenfresh` and `stablesince`. A configuration that checks the other properties on a scenario that meets a hole names the repairs it runs with, so that the rest of the design is still checked there; which configuration needs which repair was found by running it without (Findings).

| repair | hole | errata 3's decision, as the model has it | a configuration that fails without it | one that passes with it |
|---|---|---|---|---|
| `judgeguard` | H1 | a line that queues `deal` (room freed, a member up, a card ready) also queues the late key of every work card past its deadline; a replacement closes the lateness judgment | `MCSprintEventsGoalTakeRoom` | `MCSprintEventsRepairTakeRoom`, `MCSprintEventsFleet`, `MCSprintEventsFaults`, `MCSprintEventsMulti`, `MCSprintEventsDrop2` |
| `seenfresh` | H2 | R1 sets a member up only when its beat is above the present R, checked at plan and again at apply; an empty plan of R2 carries a guard-only unit on the control card as read, so it cannot remove its key past a change | `MCSprintEventsGoalSeen` | `MCSprintEventsC21`, `MCSprintEventsLand`, `MCSprintEventsFleet`, `MCSprintEventsRepairPlace` |
| `madeclose` | H3 | the add that creates n closes "blocked on something missing: n" on its waiters in its own step; the row prints `add n` only while n has no record | `MCSprintEventsGoalMade` | `MCSprintEventsC22` |
| `cutmark` | H4 | R11's cut judgment guards that the op's marks stand | `MCSprintEventsGoalCut` | `MCSprintEventsC13`, `MCSprintEventsC14`, `MCSprintEventsDrop`, `MCSprintEventsDrop2` |
| `spanreset` | H5 | a wait on the STOPPED judgment lets R17 raise it again once the hold has passed | `MCSprintEventsGoalSpan` | `MCSprintEventsC12`, `MCSprintEventsRepairStopped` |
| (H6) | H6 | not a repair: `Progress` is claimed while RUNNING or while a move is due (`ProgressScoped`) | `MCSprintEventsGoalStopped` (`Progress`) | `MCSprintEventsRepairStopped` (`ProgressScoped`) |
| `stopclose` | H7 | R17 closes its judgment at a look that finds no move due; R17's step is planned and applied apart (always, in this model) | `MCSprintEventsGoalStale` | `MCSprintEventsRepairStale` (`StoppedOnce`, `StoppedStaleCloses`); `StoppedJudgmentTrue` itself still fails (`MCSprintEventsGoalStaleClose`, H7) |
| `stopinputs` | H14 | R17's step also guards on the version of what its dry plans read, as read: modelled as the revision of every card (a counter per stream, over every stream), which covers the streams' heads and the sentinels' scores | `MCSprintEventsGoalStaleRaise` | `MCSprintEventsRepairStaleRaise` (`StoppedRaiseFresh`); a change of the fleet is not covered (`MCSprintEventsGoalStaleFleet`, H17) |
| `stoprearm` | H16 | a close of the STOPPED judgment clears `raised`: R17 raises once per span and close | `MCSprintEventsGoalRearm` | `MCSprintEventsRepairRearm` (`ProgressScoped`, `StoppedOnce`, `StoppedStaleCloses`) |
| `dropcond` | H8 | no decision is printed that DROPPING refuses: `drop`, `rework`, `release`, `land`, `ack` of a dropped, missing or refused judgment, on a card of a stream being dropped, and `add n` while n's stream is being dropped; X refuses such an ack DROPPING (the design, section 3: the model's `ack` guard has it unconditionally) | `MCSprintEventsGoalDrop`, `MCSprintEventsGoalDropAck`, `MCSprintEventsGoalDropAdd` | `MCSprintEventsRepairDrop`, `MCSprintEventsRepairDropAck`, `MCSprintEventsRepairDropAdd` |
| `downdeal` | H10 | the down or held line of a member also queues `deal` (the decision's second clause is withdrawn) | `MCSprintEventsGoalDownDeal` | `MCSprintEventsRepairDownDeal`, `MCSprintEventsFaults` |
| `freezefirst` | H11 | part 1 of `drop --stream` (and every later part) guards that every cell before its head holds no card of the stream; a part refused so returns the verb to its continue state to read again, at most `PartRereads` (2) times (the design says bounded) | `MCSprintEventsGoalFreeze` | `MCSprintEventsC13`, `MCSprintEventsDrop2`; `MCSprintEventsFreezeReach` shows a part applied after the fresh read |
| `stablesince` | H12 | R6 deals only to a member up for two beat periods (`stable_since`); R6's 30 s entry is armed while work is dealable and restarted by every deal (by a take, with `unplaced`); when it fires with work dealable, members up, and none stable or a stable one with room, "work is ready and no member is stable" opens | `sprintevents-bench/MCSprintEventsGoalFlap` | none alone: `sprintevents-bench/MCSprintEventsGoalFlapStable` still fails `Progress` (H15) |
| `unplaced` | H15 | R6's 30 s entry restarts only when a dealt card is taken, not on a deal; a card dealt and withdrawn `MaxPlaceTries` (3) times without a take raises "this card cannot be placed", naming the card (its row prints `drop`); a take closes it | `MCSprintEventsGoalPlace` (`UnplacedNamed`), `sprintevents-bench/MCSprintEventsGoalFlapStable` (`Progress`) | `MCSprintEventsRepairPlace` (`UnplacedNamed`); `sprintevents-bench/MCSprintEventsRepairFlapStable` (`Progress`, Results) |
| `rankclose` | H13 | a step after which an open card of a sentinel's stream sorts before the sentinel closes its "sentinel reached": a rank or an insertion of a card before it, or a rank of the sentinel itself behind an open card | `MCSprintEventsGoalRank`, `MCSprintEventsGoalInsert`, `MCSprintEventsGoalRankSelf` | `MCSprintEventsRepairRank`, `MCSprintEventsInsert`, `MCSprintEventsRepairRankSelf` |

H9 is wording (the model's `Progress` is its decided form). H17 is a hole in the decided repair of H14 and has no repair here.

**Not a repair: the error step's generation.** The model's LIMIT branch of `Apply` and its `ParkOnBug` wrote the stepped judgment, the parked key and the agenda without the lease generation; the design's error step is a step of notes and sprint keys through X.pre that carries the generation like every tick step (1.3.5), so a stale loop's is refused STALEGEN. Both branches now write only under `GenOK`. `MCSprintEventsLeaseErr` (two loops, a lease that expires or is taken over, a LIMIT and a bug refusal) passes `LeaseSafe`; the same configuration on the second round's module fails it in 7 states (manual probes on the bench: a LIMIT's error step after the lease expired, and a bug's).

## Running it

TLC runs only on a bench. **The gated cases** are declared in `CASES.tsv` in fourteen groups named `sprintevents*`, recorded in `RUNS.tsv` by `tlacheck` (two workers for a case expected to pass, one for a counterexample, 110 s a group):

```sh
for g in sprintevents sprintevents-land sprintevents-drop sprintevents-faults sprintevents-fleet sprintevents-lease \
         sprintevents-loops sprintevents-controls-a sprintevents-controls-b sprintevents-witnesses-a \
         sprintevents-witnesses-b sprintevents-goals sprintevents-goals-b sprintevents-repairs; do
  go run ./tools/tlacheck run --root . --jar /path/to/tla2tools.jar --dir /tmp/tlc-$g --group $g
done
```

**The bench runs** are in `tla/sprintevents-bench/`, outside `CASES.tsv`, each with 8 workers (6 for the four smaller ones), 8 GB of heap, final liveness checking and a 900 s cap:

```sh
cd tla
for c in MCSprintEventsFullDesign MCSprintEventsFull2 MCSprintEventsFullOps2 MCSprintEventsFullLive \
         MCSprintEventsGoalFlap MCSprintEventsGoalFlapStable MCSprintEventsRepairFlapStable; do
  mkdir -p /tmp/tlc-$c
  timeout 900 java -XX:+UseParallelGC -Xmx8g -Djava.io.tmpdir=/tmp/tlc-$c -cp /path/to/tla2tools.jar \
    tlc2.TLC -workers 8 -deadlock -lncheck final -metadir /tmp/tlc-$c/states \
    -config sprintevents-bench/$c.cfg MCSprintEvents.tla > /tmp/tlc-$c/$c.log 2>&1
done
```

`-deadlock` turns TLC's deadlock check off, as for the other models here (the gated cases declare `ignore-terminal`); the safety and liveness properties are what these runs check.

A note on the model's form: TLC keeps no LET value and no operator argument while it evaluates ENABLED for fairness, so a value used more than once is bound once with `CHOOSE r \in {e(v) : v \in {heavy}} : TRUE` or `\E v \in {heavy}`, and each tick action tests its UNCHANGED part first. Without that, the liveness cases did not finish.

## Amendment 4: the deal's order

The deal takes one card from each stream's front in turn (`TurnSorted`, used by `PlanDeal` and by `HeadActionable`'s deal head), not the room lowest over the whole table: errata 3, amendment 4, the owner's ruling of 2026-09-30, and the engine's `dealTurns` (`internal/sprint/rules_fleet.go`). `DealTakesTurns` is the new invariant; `MCSprintEventsW28.cfg` breaks the order back to the whole table's and fails it in its first state, and `MCSprintEventsC28.cfg` is the same instance on the design with every safety property, `Progress` and `DealTakesTurns`. The model orders the streams by the `Ord` of each one's first card (the engine's is by name; the order of the streams is not what any property here depends on, and in the instances the two are the same).

**Run.** On the bench (TLC 2.19, `tla2tools.jar` sha256 `936a2620...`, Java 21.0.12.1, 32 cores, 2026-09-30 UTC), the fourteen groups are recorded again in `RUNS.tsv`: every existing case keeps its verdict (a pass passes, each recorded counterexample fails with the property its row names). `MCSprintEventsW28` fails `DealTakesTurns` in its initial state (1 state generated): the whole table's order takes p1 and p3, both of s1, and none of s2's p2, so the plan takes two cards from the first stream in turn. `MCSprintEventsC28` passes with every safety property, `Progress` and `DealTakesTurns` (100 generated, 75 distinct states).

## Results

All runs used TLC 2.19 (`tla2tools.jar` sha256 `936a2620...`) on Java 21.0.12.1, on a 32-core Linux bench shared with other work (load average 8 to 32 during the runs), on 2026-09-30 (UTC), on this module as committed.

**The gated groups**, recorded by `tlacheck` (a case expected to pass on two workers, a counterexample on one; the seconds are the records', summed over the group's cases):

| group | cases | seconds |
|---|---|---|
| `sprintevents` | 8 | 30.2 |
| `sprintevents-land` | 1 | 55.5 |
| `sprintevents-faults` | 1 | 53.0 |
| `sprintevents-fleet` | 1 | 43.5 |
| `sprintevents-lease` | 4 | 40.0 |
| `sprintevents-loops` | 2 | 53.9 |
| `sprintevents-drop` | 8 | 43.5 |
| `sprintevents-controls-a` | 11 | 18.8 |
| `sprintevents-controls-b` | 14 | 20.9 |
| `sprintevents-witnesses-a` | 12 | 12.6 |
| `sprintevents-witnesses-b` | 14 | 13.5 |
| `sprintevents-goals` | 9 | 9.0 |
| `sprintevents-goals-b` | 15 | 17.9 |
| `sprintevents-repairs` | 13 | 30.3 |

**Every gated case** (`RUNS.tsv` has the rest of each record):

| case | result | generated | distinct | seconds |
|---|---|---|---|---|
| `MCSprintEvents.cfg` | pass | 149163 | 82395 | 13.152 |
| `MCSprintEventsCursor.cfg` | pass | 5661 | 3296 | 2.149 |
| `MCSprintEventsProbe.cfg` | pass | 4953 | 2827 | 2.274 |
| `MCSprintEventsStop.cfg` | pass | 4557 | 2665 | 2.241 |
| `MCSprintEventsLimit.cfg` | pass | 276 | 217 | 1.598 |
| `MCSprintEventsReview.cfg` | pass | 24619 | 12857 | 5.535 |
| `MCSprintEventsMulti.cfg` | pass | 1461 | 978 | 2.290 |
| `MCSprintEventsMultiReach.cfg` | fails ProbeTwoMoves | 5 | 5 | 0.926 |
| `MCSprintEventsLand.cfg` | pass | 208266 | 88267 | 55.493 |
| `MCSprintEventsFaults.cfg` | pass | 565021 | 327286 | 53.037 |
| `MCSprintEventsFleet.cfg` | pass | 936787 | 363266 | 43.542 |
| `MCSprintEventsLease.cfg` | pass | 370752 | 161728 | 34.278 |
| `MCSprintEventsC6.cfg` | pass | 27242 | 12808 | 3.151 |
| `MCSprintEventsW6.cfg` | fails LeaseSafe | 70 | 49 | 0.952 |
| `MCSprintEventsLeaseErr.cfg` | pass | 1820 | 940 | 1.611 |
| `MCSprintEventsC1.cfg` | pass | 759368 | 343590 | 39.811 |
| `MCSprintEventsW1.cfg` | pass | 759368 | 343590 | 14.123 |
| `MCSprintEventsDrop.cfg` | pass | 297254 | 145737 | 21.370 |
| `MCSprintEventsDrop2.cfg` | pass | 120369 | 62575 | 11.627 |
| `MCSprintEventsDrop2Reach.cfg` | fails ProbeTwoDrops | 302 | 195 | 1.059 |
| `MCSprintEventsFreezeReach.cfg` | fails ProbeReread | 1762 | 1046 | 1.436 |
| `MCSprintEventsC13.cfg` | pass | 6342 | 3730 | 2.244 |
| `MCSprintEventsC14.cfg` | pass | 20531 | 11248 | 3.463 |
| `MCSprintEventsW13.cfg` | fails DropComplete | 1275 | 734 | 1.335 |
| `MCSprintEventsW14.cfg` | fails DropComplete | 27 | 16 | 0.923 |
| `MCSprintEventsC2.cfg` | pass | 500 | 370 | 1.414 |
| `MCSprintEventsC3.cfg` | pass | 232 | 161 | 1.198 |
| `MCSprintEventsC4.cfg` | pass | 2822 | 1756 | 1.858 |
| `MCSprintEventsC5.cfg` | pass | 22350 | 10963 | 3.328 |
| `MCSprintEventsC7.cfg` | pass | 51 | 38 | 1.063 |
| `MCSprintEventsC7b.cfg` | pass | 664 | 412 | 1.676 |
| `MCSprintEventsC8.cfg` | pass | 4208 | 2361 | 2.288 |
| `MCSprintEventsC9.cfg` | pass | 136 | 102 | 1.265 |
| `MCSprintEventsC10.cfg` | pass | 371 | 249 | 1.583 |
| `MCSprintEventsC11.cfg` | pass | 827 | 581 | 1.834 |
| `MCSprintEventsC12.cfg` | pass | 72 | 51 | 1.263 |
| `MCSprintEventsC15.cfg` | pass | 3208 | 1971 | 2.081 |
| `MCSprintEventsC16.cfg` | pass | 371 | 249 | 1.108 |
| `MCSprintEventsC17.cfg` | pass | 1664 | 1020 | 1.581 |
| `MCSprintEventsC18.cfg` | pass | 1694 | 992 | 1.564 |
| `MCSprintEventsC19.cfg` | pass | 80 | 67 | 1.040 |
| `MCSprintEventsC20.cfg` | pass | 336 | 229 | 1.246 |
| `MCSprintEventsC21.cfg` | pass | 2761 | 1365 | 1.702 |
| `MCSprintEventsC22.cfg` | pass | 2948 | 1944 | 2.099 |
| `MCSprintEventsC23.cfg` | pass | 100 | 75 | 1.523 |
| `MCSprintEventsC24.cfg` | pass | 55 | 47 | 1.033 |
| `MCSprintEventsC25.cfg` | pass | 439 | 294 | 1.491 |
| `MCSprintEventsC26.cfg` | pass | 169 | 130 | 1.003 |
| `MCSprintEventsC27.cfg` | pass | 2016 | 1383 | 1.897 |
| `MCSprintEventsC28.cfg` | pass | 100 | 75 | 1.563 |
| `MCSprintEventsW2.cfg` | fails NoLostWork | 9 | 9 | 0.936 |
| `MCSprintEventsW3.cfg` | fails DueAgrees | 22 | 17 | 0.937 |
| `MCSprintEventsW4.cfg` | fails PositionHolds | 141 | 99 | 0.984 |
| `MCSprintEventsW5.cfg` | fails PositionHolds | 19 | 16 | 0.917 |
| `MCSprintEventsW5Reach.cfg` | fails Answerable | 134 | 91 | 0.971 |
| `MCSprintEventsW7a.cfg` | fails Progress | 72 | 52 | 1.031 |
| `MCSprintEventsW7b.cfg` | fails RedealsCounted | 121 | 81 | 0.968 |
| `MCSprintEventsW8.cfg` | fails OpenExact | 422 | 251 | 1.072 |
| `MCSprintEventsW9.cfg` | fails HeadActionable | 11 | 10 | 0.944 |
| `MCSprintEventsW10.cfg` | fails Progress | 364 | 242 | 1.250 |
| `MCSprintEventsW11.cfg` | fails Progress | 812 | 572 | 1.553 |
| `MCSprintEventsW12.cfg` | fails HoldEnds | 38 | 28 | 1.011 |
| `MCSprintEventsW15.cfg` | fails ChunkProgress | 149 | 100 | 1.011 |
| `MCSprintEventsW16.cfg` | fails WaitHolds | 92 | 66 | 0.957 |
| `MCSprintEventsW17.cfg` | fails UniqueScores|ScoresBelowCounter | 9 | 8 | 0.933 |
| `MCSprintEventsW18.cfg` | fails PlaceOnlyUp | 138 | 94 | 0.988 |
| `MCSprintEventsW19.cfg` | fails HeldSticky | 11 | 10 | 0.947 |
| `MCSprintEventsW20.cfg` | fails StoppedJudgmentTrue | 227 | 158 | 0.970 |
| `MCSprintEventsW21.cfg` | fails Answerable | 44 | 28 | 0.947 |
| `MCSprintEventsW22.cfg` | fails NeedsHold | 72 | 48 | 0.954 |
| `MCSprintEventsW23.cfg` | fails Progress | 6 | 5 | 0.966 |
| `MCSprintEventsW24.cfg` | fails NoLostWork | 7 | 7 | 0.921 |
| `MCSprintEventsW25.cfg` | pass | 439 | 294 | 1.062 |
| `MCSprintEventsW26.cfg` | fails NeedsHold | 11 | 10 | 0.920 |
| `MCSprintEventsW27.cfg` | fails NoLostWork | 196 | 141 | 1.053 |
| `MCSprintEventsW28.cfg` | fails DealTakesTurns | 1 | 1 | 0.904 |
| `MCSprintEventsGoalNamed.cfg` | fails ProgressLiteral | 40 | 31 | 1.028 |
| `MCSprintEventsGoalStopped.cfg` | fails Progress | 5 | 4 | 0.931 |
| `MCSprintEventsGoalSpan.cfg` | fails Progress | 60 | 39 | 1.027 |
| `MCSprintEventsGoalStale.cfg` | fails StoppedJudgmentTrue | 94 | 62 | 0.968 |
| `MCSprintEventsGoalDrop.cfg` | fails Answerable | 264 | 157 | 1.001 |
| `MCSprintEventsGoalMissing.cfg` | fails IndexAgreesLiteral | 9 | 8 | 0.909 |
| `MCSprintEventsGoalCursor.cfg` | fails CursorSoundLiteral | 642 | 416 | 1.137 |
| `MCSprintEventsGoalDue.cfg` | fails DueAgreesLiteral | 57 | 41 | 0.984 |
| `MCSprintEventsGoalDownDeal.cfg` | fails NoLostWork | 46 | 39 | 0.988 |
| `MCSprintEventsGoalRank.cfg` | fails Answerable | 126 | 87 | 0.985 |
| `MCSprintEventsGoalTakeRoom.cfg` | fails NoLostWork | 224 | 145 | 1.042 |
| `MCSprintEventsGoalSeen.cfg` | fails NoLostWork | 233 | 131 | 1.035 |
| `MCSprintEventsGoalMade.cfg` | fails Answerable | 11 | 10 | 0.920 |
| `MCSprintEventsGoalCut.cfg` | fails Answerable | 1132 | 648 | 1.304 |
| `MCSprintEventsGoalFreeze.cfg` | fails DropComplete | 1424 | 832 | 1.384 |
| `MCSprintEventsGoalDropAck.cfg` | fails Answerable | 27 | 16 | 0.941 |
| `MCSprintEventsGoalInsert.cfg` | fails Answerable | 1101 | 684 | 1.239 |
| `MCSprintEventsGoalStaleClose.cfg` | fails StoppedJudgmentTrue | 94 | 62 | 0.967 |
| `MCSprintEventsGoalStaleRaise.cfg` | fails StoppedRaiseFresh | 95 | 62 | 0.962 |
| `MCSprintEventsGoalDropAdd.cfg` | fails Answerable | 43 | 26 | 0.941 |
| `MCSprintEventsGoalRankSelf.cfg` | fails Answerable | 129 | 89 | 0.979 |
| `MCSprintEventsGoalRearm.cfg` | fails ProgressScoped | 1117 | 624 | 3.038 |
| `MCSprintEventsGoalStaleFleet.cfg` | fails StoppedRaiseFresh | 176 | 111 | 0.995 |
| `MCSprintEventsGoalPlace.cfg` | fails UnplacedNamed | 1263 | 718 | 1.156 |
| `MCSprintEventsRepairDownDeal.cfg` | pass | 76 | 62 | 1.017 |
| `MCSprintEventsRepairDrop.cfg` | pass | 14109 | 7985 | 2.683 |
| `MCSprintEventsRepairRank.cfg` | pass | 1553 | 1057 | 1.844 |
| `MCSprintEventsRepairTakeRoom.cfg` | pass | 1049 | 725 | 1.718 |
| `MCSprintEventsRepairDropAck.cfg` | pass | 13018 | 7195 | 2.976 |
| `MCSprintEventsRepairStopped.cfg` | pass | 465 | 304 | 2.100 |
| `MCSprintEventsRepairStale.cfg` | pass | 129 | 85 | 1.148 |
| `MCSprintEventsInsert.cfg` | pass | 21169 | 12488 | 3.738 |
| `MCSprintEventsRepairDropAdd.cfg` | pass | 15684 | 8749 | 3.651 |
| `MCSprintEventsRepairRankSelf.cfg` | pass | 1508 | 1019 | 1.781 |
| `MCSprintEventsRepairRearm.cfg` | pass | 1031 | 565 | 2.782 |
| `MCSprintEventsRepairStaleRaise.cfg` | pass | 129 | 85 | 1.200 |
| `MCSprintEventsRepairPlace.cfg` | pass | 27379 | 13236 | 3.646 |


**The bench runs** (`tla/sprintevents-bench/`; the seconds are TLC's own):

| config | instance | result | generated | distinct | seconds |
|---|---|---|---|---|---|
| `MCSprintEventsFullDesign` | 3 primaries and the sentinel, 2 readers, cap 2, every verb but the drops, 3 verbs; the design as written (6 workers) | `Answerable` violated, 10 states: H13 | 44,019 | 26,109 | 5 |
| `MCSprintEventsFull2` | the same with every repair, 2 verbs (8 workers) | did not finish: no violation in 14,359,942 distinct states (33,736,189 generated, depth 34 at TLC's last report, 1,540,931 states on the queue) when the 900 s cap stopped it; a timeout is not a pass | - | 14,359,942 | 900 |
| `MCSprintEventsFullOps2` | drops in parts with two op names, resume, abort, a crash, an error, stop and start, rank, ack; every repair; `StoppedOnce` for `StoppedJudgmentTrue` (8 workers) | pass, every other safety property and every action property | 16,532,388 | 7,662,264 | 486 |
| `MCSprintEventsFullLive` | `Progress` with 2 readers, 2 steady members, workers and readers acting, every repair (6 workers) | pass | 11,121 | 5,391 | 4 |
| `MCSprintEventsGoalFlap` | two members that beat and stop; `seenfresh` (6 workers) | `Progress` violated: a 59-state lasso back to state 38 (H12) | 1,497,540 | 522,822 | 67 |
| `MCSprintEventsGoalFlapStable` | the same with `stablesince` (6 workers) | `Progress` violated: a 129-state lasso back to state 64 (H15) | 15,920,778 | 5,623,278 | 689 |
| `MCSprintEventsRepairFlapStable` | the same with `unplaced` as well, `MaxPlaceTries` 3 (8 workers) | pass: `Progress` holds | 15,800,739 | 5,591,976 | 715 |

"Every repair" now includes `stopinputs`, `stoprearm` and `unplaced`. GoalFlapStable's state space is larger than in the second round (2,030,014 distinct states then) because the count of untaken withdrawals is kept as a ghost under `stablesince`.

Manual probes, on the bench and not recorded in `RUNS.tsv`. This round, on this module before its last three comment edits: GoalStaleRaise on `Fixes = {}` fails `StoppedRaiseFresh` in 12 states (H14 on the design); GoalRearm on `Fixes = {}` passes (613 distinct states: H16 needs `stopclose`); RepairPlace without `seenfresh` fails `NoLostWork` in 16 states, which is H2 (`seen:m1` and `down:m1` in one tick); RepairFlapStable with `MaxPlaceTries` 1 passes `Progress` (2,159,656 distinct states, 6 workers, 267 s). This round, on the second round's module: GoalDropAdd with its `dropcond` and `cutmark` fails `Answerable` in 4 states; GoalRankSelf with its `rankclose` fails `Answerable` in 7 states; LeaseErr fails `LeaseSafe` in 7 states, and so do its two halves (a LIMIT's error step after the lease expired, with no bug; a bug's, at a step bound of 1). The second round's, on its module: Fleet without `judgeguard` and without `seenfresh`, Faults without `judgeguard` and without `seenfresh`, Land without any, Multi without `judgeguard`, Drop2 without `downdeal`, without `freezefirst` and without `judgeguard`, and W6 checked against `OnePlace` (Findings, H1, H2, H10, H11). Two larger runs of the first round were not repeated (the two-loop instance with three lease generations, which reached 16,670,850 distinct states at the 900 s cap with no violation; and Full2 with 3 verbs, stopped by hand at 12,869,947); a timeout is not a pass.

## Findings

Every trace below was read state by state and checked against the design's text. H1 to H13 are holes in the design as written: it breaks a property section 5 states, or a claim the design makes. H14, H15 and H16 are holes in repairs errata 3 decided (the repaired model still breaks the property the repair was decided for); amendment 2 decides each, and the model has those repairs. H17 is a hole in amendment 2's decision for H14, found this round. Each has a configuration that fails; the ones in the design as written run `Fixes = {}`, or only the repairs of other holes on their path (Repairs). W-rows are witnesses whose failure the table of section 5 claims but the design does not give. The last group is wording: a property as written that the design, as it is meant, does not keep.

### H1. R11 judges a late work card and never looks again, though a replacement becomes possible

`MCSprintEventsGoalTakeRoom` (8 states).

1. p1 is dealt to m1 and p3 to m2, neither taken; each member's one ready cell is full (the cap is 1 here). The worker at m2 reads `take p3`.
2. The untaken deadline passes for both, and the pop queues `late:untaken:p1` and `late:untaken:p3`.
3. R11 plans p1 on the present state: no other member up has room (m2's cell holds p3), so it opens "a work card is past its deadline: not taken" on p1's work card, and its key goes.
4. `take p3` applies, and m2's ready cell is free. Its line queues `deal` only (2.1: "cards leave a member's ready or working cell").
5. R11's plan on the present state now replaces p1 to m2, and `late:untaken:p1` is not queued, on no line, and its entry was popped: `NoLostWork` fails.

The card is named, so nothing is silent, but the mechanical move is not made. Every gated configuration that runs `judgeguard` meets the same hole by another path, each found by running it without the repair (manual probes on the bench, not in `RUNS.tsv`): `MCSprintEventsFleet` (12,147 distinct states: m1 is held, R2 moves p1 to m2, R11 judges p1 with no other member up, and `fleet up m1` sets m1 up, whose line queues `deal` and `level`); `MCSprintEventsFaults` (42,226: p1 withdrawn from m2 frees the cell R11 found full when it judged p3); `MCSprintEventsMulti` (273: a take frees the cell); `MCSprintEventsDrop2` (10,783: `drop --stream s2` removes p2 from m2's cell). Repair (`judgeguard`, errata 3): `MCSprintEventsRepairTakeRoom` passes. A guard at apply on the judge branch alone is not a repair: with a member that beats and stops in step with the tick, it refuses both the replacement and the judgment forever (the model found that lasso when the guard was tried in the first round).

### H2. A member is left up with no beat entry, and nothing ever sets it down

`MCSprintEventsGoalSeen` (15 states).

1. m1 is up with p1 dealt to it and not taken; m2 is down. m1's beat lapses, and R2 sets m1 down and withdraws p1.
2. m1 beats once: `seen:m1` is entered at R, `beat:m1` at R + 15 s.
3. A unit passes before the loop pops `seen:m1` (in a real run, a loop away for 15 s, for instance restarting or waiting for the lease): `beat:m1` is due too.
4. One pop takes both: the agenda holds `seen:m1` and `down:m1`. RT2 plans both on one read: R1 will set m1 up; R2 sees m1 down with no card, and its plan is empty.
5. R1's step applies (m1 up). R2's empty plan removes `down:m1`.
6. m1 is up, its `beat:m1` entry is gone, and no line or entry will queue `down:m1` again. R2's plan on the present state sets m1 down; `NoLostWork` fails. From then on, deals go to a member that is not beating, until each card's untaken deadline moves it.

R1's guard is "the control card at its place with its revision, status down" (2.3); it does not look at the beat, and 2.3 does not say whether R2's key-only removal carries R2's guard. `MCSprintEventsLand` and `MCSprintEventsFleet` without `seenfresh` fail the same way (manual probes: 5,329 and 5,764 distinct states). Repair (`seenfresh`, both clauses of errata 3's decision): R1 sets a member up only while `beat:m` lies above R (so R1 plans nothing at step 4), and R2's empty plan removes `down:m` only under its guard on the control card as read (so its removal at step 5 is refused, and the next plan sets m1 down); each clause alone refuses one of the two steps the trace needs. `MCSprintEventsC21`, `MCSprintEventsLand` and `MCSprintEventsFleet` pass with it; `MCSprintEventsFaults` does not need it (it passes without it: 327,286 distinct states, a manual probe), and runs without it now.

### H3. "Blocked on something missing" outlives the creation of the need, and its `add` decision is then refused

`MCSprintEventsGoalMade` (3 states).

1. p2 waits on p1, which has no record: "a primary is blocked on something missing: p1" is open on p2 (decisions `add p1`, `ack`, `drop p2`).
2. `add p1` applies: p1 is created.
3. The judgment stays open until R4's `made:p1` runs, a tick later at the soonest. In that window its decision `add p1` is refused (the id exists; L1's EXISTS), and `Answerable` fails. (`ack` is printed only "while n has no record", its stated condition, so it is not printed.)

Row 30 of section 0 says "Creating n closes the judgment"; in 2.3 only R4's `made` closes it. Repair (`madeclose`, errata 3 as amended): the add closes the judgment in its own step, R4's `made` then only serves the waiters, and the row prints `add n` only while n has no record. `MCSprintEventsC22` passes with it.

### H4. The cut clock's judgment can open for an op that has already ended

`MCSprintEventsGoalCut` (16 states; with `freezefirst` and `dropcond`, so that H11 and H8 are out of the way).

1. `drop --stream s1` applies its first part: p1 is removed, s1 is marked, the op's `cut` entry is set.
2. A unit passes and the entry falls due while the op runs; the pop queues `late:cut:<op>` and removes the entry.
3. A tick plans R11 on `late:cut:<op>`: the op's mark stands and its entry is absent.
4. The op's last part applies: p3 is removed, the entry stays absent, the mark is cleared.
5. R11's request applies. Its guard is "XGUARD on the entry's score as read" (2.3, R11): absent as read, absent now. It opens "a verb in parts stopped before its end" for an op that has ended; its decision `drop --abort --op` is refused. `Answerable` fails.

Repair (`cutmark`): the judgment also guards that the op's marks stand. `MCSprintEventsC13`, `MCSprintEventsC14`, `MCSprintEventsDrop` and `MCSprintEventsDrop2` pass with it. Errata 3 names the repair for a drop; an abort ends an op the same way and writes its receipt, which the model's guard covers (the mark is gone either way).

### H5. R17 raises its judgment once per STOPPED span, so a wait on it ends the naming for good

`MCSprintEventsGoalSpan` (19 states, a lasso from state 17).

1. The machine is STOPPED; p1 waits and is free to go, so a dry plan releases it: moves are due.
2. R17 sets `due_since`; ten minutes later it raises "the machine is STOPPED and moves are due" and sets `stopraised` = `stopped_since`.
3. The coordinator runs `wait --for d`: the judgment is closed and held until `stophold` (1.3.4).
4. The hold passes. Every later look finds moves due, `due_since` passed and `stophold` passed, but `stopraised` equals `stopped_since`, so R17 never raises again. p1 is open forever and nothing names it: `Progress` fails (and `ProgressScoped`, since moves are due).

Repair (`spanreset`): the wait clears `stopraised`, so R17 raises again once the hold has passed. `MCSprintEventsC12` passes `Progress`, `ProgressScoped` and `HoldEnds` with it.

### H6. A machine left STOPPED with no move due names nothing, however long it stays

`MCSprintEventsGoalStopped` (4 states, a lasso from state 2).

The machine is STOPPED; p1 is dealt to m1 and not taken. R is still, so the untaken deadline never falls due; no dry plan changes a card, so R17 never raises. p1 is open forever and nothing names it: `Progress` fails. Errata 3 decides this is a hole in the claim, not in the machine: `Progress` is claimed while RUNNING or while a move is due (`ProgressScoped`). `MCSprintEventsRepairStopped` (the same state, the machine free to start, stop and wait, with `spanreset` and `stopclose`) passes `ProgressScoped` and every safety property but `StoppedJudgmentTrue`, for which it checks `StoppedOnce`.

### H7. R17 never closes its judgment when no move is due any more, and closes it only at a look

`MCSprintEventsGoalStale` (12 states), and with the repair `MCSprintEventsGoalStaleClose` (12 states).

STOPPED, moves due, R17 raises its judgment; then `drop p1` removes the only card that could move. On the design, the judgment stays open with nothing to do, and only `start` closes it: `StoppedJudgmentTrue` fails. With `stopclose` (and R17 planned and applied apart, as this model always has it), the next look closes it, but the invariant as section 5 states it still fails in the states between the drop and that look: a verb can empty the dry plans at any time, and only a look sees it. What holds with the repair (`MCSprintEventsRepairStale`): the judgment is raised at most once a span (`StoppedOnce`), and a stale one is closed by a later look (`StoppedStaleCloses`). Section 5's `StoppedJudgmentTrue` has to be stated as those two.

### H8. Decisions that DROPPING refuses are printed

`MCSprintEventsGoalDrop` (9 states) and `MCSprintEventsGoalDropAck` (4 states).

- GoalDrop: p1 is in review with "cannot ask" open (decisions include `rework p1` and `drop p1`). `drop --stream s1` applies its first part and marks s1. Both decisions are now refused DROPPING ("every verb that changes a card of a stream being dropped or removed is refused DROPPING, except that op's own parts", section 3), and `Answerable` fails.
- GoalDropAck: s1 has p1 waiting and free to go, and p3 waiting on p2, which has no record ("blocked on something missing: p2" on p3, decisions `add p2`, `ack`, `drop p3`). `drop --stream s1` applies its first part: p1 is removed and s1 is marked, p3 left for the next part. Now `drop p3` is refused DROPPING, and so is `ack`: an ack of a blocked, missing or refused judgment waives a need or clears `refused`, so it changes the card, and the model's `ack` guard refuses it DROPPING, as section 3 says (the first round's model lacked this guard). `add p2` (stream s2) is accepted. `Answerable` fails on both.

- GoalDropAdd (amendment 2; the second round's check found it by reading): s2 has p2 waiting on p1 (s1), which has no record ("blocked on something missing: p1" on p2, decisions `add p1`, `ack`, `drop p2`); s1 has p3 waiting at 2 and g1 behind it at 4. `drop --stream s1` reads (the verb, then its part 1's head, p3) and applies part 1: p3 is removed, s1 is marked, g1 is left for the next part. `drop p2` and `ack` are accepted (p2 is in s2), but `add p1` is refused DROPPING: p1's stream is being dropped (the model's add guard, `~Frozen`, as 1.5.4 says of every other step touching s). `Answerable` fails in 4 states, on `Fixes = {}` and, a manual probe on the second round's module, with its `dropcond` (and `cutmark`), which did not leave out `add`.

Repair (`dropcond`, errata 3 as amended, with amendment 2's `add n`): no decision is printed that DROPPING refuses: `drop`, `rework`, `release`, `land`, `ack` of a dropped, missing or refused judgment, on a card of a stream being dropped, and `add n` while n's stream is being dropped. `MCSprintEventsRepairDrop`, `MCSprintEventsRepairDropAck` and `MCSprintEventsRepairDropAdd` pass with it (and `cutmark`, since H4 is on the same path). In GoalDropAck's last state the four verbs of the first round's condition would still print `ack`, which the guard refuses; the widened condition leaves it out.

### H9. Section 5's `Progress` names no card that waits on a named card

`MCSprintEventsGoalNamed` (14 states, a lasso from state 12).

p2 waits on p1. p1 is dealt to m1, the only member up, which beats steadily; p1 is not taken, R11 finds no other member and opens "a work card is past its deadline: not taken" on p1's work card. p1 is named; p2 is not, and never will be until p1 moves. `ProgressLiteral` (section 5's `Named`) fails. The same holds for a card behind an unlanded sentinel, whose "sentinel reached" names only the sentinel, and for a ready card behind members whose ready cells are full. R16's table has these as (d), "what it waits on, itself held"; errata 3 decides `Progress` names a card through what it waits on, as the model's `Progress` does, and it holds on every liveness configuration. The model's `Named` also counts a parked key only for the cards its rule could move (section 5's `k.rule \in RulesThatMove(c)`): deal for ready cards, `down:m` for the cards at m, `resolve:s` for the waiting cards of s, and so on.

### H10. A member going down queues no deal, so a deal plan emptied on a stale read removes its key

`MCSprintEventsGoalDownDeal` (12 states).

1. p1 is dealt to m1, the only member up; p3 becomes ready (fresh) and waits for room.
2. m1 stops beating. One tick plans `down:m1` (set m1 down, withdraw p1: two requests at a step bound of one) and `deal` (m1's cell is full: nothing to do).
3. RT3 in the round robin's order: `down:m1`'s first request sets m1 down; `deal`'s empty request removes `deal`. No member is up and p3 is dealable, so R6's plan now opens "no fleet member is up", and `deal` is owed nowhere: `NoLostWork` fails.
4. `down:m1`'s second request withdraws p1; its line (a card enters ready) queues `deal` again.

It is transient here, because R2's own later request queues `deal`; it is still a failure of `NoLostWork` as stated, and it rests on the chance that the down member had a card. Repair (`downdeal`, errata 3 with its second clause withdrawn): the down or held line of a member also queues `deal`. `MCSprintEventsRepairDownDeal` and `MCSprintEventsFaults` pass with it; `MCSprintEventsDrop2` without it meets the same hole (a manual probe).

### H11. Part 1 of a drop reads its head before the freeze exists, so a card that moves back to a passed cell is left

`MCSprintEventsGoalFreeze` (18 states; with `cutmark` and `dropcond`).

1. s1 has p1 and p3 working, taken, at m1 and m2. `drop --stream s1` plans part 1: waiting and ready are empty, the head is p1 in the working cell.
2. Before part 1 applies (and so before any mark exists), both members' beats lapse; R2 sets m2 and then m1 down, and withdraws p3 (no member up): p3 goes to ready, a cell part 1's read passed. (The two `down` keys of one rule go in the order `CHOOSE` gives them here, m2 first; the design leaves that order open, and the other order withdraws p1 first, which refuses part 1. The first round's reader could not confirm the trace by hand for that reason.)
3. Part 1 applies: p1 at its place, its guard passes; it removes p1 and finds the working cell and every later cell empty. It is the last part: it records the end with p3 still open in ready, and the mark never stands. `DropComplete` fails.

1.5.4 says each part "drains the head, so nothing is skipped or taken twice"; that holds once the mark is set, and the mark is written by part 1 itself. Repair (`freezefirst`, errata 3 as amended, and amendment 2): part 1 (and every part) guards that every cell before its head holds no card of the stream; a part refused so returns the verb to its continue state, and it reads again, at most `PartRereads` (2) times; any other refusal ends the verb, as before. `MCSprintEventsC13` and `MCSprintEventsDrop2` pass with it. `MCSprintEventsFreezeReach` (C13's instance, a probe) shows the fresh read: part 1 is planned with p1 in working as its head; R2 sets both members down and withdraws p3 to ready, a cell the read passed; part 1 is refused and the verb reads again; the new head is p3 in ready, and the part applies and marks s1 (20 states). In the second round's model a refused part ended the verb, which then needed a new `drop --stream`.

### H12. A fleet that beats and stops in step with the tick starves every step that places a card, and nothing names the cards

`sprintevents-bench/MCSprintEventsGoalFlap` (with `seenfresh`; a 59-state lasso back to state 38, on the bench: 522,822 distinct states).

Two members, each beating and stopping (the design's environment: "a member may beat, stop and beat again forever"). In every cycle deal plans p1 to the member that is up at its read; before its request applies, R2 (earlier in the round robin) sets that member down, and memberup refuses the deal. At the next read the other member is up, and so on. At every plan some member is up, so "no fleet member is up" is never raised, and p1 stays ready forever unnamed: `Progress` fails. Each step is refused correctly; the design has no judgment for a card that the machine keeps failing to place. The schedule is adversarial (the beats must fall in step with the tick), but beats are unfair in section 5 and the claim is stated for them. The gated liveness configurations use `Steady` members to check `Progress` apart from this. Errata 3 decides `stablesince`; the model has it, and H15 is what is left (decided in amendment 2: `unplaced`).

### H13. "Sentinel reached" outlives a card ranked or inserted before its sentinel, and prints a `release` that is refused

`MCSprintEventsGoalRank` (7 states) and `MCSprintEventsGoalInsert` (10 states); found first by `sprintevents-bench/MCSprintEventsFullDesign` (10 states).

1. g1 is the first sentinel of s1 with nothing before it (GoalInsert: g1 is added by insertion at 5, and p3, fresh at 6, is behind it). R3 reaches g1 and opens "sentinel reached: g1" (decisions `release g1`, `add --before g1`, `drop g1`).
2. The coordinator runs `rank p3 --score 3` (GoalInsert: `add p1` at 3, the insertion the judgment itself suggests): a card now sorts before g1.
3. Until R3's unreach closes the judgment (the line queues `resolve:s1`, so a tick later), the judgment prints `release g1`, which the verb refuses: "release G guards, at apply time, that no open card of s is before G" (2.4). `Answerable` fails.

The same with the sentinel ranked, not the card (`MCSprintEventsGoalRankSelf`, 7 states; amendment 2, the second round's check found it by reading): g1 at 4, p3 fresh at 6 behind it; R3 reaches g1; `rank g1 --score 7` (odd, below the counter 8) applies, and p3 now sorts before g1. "Sentinel reached: g1" stays open, and its `release g1` is refused (NBefore is 1). It fails on `Fixes = {}` and, a manual probe on the second round's module, with its `rankclose`, whose close skipped the sentinel's own rank.

Repair (`rankclose`, with amendment 2's widening): a step after which an open card of a sentinel's stream sorts before the sentinel closes its "sentinel reached" in that step: a rank or an insertion of a card before it, or a rank of the sentinel itself behind an open card (no other step moves a card's score or brings an open card into being). `MCSprintEventsRepairRank`, `MCSprintEventsInsert` (a sentinel added by insertion, then a card inserted before it) and `MCSprintEventsRepairRankSelf` pass with it.

### H14. R17's step raises on a dry plan that a verb emptied between its read and its step

`MCSprintEventsGoalStaleRaise` (12 states; with `stopclose`, and the same on `Fixes = {}`, a manual probe). A hole in errata 3's decision for H7, found in the second round.

1. The machine is STOPPED; p1 waits and is free to go, so a dry resolve releases it: moves are due. The coordinator reads `drop p1`.
2. A look sets `due_since`; a unit (ten minutes) passes.
3. The next look plans R17's step on its read: moves due, `due_since` passed, no hold, not raised: raise.
4. `drop p1` applies: no dry plan changes a card any more.
5. R17's step applies. Its guard is "XGUARD on the clock fields as read" (2.3, R17), and the drop changed no clock field: it raises "the machine is STOPPED and moves are due" with no move due. `StoppedRaiseFresh` fails.

Amendment 2 decides that R17's apply also guards on the version of its dry plans' inputs as read (the streams' heads and the sentinels' scores it looked at, through a stream-set counter or the revisions of the cards it read). Repair (`stopinputs`): the model takes the revision of every card as read, which is a counter per stream over every stream, and covers both. The drop at step 4 changes p1's revision, so R17's step is refused and the next look plans afresh. `MCSprintEventsRepairStaleRaise` passes `StoppedRaiseFresh`, `StoppedStaleCloses` and every safety property with `StoppedOnce`. The version does not cover the fleet: H17.

### H15. With `stablesince`, a card whose deals succeed and whose members keep dropping is never named

`sprintevents-bench/MCSprintEventsGoalFlapStable` (with `seenfresh` and `stablesince`; on this round's module a 129-state lasso back to state 64, 5,623,278 distinct states; the second round's module gave a 70-state lasso of the same kind). A hole in errata 3's decision for H12, found in the second round. The loop of this round's trace:

1. p1 is dealt to m1, which is stable; the 30 s entry restarts. p1's untaken deadline is already past (a withdrawal and the redeal of a withdrawn card keep it), so its late key is due at once.
2. A unit passes before the loop pops again: both members' beats lapse, and the 30 s entry falls due while p1 is dealt; deal finds nothing dealable and clears it.
3. One tick plans `down:m1`, `down:m2` and R11 on p1. R11 reads m2 up, so its plan is p1's one replacement, to m2. R2's steps apply first (the round robin): m1 and m2 are set down, and memberup refuses the replacement; its key stays.
4. The next tick reads both members down, so R2 plans to withdraw p1. m1 beats, and R1 sets it up before the withdrawal applies; the withdrawal applies (p1's revision is as read). R11's plan on a withdrawn card is empty.
5. A unit later m1 is stable, and deal redeals p1 to m1: the state of step 1.

The untaken clock is past due all along, but R11's one replacement is refused in every cycle and it never reaches its judgment; the 30 s judgment never opens, because a deal restarts the entry in every cycle and it falls due only while nothing is dealable. `Progress` fails. The schedule needs the loop to fall a whole unit behind at every redeal (the tick is only weakly fair), and the model's time grain (two beat periods of `stable_since` and 15 s of beat freshness are both one unit).

Amendment 2 decides: the entry restarts only when a dealt card is taken, not on a deal; and a card dealt and withdrawn N times (a named constant, 3) without a take raises "this card cannot be placed", naming the card and the members. Repair (`unplaced`): the take restarts the entry and ends the count; an untaken withdrawal (R2's, of a card in a ready cell) raises the count, and at `MaxPlaceTries` opens the judgment in its own step; the judgment prints `drop` (its row is not decided) and closes at a take, a rework or a drop. Its safety claim, `UnplacedNamed` (a card at the count is named until it is taken), fails without it and holds with it: `MCSprintEventsGoalPlace` (18 states, the count a ghost: m2 stops beating and is set down, p1 is dealt to m1, m1's beat lapses, and R2 sets m1 down and withdraws p1 untaken; no judgment names p1 in that state) and `MCSprintEventsRepairPlace`, both with a count of 1 and one member beating, so that the whole state space fits the gated budget. The goal shows only that the design has no such judgment: in its last state "no fleet member is up" opens a tick later. H15's own trace, and `Progress` with the repair, are the bench's. `Progress` in the flapping fleet with the repair is `sprintevents-bench/MCSprintEventsRepairFlapStable`, with the count at 3 (Results).

### H16. With `stopclose`, a move that becomes due again after a close is never named

`MCSprintEventsGoalRearm` (26 states, a lasso back to state 24; with `stopclose`). A hole in errata 3's decision for H7, found by the second round's check by reading.

1. The machine is STOPPED; p1 waits and is free to go: moves are due. A look sets `due_since`; a unit passes; a look raises "the machine is STOPPED and moves are due", and `raised` is set.
2. `drop p1` applies. The next look finds no move due and closes the judgment (`stopclose`); `raised` stays set.
3. The coordinator runs `add p3`: no needs and no sentinel, so p3 goes to ready (1.4.5 names this use: "verbs applied while STOPPED, the owner sets up work ready to go"). The next look finds a dry deal due and sets `due_since`; a unit passes.
4. From then on every look finds moves due, `due_since` passed and no hold, but `raised` is set, so R17 never raises. p3 is open, nothing names it, and moves are due: `ProgressScoped` fails.

On `Fixes = {}` nothing closes the judgment, so the stale one stays open and names p3 (GoalRearm passes there, a manual probe); the repair of H7 opened this. Amendment 2 decides: a close clears `raised`; R17 raises once per span and close. Repair (`stoprearm`): the close's step clears `raised` (and the ghost count of raises in the span starts again, so `StoppedOnce` states once per span and close). `MCSprintEventsRepairRearm` passes `ProgressScoped`, `StoppedStaleCloses` and every safety property with `StoppedOnce`.

### H17. With `stopinputs`, R17's step still raises on a dry deal that a fleet change emptied

`MCSprintEventsGoalStaleFleet` (12 states; with `stopclose` and `stopinputs`). New in this round: a hole in amendment 2's decision for H14.

1. The machine is STOPPED; p1 is ready and never dealt, m1 is up and m2 down, so a dry deal places p1 on m1: moves are due. The coordinator reads `fleet down m1`.
2. A look sets `due_since`; a unit passes; the next look plans R17's step: raise.
3. `fleet down m1` applies: m1 is held and no member is up. A dry deal now places nothing (its plan is "no fleet member is up", which changes no card), and `down:m1` has nothing to move: no move is due.
4. R17's step applies: the clock fields are as read, and so is every card (the fleet down changed only m1's control card). It raises "the machine is STOPPED and moves are due" with no move due. `StoppedRaiseFresh` fails.

The decided version covers "the streams' heads and the sentinels' scores"; deal's dry plan also reads the fleet (which members are up, and with `stablesince` which are stable), and R1's and R2's read the members' control cards. With `stopclose` the next look closes the stale judgment, so it lives at most one look, as H14 did. A repair would put the fleet table's revision (or the control cards read) into the version. Not decided.

### Witnesses the table claims that pass: defence in depth

- **W1** (ingest without comparing the cursor, `MCSprintEventsW1`): passes. Two loops can both ingest only with the current lease generation each, and every takeover raises the generation (1.1, E4). The cursor comparison is the second guard: by this argument W1 can fail only together with W6, which one `Broken` value cannot express; the two are defence in depth.
- **W25** (R14's phase 1 without its guard on the entry's score, `MCSprintEventsW25`, with probes): `ReplayNoop` holds. A second run plans on the entry the first moved and plans nothing; the guard matters only against a second writer, which the lease already refuses.
- **W6** now fails `LeaseSafe`, the rule it breaks. `OnePlace`, which section 5's row names, holds under W6 (a manual probe on the final module, 17,636 distinct states): every rule step carries place, revision and absence guards, and the ingest the cursor comparison.

### Wording

- **E3 and `CursorSound`** (`MCSprintEventsGoalCursor`, 19 states): "every key of every line at or before the cursor is in the agenda, the held queue or parked, or its rule is quiet" fails when a later change, whose line is not yet ingested, makes an old key owed again (`deal`, after a withdrawal frees a card). The model's `CursorSound` counts a line after the cursor or an entry as owing it, as `NoLostWork` does.
- **I1 for `missing`** (`MCSprintEventsGoalMissing`, 3 states): "missing: n has no record and wait:n is not empty" fails between `add n` and R4's `made:n` (without `madeclose`). The model's `IndexAgrees` holds the direction the rules rely on.
- **D1** (`MCSprintEventsGoalDue`, 13 states): "its entry has fired and its late key is queued" fails between R13's unheld line and its ingest: the hold is gone, the late key is on the line, not in the agenda. The model's `DueAgrees` counts the line.
- **`ReplayNoop`'s definition**: "a second plan and apply ... right after a run that removed k, with no outside action between" also has to exclude every change since the first run's plan. A deal plan made before resolve released a card in the same tick is empty, removes `deal`, and a second run deals the card. Nothing is lost, since resolve's line queues `deal`. The model's probe runs only while the facts are as the first plan read them.
- **`Named`'s subjects**: section 5's `Named(c)` reads `jopen[c]`, but a lateness judgment's subject is the work card or the read card (2.2). The model reads a primary's work and read cards as its subjects too.
- **`NoLostWork` and R1**: R1's plan is not empty whenever a member is down, and only a beat will queue `seen:m`. The model leaves R1's key out of `NoLostWork`; section 5's "(presence) a beat ... that queues it" says as much.

Errata 3 adopts the model's definitions for these; section 5 of the design is to be corrected when version 2.2 is cut.
