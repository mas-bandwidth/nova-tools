----------------------------- MODULE LoopRedial ------------------------------
\* A reader loop (nova-swarm member as reader-<m>) and the sprint server's
\* connection, as state. nova-tools pkg/member/member.go (BeatLoop, Tick) and
\* cmd/nova-swarm/sprintredial.go, 2026-10-11: every verb the reader sends opens
\* a fresh connection; a verb the server did not answer is an error, and the
\* reader notices the error on that verb, backs off one second doubling to
\* MaxBackoff and re-dials with a rebuilt client, so it never parks on a
\* connection the server dropped and its beat and tick go on. A reader that does
\* not re-dial parks on the dropped connection: it holds no connection the server
\* answers on, it stops beating, and the sprint asks it nothing.
\*
\* The state the code owns: serverUp (the server is answering), conn (the reader
\* holds a connection it sent a verb on), held (the server holds that
\* connection), backoff (the wait owed before the next dial), beating (the
\* readers whose beat landed since the server last came up). The outside event:
\* ServerRestart, which drops every connection the server holds.
\*
\* Broken = "none" is the design. Every other value is a reversed witness, a
\* misimplementation one invariant or property must catch:
\*   "noredial"  the reader does not notice the restart (ServerRestart leaves
\*               conn true, the code's client is not rebuilt on error): it holds
\*               a connection the server dropped, NoParkOnDropped fails at once
\*               and BeatsAgain never holds again.
\*   "nobound"   a failed verb doubles the backoff without the cap: it grows past
\*               MaxBackoff, and BackoffBounded fails.
\*
\* Not here: the bytes of the wire, the asks themselves, and the server's own
\* state beyond the connections it holds. Those are sprintredial_test.go.
EXTENDS Naturals

CONSTANTS Readers, MaxBackoff, Broken
ASSUME MaxBackoff \in Nat /\ MaxBackoff > 0
ASSUME Broken \in {"none", "noredial", "nobound"}

VARIABLES serverUp, conn, held, backoff, beating

vars == <<serverUp, conn, held, backoff, beating>>

TypeOK ==
  /\ serverUp \in BOOLEAN
  /\ conn \in [Readers -> BOOLEAN]
  /\ held \in [Readers -> BOOLEAN]
  /\ backoff \in [Readers -> Nat]
  /\ beating \in SUBSET Readers

Init ==
  /\ serverUp = TRUE
  /\ conn = [r \in Readers |-> FALSE]
  /\ held = [r \in Readers |-> FALSE]
  /\ backoff = [r \in Readers |-> 0]
  /\ beating = {}

\* The wait a reader owes after a failed verb: one the first time, then twice
\* the last, capped at MaxBackoff. The nobound reader never caps, so a failed
\* verb grows the wait past the bound.
NextBackoff(r) ==
  IF backoff[r] = 0
  THEN 1
  ELSE IF Broken = "nobound"
       THEN 2 * backoff[r]
       ELSE IF 2 * backoff[r] <= MaxBackoff THEN 2 * backoff[r] ELSE MaxBackoff

\* The server restarts and drops every connection it holds. The reader notices
\* on its next verb's error (Redial), not here: the connection is gone, but the
\* reader's backoff is unchanged until that verb fails. The noredial reader keeps
\* believing it holds the connection.
ServerRestart ==
  /\ serverUp
  /\ serverUp' = FALSE
  /\ held' = [r \in Readers |-> FALSE]
  /\ conn' = IF Broken = "noredial" THEN conn ELSE [r \in Readers |-> FALSE]
  /\ UNCHANGED <<backoff, beating>>

\* The server is back.
ServerBack ==
  /\ ~serverUp
  /\ serverUp' = TRUE
  /\ UNCHANGED <<conn, held, backoff, beating>>

\* A reader that holds no connection sends its next verb on a fresh connection:
\* when the server answers the reader is connected and held and its beat lands;
\* when the server is down the verb fails and the reader backs off one more step,
\* so the next verb's error notices the restart. A successful verb clears the
\* wait; a failed verb doubles it, capped at MaxBackoff.
Redial(r) ==
  /\ r \in Readers
  /\ conn[r] = FALSE
  /\ serverUp' = serverUp
  /\ IF serverUp
     THEN /\ conn' = [conn EXCEPT ![r] = TRUE]
          /\ held' = [held EXCEPT ![r] = TRUE]
          /\ beating' = beating \cup {r}
          /\ backoff' = [backoff EXCEPT ![r] = 0]
     ELSE /\ UNCHANGED <<conn, held, beating>>
          /\ backoff' = [backoff EXCEPT ![r] = NextBackoff(r)]

\* A reader whose connection the server still holds goes on beating.
Beat(r) ==
  /\ r \in Readers
  /\ held[r]
  /\ beating' = beating \cup {r}
  /\ UNCHANGED <<serverUp, conn, held, backoff>>

Next ==
  \/ ServerRestart
  \/ ServerBack
  \/ \E r \in Readers: Redial(r) \/ Beat(r)

\* No reader waits on a connection the server no longer holds: a reader that
\* holds a connection (conn) is held by the server (held). A reader that just
\* waits a backoff has conn false, so it is never parked on the dropped
\* connection (the card's invariant, one backoff apart).
NoParkOnDropped ==
  \A r \in Readers: conn[r] => held[r]

\* After the server is back for good, every reader beats again: a server that is
\* eventually always up (the restart is an event, not a permanent condition)
\* reaches a state where every reader has re-dialed and its beat landed.
BeatsAgain ==
  (<>[](serverUp)) => <>(\A r \in Readers: r \in beating)

\* The wait a reader owes never passes MaxBackoff.
BackoffBounded ==
  \A r \in Readers: backoff[r] <= MaxBackoff

Spec ==
  Init /\ [][Next]_vars
       /\ WF_vars(ServerBack)
       /\ \A r \in Readers: WF_vars(Redial(r)) /\ WF_vars(Beat(r))
=============================================================================
