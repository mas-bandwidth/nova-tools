------------------------------- MODULE Friend -------------------------------

\* A friend's daemon speaking to the coordinator over the message bus
\* (machine.go, daemon.go).
\*
\* The daemon's state is its connection (connected or silent, the ping watch
\* machine.go Tick/GoesSilent), the coordinator's last ping (lastPing), the
\* challenge the coordinator has open (chal: quiet, challenged, deaf), the
\* current ping's nonce (nonce), and the session's pongs (pongs). The daemon
\* records presence on every beat; presence is Up only after a session pong
\* (machine.go Up).
\*
\* session-pong.w1: the daemon answers coordinator pings at once (daemonPongs)
\* while a wake check is pending, but presence is the session's, not the
\* daemon's. State added: daemonPongs, busy, whether a turn is running in
\* the session, and owed, a wake check not yet pushed in (daemon.go loop.wake).
\* The clock is now, one unit a tick, Window units a window. The outside: the
\* coordinator pinging (Ping, each ping a fresh nonce, plain or a wake check),
\* the session's turns (Turn, one carrying messages, with the pong line at its
\* head while a challenge is open; WakeTurn, the pong line alone, pushed into
\* a free session for a wake check; TurnEnds), the session having seen every
\* nonce a turn put in front of it (seen), and the session answering (Pong,
\* with any nonce it has seen, so a stale or replayed pong is possible). The pushes the session is owed are counted: silentSaid
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
\*   "daemonpongends"       the daemon's pong ends a wake challenge (session-pong.w1):
\*                          OnlySessionPongEnds
\*   "deliverwhilelimited"  a turn delivers while the harness is limited: NoTurnWhileLimited

EXTENDS Naturals, FiniteSets

CONSTANTS Window, MaxTime, MaxPings, Broken

VARIABLES now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
          daemonPongs, busy, owed, limited, limitUntil
vars == <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
          daemonPongs, busy, owed, limited, limitUntil>>

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
  /\ daemonPongs \in 0..MaxPings
  /\ busy \in BOOLEAN
  /\ owed \in BOOLEAN
  /\ limited \in BOOLEAN
  /\ limitUntil \in 0..MaxTime

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
  /\ daemonPongs = 0 /\ busy = FALSE /\ owed = FALSE
  /\ limited = FALSE /\ limitUntil = 0

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
  /\ IF limited /\ now + 1 >= limitUntil
       THEN /\ limited' = FALSE /\ limitUntil' = 0
       ELSE /\ UNCHANGED <<limited, limitUntil>>
  /\ UNCHANGED <<lastPing, nonce, asked, pongs, seen, answered, daemonPongs, busy, owed>>

\* A ping from the coordinator with a fresh nonce (machine.go Ping;
\* daemon.go loop.ping): the daemon answers it at once (daemonPongs), the
\* connection is back (said once; the push is "coordinator back", not
\* counted here), the session is challenged with this nonce whatever it was
\* before, deaf staying deaf until a pong; a wake check (wake) owes the
\* session a wake turn. The ping itself is never a turn: the session sees the
\* nonce only when a turn carries the pong line. The witness lets the
\* daemon's pong to a wake check end the challenge.
Ping(wake) ==
  /\ nonce < MaxPings
  /\ nonce' = nonce + 1
  /\ daemonPongs' = daemonPongs + 1
  /\ conn' = "connected" /\ lastPing' = now
  /\ chal' = IF wake /\ Broken = "daemonpongends" THEN "quiet"
            ELSE IF chal = "deaf" THEN "deaf" ELSE "challenged"
  /\ asked' = now
  /\ owed' = ((owed \/ wake) /\ chal' # "quiet")
  /\ UNCHANGED <<now, silentFrom, pongs, seen, silentSaid, outages, answered, busy, limited, limitUntil>>

\* A turn carrying messages starts in the free session (daemon.go
\* startBatch): while a challenge is open the pong line for the current nonce
\* rides at its head (loop.head), and an owed wake check is paid by it.
Turn ==
  /\ ~busy
  /\ IF Broken = "deliverwhilelimited" THEN TRUE ELSE ~limited
  /\ busy' = TRUE
  /\ IF chal # "quiet"
       THEN seen' = seen \cup {nonce} /\ owed' = FALSE
       ELSE UNCHANGED <<seen, owed>>
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs, limited, limitUntil>>

\* A wake check owed to a free session with no message waiting is pushed
\* in as its own turn holding only the pong line (daemon.go startWake).
WakeTurn ==
  /\ ~busy /\ owed /\ chal # "quiet"
  /\ IF Broken = "deliverwhilelimited" THEN TRUE ELSE ~limited
  /\ busy' = TRUE /\ owed' = FALSE
  /\ seen' = seen \cup {nonce}
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs, limited, limitUntil>>

TurnEnds ==
  /\ busy
  /\ busy' = FALSE
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, owed, limited, limitUntil>>

TurnHitsLimit ==
  /\ busy
  /\ ~limited
  /\ now + 2 <= MaxTime
  /\ busy' = FALSE
  /\ limited' = TRUE
  /\ limitUntil' = now + 2
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, owed>>

\* The session answers with a nonce it has seen (machine.go Pong): the
\* current one ends the challenge; any other changes nothing. The witness
\* takes any seen nonce; answered records which one did.
Pong(n) ==
  /\ n \in seen
  /\ chal # "quiet"
  /\ (n = nonce \/ Broken = "stalepong")
  /\ chal' = "quiet" /\ pongs' = pongs + 1 /\ answered' = n /\ owed' = FALSE
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, nonce, asked, seen, silentSaid, outages, daemonPongs, busy, limited, limitUntil>>

Next ==
  \/ Tick
  \/ \E wake \in BOOLEAN : Ping(wake)
  \/ Turn
  \/ WakeTurn
  \/ TurnEnds
  \/ TurnHitsLimit
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
\* changes nothing. (A challenge ended by anything but a pong is
\* OnlySessionPongEnds's to catch.)
OnlyCurrentNonceAnswers ==
  [][(chal # "quiet" /\ chal' = "quiet" /\ pongs' = pongs + 1) => answered' = nonce]_vars

\* Only the session's pong ends a challenge (session-pong.w1): the daemon's
\* own answer to a ping, wake check or not, proves transport and nothing
\* more, so a wake check the session never answers goes deaf while the
\* daemon pongs.
OnlySessionPongEnds ==
  [][(chal # "quiet" /\ chal' = "quiet") => pongs' = pongs + 1]_vars

\* A wake check is owed only while a challenge is open: the session's pong
\* pays it, so a wake turn never carries an answered nonce.
OwedOnlyWhileAsked == owed => chal # "quiet"

\* No turn is delivered while the harness is limited.
NoTurnWhileLimited == busy => ~limited

\* A limit ends once the clock reaches its reset time.
LimitedEnds == limited => now < limitUntil

\* No liveness is claimed: the clock is finite here, and DeafAfterWindow
\* already says an open challenge is younger than a window at every state,
\* so once the clock moves a window it is answered or deaf.

=============================================================================
