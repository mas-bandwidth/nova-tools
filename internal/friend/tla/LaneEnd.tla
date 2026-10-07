---------------------------- MODULE LaneEnd ----------------------------
(* A one-shot lane's end is its card's finish (internal/friend/lane_end.go,
   docs/SPEC-FRIEND.md "a lane's end is a finish"). A lane begins a card and
   marks it started in lanes.json; the run ends (exit, cap, error) or the
   daemon goes away under it (stop, kill, crash). At the run's end the
   friend's REPORT.md is the finish when she wrote one; else the lane writes
   one and sends the failed finish, which the server may not answer. A daemon
   starting up ends every card still marked. Friend sync finishes any working
   card with a report. LaneWrites = FALSE is the reversed witness: the lane
   before this change, which wrote nothing and left the card working.

   A lane's wall is capped by its card's tier (lane_cap.go,
   a-lane-is-capped-by-its-tier.w1): CapEnds is the daemon ending a run at
   the cap, the lane's report a HOLD that names it; the sprint reads the cap
   off the finish and re-deals the card once, one tier up, before the cap
   counts as a failure (internal/sprint lane_cap.go, Redeal). RedealOnce
   holds; CapOnce = FALSE is its reversed witness, a sprint that re-deals
   every capped finish (MCLaneEndBrokenCapAlways.cfg). *)
EXTENDS FiniteSets, Naturals

CONSTANTS Cards, LaneWrites, CapOnce

VARIABLES store,    \* the sprint's word on the card: "working" or "finished"
          run,      \* "none", "running", "ended" (the lane saw it end), "gone" (its daemon went away)
          started,  \* the mark in lanes.json
          report,   \* outbox REPORT.md: "none", "friend" (hers), "lane" (the lane's)
          up,       \* a daemon is running
          capped,   \* the lane's report names its cap (the run was ended at it)
          redeals   \* the re-deals a cap has spent on the card

vars == <<store, run, started, report, up, capped, redeals>>

Init == /\ store = [c \in Cards |-> "working"]
        /\ run = [c \in Cards |-> "none"]
        /\ started = [c \in Cards |-> FALSE]
        /\ report = [c \in Cards |-> "none"]
        /\ up = TRUE
        /\ capped = [c \in Cards |-> FALSE]
        /\ redeals = [c \in Cards |-> 0]

\* a free lane hands the card: marked started before the turn runs
Begin(c) == /\ up /\ run[c] = "none" /\ report[c] = "none"
            /\ run' = [run EXCEPT ![c] = "running"]
            /\ started' = [started EXCEPT ![c] = TRUE]
            /\ UNCHANGED <<store, report, up, capped, redeals>>

\* the friend writes her REPORT.md during the run
FriendReports(c) == /\ run[c] = "running" /\ report[c] = "none"
                    /\ report' = [report EXCEPT ![c] = "friend"]
                    /\ UNCHANGED <<store, run, started, up, capped, redeals>>

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
                    /\ UNCHANGED <<up, capped, redeals>>

\* the card's wall reaches its tier's cap: the daemon ends the run, and the
\* lane's report (when hers is not there) names the cap
CapEnds(c, sent) == /\ up /\ run[c] = "running"
                    /\ run' = [run EXCEPT ![c] = "ended"]
                    /\ capped' = [capped EXCEPT ![c] = report[c] = "none" /\ LaneWrites]
                    /\ EndCard(c, sent)
                    /\ UNCHANGED <<up, redeals>>

\* the sprint may re-deal a capped finish: once (CapOnce), else every time
\* (the reversed witness, bounded at two so the states are finite)
CanRedeal(c) == capped[c] /\ (redeals[c] = 0 \/ ~CapOnce) /\ redeals[c] < 2

\* the sprint takes a capped finish back as a new deal one tier up: a new job,
\* begun again by a lane, with no report yet
Redeal(c) == /\ store[c] = "finished" /\ CanRedeal(c)
             /\ store' = [store EXCEPT ![c] = "working"]
             /\ run' = [run EXCEPT ![c] = "none"]
             /\ report' = [report EXCEPT ![c] = "none"]
             /\ capped' = [capped EXCEPT ![c] = FALSE]
             /\ redeals' = [redeals EXCEPT ![c] = @ + 1]
             /\ UNCHANGED <<started, up>>

\* the daemon stops, is killed or crashes: its runs are gone, the marks stay
Down == /\ up
        /\ up' = FALSE
        /\ run' = [c \in Cards |-> IF run[c] = "running" THEN "gone" ELSE run[c]]
        /\ UNCHANGED <<store, started, report, capped, redeals>>

\* a daemon starts up and ends every card still marked
Restart(sent) ==
    /\ ~up /\ up' = TRUE
    /\ started' = [c \in Cards |-> FALSE]
    /\ report' = [c \in Cards |-> IF started[c] /\ report[c] = "none" /\ LaneWrites THEN "lane" ELSE report[c]]
    /\ store' = [c \in Cards |-> IF sent /\ started[c] /\ report[c] = "none" /\ LaneWrites THEN "finished" ELSE store[c]]
    /\ UNCHANGED <<run, capped, redeals>>

\* friend sync finishes a working card from its REPORT.md
Sync(c) == /\ store[c] = "working" /\ report[c] # "none"
           /\ store' = [store EXCEPT ![c] = "finished"]
           /\ UNCHANGED <<run, started, report, up, capped, redeals>>

Next == \/ \E c \in Cards : Begin(c) \/ FriendReports(c) \/ Sync(c) \/ Redeal(c) \/ \E s \in BOOLEAN : RunEnds(c, s) \/ CapEnds(c, s)
        \/ Down
        \/ \E s \in BOOLEAN : Restart(s)

Spec == Init /\ [][Next]_vars /\ WF_vars(Restart(FALSE))
        /\ \A c \in Cards : WF_vars(Sync(c)) /\ WF_vars(RunEnds(c, FALSE)) /\ WF_vars(Redeal(c)) /\ SF_vars(Begin(c))

TypeOK == /\ store \in [Cards -> {"working", "finished"}]
          /\ run \in [Cards -> {"none", "running", "ended", "gone"}]
          /\ started \in [Cards -> BOOLEAN]
          /\ report \in [Cards -> {"none", "friend", "lane"}]
          /\ up \in BOOLEAN
          /\ capped \in [Cards -> BOOLEAN]
          /\ redeals \in [Cards -> 0..2]

\* no card stays working after its run with no way to be finished: a report for
\* sync to read, or a mark for the next daemon to end
NoOrphan == \A c \in Cards : (store[c] = "working" /\ run[c] \in {"ended", "gone"}) => (report[c] # "none" \/ started[c])

\* the lane never writes over the friend's own report
HersStands == [][\A c \in Cards : report[c] = "friend" => report'[c] = "friend"]_vars

\* a cap re-deals a card once: the second cap is a failure
RedealOnce == \A c \in Cards : redeals[c] <= 1

\* every card begun is finished in the end, with no re-deal left to take
Finished == \A c \in Cards : run[c] # "none" ~> (store[c] = "finished" /\ ~CanRedeal(c))
=============================================================================
