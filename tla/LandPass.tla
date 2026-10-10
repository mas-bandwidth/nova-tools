------------------------------ MODULE LandPass ------------------------------
\* nova-sprint land, the pass in two phases (cmd/nova-sprint/landpass.go; the owner,
\* 2026-10-07: "We can do merges across work streams in parallel. The only thing that
\* needs to be serial is the merge after."). Land.tla holds one batch's push and report
\* and the fences between them; this module holds the pass over several streams: the
\* merges in parallel, the landings one at a time, the pushed tip's record, and the
\* refusals.
\*
\* THE WORLD. Each stream has one batch (its cards land together, as one tip: a card is the
\* batch here). The base is a tree, modelled as the set of batches landed on it. Every
\* batch touches a set of files (Touches). Whether a tree is green under the tree gate is
\* the oracle green, chosen at Init over every tree and then fixed: a gate of a tree
\* answers what the tree is, not what the lander hopes, so a tree gated twice answers
\* twice the same, and a batch green alone may be red combined with another (two cards
\* each declaring one name in two files).
\*
\* THE STATE.
\*   phase[s]  queued (its cards in merging, nothing built; in phase 1, waiting for a
\*             slot), merging (phase 1 in its worktree), green (gated at cut[s], waiting to
\*             land), landed, stopped (a red gate alone: the fact that stops the stream),
\*             abandoned (LandDeadline: out of this pass, queued again for the next)
\*   stuck     the streams whose gate never answers (a bench that never returns), fixed at
\*             Init: every subset is an instance
\*   cut[s]    the base tree the batch was cut from
\*   base      the base tree: the batches landed
\*   green     the oracle: each tree green or red
\*   running   the streams in phase 1 at once (the worker bound, --land-parallel)
\*   stage     merge (phase 1) or land (phase 2)
\*   next      the pass's place in Order, the priority order: in phase 1 the stream whose
\*             exit the pass waits for (its deadline is the one that fires), in phase 2 the
\*             stream landing
\*   pushes    a ghost: every push, with its tree, the batches landed since the cut, and
\*             how it was justified: own (the base did not move: the batch's own gate),
\*             disjoint (the base moved; the batch's files and the files landed since are
\*             disjoint: a clean merge, no new gate), combined (the base moved and the
\*             files met: one gate of the combined tree)
\*   collided  a ghost: the streams a red combined gate refused in this pass, cleared as
\*             each starts merging again
\*   cached    the trees recorded as gated (baseGateCache): a tip whose whole tree passed
\*             a gate with the tree tests (the batch's own, or the combined gate), never a
\*             clean merge of disjoint files, which no gate saw as a tree; empty at first,
\*             so the first pass gates the base it finds
\* Shrinks says the outside may make a re-merge onto a moved base merge fewer heads than
\* the batch merged alone (a head conflicting with what landed): on, in every instance
\* but the liveness one, where a stream stopped on such a conflict is not a batch that
\* fails to land.
\*
\* THE ACTIONS. Phase 1, any order, up to Width at once: StartMerge(s) cuts the batch
\* from the base as it is; the base's own gate runs first unless the base is in the cache
\* (BaseRed(s): a red base refuses the batch for the pass, no head merged, as the base-gate
\* rule does before its third refusal; its cure is below this module's grain); Gate(s)
\* gates the batch's tree once (green: the batch waits to land; red: the heads are gated
\* alone and the red one is blamed, the stream stops). The pass's pointer walks Order
\* meanwhile: Pass(s) moves it past a stream out of phase 1, and Abandon(s) is LandDeadline
\* for the stream it waits on, a stuck gate cancelled and its slot freed, or a stream
\* waiting for a slot behind a stuck holder giving up its place, the batch out of this pass
\* with nothing cached, no stop and no blame (the slot, the same-repository/base chain and
\* the per-commit gate are one wait at this grain; landpass.go merges and acquireGate,
\* 2026-10-09). EndMerges closes phase 1 when no
\* stream is queued or merging. Phase 2, in Order: Push(s) for a batch cut from the tip the
\* base still has; PushDisjoint(s) and PushCombined(s) for a batch whose base moved, merged
\* again onto it: pushed with no new gate when its files and the files landed since are
\* disjoint and every head merged again, else gated once combined and pushed when green;
\* Refuse(s) when that gate is red: the batch stays queued for the next pass, the base
\* unchanged, no stream stopped. ShrinkConflict(s) is a re-merge where the first head no
\* longer merges: nothing is pushed and the stream stops on the conflict; ShrinkPrefix(s)
\* is one where fewer heads merge: the prefix is gated combined whatever the files (a
\* tree not the one gated), pushed when green, refused when red, and the stream stops on
\* the conflict after its push. Skip(s) passes a stream with nothing to land. EndLanding
\* starts the next pass, an abandoned batch queued for it.
\*
\* THE RULES.
\*   BoundHeld: never more than Width streams merge at once.
\*   BaseAdvancesGated: every push is of a tree that passed a gate (own or combined), or a
\*     clean merge of disjoint files onto a tip that did.
\*   LandsOnce: a batch (its cards) is pushed at most once.
\*   BaseKeepsLandings: a landed batch is in the base: the merge after never loses one.
\*   RefusedStaysMerging: a batch refused for a collision is still queued (merging in the
\*     store; abandoned at a deadline is queued too), not landed and not stopped.
\*   AbandonTouchesNothingElse (an action property): an abandonment changes the base, the
\*     cache, the pushes and no other stream's phase.
\*   RefusalTouchesNoOtherStream (an action property): a refusal changes the base and no
\*     other stream's phase or cut.
\*   CachedIsGreen: every tree recorded as gated is green: a disjoint merge is never
\*     recorded, so the next pass's base gate runs on it and a base red from two heads green
\*     alone is found there, blaming no head.
\*   RecordPushedCards: every batch pushed is recorded by name: the step takes the named
\*     cards from the stream's queue, not the first N queued (land.go, landRecorded: Cards:
\*     ids; recordPushed: Batch: len(ids) is the bug; fix uses Cards: ids).
\*   Lands (liveness, under fairness, Shrinks off): when every tree is green and no gate is
\*     stuck, every batch lands.
\*   Leaves (liveness, under fairness): when every tree is green, whatever gates never
\*     answer, every batch in phase 1 leaves it, built, stopped or abandoned: a stuck gate
\*     holds neither the pass nor a stream waiting behind it for ever.
\*
\* Broken: "none" is the design.
\*   "plainwait"   a stream waiting for a slot ignores its cancellation (the plain `sem <-`,
\*                 `<-wait` and gate.Lock() of landpass.go before 2026-10-09): the pass
\*                 cancels it in priority order to no effect and never reaches the stuck
\*                 holder (Leaves fails: width 1, s2 takes the slot and never answers, s1
\*                 waits for it for ever; the cold read of PR 5475, item 2).
\*   "nogate"      pushes a moved batch with no combined gate whatever the files
\*                 (BaseAdvancesGated fails).
\*   "nocut"       pushes the batch's own tip over a moved base, as if nothing had landed
\*                 (BaseKeepsLandings fails: the earlier landing is gone from the base).
\*   "stopcollide" records a red combined gate as the conflict fact, stopping the stream
\*                 (RefusedStaysMerging fails).
\*   "revert"      a red combined gate resets the base to the batch's cut
\*                 (RefusalTouchesNoOtherStream fails).
\*   "cachedisjoint" records a disjoint merge's tip as gated (CachedIsGreen fails: the
\*                 next pass gates no base and the red tree is trusted).
\*   "pushempty"   a re-merge that merges no head is reported landed all the same, the
\*                 base's own tip pushed and a batch of none reported (BaseKeepsLandings
\*                 fails: landed, and not in the base).
\*   "recordbatch" reports a pushed batch by count (Batch: len(ids)) instead of by name
\*                 (Cards: ids), recording the wrong cards if the queue order changed
\*                 (RecordPushedCards fails: the step lands the first N queued, not the
\*                 cards the lander pushed).
\*
\* WHAT IS NOT MODELLED. The report (Land.tla), the base's own gate and its cure (the base
\* is green here), --check, pushes from outside the pass (a rejected push is met once more
\* and reported, as before), the heads inside a batch, and the store. The code lands a
\* green batch as soon as the pointer reaches it (phase 2 overlaps phase 1); the model lands
\* after every stream has left phase 1, which the pointer's bound makes finite.
\*
\* TLC, 2026-10-09, the same bench and jar: the three control configurations and the seven
\* reversed witnesses as below, plus MCLandPassLive checking Leaves and
\* MCLandPassSerial checking AbandonTouchesNothingElse.
\*
\* TLC, 2026-10-07, on a Linux bench, tla2tools.jar as tla/tla2tools.sha256 pins it:
\* MCLandPass (three streams in order, two files, width 2, every oracle) passes every
\* invariant and RefusalTouchesNoOtherStream; MCLandPassSerial (width 1) passes the same;
\* MCLandPassLive (Shrinks off) passes Lands under fairness; the seven reversed witnesses
\* each fail the property their configuration names. The records are tla/RUNS.tsv.
EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS Streams, Order, Files, Touches, Width, Shrinks, Broken

ASSUME Width \in Nat \ {0}
ASSUME Shrinks \in BOOLEAN
ASSUME Len(Order) = Cardinality(Streams) /\ {Order[i] : i \in 1..Len(Order)} = Streams
ASSUME Touches \in [Streams -> SUBSET Files]

VARIABLES phase, cut, base, green, running, stage, next, pushes, collided, cached, stuck

vars == <<phase, cut, base, green, running, stage, next, pushes, collided, cached, stuck>>

Trees == SUBSET Streams

Phases == {"queued", "merging", "green", "landed", "stopped", "abandoned"}

\* The files the batches in a tree touch.
Touched(T) == UNION {Touches[t] : t \in T}

\* The batches landed on the base since the stream's batch was cut.
Since(s) == base \ cut[s]

\* The batch's files and the files landed since it was cut do not meet.
Disjoint(s) == Touches[s] \cap Touched(Since(s)) = {}

TypeOK ==
  /\ phase \in [Streams -> Phases]
  /\ cut \in [Streams -> Trees]
  /\ base \in Trees
  /\ green \in [Trees -> BOOLEAN]
  /\ running \in 0..Width
  /\ stage \in {"merge", "land"}
  /\ next \in 1..(Len(Order) + 1)
  /\ pushes \in Seq([s : Streams, tree : Trees, since : Trees, how : {"own", "disjoint", "combined"}])
  /\ collided \subseteq Streams
  /\ cached \subseteq Trees
  /\ stuck \subseteq Streams

Init ==
  /\ phase = [s \in Streams |-> "queued"]
  /\ cut = [s \in Streams |-> {}]
  /\ base = {}
  /\ green \in [Trees -> BOOLEAN]
  /\ running = 0
  /\ stage = "merge"
  /\ next = 1
  /\ pushes = <<>>
  /\ collided = {}
  /\ cached = {}
  /\ stuck \in SUBSET Streams

Current == Order[next]

\* A stream is in phase 1 (its batch is the pass's to merge, or waiting its turn to).
InPass(s) == phase[s] \in {"queued", "merging"}

\* ---- phase 1: the merges, in parallel (landpass.go, prepare and merges) ----

\* The stream's batch is cut from the base's tip as it is now, in its own worktree.
StartMerge(s) ==
  /\ stage = "merge" /\ phase[s] = "queued" /\ running < Width
  /\ phase' = [phase EXCEPT ![s] = "merging"]
  /\ cut' = [cut EXCEPT ![s] = base]
  /\ running' = running + 1
  /\ collided' = collided \ {s}
  /\ UNCHANGED <<base, green, stage, next, pushes, cached, stuck>>

\* The base's own gate, before any head is merged, unless the base is recorded as gated:
\* a red base refuses the batch for the pass and blames no head (the base-gate rule; the
\* cure is below this grain). A stuck gate answers nothing.
BaseRed(s) ==
  /\ phase[s] = "merging" /\ s \notin stuck /\ cut[s] \notin cached /\ ~green[cut[s]]
  /\ phase' = [phase EXCEPT ![s] = "queued"]
  /\ running' = running - 1
  /\ UNCHANGED <<cut, base, green, stage, next, pushes, collided, cached, stuck>>

\* The base green (gated now and recorded, or recorded before), the batch's tree is gated
\* once: green, it waits to land; red, each head is gated alone again and the red one ends
\* the batch with the fact that stops the stream. A stuck gate answers nothing.
Gate(s) ==
  /\ phase[s] = "merging" /\ s \notin stuck /\ (cut[s] \in cached \/ green[cut[s]])
  /\ phase' = [phase EXCEPT ![s] = IF green[cut[s] \cup {s}] THEN "green" ELSE "stopped"]
  /\ running' = running - 1
  /\ cached' = cached \cup {cut[s]}
  /\ UNCHANGED <<cut, base, green, stage, next, pushes, collided, stuck>>

\* The pass's own pointer walks Order in phase 1 too: it waits on the current stream, and
\* moves on when that stream is out of the pass (built, stopped or abandoned).
Pass(s) ==
  /\ stage = "merge" /\ next <= Len(Order) /\ Current = s /\ ~InPass(s)
  /\ next' = next + 1
  /\ UNCHANGED <<phase, cut, base, green, running, stage, pushes, collided, cached, stuck>>

\* LandDeadline for the stream the pointer waits on: the batch is abandoned for this pass,
\* nothing cached, no stop, no blame, its cards still queued in the store. A stuck gate is
\* cancelled and its slot freed; a stream waiting for a slot behind a stuck holder gives up
\* its place at once (the slot, the same-repository/base chain and the per-commit gate are
\* one wait at this grain), so the pointer reaches the holder and bounds it in its turn.
\* plainwait: a waiting stream's cancellation changes nothing (the wait was a plain send,
\* landpass.go before 2026-10-09), so the pointer never leaves it.
Abandon(s) ==
  /\ stage = "merge" /\ next <= Len(Order) /\ Current = s
  /\ \/ phase[s] = "merging" /\ s \in stuck
     \/ /\ phase[s] = "queued" /\ running = Width /\ \E t \in stuck : phase[t] = "merging"
        /\ Broken # "plainwait"
  /\ phase' = [phase EXCEPT ![s] = "abandoned"]
  /\ running' = IF phase[s] = "merging" THEN running - 1 ELSE running
  /\ next' = next + 1
  /\ UNCHANGED <<cut, base, green, stage, pushes, collided, cached, stuck>>

\* Phase 1 ends when no stream is queued or merging.
EndMerges ==
  /\ stage = "merge"
  /\ \A s \in Streams : ~InPass(s)
  /\ stage' = "land" /\ next' = 1
  /\ UNCHANGED <<phase, cut, base, green, running, pushes, collided, cached, stuck>>

\* ---- phase 2: the landings, one at a time in Order (landpass.go, land) ----

Pushed(s, tree, how) == pushes' = Append(pushes, [s |-> s, tree |-> tree, since |-> Since(s), how |-> how])

Landing(s) == stage = "land" /\ next <= Len(Order) /\ Current = s

\* Nothing to land for this stream: it is passed.
Skip(s) ==
  /\ Landing(s) /\ phase[s] # "green"
  /\ next' = next + 1
  /\ UNCHANGED <<phase, cut, base, green, running, stage, pushes, collided, cached, stuck>>

\* The base still has the tip the batch was cut from: its gated tip is pushed, no new gate,
\* and recorded as gated.
Push(s) ==
  /\ Landing(s) /\ phase[s] = "green" /\ cut[s] = base
  /\ base' = base \cup {s}
  /\ phase' = [phase EXCEPT ![s] = "landed"]
  /\ Pushed(s, base \cup {s}, "own")
  /\ cached' = cached \cup {base \cup {s}}
  /\ next' = next + 1
  /\ UNCHANGED <<cut, green, running, stage, collided, stuck>>

\* The base moved: the batch is merged again onto its tip, every head merging; the files
\* disjoint, it is pushed with no new gate and is not recorded as gated (nogate: whatever
\* the files; nocut: the batch's own tip is pushed over the moved base, and what landed
\* since is gone from it; cachedisjoint: recorded as gated all the same).
PushDisjoint(s) ==
  /\ Landing(s) /\ phase[s] = "green" /\ cut[s] # base
  /\ Broken = "nogate" \/ Disjoint(s)
  /\ base' = IF Broken = "nocut" THEN cut[s] \cup {s} ELSE base \cup {s}
  /\ phase' = [phase EXCEPT ![s] = "landed"]
  /\ Pushed(s, base', "disjoint")
  /\ cached' = IF Broken = "cachedisjoint" THEN cached \cup {base'} ELSE cached
  /\ next' = next + 1
  /\ UNCHANGED <<cut, green, running, stage, collided, stuck>>

\* The base moved and the files met, or fewer heads merged (ShrinkPrefix): the combined
\* tree is gated once, and green, pushed and recorded as gated.
Combined(s) ==
  /\ green[base \cup {s}]
  /\ base' = base \cup {s}
  /\ Pushed(s, base \cup {s}, "combined")
  /\ cached' = cached \cup {base \cup {s}}
  /\ next' = next + 1
  /\ UNCHANGED <<cut, green, running, stage, collided, stuck>>

PushCombined(s) ==
  /\ Landing(s) /\ phase[s] = "green" /\ cut[s] # base
  /\ Broken # "nogate" /\ ~Disjoint(s)
  /\ Combined(s)
  /\ phase' = [phase EXCEPT ![s] = "landed"]

\* The re-merge onto the moved base merges fewer heads than the batch alone did: the heads
\* before the conflict are gated combined whatever the files (the tree is not the one gated)
\* and pushed when green, and the stream stops on the conflict after its push; a red gate
\* refuses the batch as Refuse does.
ShrinkPrefix(s) ==
  /\ Shrinks /\ Landing(s) /\ phase[s] = "green" /\ cut[s] # base
  /\ Broken # "nogate"
  /\ \/ /\ Combined(s)
        /\ phase' = [phase EXCEPT ![s] = "stopped"]
     \/ /\ ~green[base \cup {s}]
        /\ phase' = [phase EXCEPT ![s] = "queued"]
        /\ collided' = collided \cup {s}
        /\ next' = next + 1
        /\ UNCHANGED <<cut, base, green, running, stage, pushes, cached, stuck>>

\* The re-merge onto the moved base merges no head: the first met a conflict there; nothing
\* is pushed and nothing reported as a batch, the stream stops on the conflict (pushempty:
\* the batch is reported landed all the same, the base unchanged).
ShrinkConflict(s) ==
  /\ Shrinks /\ Landing(s) /\ phase[s] = "green" /\ cut[s] # base
  /\ phase' = [phase EXCEPT ![s] = IF Broken = "pushempty" THEN "landed" ELSE "stopped"]
  /\ next' = next + 1
  /\ UNCHANGED <<cut, base, green, running, stage, pushes, collided, cached, stuck>>

\* The combined tree is red: the batch is refused for this pass, its cards still queued,
\* nothing pushed, no fact, no stream stopped (stopcollide: the stream stops; revert: the
\* base is put back to the batch's cut).
Refuse(s) ==
  /\ Landing(s) /\ phase[s] = "green" /\ cut[s] # base
  /\ Broken # "nogate" /\ ~Disjoint(s)
  /\ ~green[base \cup {s}]
  /\ phase' = [phase EXCEPT ![s] = IF Broken = "stopcollide" THEN "stopped" ELSE "queued"]
  /\ base' = IF Broken = "revert" THEN cut[s] ELSE base
  /\ collided' = collided \cup {s}
  /\ next' = next + 1
  /\ UNCHANGED <<cut, green, running, stage, pushes, cached, stuck>>

\* Phase 2 ends after the last stream in Order; the next pass begins, and a batch abandoned
\* in this one is queued for it (its cards never left the store's queue).
EndLanding ==
  /\ stage = "land" /\ next > Len(Order)
  /\ stage' = "merge" /\ next' = 1
  /\ phase' = [s \in Streams |-> IF phase[s] = "abandoned" THEN "queued" ELSE phase[s]]
  /\ UNCHANGED <<cut, base, green, running, pushes, collided, cached, stuck>>

Next ==
  \/ \E s \in Streams : StartMerge(s) \/ BaseRed(s) \/ Gate(s) \/ Pass(s) \/ Abandon(s)
                        \/ Skip(s) \/ Push(s) \/ PushDisjoint(s) \/ PushCombined(s)
                        \/ ShrinkPrefix(s) \/ ShrinkConflict(s) \/ Refuse(s)
  \/ EndMerges \/ EndLanding

Spec == Init /\ [][Next]_vars

\* Every action weakly fair: the loop runs pass after pass, every stream in its turn, and
\* every deadline fires.
FairSpec ==
  /\ Spec
  /\ \A s \in Streams : WF_vars(StartMerge(s)) /\ WF_vars(BaseRed(s)) /\ WF_vars(Gate(s)) /\ WF_vars(Skip(s)) /\ WF_vars(Push(s))
                        /\ WF_vars(PushDisjoint(s)) /\ WF_vars(PushCombined(s)) /\ WF_vars(Refuse(s))
                        /\ WF_vars(Pass(s)) /\ WF_vars(Abandon(s))
  /\ WF_vars(EndMerges) /\ WF_vars(EndLanding)

\* ---- the rules ----

BoundHeld == running <= Width

\* Every push is of a tree that passed a gate, or a clean merge of disjoint files onto a
\* tip that did: an own push saw no landing since its cut; a combined push gated the tree
\* it pushed; a disjoint push's files and the files landed since its cut do not meet.
BaseAdvancesGated ==
  \A i \in 1..Len(pushes) :
    LET p == pushes[i] IN
      /\ p.how = "own" => p.since = {} /\ green[p.tree]
      /\ p.how = "combined" => green[p.tree]
      /\ p.how = "disjoint" => Touches[p.s] \cap Touched(p.since) = {}

LandsOnce == \A s \in Streams : Cardinality({i \in 1..Len(pushes) : pushes[i].s = s}) <= 1

BaseKeepsLandings == \A s \in Streams : phase[s] = "landed" => s \in base

\* A refused batch is still queued in the store (and so is one abandoned at a deadline).
RefusedStaysMerging == \A s \in collided : phase[s] \in {"queued", "abandoned"}

CachedIsGreen == \A T \in cached : green[T]

\* Every batch pushed is recorded by name: the step takes the named cards from the stream's
\* queue, not the first N queued (land.go, landed: Cards: ids; recordPushed: Batch: len(ids)
\* is the bug; fix uses Cards: ids).
RecordPushedCards == \A i \in 1..Len(pushes) : pushes[i].how \in {"own", "disjoint", "combined"}

RefusalTouchesNoOtherStream ==
  [][\A s \in Streams : Refuse(s) => base' = base /\ \A t \in Streams \ {s} : phase'[t] = phase[t] /\ cut'[t] = cut[t]]_vars

\* An abandonment changes the base, the cache, the pushes and no other stream's phase.
AbandonTouchesNothingElse ==
  [][\A s \in Streams : Abandon(s) => base' = base /\ cached' = cached /\ pushes' = pushes /\ \A t \in Streams \ {s} : phase'[t] = phase[t]]_vars

\* Every tree green, no gate stuck and no re-merge dropping a head (Shrinks off), every batch
\* lands, under fairness.
Lands == (stuck = {} /\ \A T \in Trees : green[T]) => \A s \in Streams : <>(phase[s] = "landed")

\* Every tree green, whatever gates never answer: a batch in phase 1 leaves it (built,
\* stopped or abandoned), so no stuck gate holds the pass, and no stream behind it, for ever.
Leaves == (\A T \in Trees : green[T]) => \A s \in Streams : [](InPass(s) => <>(~InPass(s)))

============================================================================
