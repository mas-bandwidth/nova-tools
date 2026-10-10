------------------------------ MODULE Presence ------------------------------
\* The bus presence record (docs/SPEC-FRIEND.md, Presence;
\* internal/friend/storepresence.go PresenceState). Variables: the record's
\* seen and asleep, the clock, whether the daemon runs, and whether the server
\* answers. Up is exactly a fresh record that is not asleep, whatever the
\* server does. No action of the server changes that state.
\*
\* Broken = "none" is the design. "serverbeat" derives up from the server and
\* is refused by PresenceIndependent.

EXTENDS Naturals

CONSTANTS DownAfter, MaxTime, Broken

VARIABLES now, seen, asleep, running, server, have

vars == <<now, seen, asleep, running, server, have>>

TypeOK ==
  /\ now \in 0..MaxTime
  /\ seen \in 0..MaxTime
  /\ asleep \in BOOLEAN
  /\ running \in BOOLEAN
  /\ server \in BOOLEAN
  /\ have \in BOOLEAN

Init ==
  /\ now = 0
  /\ seen = 0
  /\ asleep = FALSE
  /\ running = TRUE
  /\ server = TRUE
  /\ have = FALSE

fresh == have /\ now >= seen /\ (now - seen) < DownAfter

\* asleep here is the record's flag. The string is the state's name.
State ==
  IF Broken = "serverbeat"
  THEN IF server /\ fresh /\ ~asleep THEN "up" ELSE "down"
  ELSE IF ~fresh THEN "down"
       ELSE IF asleep THEN "asleep"
       ELSE "up"

PresenceIndependent == (State = "up") <=> (fresh /\ ~asleep)

ServerLeavesPresence == [][server' /= server => State' = State]_vars

Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ UNCHANGED <<seen, asleep, running, server, have>>

Write ==
  /\ running
  /\ seen' = now
  /\ have' = TRUE
  /\ UNCHANGED <<now, asleep, running, server>>

Die ==
  /\ running
  /\ running' = FALSE
  /\ UNCHANGED <<now, seen, asleep, server, have>>

Sleep ==
  /\ ~asleep
  /\ asleep' = TRUE
  /\ UNCHANGED <<now, seen, running, server, have>>

Wake ==
  /\ asleep
  /\ asleep' = FALSE
  /\ UNCHANGED <<now, seen, running, server, have>>

ServerDown ==
  /\ server
  /\ server' = FALSE
  /\ UNCHANGED <<now, seen, asleep, running, have>>

ServerUp ==
  /\ ~server
  /\ server' = TRUE
  /\ UNCHANGED <<now, seen, asleep, running, have>>

Next == Tick \/ Write \/ Die \/ Sleep \/ Wake \/ ServerDown \/ ServerUp

Spec == Init /\ [][Next]_vars

=============================================================================
