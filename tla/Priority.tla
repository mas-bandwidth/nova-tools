---------------------------- MODULE Priority ----------------------------
EXTENDS Naturals, FiniteSets
\* docs/SPEC-SPRINT.md section 1, "Priority": the ladder with eviction
\* (internal/sprint/priority.go ladderOrder, priority_evict.go blockerEvictions,
\* steps_tick.go TickDeal, steps_work.go takeOne). One row of Lanes lanes, the
\* cards each at a level of the ladder (0 is blocker; the greater the number the
\* lower the level), a clock in ticks. A ready card is dealt into a free lane,
\* a blocker before any other (ladderOrder). A ready blocker with no free lane
\* evicts one running card: the lowest level first, then among equals the one
\* running the shortest (started latest), never a blocker; the evicted card goes
\* back to ready at its level, marked by the blocker that evicted it, and the
\* blocker takes its lane. With every lane holding a blocker nothing is evicted
\* and the blocker waits (the tick's one judgment). The code spreads an eviction
\* and the deal that follows over two consecutive ticks (the eviction is due
\* work, so the next tick runs at once); the model takes them as one step, and
\* the clock advances only when nothing is due, as the tick does everything due
\* before the next. The model checks who is evicted and that a blocker runs
\* within a tick; not the stamps, the branches or the friends' rows.
CONSTANTS Cards, Lanes, MaxLevel, MaxTime, BadEvictsBlocker, BadPicksWrong, BadNoEvict
Levels == 0..MaxLevel
Blocker == 0
None == "none"
VARIABLES col, level, started, now, evictedBy, waited, lastPick
vars == <<col, level, started, now, evictedBy, waited, lastPick>>

Running == {c \in Cards : col[c] = "running"}
Ready == {c \in Cards : col[c] = "ready"}
ReadyBlockers == {c \in Ready : level[c] = Blocker}
LaneFree == Cardinality(Running) < Lanes
\* the running cards a blocker may evict: never a blocker (unless the model is broken)
Evictable == IF BadEvictsBlocker THEN Running ELSE {c \in Running : level[c] # Blocker}
\* the lowest level among them, then the shortest running: the latest start
Lowest == {c \in Evictable : \A d \in Evictable : level[c] >= level[d]}
Shortest == {c \in Lowest : \A d \in Lowest : started[c] >= started[d]}

TypeOK == /\ col \in [Cards -> {"new", "ready", "running", "done"}]
          /\ level \in [Cards -> Levels]
          /\ started \in [Cards -> 0..MaxTime]
          /\ now \in 0..MaxTime
          /\ evictedBy \in [Cards -> Cards \cup {None}]
          /\ waited \in [Cards -> 0..1]

Init == /\ col = [c \in Cards |-> "new"]
        /\ level \in [Cards -> Levels]
        /\ started = [c \in Cards |-> 0]
        /\ now = 0
        /\ evictedBy = [c \in Cards |-> None]
        /\ waited = [c \in Cards |-> 0]
        /\ lastPick = None

\* a card is admitted (add), or an evicted one is offered again: it is ready
Arrive(c) == /\ col[c] = "new"
             /\ col' = [col EXCEPT ![c] = "ready"]
             /\ UNCHANGED <<level, started, now, evictedBy, waited, lastPick>>

\* the deal into a free lane: a ready blocker goes before any other card (ladderOrder,
\* friendDealByLadder, takeOne)
Deal(c) == /\ col[c] = "ready" /\ LaneFree
           /\ (level[c] = Blocker \/ ReadyBlockers = {})
           /\ col' = [col EXCEPT ![c] = "running"]
           /\ started' = [started EXCEPT ![c] = now]
           /\ waited' = [waited EXCEPT ![c] = 0]
           /\ UNCHANGED <<level, now, evictedBy, lastPick>>

\* a ready blocker with no free lane evicts one running card and takes its lane
\* (blockerEvictions, evictionPick); the evicted card is ready again at its level,
\* marked by the blocker
Evict(b, v) == /\ ~BadNoEvict
               /\ b \in ReadyBlockers /\ ~LaneFree
               /\ v \in Evictable
               /\ (BadPicksWrong \/ v \in Shortest)
               /\ col' = [col EXCEPT ![b] = "running", ![v] = "ready"]
               /\ started' = [started EXCEPT ![b] = now]
               /\ evictedBy' = [evictedBy EXCEPT ![v] = b]
               /\ waited' = [waited EXCEPT ![b] = 0]
               /\ lastPick' = v
               /\ UNCHANGED <<level, now>>

\* a running card finishes: its lane frees
Finish(c) == /\ col[c] = "running"
             /\ col' = [col EXCEPT ![c] = "done"]
             /\ UNCHANGED <<level, started, now, evictedBy, waited, lastPick>>

\* nothing more is due this tick (no deal, no eviction): the clock advances, and a
\* blocker left ready though a lane was free or held a card below blocker has waited a
\* tick it should not have
Due == \/ \E c \in Cards : ENABLED Deal(c)
       \/ \E b, v \in Cards : ENABLED Evict(b, v)
CouldRun == LaneFree \/ \E c \in Running : level[c] # Blocker
Tick == /\ now < MaxTime /\ ~Due
        /\ now' = now + 1
        /\ waited' = [c \in Cards |-> IF c \in ReadyBlockers /\ CouldRun THEN 1 ELSE waited[c]]
        /\ UNCHANGED <<col, level, started, evictedBy, lastPick>>

Next == \/ \E c \in Cards : Arrive(c) \/ Deal(c) \/ Finish(c)
        \/ \E b, v \in Cards : Evict(b, v)
        \/ Tick

\* the tick is fair, and so are its deal and its eviction: what is due is done
Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)
        /\ WF_vars(\E c \in Cards : Deal(c)) /\ WF_vars(\E b, v \in Cards : Evict(b, v))

\* a blocker is never evicted
NoBlockerEvicted == \A c \in Cards : evictedBy[c] # None => level[c] # Blocker

\* the card evicted was the lowest level running, then the shortest running: at the
\* eviction's step the pick is in Shortest as it stood before the step
EvictedIsLowestThenShortest == [][\A b, v \in Cards : Evict(b, v) => v \in Shortest]_vars

\* a blocker ready when a lane of its row is free or holds a card below blocker runs
\* within one tick: no tick ends with such a blocker still ready (waited marks one)
ABlockerReadyWithARowOfItsTierIsRunningWithinOneTick == \A b \in Cards : waited[b] = 0

\* an evicted card is ready again at its own level, never dropped
EvictedIsReadyAtItsLevel == \A c \in Cards : evictedBy[c] # None /\ lastPick = c /\ col[c] = "ready" => level[c] # Blocker

\* liveness, bounded: a ready blocker is running or done by the time the clock ends
BlockersRun == <>[](now = MaxTime => \A b \in Cards : level[b] = Blocker /\ col[b] # "new" => col[b] \in {"running", "done"} \/ \A c \in Running : level[c] = Blocker)
=============================================================================
