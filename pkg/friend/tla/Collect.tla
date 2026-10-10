------------------------------ MODULE Collect ------------------------------
(* The collect: nova-sprint collect, the coordinator's hand over every friend's
   tree (cmd/nova-sprint/collect.go, internal/sprint/collect.go), and the
   nova-friend daemon's outbox pass over her own (internal/friend/outbox.go),
   one rule kept by both (docs/SPEC-SPRINT.md section 1, collect;
   docs/SPEC-FRIEND.md, the daemon reads every outbox job). A card is dealt
   working onto her row; her runner starts it; the run ENDs with a REPORT.md
   (no verdict yet, LAND or HOLD), written in her own tree or, written ahead,
   in another friend's; or it ENDs with none (a dead lane), or stops at a usage
   limit and is run again. A LAND's Head is origin's tip of the card's branch
   or not yet (she pushes later). The card may be taken back. Each hand asks
   for the rows (a snapshot) and finishes from it; the server takes a finish
   only while the card is working on her row, so one taken leaves it working
   no more and nothing finishes it twice.
   SearchAll = FALSE is the reversed witness of the coordinator reading her own
   tree alone (friend sync before collect); CheckTip = FALSE that of a LAND
   finished at a Head not on origin. *)
EXTENDS Naturals

CONSTANTS Cards, SearchAll, CheckTip

Hands == {"coord", "daemon"}
Trees == {"own", "other"}

VARIABLES col,     \* the server: "ready", "working", "done", "gone"
          run,     \* her runner: "idle", "running", "reported", "dead", "limited"
          rep,     \* REPORT.md: "none", "noverdict", "land", "hold"
          tree,    \* the tree it is written in
          pushed,  \* a LAND's Head is origin's tip of the card's branch
          seen,    \* each hand's last snapshot of the rows
          taken,   \* the finishes the server took, per card
          badLand, \* a LAND was finished at a Head not on origin
          badDead  \* a card was finished as a dead lane that was not one

vars == <<col, run, rep, tree, pushed, seen, taken, badLand, badDead>>

Init == /\ col = [c \in Cards |-> "ready"]
        /\ run = [c \in Cards |-> "idle"]
        /\ rep = [c \in Cards |-> "none"]
        /\ tree = [c \in Cards |-> "own"]
        /\ pushed = [c \in Cards |-> FALSE]
        /\ seen = [h \in Hands |-> [c \in Cards |-> "ready"]]
        /\ taken = [c \in Cards |-> 0]
        /\ badLand = FALSE /\ badDead = FALSE

Deal(c) == /\ col[c] = "ready" /\ col' = [col EXCEPT ![c] = "working"]
           /\ UNCHANGED <<run, rep, tree, pushed, seen, taken, badLand, badDead>>

Start(c) == /\ col[c] = "working" /\ run[c] \in {"idle", "limited"}
            /\ run' = [run EXCEPT ![c] = "running"]
            /\ UNCHANGED <<col, rep, tree, pushed, seen, taken, badLand, badDead>>

EndReport(c) == /\ run[c] = "running"
                /\ run' = [run EXCEPT ![c] = "reported"]
                /\ \E r \in {"noverdict", "land", "hold"}, t \in Trees, p \in BOOLEAN :
                      /\ rep' = [rep EXCEPT ![c] = r]
                      /\ tree' = [tree EXCEPT ![c] = t]
                      /\ pushed' = [pushed EXCEPT ![c] = p]
                /\ UNCHANGED <<col, seen, taken, badLand, badDead>>

EndNoReport(c) == /\ run[c] = "running" /\ run' = [run EXCEPT ![c] = "dead"]
                  /\ UNCHANGED <<col, rep, tree, pushed, seen, taken, badLand, badDead>>

Limit(c) == /\ run[c] = "running" /\ run' = [run EXCEPT ![c] = "limited"]
            /\ UNCHANGED <<col, rep, tree, pushed, seen, taken, badLand, badDead>>

Verdict(c) == /\ rep[c] = "noverdict" /\ \E r \in {"land", "hold"} : rep' = [rep EXCEPT ![c] = r]
              /\ UNCHANGED <<col, run, tree, pushed, seen, taken, badLand, badDead>>

Push(c) == /\ rep[c] = "land" /\ ~pushed[c] /\ pushed' = [pushed EXCEPT ![c] = TRUE]
           /\ UNCHANGED <<col, run, rep, tree, seen, taken, badLand, badDead>>

TakeBack(c) == /\ col[c] \in {"ready", "working"} /\ col' = [col EXCEPT ![c] = "gone"]
               /\ UNCHANGED <<run, rep, tree, pushed, seen, taken, badLand, badDead>>

Ask(h) == /\ seen' = [seen EXCEPT ![h] = col]
          /\ UNCHANGED <<col, run, rep, tree, pushed, taken, badLand, badDead>>

\* the report is in a tree the hand reads: the daemon reads her own, the coordinator every one
Reads(h, c) == tree[c] = "own" \/ (h = "coord" /\ SearchAll)

\* what the hand finishes the card by: a report with a verdict it reads (a LAND only at
\* origin's tip when CheckTip), or, with no report, a dead lane
Finishes(h, c) == \/ /\ rep[c] = "hold" /\ Reads(h, c)
                  \/ /\ rep[c] = "land" /\ Reads(h, c) /\ (CheckTip => pushed[c])
                  \/ /\ rep[c] = "none" /\ run[c] = "dead"

\* the hand finishes from its snapshot; the server takes it only while the card is working
Collect(h, c) == /\ seen[h][c] = "working" /\ Finishes(h, c)
                 /\ IF col[c] = "working"
                      THEN /\ col' = [col EXCEPT ![c] = "done"]
                           /\ taken' = [taken EXCEPT ![c] = @ + 1]
                           /\ badLand' = (badLand \/ (rep[c] = "land" /\ ~pushed[c]))
                           /\ badDead' = (badDead \/ (rep[c] = "none" /\ run[c] # "dead"))
                      ELSE UNCHANGED <<col, taken, badLand, badDead>>
                 /\ UNCHANGED <<run, rep, tree, pushed, seen>>

Next == \/ \E h \in Hands : Ask(h)
        \/ \E c \in Cards : \/ Deal(c) \/ Start(c) \/ EndReport(c) \/ EndNoReport(c) \/ Limit(c)
                            \/ Verdict(c) \/ Push(c) \/ TakeBack(c)
                            \/ \E h \in Hands : Collect(h, c)

Spec == /\ Init /\ [][Next]_vars
        /\ \A h \in Hands : WF_vars(Ask(h))
        /\ \A c \in Cards : WF_vars(Start(c)) /\ WF_vars(Collect("coord", c))

TypeOK == /\ col \in [Cards -> {"ready", "working", "done", "gone"}]
          /\ run \in [Cards -> {"idle", "running", "reported", "dead", "limited"}]
          /\ rep \in [Cards -> {"none", "noverdict", "land", "hold"}]
          /\ tree \in [Cards -> Trees]
          /\ pushed \in [Cards -> BOOLEAN]
          /\ taken \in [Cards -> 0..2]
          /\ badLand \in BOOLEAN /\ badDead \in BOOLEAN

\* a report finished once is never finished twice, whichever hand reads it
FinishedOnce == \A c \in Cards : taken[c] <= 1
\* a LAND finishes only at origin's tip of the card's branch
LandOnTip == ~badLand
\* a dead lane is a run that ENDed with no report, never one stopped at its limit or running
DeadOnlyEnded == ~badDead

\* a working card whose report says HOLD (in any tree), LAND on origin's tip, or whose run
\* ENDed with none, is finished, or leaves her row
Collected == \A c \in Cards :
               (col[c] = "working" /\ (rep[c] = "hold" \/ (rep[c] = "land" /\ pushed[c]) \/ (rep[c] = "none" /\ run[c] = "dead")))
                 ~> col[c] # "working"
=============================================================================
