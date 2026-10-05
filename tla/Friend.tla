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
\* outside: the coordinator pinging (Ping, each ping a fresh nonce, the
\* session having seen every nonce pushed to it: seen), and the session
\* answering (Pong, with any nonce it has seen, so a stale or replayed pong
\* is possible). The pushes the session is owed are counted: silentSaid
\* (the "coordinator silent" pushes) beside outages (the times the
\* connection went silent).
\*
\* The harness limits (limits.go): a turn hits a usage limit or runs out of
\* credits (TurnHitsLimit), marking the friend limited until its reset time
\* (limitUntil). While limited, no turn is delivered into the session
\* (NoTurnWhileLimited). At the reset time, the limit ends (LimitedEnds).
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* each caught by one property below:
\*   "upwithoutpong"        the daemon's own beat makes the friend up: UpOnlyAfterPong
\*   "neverdeaf"            a challenge never times out: DeafAfterWindow
\*   "silenttwice"          "coordinator silent" is pushed every tick of an outage:
\*                          SilentOncePerOutage
\*   "neversilent"          the outage is never said: SilentOncePerOutage
\*   "stalepong"            any nonce the session ever saw answers: OnlyCurrentNonceAnswers
\*   "deliverwhilelimited"  a turn delivers while the harness is limited: NoTurnWhileLimited

EXTENDS Naturals, FiniteSets

CONSTANTS Window, MaxTime, MaxPings, Broken

VARIABLES now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
          limited, limitUntil, delivering
vars == <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
          limited, limitUntil, delivering>>

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
  /\ limited \in BOOLEAN
  /\ limitUntil \in 0..MaxTime
  /\ delivering \in BOOLEAN

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
  /\ limited = FALSE /\ limitUntil = 0 /\ delivering = FALSE

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
  /\ IF limited /\ now + 1 >= limitUntil /\ Broken # "limitneverends"
       THEN /\ limited' = FALSE /\ limitUntil' = 0
       ELSE /\ UNCHANGED <<limited, limitUntil>>
  /\ UNCHANGED <<lastPing, nonce, asked, pongs, seen, answered, delivering>>

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
  /\ UNCHANGED <<now, silentFrom, pongs, silentSaid, outages, answered, limited, limitUntil, delivering>>

\* The session answers with a nonce it has seen (machine.go Pong): the
\* current one ends the challenge; any other changes nothing. The witness
\* takes any seen nonce; answered records which one did.
Pong(n) ==
  /\ n \in seen
  /\ chal # "quiet"
  /\ (n = nonce \/ Broken = "stalepong")
  /\ chal' = "quiet" /\ pongs' = pongs + 1 /\ answered' = n
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, nonce, asked, seen, silentSaid, outages, limited, limitUntil, delivering>>

\* A turn is delivered into the session. While the harness is limited,
\* no turn is delivered unless the broken witness permits it.
Deliver ==
  /\ ~delivering
  /\ IF Broken = "deliverwhilelimited" THEN TRUE ELSE ~limited
  /\ delivering' = TRUE
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, limited, limitUntil>>

\* The turn completes successfully.
TurnSuccess ==
  /\ delivering
  /\ delivering' = FALSE
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, limited, limitUntil>>

\* The turn hits a usage limit or runs out of credits.
TurnHitsLimit ==
  /\ delivering
  /\ ~limited
  /\ now + 2 <= MaxTime
  /\ delivering' = FALSE
  /\ limited' = TRUE
  /\ limitUntil' = now + 2
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered>>

Next ==
  \/ Tick
  \/ Ping
  \/ (\E n \in 1..MaxPings : Pong(n))
  \/ Deliver
  \/ TurnSuccess
  \/ TurnHitsLimit

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

\* No turn is delivered while the harness is limited.
NoTurnWhileLimited == delivering => ~limited

\* A limit ends once the clock reaches its reset time.
LimitedEnds == limited => now < limitUntil

\* No liveness is claimed: the clock is finite here, and DeafAfterWindow
\* already says an open challenge is younger than a window at every state,
\* so once the clock moves a window it is answered or deaf.

=============================================================================
