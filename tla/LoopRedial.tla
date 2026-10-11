------------------------------ MODULE LoopRedial ------------------------------
\* A reader loop (nova-swarm member as reader-<m>) and the sprint server's
\* connection, as state. nova-tools pkg/member/member.go (BeatLoop, Tick) and
\* cmd/nova-swarm/sprintredial.go, 2026-10-11: the reader dials the server once,
\* beats and takes its asked reads while the server holds the connection; the
\* server's restart (an adopt, nova-sprint run --listen stopped and started)
\* drops every connection it holds, and the reader must notice, back off one
\* backoff and re-dial, so its beat and tick go on. A reader that does not
\* re-dial parks on the dropped connection: it holds no connection the server
\* answers on, it stops beating, and the sprint asks it nothing.
\*
\* The state the code owns: serverUp (the server is answering), conn (the
\* reader believes it holds a connection; it sent a verb and is owed an
\* answer), held (the server holds that connection), backoff (the wait owed
\* before the next dial), beating (the readers whose beat landed since the
\* server last came up). The outside event: ServerRestart, which drops every
\* connection the server holds.
\*
\* Broken = "none" is the design. Every other value is a reversed witness, a
\* misimplementation one invariant or property must catch:
\*   "noredial"  the reader does not notice the restart (ServerRestart leaves
\*               conn true, the code's client is not rebuilt on error): it holds
\*               a connection the server dropped, NoParkOnDropped fails at once
\*               and BeatsAgain never holds again.
\*
\* Not here: the bytes of the wire, the backoff's doubling (MaxBackoff is one
\* bound in the range of the wait owed), the asks themselves, and the server's
\* own state beyond the connections it holds. Those are sprintredial_test.go.
EXTENDS Naturals

CONSTANTS Readers, MaxBackoff, Broken
ASSUME MaxBackoff \in Nat /\ MaxBackoff > 0
ASSUME Broken \in {"none", "noredial"}

VARIABLES serverUp, conn, held, backoff, beating

vars == <<serverUp, conn, held, backoff, beating>>

TypeOK ==
  /\ serverUp \in BOOLEAN
  /\ conn \in [Readers -> BOOLEAN]
  /\ held \in [Readers -> BOOLEAN]
  /\ backoff \in [Readers -> 0..MaxBackoff]
  /\ beating \in SUBSET Readers

Init ==
  /\ serverUp
  /\ conn = [r \in Readers |-> FALSE]
  /\ held = [r \in Readers |-> FALSE]
  /\ backoff = [r \in Readers |-> 0]
  /\ beating = {}

\* The server restarts and drops every connection it holds. The design's reader
\* notices (conn false) and will re-dial; the broken reader keeps believing it
\* holds the connection.
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

\* A reader holds no connection and owes no backoff: it dials. When the server
\* answers the connection is held and the reader beats; when the server is down
\* the reader waits one backoff and dials again after it. The dial of a reader
\* that believes it holds a connection (the broken design) never happens, which
\* is the park.
Dial(r) ==
  /\ r \in Readers
  /\ conn[r] = FALSE
  /\ backoff[r] = 0
  /\ serverUp' = serverUp
  /\ IF serverUp
     THEN /\ conn' = [conn EXCEPT ![r] = TRUE]
          /\ held' = [held EXCEPT ![r] = TRUE]
          /\ beating' = beating \cup {r}
          /\ backoff' = backoff
     ELSE /\ UNCHANGED <<conn, held, beating>>
          /\ backoff' = [backoff EXCEPT ![r] = MaxBackoff]

\* The backoff is waited out: the reader is ready to dial again.
WaitOut(r) ==
  /\ r \in Readers
  /\ backoff[r] > 0
  /\ backoff' = [backoff EXCEPT ![r] = 0]
  /\ UNCHANGED <<serverUp, conn, held, beating>>

\* A reader whose connection the server still holds goes on beating.
Beat(r) ==
  /\ r \in Readers
  /\ held[r]
  /\ beating' = beating \cup {r}
  /\ UNCHANGED <<serverUp, conn, held, backoff>>

Next ==
  \/ ServerRestart
  \/ ServerBack
  \/ \E r \in Readers: Dial(r) \/ WaitOut(r) \/ Beat(r)

\* No reader waits on a connection the server no longer holds: a reader that
\* believes it holds a connection (conn) is held by the server (held). A reader
\* that just waits a backoff has conn false, so it is never parked on the
\* dropped connection (the card's invariant, one backoff apart).
NoParkOnDropped ==
  \A r \in Readers: conn[r] => held[r]

\* After the server is back for good, every reader beats again: a server that is
\* eventually always up (the restart is an event, not a permanent condition)
\* reaches a state where every reader has re-dialed and its beat landed.
BeatsAgain ==
  (<>[](serverUp)) => <>(\A r \in Readers: r \in beating)

Spec ==
  Init /\ [][Next]_vars
       /\ WF_vars(ServerBack)
       /\ \A r \in Readers: WF_vars(Dial(r)) /\ WF_vars(WaitOut(r)) /\ WF_vars(Beat(r))
=============================================================================
