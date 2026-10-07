------------------------------- MODULE Friend -------------------------------
\* nova-friend's daemon machine (docs/SPEC-FRIEND.md; internal/friend/machine.go)
\* Start, Ping, Pong, Tick, Up. One friend's daemon, as it sees the
\* coordinator and its own session: the connection (a ping from the
\* coordinator within the window, else silent) and the challenge (a ping
\* pushed into the session carries a nonce; the session's own pong with
\* that nonce ends it; a window with no pong is deaf).
\*
\* The state the code owns: conn, lastPing, silentFrom; chal, nonce, asked,
\* pongs; daemonPongs, the daemon's own answers; busy, whether a turn is in
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
\* A harness at its usage limit or out of credits is down until the reset
\* (limits-mean-down-w-r.w1~15; internal/friend/limits.go, limit.go Limits.Gate):
\* lim is "up" or "limited", limUntil the reset the text named (or the rest). A
\* turn that hits the limit ends at once and the friend is limited (HitLimit);
\* while limited no turn starts, message or wake check, so every message stays
\* pending, but pings are still answered by the daemon (Ping is not a turn);
\* once the reset has passed one wake turn is tried (Wake): it answers and the
\* friend is up again, or it still says limited and the next reset is taken
\* from its text.
\*
\* A session broken after BrokenAfter provider refusals may be recovered by opening
\* a fresh session. The session state: broken, recovering, or recovered.
\* Recovery is bounded: at most RecoverMax recoveries per hour. After that,
\* the session stays broken. (docs/SPEC-FRIEND.md; internal/friend/recover.go)

EXTENDS Naturals, FiniteSets

CONSTANTS Window, MaxTime, MaxPings, Broken, RecoverMax

VARIABLES now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
          daemonPongs, busy, owed, lim, limUntil, sess
vars == <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
          daemonPongs, busy, owed, lim, limUntil, sess>>
limvars == <<lim, limUntil>>
sessvars == <<sess>>

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
  /\ lim \in {"up", "limited"}
  /\ limUntil \in 0..MaxTime
  /\ sess \in {"ok", "broken", "recovering", "recovered"}

\* Up is what the daemon reports: the session answered the current challenge
\* and has answered at least once (machine.go Up). The witness lets the
\* daemon's presence alone say up.
Up == IF Broken = "upwithoutpong" THEN conn = "connected" ELSE chal = "quiet" /\ pongs > 0

\* Session is up only when it is ok or recovered (not broken or recovering).
SessionUp == sess \in {"ok", "recovered"}

Init ==
  /\ now = 0
  /\ conn = "connected" /\ lastPing = 0 /\ silentFrom = 0
  /\ chal = "quiet" /\ nonce = NoNonce /\ asked = 0 /\ pongs = 0
  /\ seen = {}
  /\ silentSaid = 0 /\ outages = 0
  /\ answered = NoNonce
  /\ daemonPongs = 0 /\ busy = FALSE /\ owed = FALSE
  /\ lim = "up" /\ limUntil = 0
  /\ sess = "ok"

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
  /\ UNCHANGED <<lastPing, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, busy, owed, limvars, sess>>

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
  /\ UNCHANGED <<now, silentFrom, pongs, seen, silentSaid, outages, answered, busy, limvars, sess>>

\* A turn carrying messages starts in the free session (daemon.go
\* startBatch): while a challenge is open the pong line for the current nonce
\* rides at its head (loop.head), and an owed wake check is paid by it.
Turn ==
  /\ ~busy
  /\ (lim = "up" \/ Broken = "deliverlimited")
  /\ (sess = "ok" \/ sess = "recovered")
  /\ busy' = TRUE
  /\ IF chal # "quiet"
       THEN seen' = seen \cup {nonce} /\ owed' = FALSE
       ELSE UNCHANGED <<seen, owed>>
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs, limvars, sess>>

\* A wake check owed to a free session with no message waiting is pushed
\* in as its own turn holding only the pong line (daemon.go startWake).
WakeTurn ==
  /\ ~busy /\ owed /\ chal # "quiet"
  /\ (lim = "up" \/ Broken = "deliverlimited")
  /\ (sess = "ok" \/ sess = "recovered")
  /\ busy' = TRUE /\ owed' = FALSE
  /\ seen' = seen \cup {nonce}
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs, limvars, sess>>

TurnEnds ==
  /\ busy
  /\ busy' = FALSE
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, owed, limvars, sess>>

\* The turn in the session hits the harness's usage limit or empty balance
\* (limit.go Limits.see): it ends at once, deferred, its messages kept
\* pending, and the friend is down until a reset after now (the text's own,
\* else the rest).
HitLimit ==
  /\ busy /\ lim = "up" /\ now < MaxTime
  /\ busy' = FALSE
  /\ lim' = "limited"
  /\ \E u \in (now + 1)..MaxTime : limUntil' = u
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, owed, sess>>

\* After the reset one wake turn is tried (limit.go Limits.Gate, a nonce the
\* session must answer): answered, the friend is up again; still limited, the
\* next reset is taken from the text. The try is no message turn: a message
\* goes in only after it answered.
Wake ==
  /\ lim = "limited" /\ ~busy /\ now >= limUntil /\ Broken # "neverwake"
  /\ \/ lim' = "up" /\ UNCHANGED limUntil
     \/ /\ now < MaxTime /\ lim' = "limited" /\ \E u \in (now + 1)..MaxTime : limUntil' = u
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, busy, owed, sess>>

\* The session answers with a nonce it has seen (machine.go Pong): the
\* current one ends the challenge; any other changes nothing. The witness
\* takes any seen nonce; answered records which one did.
Pong(n) ==
  /\ n \in seen
  /\ chal # "quiet"
  /\ (n = nonce \/ Broken = "stalepong")
  /\ chal' = "quiet" /\ pongs' = pongs + 1 /\ answered' = n /\ owed' = FALSE
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, nonce, asked, seen, silentSaid, outages, daemonPongs, busy, limvars, sess>>

\* Session breaks after BrokenAfter provider refusals in a row.
BreakSession ==
  /\ sess = "ok"
  /\ sess' = "broken"
  /\ UNCHANGED vars \setminus sessvars

\* Session recovers by opening a fresh session. Recovery is bounded by
\* RecoverMax per hour. After that limit, session stays broken.
RecoverSession ==
  /\ sess = "broken"
  /\ sess' = "recovering"
  /\ UNCHANGED vars \setminus sessvars

\* Recovery completes and session becomes recovered.
FinishRecover ==
  /\ sess = "recovering"
  /\ sess' = "recovered"
  /\ UNCHANGED vars \setminus sessvars

Next ==
  \/ Tick
  \/ \E wake \in BOOLEAN : Ping(wake)
  \/ Turn
  \/ WakeTurn
  \/ TurnEnds
  \/ HitLimit
  \/ Wake
  \/ \E n \in 1..MaxPings : Pong(n)
  \/ BreakSession
  \/ RecoverSession
  \/ FinishRecover

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ WF_vars(Wake)

\* ---------------------------------------------------------------- The rules

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

\* No turn starts while the harness is limited (limits-mean-down-w-r.w1~15):
\* a message or a wake check never goes into a session that cannot answer, so
\* every message stays pending, counted toward nothing. Pings are no turn.
NoTurnWhileLimited == [][(~busy /\ busy') => lim = "up"]_vars

\* Limited ends: once limited, the friend is up again (one wake turn after the
\* reset answered); WF(Wake) and the finite clock (a reset is always after
\* now, and no later than MaxTime) make it so.
LimitedEnds == (lim = "limited") ~> (lim = "up")

\* Session recovery is bounded: at most RecoverMax recoveries per hour.
\* After that, the session stays broken. (This is a simplification of the
\* actual rate-limiting which tracks recoveries in a time window.)
RecoveryBounded ==
  [][sess' = "recovered" => sess \in {"broken", "recovering"}]_vars

\* A recovered session must have gone through recovering state first.
RecoverViaRecovering ==
  [][(sess = "ok" \/ sess = "broken") /\ sess' = "recovered"]_vars
  =>
  []<>(sess = "recovering")

\* Once broken, session must eventually be recovered or stay broken.
\* This models the rate limit: after RecoverMax recoveries/hour, it stays broken.
BrokenEventuallyHandled ==
  (sess = "broken") ~> (sess \in {"recovered", "broken"})

\* A reversed witness: session becomes recovered without being recovering.
MCFriendBrokenRecoverWithoutRecovering ==
  [][sess' = "recovered" => ~ (sess = "recovering")]_vars

\* A reversed witness: recovery happens too often (more than RecoverMax times).
\* This catches the rate-limiting bug.
MCFriendBrokenRecoveryTooFast ==
  [][\E count \in 1..(RecoverMax + 1) : sess' = "recovered" /\ count > RecoverMax]_vars

\
 =============================================================================
