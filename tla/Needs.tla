---- MODULE Needs ----
\* The needs a card waits on. The Go drain is internal/sprint/steps_work.go
\* (waitingDrain, goneNeeds, detachDropped). This module stores only the ids
\* still waited on: a landed id leaves the set at once. The Go field may
\* still store a landed id. A dropped or replaced id leaves at the drop or
\* recut. A name that is no card stays until Tick.
EXTENDS Naturals, FiniteSets

CONSTANTS Cards, MaxNeeds

Status == {"waiting", "ready", "landed", "dropped", "replaced", "archived", "absent"}

VARIABLES card, needs, ticked

TypeOK ==
    /\ card \in [Cards -> Status]
    /\ needs \in [Cards -> SUBSET Cards]
    /\ \A c \in Cards : Cardinality(needs[c]) <= MaxNeeds
    /\ ticked \in BOOLEAN

\* After a tick, no waiting card names a need that has landed, been dropped,
\* been replaced, been archived, or names no card.
NoWaitOnGone ==
    ticked => \A c \in Cards :
        card[c] = "waiting" => \A n \in needs[c] :
            card[n] \in {"waiting", "ready"}

Init ==
    /\ card = [c \in Cards |-> "absent"]
    /\ needs = [c \in Cards |-> {}]
    /\ ticked = FALSE

\* Go add refuses a missing or dropped name. The model admits an absent name
\* so Tick's detach of it is reachable. A set of only landed names admits
\* the card ready, and those names are not stored.
Add(c, ns) ==
    /\ card[c] = "absent"
    /\ ns \subseteq Cards
    /\ Cardinality(ns) <= MaxNeeds
    /\ \A n \in ns : card[n] \in {"waiting", "ready", "landed", "absent"}
    /\ LET live == {n \in ns : card[n] \in {"waiting", "ready"}}
           absent == {n \in ns : card[n] = "absent"}
       IN /\ needs' = [needs EXCEPT ![c] = live \cup absent]
          /\ card' = [card EXCEPT ![c] = IF live = {} /\ absent = {} THEN "ready" ELSE "waiting"]
    /\ ticked' = FALSE

Land(c) ==
    /\ card[c] \in {"waiting", "ready"}
    /\ card' = [card EXCEPT ![c] = "landed"]
    /\ needs' = [w \in Cards |-> needs[w] \ {c}]
    /\ ticked' = FALSE

\* The id leaves every waiter now. The waiter stays waiting until Tick.
Drop(c) ==
    /\ card[c] \in {"waiting", "ready"}
    /\ card' = [card EXCEPT ![c] = "dropped"]
    /\ needs' = [w \in Cards |-> IF w = c THEN {} ELSE needs[w] \ {c}]
    /\ ticked' = FALSE

Recut(c, twin) ==
    /\ card[c] \in {"waiting", "ready"}
    /\ twin \in Cards /\ twin # c
    /\ card[twin] = "absent"
    /\ card' = [card EXCEPT ![c] = "replaced", ![twin] = "waiting"]
    /\ needs' = [w \in Cards |->
         IF w = c THEN {}
         ELSE IF c \in needs[w] THEN (needs[w] \ {c}) \cup {twin}
         ELSE needs[w]]
    /\ ticked' = FALSE

Archive(c) ==
    /\ card[c] = "landed"
    /\ card' = [card EXCEPT ![c] = "archived"]
    /\ needs' = [w \in Cards |-> needs[w] \ {c}]
    /\ ticked' = FALSE

Tick ==
    /\ needs' = [w \in Cards |-> {n \in needs[w] : card[n] \in {"waiting", "ready"}}]
    /\ card' = [w \in Cards |->
         IF card[w] = "waiting" /\ {n \in needs[w] : card[n] \in {"waiting", "ready"}} = {}
         THEN "ready"
         ELSE card[w]]
    /\ ticked' = TRUE

Next ==
    \/ \E c \in Cards, ns \in SUBSET Cards : Add(c, ns)
    \/ \E c \in Cards : Land(c) \/ Drop(c) \/ Archive(c)
    \/ \E c, twin \in Cards : Recut(c, twin)
    \/ Tick

Spec == Init /\ [][Next]_<<card, needs, ticked>> /\ WF_<<card, needs, ticked>>(Tick)

\* A waiting card whose needs are all gone, or already empty, becomes ready.
GoneBecomesReady ==
    \A c \in Cards :
        (card[c] = "waiting" /\ \A n \in needs[c] : card[n] \in {"landed", "dropped", "replaced", "archived", "absent"})
        ~> card[c] = "ready"

====
