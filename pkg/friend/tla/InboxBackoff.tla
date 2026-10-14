---------------------------- MODULE InboxBackoff ----------------------------
(* The daemon's ask of the sprint server for the cards on her row (pkg/friend/inbox.go inboxStep,
   inboxWait, InboxBackoffMax). She asks every loop (InboxEvery) while the server answers. The
   server runs one verb at a time and an ask the client gave up on at its deadline is still run, so
   asking again at the same cadence after each unanswered ask piled abandoned asks on a server
   already too slow to answer them (emma's log, 48,796 "did not answer" lines). Each unanswered
   ask in a row now doubles the wait up to Max; an answer, a refusal included, ends the back-off.
   Time is in loops (the loop runs every beat), Wait(n) is inboxWait with Max in loops, and time
   cannot pass an ask that is due.

   NoCap = TRUE is the reversed witness of a wait that doubles for ever (WaitBounded fails);
   NoReset = TRUE the witness of an answer that leaves the back-off standing (AnswerEndsBackoff
   fails); WitnessBackedOff is a reversed witness that the back-off is reached. *)
EXTENDS Integers

CONSTANTS Max,       \* InboxBackoffMax in loops
          Horizon,   \* loops the run spans
          Flips,     \* times the server goes down or comes back
          NoCap, NoReset

VARIABLES now,         \* the loop clock
          up,          \* the server answers
          flips,       \* the server's changes so far
          unanswered,  \* unanswered asks in a row (d.inboxUnanswered), kept to 0..6
          lastAsk,     \* when the last ask was made, -1 before the first
          lastGap,     \* the wait owed after the last ask
          answered     \* the last ask was answered

vars == <<now, up, flips, unanswered, lastAsk, lastGap, answered>>

Wait(n) == LET w == IF n = 0 THEN 1 ELSE 2^n IN IF NoCap THEN w ELSE IF w > Max THEN Max ELSE w

TypeOK == /\ now \in 0..Horizon /\ up \in BOOLEAN /\ flips \in 0..Flips
          /\ unanswered \in 0..6 /\ lastAsk \in -1..Horizon /\ lastGap \in 1..64
          /\ answered \in BOOLEAN

Init == /\ now = 0 /\ up = TRUE /\ flips = 0 /\ unanswered = 0
        /\ lastAsk = -1 /\ lastGap = 1 /\ answered = TRUE

Due == lastAsk = -1 \/ now - lastAsk >= lastGap

\* the loop asks when the wait is out (inboxStep): an answer sets the count to zero
Ask == /\ Due
       /\ lastAsk' = now
       /\ IF up
          THEN /\ unanswered' = IF NoReset THEN unanswered ELSE 0
               /\ lastGap' = Wait(IF NoReset THEN unanswered ELSE 0)
               /\ answered' = TRUE
          ELSE /\ unanswered' = IF unanswered < 6 THEN unanswered + 1 ELSE 6
               /\ lastGap' = Wait(IF unanswered < 6 THEN unanswered + 1 ELSE 6)
               /\ answered' = FALSE
       /\ UNCHANGED <<now, up, flips>>

\* time passes only while no ask is due: the loop runs every beat
Tick == /\ now < Horizon /\ ~Due /\ now' = now + 1
        /\ UNCHANGED <<up, flips, unanswered, lastAsk, lastGap, answered>>

Flip == /\ flips < Flips /\ up' = ~up /\ flips' = flips + 1
        /\ UNCHANGED <<now, unanswered, lastAsk, lastGap, answered>>

Ended == now = Horizon /\ ~Due /\ UNCHANGED vars

Next == Ask \/ Tick \/ Flip \/ Ended

Spec == Init /\ [][Next]_vars

\* the wait between asks never passes the bound
WaitBounded == lastGap <= Max
\* an answered ask leaves no back-off standing
AnswerEndsBackoff == answered => unanswered = 0
\* the next ask is never later than the wait owed
NoAskLate == lastAsk = -1 \/ now - lastAsk <= lastGap

\* reversed witness: the back-off is reached
WitnessBackedOff == unanswered < 3
=============================================================================
