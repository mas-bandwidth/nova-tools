----------------------------- MODULE Presence -----------------------------
\* The bus presence record (docs/SPEC-FRIEND.md, Presence;
\* internal/friend/presence_bus.go, PresenceState). One record and a clock.
\* Up is exactly a present record whose seen is younger than DownAfter and
\* that is not asleep. A server the beat talks to answers or does not; no
\* action of that server changes the record (ServerChangesNothing).
\*
\* Broken = "frombeat" is the reversed witness: the state is taken from
\* whether that server answers, and StateIsUp fails.

EXTENDS Naturals

CONSTANTS DownAfter, MaxNow, Broken

VARIABLES now, seen, asleep, daemon, server, present

vars == <<now, seen, asleep, daemon, server, present>>

State ==
  IF Broken = "frombeat" THEN (IF server THEN "up" ELSE "down")
  ELSE IF ~present \/ now - seen >= DownAfter THEN "down"
  ELSE IF asleep THEN "asleep"
  ELSE "up"

TypeOK ==
  /\ now \in 0..MaxNow
  /\ seen \in 0..MaxNow
  /\ asleep \in BOOLEAN
  /\ daemon \in BOOLEAN
  /\ server \in BOOLEAN
  /\ present \in BOOLEAN
  /\ State \in {"up", "asleep", "down"}

Init ==
  /\ now = 0
  /\ seen = 0
  /\ asleep = FALSE
  /\ daemon = TRUE
  /\ server = TRUE
  /\ present = TRUE

Tick ==
  /\ now < MaxNow
  /\ now' = now + 1
  /\ UNCHANGED <<seen, asleep, daemon, server, present>>

Write ==
  /\ daemon
  /\ seen' = now
  /\ present' = TRUE
  /\ UNCHANGED <<now, asleep, daemon, server>>

Die ==
  /\ daemon
  /\ daemon' = FALSE
  /\ present' \in {present, FALSE}
  /\ UNCHANGED <<now, seen, asleep, server>>

Sleep ==
  /\ asleep' = TRUE
  /\ UNCHANGED <<now, seen, daemon, server, present>>

Wake ==
  /\ asleep' = FALSE
  /\ UNCHANGED <<now, seen, daemon, server, present>>

ServerDown ==
  /\ server
  /\ server' = FALSE
  /\ UNCHANGED <<now, seen, asleep, daemon, present>>

ServerUp ==
  /\ ~server
  /\ server' = TRUE
  /\ UNCHANGED <<now, seen, asleep, daemon, present>>

Next ==
  \/ Tick
  \/ Write
  \/ Die
  \/ Sleep
  \/ Wake
  \/ ServerDown
  \/ ServerUp

Spec == Init /\ [][Next]_vars

\* Up exactly when the record is present, seen is within DownAfter, and the
\* friend is not asleep. At DownAfter it is down. The server is not in it.
StateIsUp == (State = "up") <=> (present /\ now - seen < DownAfter /\ ~asleep)

ServerChangesNothing ==
  [][ServerDown \/ ServerUp => UNCHANGED <<seen, asleep, now, daemon, present>>]_vars

=============================================================================
