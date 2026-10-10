---------------------------- MODULE OneLane ----------------------------
(* One live lane per card (internal/friend/one_lane.go, docs/SPEC-FRIEND.md
   "one lane per card"). Lanes of any daemon (two daemons on one working
   directory, or another friend's) may be handed the same card. A lane claims
   the card's job by its lane mark before its first turn: created only where
   there is none, kept while its lane runs (refreshed), free again only when
   the lane that holds it is gone and the mark goes stale. A lane that finishes
   the card writes "ended" on the mark; a lane still running a card whose mark
   names another lane, or says ended, is ended (its run stopped, nothing
   finished by it). Claim = FALSE is the reversed witness: lanes that start
   without the mark, as before this change, checking only that the card is not
   finished. *)
EXTENDS FiniteSets, Naturals

CONSTANTS Lanes, Claim

VARIABLES mark,     \* the job's lane mark: "none", a lane (running it), or "ended"
          run,      \* each lane: "idle", "running", "gone" (its daemon went away under it)
          finished  \* the card's report is written

vars == <<mark, run, finished>>

Init == /\ mark = "none"
        /\ run = [l \in Lanes |-> "idle"]
        /\ finished = FALSE

\* a free lane is handed the card: with the claim, only where the mark is none
Start(l) == /\ run[l] = "idle" /\ ~finished
            /\ IF Claim THEN mark = "none" ELSE TRUE
            /\ mark' = l
            /\ run' = [run EXCEPT ![l] = "running"]
            /\ UNCHANGED finished

\* the lane's run writes the report and the lane ends the card: "ended" on the mark
Finish(l) == /\ run[l] = "running"
             /\ finished' = TRUE
             /\ mark' = "ended"
             /\ run' = [run EXCEPT ![l] = "idle"]

\* a running lane whose mark names another lane or says ended is ended: no finish
Ended(l) == /\ run[l] = "running" /\ mark # l
            /\ run' = [run EXCEPT ![l] = "idle"]
            /\ UNCHANGED <<mark, finished>>

\* the lane's daemon goes away under it: the mark stays, no longer refreshed
Gone(l) == /\ run[l] = "running"
           /\ run' = [run EXCEPT ![l] = "gone"]
           /\ UNCHANGED <<mark, finished>>

\* a mark not refreshed for LaneMarkStale is a gone lane's: a new lane may claim the card
Stale == /\ mark \in Lanes /\ run[mark] = "gone"
         /\ mark' = "none"
         /\ UNCHANGED <<run, finished>>

\* a gone lane's daemon starts again: it ends the card its lane began (endStarted, lane_end.go),
\* its failed report written and "ended" on the mark, and the lane is free
Back(l) == /\ run[l] = "gone"
           /\ run' = [run EXCEPT ![l] = "idle"]
           /\ finished' = TRUE
           /\ mark' = "ended"

\* the card finished and every lane free: nothing is owed
Done == /\ finished /\ \A l \in Lanes : run[l] = "idle"
        /\ UNCHANGED vars

Next == \/ \E l \in Lanes : Start(l) \/ Finish(l) \/ Ended(l) \/ Gone(l) \/ Back(l)
        \/ Stale
        \/ Done

Spec == Init /\ [][Next]_vars /\ \A l \in Lanes : WF_vars(Ended(l))

TypeOK == /\ mark \in {"none", "ended"} \cup Lanes
          /\ run \in [Lanes -> {"idle", "running", "gone"}]
          /\ finished \in BOOLEAN

\* never two live lanes on one card
OneLive == Cardinality({l \in Lanes : run[l] = "running"}) <= 1

\* a finished card's other lanes all end
NoneLeft == finished ~> \A l \in Lanes : run[l] # "running"
=============================================================================
