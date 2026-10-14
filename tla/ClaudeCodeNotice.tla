---------------------------- MODULE ClaudeCodeNotice ----------------------------
\* One pending ordinary notice for the Claude Code hook client
\* (cmd/nova-hook/claude.go). Arrive folds a burst into the queue. Deliver
\* emits once and holds the turn. Arrive while the turn is held does not emit.
\* Boundary releases the hold and, when messages folded during the turn, emits
\* exactly one ordinary notice.
\*
\* A challenge nonce is NewerNonce, not an ordinary Arrive. Deliver shows the
\* nonce that is pending when the ordinary notice leaves. A newer nonce that
\* arrives while the turn is held is DeliverChallenge: it leaves without
\* waiting for Boundary, and it does not take the ordinary queue. The same
\* nonce is not due again (NewerNonce requires k > shown). One Go offer of a
\* challenge is Arrive for the companions plus NewerNonce for the line; a
\* challenge-only offer while held is NewerNonce alone.
\*
\* The wake bit is a label on a delivery, not a second transport. The first
\* Deliver while idle is a wake, a Deliver while busy is not, and Boundary
\* marks the session idle again before it emits. DeliverChallenge is not a
\* wake. The model has no askIdle override.

EXTENDS Naturals

CONSTANT MaxArrive, MaxNonce, Broken

VARIABLES queued, holding, turnDelivers, idle, wake, prove, shown

vars == <<queued, holding, turnDelivers, idle, wake, prove, shown>>

Init ==
    /\ queued = 0
    /\ holding = FALSE
    /\ turnDelivers = 0
    /\ idle = TRUE
    /\ wake = FALSE
    /\ prove = 0
    /\ shown = 0

Arrive(n) ==
    /\ n \in 1..MaxArrive
    /\ queued + n <= MaxArrive
    /\ queued' = queued + n
    /\ UNCHANGED <<holding, turnDelivers, idle, wake, prove, shown>>

\* k is a newer nonce than the one already shown and any line still pending.
NewerNonce(k) ==
    /\ k \in 1..MaxNonce
    /\ k > prove
    /\ k > shown
    /\ prove' = k
    /\ UNCHANGED <<queued, holding, turnDelivers, idle, wake, shown>>

Deliver ==
    /\ ~holding
    /\ \/ queued > 0
       \/ prove > shown
    /\ wake' = idle
    /\ idle' = FALSE
    /\ queued' = 0
    /\ holding' = TRUE
    /\ turnDelivers' = 1
    /\ IF prove > shown
       THEN /\ shown' = prove
            /\ prove' = 0
       ELSE UNCHANGED <<prove, shown>>

\* A current nonce, or a newer one, leaves while the ordinary notice is held.
\* The ordinary queue and the ordinary turn count stay where they are.
DeliverChallenge ==
    /\ holding
    /\ prove > shown
    /\ shown' = prove
    /\ prove' = 0
    /\ wake' = FALSE
    /\ UNCHANGED <<queued, holding, turnDelivers, idle>>

\* A Stop-hook boundary. Releasing the hold ends the turn. A nonempty queue,
\* or a nonce not yet shown, leaves in this step, labeled a wake.
Boundary ==
    /\ \/ holding
       \/ queued > 0
       \/ prove > shown
    /\ idle' = TRUE
    /\ IF queued > 0 \/ prove > shown
       THEN /\ queued' = 0
            /\ holding' = TRUE
            /\ turnDelivers' = 1
            /\ wake' = TRUE
            /\ IF prove > shown
               THEN /\ shown' = prove
                    /\ prove' = 0
               ELSE UNCHANGED <<prove, shown>>
       ELSE /\ holding' = FALSE
            /\ turnDelivers' = 0
            /\ UNCHANGED <<queued, wake, prove, shown>>

\* Reversed witness: a second ordinary emit while a notice is already held.
DoubleDeliver ==
    /\ Broken = "double"
    /\ holding
    /\ turnDelivers' = turnDelivers + 1
    /\ UNCHANGED <<queued, holding, idle, wake, prove, shown>>

Next ==
    \/ \E n \in 1..MaxArrive : Arrive(n)
    \/ \E k \in 1..MaxNonce : NewerNonce(k)
    \/ Deliver
    \/ DeliverChallenge
    \/ Boundary
    \/ DoubleDeliver

Spec == Init /\ [][Next]_vars /\ WF_vars(Deliver) /\ WF_vars(DeliverChallenge)

TypeOK ==
    /\ queued \in 0..MaxArrive
    /\ holding \in BOOLEAN
    /\ turnDelivers \in {0, 1}
    /\ idle \in BOOLEAN
    /\ wake \in BOOLEAN
    /\ prove \in 0..MaxNonce
    /\ shown \in 0..MaxNonce

\* Deliver requires the turn not already held, and sets the hold. A second
\* ordinary Deliver cannot fire until Boundary. turnDelivers stays in {0, 1}.
\* DeliverChallenge is a challenge line, not that second ordinary notice.
AtMostOneNoticeHeld == turnDelivers \in {0, 1}

HoldingMeansDelivered == holding => turnDelivers = 1

\* A pending line is a newer nonce than the one already shown, or there is none.
ChallengeDueIsNewer == prove = 0 \/ prove > shown

DeliverTakesTheWholeQueue ==
    [][Deliver => queued' = 0 /\ holding' = TRUE /\ turnDelivers' = 1]_vars

FoldWhileHeld ==
    [][\A n \in 1..MaxArrive :
        (Arrive(n) /\ holding) =>
            (holding' = TRUE /\ turnDelivers' = turnDelivers /\ queued' = queued + n)]_vars

\* The challenge leaves the ordinary queue for Boundary. It does not count as
\* the turn's ordinary notice.
ChallengeWhileHeldLeavesTheQueue ==
    [][DeliverChallenge =>
        queued' = queued /\ holding' = TRUE /\ turnDelivers' = turnDelivers]_vars

\* A due nonce is deliverable while the turn is held. Boundary is not required.
ChallengeDoesNotWaitForBoundary ==
    []((holding /\ prove > shown) => ENABLED DeliverChallenge)

BusyDeliverIsNotAWake ==
    [][Deliver /\ ~idle => wake' = FALSE]_vars

ChallengeWhileHeldIsNotAWake ==
    [][DeliverChallenge => wake' = FALSE]_vars

IdleBoundaryWakesOnce ==
    [][(Boundary /\ (queued > 0 \/ prove > shown)) =>
        (wake' = TRUE /\ queued' = 0 /\ turnDelivers' = 1)]_vars

\* A pending ordinary queue is not left undelivered forever: Deliver stays
\* enabled while the turn is not held, so weak fairness takes it. Boundary may
\* take the queue first. A due nonce is shown by Deliver, by DeliverChallenge
\* while the turn is held, or by Boundary. This is not a wall-clock bound.
PendingIsTaken == <>(queued = 0 \/ holding)

DueChallengeIsShown == (prove > shown) ~> (prove = 0)

=============================================================================
