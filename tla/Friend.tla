------------------------------- MODULE Friend -------------------------------
\* nova-friend's daemon machine (docs/SPEC-FRIEND.md; internal/friend/machine.go:
\* Start, Ping, Pong, Tick, Up). One friend's daemon, as it sees the
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
\* Broken = "none" is the design. Every other value is a reversed witness,
\* each caught by one property below:
\*   "upwithoutpong"  the daemon's own beat makes the friend up: UpOnlyAfterPong
\*   "neverdeaf"      a challenge never times out: DeafAfterWindow
\*   "silenttwice"    "coordinator silent" is pushed every tick of an outage:
\*                    SilentOncePerOutage
\*   "neversilent"    the outage is never said: SilentOncePerOutage
\*   "stalepong"      any nonce the session ever saw answers: OnlyCurrentNonceAnswers
\*   "daemonpongends" the daemon's pong ends a wake challenge (session-pong.w1):
\*                    OnlySessionPongEnds
\*   "turnwhilelimited" a turn goes into a session at its limit:
\*                    NoTurnWhileLimited
\*   "endsonclock"    the limit ends when its reset comes, nothing answered:
\*                    LimitEndsOnlyByAnAnswer
\*   "failureends"    a reset turn that fails without the answer ends the
\*                    limit (attempt 1 of limits-mean-down-w.w2):
\*                    LimitEndsOnlyByAnAnswer
\*   "neverwake"      no turn is tried at the reset: LimitedEnds
\*
\* The harness's limit (limits-mean-down-w.w2; internal/friend/limit.go
\* Limits, limits.go WatchHarness): limited, resetAt, limits. A turn's
\* output saying a usage limit or an empty balance (HitLimit, the outside,
\* at most MaxLimits times) ends it and makes the friend limited until a
\* reset at most Rest ticks away (the reset the text names, else
\* --limit-rest); while limited no turn goes in (Gate defers it; the daemon
\* still answers pings); at the reset one reset turn is tried (Gate's wake
\* with its nonce, ResetTurn), and only its answer (Heard) ends the limit
\* when it ends (ResetEnds); one that says the limit again takes the next
\* reset from it (HitLimit), one that fails otherwise leaves it limited,
\* tried again. heard is whether the reset turn's answer was seen. turn is what the running turn is: "msg" (a turn of
\* messages, or the wake check's pong line) or "reset".

EXTENDS Naturals, FiniteSets

CONSTANTS Window, MaxTime, MaxPings, Rest, MaxLimits, Broken

VARIABLES now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
          daemonPongs, busy, owed, turn, limited, resetAt, limits, heard
friendVars == <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
          daemonPongs, busy, owed>>
limitVars == <<turn, limited, resetAt, limits, heard>>
vars == <<friendVars, limitVars>>

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
  /\ turn \in {"msg", "reset"}
  /\ limited \in BOOLEAN
  /\ resetAt \in 0..MaxTime
  /\ limits \in 0..MaxLimits
  /\ heard \in BOOLEAN

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
  /\ turn = "msg" /\ limited = FALSE /\ resetAt = 0 /\ limits = 0 /\ heard = FALSE

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
  /\ limited' = IF Broken = "endsonclock" /\ limited /\ now + 1 >= resetAt THEN FALSE ELSE limited
  /\ UNCHANGED <<lastPing, nonce, asked, pongs, seen, answered, daemonPongs, busy, owed, turn, resetAt, limits, heard>>

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
  /\ UNCHANGED <<now, silentFrom, pongs, seen, silentSaid, outages, answered, busy>>
  /\ UNCHANGED limitVars

\* A turn carrying messages starts in the free session (daemon.go
\* startBatch): while a challenge is open the pong line for the current nonce
\* rides at its head (loop.head), and an owed wake check is paid by it.
Turn ==
  /\ ~busy
  /\ (~limited \/ Broken = "turnwhilelimited")
  /\ busy' = TRUE /\ turn' = "msg"
  /\ IF chal # "quiet"
       THEN seen' = seen \cup {nonce} /\ owed' = FALSE
       ELSE UNCHANGED <<seen, owed>>
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs>>
  /\ UNCHANGED <<limited, resetAt, limits, heard>>

\* A wake check owed to a free session with no message waiting is pushed
\* in as its own turn holding only the pong line (daemon.go startWake).
WakeTurn ==
  /\ ~busy /\ owed /\ chal # "quiet"
  /\ ~limited
  /\ busy' = TRUE /\ owed' = FALSE /\ turn' = "msg"
  /\ seen' = seen \cup {nonce}
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs>>
  /\ UNCHANGED <<limited, resetAt, limits, heard>>

TurnEnds ==
  /\ busy /\ turn = "msg"
  /\ busy' = FALSE
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, owed>>
  /\ UNCHANGED limitVars

\* A turn's output says a usage limit or an empty balance (limits.go
\* WatchHarness, limit.go Limits.see), the outside: the turn ends, Deferred
\* (its messages stay in hand), and the friend is limited until a reset in
\* the next Rest ticks; a reset turn that says it again takes the next reset
\* from it.
HitLimit ==
  /\ busy /\ limits < MaxLimits /\ now < MaxTime
  /\ busy' = FALSE /\ limits' = limits + 1 /\ limited' = TRUE
  /\ resetAt' \in (now + 1)..(IF now + Rest > MaxTime THEN MaxTime ELSE now + Rest)
  /\ UNCHANGED <<turn, heard>>
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, owed>>

\* At the reset one turn is tried (limit.go gated.Deliver, the wake with its
\* fresh nonce). The witness never tries it.
ResetTurn ==
  /\ ~busy /\ limited /\ now >= resetAt
  /\ Broken # "neverwake"
  /\ busy' = TRUE /\ turn' = "reset" /\ heard' = FALSE
  /\ UNCHANGED <<limited, resetAt, limits>>
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, owed>>

\* The session answers the reset turn: its output carries the nonce
\* (Limits.Watch sees it), the outside.
Heard ==
  /\ busy /\ turn = "reset" /\ ~heard
  /\ heard' = TRUE
  /\ UNCHANGED <<turn, limited, resetAt, limits>>
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, busy, owed>>

\* The reset turn ends: answered, the friend is up (Limits.Up, the seat told
\* once; the beat goes again); otherwise (no answer, an ordinary failure)
\* still limited, tried again. The witness ends the limit on any end.
ResetEnds ==
  /\ busy /\ turn = "reset"
  /\ busy' = FALSE
  /\ limited' = ~(heard \/ Broken = "failureends")
  /\ UNCHANGED <<turn, resetAt, limits, heard>>
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, owed>>

\* The session answers with a nonce it has seen (machine.go Pong): the
\* current one ends the challenge; any other changes nothing. The witness
\* takes any seen nonce; answered records which one did.
Pong(n) ==
  /\ n \in seen
  /\ chal # "quiet"
  /\ (n = nonce \/ Broken = "stalepong")
  /\ chal' = "quiet" /\ pongs' = pongs + 1 /\ answered' = n /\ owed' = FALSE
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, nonce, asked, seen, silentSaid, outages, daemonPongs, busy>>
  /\ UNCHANGED limitVars

Next ==
  \/ Tick
  \/ \E wake \in BOOLEAN : Ping(wake)
  \/ Turn
  \/ WakeTurn
  \/ TurnEnds
  \/ \E n \in 1..MaxPings : Pong(n)
  \/ HitLimit
  \/ ResetTurn
  \/ Heard
  \/ ResetEnds

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)

\* Limited ends only under the assumption that the session answers some
\* reset turn: the daemon tries one at each reset and every turn ends (weak
\* fairness), and a session tried again and again answers one (strong
\* fairness).
SpecLive == Spec /\ WF_vars(ResetTurn) /\ WF_vars(ResetEnds) /\ SF_vars(Heard)

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

\* No turn goes into the session while it is limited: no message turn and
\* no wake check runs at its limit; a reset turn alone is tried.
NoTurnWhileLimited == (limited /\ busy) => turn = "reset"

\* The limit ends only by the session's answer to a reset turn, never by
\* the clock alone, never by a turn that failed.
LimitEndsOnlyByAnAnswer == [][(limited /\ ~limited') => (busy /\ turn = "reset" /\ heard)]_vars

\* Limited ends (under SpecLive).
LimitedEnds == limited ~> ~limited

\* No liveness is claimed for the challenge: the clock is finite here, and DeafAfterWindow
\* already says an open challenge is younger than a window at every state,
\* so once the clock moves a window it is answered or deaf.

=============================================================================
