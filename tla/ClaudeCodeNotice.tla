---------------------------- MODULE ClaudeCodeNotice ----------------------------
\* One pending notice for the Claude Code hook client (cmd/nova-hook/claude.go).
\* Arrive folds a burst into the queue. Deliver emits once and holds the turn.
\* Arrive while the turn is held does not emit. Boundary releases the hold and,
\* when messages folded during the turn, emits exactly one notice.
\* The wake bit is a label on that delivery, not a second transport: the first
\* Deliver while idle is a wake, a Deliver while busy is not, and Boundary
\* marks the session idle again before it emits.

EXTENDS Naturals

CONSTANT MaxArrive, Broken

VARIABLES queued, holding, turnDelivers, idle, wake

vars == <<queued, holding, turnDelivers, idle, wake>>

Init ==
    /\ queued = 0
    /\ holding = FALSE
    /\ turnDelivers = 0
    /\ idle = TRUE
    /\ wake = FALSE

Arrive(n) ==
    /\ n \in 1..MaxArrive
    /\ queued + n <= MaxArrive
    /\ queued' = queued + n
    /\ UNCHANGED <<holding, turnDelivers, idle, wake>>

Deliver ==
    /\ ~holding
    /\ queued > 0
    /\ wake' = idle
    /\ idle' = FALSE
    /\ queued' = 0
    /\ holding' = TRUE
    /\ turnDelivers' = 1

\* A Stop-hook boundary. Releasing the hold ends the turn. A nonempty queue
\* leaves in this step, labeled a wake, which is the next turn's one notice.
Boundary ==
    /\ \/ holding
       \/ queued > 0
    /\ idle' = TRUE
    /\ IF queued > 0
       THEN /\ queued' = 0
            /\ holding' = TRUE
            /\ turnDelivers' = 1
            /\ wake' = TRUE
       ELSE /\ holding' = FALSE
            /\ turnDelivers' = 0
            /\ UNCHANGED <<queued, wake>>

\* Reversed witness: a second emit while a notice is already held.
DoubleDeliver ==
    /\ Broken = "double"
    /\ holding
    /\ turnDelivers' = turnDelivers + 1
    /\ UNCHANGED <<queued, holding, idle, wake>>

Next ==
    \/ \E n \in 1..MaxArrive : Arrive(n)
    \/ Deliver
    \/ Boundary
    \/ DoubleDeliver

Spec == Init /\ [][Next]_vars /\ WF_vars(Deliver)

TypeOK ==
    /\ queued \in 0..MaxArrive
    /\ holding \in BOOLEAN
    /\ turnDelivers \in {0, 1}
    /\ idle \in BOOLEAN
    /\ wake \in BOOLEAN

\* Deliver requires the turn not already held, and sets the hold. A second
\* Deliver cannot fire until Boundary. turnDelivers stays in {0, 1}.
AtMostOneNoticeHeld == turnDelivers \in {0, 1}

HoldingMeansDelivered == holding => turnDelivers = 1

DeliverTakesTheWholeQueue ==
    [][Deliver => queued' = 0 /\ holding' = TRUE /\ turnDelivers' = 1]_vars

FoldWhileHeld ==
    [][\A n \in 1..MaxArrive :
        (Arrive(n) /\ holding) =>
            (holding' = TRUE /\ turnDelivers' = turnDelivers /\ queued' = queued + n)]_vars

BusyDeliverIsNotAWake ==
    [][Deliver /\ ~idle => wake' = FALSE]_vars

IdleBoundaryWakesOnce ==
    [][(Boundary /\ queued > 0) => (wake' = TRUE /\ queued' = 0 /\ turnDelivers' = 1)]_vars

\* A pending queue is not left undelivered forever: Deliver stays enabled
\* while the turn is not held, so weak fairness takes it. Boundary may take
\* the queue first. This is not a wall-clock bound.
PendingIsTaken == <>(queued = 0 \/ holding)

=============================================================================
