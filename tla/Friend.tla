------------------------------- MODULE Friend -------------------------------
\* nova-friend's daemon machine (docs/SPEC-FRIEND.md; internal/friend/machine.go:
\* Start, Ping, Pong, Tick, Up). One friend's daemon, as it sees the
\* coordinator and its own session: the connection (a ping from the
\* coordinator within the window, else silent) and the challenge (a ping
\* pushed into the session carries a nonce; the session's own pong with
\* that nonce ends it; a window with no pong is deaf).
\*
\* The state the code owns: conn, lastPing, silentFrom; chal, nonce, asked,
\* pongs; daemonPongs, the daemon's own answers; turns, how many turns run in
\* each session (busy is the batch session's), and owed, a wake check not yet
\* pushed in (daemon.go loop.wake).
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
\* from its text. The gate holds the batch session only: a one-shot lane's
\* turn goes straight to the harness (limit.go gatedLanes).
\*
\* Delivery (friend-tla-delivery-states; daemon.go read, startBatch, watch,
\* settle, batchDone; lanes.go laneStep, laneDone). The messages are Msgs,
\* each at loc: "bus" (pending on the stream, not read), "hand" (read, in the
\* daemon's hand), a session (carried by a turn running there), "acked" or
\* "given" (acked after MaxDeliveries failed turns, said on the record).
\*   batch with one ack: a free batch session takes every message in the hand
\*     in one turn (Turn, take), and the turn's end settles them together:
\*     exit 0 acks them all (heard, the ghost of messages a turn carried to a
\*     clean end); a refusal by the provider leaves them pending, counted
\*     toward nothing; any other failure counts one delivery against each, and
\*     the one at MaxDeliveries is given up. A turn the limit defers keeps its
\*     messages in hand (HitLimit). Turn with an empty hand stands for the
\*     daemon's other turns in the free batch session: the dealt briefs and
\*     the idle wake (startDealt, idle), which carry no message.
\*   answered pings: a ping is answered by the daemon and acked at the read,
\*     never put in the hand (read, ping); pingAt is where a ping went, "none"
\*     in the design.
\*   the silent stop: a running turn's quiet is the ticks since it last
\*     printed (Print sets it to 0); one quiet for SilentStop is stopped
\*     (StopSilent, watch), and a stopped turn's end is a failure. No action
\*     ends a printing turn by its age. A lane's wall cap by tier (lane_cap.go)
\*     is internal/friend/tla/LaneEnd.tla's, not this module's.
\*   a broken session: the same provider refusal K turns in a row (streak,
\*     refusal: the reason, NoReason after any other end) marks the session
\*     broken; from then on no turn starts, batch or lane, and every message
\*     stays pending. The streak is the daemon's, one for all its sessions, as
\*     the code keeps it (loop.streak); refRow and rowRsn are the ghost streak
\*     of each session's own turns, read only by the finding below.
\*   one-shot lanes (Mode "oneshot"; Mode "batch" runs the batch session
\*     alone, as the daemon delivers in one mode at a time and a change of
\*     mode waits for every turn to end): each lane is its own session and holds
\*     one card at a time (cst[c] is "queued", a lane, "done" or "aside");
\*     a free lane hands its card, or takes the first queued one, with the
\*     hand's messages riding along and the pong line at its head. The card is
\*     done only when its RESULT.md is there (result, WriteResult) when the
\*     turn ends; a turn without one hands it again, and after CardTurns turns
\*     with none it is set aside (attempts, the lane's turns of its card that
\*     ended with none).
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
\*   "deliverlimited" a turn starts while the harness is limited: NoTurnWhileLimited
\*   "neverwake"      no wake turn is tried after the reset: LimitedEnds
\*   "ackunread"      a refused turn acks its batch: AckedOnlyHeard
\*   "lostonstop"     a stopped turn's messages are neither pending nor given
\*                    up: NoMessageLost
\*   "wakeoverturn"   a wake check is pushed into a session with a turn
\*                    running: OneTurnPerSession
\*   "deliverbroken"  a turn starts in a broken session: NoDeliveryWhileBroken
\*   "pingdelivered"  a ping goes into the hand as a message: PingNeverDelivered
\*   "killprinting"   a running turn is stopped while it prints: StopOnlySilent
\*   "neveraside"     a card with no RESULT.md after CardTurns turns is handed
\*                    again: CardTurnsBounded
\*   "doneonexit"     a lane's exit 0 ends the card with no RESULT.md:
\*                    DoneOnlyWithResult
\*
\* One finding, checked as an expected failure (MCFriendFindingStreakPerDaemon):
\* RefusedSessionBroken, a session whose own turns were refused K times in a row
\* is broken, fails in one-shot mode, because the streak is the daemon's and a
\* clean end in another lane resets it (daemon.go loop.streak, settle).

EXTENDS Naturals, FiniteSets

CONSTANTS Window, MaxTime, MaxPings, Broken,
          Mode, Lanes, NMsgs, NCards, K, Reasons, MaxDeliveries, CardTurns, SilentStop

VARIABLES now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
          daemonPongs, owed, lim, limUntil,
          turns, loc, fails, heard, pingAt, quiet, stopped,
          streak, refusal, broken, refRow, rowRsn,
          cst, attempts, result
livevars == <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
              daemonPongs, owed, lim, limUntil>>
limvars == <<lim, limUntil>>
msgvars == <<loc, fails, heard, pingAt>>
turnvars == <<turns, quiet, stopped>>
refvars == <<streak, refusal, broken, refRow, rowRsn>>
cardvars == <<cst, attempts, result>>
delvars == <<turns, loc, fails, heard, pingAt, quiet, stopped, streak, refusal, broken, refRow, rowRsn,
             cst, attempts, result>>
vars == <<livevars, delvars>>

NoNonce == 0
NoReason == 0
Msgs == 1..NMsgs
Cards == 1..NCards
Sess == {"batch"} \cup Lanes
Min(a, b) == IF a < b THEN a ELSE b

busy == turns["batch"] > 0

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
  /\ owed \in BOOLEAN
  /\ lim \in {"up", "limited"}
  /\ limUntil \in 0..MaxTime
  /\ turns \in [Sess -> 0..2]
  /\ loc \in [Msgs -> {"bus", "hand", "acked", "given"} \cup Sess]
  /\ fails \in [Msgs -> 0..MaxDeliveries]
  /\ heard \subseteq Msgs
  /\ pingAt \in {"none", "hand", "turn"}
  /\ quiet \in [Sess -> 0..SilentStop]
  /\ stopped \in [Sess -> BOOLEAN]
  /\ streak \in 0..K
  /\ refusal \in {NoReason} \cup Reasons
  /\ broken \in BOOLEAN
  /\ refRow \in [Sess -> 0..K]
  /\ rowRsn \in [Sess -> {NoReason} \cup Reasons]
  /\ cst \in [Cards -> {"queued", "done", "aside"} \cup Lanes]
  /\ attempts \in [Lanes -> 0..CardTurns]
  /\ result \subseteq Cards

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
  /\ daemonPongs = 0 /\ owed = FALSE
  /\ lim = "up" /\ limUntil = 0
  /\ turns = [s \in Sess |-> 0]
  /\ loc = [m \in Msgs |-> "bus"] /\ fails = [m \in Msgs |-> 0] /\ heard = {} /\ pingAt = "none"
  /\ quiet = [s \in Sess |-> 0] /\ stopped = [s \in Sess |-> FALSE]
  /\ streak = 0 /\ refusal = NoReason /\ broken = FALSE
  /\ refRow = [s \in Sess |-> 0] /\ rowRsn = [s \in Sess |-> NoReason]
  /\ cst = [c \in Cards |-> "queued"] /\ attempts = [l \in Lanes |-> 0]
  /\ result = {}

\* The clock (machine.go Tick): a window without a ping makes the
\* coordinator silent, said once at that moment; a window challenged with
\* no pong makes the session deaf. Each running turn grows one tick quieter
\* (daemon.go watch reads the output seen since the last step).
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
  /\ quiet' = [s \in Sess |-> IF turns[s] > 0 THEN Min(quiet[s] + 1, SilentStop) ELSE 0]
  /\ UNCHANGED <<lastPing, nonce, asked, pongs, seen, answered, daemonPongs, owed, limvars>>
  /\ UNCHANGED <<msgvars, turns, stopped, refvars, cardvars>>

\* A ping from the coordinator with a fresh nonce (machine.go Ping;
\* daemon.go loop.ping): the daemon answers it at once (daemonPongs), the
\* connection is back (said once; the push is "coordinator back", not
\* counted here), the session is challenged with this nonce whatever it was
\* before, deaf staying deaf until a pong; a wake check (wake) owes the
\* session a wake turn. The ping itself is never a turn: the session sees the
\* nonce only when a turn carries the pong line, and the read acks the ping
\* without putting it in the hand (daemon.go read). The witnesses let the
\* daemon's pong to a wake check end the challenge, and put a ping in the hand.
Ping(wake) ==
  /\ nonce < MaxPings
  /\ nonce' = nonce + 1
  /\ daemonPongs' = daemonPongs + 1
  /\ conn' = "connected" /\ lastPing' = now
  /\ chal' = IF wake /\ Broken = "daemonpongends" THEN "quiet"
            ELSE IF chal = "deaf" THEN "deaf" ELSE "challenged"
  /\ asked' = now
  /\ owed' = ((owed \/ wake) /\ chal' # "quiet")
  /\ pingAt' = IF Broken = "pingdelivered" /\ pingAt = "none" THEN "hand" ELSE pingAt
  /\ UNCHANGED <<now, silentFrom, pongs, seen, silentSaid, outages, answered, limvars>>
  /\ UNCHANGED <<loc, fails, heard, turnvars, refvars, cardvars>>

\* The step's read (daemon.go read): with no batch turn running and the
\* session not broken, every pending message is taken into the hand; else
\* the read only peeks, answering pings.
Read ==
  /\ ~busy /\ ~broken
  /\ \E m \in Msgs : loc[m] = "bus"
  /\ loc' = [m \in Msgs |-> IF loc[m] = "bus" THEN "hand" ELSE loc[m]]
  /\ UNCHANGED <<livevars, fails, heard, pingAt, turnvars, refvars, cardvars>>

\* A turn starting in session s carries every message in the hand (take; the
\* instance's NMsgs is under MaxBatch) and any ping the witness put there.
Carry(s) ==
  /\ loc' = [m \in Msgs |-> IF loc[m] = "hand" THEN s ELSE loc[m]]
  /\ pingAt' = IF pingAt = "hand" THEN "turn" ELSE pingAt
  /\ turns' = [turns EXCEPT ![s] = @ + 1]
  /\ quiet' = [quiet EXCEPT ![s] = 0]
  /\ stopped' = [stopped EXCEPT ![s] = FALSE]

\* The pong line at a turn's head while a challenge is open (loop.head):
\* the session sees the nonce, and an owed wake check is paid.
Head ==
  IF chal # "quiet"
    THEN seen' = seen \cup {nonce} /\ owed' = FALSE
    ELSE UNCHANGED <<seen, owed>>

\* A turn carrying messages starts in the free batch session (daemon.go
\* startBatch): while a challenge is open the pong line for the current nonce
\* rides at its head (loop.head), and an owed wake check is paid by it.
Turn ==
  /\ Mode = "batch"
  /\ ~busy
  /\ (~broken \/ Broken = "deliverbroken")
  /\ (lim = "up" \/ Broken = "deliverlimited")
  /\ Carry("batch")
  /\ Head
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs, limvars>>
  /\ UNCHANGED <<fails, heard, refvars, cardvars>>

\* A wake check owed to a free session with no message waiting is pushed
\* in as its own turn holding only the pong line (daemon.go startWake).
WakeTurn ==
  /\ Mode = "batch"
  /\ (~busy \/ Broken = "wakeoverturn")
  /\ owed /\ chal # "quiet"
  /\ ~broken
  /\ (lim = "up" \/ Broken = "deliverlimited")
  /\ \A m \in Msgs : loc[m] # "hand"
  /\ owed' = FALSE
  /\ seen' = seen \cup {nonce}
  /\ turns' = [turns EXCEPT !["batch"] = @ + 1]
  /\ quiet' = [quiet EXCEPT !["batch"] = 0]
  /\ stopped' = [stopped EXCEPT !["batch"] = FALSE]
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs, limvars>>
  /\ UNCHANGED <<msgvars, refvars, cardvars>>

\* A free lane hands a card (lanes.go laneStep): the one it holds between
\* turns, else the first queued; the hand's messages ride along, the pong
\* line at its head. With no card to hand, the messages wait.
LaneCard(l) ==
  IF \E c \in Cards : cst[c] = l
    THEN CHOOSE c \in Cards : cst[c] = l
    ELSE CHOOSE c \in Cards : cst[c] = "queued" /\ \A d \in Cards : cst[d] = "queued" => c <= d
LaneTurn(l) ==
  /\ Mode = "oneshot"
  /\ turns[l] = 0
  /\ (~broken \/ Broken = "deliverbroken")
  /\ \E c \in Cards : cst[c] \in {l, "queued"}
  /\ cst' = [cst EXCEPT ![LaneCard(l)] = l]
  /\ Carry(l)
  /\ Head
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs, limvars>>
  /\ UNCHANGED <<fails, heard, refvars, attempts, result>>

\* The running turn prints (WithOutputSeen): its silence starts again.
Print(s) ==
  /\ turns[s] > 0 /\ quiet[s] > 0
  /\ quiet' = [quiet EXCEPT ![s] = 0]
  /\ UNCHANGED <<livevars, msgvars, turns, stopped, refvars, cardvars>>

\* A turn quiet for SilentStop is stopped (daemon.go watch): its process
\* group is signalled and its end, whatever it says, is a failure. The
\* witness stops a turn that is printing.
StopSilent(s) ==
  /\ turns[s] > 0 /\ ~stopped[s]
  /\ (quiet[s] >= SilentStop \/ Broken = "killprinting")
  /\ stopped' = [stopped EXCEPT ![s] = TRUE]
  /\ UNCHANGED <<livevars, msgvars, turns, quiet, refvars, cardvars>>

\* The lane's card's RESULT.md is written while its turn runs.
WriteResult(l) ==
  /\ turns[l] > 0
  /\ \E c \in Cards : cst[c] = l /\ c \notin result
  /\ result' = result \cup {CHOOSE c \in Cards : cst[c] = l}
  /\ UNCHANGED <<livevars, msgvars, turnvars, refvars, cst, attempts>>

\* The end of a turn in session s settles what it carried (daemon.go
\* settle): "ok" (exit 0) acks every message together; "refused" (the
\* provider's refusal, with reason r) leaves them pending, counted toward
\* nothing, and moves the streak; "fail" counts a delivery against each, the
\* one at MaxDeliveries given up. A stopped turn is a failure. The
\* witnesses ack a refused batch, and leave a stopped turn's messages where
\* the turn was.
Settle(s, o, r) ==
  /\ loc' = [m \in Msgs |->
       IF loc[m] # s THEN loc[m]
       ELSE CASE o = "ok" -> "acked"
              [] o = "refused" -> IF Broken = "ackunread" THEN "acked" ELSE "bus"
              [] o = "fail" -> IF Broken = "lostonstop" /\ stopped[s] THEN s
                               ELSE IF fails[m] + 1 >= MaxDeliveries THEN "given" ELSE "bus"]
  /\ fails' = [m \in Msgs |-> IF loc[m] = s /\ o = "fail" THEN Min(fails[m] + 1, MaxDeliveries)
                              ELSE IF loc[m] = s /\ o = "ok" THEN 0 ELSE fails[m]]
  /\ heard' = IF o = "ok" THEN heard \cup {m \in Msgs : loc[m] = s} ELSE heard
  /\ IF o = "refused"
       THEN /\ streak' = IF r = refusal THEN Min(streak + 1, K) ELSE 1
            /\ refusal' = r
            /\ broken' = (broken \/ streak' >= K)
            /\ refRow' = [refRow EXCEPT ![s] = IF r = rowRsn[s] THEN Min(@ + 1, K) ELSE 1]
            /\ rowRsn' = [rowRsn EXCEPT ![s] = r]
       ELSE /\ streak' = 0 /\ refusal' = NoReason /\ UNCHANGED broken
            /\ refRow' = [refRow EXCEPT ![s] = 0]
            /\ rowRsn' = [rowRsn EXCEPT ![s] = NoReason]
  /\ turns' = [turns EXCEPT ![s] = @ - 1]
  /\ quiet' = [quiet EXCEPT ![s] = 0]
  /\ stopped' = [stopped EXCEPT ![s] = FALSE]

\* How a turn in s may end, with the refusal's reason: a stopped turn fails.
Ends(s) ==
  IF stopped[s] THEN {<<"fail", NoReason>>}
  ELSE {<<"ok", NoReason>>, <<"fail", NoReason>>} \cup {<<"refused", r>> : r \in Reasons}

\* The batch turn ends (daemon.go batchDone).
TurnEnds ==
  /\ busy
  /\ \E e \in Ends("batch") : Settle("batch", e[1], e[2])
  /\ UNCHANGED <<livevars, pingAt, cardvars>>

\* A lane's turn ends (lanes.go laneDone): the messages settle as a batch
\* turn's; the card is done when its RESULT.md is there, else it is handed
\* again, and set aside after CardTurns turns with none. The witnesses take
\* exit 0 for done, and queue a card again after its last turn.
LaneEnds(l) ==
  /\ turns[l] > 0
  /\ \E e \in Ends(l) :
       /\ Settle(l, e[1], e[2])
       /\ LET c == CHOOSE c \in Cards : cst[c] = l IN
            IF c \in result \/ (Broken = "doneonexit" /\ e[1] = "ok")
              THEN /\ cst' = [cst EXCEPT ![c] = "done"]
                   /\ attempts' = [attempts EXCEPT ![l] = 0]
              ELSE IF attempts[l] + 1 >= CardTurns /\ Broken # "neveraside"
                THEN /\ cst' = [cst EXCEPT ![c] = "aside"]
                     /\ attempts' = [attempts EXCEPT ![l] = 0]
                ELSE /\ attempts' = [attempts EXCEPT ![l] = @ + 1]
                     /\ UNCHANGED cst
  /\ UNCHANGED <<livevars, pingAt, result>>

\* The turn in the session hits the harness's usage limit or empty balance
\* (limit.go Limits.see): it ends at once, deferred, its messages kept in
\* the hand, counted toward nothing, and the friend is down until a reset
\* after now (the text's own, else the rest).
HitLimit ==
  /\ busy /\ lim = "up" /\ now < MaxTime
  /\ turns' = [turns EXCEPT !["batch"] = 0]
  /\ quiet' = [quiet EXCEPT !["batch"] = 0]
  /\ stopped' = [stopped EXCEPT !["batch"] = FALSE]
  /\ loc' = [m \in Msgs |-> IF loc[m] = "batch" THEN "hand" ELSE loc[m]]
  /\ lim' = "limited"
  /\ \E u \in (now + 1)..MaxTime : limUntil' = u
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, owed>>
  /\ UNCHANGED <<fails, heard, pingAt, refvars, cardvars>>

\* After the reset one wake turn is tried (limit.go Limits.Gate, a nonce the
\* session must answer): answered, the friend is up again; still limited, the
\* next reset is taken from the text. The try is no message turn: a message
\* goes in only after it answered.
Wake ==
  /\ lim = "limited" /\ ~busy /\ now >= limUntil /\ Broken # "neverwake"
  /\ \/ lim' = "up" /\ UNCHANGED limUntil
     \/ /\ now < MaxTime /\ lim' = "limited" /\ \E u \in (now + 1)..MaxTime : limUntil' = u
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered, daemonPongs, owed>>
  /\ UNCHANGED delvars

\* The session answers with a nonce it has seen (machine.go Pong): the
\* current one ends the challenge; any other changes nothing. The witness
\* takes any seen nonce; answered records which one did.
Pong(n) ==
  /\ n \in seen
  /\ chal # "quiet"
  /\ (n = nonce \/ Broken = "stalepong")
  /\ chal' = "quiet" /\ pongs' = pongs + 1 /\ answered' = n /\ owed' = FALSE
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, nonce, asked, seen, silentSaid, outages, daemonPongs, limvars>>
  /\ UNCHANGED delvars

Next ==
  \/ Tick
  \/ \E wake \in BOOLEAN : Ping(wake)
  \/ Read
  \/ Turn
  \/ WakeTurn
  \/ TurnEnds
  \/ HitLimit
  \/ Wake
  \/ \E n \in 1..MaxPings : Pong(n)
  \/ \E l \in Lanes : LaneTurn(l) \/ WriteResult(l) \/ LaneEnds(l)
  \/ \E s \in Sess : Print(s) \/ StopSilent(s)

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ WF_vars(Wake)

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

\* No turn starts in the batch session while the harness is limited
\* (limits-mean-down-w-r.w1~15): a message or a wake check never goes into a
\* session that cannot answer, so every message stays pending, counted toward
\* nothing. Pings are no turn.
NoTurnWhileLimited == [][(~busy /\ busy') => lim = "up"]_vars

\* Limited ends: once limited, the friend is up again (one wake turn after the
\* reset answered); WF(Wake) and the finite clock (a reset is always after
\* now, and no later than MaxTime) make it so.
LimitedEnds == (lim = "limited") ~> (lim = "up")

\* No message is acked unread: an acked message was carried by a turn that
\* ended clean.
AckedOnlyHeard == \A m \in Msgs : loc[m] = "acked" => m \in heard

\* No message is lost: one a turn carries is in a turn still running, and one
\* given up had MaxDeliveries failed turns; every other is pending, in hand or
\* acked.
NoMessageLost ==
  \A m \in Msgs :
    /\ loc[m] \in Sess => turns[loc[m]] > 0
    /\ loc[m] = "given" => fails[m] >= MaxDeliveries

\* No two turns run in one session at once.
OneTurnPerSession == \A s \in Sess : turns[s] <= 1

\* A broken session receives no delivery: no turn starts once it is broken.
NoDeliveryWhileBroken == [][\A s \in Sess : turns'[s] > turns[s] => ~broken]_vars

\* An answered ping is never delivered as a message.
PingNeverDelivered == pingAt # "turn"

\* The silent stop: a turn is stopped only after SilentStop with no output,
\* never for its age.
StopOnlySilent == [][\A s \in Sess : (~stopped[s] /\ stopped'[s]) => quiet[s] >= SilentStop]_vars

\* A card is handed at most CardTurns turns, then done or set aside: a lane
\* holds a card it has handed fewer than CardTurns times.
CardTurnsBounded == \A l \in Lanes : attempts[l] < CardTurns

\* A card is done only with its RESULT.md.
DoneOnlyWithResult == \A c \in Cards : cst[c] = "done" => c \in result

\* The finding (MCFriendFindingStreakPerDaemon): a session whose own turns were
\* refused K times in a row with one reason is broken. It fails: the streak is
\* the daemon's, and another lane's clean end resets it in between.
RefusedSessionBroken == \A s \in Sess : refRow[s] >= K => broken

\* The one liveness claimed beyond that: the clock is finite here, and
\* DeafAfterWindow already says an open challenge is younger than a window at
\* every state, so once the clock moves a window it is answered or deaf.

=============================================================================
