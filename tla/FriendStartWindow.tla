------------------------ MODULE FriendStartWindow ------------------------
EXTENDS Naturals, FiniteSets
\* docs/SPEC-SPRINT.md section 1, a friend is dealt what her session starts
\* (internal/sprint/friend_deal.go friendStarts, TickFriendStart, cappedLanes,
\* friendRoom). One friend's row in batch mode at width W. Cards are dealt to
\* her row into working while a lane is free and ready behind otherwise, within
\* her room (twice her effective width). Her beat names a working card running
\* (Start); the start window passes on a card she has not started (Age). The
\* tick's start part returns every working card past the window she has not
\* started to the pool, never to her again; caps her lanes at the cards she has
\* started, raised as she starts more and lifted at W; and while capped returns
\* the ready cards past her room she has not started, past the window of their
\* deal. Her effective width is her lanes, never above W and never below one.
\* The model checks the returns, the cap and its bounds; not the stamps, the
\* notes, the hard pin (never returned, outside the model), or the deal off her.
CONSTANTS Cards, W, BadNoFloor, BadReturnStarted, BadNoReturn, BadRedeal, BadNoTrim
\* tock flips at each start part, so a part that returns nothing is still a
\* step the action properties judge, never a stutter
VARIABLES col, started, aged, capped, lanes, left, tock
vars == <<col, started, aged, capped, lanes, left, tock>>

Min(a, b) == IF a < b THEN a ELSE b
Max(a, b) == IF a > b THEN a ELSE b
Working == {c \in Cards : col[c] = "working"}
Ready == {c \in Cards : col[c] = "ready"}
Load == Cardinality(Working) + Cardinality(Ready)
\* cappedLanes: her started lanes, never above her width, never below one
Eff(cap, l) == IF ~cap THEN W
               ELSE IF BadNoFloor THEN Min(l, W) ELSE Max(1, Min(l, W))
EffWidth == Eff(capped, lanes)
Room == 2 * EffWidth

Init == /\ col = [c \in Cards |-> "pool"]
        /\ started = [c \in Cards |-> FALSE]
        /\ aged = [c \in Cards |-> FALSE]
        /\ capped = FALSE
        /\ lanes = W
        /\ left = {}
        /\ tock = FALSE

\* the deal (friendDeal): a pool card she has not left, within her room
Deal(c) == /\ col[c] = "pool" /\ (c \notin left \/ BadRedeal) /\ Load < Room
           /\ col' = [col EXCEPT ![c] = IF Cardinality(Working) < EffWidth THEN "working" ELSE "ready"]
           /\ aged' = [aged EXCEPT ![c] = FALSE]
           /\ UNCHANGED <<started, capped, lanes, left, tock>>

\* a free lane taken from her ready cards (the deal's fill, her take, her next):
\* a new take, so a new window
Fill(c) == /\ col[c] = "ready" /\ Cardinality(Working) < EffWidth
           /\ col' = [col EXCEPT ![c] = "working"]
           /\ aged' = [aged EXCEPT ![c] = FALSE]
           /\ UNCHANGED <<started, capped, lanes, left, tock>>

\* her beat names it running (friendStarted)
Start(c) == /\ col[c] = "working" /\ ~started[c]
            /\ started' = [started EXCEPT ![c] = TRUE]
            /\ UNCHANGED <<col, aged, capped, lanes, left, tock>>

\* the start window passes on a card of hers she has not started
Age(c) == /\ col[c] \in {"ready", "working"} /\ ~started[c] /\ ~aged[c]
          /\ aged' = [aged EXCEPT ![c] = TRUE]
          /\ UNCHANGED <<col, started, capped, lanes, left, tock>>

Finish(c) == /\ col[c] = "working" /\ started[c]
             /\ col' = [col EXCEPT ![c] = "done"]
             /\ UNCHANGED <<started, aged, capped, lanes, left, tock>>

\* the tick's start part (TickFriendStart over friendStarts), one step
Back1 == IF BadNoReturn THEN {}
         ELSE {c \in Working : (aged[c] /\ ~started[c]) \/ (BadReturnStarted /\ started[c])}
Part == LET back == Back1
            s == Cardinality({c \in Cards : col[c] \in {"ready", "working"} /\ c \notin back /\ started[c]})
            l1 == IF back # {} THEN s ELSE IF capped THEN Max(lanes, s) ELSE W
            cap == (back # {} \/ capped) /\ l1 < W
            lane == IF cap THEN l1 ELSE W
            over == Load - Cardinality(back) - 2 * Eff(cap, lane)
            can == {c \in Ready : aged[c] /\ ~started[c]}
            trim == IF ~cap \/ BadNoTrim \/ over <= 0 THEN {}
                    ELSE CHOOSE t \in SUBSET can : Cardinality(t) = Min(over, Cardinality(can))
            out == back \cup trim
        IN /\ col' = [c \in Cards |-> IF c \in out THEN "pool" ELSE col[c]]
           /\ left' = left \cup out
           /\ capped' = cap
           /\ lanes' = lane
           /\ tock' = ~tock
           /\ UNCHANGED <<started, aged>>

Next == \/ \E c \in Cards : Deal(c) \/ Fill(c) \/ Start(c) \/ Age(c) \/ Finish(c)
        \/ Part

Spec == Init /\ [][Next]_vars

TypeOK == /\ col \in [Cards -> {"pool", "ready", "working", "done"}]
          /\ started \in [Cards -> BOOLEAN] /\ aged \in [Cards -> BOOLEAN]
          /\ capped \in BOOLEAN /\ lanes \in 0..W /\ left \subseteq Cards /\ tock \in BOOLEAN
\* her width is the ceiling, never the target, and she keeps a lane to start in
CeilingAndFloor == EffWidth \in 1..W
\* a card she started is never taken from her
StartedStays == \A c \in left : ~started[c]
\* a card that went back to the pool is never dealt to her again
NeverBackToHer == \A c \in left : col[c] = "pool"
\* the part leaves no working card past the window she has not started
ReturnedAtWindow == [][Part => \A c \in Cards : (col[c] = "working" /\ aged[c] /\ ~started[c]) => col'[c] = "pool"]_vars
\* while capped, her row past her room holds no ready card past the window she
\* has not started
BoundedAfterPart == [][Part /\ capped' /\ Load' > 2 * Eff(capped', lanes') =>
                        \A c \in Cards : col'[c] = "ready" => (started'[c] \/ ~aged'[c])]_vars
\* her started lanes are at least the cards she has started on her row
LanesRise == [][Part /\ capped' => lanes' >= Cardinality({c \in Cards : col'[c] \in {"ready", "working"} /\ started'[c]})]_vars
=============================================================================
