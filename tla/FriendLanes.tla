------------------------------ MODULE FriendLanes ------------------------------
EXTENDS Naturals, FiniteSets
\* docs/SPEC-FRIEND.md, one-shot lanes and the loop (internal/friend/daemon.go
\* Run, takeResults; lanes.go laneStep). A friend's daemon runs width lanes;
\* each step of its loop takes the results of the turns that ended, then every
\* free lane within the width takes the next queued card. The width is the
\* friend row's as the beat answers it, read every step: lowered, a lane
\* beyond it finishes the turn under way and takes no other; raised, the lanes
\* refill. Outside the step: cards dealt to the queue, a turn ending (its
\* result waits for the step), the width changing. The model checks the step's
\* bound (no ended lane left behind a step, no free lane within the width
\* while a card is queued), the width (no lane starts beyond it) and that
\* every dealt card ends; not the session, the brief or the cost.
CONSTANTS Lanes, Cards, MaxWidth, BadOneResultPerStep, BadStartBeyondWidth
VARIABLES width, lane, holds, queue, done
vars == <<width, lane, holds, queue, done>>

None == "none"
Free == {l \in Lanes : lane[l] = "free"}
Ended == {l \in Lanes : lane[l] = "ended"}
Busy == {l \in Lanes : lane[l] = "busy"}
Within == {l \in Lanes : l <= width}

Init == /\ width = MaxWidth
        /\ lane = [l \in Lanes |-> "free"]
        /\ holds = [l \in Lanes |-> None]
        /\ queue = {}
        /\ done = {}

\* a card dealt to her row: delivered, queued for a lane (inbox.go, nextCard)
Deal(c) == /\ c \notin queue /\ c \notin done
           /\ \A l \in Lanes : holds[l] # c
           /\ queue' = queue \cup {c}
           /\ UNCHANGED <<width, lane, holds, done>>

\* a lane's turn ends: its result waits on the channel for the step (laneResult)
End(l) == /\ lane[l] = "busy"
          /\ lane' = [lane EXCEPT ![l] = "ended"]
          /\ UNCHANGED <<width, holds, queue, done>>

\* the row's width as the next beat answers it (Row, read every step)
Width(w) == /\ w # width
            /\ width' = w
            /\ UNCHANGED <<lane, holds, queue, done>>

\* the step's take of results: every ended lane's (takeResults), or one a step
\* (the loop before it: one select, one result)
Collected == IF BadOneResultPerStep /\ Ended # {} THEN {CHOOSE l \in Ended : TRUE} ELSE Ended

\* the lanes that take a card this step: free after the take, within the
\* width (or any lane, the reversed witness), as many as the queue holds, lowest first
RECURSIVE Fill(_, _)
Fill(free, cards) == IF free = {} \/ cards = {} THEN {}
                     ELSE LET l == CHOOSE x \in free : \A y \in free : x <= y
                              c == CHOOSE x \in cards : TRUE
                          IN {<<l, c>>} \cup Fill(free \ {l}, cards \ {c})

Step == LET took == Collected
            free1 == Free \cup took
            eligible == IF BadStartBeyondWidth THEN free1 ELSE free1 \cap Within
            pairs == Fill(eligible, queue)
            lanesTaking == {p[1] : p \in pairs}
            cardsTaken == {p[2] : p \in pairs}
        IN /\ lane' = [l \in Lanes |-> IF l \in lanesTaking THEN "busy"
                                        ELSE IF l \in took THEN "free" ELSE lane[l]]
           /\ holds' = [l \in Lanes |-> IF l \in lanesTaking THEN (CHOOSE p \in pairs : p[1] = l)[2]
                                         ELSE IF l \in took THEN None ELSE holds[l]]
           /\ done' = done \cup {holds[l] : l \in took}
           /\ queue' = queue \ cardsTaken
           /\ UNCHANGED width

Next == \/ \E c \in Cards : Deal(c)
        \/ \E l \in Lanes : End(l)
        \/ \E w \in 1..MaxWidth : Width(w)
        \/ Step

Spec == Init /\ [][Next]_vars /\ WF_vars(Step) /\ \A l \in Lanes : WF_vars(End(l))

TypeOK == /\ width \in 1..MaxWidth
          /\ lane \in [Lanes -> {"free", "busy", "ended"}]
          /\ holds \in [Lanes -> Cards \cup {None}]
          /\ queue \subseteq Cards /\ done \subseteq Cards
\* a lane is busy or ended with a card, free with none
HoldsMatch == \A l \in Lanes : (lane[l] = "free") <=> (holds[l] = None)
\* a card is in one place: queued, in one lane's hand, or done
OneHand == /\ \A c \in Cards : Cardinality({l \in Lanes : holds[l] = c}) <= 1
           /\ \A c \in queue : c \notin done /\ \A l \in Lanes : holds[l] # c
           /\ \A c \in done : \A l \in Lanes : holds[l] # c
\* a step leaves no ended lane uncollected, and no free lane within the width
\* while a card is queued: every freed lane refills on the step after its end
StepLeavesNoLaneBehind == [][Step => Ended' = {} /\ ~(\E l \in Within' : lane'[l] = "free" /\ queue' # {})]_vars
\* no lane starts a turn beyond the width of that step
StartsWithinWidth == [][\A l \in Lanes : (lane[l] # "busy" /\ lane'[l] = "busy") => l <= width']_vars
\* every card dealt ends done
EveryCardEnds == \A c \in Cards : (c \in queue) ~> (c \in done)
================================================================================
