---------------------------- MODULE LaneEnd ----------------------------
(* A one-shot lane's end is its card's finish (internal/friend/lane_end.go,
   docs/SPEC-FRIEND.md "a lane's end is a finish"). A lane begins a card and
   marks it started in lanes.json; the run ends (exit, cap, error) or the
   daemon goes away under it (stop, kill, crash). At the run's end the
   friend's REPORT.md is the finish when she wrote one; else the lane writes
   one and sends the failed finish, which the server may not answer. A daemon
   starting up ends every card still marked. Friend sync finishes any working
   card with a report. LaneWrites = FALSE is the reversed witness: the lane
   before this change, which wrote nothing and left the card working. *)
EXTENDS FiniteSets

CONSTANTS Cards, LaneWrites

VARIABLES store,    \* the sprint's word on the card: "working" or "finished"
          run,      \* "none", "running", "ended" (the lane saw it end), "gone" (its daemon went away)
          started,  \* the mark in lanes.json
          report,   \* outbox REPORT.md: "none", "friend" (hers), "lane" (the lane's)
          up        \* a daemon is running

vars == <<store, run, started, report, up>>

Init == /\ store = [c \in Cards |-> "working"]
        /\ run = [c \in Cards |-> "none"]
        /\ started = [c \in Cards |-> FALSE]
        /\ report = [c \in Cards |-> "none"]
        /\ up = TRUE

\* a free lane hands the card: marked started before the turn runs
Begin(c) == /\ up /\ run[c] = "none" /\ report[c] = "none"
            /\ run' = [run EXCEPT ![c] = "running"]
            /\ started' = [started EXCEPT ![c] = TRUE]
            /\ UNCHANGED <<store, report, up>>

\* the friend writes her REPORT.md during the run
FriendReports(c) == /\ run[c] = "running" /\ report[c] = "none"
                    /\ report' = [report EXCEPT ![c] = "friend"]
                    /\ UNCHANGED <<store, run, started, up>>

\* the lane ends the card: hers stands; else it writes one (when it does) and
\* the finish it sends is answered or not
EndCard(c, sent) ==
    /\ started' = [started EXCEPT ![c] = FALSE]
    /\ IF report[c] = "none" /\ LaneWrites
         THEN /\ report' = [report EXCEPT ![c] = "lane"]
              /\ store' = IF sent THEN [store EXCEPT ![c] = "finished"] ELSE store
         ELSE UNCHANGED <<report, store>>

RunEnds(c, sent) == /\ up /\ run[c] = "running"
                    /\ run' = [run EXCEPT ![c] = "ended"]
                    /\ EndCard(c, sent)
                    /\ UNCHANGED up

\* the daemon stops, is killed or crashes: its runs are gone, the marks stay
Down == /\ up
        /\ up' = FALSE
        /\ run' = [c \in Cards |-> IF run[c] = "running" THEN "gone" ELSE run[c]]
        /\ UNCHANGED <<store, started, report>>

\* a daemon starts up and ends every card still marked
Restart(sent) ==
    /\ ~up /\ up' = TRUE
    /\ started' = [c \in Cards |-> FALSE]
    /\ report' = [c \in Cards |-> IF started[c] /\ report[c] = "none" /\ LaneWrites THEN "lane" ELSE report[c]]
    /\ store' = [c \in Cards |-> IF sent /\ started[c] /\ report[c] = "none" /\ LaneWrites THEN "finished" ELSE store[c]]
    /\ UNCHANGED run

\* friend sync finishes a working card from its REPORT.md
Sync(c) == /\ store[c] = "working" /\ report[c] # "none"
           /\ store' = [store EXCEPT ![c] = "finished"]
           /\ UNCHANGED <<run, started, report, up>>

Next == \/ \E c \in Cards : Begin(c) \/ FriendReports(c) \/ Sync(c) \/ \E s \in BOOLEAN : RunEnds(c, s)
        \/ Down
        \/ \E s \in BOOLEAN : Restart(s)

Spec == Init /\ [][Next]_vars /\ WF_vars(Restart(FALSE)) /\ \A c \in Cards : WF_vars(Sync(c)) /\ WF_vars(RunEnds(c, FALSE))

TypeOK == /\ store \in [Cards -> {"working", "finished"}]
          /\ run \in [Cards -> {"none", "running", "ended", "gone"}]
          /\ started \in [Cards -> BOOLEAN]
          /\ report \in [Cards -> {"none", "friend", "lane"}]
          /\ up \in BOOLEAN

\* no card stays working after its run with no way to be finished: a report for
\* sync to read, or a mark for the next daemon to end
NoOrphan == \A c \in Cards : (store[c] = "working" /\ run[c] \in {"ended", "gone"}) => (report[c] # "none" \/ started[c])

\* the lane never writes over the friend's own report
HersStands == [][\A c \in Cards : report[c] = "friend" => report'[c] = "friend"]_vars

\* every card begun is finished in the end
Finished == \A c \in Cards : run[c] # "none" ~> store[c] = "finished"
=============================================================================
