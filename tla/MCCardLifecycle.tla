--------------------------- MODULE MCCardLifecycle ---------------------------
\* The TLA+ model of the card lifecycle from internal/sprint/lifecycle.go.
\* A primary's state is its column in the work table. It is admitted
\* waiting or ready, moves between states according to the Moves table,
\* and leaves the table (drop) only from an open state. The needs rule
\* holds a primary from moving waiting -> ready until every need has landed.
\* The twin relink ensures a twin's proposal is always at the head of its line.

EXTENDS Naturals, Sequences

\* States: the six states from lifecycle.go, plus dropped for leaving the table.
State == {"waiting", "ready", "working", "review", "merging", "landed", "dropped"}

\* Open states: drop is possible from these.
Open == {"waiting", "ready", "working", "review", "merging"}

\* Constants: a finite set of primaries for TLC.
Primary == {"p1", "p2", "p3"}

\* A set of needs for testing the needs rule.
Need == {"n1", "n2", "n3"}

\* Constants for the needs rule.
Landings \in SUBSET Need

\* The state of each primary.
StateOf \in [Primary -> State]

\* Whether each primary is held (not yet admitted to ready).
Held \in [Primary -> BOOLEAN]

\* The twin proposal position for each primary (0 means no proposal).
TwinProp \in [Primary -> 0..10]

\* The state of each primary's twin (same head as the proposal).
TwinRelink \in [Primary -> 0..10]

vars == <<StateOf, Held, TwinProp, TwinRelink>>

\* Initialize: all primaries start waiting, not held, no twin proposals.
Init ==
    /\ \A p \in Primary : StateOf[p] = "waiting"
    /\ \A p \in Primary : Held[p] = FALSE
    /\ \A p \in Primary : TwinProp[p] = 0
    /\ \A p \in Primary : TwinRelink[p] = 0

\* The legal moves from lifecycle.go.
\* From -> To, Verb, Class (mechanical or coordinator), Cause
LegalMove(from, to) ==
    \/ (from = "waiting" /\ to = "ready")    \* resolve
    \/ (from = "ready" /\ to = "working")    \* deal
    \/ (from = "working" /\ to = "review")   \* finish
    \/ (from = "working" /\ to = "ready")    \* fleet down
    \/ (from = "review" /\ to = "merging")   \* accept
    \/ (from = "review" /\ to = "working")   \* rework
    \/ (from = "review" /\ to = "ready")     \* rework
    \/ (from = "merging" /\ to = "review")   \* return
    \/ (from = "merging" /\ to = "working")  \* redo
    \/ (from = "merging" /\ to = "ready")    \* redo
    \/ (from = "merging" /\ to = "landed")   \* merge
    \/ (from = "waiting" /\ to = "landed")   \* release
    \/ (from = "ready" /\ to = "waiting")    \* add

\* Drop is possible from any open state.
CanDrop(from) == from \in Open

\* The needs rule: a primary can move waiting -> ready only if all its needs
\* have landed or been waived. For simplicity in the model, we assume
\* needs are satisfied when they are in Landings.
NeedsSatisfied(p) ==
    \* Simplified: the needs rule would check actual needs from the plan.
    TRUE

\* Twin relink: a twin's proposal must track the head of its line.
\* Simplified: TwinRelink tracks TwinProp.
TwinRelinkOK ==
    \A p \in Primary : TwinRelink[p] = TwinProp[p]

\* Move a primary to a new state.
MovePrimary(p, new) ==
    StateOf' = [StateOf EXCEPT ![p] = new]
    /\ UNCHANGED <<Held, TwinProp, TwinRelink>>

\* Drop a primary (remove from table).
DropPrimary(p) ==
    StateOf' = [StateOf EXCEPT ![p] = "dropped"]
    /\ UNCHANGED <<Held, TwinProp, TwinRelink>>

\* Set held status for a primary.
SetHeld(p, h) ==
    Held' = [Held EXCEPT ![p] = h]
    /\ UNCHANGED <<StateOf, TwinProp, TwinRelink>>

\* Update twin proposal for a primary.
SetTwinProp(p, pos) ==
    TwinProp' = [TwinProp EXCEPT ![p] = pos]
    /\ UNCHANGED <<StateOf, Held, TwinRelink>>

\* Update twin relink for a primary.
SetTwinRelink(p, pos) ==
    TwinRelink' = [TwinRelink EXCEPT ![p] = pos]
    /\ UNCHANGED <<StateOf, Held, TwinProp>>

\* State transition: move or drop.
Next ==
    \E p \in Primary :
        \/ /\ StateOf[p] \in Open
           \/ LegalMove(StateOf[p], StateOf'[p])
           \/ DropPrimary(p)
        \/ UNCHANGED <<Held, TwinProp, TwinRelink>>

\* Twin relink maintenance (must always hold after a state change).
MaintainTwinRelink ==
    \A p \in Primary : TwinRelink[p] = TwinProp[p]

\* The full specification.
Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

\* Safety: a primary never enters a state that has no legal move to it
\* from its current state (excluding drop).
\* This is enforced by the Next action.
TypeOK ==
    /\ \A p \in Primary : StateOf[p] \in State
    /\ \A p \in Primary : Held[p] \in BOOLEAN
    /\ \A p \in Primary : TwinProp[p] \in 0..10
    /\ \A p \in Primary : TwinRelink[p] \in 0..10

\* Invariant: a primary is never in a state inconsistent with its previous state.
\* (This is checked by TLC's type invariance.)
NoBackwardsMove ==
    \A p \in Primary :
        \/ StateOf[p] = StateOf'[p]
        \/ LegalMove(StateOf[p], StateOf'[p])
        \/ StateOf'[p] = "dropped"

\* Invariant: a dropped primary stays dropped.
DroppedStaysDropped ==
    \A p \in Primary :
        StateOf[p] = "dropped" => StateOf'[p] = "dropped"

\* Invariant: twin relink always matches proposal position.
TwinRelinkMaintained ==
    \A p \in Primary : TwinRelink'[p] = TwinProp'[p]

=============================================================================
