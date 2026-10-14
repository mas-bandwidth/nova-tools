---------------------------- MODULE GrokHookNotice ----------------------------
\* One pending notice for the hook client's grok adapter (cmd/nova-hook/grok.go).
\* A burst emits one line. Notes that arrive while it is outstanding fold into
\* the next line. A turn boundary consumes the outstanding notice, and the next
\* release emits at most one more line. A failed write puts the count back.
\* Bench configurations are owed; this module has no cfg until that run.
EXTENDS Naturals
CONSTANTS Broken
VARIABLES outstanding, waiting, sawBusy, idle, emitted, arrived, folded

vars == <<outstanding, waiting, sawBusy, idle, emitted, arrived, folded>>

Init ==
    /\ outstanding = FALSE
    /\ waiting = 0
    /\ sawBusy = FALSE
    /\ idle \in BOOLEAN
    /\ emitted = 0
    /\ arrived = 0
    /\ folded = 0

Add ==
    /\ arrived' = arrived + 1
    /\ waiting' = waiting + 1
    /\ UNCHANGED <<outstanding, sawBusy, idle, emitted, folded>>

\* A busy reading while a notice is out marks that a turn ran. An idle reading
\* after that mark consumes the notice. An idle wake that never starts a turn
\* stays outstanding, so a later release writes no second line.
Observe ==
    /\ idle' \in BOOLEAN
    /\ IF outstanding /\ ~idle'
         THEN /\ sawBusy' = TRUE
              /\ UNCHANGED <<outstanding, waiting, emitted, arrived, folded>>
       ELSE IF outstanding /\ sawBusy /\ idle'
         THEN /\ outstanding' = FALSE
              /\ sawBusy' = FALSE
              /\ UNCHANGED <<waiting, emitted, arrived, folded>>
       ELSE UNCHANGED <<outstanding, waiting, sawBusy, emitted, arrived, folded>>

\* One line, and only while none is outstanding, unless Broken is the witness
\* that writes a second line over the one already out.
Release ==
    /\ waiting > 0
    /\ \/ ~outstanding
       \/ Broken = "second-line"
    /\ emitted' = emitted + 1
    /\ folded' = waiting
    /\ waiting' = 0
    /\ outstanding' = TRUE
    /\ sawBusy' = ~idle
    /\ UNCHANGED <<idle, arrived>>

Undo ==
    /\ outstanding
    /\ folded > 0
    /\ emitted > 0
    /\ outstanding' = FALSE
    /\ sawBusy' = FALSE
    /\ waiting' = waiting + folded
    /\ folded' = 0
    /\ emitted' = emitted - 1
    /\ UNCHANGED <<idle, arrived>>

Next == Add \/ Observe \/ Release \/ Undo

Spec == Init /\ [][Next]_vars

\* waiting holds notes not yet in a line. folded holds the count of the line
\* that is outstanding. A second-line release drops the earlier folded count,
\* so Accounted is the witness that release refuses while one line is out.
Accounted == waiting + (IF outstanding THEN folded ELSE 0) <= arrived

OnePending ==
    [][ (emitted' = emitted + 1 /\ outstanding) => Broken = "second-line" ]_vars

=============================================================================
