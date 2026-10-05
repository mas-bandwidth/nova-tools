--------------------------- MODULE MCFriendLimited ---------------------------
\* nova-friend's daemon machine with usage limits (limits-mean-down).
\* Extends Friend with limited state: TurnHitsLimit, limitedEnds, NoTurnWhileLimited.

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

Up == chal = "quiet" /\ pongs > 0

Init ==
  /\ now = 0
  /\ conn = "connected"
  /\ lastPing = 0
  /\ silentFrom = 0
  /\ chal = "quiet"
  /\ nonce = NoNonce
  /\ asked = 0
  /\ pongs = 0
  /\ seen = {}
  /\ silentSaid = 0 /\ outages = 0
  /\ answered = NoNonce
  /\ daemonPongs = 0 /\ busy = FALSE /\ owed = FALSE
  /\ limited = FALSE /\ limitUntil = 0

Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ IF now + 1 - lastPing >= Window
       THEN /\ conn' = "silent"
            /\ silentFrom' = IF conn = "connected" THEN now + 1 ELSE silentFrom
            /\ outages' = IF conn = "connected" THEN outages + 1 ELSE outages
            /\ silentSaid' = IF conn = "connected" /\ Broken # "neversilent"
                               THEN silentSaid + 1
                               ELSE IF Broken = "silenttwice" THEN silentSaid + 1 ELSE silentSaid
       ELSE /\ UNCHANGED <<conn, silentFrom, outages>>
            /\ silentSaid' = IF Broken = "silenttwice" /\ conn = "silent" THEN silentSaid + 1 ELSE silentSaid
  /\ chal' = IF chal = "challenged" /\ now + 1 - asked >= Window /\ Broken # "neverdeaf" THEN "deaf" ELSE chal
  /\ IF limited /\ now + 1 >= limitUntil
       THEN /\ limited' = FALSE /\ limitUntil' = 0
       ELSE /\ UNCHANGED <<limited, limitUntil>>
  /\ UNCHANGED <<lastPing, nonce, asked, pongs, seen, answered, daemonPongs, busy, owed>>

Ping(wake) ==
  /\ now < MaxTime
  /\ nonce < MaxPings
  /\ lastPing' = now
  /\ conn' = "connected"
  /\ nonce' = nonce + 1
  /\ daemonPongs' = IF wake THEN daemonPongs + 1 ELSE daemonPongs
  /\ chal' = IF wake /\ Broken = "daemonpongends"
               THEN "quiet"
             ELSE IF chal = "quiet"
               THEN "challenged"
             ELSE IF chal = "deaf" THEN "deaf" ELSE "challenged"
  /\ asked' = now
  /\ owed' = ((owed \/ wake) /\ chal' # "quiet")
  /\ UNCHANGED <<now, silentFrom, pongs, seen, silentSaid, outages, answered, busy, limited, limitUntil>>

Turn ==
  /\ ~busy
  /\ IF Broken = "deliverwhilelimited" THEN TRUE ELSE ~limited
  /\ busy' = TRUE
  /\ IF chal # "quiet"
       THEN seen' = seen \cup {nonce} /\ owed' = FALSE
       ELSE UNCHANGED <<seen, owed>>
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs, limited, limitUntil>>

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

Pong(n) ==
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

UpOnlyAfterPong == Up => answered = nonce
DeafAfterWindow == chal = "challenged" => now - asked < Window
SilentOncePerOutage == silentSaid = outages
OnlyCurrentNonceAnswers == answered # NoNonce => answered = nonce
OnlySessionPongEnds == chal = "quiet" /\ asked > 0 => answered = nonce
OwedOnlyWhileAsked == owed => chal # "quiet"

NoTurnWhileLimited == busy => ~limited
LimitedEnds == limited => now < limitUntil

=============================================================================
