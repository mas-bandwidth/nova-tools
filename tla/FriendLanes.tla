------------------------------ MODULE FriendLanes ------------------------------
EXTENDS Naturals, FiniteSets
\* docs/SPEC-FRIEND.md, one-shot lanes and the loop (internal/friend/daemon.go
\* Run, takeResults; lanes.go laneStep). A friend's daemon runs width lanes;
\* each step of its loop takes the results of the turns that ended, then every
\* free lane within the width takes the next queued card. The width is the
\* friend row's as the beat answers it, read every step: lowered, a lane
\* beyond it finishes the turn under way and takes no other; raised, the lanes
\* refill. Outside the step: cards dealt to the queue, a turn ending (its
\* result waits for the step), the width changing. The push rule for a lane
\* friend (lanes.go messageTurn): her session is a lane's, so while the push
\* is unproven a free lane hands the check's own pong line as a message turn
\* (CheckTurn), the session's run of it is the proof (Answer), and no card
\* goes in until then. The model checks the step's bound (no ended lane left
\* behind a step, no free lane within the width while a card is queued and the
\* push is proven), the width (no lane starts beyond it), the proof (a card only
\* once proven; proven only by the session's answer, never the daemon's own
\* pong) and that every dealt card ends; not the brief, the cost or the cadence.
CONSTANTS Lanes, Cards, MaxWidth, BadOneResultPerStep, BadStartBeyondWidth,
          BadCardBeforeProof, BadDaemonPong, BadRunWithoutKey
VARIABLES width, lane, holds, queue, done, proven, handed, answered, keyed
vars == <<width, lane, holds, queue, done, proven, handed, answered, keyed>>

None == "none"
Check == "check"  \* a message turn carrying the push check's pong line, in a lane's hand
Free == {l \in Lanes : lane[l] = "free"}
Ended == {l \in Lanes : lane[l] = "ended"}
Busy == {l \in Lanes : lane[l] = "busy"}
Within == {l \in Lanes : l <= width}

Init == /\ width = MaxWidth
        /\ lane = [l \in Lanes |-> "free"]
        /\ holds = [l \in Lanes |-> None]
        /\ queue = {}
        /\ done = {}
        /\ proven = FALSE
        /\ handed = FALSE
        /\ answered = FALSE
        /\ keyed = TRUE

\* a card dealt to her row: delivered, queued for a lane (inbox.go, nextCard)
Deal(c) == /\ c \notin queue /\ c \notin done
           /\ \A l \in Lanes : holds[l] # c
           /\ queue' = queue \cup {c}
           /\ UNCHANGED <<width, lane, holds, done, proven, handed, answered, keyed>>

\* a lane's turn ends: its result waits on the channel for the step (laneResult)
End(l) == /\ lane[l] = "busy"
          /\ lane' = [lane EXCEPT ![l] = "ended"]
          /\ UNCHANGED <<width, holds, queue, done, proven, handed, answered, keyed>>

\* the row's width as the next beat answers it (Row, read every step)
Width(w) == /\ w # width
            /\ width' = w
            /\ UNCHANGED <<lane, holds, queue, done, proven, handed, answered, keyed>>

\* the push unproven: a free lane within the width hands the check's pong line
\* as a message turn of its own (messageTurn; once per check, the cadence apart)
CheckTurn(l) == /\ ~proven /\ ~handed
                /\ (keyed \/ BadRunWithoutKey)
                /\ lane[l] = "free" /\ l \in Within
                /\ lane' = [lane EXCEPT ![l] = "busy"]
                /\ holds' = [holds EXCEPT ![l] = Check]
                /\ handed' = TRUE
                /\ UNCHANGED <<width, queue, done, proven, answered, keyed>>

\* the session runs the line it was handed: its own pong on the bus is the proof
\* (SessionCheck, the pong read off the log); nothing else proves
Answer == /\ handed /\ ~answered
          /\ answered' = TRUE
          /\ proven' = TRUE
          /\ UNCHANGED <<width, lane, holds, queue, done, handed, keyed>>

\* the reversed witness: the daemon counts its own answer (a daemon-pong, a beat,
\* an exit 0) as the proof
DaemonPong == /\ BadDaemonPong /\ ~proven
              /\ proven' = TRUE
              /\ UNCHANGED <<width, lane, holds, queue, done, handed, answered, keyed>>

\* the provider's key leaves the environment, or is sealed and appears (needs_env.go):
\* while it is missing no lane opens or starts; what runs finishes
Key == /\ keyed' = ~keyed
       /\ UNCHANGED <<width, lane, holds, queue, done, proven, handed, answered>>

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
            within == IF BadStartBeyondWidth THEN free1 ELSE free1 \cap Within
            eligible == IF (proven \/ BadCardBeforeProof) /\ (keyed \/ BadRunWithoutKey) THEN within ELSE {}
            pairs == Fill(eligible, queue)
            lanesTaking == {p[1] : p \in pairs}
            cardsTaken == {p[2] : p \in pairs}
        IN /\ lane' = [l \in Lanes |-> IF l \in lanesTaking THEN "busy"
                                        ELSE IF l \in took THEN "free" ELSE lane[l]]
           /\ holds' = [l \in Lanes |-> IF l \in lanesTaking THEN (CHOOSE p \in pairs : p[1] = l)[2]
                                         ELSE IF l \in took THEN None ELSE holds[l]]
           /\ done' = done \cup ({holds[l] : l \in took} \ {Check})
           /\ queue' = queue \ cardsTaken
           /\ UNCHANGED <<width, proven, handed, answered, keyed>>

Next == \/ \E c \in Cards : Deal(c)
        \/ \E l \in Lanes : End(l) \/ CheckTurn(l)
        \/ \E w \in 1..MaxWidth : Width(w)
        \/ Step \/ Answer \/ DaemonPong \/ Key

\* fairness: the step and the check's hand strongly (the loop steps every second whatever
\* the key does between), the session's answer and every turn's end weakly; the key, once
\* missing, is sealed again in the end (EveryCardEnds asks nothing of a friend left keyless)
Spec == Init /\ [][Next]_vars /\ SF_vars(Step) /\ WF_vars(Answer)
        /\ \A l \in Lanes : WF_vars(End(l)) /\ SF_vars(CheckTurn(l))
        /\ WF_vars(~keyed /\ Key)

TypeOK == /\ width \in 1..MaxWidth
          /\ lane \in [Lanes -> {"free", "busy", "ended"}]
          /\ holds \in [Lanes -> Cards \cup {None, Check}]
          /\ queue \subseteq Cards /\ done \subseteq Cards
          /\ proven \in BOOLEAN /\ handed \in BOOLEAN /\ answered \in BOOLEAN /\ keyed \in BOOLEAN
\* a lane is busy or ended with a card or the check, free with none
HoldsMatch == \A l \in Lanes : (lane[l] = "free") <=> (holds[l] = None)
\* a card goes into a lane only once the push is proven (the push rule)
CardsOnlyAfterProof == \A l \in Lanes : holds[l] \in Cards => proven
\* the proof is the session's answer and nothing else (no fabricated pong)
ProvenOnlyBySession == proven => answered
\* no lane starts a turn while the provider's key is missing (no key sealed: down, never a fake up)
NoStartWithoutKey == [][\A l \in Lanes : (lane[l] # "busy" /\ lane'[l] = "busy") => keyed]_vars
\* a card is in one place: queued, in one lane's hand, or done
OneHand == /\ \A c \in Cards : Cardinality({l \in Lanes : holds[l] = c}) <= 1
           /\ \A c \in queue : c \notin done /\ \A l \in Lanes : holds[l] # c
           /\ \A c \in done : \A l \in Lanes : holds[l] # c
\* a step leaves no ended lane uncollected, and no free lane within the width
\* while a card is queued: every freed lane refills on the step after its end
StepLeavesNoLaneBehind == [][Step => Ended' = {} /\ ~(proven' /\ keyed' /\ \E l \in Within' : lane'[l] = "free" /\ queue' # {})]_vars
\* no lane starts a turn beyond the width of that step
StartsWithinWidth == [][\A l \in Lanes : (lane[l] # "busy" /\ lane'[l] = "busy") => l <= width']_vars
\* every card dealt ends done
EveryCardEnds == \A c \in Cards : (c \in queue) ~> (c \in done)
================================================================================
