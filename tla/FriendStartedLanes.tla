------------------------ MODULE FriendStartedLanes ------------------------
EXTENDS Naturals, FiniteSets
\* docs/SPEC-SPRINT.md section 1, a friend is dealt what her session starts
\* (internal/sprint/friend_deal.go friendStartedLanes, friendLimits,
\* friendReturnUnit, friendDeal; friend_level.go friendLevel). One friend in
\* batch mode at width W, her room DealAhead (2) times her lanes. A card dealt
\* to her goes into working while a lane is free and ready behind otherwise;
\* her beat names a working card running (Start: an outside event), and she
\* finishes a started card. The tick's deal returns each working card she has
\* not started within the start window (Window ticks) to the pool, taken from
\* her (left: never dealt to her again), and from then her lanes are the cards
\* she has started (at least 1): she holds her started cards and as many
\* unstarted again as her lanes in working, her started cards and twice her
\* lanes in all, never past her width; her lanes rise as she starts more, and
\* at her width she is dealt as before (lanes = 0: no record). The model checks
\* the bounds and the return; not the stamps, the record's transport on the
\* cards, the level's moves or the reads.
CONSTANTS Cards, W, Window, BadNoReturn, BadBackToHer, BadNoTrialLane, BadPastWidth
VARIABLES col, age, left, lanes
vars == <<col, age, left, lanes>>

Min(a, b) == IF a < b THEN a ELSE b
Max(a, b) == IF a > b THEN a ELSE b
Started == {c \in Cards : col[c] = "started"}
Unstarted == {c \in Cards : col[c] = "working"}
Ready == {c \in Cards : col[c] = "ready"}
Working == Cardinality(Started) + Cardinality(Unstarted)
Held == Working + Cardinality(Ready)
Throttled == lanes # 0 /\ lanes < W
\* friendLimits: her lanes and her room
WidthLim == IF ~Throttled THEN W
            ELSE IF BadNoTrialLane THEN lanes
            ELSE IF BadPastWidth THEN Cardinality(Started) + lanes
            ELSE Min(W, Cardinality(Started) + lanes)
RoomLim == IF ~Throttled THEN 2 * W ELSE Min(2 * W, Cardinality(Started) + 2 * lanes)

Init == /\ col = [c \in Cards |-> "pool"]
        /\ age = [c \in Cards |-> 0]
        /\ left = {}
        /\ lanes = 0

\* friendDeal: a card from the pool, never one taken from her, within her room
Deal(c) == /\ col[c] = "pool"
           /\ (c \notin left \/ BadBackToHer)
           /\ Held < RoomLim
           /\ col' = [col EXCEPT ![c] = IF Working < WidthLim THEN "working" ELSE "ready"]
           /\ age' = [age EXCEPT ![c] = 0]
           /\ UNCHANGED <<left, lanes>>

\* friendDeal: her ready card taken into a lane of hers that is free
Take(c) == /\ col[c] = "ready"
           /\ Working < WidthLim
           /\ col' = [col EXCEPT ![c] = "working"]
           /\ age' = [age EXCEPT ![c] = 0]
           /\ UNCHANGED <<left, lanes>>

\* her beat names it running
Start(c) == /\ col[c] = "working"
            /\ col' = [col EXCEPT ![c] = "started"]
            /\ age' = [age EXCEPT ![c] = 0]
            /\ UNCHANGED <<left, lanes>>

Finish(c) == /\ col[c] = "started"
             /\ col' = [col EXCEPT ![c] = "done"]
             /\ UNCHANGED <<age, left, lanes>>

\* the tick's deal: the cards past the window go back to the pool, her lanes are
\* the cards she has started (at least 1), rising as she starts more; the rest age
Tick == LET ret == IF BadNoReturn THEN {} ELSE {c \in Unstarted : age[c] >= Window}
            n == Cardinality(Started)
            l == IF ret # {} THEN Max(1, n) ELSE IF lanes # 0 THEN Max(lanes, n) ELSE 0
        IN /\ col' = [c \in Cards |-> IF c \in ret THEN "pool" ELSE col[c]]
           /\ age' = [c \in Cards |-> IF c \in Unstarted \ ret THEN Min(age[c] + 1, Window + 1) ELSE 0]
           /\ left' = left \cup ret
           /\ lanes' = Min(l, W)

Next == \/ \E c \in Cards : Deal(c) \/ Take(c) \/ Start(c) \/ Finish(c)
        \/ Tick

Spec == Init /\ [][Next]_vars

TypeOK == /\ col \in [Cards -> {"pool", "ready", "working", "started", "done"}]
          /\ age \in [Cards -> 0..(Window + 1)]
          /\ left \subseteq Cards
          /\ lanes \in 0..W
\* her width is the ceiling, never passed
WidthCeiling == Working <= W
\* a card she has not started is held in working no longer than the window
NeverHeldPastWindow == \A c \in Unstarted : age[c] <= Window
\* a card returned to the pool is never on her row again
NeverBackToHer == \A c \in left : col[c] \in {"pool", "done"}
\* below her width she always has a lane to show a start in, so her lanes can rise
RoomToRise == (Throttled /\ Cardinality(Started) < W) => WidthLim > Cardinality(Started)
\* a card dealt into working while she is held to her lanes keeps her unstarted
\* working cards within them
DealtWithinLanes == [][\A c \in Cards : (Throttled /\ col[c] \in {"pool", "ready"} /\ col'[c] = "working")
                        => Cardinality({d \in Cards : col'[d] = "working"}) <= lanes]_vars
=============================================================================
