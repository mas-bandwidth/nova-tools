---------------------------- MODULE GrokHookNotice ----------------------------
\* One pending notice for the hook client's grok adapter (cmd/nova-hook/grok.go).
\* A burst emits one line. Notes that arrive while it is outstanding fold into
\* the next line. A turn boundary consumes the outstanding notice, and the next
\* release emits at most one more line.
\* pendingBusy is the generation not yet applied. MarkBusy publishes the next
\* generation and the busy word in one step. MarkIdle publishes idle at that
\* same generation. A generation is a completed turn only when Observe samples
\* it idle. Broken = "drop-witness" is the reversed case that forgets the
\* generation and leaves the notice stuck. A failed write puts the count back.
\* Bench configurations are owed; this module has no cfg until that run.
EXTENDS Naturals
CONSTANTS Broken
VARIABLES outstanding, waiting, sawBusy, idle, emitted, arrived, folded, pendingBusy

vars == <<outstanding, waiting, sawBusy, idle, emitted, arrived, folded, pendingBusy>>

Init ==
    /\ outstanding = FALSE
    /\ waiting = 0
    /\ sawBusy = FALSE
    /\ idle \in BOOLEAN
    /\ emitted = 0
    /\ arrived = 0
    /\ folded = 0
    /\ pendingBusy = 0

Add ==
    /\ arrived' = arrived + 1
    /\ waiting' = waiting + 1
    /\ UNCHANGED <<outstanding, sawBusy, idle, emitted, folded, pendingBusy>>

\* Busy publishes the next generation and the busy word together. Idle keeps
\* the generation and publishes the idle word. Neither step is half written.
MarkBusy ==
    /\ pendingBusy' = pendingBusy + 1
    /\ idle' = FALSE
    /\ UNCHANGED <<outstanding, waiting, sawBusy, emitted, arrived, folded>>

MarkIdle ==
    /\ idle' = TRUE
    /\ UNCHANGED <<outstanding, waiting, sawBusy, emitted, arrived, folded, pendingBusy>>

\* Account every unobserved busy mark. A witness together with an idle word
\* is a finished turn, even when this read never sampled busy.
Observe ==
    /\ UNCHANGED <<idle, waiting, emitted, arrived, folded>>
    /\ IF Broken = "drop-witness"
         THEN /\ pendingBusy' = 0
              /\ IF outstanding /\ ~idle
                   THEN /\ sawBusy' = TRUE
                        /\ UNCHANGED outstanding
                 ELSE IF outstanding /\ sawBusy /\ idle
                   THEN /\ outstanding' = FALSE
                        /\ sawBusy' = FALSE
                 ELSE UNCHANGED <<outstanding, sawBusy>>
       ELSE /\ pendingBusy' = 0
            /\ IF outstanding /\ pendingBusy > 0 /\ idle
                 THEN /\ outstanding' = FALSE
                      /\ sawBusy' = FALSE
               ELSE IF outstanding /\ (~idle \/ pendingBusy > 0)
                 THEN /\ sawBusy' = TRUE
                      /\ UNCHANGED outstanding
               ELSE IF outstanding /\ sawBusy /\ idle
                 THEN /\ outstanding' = FALSE
                      /\ sawBusy' = FALSE
               ELSE UNCHANGED <<outstanding, sawBusy>>

\* One line, and only while none is outstanding and the witness is already
\* accounted, unless Broken is the witness that writes a second line.
Release ==
    /\ pendingBusy = 0
    /\ waiting > 0
    /\ \/ ~outstanding
       \/ Broken = "second-line"
    /\ emitted' = emitted + 1
    /\ folded' = waiting
    /\ waiting' = 0
    /\ outstanding' = TRUE
    /\ sawBusy' = ~idle
    /\ UNCHANGED <<idle, arrived, pendingBusy>>

Undo ==
    /\ outstanding
    /\ folded > 0
    /\ emitted > 0
    /\ outstanding' = FALSE
    /\ sawBusy' = FALSE
    /\ waiting' = waiting + folded
    /\ folded' = 0
    /\ emitted' = emitted - 1
    /\ UNCHANGED <<idle, arrived, pendingBusy>>

Next == Add \/ MarkBusy \/ MarkIdle \/ Observe \/ Release \/ Undo

Spec == Init /\ [][Next]_vars

\* waiting holds notes not yet in a line. folded holds the count of the line
\* that is outstanding. A second-line release drops the earlier folded count,
\* so Accounted is the witness that release refuses while one line is out.
Accounted == waiting + (IF outstanding THEN folded ELSE 0) <= arrived

OnePending ==
    [][ (emitted' = emitted + 1 /\ outstanding) => Broken = "second-line" ]_vars

\* A generation sampled idle still ends the outstanding notice. drop-witness
\* violates this by clearing pendingBusy and sampling idle.
TurnWitness ==
    [][ (outstanding /\ pendingBusy > 0 /\ idle) => ~outstanding' ]_vars

\* The generation and the busy word are one publication. A step that advances
\* the generation is not idle.
AtomicBusy ==
    [][ pendingBusy' > pendingBusy => ~idle' ]_vars

=============================================================================
