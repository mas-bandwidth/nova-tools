------------------------------ MODULE Delivery ------------------------------
(* A written report is finished within the poll bound (internal/friend/outbox.go,
   OutboxPoll; docs/SPEC-FRIEND.md, the daemon reads every outbox job). The
   daemon's loop is the watch: the first pass is immediate, and a later pass
   is at most Poll ticks after the report was written. StallOK is the reversed
   witness: time advances while the report stays unfinished, and WithinPoll
   breaks once that wait reaches Poll. *)
EXTENDS Naturals

CONSTANTS Poll, StallOK

VARIABLES clock,   \* ticks since the run started
          report,  \* "none" or "verdict"
          finished,\* the daemon has sent the finish
          owed     \* the clock when the verdict was written; 0 before that

vars == <<clock, report, finished, owed>>

Init == /\ clock = 0
        /\ report = "none"
        /\ finished = FALSE
        /\ owed = 0

Write == /\ report = "none"
         /\ report' = "verdict"
         /\ owed' = clock
         /\ UNCHANGED <<clock, finished>>

\* one poll step finishes a written report and counts as one tick
FinishReport == /\ report = "verdict" /\ ~finished
                /\ finished' = TRUE
                /\ clock' = clock + 1
                /\ UNCHANGED <<report, owed>>

\* time passes only when nothing is owed, and only up to the poll bound
Tick == /\ ~(report = "verdict" /\ ~finished)
        /\ clock < Poll
        /\ clock' = clock + 1
        /\ UNCHANGED <<report, finished, owed>>

\* reversed witness: time passes while a written report stays unfinished
Stall == /\ StallOK
         /\ report = "verdict" /\ ~finished
         /\ clock < Poll
         /\ clock' = clock + 1
         /\ UNCHANGED <<report, finished, owed>>

\* a finished run that has reached the bound has nothing left to do
Idle == /\ finished /\ clock >= Poll
        /\ UNCHANGED vars

Next == Write \/ FinishReport \/ Tick \/ Stall \/ Idle

Spec == Init /\ [][Next]_vars /\ WF_vars(FinishReport)

TypeOK == /\ clock \in 0..(Poll + 1)
          /\ report \in {"none", "verdict"}
          /\ finished \in BOOLEAN
          /\ owed \in 0..Poll
          /\ owed <= clock

\* while a verdict is unfinished, fewer than Poll ticks have passed since it was written
WithinPoll == (report = "verdict" /\ ~finished) => clock - owed < Poll

\* a written report is finished
WrittenFinishes == (report = "verdict" /\ ~finished) ~> finished
=============================================================================
