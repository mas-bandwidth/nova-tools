----------------------------- MODULE Needs -----------------------------
\* The needs machine: a card never waits on a need that is gone (the owner,
\* 2026-10-07 5:10 PM ET: when a card is removed, landed or any way the
\* dependency is removed, the machine mechanically removes it from the cards
\* that name it; docs/SPEC-SPRINT.md section 11; internal/sprint/steps_work.go,
\* Resolve and resolveAfter). A need is satisfied by landing, and it is
\* detached when its card is gone: dropped, replaced by a recut twin,
\* archived, or named by no card at all. The tick's drain detaches every gone
\* named need and moves a waiting card whose needs are all gone to ready.
\*
\* The actions are the verbs and the outside events: Add (add), Land, Drop,
\* Recut (the twin takes over the edges), Archive, and TickDrain. Broken =
\* "none" is the design; the reversed witnesses break one property each:
\*   "nodetach"  the drain moves a card to ready but keeps its gone needs:
\*               NoWaitOnGone
\*   "noready"   the drain detaches the gone needs but never moves the card:
\*               Settles
\*
\* The instance is the small one the card names: three cards, and a card may
\* name up to two needs (MCards: MCNeeds.tla).

EXTENDS Integers, FiniteSets

CONSTANTS Cards, Broken

None == "none"

\* absent (never admitted), waiting, ready, landed, dropped
Cols == {"absent", "waiting", "ready", "landed", "dropped"}

VARIABLES col, need, replaced, archived, drained

vars == <<col, need, replaced, archived, drained>>

\* Gone says the need x is gone: its card left the table (dropped, landed or
\* absent), a recut replaced it, or its stream was archived.
Gone(x) == col[x] \in {"absent", "landed", "dropped"} \/ x \in replaced \/ x \in archived

\* Live(d) is the named needs of d that are not gone: what it still waits for.
Live(d) == {n \in need[d] : ~Gone(n)}

\* Due says the drain has something to do: a waiting card whose needs are all
\* gone. The machine runs the drain before any other move (a tick's resolve
\* part), so a due card is not landed or dropped out from under it: only
\* TickDrain is enabled while it is due. This is what makes the liveness below
\* a property of the design and not of the outside.
Due == \E d \in Cards : col[d] = "waiting" /\ Live(d) = {}

TypeOK ==
  /\ col \in [Cards -> Cols]
  /\ need \in [Cards -> SUBSET Cards]
  /\ replaced \subseteq Cards
  /\ archived \subseteq Cards
  /\ drained \in BOOLEAN

Init ==
  /\ col = [x \in Cards |-> "absent"]
  /\ need = [x \in Cards |-> {}]
  /\ replaced = {}
  /\ archived = {}
  /\ drained = TRUE

\* add: a card is admitted waiting, naming a nonempty set of needs (a need may
\* name a card that was never admitted: the "names no card" case the drain
\* detaches)
Add(d, ns) ==
  /\ ~Due
  /\ col[d] = "absent"
  /\ ns \subseteq Cards
  /\ ns # {}
  /\ d \notin ns
  /\ col' = [col EXCEPT ![d] = "waiting"]
  /\ need' = [need EXCEPT ![d] = ns]
  /\ UNCHANGED <<replaced, archived>>
  /\ drained' = FALSE

\* land: a card on the table goes on; every waiting card that named it has a
\* satisfied need when the drain next runs
Land(x) ==
  /\ ~Due
  /\ col[x] \in {"waiting", "ready"}
  /\ col' = [col EXCEPT ![x] = "landed"]
  /\ UNCHANGED <<need, replaced, archived>>
  /\ drained' = FALSE

\* drop: the coordinator takes an open card off the table
Drop(x) ==
  /\ ~Due
  /\ col[x] \in {"waiting", "ready"}
  /\ col' = [col EXCEPT ![x] = "dropped"]
  /\ UNCHANGED <<need, replaced, archived>>
  /\ drained' = FALSE

\* recut: x is replaced by its twin t; every waiting card that named x names t
\* in its place, in the same step, and x is recorded replaced
Recut(x, t) ==
  /\ ~Due
  /\ col[x] \in {"waiting", "ready", "dropped"}
  /\ col[t] = "absent"
  /\ x # t
  /\ col' = [col EXCEPT ![x] = "dropped", ![t] = "waiting"]
  /\ need' = [d \in Cards |-> IF x \in need[d] THEN (need[d] \ {x}) \cup {t} ELSE need[d]]
  /\ replaced' = replaced \cup {x}
  /\ UNCHANGED archived
  /\ drained' = FALSE

\* archive: a landed stream's cards are hidden off the drawn table
Archive(x) ==
  /\ ~Due
  /\ col[x] = "landed"
  /\ archived' = archived \cup {x}
  /\ UNCHANGED <<col, need, replaced>>
  /\ drained' = FALSE

\* the tick's drain: detach every gone named need, and move a waiting card
\* whose needs are all gone to ready. drained records that the drain has run
\* since the last change, so NoWaitOnGone is the state after a tick.
TickDrain ==
  /\ drained' = TRUE
  /\ IF Broken = "nodetach"
       THEN /\ UNCHANGED need
            /\ col' = [d \in Cards |-> IF col[d] = "waiting" /\ Live(d) = {} THEN "ready" ELSE col[d]]
     ELSE IF Broken = "noready"
       THEN /\ need' = [d \in Cards |-> Live(d)]
            /\ UNCHANGED col
     ELSE /\ need' = [d \in Cards |-> Live(d)]
          /\ col' = [d \in Cards |-> IF col[d] = "waiting" /\ Live(d) = {} THEN "ready" ELSE col[d]]
  /\ UNCHANGED <<replaced, archived>>

Next ==
  \/ \E d \in Cards, ns \in SUBSET Cards : Add(d, ns)
  \/ \E x \in Cards : Land(x)
  \/ \E x \in Cards : Drop(x)
  \/ \E x \in Cards, t \in Cards : Recut(x, t)
  \/ \E x \in Cards : Archive(x)
  \/ TickDrain

Fairness == WF_vars(TickDrain)

Spec == Init /\ [][Next]_vars /\ Fairness

\* No waiting card names a need that is gone after a tick.
NoWaitOnGone ==
  drained => \A d \in Cards : col[d] = "waiting" => \A n \in need[d] : ~Gone(n)

\* No waiting card waits on nothing after a tick: the drain moved it to ready.
NoWaitingEmpty ==
  drained => \A d \in Cards : col[d] = "waiting" => Live(d) # {}

\* A waiting card whose needs are all gone becomes ready. It is due, so only
\* the drain is enabled, and the drain is fair.
Settles == \A d \in Cards : (col[d] = "waiting" /\ Live(d) = {}) ~> (col[d] = "ready")

=============================================================================
