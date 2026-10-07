---------------------------- MODULE Priority ----------------------------
EXTENDS Naturals, FiniteSets
\* docs/SPEC-SPRINT.md section 1, "Priority": the ladder with eviction
\* (internal/sprint/priority.go ladderOrder, queueOrder, SetPriority;
\* priority_evict.go blockerEvictions; steps_tick.go TickDeal; steps_work.go
\* takeOne). One row: its lanes (Lanes of them, each two half slots: a work
\* card holds two, a read card one, read_cards.go halfLoad) and its ready
\* queue behind them (as many half slots again, DealAhead). The cards are
\* work or read, each at a level of the ladder (0 is blocker; the greater the
\* number the lower the level; a read's is the reader level, never set); a
\* work card's level changes by the verb (SetLevel), and a card already dealt
\* is re-levelled in place. The deal puts a ready card into the queue while
\* the queue has room, a blocker first; the take moves a queued card into a
\* lane while a lane has room, a blocker first. A blocker that is ready with
\* no queue room, or queued with no lane room, evicts one running work card:
\* the lowest level first, then the shortest running (started latest), never a
\* blocker, never a read; the evicted card goes back to ready at its level,
\* marked by the blocker, and the blocker takes its lane in the same step.
\* With nothing to evict (every lane a blocker or a read) the blocker is
\* judged (NBlockerWaits). The clock advances only when nothing is due, as
\* the tick does everything due before the next. The Bad constants reverse
\* one rule each (the cold read of nova-tools#5405): BadReadyBlind evicts for
\* ready blockers alone, never a queued one (item 1); BadTwoTicks evicts in one
\* tick and deals the blocker in the next, a take in between free to fill the
\* lane (item 5); BadNoJudgeReads raises no judgment when reads hold the lanes
\* (item 8).
CONSTANTS Cards, Lanes, MaxLevel, MaxTime,
          BadEvictsBlocker, BadPicksWrong, BadNoEvict, BadReadyBlind, BadTwoTicks, BadNoJudgeReads
Levels == 0..MaxLevel
Blocker == 0
Reader == 1
None == "none"
VARIABLES col, kind, level, started, now, evictedBy, waited, unjudged, pending, judged, lastPick
vars == <<col, kind, level, started, now, evictedBy, waited, unjudged, pending, judged, lastPick>>

Cost(c) == IF kind[c] = "work" THEN 2 ELSE 1
Sum(S) == LET RECURSIVE S2(_)
              S2(T) == IF T = {} THEN 0 ELSE LET x == CHOOSE y \in T : TRUE IN Cost(x) + S2(T \ {x})
          IN S2(S)
Running == {c \in Cards : col[c] = "running"}
Queued == {c \in Cards : col[c] = "queued"}
Ready == {c \in Cards : col[c] = "ready"}
Work == {c \in Cards : kind[c] = "work"}
Half == 2 * Lanes
LaneRoom(c) == Sum(Running) + Cost(c) <= Half
QueueRoom(c) == Sum(Queued) + Cost(c) <= Half
Blockers == {c \in Work : level[c] = Blocker}
\* a blocker that waits for a lane: ready with no queue room, or queued with no lane room
WaitsForALane(b) == /\ b \in Blockers
                    /\ \/ (col[b] = "ready" /\ ~QueueRoom(b))
                       \/ (col[b] = "queued" /\ ~LaneRoom(b) /\ ~BadReadyBlind)
\* the running cards a blocker may evict: work, never a blocker (unless the model is broken), never a read
Evictable == IF BadEvictsBlocker THEN Running \cap Work ELSE {c \in Running \cap Work : level[c] # Blocker}
Lowest == {c \in Evictable : \A d \in Evictable : level[c] >= level[d]}
Shortest == {c \in Lowest : \A d \in Lowest : started[c] >= started[d]}
LanesHeldByReadsOrBlockers == Evictable = {} /\ Running # {}

TypeOK == /\ col \in [Cards -> {"new", "ready", "queued", "running", "done"}]
          /\ kind \in [Cards -> {"work", "read"}]
          /\ level \in [Cards -> Levels]
          /\ started \in [Cards -> 0..MaxTime]
          /\ now \in 0..MaxTime
          /\ evictedBy \in [Cards -> Cards \cup {None}]
          /\ waited \in [Cards -> 0..1]
          /\ unjudged \in [Cards -> 0..1]
          /\ pending \subseteq Cards
          /\ judged \subseteq Cards

Init == /\ col = [c \in Cards |-> "new"]
        /\ kind \in [Cards -> {"work", "read"}]
        /\ level \in {l \in [Cards -> Levels] : \A c \in Cards : kind[c] = "read" => l[c] = Reader}
        /\ started = [c \in Cards |-> 0]
        /\ now = 0
        /\ evictedBy = [c \in Cards |-> None]
        /\ waited = [c \in Cards |-> 0]
        /\ unjudged = [c \in Cards |-> 0]
        /\ pending = {}
        /\ judged = {}
        /\ lastPick = None

Unchanged(except) == UNCHANGED <<kind, now, evictedBy, waited, unjudged, judged, lastPick>>

\* a card is admitted (add), or an evicted one is offered again: it is ready
Arrive(c) == /\ col[c] = "new"
             /\ col' = [col EXCEPT ![c] = "ready"]
             /\ UNCHANGED <<kind, level, started, now, evictedBy, waited, unjudged, pending, judged, lastPick>>

\* the verb priority: a work card already dealt (queued or running) is raised to blocker
\* in place (relevelCards); the verb sets any level, the model checks the raise, the one
\* that moves a card up the ladder past the lanes
SetLevel(c, l) == /\ kind[c] = "work" /\ col[c] \in {"queued", "running"} /\ l = Blocker /\ level[c] # l
                  /\ level' = [level EXCEPT ![c] = l]
                  /\ judged' = judged \ {c}
                  /\ UNCHANGED <<col, kind, started, now, evictedBy, waited, unjudged, pending, lastPick>>

\* the deal into the row's ready queue, a blocker before any other card (ladderOrder)
Deal(c) == /\ col[c] = "ready" /\ QueueRoom(c)
           /\ (level[c] = Blocker \/ Ready \cap Blockers = {})
           /\ col' = [col EXCEPT ![c] = "queued"]
           /\ UNCHANGED <<kind, level, started, now, evictedBy, waited, unjudged, pending, judged, lastPick>>

\* the take into a lane, a blocker's card first (queueOrder); with BadTwoTicks a take
\* between the eviction and the deal that follows may fill the freed lane with any card
Take(c) == /\ col[c] = "queued" /\ LaneRoom(c)
           /\ (level[c] = Blocker \/ Queued \cap Blockers = {})
           /\ col' = [col EXCEPT ![c] = "running"]
           /\ started' = [started EXCEPT ![c] = now]
           /\ waited' = [waited EXCEPT ![c] = 0]
           /\ unjudged' = [unjudged EXCEPT ![c] = 0]
           /\ judged' = judged \ {c}
           /\ pending' = pending \ {c}
           /\ UNCHANGED <<kind, level, now, evictedBy, lastPick>>

\* a blocker waiting for a lane evicts one running card and takes its lane in the same
\* step (blockerEvictions, evictionPick, evictUnit); the evicted card is ready again at
\* its level, marked by the blocker. BadTwoTicks leaves the blocker where it was, pending
\* the deal of the tick that follows (TickDeals).
Evict(b, v) == /\ ~BadNoEvict
               /\ WaitsForALane(b)
               /\ v \in Evictable
               /\ (BadPicksWrong \/ v \in Shortest)
               /\ evictedBy' = [evictedBy EXCEPT ![v] = b]
               /\ lastPick' = v
               /\ IF BadTwoTicks
                  THEN /\ col' = [col EXCEPT ![v] = "ready"]
                       /\ pending' = pending \cup {b}
                       /\ UNCHANGED <<started, waited, unjudged, judged>>
                  ELSE /\ col' = [col EXCEPT ![b] = "running", ![v] = "ready"]
                       /\ started' = [started EXCEPT ![b] = now]
                       /\ waited' = [waited EXCEPT ![b] = 0]
                       /\ unjudged' = [unjudged EXCEPT ![b] = 0]
                       /\ judged' = judged \ {b}
                       /\ UNCHANGED pending
               /\ UNCHANGED <<kind, level, now>>

\* the judgment of a blocker nothing can be evicted for: every lane a blocker or a read
Judge(b) == /\ WaitsForALane(b) /\ LanesHeldByReadsOrBlockers /\ b \notin judged
            /\ ~(BadNoJudgeReads /\ \E r \in Running : kind[r] = "read")
            /\ judged' = judged \cup {b}
            /\ UNCHANGED <<col, kind, level, started, now, evictedBy, waited, unjudged, pending, lastPick>>

\* a running card finishes: its lane frees
Finish(c) == /\ col[c] = "running"
             /\ col' = [col EXCEPT ![c] = "done"]
             /\ judged' = judged \ {c}
             /\ UNCHANGED <<kind, level, started, now, evictedBy, waited, unjudged, pending, lastPick>>

\* nothing more is due this tick: the clock advances. A blocker left waiting though a
\* lane was free or held a card below blocker has waited a tick it should not have
\* (waited); one left waiting unjudged with nothing to evict has too (unjudged). With
\* BadTwoTicks the blockers pending from an eviction are dealt now, where there is room.
Due == \/ \E c \in Cards : ENABLED Deal(c) \/ ENABLED Take(c) \/ ENABLED Judge(c)
       \/ \E b, v \in Cards : ENABLED Evict(b, v)
CouldRun(b) == LaneRoom(b) \/ (\E c \in Running \cap Work : level[c] # Blocker)
TickDeals == {b \in pending : LaneRoom(b)}
Tick == /\ now < MaxTime /\ ~Due
        /\ now' = now + 1
        /\ col' = [c \in Cards |-> IF c \in TickDeals THEN "running" ELSE col[c]]
        /\ started' = [c \in Cards |-> IF c \in TickDeals THEN now ELSE started[c]]
        /\ pending' = pending \ TickDeals
        /\ waited' = [c \in Cards |-> IF c \in Blockers /\ col[c] \in {"ready", "queued"} /\ c \notin TickDeals /\ CouldRun(c) THEN 1 ELSE waited[c]]
        /\ unjudged' = [c \in Cards |-> IF WaitsForALane(c) /\ c \notin TickDeals /\ LanesHeldByReadsOrBlockers /\ c \notin judged THEN 1 ELSE unjudged[c]]
        /\ UNCHANGED <<kind, level, evictedBy, judged, lastPick>>

Next == \/ \E c \in Cards : Arrive(c) \/ Deal(c) \/ Take(c) \/ Finish(c) \/ Judge(c)
        \/ \E c \in Cards, l \in Levels : SetLevel(c, l)
        \/ \E b, v \in Cards : Evict(b, v)
        \/ Tick

\* the tick is fair, and so are its deal, its take, its eviction and its judgment: what
\* is due is done
Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)
        /\ WF_vars(\E c \in Cards : Deal(c)) /\ WF_vars(\E c \in Cards : Take(c))
        /\ WF_vars(\E b, v \in Cards : Evict(b, v)) /\ WF_vars(\E c \in Cards : Judge(c))

\* a blocker is never evicted (its level as it stood at the eviction: the verb may raise
\* an evicted card later), and a read is never evicted
NoBlockerEvicted == [][\A b, v \in Cards : Evict(b, v) => level[v] # Blocker]_vars
NoReadEvicted == \A c \in Cards : evictedBy[c] # None => kind[c] = "work"

\* the card evicted was the lowest level running, then the shortest running, as they stood
EvictedIsLowestThenShortest == [][\A b, v \in Cards : Evict(b, v) => v \in Shortest]_vars

\* a blocker ready or queued when a lane of its row is free or holds a card below blocker
\* runs within one tick: no tick ends with such a blocker still waiting (item 1: a blocker
\* queued behind held lanes is one of them)
ABlockerReadyWithARowOfItsTierIsRunningWithinOneTick == \A b \in Cards : waited[b] = 0

\* the lane an eviction frees is the blocker's: no other card is taken into a lane while
\* a blocker evicted for it waits (item 5: the eviction and the deal are one plan)
EvictedLaneGoesToTheBlocker == [][\A c \in Cards : Take(c) => level[c] = Blocker \/ pending = {}]_vars

\* a blocker that waits with nothing to evict is judged within the tick (item 8: lanes of
\* reads too)
BlockerWaitingIsJudged == \A b \in Cards : unjudged[b] = 0

\* liveness, bounded (not in the required cases: the budget): by the clock's end a blocker
\* is running or done, or every lane holds a blocker or a read
BlockersRun == <>[](now = MaxTime => \A b \in Blockers : col[b] \in {"new", "running", "done"} \/ LanesHeldByReadsOrBlockers)
=============================================================================
