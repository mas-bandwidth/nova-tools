----------------------------- MODULE FriendLanes -----------------------------
EXTENDS Naturals, FiniteSets
\* A friend's lanes, each independent (docs/SPEC-FRIEND.md, "Every lane refreshes
\* independently"; internal/friend/lanes.go laneStep, laneDone; daemon.go Run, row).
\* The owner, 2026-10-10, on the batch turn ("a turn takes the cards that were ready
\* when it started, and anything dealt mid-turn waits for the next turn"): "this is a
\* bad design. each lane should refresh independently."
\*
\* One friend's daemon. Her row holds cards (row): new (not yet dealt to her), ready
\* (dealt, its brief in her inbox, not begun), lane (a lane runs it), done (its run
\* ended: a report written, failed or set aside; each frees the lane alike). Her lanes
\* are 1..MaxWidth; lane[l] is the card lane l runs, or None. Her row's width can move
\* (SetWidth, the row as her beat answers it): a lane beyond a width since lowered
\* finishes the run under way and takes no other (retiring). Her main session (main)
\* only talks: a turn of bus messages (Talk, TalkEnds; daemon.go startBatch in
\* one-shot mode) runs beside the lanes and holds no card (mainCards stays empty).
\*
\* The outside: a card dealt mid-run (Deal), a lane's run ending (Finish, any lane, in
\* any order), the width moving (SetWidth, at most MaxMoves times), the main session's
\* comms turns. The daemon's step (Step, one pass of Run's loop): every free lane within
\* the width takes the next ready card, at once, each its own card; nothing waits for
\* another lane or any turn. stepped is TRUE right after a step and FALSE after any
\* outside event, so Filled reads the state the step left.
\*
\* Broken = "none" is the design. Every other value is a reversed witness, each caught
\* by one property:
\*   "batch"     the free main session takes every ready card as one turn (the batch
\*               turn this replaces): MainHoldsNoCard
\*   "boundary"  a free lane takes only when every lane is free, at a turn's boundary:
\*               Filled
\*   "pastwidth" a lane past the width takes a card: WithinWidth
\*   "shared"    a free lane may take a card another lane runs: OneLanePerCard
\*   "idle"      a lane that ran a card never takes another (no refresh): ReadyStarts,
\*               checked alone in MCFriendLanesBrokenIdle under SpecLive
CONSTANTS Cards, MaxWidth, MaxMoves, Broken
None == "none"
L == 1..MaxWidth
VARIABLES row, lane, width, retiring, main, mainCards, moves, ran, stepped
vars == <<row, lane, width, retiring, main, mainCards, moves, ran, stepped>>

Working == {l \in L : lane[l] # None}
Ready == {c \in Cards : row[c] = "ready"}
\* a lane that may take a card now: free, within the width, and (idle) never ran one
Free == {l \in L : lane[l] = None /\ (l <= width \/ Broken = "pastwidth") /\ ~(Broken = "idle" /\ ran[l])}
\* what a free lane may take: a ready card, and (shared) one another lane runs
Takeable == IF Broken = "shared" THEN Ready \cup {c \in Cards : row[c] = "lane"} ELSE Ready
Min(a, b) == IF a < b THEN a ELSE b
\* one-to-one maps of the lanes that take onto the cards they take
Matches(ls, cs) == {f \in [ls -> cs] : \A a, b \in ls : a # b => f[a] # f[b]}

Init == /\ row = [c \in Cards |-> "new"]
        /\ lane = [l \in L |-> None]
        /\ width \in L
        /\ retiring = [l \in L |-> FALSE]
        /\ main = "free"
        /\ mainCards = {}
        /\ moves = 0
        /\ ran = [l \in L |-> FALSE]
        /\ stepped = FALSE

\* ---------------------------------------------------------------- the outside
\* a card dealt to her row mid-run: its brief in her inbox, ready (inbox.go inboxStep)
Deal(c) == /\ row[c] = "new"
           /\ row' = [row EXCEPT ![c] = "ready"]
           /\ stepped' = FALSE
           /\ UNCHANGED <<lane, width, retiring, main, mainCards, moves, ran>>

\* lane l's run ends, whatever the other lanes do (lanes.go laneDone): the card done,
\* failed or set aside, the lane free
Finish(l) == /\ lane[l] # None
             /\ row' = [row EXCEPT ![lane[l]] = "done"]
             /\ lane' = [lane EXCEPT ![l] = None]
             /\ retiring' = [retiring EXCEPT ![l] = FALSE]
             /\ stepped' = FALSE
             /\ UNCHANGED <<width, main, mainCards, moves, ran>>

\* her row's width moves (daemon.go row): a lane running beyond it retires, its run
\* going on to its end
SetWidth(w) == /\ moves < MaxMoves /\ w # width
               /\ width' = w
               /\ retiring' = [l \in L |-> lane[l] # None /\ l > w]
               /\ moves' = moves + 1
               /\ stepped' = FALSE
               /\ UNCHANGED <<row, lane, main, mainCards, ran>>

\* the main session's comms turn: bus messages, presence checks, relays; never a card
Talk == /\ main = "free"
        /\ main' = "talk"
        /\ stepped' = FALSE
        /\ UNCHANGED <<row, lane, width, retiring, mainCards, moves, ran>>

\* the main session's turn ends; a batch turn's cards end with it
TalkEnds == /\ main # "free"
            /\ main' = "free"
            /\ row' = [c \in Cards |-> IF c \in mainCards THEN "done" ELSE row[c]]
            /\ mainCards' = {}
            /\ stepped' = FALSE
            /\ UNCHANGED <<lane, width, retiring, moves, ran>>

\* ---------------------------------------------------------------- the daemon's step
\* Every free lane within the width takes the next ready card, each its own, as many as
\* there are (lanes.go laneStep: the loop over the lanes, nextCard skipping a card
\* another lane holds). In "boundary" a lane takes only when no lane runs; in "batch"
\* the free main session takes every ready card in one turn instead.
Step == /\ \/ /\ Broken = "batch" /\ main = "free" /\ Ready # {}
              /\ main' = "batch"
              /\ mainCards' = Ready
              /\ row' = [c \in Cards |-> IF c \in Ready THEN "main" ELSE row[c]]
              /\ UNCHANGED <<lane, ran>>
           \/ /\ ~(Broken = "batch" /\ main = "free" /\ Ready # {})
              /\ IF Broken = "boundary" /\ Working # {}
                 THEN UNCHANGED <<row, lane, ran>>
                 ELSE LET k == Min(Cardinality(Free), Cardinality(Takeable)) IN
                      \E ls \in SUBSET Free, cs \in SUBSET Takeable :
                        /\ Cardinality(ls) = k /\ Cardinality(cs) = k
                        /\ \E f \in Matches(ls, cs) :
                             /\ lane' = [l \in L |-> IF l \in ls THEN f[l] ELSE lane[l]]
                             /\ row' = [c \in Cards |-> IF c \in cs THEN "lane" ELSE row[c]]
                             /\ ran' = [l \in L |-> ran[l] \/ l \in ls]
              /\ UNCHANGED <<main, mainCards>>
        /\ stepped' = TRUE
        /\ UNCHANGED <<width, retiring, moves>>

Next == \/ Step
        \/ \E c \in Cards : Deal(c)
        \/ \E l \in L : Finish(l)
        \/ \E w \in L : SetWidth(w)
        \/ Talk \/ TalkEnds

Spec == Init /\ [][Next]_vars
\* the daemon steps, every run ends and every comms turn ends
SpecLive == Spec /\ WF_vars(Step) /\ WF_vars(TalkEnds) /\ \A l \in L : WF_vars(Finish(l))

\* ---------------------------------------------------------------- the properties
TypeOK == /\ row \in [Cards -> {"new", "ready", "lane", "main", "done"}]
          /\ lane \in [L -> Cards \cup {None}]
          /\ width \in L /\ retiring \in [L -> BOOLEAN]
          /\ main \in {"free", "talk", "batch"} /\ mainCards \subseteq Cards
          /\ moves \in 0..MaxMoves /\ ran \in [L -> BOOLEAN] /\ stepped \in BOOLEAN

\* no lane runs past her width but one retiring from a width since lowered; so the lanes
\* working are at most the width, and the retiring lanes finishing their runs
WithinWidth == \A l \in L : lane[l] # None => (l <= width \/ retiring[l])
WorkingWithinWidth == Cardinality(Working) <= width + Cardinality({l \in L : retiring[l]})

\* a card runs on at most one lane
OneLanePerCard == \A a, b \in L : (a # b /\ lane[a] # None) => lane[a] # lane[b]

\* the main session never holds a card: it only talks
MainHoldsNoCard == mainCards = {} /\ \A c \in Cards : row[c] # "main"

\* the step leaves no free lane within the width beside a ready card: each lane refreshes
\* on its own, never at another lane's or a turn's boundary
Filled == stepped => ~(\E l \in L : lane[l] = None /\ l <= width) \/ Ready = {}

\* every card dealt to her row is started by a lane
ReadyStarts == \A c \in Cards : row[c] = "ready" ~> row[c] = "lane"
=============================================================================
