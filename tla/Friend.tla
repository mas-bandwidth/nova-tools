------------------------------- MODULE Friend -------------------------------
\* nova-friend's daemon machine (docs/SPEC-FRIEND.md; internal/friend/machine.go:
\* Start, Ping, Pong, Tick, Up). One friend's daemon, as it sees the
\* coordinator and its own session: the connection (a ping from the
\* coordinator within the window, else silent) and the challenge (a ping
\* pushed into the session carries a nonce; the session's own pong with
\* that nonce ends it; a window with no pong is deaf).
\*
\* The state the code owns: conn, lastPing, silentFrom; chal, nonce, asked,
\* pongs. The clock is now, one unit a tick, Window units a window. The
\* outside: the coordinator pinging (Ping with a fresh nonce, RepeatPing
\* with the current nonce, the
\* session having seen every nonce pushed to it: seen), and the session
\* answering (Pong, with any nonce it has seen, so a stale or replayed pong
\* is possible). The pushes the session is owed are counted: silentSaid
\* (the "coordinator silent" pushes) beside outages (the times the
\* connection went silent).
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* each caught by one property below:
\*   "upwithoutpong"  the daemon's own beat makes the friend up: UpOnlyAfterPong
\*   "neverdeaf"      a challenge never times out: DeafAfterWindow
\*   "silenttwice"    "coordinator silent" is pushed every tick of an outage:
\*                    SilentOncePerOutage
\*   "neversilent"    the outage is never said: SilentOncePerOutage
\*   "stalepong"      any nonce the session ever saw answers: OnlyCurrentNonceAnswers

EXTENDS Naturals, FiniteSets

CONSTANTS Window, MaxTime, MaxPings, Broken

VARIABLES now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered
vars == <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered>>

NoNonce == 0

TypeOK ==
  /\ now \in 0..MaxTime
  /\ conn \in {"connected", "silent"}
  /\ lastPing \in 0..MaxTime
  /\ silentFrom \in 0..MaxTime
  /\ chal \in {"quiet", "challenged", "deaf"}
  /\ nonce \in 0..MaxPings
  /\ asked \in 0..MaxTime
  /\ pongs \in 0..MaxPings
  /\ seen \subseteq 1..MaxPings
  /\ silentSaid \in 0..MaxTime
  /\ outages \in 0..MaxTime
  /\ answered \in 0..MaxPings

\* Up is what the daemon reports: the session answered the current challenge
\* and has answered at least once (machine.go Up). The witness lets the
\* daemon's presence alone say up.
Up == IF Broken = "upwithoutpong" THEN conn = "connected" ELSE chal = "quiet" /\ pongs > 0

Init ==
  /\ now = 0
  /\ conn = "connected" /\ lastPing = 0 /\ silentFrom = 0
  /\ chal = "quiet" /\ nonce = NoNonce /\ asked = 0 /\ pongs = 0
  /\ seen = {}
  /\ silentSaid = 0 /\ outages = 0
  /\ answered = NoNonce

\* The clock (machine.go Tick): a window without a ping makes the
\* coordinator silent, said once at that moment; a window challenged with
\* no pong makes the session deaf.
GoesSilent == conn = "connected" /\ now + 1 - lastPing >= Window
Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ IF GoesSilent
       THEN /\ conn' = "silent" /\ silentFrom' = lastPing /\ outages' = outages + 1
            /\ silentSaid' = IF Broken = "neversilent" THEN silentSaid ELSE silentSaid + 1
       ELSE /\ UNCHANGED <<conn, silentFrom, outages>>
            /\ silentSaid' = IF Broken = "silenttwice" /\ conn = "silent" THEN silentSaid + 1 ELSE silentSaid
  /\ chal' = IF chal = "challenged" /\ now + 1 - asked >= Window /\ Broken # "neverdeaf" THEN "deaf" ELSE chal
  /\ UNCHANGED <<lastPing, nonce, asked, pongs, seen, answered>>

\* A ping from the coordinator with a fresh nonce (machine.go Ping): the
\* connection is back (said once; the push is "coordinator back", not
\* counted here), the session is challenged with this nonce whatever it was
\* before, deaf staying deaf until a pong; the session sees the nonce.
Ping ==
  /\ nonce < MaxPings
  /\ nonce' = nonce + 1
  /\ conn' = "connected" /\ lastPing' = now
  /\ chal' = IF chal = "deaf" THEN "deaf" ELSE "challenged"
  /\ asked' = now
  /\ seen' = seen \cup {nonce'}
  /\ UNCHANGED <<now, silentFrom, pongs, silentSaid, outages, answered>>

\* Repeating the current nonce refreshes the connection only: the original
\* deadline and any accepted pong survive both peek/receive and new entries.
RepeatPing ==
  /\ nonce # NoNonce
  /\ conn' = "connected" /\ lastPing' = now
  /\ UNCHANGED <<now, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered>>

\* The session answers with a nonce it has seen (machine.go Pong): the
\* current one ends the challenge; any other changes nothing. The witness
\* takes any seen nonce; answered records which one did.
Pong(n) ==
  /\ n \in seen
  /\ chal # "quiet"
  /\ (n = nonce \/ Broken = "stalepong")
  /\ chal' = "quiet" /\ pongs' = pongs + 1 /\ answered' = n
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, nonce, asked, seen, silentSaid, outages>>

Next ==
  \/ Tick
  \/ Ping
  \/ RepeatPing
  \/ \E n \in 1..MaxPings : Pong(n)

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)

\* ---------------------------------------------------------------- the rules

\* A friend is up only after a session pong: the daemon alone never makes
\* it up, and up means the current challenge is answered.
UpOnlyAfterPong == Up => (pongs >= 1 /\ chal = "quiet")

\* A challenge is open for less than a window: after a window with no
\* pong the session is deaf.
DeafAfterWindow == chal = "challenged" => now - asked < Window

\* "coordinator silent" is pushed exactly once per outage.
SilentOncePerOutage == silentSaid = outages

\* Only the current nonce ends a challenge: a stale or replayed pong
\* changes nothing.
OnlyCurrentNonceAnswers ==
  [][(chal # "quiet" /\ chal' = "quiet") => answered' = nonce]_vars

\* No liveness is claimed: the clock is finite here, and DeafAfterWindow
\* already says an open challenge is younger than a window at every state,
\* so once the clock moves a window it is answered or deaf.

=============================================================================
