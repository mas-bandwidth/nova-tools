---------------------------- MODULE CardLifecycle ----------------------------
\* The lifecycle of a primary as the store runs it (internal/sprint/store/steps.go
\* builds these steps and internal/sprint/lifecycle.go holds them to the table).
\* A primary sits in one column of the work table; landed is final and a card
\* that stops any other way leaves the table. The moves are the rows of
\* lifecycle.go's Moves: resolve, deal, finish, withdraw (a worker vanishes or
\* the fleet is down), accept, rework, return (a push fails or the stream's CI
\* goes red), redo, merge (land), release (a sentinel), add (a sentinel inserted
\* in front), drop (the coordinator drops), and the twin replacement Recut/Replace
\* with Relink. A stream, member or reader may be held; a held stream is dealt
\* nothing, folded here onto the card's own held flag. The outside events are
\* the worker that vanishes, the push that fails and the coordinator's drop; the
\* lifecycle itself is judged by lifecycle.go's Lawful, unmet and unlawful.
\*
\* The state the code owns: the column each card is placed in (cols, a one-card
\* column set), the hold (held), the needs a card is waiting on (needs, repointed
\* by a twin replacement) and, for the invariant that landed is final, whether a
\* card ever landed (everLanded); replaced marks a card that a twin replaced.
\*
\* Invariants:
\*   OneColumn          a card is in exactly one column.
\*   LandedFinal        a card that landed never leaves landed (landed is final;
\*                      the one exception, a reopen, belongs to
\*                      card land-verify-landed-ancestry and is not in this code).
\*   NoLiveCardWithOpenNeed  a waiting card with an open need is never moved on:
\*                      every card ready or later has all its needs landed or
\*                      waived (steps.go unmet).
\*   ReplacedNeedTwin   a replaced card's dependents need its twin (twins.go
\*                      Relink).
\*
\* Reversed witnesses (Broken):
\*   "deallanded" a landed card dealt again -> LandedFinal fails.
\*   "twocolumn"  a move adds the new column and keeps the old -> OneColumn fails.
\*   "openneed"   a card is moved to ready with an open need -> NoLiveCard...
\*                 fails.
\*   "notwin"     a replacement drops the old card without repointing its
\*                 dependents -> ReplacedNeedTwin fails.
\*
\* What it leaves out: the work card, the route and the readers are not states
\* here, only the primary's own column; a sentinel is a card that lands from
\* waiting, not a kind; the epochs, the batch and the CI are outside the grain.
\* It is a bounded design model with reversed witnesses, not a refinement proof.
EXTENDS Naturals, FiniteSets

CONSTANTS Cards, Needs, Waived, Twin, Broken

Columns == {"waiting", "ready", "working", "review", "merging", "landed", "dropped"}
Open == {"waiting", "ready", "working", "review", "merging"}
Live == {"ready", "working", "review", "merging"}
None == "none"

VARIABLES cols, held, needs, everLanded, replaced
vars == <<cols, held, needs, everLanded, replaced>>

TypeOK ==
    /\ cols \in [Cards -> SUBSET Columns]
    /\ held \in [Cards -> BOOLEAN]
    /\ needs \in [Cards -> SUBSET Cards]
    /\ everLanded \in [Cards -> BOOLEAN]
    /\ replaced \in [Cards -> BOOLEAN]

Init ==
    /\ cols = [c \in Cards |-> {"waiting"}]
    /\ held = [c \in Cards |-> FALSE]
    /\ needs = Needs
    /\ everLanded = [c \in Cards |-> FALSE]
    /\ replaced = [c \in Cards |-> FALSE]

\* the one column a card sits in; the model keeps cols[c] non-empty
col(c) == CHOOSE x \in cols[c] : TRUE

\* every need of c has landed or was waived
AllLanded(c) == \A n \in needs[c] : col(n) = "landed" \/ n \in Waived[c]

\* a move of c to column to; the twocolumn witness keeps the old column too
Move(c, to) ==
    cols' = [cols EXCEPT ![c] =
                IF "twocolumn" \in Broken THEN (@ \union {to}) ELSE {to}]

Resolve(c) ==
    /\ col(c) = "waiting"
    /\ (AllLanded(c) \/ "openneed" \in Broken)
    /\ Move(c, "ready")
    /\ UNCHANGED <<held, needs, everLanded, replaced>>

Deal(c) ==
    /\ \/ col(c) = "ready"
          \/ ("deallanded" \in Broken /\ col(c) = "landed")
    /\ ~held[c]
    /\ Move(c, "working")
    /\ UNCHANGED <<held, needs, everLanded, replaced>>

Finish(c) ==
    /\ col(c) = "working"
    /\ Move(c, "review")
    /\ UNCHANGED <<held, needs, everLanded, replaced>>

Withdraw(c) ==
    \* a worker vanishes, the fleet is down, or the coordinator takes the card back
    /\ col(c) = "working"
    /\ Move(c, "ready")
    /\ UNCHANGED <<held, needs, everLanded, replaced>>

Accept(c) ==
    /\ col(c) = "review"
    /\ Move(c, "merging")
    /\ UNCHANGED <<held, needs, everLanded, replaced>>

Rework(c, to) ==
    /\ col(c) = "review"
    /\ to \in {"working", "ready"}
    /\ Move(c, to)
    /\ UNCHANGED <<held, needs, everLanded, replaced>>

Return(c) ==
    \* the stream's CI went red, or the coordinator returns a merging card
    /\ col(c) = "merging"
    /\ Move(c, "review")
    /\ UNCHANGED <<held, needs, everLanded, replaced>>

Redo(c, to) ==
    /\ col(c) = "merging"
    /\ to \in {"working", "ready"}
    /\ Move(c, to)
    /\ UNCHANGED <<held, needs, everLanded, replaced>>

Merge(c) ==
    /\ col(c) = "merging"
    /\ Move(c, "landed")
    /\ everLanded' = [everLanded EXCEPT ![c] = TRUE]
    /\ UNCHANGED <<held, needs, replaced>>

Release(c) ==
    \* a sentinel that is reached lands from waiting
    /\ col(c) = "waiting"
    /\ Move(c, "landed")
    /\ everLanded' = [everLanded EXCEPT ![c] = TRUE]
    /\ UNCHANGED <<held, needs, replaced>>

Add(c) ==
    \* a sentinel inserted in front of a ready card
    /\ col(c) = "ready"
    /\ Move(c, "waiting")
    /\ UNCHANGED <<held, needs, everLanded, replaced>>

Drop(c) ==
    \* the coordinator drops an open card, or an outside event takes it off the table
    /\ col(c) \in Open
    /\ Move(c, "dropped")
    /\ UNCHANGED <<held, needs, everLanded, replaced>>

Hold(c) ==
    /\ ~held[c]
    /\ held' = [held EXCEPT ![c] = TRUE]
    /\ UNCHANGED <<cols, needs, everLanded, replaced>>

Unhold(c) ==
    /\ held[c]
    /\ held' = [held EXCEPT ![c] = FALSE]
    /\ UNCHANGED <<cols, needs, everLanded, replaced>>

Replace(c) ==
    \* a twin replaces an old card; Relink repoints its dependents' needs
    /\ Twin[c] # None
    /\ col(c) \in Open
    /\ cols' = [cols EXCEPT ![c] = {"dropped"}]
    /\ needs' = [d \in Cards |->
                    IF c \in needs[d] /\ "notwin" \notin Broken
                    THEN (needs[d] \ {c}) \union {Twin[c]}
                    ELSE needs[d]]
    /\ replaced' = [replaced EXCEPT ![c] = TRUE]
    /\ UNCHANGED <<held, everLanded>>

Next ==
    \E c \in Cards :
        \/ Resolve(c) \/ Deal(c) \/ Finish(c) \/ Withdraw(c) \/ Accept(c)
        \/ Rework(c, "working") \/ Rework(c, "ready")
        \/ Return(c) \/ Redo(c, "working") \/ Redo(c, "ready")
        \/ Merge(c) \/ Release(c) \/ Add(c) \/ Drop(c)
        \/ Hold(c) \/ Unhold(c) \/ Replace(c)

Spec == Init /\ [][Next]_vars /\ (\A c \in Cards : WF_vars(Resolve(c)))

\* a card is in exactly one column
OneColumn == \A c \in Cards : Cardinality(cols[c]) = 1

\* landed is final
LandedFinal == \A c \in Cards : everLanded[c] => col(c) = "landed"

\* a waiting card with an open need is never moved on: every live card's needs landed
NoLiveCardWithOpenNeed == \A c \in Cards : col(c) \in Live => AllLanded(c)

\* a replaced card's dependents need its twin
ReplacedNeedTwin ==
    \A o \in Cards : replaced[o] =>
        \A d \in Cards : o \in Needs[d] => Twin[o] \in needs[d]

\* under fair resolves, a waiting card whose needs have landed does not wait for ever
ResolveWakes ==
    \A c \in Cards : (col(c) = "waiting" /\ AllLanded(c)) ~> (col(c) # "waiting")
=============================================================================
