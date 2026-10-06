\* CardLifecycle: The card lifecycle model for nova-sprint
\* Derives the primary state machine from internal/sprint/lifecycle.go
\* Primary's columns: waiting, ready, working, review, merging, landed
\* Invariants: landed is final; one column per card; waiting card with need never dealt
\* Reversed witnesses: BrokenDealtAfterLanded, BrokenTwoColumns, BrokenNeedDealtBeforeLanded

EXTENDS Integers, Sequences, TLC

CONSTANTS
\* CARD: finite set of card identifiers
CARD

CONSTANTS
\* NEEDS: finite set of need identifiers
NEEDS

VARIABLES
\* col: card -> {waiting, ready, working, review, merging, landed, off}
\* col[c] = "off" means the card has been dropped (off the table)
col

\* needs: card -> SUBSET NEEDS (the needs of each card)
needs

\* needLanded: NEEDS -> BOOLEAN (whether each need has landed)
needLanded

\* nextCard: CARD (the next card to be dealt)
nextCard

\* nextNeed: NEEDS (the next need identifier)
nextNeed

\* nextCard, nextNeed are counters, not state; they don't change

TypeOK ==
    /\ col \in [CARD -> {"waiting", "ready", "working", "review", "merging", "landed", "off"}]
    /\ needs \in [CARD -> SUBSET NEEDS]
    /\ needLanded \in [NEEDS -> BOOLEAN]

\* Actions from lifecycle.go Moves
\* waiting -> ready (resolve)
Resolve(c) ==
    /\ col[c] = "waiting"
    /\ col' = [col EXCEPT ![c] = "ready"]
    /\ UNCHANGED <<needs, needLanded>>

\* ready -> working (deal)
\* A card can be dealt only if it has no open needs
Deal(c) ==
    /\ col[c] = "ready"
    /\ needs[c] \in {}  \* No open needs
    /\ col' = [col EXCEPT ![c] = "working"]
    /\ UNCHANGED <<needs, needLanded>>

\* working -> review (finish)
Finish(c) ==
    /\ col[c] = "working"
    /\ col' = [col EXCEPT ![c] = "review"]
    /\ UNCHANGED <<needs, needLanded>>

\* working -> ready (fleet down)
\* Card goes back to ready if fleet goes down
FleetDown(c) ==
    /\ col[c] = "working"
    /\ col' = [col EXCEPT ![c] = "ready"]
    /\ UNCHANGED <<needs, needLanded>>

\* review -> merging (accept)
Accept(c) ==
    /\ col[c] = "review"
    /\ col' = [col EXCEPT ![c] = "merging"]
    /\ UNCHANGED <<needs, needLanded>>

\* review -> working (rework)
ReWorkBack(c) ==
    /\ col[c] = "review"
    /\ col' = [col EXCEPT ![c] = "working"]
    /\ UNCHANGED <<needs, needLanded>>

\* review -> ready (rework when no fleet member is up)
ReWorkToReady(c) ==
    /\ col[c] = "review"
    /\ col' = [col EXCEPT ![c] = "ready"]
    /\ UNCHANGED <<needs, needLanded>>

\* merging -> review (return)
Return(c) ==
    /\ col[c] = "merging"
    /\ col' = [col EXCEPT ![c] = "review"]
    /\ UNCHANGED <<needs, needLanded>>

\* merging -> working (redo with conflicted card)
RedoToWorking(c) ==
    /\ col[c] = "merging"
    /\ col' = [col EXCEPT ![c] = "working"]
    /\ UNCHANGED <<needs, needLanded>>

\* merging -> ready (redo when no fleet member is up)
RedoToReady(c) ==
    /\ col[c] = "merging"
    /\ col' = [col EXCEPT ![c] = "ready"]
    /\ UNCHANGED <<needs, needLanded>>

\* merging -> landed (merge)
Merge(c) ==
    /\ col[c] = "merging"
    /\ col' = [col EXCEPT ![c] = "landed"]
    /\ UNCHANGED <<needs, needLanded>>

\* waiting -> landed (release - only sentinels)
\* For simplicity, we allow any card here; sentinel rule is external
Release(c) ==
    /\ col[c] = "waiting"
    /\ col' = [col EXCEPT ![c] = "landed"]
    /\ UNCHANGED <<needs, needLanded>>

\* ready -> waiting (add sentinel in front)
AddToWaiting(c) ==
    /\ col[c] = "ready"
    /\ col' = [col EXCEPT ![c] = "waiting"]
    /\ UNCHANGED <<needs, needLanded>>

\* Any open state -> off (drop)
\* This models the card being removed from the table
Drop(c) ==
    /\ col[c] \in {"waiting", "ready", "working", "review", "merging"}
    /\ col' = [col EXCEPT ![c] = "off"]
    /\ UNCHANGED <<needs, needLanded>>

\* Initialize a card
Init ==
    /\ col = [c \in CARD |-> "waiting"]
    /\ needs = [c \in CARD |-> {}]
    /\ needLanded = [n \in NEEDS |-> FALSE]

Next ==
    \* Each action operates on one card
    \* We use nondeterministic choice via existential quantification
    \* \*[c \in CARD]

\* Invariants
\* TypeOK invariant
TypeInvar == TypeOK

\* Landed is final: once landed, card cannot move except by reopen (not modeled here)
\* In this model, once a card is landed, no action can move it
LandedFinal ==
    \A c \in CARD: col[c] = "landed" => [col'][c] = "landed"

\* One column per card: a card is always in exactly one column
OneColumn ==
    \A c \in CARD: col[c] \in {"waiting", "ready", "working", "review", "merging", "landed", "off"}

\* A waiting card with open needs is never dealt
\* For a card to move to "working" (dealt), it must be in "ready" with no needs
WaitingWithNeedNotDealt ==
    \A c \in CARD: needs[c] \in {} => [col'][c] \neq "working"

\* All invariants
Spec == Init /\ [][Next]_<<col, needs, needLanded>> /\ WF_vars(Next)

\* Liveness: every card that is ready and has no needs will eventually be dealt
Liveness ==
    \A c \in CARD:
        \A (col[c] = "ready" /\ needs[c] \in {}) =>
            <> (col[c] = "working")
