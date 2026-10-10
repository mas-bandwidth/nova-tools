# tla: the models of nova-tools' state machines, and their runners

> **The nova-sprint models are stale as of 2026-10-01 and are not the design.** The owner's ruling that day: nova-tables is
> the modelled layer and stays so; nova-sprint is in its get-it-done stage and is changed, tested on the twin store, read,
> landed and run on the real fleet without a model change each time ("we aren't going to model the whole thing in TLA+
> everytime we make a change"; "the thing that is built upon, is held to a higher standard than the thing built on top of
> it"). The modules this covers: `DirtyTick` (and `DirtyTickRead`), `RouteIndex`, `Land`, `CardContract`, `SprintEvents`,
> `CardMachine`, `WorkImport`. Where they are known to disagree with the code today: the deal places up to twice a member's
> width (`DealAhead`), the models say never past the width; the fleet table and the readers table are rebalanced at the
> start of every tick and a held or down member keeps no card, the models have no such step; a read's re-ask is counted
> when the reader hands it back, the model counts it at the ask; reads draw routes from the tier's index beside work
> cards, checked in the model with one read and no redeal; `SprintEvents` and `CardMachine` describe designs no longer run.
> The run records (`RUNS.tsv`) still match these model files, which is all `TestTLCRecordsCoverCurrentModels` checks: it
> does not check the models against the code. At the contraction pass each is either re-derived from the behaviour the
> fleet passes proved, or deleted. `BenchStage` is current. The table-layer models below (`TableMachine`, `MemberTable`, `EpochMemberTable`,
> `TableEdit`, `TableOrder`, `TableSession`, `RedisFn`, `TableFirstContact`, `FirstConn`, `FuseBox`) are current. [`COVERAGE.tsv`](COVERAGE.tsv) lists every state machine with its code, its module and its status (current, partial, stale or missing) and the card that owes a model that is not current; `TestEveryStateMachineHasACurrentModel` (internal/ci) holds it.

The TLA+ modules here are the specifications of the state machines this repo implements (rowan-new SPEC-COORDINATOR section 8: the backend is the state machine, the verbs are its actions; Glenn 2026-09-27: TLA+ for every state machine, every project). The findings each model produced, verified against the code by hand, are in rowan-new `specs/tla/FINDINGS.md`; the model documents (`TABLE-MODEL.md`, `MEMBER-TABLE-MODEL.md`) are copied here beside the modules they describe.

| Module | Instance | What it is |
|---|---|---|
| `CardMachine.tla` | `MCCardMachine` | the card's life over cells (the copy model of 02_card_move.lua), the corrected design after its three findings |
| `CardISA.tla` | `MCCardISA*`, `MCCardISALive` | the card instruction set (docs/SPEC-ISA.md, layer 1): a card is one instruction and its kind says what it carries. It `EXTENDS CardMachine` and reuses its actions through one frame; it adds only what the kinds add: the one `wait` kind, whose one action `Wait(c)` has one guard `OperandHolds(c)` over an operand that is a card id, `release` or `external` (today's hold, sentinel and wave are one action's data, where CardMachine's `Release` is one path and the code it was read from holds three), and the per-kind results (`think`/`script`/`merge` a head and a verdict, `verify` a verdict, `wait` none). It replaces `CardMachine.Release`, `DealWork`, `Land` and `LandEvent`, named in its header. It proves `NoCardRetiresTwice` (no card lands twice), `RetiredHeadOnBase` (a landed card's head is an ancestor of its base) and `WaitDispatchesOnlyWhenOperandHolds` (a wait card is never dispatched before its operand holds), with one reversed witness each (`MCCardISABrokenSecondLand`, `MCCardISABrokenOtherBase`, `MCCardISABrokenWaitFirst`), and, on the small `MCCardISALive`, that a wait whose operand comes to hold is dealt. The card model's stale note above does not cover this module; it is written to `docs/SPEC-ISA.md` (landed on the base at `817177be66d4a2764bd228c223eb485a8ef7c164`), and `LandTwoLevel.tla` (the `merge-tree-proof` branch, head `40e9dc34ea18f7362f3b1b914ac204fe48263ae7`) is cited by branch and head, not copied: that branch has not landed |
| `TableMachine.tla` | `MCTable*` | nova-table as table.lua is today at f7745885, with its actual gaps (Stella; the strict gate fails on purpose) |
| `MemberTable.tla`, `EpochMemberTable.tla` | `MCMember*`, `MCEpochMember*` | the corrected member placement and epoch protocol (Stella): one place per table inside the epoch, lossless shape, no owned alias, stale writers refused |
| `TableEdit.tla`, `TableOrder.tla` | `MCTableEdit*`, `MCTableOrder*` | nova-table's edit verbs and the order of its rows and columns, with reversed witnesses |
| `TableSession.tla` | `MCTableSession*` | `nova-table shell`: lines, one connection, the store coming and going, a stop signal, the exit code (the design #4458 is held to) |
| `RedisFn.tla` | `MCRedisFn*` | the function libraries of one Redis under several loaders (internal/redisfn: Check, Load, Ensure, LoadMissing): one holder to a function name, a refusal that writes nothing, no moment without the library, a LoadMissing that never replaces |
| `FirstConn.tla` | `MCFirstConn*` | `internal/redisconn`'s first connection: the probe Open sends, taken and answered in the store's place only after HELLO was accepted, with seven reversed witnesses |
| `TableFirstContact.tla` | `MCTableFirstContact*` | nova-table's first contact with a store (cmd/nova-table/library.go): a verb that meets "Function not found" loads the library with LoadMissing at most once per process and is sent again once, only when its first send ran nothing, with three reversed witnesses |
| `FuseBox.tla` | `MCFuseBox*` | nova-fuse's box: the gate answers only from a box it read and from every box named, only a lift, init or a hand-edit makes a surface clear, init never replaces a box, a lockdown always blows; five reversed witnesses |
| `Bus2.tla` | `MCBus2*` | nova-bus's delivery machine (docs/SPEC-BUS.md, internal/bus): a message sent to every recipient's stream and the log at once, pending from recv until ack, a live reader keeping its message (idle under ClaimAfter, in the code), a reader that crashed holding it handed it again before any new one, ack idempotent; two recipients, three messages, two consumers, crashes bounded; five reversed witnesses (a send that writes the streams one by one, a recv that reads new before pending, a recv that claims a message a live reader holds, an ack of an undelivered entry, an ack that re-adds), and the liveness that every sent message is acked by every recipient it names |
| `Friend.tla` | `MCFriend*` | nova-friend's daemon machine (docs/SPEC-FRIEND.md, internal/friend/machine.go): the connection with the coordinator (a ping within the window, else silent, said to the session once per outage and back once) and the challenge of the session (a ping's nonce pushed in, the session's own pong with that nonce ending it, a window unanswered deaf, only the current nonce counting); a clock of nine ticks, a window of three, three pings; five reversed witnesses (the daemon's beat alone making the friend up, a challenge that never times out, the outage said every tick, the outage never said, any seen nonce answering); no liveness is claimed, the clock being finite and DeafAfterWindow bounding an open challenge at every state |
| `Reach.tla` | `MCReach*` | nova-friend reach, the escalation ladder (docs/SPEC-FRIEND.md, Reach; cmd/nova-friend/reach.go): steps bus, push, window, then ok or failed; a proof stops the ladder; a step's clock never passes Bound; failed only after bus, push and window were left; one reversed witness takes a step after a proof; no liveness, the clock being finite |
| `FriendPresence.tla` | `MCFriendPresence*` | a friend's presence on the sprint's table and the cards it deals the friend (docs/SPEC-FRIEND.md, "Presence"; written before the code, card fr-presence-model): the world per friend (harness running or closed, session answering or silent, a provider limit until a reset, a daemon beating) and the table (the coordinator's hold, the age of the session's last answer to a nonce, where each card is); time is ages that stop at the bound, so the clock never ends and liveness is checked; up is an answer younger than the bound and never the beat, and the hold and the tick take back a friend's cards in the step that makes the friend not up; two friends, two cards (one in the liveness cases), a bound of two ticks, three disruptions (two). It proves a friend shown up has a fresh session answer (`UpHasFreshAnswer`) and a harness running or closed under the bound (`UpHasRunningHarness`, `ClosedDownWithinBound`), a held or down friend holds no card (`NoCardOffUp`), the beat alone never makes a friend up (`BeatAloneNeverUp`), a closed harness is shown down on the clock alone (`ClosedShownDown`, `SpecClosed`) and a card taken back is dealt to another friend under the full environment assumption (`HeldCardDealtElsewhere`, `SpecLive`); seven reversed witnesses (the beat makes up, an answer never ages, three ways, an answer from a closed harness, a deal to a down friend, a take-back a step late, a card dealt back to the friend it was taken from); one finding case, `MCFriendPresenceFindingRunningNow`: "shown up has a running harness" read at every state fails, since the table cannot see the app close until the answers stop |
| `FriendCard.tla` | `MCFriendCard*` | the friend-card lifecycle (docs/SPEC-SPRINT.md, "Friend-card lifecycle model"; docs/SPEC-FRIEND.md, "Combined daemon and card lifecycle"; card friend-card-lifecycle-tla-b, 2026-10-07): separate daemon-heartbeat and session-wake evidence; tier- and idle-lane-first deal/level; delivery receipt, start receipt, progress, report plus coordinator bus note, review/merge return, explicit take-back, and bounded alarms. Every evidence age saturates at the bound; the safety control is two friends and three cards, the liveness controls and witnesses two friends and two cards (the back-up witness one card). The `shown` row is a projection of the variables (`shown == DashboardMap`), so no action can write it but by moving the facts it reads. It checks `WorkingOnlyAfterStart`, `FinishedHasEvidenceAndNotice`, `NoCardOffUp`, `TierHeld`, `WidthRespected`, `DeadRunFinishedWithinBound`, `DashboardIsDerived`, `DeliveryFailureVisible`, `DealOnlyToHearing`, `DeliveredHears` and `WorkingMeansStarted`, and the liveness `EveryDealtSettles`, `EveryFinishedReturns` and `EveryAbleFriendUp` under `SpecLive`; nine reversed witnesses each fail on their named property (a beat alone, a down friend never re-asked, a card shown working before a start, a tier-ignored deal, a delivery failure hidden from the row, a stall kept past the bound, a finish never judged, an able friend never brought back, a row shown up while a lower layer fails). `MCFriendCard.cfg`, `MCFriendCardLive*.cfg` and `MCFriendCardFinding*.cfg` are the `friendcard` group of tla/CASES.tsv, with records in tla/RUNS.tsv; two gaps (lane-end-finishes-the-card, friend-deal-idle-lanes-first) have no witness in this abstraction, named in the report. This bounded abstraction is not a refinement proof. |
| `FriendStage.tla` | `MCFriendStage*` | the daemon stages every job it writes (internal/friend/stage.go, docs/SPEC-FRIEND.md "The daemon stages every job it writes"; card daemon-stages-every-job2, 2026-10-06): three cards, two on one repository, each job unstaged, staging, waiting or staged; her account's reach of a repository and a card's base come and go outside (bounded), and the daemon may stop while a stage runs. It proves a lane is handed a card only once its JOB.md is there (`HandedOnlyStaged`), a staged checkout came from the repository's mirror (`StagedHasMirror`), one judgment per repository or card while it stands (`OneJudgmentWhileItStands`, `SaidIsTold`), and, under fairness, that every card whose repository and base are there for good is handed (`EveryCardIsHanded`). Three reversed witnesses: nextCard without the stageOwed guard, a judgment said for every failed stage, and a failed job never staged again |
| `SeatHealth.tla` | `MCSeatHealth*` | nova-sprint's seat generation and a friend's health (docs/SPEC-SPRINT.md section 1, "A friend's health"; internal/sprint seat.go, friend_health.go): the seat (holder, generation) moved by handover, one friend observed by the coordinator's daemon under the seat it read (observations in flight, delivered in any order), her own beat, the coordinator's hold, and the table's word derived at every read; two holders, a clock of six, three observations, a proof fresh for three; it proves the table shows up, held or down and nothing else, held is the hold alone, up only on an up word under the current seat with a fresh proof (or, never observed, a fresh beat), an old seat's proof never up under a new seat (A->B->A), an accepted observation at the seat's generation, the row's proof never going back, an observed friend never up by her beat, the row's proof never dated after the server's clock (a daemon's proof may be dated at any time), and the generation stepping with every handover; six reversed witnesses (any generation writes, an older proof overwrites, the beat wins, a proof never ages, a handover keeps the generation, a proof from the future writes) |
| `BenchStage.tla` | `MCBenchStage*` | one bench's release stage under `fleet/tools.yml` (nova-tools#5102; written by Zhi, adopted with the send rule of today): a stage seeded from the installed build's directory unverified, the files sent, `release install`'s whole verification, refusal and runs that crash anywhere, over a healthy, corrupt and two partial installed builds. `ReusedByteIdentical` (once the send is done every stage file holds the release's bytes), `NoWrongBinary`, `NoVerifiedWithWrong` and `Liveness` hold when the send list is measured from the stage's bytes (`MCBenchStage`); one reversed witness, the first cut's send list by `SHA256SUMS` line, breaks `ReusedByteIdentical` (`MCBenchStageBrokenLines`) |
| `SprintEvents.tla` | `MCSprintEvents*` | the upper layers of nova-sprint (layers 3 to 8 of the event-driven tick design, standing on the table layer's guarantees): the log turned into keys, due times in running time, the rules with plan and apply separate, the tick, the verbs; 29 reversed witness configurations (W1 to W27, W7 split in two, and W5's reach half, which breaks reach and unreach and not the verb), 27 of which fail with the property their row names while W1 and W25 pass because the design holds there in depth, each with its unbroken control; goal cases that fail for the seventeen holes the model found (thirteen in the design as written, four in repairs errata 3 decided, three of them decided in its amendment 2 and repaired here; H12 and H15 on the bench), each decided repair with a case that passes. The larger runs are in `sprintevents-bench/`, outside `CASES.tsv`. `README-SprintEvents.md` says the rest, including what is not modelled |
| `SprintRules.tla` | `MCSprintRules*` | the machine feeding itself (docs/SPEC-SPRINT.md section 2, a card replaced by its twin; section 8, answered by rule; section 14, the fleet is idle; internal/sprint twins.go, rules.go, rules_read.go, idle.go, cmd/nova-sprint landgo.go), six small machines each checked alone by its `Part`: **twins** (an old card, its twin, two waiting cards that need it; add --replaces re-points every waiting need in one step and drops the old card raising no blocked judgment, relink repairs a drop and an add made apart): `TwinsInherit` (no waiting card needs a replaced id), `NoBlockedForReplaced`, `NoDanglingNeed` (a waiting card that needs a dropped card has a judgment that says so); **rules** (one card's attempts under the failed and bound rules, tiers flash, pro, heavy, then a friend's card where a mind answers): `RuleAttemptsBounded` (at most Cap attempts a tier on three tiers), `LadderClimbs` (never a lower tier), `Answered` (every failure or bound is answered, under fairness of the rules and the mind); **late** (one late work card, its holder's progress stamps as an outside event, the one wait a generation, the hold of a card whose holder never stamped, the return and redeal of one that stamped and went silent): `WaitOnce`, `NeverStampedNeverReturned` (a card whose holder never stamps is never returned by the late rule), `ProgressIsStamped`, and the reachability witness `MCSprintRulesLateReachReturn` (`NeverReturned` fails: a card is still returned); **reads** (one card's attempts under a reader's broken reads: the finding the fix of a rework, a finding naming a file outside PATHS a twin with PATHS widened and its attempts from the first, a card at its brief's bound a mind's): `ReadAnswersBounded`, `TwinsWiden` (every twin widens PATHS by a file), `ReadAnswered`; **gate** (one base commit gated again and again): `BaseStopsOnThird`; **idle** (an idle fleet each tick, the window, the episode): `AlarmOncePerEpisode`, `ClearFollowsAlarm`; **answers** (one card's attempts under the broken-read, harness-fault and late-report answers: a broken read with a finding and a harness fault or a HOLD with findings reworked with it, a broken read with no finding and a failure no class names a mind's, a deadline's failure racing the worker's late LAND): `AnswerAttemptsBounded`, `ReworkKeepsCount` (every rework is one attempt more), `ReworkCarriesHead` (each attempt starts from the head the one before pushed), `ReworkHasAFix`, `LateReportFinishes` (a report for an attempt the deadline failed, before a later attempt started, finishes it), `AnAnswered`, with five reversed witnesses of their own (a rework with no finding, a rework not counted, a rework from the base, a stale refusal of the late report, a harness rework past the bound). Ten reversed witnesses, each breaking its property: a replace that does not re-point (`TwinsInherit`), one that drops in silence (`NoDanglingNeed`), a failed rule with no cap (`RuleAttemptsBounded`), a bound rule that lowers the tier (`LadderClimbs`), a late rule that waits whenever there is progress (`WaitOnce`), a late rule that returns a card with no progress whether its holder stamped or not (`NeverStampedNeverReturned`), a base gate that stops on its first failure (`BaseStopsOnThird`), an idle alarm every tick of an episode (`AlarmOncePerEpisode`), a read-broken rule that reworks at the brief's bound (`ReadAnswersBounded`), one that twins on a finding inside PATHS (`TwinsWiden`) |
| `CoordinatorPass.tla` | `MCCoordinatorPass*` | the coordinator's pass (docs/SPEC-SPRINT.md section 8, "The coordinator's pass"; internal/sprint coordinator_pass.go): one condition on one subject (a friend's session deaf, a friend holding working cards that finishes none, judgments late on the coordinator) coming and going as an outside event, the tick's pass raising its judgment once an episode, raising it again in place every Every steps of running time while it holds, the coordinator's ack quieting it, and the close when it stops holding: `OneJudgmentAnEpisode`, `PushedEveryWindow`, `ClosedWhenCleared`, `RaisedWhileItHolds`, `PushedOnlyWhileRaised`. Three reversed witnesses: a judgment raised once and never again (`PushedEveryWindow`), a raise again that writes a second judgment (`OneJudgmentAnEpisode`), a judgment left open when its condition clears (`ClosedWhenCleared`); the instance is Every = 2, MaxClock = 6. With Kind = "behind" (`MCCoordinatorPassBehind`, MaxClock = 7) the condition is read off one late judgment: its overdue line is the first reminder and the pass counts it from that line, so it is pushed once a tick at most (`OnePushATick`) and never a whole window without a push (`LateRemindedEveryWindow`); the reversed witness counts it from its deadline, and the line and the pass push in one tick (`OnePushATick`). With Kind = "empty" (`MCCoordinatorPassEmpty`, MaxClock = 7) the condition is an up friend's empty row while cards she could do wait, judged only after the empty-row clock is Every old and the clock closed whenever the conjunction is gone (`EmptyAWholeWindow`); the reversed witness keeps the clock while she is down and judges her on her first tick back. With Kind = "pin" (`MCCoordinatorPassPin`) the deal writes the judgment of a named pin placed on another row and the pass keeps that one note; the reversed witness has the pass write its own beside it (`OneJudgmentAnEpisode`) |
| `ReadsByRoom.tla` | `MCReadsByRoom*` | the ask of nova-sprint (docs/SPEC-SPRINT.md section 6, the reads, sequential and by room; internal/sprint readers.go `ReadsWanted`, `finderFirst`, `askFinders`, `askPicks`): a card's reads asked one at a time, each of a reader with free room under its machine's width, the rework's first read of the reader who found the attempt before broken when it has room; two readers of widths 2 and 1, two pro cards, two attempts each; `WidthRespected`, `OneAtATime`, `OneBroken` (an attempt has at most one broken read, so the finder needs no reader order here; where ask --another leaves two, the engine and the reference model take the first in reader row order), `ReadsPerLanding` (a pro card whose first read finds it broken once lands on three reads, where the pair asked four), `FinderFirst` and the liveness `Settles` hold; five reversed witnesses (the pair asked at once breaks `OneAtATime` and, in their own cases, `ReadsPerLanding` and `OneBroken`; readers asked whatever their room break `WidthRespected`; the rework's first read of any reader breaks `FinderFirst`), each counterexample checked by hand against the ask before the change (the pair of the integration branch, the finder-blind room of 2026-10-03) |
| `WorkImport.tla` | `MCWorkImport*` | nova-work v1 (docs/SPEC-WORK-V1.md section 1): an issue under import (absent, fetched, in the tree, mirrored, closed by the destructive mode, re-opened by export) and a repository's sync, with outside edits at any time; a tree is written only whole, only a verify receipt at GitHub's current version licenses a close, an issue the import closed is held by the tree exactly, an external issue is never closed, export restores; five reversed witnesses |
| `CardContract.tla` | `MCCardContract*` | the card contract's finish (docs/SPEC-CARD-CONTRACT.md): what a launch is staged from, how its child ends, the member's push and the one judgment (internal/member `Judge`): ok only with a commit the member pushed and the result's shape and verdict ok, a reaped launch never reported, a rework staged at the tip of its base branch, carrying the last pushed head of any earlier attempt where it applies and else naming that work to be redone, so no pushed work is lost to it; eleven reversed witnesses (the finish of 2026-09-30 that sent a card with no commit to review, ok without the shape, ok over a refused push, a reaped launch reported, a rework staged from a branch name, a rework staged from the immediately previous attempt only, a rework staged at the old pushed head (nova-tools#5215), a carry that did not apply dropped in silence, a provider failure judged as failed work, a redeal on the route that failed, a redeal past MaxRedeals); a run the provider failed with no result is finished `provider`, redealt (the same attempt, restaged from the same base) within MaxRedeals and retired at the bound with one judgment; the instance is one card |
| `DirtyTick.tla` | `MCDirtyTick*` | the sprint machine's tick as the owner shaped it on 2026-09-30, before it is built: four tables updated in turn (work, readers, merge, fleet), a queue per table as its dirty bit, readers, merge and fleet drained until empty, the work table written only by the one pump at the start of each tick; placements by counter modulo the count, width, one wake at the tick's end; 16 reversed witnesses and three goals for the holes it found (a placement blind to the fleet's status never ends the tick; room read from an undrained fleet queue over-fills a machine; the v2.1 rules write the work table in their own steps). The readers' presence (`MCDirtyTickReader`, `MCDirtyTickReaderAway`, W21, W22) abstracts two things away from the Go code: `ReadsStandOnReadersUp` says a read is held only by a reader that is up at every state, but the code keeps a read already begun on a reader that went away (only a read asked and not begun is taken back), and the model has one read per card on a host while the code has none. The model's `raway` always finds a reader to ask again, but the code with fewer than two readers up takes nothing back and asks none, raising the one judgment `fewer than two readers up` instead. `README-DirtyTick.md` says the rest |
| `Timer.tla` | `MCTimer*` | nova-sprint's timers (docs/SPEC-SPRINT.md, "Timers"; `internal/sprint/timers.go`, `internal/sprint/store/timers.go`): an actor sets a timer with `remind`, the machine's tick reads the store's open timers and raises each one whose due time the clock has reached as one judgment addressed to its actor, closing it in the same step, and a restart loses the machine's memory and keeps the store; a timer is raised at most once, never before its due time, a cancelled timer never fires, and an uncancelled one is raised once the clock passes its due time under fair ticks; three reversed witnesses |
| `RouteIndex.tla` | `MCRouteIndex*` | the deal's route choice (internal/sprint/route.go): each tier's route array taken at a uint64 counter modulo its length, one step a card dealt, a redeal moved past the entries it leaves out, a pinned card moving nothing, a read card drawn from the reader tier's array at the same index; 3 routes x 2 tiers x 7 cards (one a read), three reversed witnesses (a random draw breaks RouteFair, a redeal that does not move past the excluded entry breaks ExcludedNeverDrawn, a read that leaves the index where it is breaks RouteIndexAdvancesOncePerCard) and one reach witness (ReachSkip) |
| `Level.tla` | `MCLevel*` | the fleet's level (internal/sprint `level`, `round.levelTo`), the rebalance at the start of every tick and inside every planner that computes holds: one call's loop over every start of a bounded fleet (every up order, every start of the deal's index, members over their width, a card with a refuser). It ends by two guards, each enough alone: every move lowers the sum of squared backlogs by at least two (`PotentialFalls`), and a moved card is never queued again, so a call makes no more moves than there were ready cards (`MovesBounded`); no card lands on a member that refused it at staging, and an older card moves when the newest is blocked (`SelectionComplete`). Five reversed witnesses: the rule of the wedge of 2026-10-02 (nova-tools#5122) does not end, nor does the gap test removed, a moved card queued again moves twice, the gap tested only on the newest card stops early, the refusers not skipped lands a card on one |
| `Land.tla` | `MCLand*` | `nova-sprint land` (cmd/nova-sprint/land.go): a batch of a stream's merge queue pushed to a remote base, then reported to the store through the merge step, with the outside between them (an accept anywhere in the queue, a return, a rework that replaces a card's head and keeps its id and epoch, another lander, a clear that moves the epoch, the base moving, a crash at any step); a head is <<card, attempt>>, and the check before the push and the push are separate steps, as in the code. It proves: the store records a card landed only at a head the base holds (LandedInBase), a card lands only with every card ahead of it (LandsInOrder), no push for a caller whose epoch the store was not at when read (CallerEpochCheckedBeforePush), no report records at an epoch the lander does not hold (ReportHoldsTheEpoch), and a batch pushed and not reported is recorded by running land again (Recovers, under fairness). It does not claim that no push follows a clear: a clear between the check and the push makes a push for an epoch just left (ReachStalePush reaches it), which nothing records and nothing undoes. Five reversed witnesses (the report before the push, the caller's epoch checked only after the push, a report with no guard, a guard on ids and not heads, a report with no epoch fence) and two reach witnesses (ReachStranded: the lander's own push for the store's epoch left unreported, marked by the ghost lpushed, to which a push for an epoch the store has left adds nothing; ReachStalePush). Recovers is proved only once the outside goes quiet (the instance caps outside events at 4), so it does not cover (a) a lander that crashes between the push and the report on every run, which never records the batch, or (b) a base that moves twice inside every read-to-push window: one rebuild, then the lander gives up (the rejected fact), every run; nothing is pushed or lost, the cards stay queued, and the coordinator resumes the stream and runs land again |
| `LandPass.tla` | `MCLandPass*` | the land pass over several streams (cmd/nova-sprint/landpass.go; the owner, 2026-10-07: the merges in parallel, the merge after serial): each stream's batch cut from the base and gated once in its own worktree, up to a width at once; the green ones landed one at a time in priority order, a batch whose base moved merged again onto the new tip and pushed with no new gate when its files and the files landed since are disjoint, else gated once combined; the oracle of which trees are green enumerated over every tree, so a batch green alone and red combined is a behaviour. It proves `BoundHeld`, `BaseAdvancesGated` (every push is a gated tree or a clean merge of disjoint files onto one), `LandsOnce`, `BaseKeepsLandings` (the merge after never loses a landing), `RefusedStaysMerging` (a collision refusal leaves the batch queued), the action property `RefusalTouchesNoOtherStream`, and under fairness `Lands` (a batch green on every tree lands); six reversed witnesses (no combined gate, the own tip pushed over a moved base, a collision recorded as the conflict fact, a refusal that reverts the base, a disjoint merge's tip recorded as gated, a re-merge of no head reported landed); the next pass's base gate reads the record of gated trees (`CachedIsGreen`: a clean merge of disjoint files is never recorded, since two heads green alone can be red together) and a re-merge may merge fewer heads or none |
| `ServerLanes.tla` | `MCServerLanes*` | the sprint server's line of control and its lanes (cmd/nova-sprint serve.go `serveCtx`, servelanes.go, seriallock.go; docs/SPEC-SPRINT.md section 14, The server; 2026-10-04): the run loop's tick and each batch of writes hold the one line in turn, a friend's beat runs on the beat lane and a read on the read lane (one read at a time), and any caller can go away at any time. It proves a write runs only holding the line (`WriteHoldsTheLine`), a waiting beat can always start whoever holds the line (`BeatNeverWaitsForTheLine`), a read waits for another read alone (`ReadWaitsOnlyForReads`), a verb whose caller went before it started never runs (`GoneNeverRuns`; TLC found the first cut's beat lane breaking it), the lanes never hold the line (`LanesHoldNoLine`), and every batch sent is answered or dropped (`EveryBatchEnds`, under fairness, on the smaller `MCServerLanesLive`). Three reversed witnesses: a beat on the line (the code before the lanes), a read on the line, and a write whose caller went still run (the sync.Mutex of 2026-10-04, which kept every timed-out beat in the line). It does not model what a verb does to the sprint, nor the tick's own length on the line |
| `UpdateApply.tla` | `MCUpdateApply*` | `internal/update` (docs/SPEC-UPDATE.md rules 7, 9, 10, 13, 13a, 24, 25, 26): one invocation of the version inventory: a run reads each entry's installed version and its latest (a read that does not answer is UNKNOWN), takes the verdict of the two reads (EQUAL, STALE, AHEAD, DIFFERENT, UNKNOWN), writes the report's state file with the CHANGED names computed against it and the deliveries it records SENT, and one apply installs the entry a person named, or prints the plan and installs nothing; an install process starts only for the entry the invocation named and never on a verdict, a dry run changes no installed version, a verdict exists only after both reads of its entry in that run, the state file holds only versions a run read to completion, a delivery is recorded SENT only after the bus's SEND OK, CHANGED names exactly the entries whose read differs from the state file, and a report that crashed between the write and the send is delivered by a later run (proved once the competing events go quiet, as `Land.tla` proves `Recovers`); three reversed witnesses |
| `CairnStore.tla` | `MCCairnStore*` | the cairn store (internal/cairn, docs/SPEC-CAIRN.md): session records, atomic entry files as the source of truth, the append-only event log, index and coverage ledger, nested and flat shapes; Open starts or re-starts a session idempotently without touching another session, Append stores exact prose under a stable id with a clock stamp, retries succeed with Duplicate=true and no second entry, conflicting prose is refused and never overwrites, persistence is fsync-durable before reported, publication is separate and implies persistence, every logged entry is indexed or the coverage ledger names the gap, and an entry crashed between entry and log is reported as a gap rather than lost silently; three reversed witnesses (BrokenOverwrite breaks NoOverwrite, BrokenReportBeforeFsync breaks DurableBeforeReported, BrokenPublishedWithoutPersisted breaks PublishedImpliesPersisted) |
| `ConfigApply.tla` | `MCConfigApply*` | `internal/config` apply (docs/SPEC-CONFIG.md, "History" and "Apply"): the store rows and one history row per change, the per-kind revision, Redis's views and `config:decl`'s stamps, the plan and the idempotency key `config:<kind>:<rev>`. Two machines and two friends. It proves a change has its history row, Redis equals the store after a completed apply, an ahead stamp is refused, a machine ceiling precedes a friend placed on it and a friend's removal is last among the ops, the stamp follows the ops, and a second apply of the same revision leaves that one copy; a crashed apply is completed by the next apply once crashes are exhausted and no earlier kind's stamp is ahead. Four reversed witnesses: a stamp before the ops, a friend written before its ceiling, a write with no history row, and an apply that writes over an ahead stamp |

Runners. `tools/tlacheck` (Go, over `internal/tlc` and `internal/tablemodel`) runs the checks. Run it on a bench that has java and, for the replays, redis-server: neither belongs on a working machine. Every run is bounded by `--timeout` (a timeout is a failure, never a green), downloads nothing, and runs TLC in a private copy of the models under `--dir`, so the checkout never gains the error-trace files TLC writes beside a spec. The jar is `--jar`, or the environment variable `TLC_JAR`; java and redis-server are found on PATH, or named with `--java` and `--redis-server`, and the path found is echoed. `tlacheck help` and `tlacheck <verb> -h` say the rest.

| Verb | What it runs |
|---|---|
| `run` | the declared cases of `CASES.tsv` (one group, or a shard), each held to the result the plan declares, under a 110 s budget, writing `RUNS.tsv` (`make tlc`) |
| `groups` | the required groups of the plan, as JSON (`make tlc-groups`); with `--stale`, the groups that hold a case whose record is missing or no longer current |
| `merge` | the `RUNS.tsv` of the group runs, joined in plan order into the committed `tla/RUNS.tsv`; with `--keep`, the current records of the cases no run measured again stay |
| `inputs` | the files a case's TLC run reads and the hash of each, with the case's fingerprint |
| `table` | the table model: contracts, the five findings, the cross-table scope control (`--mode`), 120 s |
| `member` | the member and epoch protocol and its four mutation controls (`--suite`), 120 s |
| `replay` | the execution replay of a `table.lua` against `EpochMemberTable`, 120 s |
| `witnesses` | the table model's findings replayed against a pinned `table.lua` in a disposable Redis |

```sh
go run ./tools/tlacheck groups --root .
go run ./tools/tlacheck inputs --root . --case MCEpochMemberFixedPoint
go run ./tools/tlacheck run --root . --jar /path/to/tla2tools.jar --dir /tmp/tlc-out --group tablefirstcontact
go run ./tools/tlacheck table --root . --jar /path/to/tla2tools.jar --dir /tmp/table-results --mode all
go run ./tools/tlacheck member --root . --jar /path/to/tla2tools.jar --dir /tmp/member-results
go run ./tools/tlacheck replay --root . --jar /path/to/tla2tools.jar --dir /tmp/member-replay --source internal/nsprint/fn/lua/table.lua
git show f77458853af46fdbbafd6881a4b46006431f266f:internal/nsprint/fn/lua/table.lua > /tmp/table-pinned.lua
go run ./tools/tlacheck witnesses /tmp/table-pinned.lua
timeout 120 java -cp /path/to/tla2tools.jar tlc2.TLC -workers 8 -deadlock tla/MCCardMachine.tla
timeout 120 java -cp /path/to/tla2tools.jar tlc2.TLC -workers 2 -deadlock tla/MCFirstConn.tla
timeout 120 java -cp /path/to/tla2tools.jar tlc2.TLC -workers 2 -deadlock -config tla/MCFuseBox.cfg tla/MCFuseBox.tla
```

`-deadlock` on the java commands turns TLC's deadlock check off: these models end in a terminal stutter by design, and the safety and liveness properties are what they check; the bare java commands carry no cap of their own, so they are wrapped in `timeout`. The runners live here and not in the self repo because the self repo is text only.

`RUNS.tsv` holds one record per declared case. Its `input_sha256` is the fingerprint of the inputs that case was measured on, and its `input_files` is how many inputs that is. The record also names the jar (`jar_sha256`), the java version (`java_version`), the platform (`host`), the logical CPUs of the machine that ran TLC (`cpus`) and the TLC workers the case ran with (`workers`); none of them is an input. `host` holds a platform label the tool computes, `<goos>-<goarch>` of the machine that ran TLC (`linux-amd64`), from the closed list `Platforms` in `internal/tlc/records.go` (TLC runs on Linux only); it never holds a machine's name, and no flag or environment variable sets it. `cpus` and `java_version` say what capacity and runtime measured the record (`java_version` is the quoted version of the first line of `java -version` that starts with `openjdk version "` or `java version "`, so a `Picked up JAVA_TOOL_OPTIONS` line before it changes nothing); `merge` refuses records of more than one `jar_sha256` and accepts records that differ in `java_version`, `host` or `cpus`, which are observations of the machine and not the identity of the checker; the class test refuses a `host` cell that is not in the list. A case's inputs are exactly what its TLC run reads:

- its configuration (`MCFoo.cfg`);
- the module `CASES.tsv` names for it, and every module that one `EXTENDS` or `INSTANCE`s, transitively (`EXTENDS A, B`, `INSTANCE M`, `LOCAL INSTANCE M` and `F(x) == INSTANCE M WITH ...` are read from the module text at every depth of nested modules, not from comments or strings). A name with no file under `tla/` must be one of the ten modules the TLC jar bundles (`Bags`, `FiniteSets`, `Integers`, `Naturals`, `Randomization`, `RealTime`, `Reals`, `Sequences`, `TLC`, `Toolbox`), which read nothing from the tree. That list is `standardModules` in `internal/tlc/inputs.go`; it is bookkeeping, so extending it stales nothing. Any other name refuses the case, and the refusal says the two ways out: add the module file under `tla/`, or add the name to that list when the jar bundles it;
- the case's own row of `CASES.tsv`, under the file's header, and no other row;
- the runner's result files, as the binary was built: `outcome.go`, `plan.go`, `run.go` and `suite.go` of `internal/tlc`, which decide how a result is produced and read (the command line, flags, workers and timeouts of a TLC run, the reading of its output into pass or fail, and the reading of a row of `CASES.tsv` into the case a run is judged by: its expected outcome, property and deadlock policy). The package's other non-test files (`cases.go`, `doc.go`, `fingerprint.go`, `inputs.go`, `jar.go`, `records.go`) are bookkeeping and are in no fingerprint; `ResultFiles` and `BookkeepingFiles` in `fingerprint.go` name each file in exactly one list, and a test refuses a file in neither. One bookkeeping file, `inputs.go`, computes the list of files a case reads, so the binary carries it as well and every verb that takes a fingerprint refuses a binary built from another `inputs.go` than the checkout's (`InputListFiles`).

The fingerprint is the SHA-256 over those inputs in path order, each as its path, a NUL, the hex SHA-256 of its bytes and a newline. It holds no timestamp, no host and no absolute path. The jar is not an input: the record names it in `jar_sha256`.

Editing one model therefore stales the records of the cases that read it and no other: a change to a module stales every case whose module extends or instantiates it, a change to a configuration or to a case's own row stales that case, and a change to a result file of the runner stales every case. `tlacheck inputs --case <config>` prints each input with its hash, then the fingerprint and the count (`inputs`, `groups --stale`, `merge` and `run` refuse a binary whose embedded result files or `inputs.go` differ from the ones under `--root`, and say to build `tlacheck` from that tree); a record is current when its two columns equal them and its `module`, `expected` and `property` cells equal the case's row (the row is hashed and the cells are not, so a merge refuses a record whose cells were edited, naming the cell and both values, and `groups --stale` counts its group stale). `TestTLCRecordsCoverCurrentModels` refuses a stale record by naming its case and the files it reads, a configuration with no record, a record with no configuration, and a case whose module or extended modules cannot be found.

### The record machines

A record machine is a Linux bench of the fleet whose machine row says `tla=true` (`nova-config machine list` prints it; `nova-config machine set <m> --tla true --as <actor>`, then `nova-config apply`, makes one). Which benches they are is that list's answer, never this file's: `nova-config machine list | grep ' tla=true'`. On each, the tools play's tla play (`fleet/tools.yml`, the inventory's `tla` group; `--tags tla` runs it alone) holds the TLC jar at `/opt/tla/tla2tools.jar`, owned by the bench's login, and refuses the machine when the jar is missing or its SHA-256 is not the one `tla/tla2tools.sha256` pins (the `jar_sha256` the records name), or when no java runs. The play downloads nothing: a jar is placed by hand, `scp` from a record machine that holds it, then `sha256sum` there, which has to print the pinned hash.

A record refresh is one command from the working machine, in the checkout of the change:

```sh
go build -o /tmp/tlacheck ./tools/tlacheck
runs=$(mktemp -d)
/tmp/tlacheck run --root . --dir "$runs" --bench any
```

`--bench any` reads the machine rows (`nova-config machine list`, in the command's environment: `NOVA_PG_DSN`) and takes the record machine with the lowest load per CPU among those holding the pinned jar; `--bench <machine>` names one, whose rows are not read: the pinned jar at the path is the check. Without `--group` it runs every group `groups --stale` lists. It builds this tree's `tlacheck` for the bench, stages it with the top of `tla/` in `~/tla-runs/tlacheck-*` there, and runs each case of each group as its own bounded run (`--group <g> --shards <n> --shard <i>`, `n` the group's size), under `nice -n 15` with `--workers 2`, each after the bench's 1-minute load falls under `--load-below` (default 0.8 x its logical CPUs; it waits at most `--trough-wait`, default 30m, reading every `--trough-poll`, 15s). Each record is under the 110 s cap; a group's total is not one budget. The records come back under `--dir`, and when every case is as declared they are merged into `tla/RUNS.tsv` with `merge --keep`, so `groups --stale` prints `[]` after it; when one is not, nothing is merged and the logs are under `--dir`. The staged directory is removed when the run ends, and one a dead run left is removed by the next run after a day.

A `models` card names its bench in its JOB.md with this sentence, the card builder filling in the record machine it chose (a row with `tla=true`) and the card's name: "Refresh the TLC records from this checkout with `go build -o /tmp/tlacheck ./tools/tlacheck && /tmp/tlacheck run --root . --dir /tmp/tlc-<card> --bench <machine>`; <machine> is a TLC record machine (its nova-config machine row says tla=true, and its pinned jar is at /opt/tla/tla2tools.jar), so never run TLC on this machine, and commit tla/RUNS.tsv only when the command ends with MERGE OK."

### Refreshing the records after a model edit

The author of a model change refreshes only the records the change staled. Start from the branch with the change rebased on the base branch (`git rebase origin/dev`), including a branch that still holds records in an older column layout: the base branch's `tla/RUNS.tsv` replaces the branch's, and the runs below measure again whatever the edit staled. The commands run as they stand, in this order, from the checkout root.

1. Take the base branch's records, build `tlacheck` from this tree, and ask which groups are stale (the tool refuses a binary built from other runner files than this tree's, and records in another layout than its own, and says so):

```sh
git fetch origin
git checkout origin/dev -- tla/RUNS.tsv
go build -o /tmp/tlacheck ./tools/tlacheck
/tmp/tlacheck groups --root . --stale
```

`[]` means the edit staled nothing: `tla/RUNS.tsv` is already current, and steps 2 and 3 have nothing to do. Otherwise the output lists the groups to run.

2. Run the stale groups on a record machine and merge them, from here: `/tmp/tlacheck run --root . --dir "$runs" --bench any` with `runs=$(mktemp -d)` (above, "The record machines"), which ends with `MERGE OK`. By hand instead, on a Linux bench with java (never on a working machine), with the same tree, run each stale group into a clean directory of its own (a directory that holds an earlier run's records would be merged with them) and join the runs onto the base branch's records. Use the jar the kept records name (`cut -f5 tla/RUNS.tsv | sed 1d | sort -u`): one jar measures the whole file, and `merge` refuses a set of records with more than one. The tool downloads nothing and the records name the jar only by its SHA-256, so the jar comes from you: a `tla2tools.jar` of the TLA+ project (its releases are at github.com/tlaplus/tlaplus), checked by `sha256sum /path/to/tla2tools.jar`, which has to print the hash the records name. When no jar you can obtain has that hash, run every group with the one jar you have and merge without `--keep`, as below:

```sh
runs=$(mktemp -d)
stale=$(/tmp/tlacheck groups --root . --stale | tr -d '[]"' | tr ',' ' ')
for g in $stale; do
  /tmp/tlacheck run --root . --jar /path/to/tla2tools.jar --dir "$runs/$g" --group "$g"
done
test -z "$stale" || /tmp/tlacheck merge --root . --keep tla/RUNS.tsv --out tla/RUNS.tsv "$runs"/*/RUNS.tsv
```

A group that ends over its budget exits 1 and still writes its records (the cases with declared debt are recorded as the failed measurements they are); a case that is not a declared debt and fails is a defect in the model or the plan, and the class test refuses its record.

3. Commit `tla/RUNS.tsv` with the change. `merge --keep` keeps a record of the base branch's file only when its case was not measured again and the record is current; a stale record it cannot carry is named as stale with its group, and a kept record of a case the plan no longer declares is dropped and named.

The lander holds a change to this: a head that edits `tla/*.tla` or `tla/*.cfg` without a current record for each case whose inputs it edits is refused, one line per case naming it and its group (`internal/sprint/land_records.go`; docs/SPEC-SPRINT.md section 7, the run records). A generated card whose PATHS reach `tla/` runs `make tlc` for the stale groups in its own STEP 4 gate and commits the `tla/RUNS.tsv` the merge writes.

To measure every case instead (a new jar, or a runner change that stales everything), run every group and merge without `--keep`:

```sh
runs=$(mktemp -d)
for g in $(cut -f6 tla/CASES.tsv | sed 1d | sort -u); do
  /tmp/tlacheck run --root . --jar /path/to/tla2tools.jar --dir "$runs/$g" --group "$g"
done
/tmp/tlacheck merge --root . --out tla/RUNS.tsv "$runs"/*/RUNS.tsv
```

## Table execution and receipt replay

`tlacheck replay` captures 32 controlled source-API transitions in a
disposable Redis, then replays their committed receipt arguments into a second
fresh store. It checks event counts, revision continuity, metadata, member
changes and affected cells independently against the observed store states.
Initial state, externally advanced epochs, writer epoch reads, refused attempts
and committed events are retained in `trace.json`, along with source/model hashes.

A generated linear TLC harness invokes the corresponding `EpochMemberTable`
actions and requires its state to match every observed record/set/shape/epoch
state. Its positive run retains the model's safety properties. Negative controls
must detect a corrupted observed record link, a receipt member change and a
revision gap. The output includes runnable generated modules/configuration files
and TLC logs. Both Redis servers disable TCP; all transient stores are discarded.

This checks the stated finite traces and abstraction mapping. It is not an
unbounded implementation-refinement proof or a live/day-long trace collector.
The model predeclares records, columns and future empty epoch namespaces;
creation/ID reuse, template removal, arbitrary definitions, raw corruption and
Redis error preflight remain concrete-code functional-test obligations. No-ops
map to unchanged abstract user state; the model does not encode the receipt
ledger, which the replay runner checks separately. The original pinned baseline
runner and its deliberately failing desired-contract gate remain unchanged.

## Shell execution traces

`tools/sessiontrace` captures 16 shell sessions (eight fixed random seeds,
both keep-going settings) in an owned Redis. Each executed line records its
output, refusal status and newly committed receipts. The relay loses selected
write replies after the store answers; the receipt ledger must still contain
exactly one effect. The harness also covers successful reads/writes, logical
refusals, usage errors, overlong lines, quit and EOF without timing assertions.

```sh
go run ./tools/sessiontrace --jar /path/to/tla2tools.jar --out /tmp/session-trace
```

The runner has one 120-second budget for capture, TLC and negative controls.
Go dependencies must already be cached; capture disables module downloads and
automatic toolchain selection.
It retains the trace, source/model/jar hashes, generated modules and TLC logs.
The generated module invokes `TableSession` actions and checks observed line
statuses, final exit, unread input and termination reason. Its input domain is
the captured sequences and their suffixes. Corrupted final exit and line status
must fail TLC; a duplicated durable effect must fail receipt validation.

Receipts witness effects separately because `TableSession` does not model the
ledger or table contents. This bounded replay covers the stated subset; signal
delivery, store outages and watch liveness retain their existing functional
and model controls. It is not a proof over arbitrary shell executions. The
per-tick runtime check remains `watch --check`.

## The edit verbs (TableEdit) and order (TableOrder)

Two bounded safety models of nova-table's edit surface, each an abstraction
with reversed witnesses, neither a refinement proof of table.lua. What each
leaves out is listed in its header. The functional tests hold the rest.

`TableEdit.tla`: `set` (footer, rename, columns), `row add` and `row
hide/show` over many rows, `row set` (text), column hide/show, the cell
verbs, and one outside event (a stored formula column whose fold the library
no longer reads). `Staged = FALSE` is table.lua at 109939a85.

`TableOrder.tla`: `row add`, `row del`, `row move`, `row order`, `row sort`
(once, `--keep`, `--manual`), `bind`, one `set` call that sorts and places,
`col add`, `col del`, `col move`. Every call records what it asked for, and
the invariants say the result is the one requested, not only a permutation.
`Broken` names the misimplementation a witness config turns on.

Run on a bench, never the Studio. Every config runs at once, each in its own
temp directory (TLC unpacks its standard modules into `java.io.tmpdir`, and
two runs sharing one collide), each under a 60 s cap; a timeout is a failure:

    for cfg in MCTableEdit*.cfg MCTableOrder*.cfg; do c=${cfg%.cfg}
      m=MCTableOrder.tla; case $c in MCTableEdit*) m=MCTableEdit.tla;; esac
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -deadlock -metadir /tmp/tlc-$c/meta -config $cfg $m > $c.log 2>&1 &
    done; wait

Measured on space, 2026-09-27, load 9, all thirteen at once: 38 s wall.

| config | result | time |
|---|---|---|
| `MCTableEdit` | no error, 468,243 distinct states, depth 4: TypeOK, RefusalWritesNothing, ShapeLosesNothing, PlacedInShape, TextInTextColumns, HideKeepsData, RenameKeepsData | 37 s |
| `MCTableEditBrokenRefusal` | RefusalWritesNothing violated in 2 states: `row add 1 2`, row 2's key of the wrong type, row 1 left written (109939a85 lines 189-192) | 1 s |
| `MCTableEditBrokenText` | ShapeLosesNoText violated in 4 states: a text value set, `set --columns` without the column deletes it (line 341) | 2 s |
| `MCTableEditBrokenLegacy` | ShapeLosesNoMember violated in 5 states: a member placed, a formula column's stored fold stops parsing, `set --columns` drops the member's column with no OCCUPIED check (line 311) | 8 s |
| `MCTableOrder` | no error, 241,073 distinct states, depth 4, 3 rows, 3 columns: TypeOK, RefusalWritesNothing, RowMoveIsExact, RowOrderIsExact, ColMoveIsExact, AddIsExact, BindIsExact, RowSortIsExact, StandingSortHolds, ReorderIsPermutation, HeldInShape, OnlyRowDelDrops, RowsAndColumnsApart | 19 s |
| `MCTableOrderBrokenBind` | StandingSortHolds violated: bind writes its input order under a standing sort (3ee97bea: bind never reached the standing-sort step; Stella's probe 1) | 2 s |
| `MCTableOrderBrokenCombined` | StandingSortHolds violated: one call sets `--keep` and moves a row (3ee97bea: the guard read the sort before the edit and exempted any call with row_sort; Stella's probe 2) | 2 s |
| `MCTableOrderBrokenOnce` | RowSortIsExact violated: a sort without `--keep` leaves the rows as they were | 2 s |
| `MCTableOrderBrokenSort` | StandingSortHolds violated: row add ignores the standing sort | 2 s |
| `MCTableOrderBrokenPrefix` | RowOrderIsExact violated: the named rows put last | 2 s |
| `MCTableOrderBrokenItem` | RowMoveIsExact violated: `--first` moves another row | 1 s |
| `MCTableOrderBrokenDel` | OnlyRowDelDrops violated: `col del` removes a column that holds a member | 2 s |
| `MCTableOrderBrokenBindLoss` | OnlyRowDelDrops violated: bind drops an omitted row that holds a member | 2 s |

The three witnesses of the edit model and the Bind and Combined witnesses of
the order model are defects that were in the code, each checked by hand
against the lines named. The other six are misimplementations the invariants
are shown to catch. The order model also found one defect by disagreeing with
the code: it refuses a bind that omits a row holding a text value, and the
kernel at 6b3346174 deleted the text (Stella's read, stella-9a49e4eda437); the
kernel was changed to refuse, the model was not. Bounds of the instance: a
combined sort-and-move places at `--first` or `--last`, a combined
sort-and-order names one row. A depth-5 run of the edit model with one member (1,652,467
distinct states, no error) took 92 s on the same bench and is not in the set.
## The file lock (FileLock)

`FileLock.tla`: a lock on a file across processes, the design of the shared
module `internal/filelock`: one holder at a time, and a refusal that tells the
truth. Written from the locks the tools already carry (`internal/bus`, `merge`,
`tokens`, `swarm`, `wake`, `update`) and against the first candidate,
nova-tools#4473 at d653eb53e. A bounded design model with reversed witnesses,
not a refinement proof. What it leaves out is listed in its header.

The rule it stands on is `internal/merge/lock.go`'s: the kernel releases the
lock when its holder dies, so there is nothing to break and no age to compute.
The lock is the kernel's lock on the file and nothing else. What is written in
the file is a note and never what decides. The file is never removed.

What it holds the module to:

- the lock is never handed to two callers (`MutualExclusion`), and who holds it
  holds the file at the path by the kernel's lock (`HolderHoldsThePath`);
- the path names one file for ever (`OneFileForEver`);
- the file names its holder (`HolderIsNamed`);
- "held" is said only of a holder (`HeldIsTrue`): a refused taker asks for a
  shared lock before it says "held", so another taker asking is never taken
  for a holder; a taker that only askers kept out answers "busy";
- the kernel's lock is only ever with a live process that knows it has it, so
  a death leaves nothing for anybody to clear (`NothingToClear`).

Run as the other table models are, every config at once, each in its own temp
directory under a 60 s cap:

    for cfg in MCFileLock*.cfg; do c=${cfg%.cfg}
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -deadlock -metadir /tmp/tlc-$c/meta -config $cfg MCFileLock.tla > $c.log 2>&1 &
    done; wait

Measured on a Linux amd64 bench, 2026-10-02, with Java 21.0.12.1 and
TLC jar SHA-256 `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`.
The seven current-input runs are recorded in `RUNS.tsv` (15:02:10–15:02:17
UTC); the `tlc-filelock-contract.log` bench receipt identifies extracted
source `ef5746abb0d484de8866bcb3b69292669c1e104d` and reports all seven cases
PASS. A broken case passes by producing its expected invariant violation
(exit 12); its count below is distinct explored states, not trace length.
`MCFileLockCandidate` permits either named violation; the receipt does not
identify which one. The 2026-09-27 counts describe the earlier module.

| config | result | distinct states | time |
|---|---|---|---|
| `MCFileLock` | no error (exit 0), three processes, each pid reused twice: TypeOK, MutualExclusion, HolderHoldsThePath, HolderIsNamed, OneFileForEver, HeldIsTrue, NothingToClear | 16,335 | 1.422 s |
| `MCFileLockFour` | no error (exit 0), four processes, each pid reused once: the same seven | 52,128 | 2.062 s |
| `MCFileLockBrokenStale` | expected MutualExclusion violation (exit 12): a holder dies; two processes find the lock stale and both clear it; the first removes the file and takes a new one at the path; the second opens that new file, finds no name in it, and removes it; both then create a file and hold | 2,318 | 0.858 s |
| `MCFileLockBrokenSentinel` | expected NothingToClear violation (exit 12): the holder dies and the lock stays taken | 15 | 0.697 s |
| `MCFileLockCandidate` | expected HeldIsTrue or MutualExclusion violation (exit 12), the candidate's take and stale clear with its probe gone | 162 | 0.730 s |
| `MCFileLockBrokenBusyIsHeld` | expected HeldIsTrue violation (exit 12): a refused taker whose shared ask is granted answers "held" after the holder leaves | 41 | 0.704 s |
| `MCFileLockBrokenUnlink` | expected MutualExclusion violation (exit 12): release removes the file under a taker that has it open | 180 | 0.739 s |

Stale and Sentinel are in code that exists or existed, each checked by hand
against the lines named. The first Stale counterexample TLC gave did not
survive that check (it counted as a holder a process the code makes give up),
so the invariant was tightened to locks handed to a caller, and the trace above
is the one that holds. BusyIsHeld and Unlink are misimplementations the
invariants are shown to catch.

Until 2026-10-02 the model also had a probe (ask who holds the lock without
taking it) and two witnesses on it: ExProbe (a probe taking the exclusive lock
for an instant, the candidate's and `internal/wake`'s) and PidLive (a probe
answering from the pid in the file). They left with the module's `Probe`,
which no caller used; HeldIsTrue stayed, on the refused taker, with BusyIsHeld
as its reversed witness. The same day the telling left too: a take handed the
caller what the file said (empty, the last holder released; a name, it never
did), checked as UncleanIsTold with the reversed witness KeepStamp (release
leaving the name). It left with the module's `FileLock.Previous`, which no
caller used. Release still clears the note, so a free lock names nobody, but
the model no longer checks it: the note is what a refusal reports
(HolderIsNamed) and nothing decides on it. A probe and the telling can each
return with a caller, and their witnesses with them.

## The shell (TableSession)

`TableSession.tla`: `nova-table shell` as a state machine, the design that
nova-tools#4458 is held to. The session owns its input, where its reader is
(at a line, in a verb, in a watch, ended), one connection (none, live, or dead
and not yet used again), the dial error its pool keeps, and its exit code. The
outside is the store (up; refusing; gone from its socket path) and two
signals. A bounded design model with reversed witnesses, not a refinement
proof of session.go. What it leaves out is listed in its header.

The signals: SIGTERM is a stop wherever it
arrives. SIGINT inside a watch is how a watch is left, and the reader goes on
to the next line; at the prompt or inside a verb it is a stop. A session ended
by a stop reports the signal (143, 130), not the codes of its lines.

What it holds the shell to:

- after a stop no new line starts (an in-flight write may complete)
  (`NothingStartsAfterStop`); SIGTERM ends the session (`TermEnds`); only a
  stop ends the session as one (`StopOnlyWhenStopped`), and SIGINT leaves a
  watch (`IntLeavesWatch`);
- a line fails for the connection only when a dial made for that line failed
  (`NoFalseAlarm`);
- a store that could not be reached is code 2, whatever the dial said
  (`ConnectionFailureIsTwo`);
- a write is sent once: when its reply is lost the line ends with code 2 and
  the write is not sent again, because it may have been done (`AtMostOnce`);
- without `--keep-going` nothing is read after the first failed line
  (`StopsAtFirstFailure`, `EndOfInputMeansNoFailure`); with it every line is
  read unless the session was told to end (`KeepGoingReadsEveryLine`);
- the exit code is the highest code of any line, unless a stop ended the
  session (`ExitIsHighest`);
- a verb ends: the session is never stuck inside a line (`VerbEnds`).

The reader waits for input as long as the outside likes, so nothing here says
a session must end: a shell left open at its prompt is not stuck, and a watch
draws until it is told to stop. Fairness is on what the session owes (a verb
in hand, a signal received), never on the arrival of a line.

Run as the edit and order models are, every config at once, each in its own
temp directory under a 60 s cap:

    for cfg in MCTableSession*.cfg; do c=${cfg%.cfg}
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -deadlock -metadir /tmp/tlc-$c/meta -config $cfg MCTableSession.tla > $c.log 2>&1 &
    done; wait

The exact-head run:
all nine configurations passed their expected outcomes in 8 s: the positive
model retained 87,925 states and the stronger cached-error witness failed in
5 states. The strengthened `NoFalseAlarm` rejects a cached error even while
the store remains down, matching the fresh-dial requirement.

At model commit `b0107f910`, the added lost-reply transition and `AtMostOnce`
invariant bring the positive run to 92,331 distinct states, thirteen invariants
and three liveness properties. All ten configurations ran in 9 s; all eight
negative witnesses were caught. These are bench measurements, not a
new run on the reader's machine. Inputs have up to four lines over six kinds
of line, with and without `--keep-going`, from each state of the store.

| config | result |
|---|---|
| `MCTableSession` | no error, 92,331 distinct states: TypeOK, NothingStartsAfterStop, StopOnlyWhenStopped, NoFalseAlarm, ConnectionFailureIsTwo, AtMostOnce, StopsAtFirstFailure, ExitIsHighest, KeepGoingReadsEveryLine, EndOfInputMeansNoFailure, EndsForAReason, LineInHand, LiveMeansUp; TermEnds, IntLeavesWatch and VerbEnds under weak fairness of what the session owes |
| `MCTableSessionBrokenTerm` | NothingStartsAfterStop violated in 5 states: `watch`, SIGTERM, the watch returns 0, the next line `ok` is run (ed959e1a3: watch.go:75, watchLoop, session.go:126) |
| `MCTableSessionBrokenTermLive` | TermEnds violated: `watch`, SIGTERM, the watch returns 0 and the session is still there |
| `MCTableSessionBrokenStale` | NoFalseAlarm violated in 5 states: a line answers the cached error without making a fresh dial, whether or not the store recovered (ed959e1a3: session.go:88, a pool of one; go-redis v9.22.0 pool.go:692) |
| `MCTableSessionBrokenClass` | ConnectionFailureIsTwo violated in 3 states: the store gone from its socket path, the line ends with code 1 (ed959e1a3: main.go:303) |
| `MCTableSessionBrokenLong` | KeepGoingReadsEveryLine violated in 2 states: a line too long ends a `--keep-going` session with a line unread (ed959e1a3: session.go:135) |
| `MCTableSessionBrokenReplay` | AtMostOnce violated in 3 states: a write's reply is lost and the write is sent again (session.go:88 opens with go-redis's command retries; v9.22.0 error.go shouldRetry answers true for io.EOF, giving code 0 and a second receipt) |
| `MCTableSessionBrokenOn` | StopsAtFirstFailure violated: a line is read after a failed one without `--keep-going` |
| `MCTableSessionBrokenLast` | ExitIsHighest violated: `usage` then `ok` exits 0 |
| `MCTableSessionBrokenInt` | StopOnlyWhenStopped violated: SIGINT inside a watch ends the session |

Term, Stale, Class, Long and Replay are defects of the shell at ed959e1a3,
reproduced on a store by a second reader and checked against
the lines named. On, Last and Int are misimplementations the
invariants are shown to catch; the code at ed959e1a3 has none of them. What a
stop does to a verb in flight is left open: the verb may finish, or the
process may end inside it. The model says only that no line starts afterwards.

## The function library loader (RedisFn)

`RedisFn.tla` is the store of internal/redisfn: under each library name one
build or none, a function name held by one library, and loaders that each
carry one build. A loader outside `Missers` runs Ensure, the deployer's load
(a read, then a load when the store does not hold its build, then a second
read to name the holder when the store refuses the load for a function
another library holds); a deployer runs it on every pass. A loader in
`Missers` runs LoadMissing once: a read, and only when the store holds no
build of its library, FUNCTION LOAD without REPLACE, which the store refuses
when a build is there by then. `Atomic = TRUE` is the code, FUNCTION LOAD
REPLACE; `Atomic = FALSE` is FUNCTION DELETE followed by FUNCTION LOAD.
`MissReplaces = TRUE` is a LoadMissing that sends REPLACE, the load of
a miss. What it leaves out is listed in its header.

Run on space, every config at once, each in its own temp directory under a
60 s cap (no `-deadlock`: the terminal stutter is an action of the spec):

    for c in MCRedisFn MCRedisFnDeleteThenLoad MCRedisFnHolderGone MCRedisFnTwoDeployers MCRedisFnOneDeployer MCRedisFnLoadMissing MCRedisFnMissReplaces; do
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -metadir /tmp/tlc-$c/meta -config $c.cfg MCRedisFn.tla > $c.log 2>&1 &
    done; wait

The run (tla2tools v1.7.4, TLC 2.19),
the modules and configs matching these files by sha256, logs in
`space:~/tla/redisfn-2/`. All seven ran in under a second. The distinct
states of a run that stops at a violation are what the two workers had found
by then, and vary from run to run; the length of the counterexample does not.
The first five configs, before `Missers` was added, gave the same outcomes at
`~/tla/redisfn/`.

| config | result |
|---|---|
| `MCRedisFn` | no error, 54 distinct states. The migration: `old` on the store registers f and g, loader a carries the `old` that registers g alone, loader b carries the `new` that registers f, both deploy on every pass. TypeOK, OneHolder, NoGap, RefusalWritesNothing, HolderHeld, Settles, MCMigrated |
| `MCRedisFnDeleteThenLoad` | NoGap violated, a counterexample of 3 states (8 distinct found): a reads, a deletes `old`, and the store holds no `old` until a's second command. The reversed witness for Load being one FUNCTION LOAD REPLACE |
| `MCRedisFnHolderGone` | HolderFound violated, a counterexample of 6 states (44 distinct found): b is refused for f, a loads the `old` that lets f go, b looks for the holder and there is none. The reversed witness for CollisionError's line for a function with no holder (store.go, `CollisionError.Error`) |
| `MCRedisFnTwoDeployers` | Settles violated (48 distinct states), a counterexample of 10 states that goes back to its state 3 for ever: two deployers carry two builds of one library and each replaces the other's on every pass, `store.old` going 1, 2, 1 for ever. Not a witness of a misimplementation: the hazard of two deployers, which is why Ensure is for the one place that deploys and every other caller runs LoadMissing |
| `MCRedisFnOneDeployer` | no error, 14 distinct states: the same two builds, b running Ensure once and a deploying; the library comes to rest at a's build. TypeOK, OneHolder, NoGap, RefusalWritesNothing, HolderHeld, Settles |
| `MCRedisFnLoadMissing` | no error, 25 distinct states: the rivals on a store that starts empty, a deploying and b, the older binary, running LoadMissing once. TypeOK, OneHolder, NoGap, RefusalWritesNothing, HolderHeld, MissNeverReplaces, Settles, MCDeployed (the store comes to rest at the deployer's build) |
| `MCRedisFnMissReplaces` | MissNeverReplaces violated, a counterexample of 5 states (23 distinct found): b reads the name free, a deploys build 1, b's load with REPLACE puts build 2 over it. The reversed witness for LoadMissing's FUNCTION LOAD without REPLACE; the unit test `TestLoadMissingNeverReplacesALibraryTheStoreHolds` holds the same two cases against the code, with Ensure as its own reversed witness |

## nova-table's first contact (TableFirstContact)

`TableFirstContact.tla`: every nova-table verb is an FCALL into the
nova_sprint library, and a store that holds none answers "Function not
found". The model is the store's library (none, an older build that lacks the
verb's function, this build's) and, per process, whether a load reached an
outcome and where each verb is. The outside events are the deployer's load, an
older binary's LoadMissing, a load that fails and a reply lost after the store
ran the command. `Broken` turns on a misimplementation: `resend-lost` sends a
verb whose reply was lost again, `replace` loads with REPLACE, `every-miss`
loads on every miss. What it leaves out is listed in its header; RedisFn.tla
holds LoadMissing's read and load under racing loaders.

Run on space, every config at once, each under a 60 s cap:

    for c in MCTableFirstContactFresh MCTableFirstContact MCTableFirstContactBrokenResendLost MCTableFirstContactBrokenReplace MCTableFirstContactBrokenEveryMiss; do
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -metadir /tmp/tlc-$c/meta -config $c.cfg MCTableFirstContact.tla > $c.log 2>&1 &
    done; wait

Logs in `space:~/tla/firstcontact/`, the modules and configs matching these
files by sha256. Each ran in under a second. The distinct states of a run that
stops at a violation vary from run to run; the counterexample does not.

| config | result |
|---|---|
| `MCTableFirstContactFresh` | no error, 37 distinct states: a fresh store, no outside event, two processes of two verbs. TypeOK, AtMostOnce, SentTwiceOnlyAfterAMiss, NeverReplaced, LoadOnce, FreshStoreWorks (every verb ends ok) |
| `MCTableFirstContact` | no error, 771 distinct states: every outside event. TypeOK, AtMostOnce, SentTwiceOnlyAfterAMiss, NeverReplaced, LoadOnce |
| `MCTableFirstContactBrokenResendLost` | AtMostOnce violated, a counterexample of 5 states: a miss, the load, the verb run with its reply lost, sent again and run twice. The reversed witness for sending again only on "Function not found", which a verb that ran never answers |
| `MCTableFirstContactBrokenReplace` | NeverReplaced violated, a counterexample of 4 states: a miss, the deployer loads this build, the miss's load replaces it. The reversed witness for LoadMissing's FUNCTION LOAD without REPLACE |
| `MCTableFirstContactBrokenEveryMiss` | LoadOnce violated, a counterexample of 7 states: a miss on a store an older binary loads first, the load leaves it (UNCHANGED), the verb is refused; the next verb's miss loads again. The reversed witness for firstContact.ensure's once per process |

## The first connection (FirstConn)

`FirstConn.tla`: the connection `redisconn.Open` dials (internal/redisconn/open.go
at f6ec9e2b8, `firstConn`), as a state machine over the seven events of
`firstconn_test.go`: the store sends a reply that begins `%` (HELLO accepted)
or `-` (refused); the client reads with room to spare, or a few bytes at a
time; the client writes the probe, or another command; Open returns. go-redis
shakes hands inside the first command on a connection, so Open sends a probe
(PING); when, and only when, the first byte from the store was `%`, the probe
is taken and answered here (+PONG) and never written, so Open costs one
exchange and not two. A bounded design model with reversed witnesses, not a
refinement proof of open.go; its header lists what it leaves out.

The rules are the test's, stated on what went in and what came out and not
on the states the code keeps: a write is taken only when it is the probe, the
first byte read was `%`, nothing but the handshake was written before, no
write was taken before and Open has not returned, and then it is taken
(`TakenOnlyWhenDue`, `TakenWhenDue`); every other write reaches the store
whole and in order (`TheRestTravels`); the client reads the store's bytes in
order (`StoreBytesInOrder`), with the answer whole, once, first and alone after
the taken write, and never otherwise (`AnswerStandsInPlace`); inert is for
good (`InertStays`); a taken write is answered, so the client is never left
waiting for it (`AnswerDelivered`, under weak fairness of the client's reads).

Run as the others are, every config at once, each in its own temp directory
under a 60 s cap:

    for cfg in MCFirstConn*.cfg; do c=${cfg%.cfg}
      mkdir -p /tmp/tlc-$c
      timeout 60 java -Djava.io.tmpdir=/tmp/tlc-$c -cp tla2tools.jar tlc2.TLC -workers 2 \
        -deadlock -metadir /tmp/tlc-$c/meta -config $cfg MCFirstConn.tla > $c.log 2>&1 &
    done; wait

The run (load 11 of 32 cores), all
eight configs at once, the positive one with 4 workers: 3 s wall for the set.
The instance: six counted events (sends, writes, Open's return; reads are
uncounted, each consumes what it reads), a reply of three bytes, an answer of
three bytes, a short read of two. The same positive model with eight events
ran to 303,578 distinct states in 10 s on 8 workers, no error; it is not in
the set because of the cap.

| config | result |
|---|---|
| `MCFirstConn` | no error, 30,832 distinct states, depth 14: TypeOK, TakenOnlyWhenDue, TakenWhenDue, TheRestTravels, StoreBytesInOrder, AnswerStandsInPlace, InertStays; AnswerDelivered under weak fairness of the reads |
| `MCFirstConnBrokenRefused` | TakenOnlyWhenDue violated in 4 states: `-` read, the connection armed, the probe taken (open.go:268 without the `%` test) |
| `MCFirstConnBrokenAny` | TakenOnlyWhenDue violated in 4 states: `%` read, a write that is not the probe taken (:282 without bytes.Equal) |
| `MCFirstConnBrokenMisaligned` | TakenOnlyWhenDue violated in 5 states: `%` read, another write travels and leaves the connection armed, the probe after it is taken (:285 missing) |
| `MCFirstConnBrokenTwice` | TakenOnlyWhenDue violated in 6 states: the answer read whole, the connection armed again, a second probe taken (:261 storing armed) |
| `MCFirstConnBrokenShort` | AnswerStandsInPlace violated in 6 states: two of the answer's three bytes read, the connection inert, the store's next bytes read where the third should be (:260 without the count) |
| `MCFirstConnBrokenLate` | TakenOnlyWhenDue violated in 5 states: `%` read, Open returns, the probe written after it is taken (:293 missing) |
| `MCFirstConnBrokenHang` | AnswerDelivered violated: the probe taken, no read is possible, the client waits for an answer that never comes (:257 reading the store) |

None of the seven was a defect of the code at f6ec9e2b8: `firstconn_test.go`
holds the same rules over every order of the seven events up to six and over
long orders. Each is a misimplementation the model is shown to catch. The
Misaligned trace was read against the code by hand: state 3, `%` read,
`Read` at :267-271 swaps watching for armed; state 4, a write that is not
the probe, `Write` at :281-285 finds armed, `bytes.Equal` false, and swaps
armed for inert, which the witness omits; state 5, the probe, the code at
:287 passes it to the store because the connection is inert, and the test's
named order `writeOther, sendAccepted, readAll, writeOther, writeProbe`
(firstconn_test.go:223) says the same: not taken. The Hang trace: states 7
and 8, `%` read and the probe taken, then no read is enabled because the
witness reads the store, which sent nothing; the code at :257-263 reads the
answer from `probeAnswer` and never touches the store while answering.

## The fleet's level (Level)

`Level.tla`: one call of the fleet's level (internal/sprint/steps_work.go
`level`, round.go `levelTo`). On 2026-10-02 at 2:26 PM the call did not end:
the emptiest member had refused the longest queue's newest card at staging, the
card went to a member one below instead, the receiver became the donor and the
card went back, a unit appended per turn under the server's mutex, until the
process held 244 GB (nova-tools#5122). The module's header says what it holds
and what it leaves out; the instances are in `MCLevel.tla`.

The model found one defect beyond the wedge, in PR #5127 as it stood: a card the
call moved was appended to its receiver's queue, so it could be moved again in
the same call. `MCLevelBrokenRequeue` is the call: widths 2, 1 and 3, members up
in the order c, a, b, three ready cards on b, the middle one refused by c; b3
goes b -> a, b2 b -> a, then a is the longest, its newest (b2) is refused by c,
so b3 goes a -> c, and b1 b -> c: four moves for three ready cards. Checked by
hand against the code at 0a79ad873 and by `TestLevelMovesNoCardTwice`, red there:
the second unit of b3 is guarded on b3's place before the first unit, so the plan
the engine writes is not the plan it computed (`fleet: b3.w1: revision 2,
expected 1`). The code no longer queues a moved card again.

Run on a macOS arm64 working machine, not the bench (TLC 2.19, jar SHA-256 `936a2620...`, OpenJDK 27, one worker,
niced, 2026-10-02 ET), the eight cases with `-lncheck final -deadlock`; the bench
records in `RUNS.tsv` are owed (the class test is red for these eight until a
Linux bench run is merged):

| config | result | distinct states | time |
|---|---|---|---|
| `MCLevel` | no error (exit 0), three members, three cards, every up order: TypeOK, NeverOnARefuser, MovesBounded, SelectionComplete, PotentialFalls, Terminates | 649,537 | 57 s |
| `MCLevelFour` | no error (exit 0), the wedge's four members: the same six | 472,115 | 57 s |
| `MCLevelOldRuleNoRequeue` | no error (exit 0): the rule of the wedge with no second move ends, within the ready count, off every refuser | 473,903 | 42 s |
| `MCLevelBrokenOld` | expected Terminates violation (exit 13): c3 goes a -> t -> a for ever | 466,243 | 62 s |
| `MCLevelBrokenNoGap` | expected Terminates violation (exit 13): c2 goes a -> c -> a for ever | 654,916 | 74 s |
| `MCLevelBrokenRequeue` | expected MovesBounded violation (exit 12): four moves for three ready cards, b3 twice | 643,223 | 62 s |
| `MCLevelBrokenNaive` | expected SelectionComplete violation (exit 12): the call returns with c2 able to move | 360,485 | 36 s |
| `MCLevelBrokenNoSkip` | expected NeverOnARefuser violation (exit 12): c3 onto c, its refuser | 360,485 | 37 s |

Every counterexample was read against the code by hand. Old: the wedge's shape
in four members (a 2 above its width with c1 and c3, c3 refused by s; s 0, the
emptiest; t 1, at the mean; u 4 and full): c3 goes a -> t at the mean, then t is
the longest and c3 goes back to a, for ever (levelTo at b7776ca3, the longest
and the refusers avoided, no gap). NoGap: the same shape in three members with
the gap test removed from PR #5127's levelTo. Naive: a holds c2 and c3, c3
refused by c, the emptiest two below; only c3 is tried, and the call returns
with c2 still able to go to c. NoSkip: c3 goes onto c, its refuser (the code
before #5000).
