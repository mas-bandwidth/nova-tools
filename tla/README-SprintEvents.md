# SprintEvents: the upper layers of nova-sprint as a TLA+ model

`SprintEvents.tla` is the state machine of layers 3 to 8 of nova-sprint, written from section 5 of `design/EVENT-DRIVEN-TICK-v2.1.md` ("the design" below, cited by section), with sections 1.1 to 1.6 and 2 as the text it models. It is item IT24 of the design. `MCSprintEvents.tla` holds the small instances (the scenarios), and each `MCSprintEvents*.cfg` is one configuration. The larger runs are in `tla/sprintevents-bench/`.

The model stands on layers 1 and 2 as their own models prove them (`tla/SetTable.tla` and `tla/SetTableLog.tla`, on their own branches): one call at a time, a step all or nothing, guards checked at apply, one place per card and table, a revision that moves with every change of a record, a receipt that makes a part identity apply once, one line per change in the step's own commit, and a function that errors keeping the writes it made before the error. So a step here is one atomic action, a revision guard is "the record is as read", and a refusal writes nothing.

## What is modelled

**The state** (section 5's variables, as far as the model keeps them).

| variable | what it holds |
|---|---|
| `col`, `score`, `fld` | the work table: each card's column, score and the fields the rules read (open, refused, bound, attempt, redeals, rereads, avoid, result, waived) |
| `wk` | a primary's live work card: its places (a set, so that a card dealt twice is a state `OnePlace` sees), generation, taken, `untaken_replaced`, the member it was dealt to, and the timers of the two work-card deadlines, each as the card's field and as its due entry |
| `rd` | the read cards of a primary's attempt, by reader, with the timer of their one deadline (unbegun and unreported as one) |
| `mi` | a stream's merge-idle timer and entry |
| `status`, `beat`, `seen` | a member's control card (up, down, held), its `beat:m` entry, its `seen:m` entry |
| `waitn`, `missing` | the indexes written by intents (`wait:n`, `missing`) |
| `J` | `jopen`: one record per (type, subject, cause), open or held, with the hold's entry |
| `agenda`, `parked` | the rule keys queued, the parked keys |
| `log`, `cur` | the lines not yet dropped, each with the keys ingest gives it and how often it was ingested; the cursor |
| `running`, `clk` | RUNNING or STOPPED; R17's fields (`due_since`, `stophold`, raised in this span), in wall time |
| `remind`, `pushes` | one person's remind entry; the pushes made (mod 2) |
| `dropping`, `cut`, `receipts`, `next` | the dropping marks, the cut entries (wall time), the part receipts with their continuations, the score counter |
| `lease`, `tk`, `vk` | the lease; the tick processes (their round trip, the page read, the plans, the pending requests, the halved keys); the verb processes |

The indexes that are functions of the fields (`sent`, `elig`, `fresh`, `again`) are operators, not variables: X derives them in the step that changes a card, from its real before-state (1.3.2), so they cannot disagree with a move that applied. A witness that breaks a definition (W9, W26) changes the operator.

**Time** is relative, in units of one deadline. Every timer holds what remains until it is due: 1, 0 (due), or -1 (unset). `Wall` advances time by one unit: running-time timers and entries move only while RUNNING, and the cut entries and R17's fields always. The state stays finite however long a behaviour runs, and a deadline passing is an event under fairness.

**Actions.** Plan and apply are separate for every rule and every verb.

| action | what it is |
|---|---|
| `RT1(t)` | the tick's first step: the lease part (take a free lease at gen + 1, or renew one's own), then the pop part: every due entry's key into the agenda first, then the entries removed (A1, D3) |
| `ReadEvents(t)` | RT1's page: the lines after the cursor, read with the cursor |
| `RT2(t)` | the ingest part, refused unless the gen is current and the cursor is where the page starts (INGESTAT); keys first, then the cursor. Then, RUNNING, every key of the agenda is planned on the present state and cut into requests by the builder (1.3.6), in the round robin's order (1.4.2). STOPPED, the look (1.4.5): the rules plan dry, R17 keeps its clock and raises its judgment, and the cut clock's judgments are the only rule steps |
| `Apply(t)` | one request, atomic alone. LIMIT first (the size), then STALEGEN, STOPPED and the guards (a race writes nothing; the key stays). A plan that fits one request removes its key unless it requeues; a cut plan removes none (1.3.6) |
| `ErrorRT1(t)`, `ErrorRT2(t)` | an error between the two writes of the pop or of the ingest: the first write kept, the second lost |
| `ParkOnBug(t)` | a request refused as a bug other than LIMIT: named, and its key parked |
| `TickCrash(t)` | a loop dies; what it held in memory is gone |
| `LeaseExpire` | the lease expires with no renewal (bounded by `MaxLease`) |
| `RunTwice(t)` | the probe of E7: a second plan and apply of a key right after a run that removed it or changed nothing, while the facts are as that run's plan read them |
| `VerbPlan(v)`, `VerbApply(v)` | a verb's read (its arguments and a snapshot of what its guards compare) and its step: add, take, finish, read (begin, ok, broken), merge (land), release, drop, rank, rework, ack (with waive), wait, wait on the STOPPED judgment, fleet down, fleet up, start, stop |
| `PartPlan(v)`, `PartApply(v)`, `AbortApply(v)` | `drop --stream` in parts: part 1 marks the stream, each part removes the head of the current cell and moves the cut clock, the last part clears the mark and writes the request line; a resume reads the receipts; `--abort` |
| `VerbCrash(v)` | a verb's process dies between its read and its step, or between parts |
| `Beat(m)` | a member beats: `beat:m` to R + 15 s, and `seen:m` at R for a member that is down. Unbounded and unfair |
| `Wall` | one unit of time |

**The rules** modelled are R1 (seen), R2 (down), R3 (resolve), R4 (needs, made), R6 (deal), R8 (ask), R9 (accept), R10 (rework), R11 (late: untaken, unfinished, unread, merge-idle, the cut clock), R13 (hold), R14 (remind, phase 1), R17 (stopped) and R19 (pull back), each with its plan, its units' guards and its effects as 2.3 states them, and J's one per cause decided at apply.

## What is not modelled

- R5 (cross), R7 (level), R12 (overdue), R15 (done), R16 (held) and R18 (behind). R16's table is the invariant `NothingSilent` instead, and the held queue with its cap is not kept.
- Quarantine: a refusal naming a card is a layer-1 bug, and layer 1's model shows it does not happen.
- Clear, epochs and remove; CI, return, merge stops and resume; reader add; add in parts and insertion's anchors; `ask --another`.
- Byte and read budgets other than the step bound; the tick's step budget (a tick applies every request it planned); R19's one step a tick.
- The rules' reads as separate snapshots: a tick plans every key on one snapshot, then applies request by request, with every outside action free to run between two requests.
- A note line that queues no key is not written.

## The instances

`MCSprintEvents.tla` names the scenarios. Every configuration uses 2 streams (`s1`, `s2`), 2 members (`m1`, `m2`), 1 reader (`r1`; 2 where the review path is checked), 1 verb process and 1 tick process (2 where two loops race), and some of the cards `p1` (s1), `p2` (s2), `p3` (s1) and the sentinel `g1` (s1). Scores are even integers from the counter; rank chooses odd ones below it (U2). The constants of section 5 that the gated instances use: `StepChunk` 2, a step bound of 1 unit (2 in the LIMIT cases, with layer 1 accepting 1), `MaxAttempts` 2, `MaxRedeals` 2, `MaxRereads` 1, a ready cap of 1 (2 in three bench runs), generations mod 2 (3 in three bench runs), and 1 to 3 outside actions. Members that beat are either `Beaters` (they may beat, stop and beat again, unfairly) or `Steady` (they never stop).

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
| `DropComplete` | action | V6: while a stream is marked, only the op's parts move its cards; when a drop records its last part, no card of the stream is open |
| `UniqueScores`, `ScoresBelowCounter` | invariant | U1, U2 |
| `HeldSticky` | action | a member held by fleet down stays held until fleet up |
| `StoppedJudgmentTrue` | invariant | the STOPPED judgment open only when a dry plan would change a card; raised at most once per span |
| `StepWithinBounds` | invariant | every request fits the step bound |
| `LeaseSafe` | action | E4, T1: every tick write carries the current lease generation |
| `ReplayNoop`, `QuietStaysQuiet` | action | a rule run again at once writes nothing |
| `ChunkProgress` | action | a requeue lowers the key's variant |
| `PlaceOnlyUp` | action | a card placed in a member's ready cell only while the member is up |
| `Progress` | temporal | every open primary ends or is named, where a card that waits on a named card (an open need, the first sentinel, the cards before a sentinel, the cards filling every up member's ready cell: (d) of R16's table) is named through it |

`ProgressLiteral` is section 5's `Progress` with `Named` as written; `CursorSoundLiteral`, `IndexAgreesLiteral` and `DueAgreesLiteral` are E3, I1 and D1 as written. Each of these fails on the design (Findings).

Fairness is on each tick process's steps and on `Wall` only.

## Reversed witnesses

Each `MCSprintEventsW<n>.cfg` turns on one broken rule (`Broken = "W<n>"`) on a scenario where it matters, and `MCSprintEventsC<n>.cfg` is the same scenario on the design, checked with every safety property (and `Progress` for the liveness rows). Every control passes, seven of them with the repairs their scenario needs (below). Trace lengths are from the gated runs.

| witness | the rule broken | fails | trace |
|---|---|---|---|
| W1 | ingest without comparing the cursor (two loops) | nothing: passes (Findings) | |
| W2 | ingest moves the cursor before it adds the keys, with an error between | `NoLostWork` | 4 |
| W3 | due entries scored in wall time | `DueAgrees` | 4 |
| W4 | deal without the zguard on `sent:s` (a sentinel ranked ahead between plan and apply) | `PositionHolds` | 7 |
| W5 | release and reach without their rcount guards | `PositionHolds` holds (Findings); `Answerable` fails on reach (`W5Reach`), as it does on the design by H13 | 7 |
| W6 | a tick write without the lease generation (two loops) | `OnePlace` holds (Findings) | |
| W7 | an untaken card replaced without end; a redeal not counted | `Progress` | 17, lasso from 3 |
| W8 | `ApplyNeeds` writes `open` = the value its plan read, less one (two needs landing in one tick) | `OpenExact` | 10 |
| W9 | a refused card left in its index | `HeadActionable` | 5 |
| W10 | a hold's expiry writes no line | `Progress` | 22, lasso from 20 |
| W11 | a sentinel line does not queue `deal` | `Progress` | 20, lasso from 18 |
| W12 | the STOPPED judgment's hold in running time | nothing: passes (Findings, H5) | |
| W13 | a drop in parts without the dropping mark | `DropComplete` | 18 |
| W14 | a drop's selection resumed by offset | `DropComplete` | 4 |
| W15 | `needgone` leaves the waiter in `wait:n` | `ChunkProgress` | 7 |
| W16 | a wait's close queues its owner key, and J ignores the hold | `WaitHolds` | 11 |
| W17 | rank to a score above the counter without raising it | `ScoresBelowCounter` | 3 |
| W18 | deal without memberup (fleet down between plan and apply) | `PlaceOnlyUp` | 7 |
| W19 | R2 sets a held member down | `HeldSticky` | 5 |
| W20 | R17 counts moves due from the backlog (lines and keys) | `StoppedJudgmentTrue` | 10 |
| W21 | the late-work judgment offers rework | `Answerable` | 7 |
| W22 | waive of a missing need that has a record | `NeedsHold` | 5 |
| W23 | a step over the bound refused and its key requeued unchanged | `Progress` | 5, lasso from 2 |
| W24 | the pop removes its entries before it adds its keys, with an error between | `NoLostWork` | 3 |
| W25 | R14's phase 1 without its guard on the entry's score | nothing: passes (Findings) | |
| W26 | release reads waiting instead of `elig` | `NeedsHold` | 5 |
| W27 | the first request of a cut plan removes the keys (the second refused STOPPED) | `NoLostWork` | 11 |

The liveness witnesses and their controls use `Steady` members, so that a failure of `Progress` there comes from the broken rule and not from H12.

## Repairs

`Fixes` names proposed repairs, one for each of ten of the holes the model found (Findings). The design as written is `Fixes = {}`: every witness and every goal configuration runs it. A configuration that checks the other properties on a scenario that meets a hole names the repairs it runs with, so that the rest of the design is still checked there.

| repair | hole | what it changes | shown to close it by |
|---|---|---|---|
| `judgeguard` | H1 | a line that queues `deal` also queues the late key of every work card past its deadline; a replacement closes the lateness judgment | `MCSprintEventsC7`, `MCSprintEventsC23`, `MCSprintEventsFleet`, `MCSprintEventsFaults` |
| `seenfresh` | H2 | R1 sets a member up only while its beat is fresh | `MCSprintEventsC21`, `MCSprintEventsC23`, `MCSprintEventsLand` |
| `madeclose` | H3 | J closes "blocked on something missing: n" in the step that creates n | `MCSprintEventsC22` |
| `cutmark` | H4 | R11's cut judgment also guards that the op's marks stand | `MCSprintEventsC13`, `MCSprintEventsC14`, `MCSprintEventsDrop` |
| `spanreset` | H5 | a wait on the STOPPED judgment lets R17 raise it again once the hold has passed | `MCSprintEventsC12` |
| `stopclose` | H7 | R17 closes its judgment at a look that finds no move due | not closed: the judgment is then stale for at most one look, since a verb can empty the dry plans between two looks, so `StoppedJudgmentTrue` as stated still fails in that window (GoalStale run with the repair: 51 distinct states, fails in 10) |
| `dropcond` | H8 | a decision on a card of a stream being dropped is not printed | `MCSprintEventsRepairDrop`, `MCSprintEventsC13`, `MCSprintEventsC14`, `MCSprintEventsDrop` |
| `downdeal` | H10 | a member's down or held line also queues `deal` | `MCSprintEventsRepairDownDeal`, `MCSprintEventsFaults` |
| `freezefirst` | H11 | a part of `drop --stream` guards that the cells before its own hold no card of the stream | `MCSprintEventsC13` |
| `rankclose` | H13 | a rank or an insertion that places a card before a sentinel closes its "sentinel reached" | `MCSprintEventsRepairRank` |

H6, H9 and H12 are holes in the liveness claim, not in a rule, and have no repair here.

## Running it

TLC runs only on a bench. **The gated cases** are declared in `CASES.tsv` in fourteen groups named `sprintevents*`, recorded in `RUNS.tsv` by `tlacheck` (two workers for a case expected to pass, one for a counterexample, 110 s a group):

```sh
for g in sprintevents sprintevents-land sprintevents-drop sprintevents-faults sprintevents-fleet sprintevents-lease \
         sprintevents-loops sprintevents-controls-a sprintevents-controls-b sprintevents-witnesses-a \
         sprintevents-witnesses-b sprintevents-goals sprintevents-goals-b sprintevents-repairs; do
  go run ./tools/tlacheck run --root . --jar /path/to/tla2tools.jar --dir /tmp/tlc-$g --group $g
done
```

**The bench runs** are in `tla/sprintevents-bench/`, outside `CASES.tsv`, each with 8 workers and a 900 s cap:

```sh
cd tla
for c in MCSprintEventsFullDesign MCSprintEventsFull2 MCSprintEventsFullOps2 MCSprintEventsFullLive MCSprintEventsGoalFlap; do
  mkdir -p /tmp/tlc-$c
  timeout 900 java -XX:+UseParallelGC -Djava.io.tmpdir=/tmp/tlc-$c -cp /path/to/tla2tools.jar \
    tlc2.TLC -workers 8 -deadlock -metadir /tmp/tlc-$c/states -config sprintevents-bench/$c.cfg MCSprintEvents.tla > /tmp/tlc-$c/$c.log 2>&1
done
```

`-deadlock` turns TLC's deadlock check off, as for the other models here (the gated cases declare `ignore-terminal`); the safety and liveness properties are what these runs check.

A note on the model's form: TLC keeps no LET value and no operator argument while it evaluates ENABLED for fairness, so a value used more than once is bound once with `CHOOSE r \in {e(v) : v \in {heavy}} : TRUE` or `\E v \in {heavy}`, and each tick action tests its UNCHANGED part first. Without that, the liveness cases did not finish.

## Results

All runs used TLC 2.19 (`tla2tools.jar` sha256 `936a2620...`) on Java 21.0.12.1, on a 32-core Linux bench shared with other work (load average 20 to 30 during the runs), on 2026-09-30 (UTC).

**The gated groups**, recorded by `tlacheck` (a case expected to pass on two workers, a counterexample on one; the seconds are TLC's, summed over the group's cases):

| group | cases | seconds |
|---|---|---|
| `sprintevents` | 6 | 39.8 |
| `sprintevents-land` | 1 | 72.1 |
| `sprintevents-drop` | 5 | 39.6 |
| `sprintevents-faults` | 1 | 66.7 |
| `sprintevents-fleet` | 1 | 55.2 |
| `sprintevents-lease` | 3 | 51.1 |
| `sprintevents-loops` | 2 | 77.4 |
| `sprintevents-controls-a` | 10 | 21.4 |
| `sprintevents-controls-b` | 13 | 26.4 |
| `sprintevents-witnesses-a` | 11 | 15.8 |
| `sprintevents-witnesses-b` | 13 | 16.0 |
| `sprintevents-goals` | 9 | 11.6 |
| `sprintevents-goals-b` | 1 | 1.2 |
| `sprintevents-repairs` | 3 | 8.1 |

**Every gated case** (`RUNS.tsv` has the rest of each record):

| case | result | generated | distinct | seconds |
|---|---|---|---|---|
| `MCSprintEvents.cfg` | pass | 149163 | 82395 | 18.828 |
| `MCSprintEventsCursor.cfg` | pass | 5661 | 3296 | 3.444 |
| `MCSprintEventsProbe.cfg` | pass | 4953 | 2827 | 3.083 |
| `MCSprintEventsStop.cfg` | pass | 3822 | 2202 | 2.683 |
| `MCSprintEventsLimit.cfg` | pass | 276 | 217 | 2.668 |
| `MCSprintEventsReview.cfg` | pass | 24619 | 12857 | 9.078 |
| `MCSprintEventsLand.cfg` | pass | 207075 | 87735 | 72.145 |
| `MCSprintEventsFaults.cfg` | pass | 565021 | 327286 | 66.726 |
| `MCSprintEventsFleet.cfg` | pass | 874163 | 335786 | 55.233 |
| `MCSprintEventsLease.cfg` | pass | 370752 | 161728 | 42.666 |
| `MCSprintEventsC6.cfg` | pass | 27242 | 12808 | 4.972 |
| `MCSprintEventsW6.cfg` | pass | 35840 | 17636 | 3.435 |
| `MCSprintEventsC1.cfg` | pass | 759368 | 343590 | 58.347 |
| `MCSprintEventsW1.cfg` | pass | 759368 | 343590 | 19.039 |
| `MCSprintEventsDrop.cfg` | pass | 297254 | 145737 | 28.609 |
| `MCSprintEventsC13.cfg` | pass | 6274 | 3703 | 3.099 |
| `MCSprintEventsC14.cfg` | pass | 20531 | 11248 | 4.866 |
| `MCSprintEventsW13.cfg` | fails DropComplete | 1371 | 795 | 1.835 |
| `MCSprintEventsW14.cfg` | fails DropComplete | 27 | 16 | 1.224 |
| `MCSprintEventsC2.cfg` | pass | 500 | 370 | 1.931 |
| `MCSprintEventsC3.cfg` | pass | 222 | 151 | 1.372 |
| `MCSprintEventsC4.cfg` | pass | 2822 | 1756 | 2.548 |
| `MCSprintEventsC5.cfg` | pass | 3693 | 2299 | 3.161 |
| `MCSprintEventsC7.cfg` | pass | 51 | 38 | 1.328 |
| `MCSprintEventsC8.cfg` | pass | 4208 | 2361 | 3.531 |
| `MCSprintEventsC9.cfg` | pass | 136 | 102 | 1.518 |
| `MCSprintEventsC10.cfg` | pass | 371 | 249 | 2.139 |
| `MCSprintEventsC11.cfg` | pass | 827 | 581 | 2.508 |
| `MCSprintEventsC12.cfg` | pass | 66 | 45 | 1.399 |
| `MCSprintEventsC15.cfg` | pass | 3208 | 1971 | 3.092 |
| `MCSprintEventsC16.cfg` | pass | 371 | 249 | 1.733 |
| `MCSprintEventsC17.cfg` | pass | 1664 | 1020 | 2.296 |
| `MCSprintEventsC18.cfg` | pass | 1694 | 992 | 2.261 |
| `MCSprintEventsC19.cfg` | pass | 80 | 67 | 1.351 |
| `MCSprintEventsC20.cfg` | pass | 322 | 215 | 1.351 |
| `MCSprintEventsC21.cfg` | pass | 2809 | 1389 | 2.679 |
| `MCSprintEventsC22.cfg` | pass | 2948 | 1944 | 2.654 |
| `MCSprintEventsC23.cfg` | pass | 100 | 75 | 1.827 |
| `MCSprintEventsC24.cfg` | pass | 55 | 47 | 1.251 |
| `MCSprintEventsC25.cfg` | pass | 439 | 294 | 1.660 |
| `MCSprintEventsC26.cfg` | pass | 169 | 130 | 1.602 |
| `MCSprintEventsC27.cfg` | pass | 1912 | 1279 | 2.662 |
| `MCSprintEventsW2.cfg` | fails NoLostWork | 9 | 9 | 1.176 |
| `MCSprintEventsW3.cfg` | fails DueAgrees | 22 | 17 | 1.172 |
| `MCSprintEventsW4.cfg` | fails PositionHolds | 141 | 99 | 1.268 |
| `MCSprintEventsW5.cfg` | pass | 3720 | 2320 | 1.824 |
| `MCSprintEventsW5Reach.cfg` | fails Answerable | 126 | 87 | 1.271 |
| `MCSprintEventsW7.cfg` | fails Progress | 72 | 52 | 1.349 |
| `MCSprintEventsW8.cfg` | fails OpenExact | 422 | 251 | 1.306 |
| `MCSprintEventsW9.cfg` | fails HeadActionable | 11 | 10 | 1.322 |
| `MCSprintEventsW10.cfg` | fails Progress | 364 | 242 | 1.379 |
| `MCSprintEventsW11.cfg` | fails Progress | 812 | 572 | 2.446 |
| `MCSprintEventsW12.cfg` | pass | 36 | 26 | 1.257 |
| `MCSprintEventsW15.cfg` | fails ChunkProgress | 149 | 100 | 1.234 |
| `MCSprintEventsW16.cfg` | fails WaitHolds | 92 | 66 | 1.267 |
| `MCSprintEventsW17.cfg` | fails UniqueScores|ScoresBelowCounter | 9 | 8 | 1.202 |
| `MCSprintEventsW18.cfg` | fails PlaceOnlyUp | 138 | 94 | 1.239 |
| `MCSprintEventsW19.cfg` | fails HeldSticky | 11 | 10 | 1.090 |
| `MCSprintEventsW20.cfg` | fails StoppedJudgmentTrue | 172 | 117 | 1.268 |
| `MCSprintEventsW21.cfg` | fails Answerable | 44 | 28 | 1.202 |
| `MCSprintEventsW22.cfg` | fails NeedsHold | 72 | 48 | 1.203 |
| `MCSprintEventsW23.cfg` | fails Progress | 6 | 5 | 1.197 |
| `MCSprintEventsW24.cfg` | fails NoLostWork | 7 | 7 | 1.218 |
| `MCSprintEventsW25.cfg` | pass | 439 | 294 | 1.287 |
| `MCSprintEventsW26.cfg` | fails NeedsHold | 11 | 10 | 1.231 |
| `MCSprintEventsW27.cfg` | fails NoLostWork | 198 | 139 | 1.411 |
| `MCSprintEventsGoalNamed.cfg` | fails ProgressLiteral | 40 | 31 | 1.334 |
| `MCSprintEventsGoalStopped.cfg` | fails Progress | 5 | 4 | 1.226 |
| `MCSprintEventsGoalSpan.cfg` | fails Progress | 58 | 37 | 1.274 |
| `MCSprintEventsGoalStale.cfg` | fails StoppedJudgmentTrue | 74 | 47 | 1.244 |
| `MCSprintEventsGoalDrop.cfg` | fails Answerable | 264 | 157 | 1.325 |
| `MCSprintEventsGoalMissing.cfg` | fails IndexAgreesLiteral | 9 | 8 | 1.184 |
| `MCSprintEventsGoalCursor.cfg` | fails CursorSoundLiteral | 642 | 416 | 1.475 |
| `MCSprintEventsGoalDue.cfg` | fails DueAgreesLiteral | 57 | 41 | 1.292 |
| `MCSprintEventsGoalDownDeal.cfg` | fails NoLostWork | 46 | 39 | 1.249 |
| `MCSprintEventsRepairDownDeal.cfg` | pass | 76 | 62 | 1.169 |
| `MCSprintEventsRepairDrop.cfg` | pass | 14109 | 7985 | 4.374 |
| `MCSprintEventsGoalRank.cfg` | fails Answerable | 126 | 87 | 1.207 |
| `MCSprintEventsRepairRank.cfg` | pass | 1553 | 1057 | 2.514 |

**The bench runs** (`tla/sprintevents-bench/`, 8 workers, a 900 s cap, on the final module):

| config | instance | result | generated | distinct | seconds |
|---|---|---|---|---|---|
| `MCSprintEventsFullDesign` | 3 primaries and the sentinel, 2 readers, cap 2, every verb but the drops, 3 verbs; the design as written | `Answerable` violated, 10 states: H13 | 66,137 | 39,024 | 5.4 |
| `MCSprintEventsFull2` | the same with every repair, 2 verbs | did not finish: no violation in 12,122,583 distinct states (depth 38 when stopped); a timeout is not a pass | - | 12,122,583 | 901.7 |
| `MCSprintEventsFullOps2` | drops in parts, resume, abort, a crash, an error, stop and start, rank; every repair; `StoppedJudgmentTrue` left out (H7) | pass, every other safety property | 8,217,261 | 3,839,824 | 219.9 |
| `MCSprintEventsFullLive` | `Progress` with 2 readers, 2 steady members, workers and readers acting, every repair | pass | 7,739 | 3,799 | 3.4 |
| `MCSprintEventsGoalFlap` | two members that beat and stop; `seenfresh` | `Progress` violated: a 57-state lasso back to state 36 (H12) | 1,076,221 | 393,662 | 67.4 |

Three larger runs did not finish and are not kept as configurations. The first ran on the final module. The other two ran on an earlier revision, before the last repair (`rankclose`) was added, and were not rerun. They are reported for their size only:

- `MCSprintEventsFull2` with 3 verbs: stopped by hand after 11 minutes at 12,869,947 distinct states and depth 20, with 4,508,009 states still on the queue and no violation.
- The task's instance with two loops, a crash, five verbs and three lease generations: 16,670,850 distinct states at the 900 s cap, no violation.
- The same with one takeover (two lease generations): stopped by hand before its cap, at 1,028,349 distinct states in its last progress report, with no violation.

The two-loop cases that finish are the gated `MCSprintEventsLease`, `MCSprintEventsC1`, `MCSprintEventsW1`, `MCSprintEventsC6` and `MCSprintEventsW6`.

## Findings

Every trace below was read state by state and checked against the design's text. H1 to H13 are holes: the design as written breaks a property section 5 states, or a claim the design makes. They are numbered in the order the model found them. Each has a configuration that fails on the design (`Fixes = {}`); nine have a modelled repair (`Fixes`) and a configuration that passes with it, and H7's repair bounds the failure without closing it. W-rows are witnesses whose failure the table of section 5 claims but the design does not give. The last group is wording: a property as written that the design, as it is meant, does not keep.

### H1. R11 judges a late work card and never looks again, though a replacement becomes possible

`MCSprintEventsC7` without `judgeguard` (15 states), and `MCSprintEventsFleet` without it (12 states).

- Fleet: p1 is dealt to m1 and not taken; m2 is held by `fleet down m2`. The untaken deadline passes, the pop queues `late:untaken:p1`, and R11 finds no other member up with room, so it opens "a work card is past its deadline: not taken". Then `fleet up m2` sets m2 up (its beat is fresh). R11's plan on the present state now replaces p1 to m2. No key brings R11 back: a member's up line queues `deal` and `level` (2.1), and the judgment's owner key is queued only when the coordinator closes it. `NoLostWork` fails.
- C7: the same without a verb. R11 plans "judge" in a tick whose RT2 also plans R1 on `seen:m2`. R1's request applies first (m2 up), then R11's judge applies: its guard is only the card at its place with its revision (2.3, R11), and "no other member up" was the plan's read.
- `MCSprintEventsFaults` with `downdeal` alone (23 states) shows the same with room: m2's only ready slot was full when R11 judged p3, then p1 was withdrawn from m2 and the slot freed. A card leaving a cell queues `deal` only.

The card is named, so nothing is silent, but the mechanical move is not made, and `NoLostWork` as section 5 states it fails. Repair (`judgeguard`): a line that queues `deal` also queues the late key of every work card past its deadline, and a replacement closes the lateness judgment. A guard at apply on the judge branch alone is not a repair: with a member that beats and stops in step with the tick, it refuses both the replacement and the judgment forever (the model found that lasso when the guard was tried).

### H2. A member is left up with no beat entry, and nothing ever sets it down

`MCSprintEventsC21` without `seenfresh` (15 states); also `MCSprintEventsC23` and `MCSprintEventsLand` without it.

1. m1 is down. It beats once: `seen:m1` is entered at R, `beat:m1` at R + 15 s.
2. The loop does not pop `seen:m1` before `beat:m1` falls due (in the trace, one unit of time passes first; in a real run, a loop away for 15 s, for instance restarting or waiting for the lease).
3. One tick pops both: the agenda holds `seen:m1` and `down:m1`. RT2 plans both on one read: R1 will set m1 up; R2 sees m1 down, so "otherwise m's status goes down" changes nothing, m1 has no card, and its plan is empty.
4. RT3: R1 applies (m1 up). R2's empty plan removes `down:m1`.
5. m1 is up, its `beat:m1` entry is gone (popped), and no line or entry will queue `down:m1` again. R2's plan on the present state sets m1 down; `NoLostWork` fails. From then on, deals go to a member that is not beating, until each card's untaken deadline moves it.

R1's guard is "the control card at its place with its revision, status down" (2.3); it does not look at the beat. Repair (`seenfresh`): R1 sets a member up only while `beat:m` lies above R, the same freshness R2 and `fleet up` read (1.4.4).

### H3. "Blocked on something missing" outlives the creation of the need, and its `add` decision is then refused

`MCSprintEventsC22` without `madeclose` (3 states).

1. p2 waits on p1, which has no record: "a primary is blocked on something missing: p1" is open on p2 (decisions `add p1`, `ack`, `drop p2`).
2. `add p1` applies: p1 is created.
3. The judgment stays open until R4's `made:p1` runs, a tick later at the soonest. In that window its decision `add p1` is refused (the id exists; L1's EXISTS), and `Answerable` fails. (`ack` is printed only "while n has no record", its stated condition, so it is not printed.)

Row 30 of section 0 says "Creating n closes the judgment"; in 2.3 only R4's `made` closes it. Repair (`madeclose`): J closes the judgment in the step that creates n, as it closes a lateness judgment in the step that ends its state (1.3.4), or the table states the condition "while n has no record" on `add` too.

### H4. The cut clock's judgment can open for an op that has already ended

`MCSprintEventsC13` without `cutmark` (16 states); also `MCSprintEventsC14` and `MCSprintEventsDrop`.

1. `drop --stream s1` runs in parts; its `cut:o1` entry (wall time) falls due while the op runs, and the pop queues `late:cut:o1` and removes the entry.
2. A tick plans R11 on `late:cut:o1`: the op's mark stands and its entry is absent.
3. The op's last part applies: it removes the entry (absent again) and clears the mark.
4. R11's request applies. Its guard is "XGUARD on the entry's score as read" (2.3, R11): absent as read, absent now. It opens "a verb in parts stopped before its end" for an op that has ended; its decisions (`drop --abort --op`, the same command with `--op`) are refused. `Answerable` fails.

Repair (`cutmark`): the judgment also guards that the op's marks stand (or its receipt is not final).

### H5. R17 raises its judgment once per STOPPED span, so a wait on it ends the naming for good

`MCSprintEventsGoalSpan` (17 states, a lasso from state 15), and `MCSprintEventsC12` without `spanreset`.

1. The machine is STOPPED; p1 waits and is free to go, so a dry plan releases it: moves are due.
2. R17 sets `due_since`; ten minutes later it raises "the machine is STOPPED and moves are due" and sets `stopraised` = `stopped_since`.
3. The coordinator runs `wait --for d`: the judgment is closed and held until `stophold` (1.3.4, "wait on the STOPPED judgment sets stophold_ms = wall + d instead").
4. The hold passes. Every later look finds moves due, `due_since` passed and `stophold` passed, but `stopraised` equals `stopped_since`, so R17 never raises again. p1 is open forever and nothing names it: `Progress` fails.

If `wait` instead left the judgment open, `stophold` would do nothing, since R17 raises at most once per span. Either way the wait's own clock is dead. Repair (`spanreset`): the wait clears `stopraised`, so R17 raises again once the hold has passed.

### H6. A machine left STOPPED with no move due names nothing, however long it stays

`MCSprintEventsGoalStopped` (4 states, a lasso from state 2).

The machine is STOPPED; p1 is dealt to m1 and not taken. R is still, so the untaken deadline never falls due; no dry plan changes a card, so R17 never raises. p1 is open forever and nothing names it: `Progress` fails. The design's argument ("a machine left STOPPED is named by R17") covers only a machine with moves due. This is a hole in the liveness claim, not in the mechanism: `Progress` has to be stated for behaviours in which the machine runs again, or `Named` has to count a STOPPED machine.

### H7. R17 never closes its judgment when no move is due any more

`MCSprintEventsGoalStale` (10 states).

STOPPED, moves due, R17 raises its judgment; then `drop p1` removes the only card that could move. The judgment stays open with nothing to do, and only `start` closes it. `StoppedJudgmentTrue` fails. Repair (`stopclose`): R17 closes its judgment at a look that finds no move due. That bounds the stale judgment to one look but does not make the invariant hold at every state: a verb can empty the dry plans between two looks (GoalStale run with the repair still fails, in 10 states). The invariant would have to read "at every look".

### H8. Decisions that DROPPING refuses are printed

`MCSprintEventsGoalDrop` (9 states); also `MCSprintEventsC14`.

p1 is in review with "cannot ask" open (decisions include `rework p1` and `drop p1`). `drop --stream s1` applies its first part and marks s1. Both decisions are now refused DROPPING ("every verb that changes a card of a stream being dropped or removed is refused DROPPING, except that op's own parts", section 3), and `Answerable` fails. The same holds for a lateness judgment on a card of s1 raised just before the mark. Repair (`dropcond`): the table of 2.2 states the condition, and `inbox` does not print a decision on a card of a stream being dropped.

### H9. Section 5's `Progress` names no card that waits on a named card

`MCSprintEventsGoalNamed` (14 states, a lasso from state 12).

p2 waits on p1. p1 is dealt to m1, the only member up, which beats steadily; p1 is not taken, R11 finds no other member and opens "a work card is past its deadline: not taken" on p1's work card. p1 is named; p2 is not, and never will be until p1 moves. `ProgressLiteral` (section 5's `Named`) fails. The same holds for a card behind an unlanded sentinel, whose "sentinel reached" names only the sentinel, and for a ready card behind members whose ready cells are full. R16's table has these as (d), "what it waits on, itself held"; `Progress` needs the same: the model's `Progress` names a card through what it waits on, and holds on every liveness configuration.

### H10. A member going down queues no deal, so a deal plan emptied on a stale read removes its key

`MCSprintEventsGoalDownDeal` (12 states).

1. p1 is dealt to m1, the only member up; p3 becomes ready (fresh) and waits for room.
2. m1 stops beating. One tick plans `down:m1` (set m1 down, withdraw p1: two requests at a step bound of one) and `deal` (m1's cell is full: nothing to do).
3. RT3 in the round robin's order: `down:m1`'s first request sets m1 down; `deal`'s empty request removes `deal`. No member is up and p3 is dealable, so R6's plan now opens "no fleet member is up", and `deal` is owed nowhere: `NoLostWork` fails.
4. `down:m1`'s second request withdraws p1; its line (a card enters ready) queues `deal` again.

It is transient here, because R2's own later request queues `deal`. It is still a failure of `NoLostWork` as stated, and it rests on the chance that the down member had a card. 2.1 has no line for a member going down, and R6's "no member up" depends on it. Repair (`downdeal`): a member's down or held line also queues `deal`.

### H11. Part 1 of a drop reads its head before the freeze exists, so a card that moves back to a passed cell is left

`MCSprintEventsC13` with `cutmark` alone (18 states).

1. s1 has p1 and p3 working. `drop --stream s1` plans part 1: waiting and ready are empty, the head is p1 in the working cell.
2. Before part 1 applies (and so before any mark exists), both members go down and R2 withdraws p3: p3 goes to ready, a cell part 1's read passed.
3. Part 1 applies: p1 at its place, the guard passes; it removes p1 and continues from the working cell, where nothing is left. It is the last part: it records the end and clears the mark with p3 still open in ready. `DropComplete` fails.

1.5.4 says each part "drains the head, so nothing is skipped or taken twice"; that holds once the mark is set, and the mark is written by part 1 itself. Repair (`freezefirst`): a part guards that the cells before its own hold no card of the stream (`rcount` at most 0), or the mark is written before part 1 reads.

### H12. A fleet that beats and stops in step with the tick starves every step that places a card, and nothing names the cards

`sprintevents-bench/MCSprintEventsGoalFlap` (a 57-state lasso back to state 36, on the bench: 393,662 distinct states), and the lassos of `MCSprintEventsC7` and `MCSprintEventsC23` when their members beat and stop.

Two members, each beating and stopping (the design's environment: "a member may beat, stop and beat again forever"). In every cycle deal plans p1 to the member that is up at its read; before its request applies, R2 (earlier in the round robin) sets that member down, and memberup refuses the deal. At the next read the other member is up, and so on. At every plan some member is up, so "no fleet member is up" is never raised, and p1 stays ready forever unnamed: `Progress` fails. Each step is refused correctly; the design has no judgment for a card that the machine keeps failing to place. The schedule is adversarial (the beats must fall in step with the tick), but beats are unfair in section 5 and the claim is stated for them. The gated liveness configurations use `Steady` members (members that beat and never stop) to check `Progress` apart from this.

### H13. "Sentinel reached" outlives a card ranked before its sentinel, and prints a `release` that is refused

`MCSprintEventsGoalRank` (7 states); found first by `sprintevents-bench/MCSprintEventsFullDesign` (10 states).

1. g1 is the first sentinel of s1 with nothing before it; p3 sorts after it. R3 reaches g1 and opens "sentinel reached: g1" (decisions `release g1`, `add --before g1`, `drop g1`).
2. The coordinator runs `rank p3 --score 3`: p3 now sorts before g1.
3. Until R3's unreach closes the judgment (the rank's line queues `resolve:s1`, so a tick later), the judgment prints `release g1`, which the verb refuses: "release G guards, at apply time, that no open card of s is before G" (2.4). `Answerable` fails.

The table of 2.2 gives no condition on `release`. Repair: the rank's step closes "sentinel reached" when it places a card before the sentinel (J sees it in the pre stage, as it does for lateness judgments), or the decision is printed with the condition "while no open card of s sorts before G". Repair (`rankclose`): the first; `MCSprintEventsRepairRank` passes with it.

### Witnesses the table claims that do not fail

- **W1** (ingest without comparing the cursor, `MCSprintEventsW1`, 343,590 distinct states): passes. Two loops can both ingest only with the current lease generation each, and every takeover raises the generation (1.1, E4). The cursor comparison is the second guard: by this argument W1 can fail only together with W6 (that pair was not run).
- **W5** (release and reach without the rcount guards, `MCSprintEventsW5`): `PositionHolds` holds. A card released behind a new sentinel is not in `fresh:s` below sigma, and deal's own zguard refuses it, and the `release` verb guards that no open card sorts before the sentinel. With W5, "sentinel reached" opens while a card sorts before the sentinel and its `release` decision is refused (`MCSprintEventsW5Reach`, 7 states); but the design reaches the same state by another path (H13), so no property fails only for the broken copy.
- **W6** (a tick write without the lease generation, `MCSprintEventsW6`): `OnePlace` holds, and no card is dealt twice. Every rule step carries place, revision and absence guards, and the ingest the cursor comparison. Only `LeaseSafe`, which is the rule itself, fails.
- **W12** (the STOPPED judgment's hold in running time, `MCSprintEventsW12`): passes. A hold that never passes while STOPPED names the machine forever. The design as written is the one that fails, by H5.
- **W25** (R14 without its guard on the entry's score, `MCSprintEventsW25`, with probes): `ReplayNoop` holds. A second run plans on the entry the first moved and plans nothing; the guard only matters against a second writer, which the lease already refuses.

### Wording

- **E3 and `CursorSound`** (`MCSprintEventsGoalCursor`, 19 states): "every key of every line at or before the cursor is in the agenda, the held queue or parked, or its rule is quiet" fails when a later change, whose line is not yet ingested, makes an old key owed again (`deal`, after a withdrawal frees a card). The model's `CursorSound` counts a line after the cursor or an entry as owing it, as `NoLostWork` does.
- **I1 for `missing`** (`MCSprintEventsGoalMissing`, 3 states): "missing: n has no record and wait:n is not empty" fails between `add n` and R4's `made:n`, which removes n from `missing` a tick later. The model's `IndexAgrees` holds the direction the rules rely on.
- **D1** (`MCSprintEventsGoalDue`, 13 states): "its entry has fired and its late key is queued" fails between R13's unheld line and its ingest: the hold is gone, the late key is on the line, not in the agenda. The model's `DueAgrees` counts the line.
- **`ReplayNoop`'s definition**: "a second plan and apply ... right after a run that removed k, with no outside action between" also has to exclude every change since the first run's plan. A deal plan made before resolve released a card in the same tick is empty, removes `deal`, and a second run deals the card. Nothing is lost, since resolve's line queues `deal`. The model's probe runs only while the facts are as the first plan read them.
- **`Named`'s subjects**: section 5's `Named(c)` reads `jopen[c]`, but a lateness judgment's subject is the work card or the read card (2.2). The model reads a primary's work and read cards as its subjects too.
- **`NoLostWork` and R1**: R1's plan is not empty whenever a member is down, and only a beat will queue `seen:m`. The model leaves R1's key out of `NoLostWork`; section 5's "(presence) a beat ... that queues it" says as much.
