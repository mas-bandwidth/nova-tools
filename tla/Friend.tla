------------------------------- MODULE Friend -------------------------------
\* nova-friend's daemon machine (docs/SPEC-FRIEND.md; internal/friend/machine.go:
\* Start, Ping, Pong, Tick, Up). One friend's daemon, as it sees the
\* coordinator and its own session: the connection (a ping from the
\* coordinator within the window, else silent) and the challenge (a ping
\* pushed into the session carries a nonce; the session's own pong with
\* that nonce ends it; a window with no pong is deaf).
\*
\* The state the code owns: conn, lastPing, silentFrom; chal, nonce, asked,
\* pongs; daemonPongs, the daemon's own answers; turns, the turns running in
\* the batch session, and owed, a wake check not yet pushed in (daemon.go
\* loop.wake). file is one file under a walked root; visible means the folder
\* check has read it (activity.go NewestWrite, called from daemon.go Run before
\* Beat); fileBeat means a beat that depends on that file has run. The clock is
\* now, one unit a tick, Window units a window. The
\* outside: the coordinator pinging (Ping, each ping a fresh nonce, plain or a
\* wake check), the session's turns (StartBatch, one carrying messages, with
\* the pong line at its head while a challenge is open; WakeTurn, the pong line
\* alone, pushed into a free session for a wake check; BatchEnds), the session
\* having seen every nonce a turn put in front of it (seen), and the session
\* answering (Pong, with any nonce it has seen, so a stale or replayed pong is
\* possible), and the session writing that file (WriteFile). The pushes the
\* session is owed are counted: silentSaid
\* (the "coordinator silent" pushes) beside outages (the times the
\* connection went silent).
\*
\* A harness at its usage limit or out of credits is down until the reset
\* (limits-mean-down-w-r.w1~15; internal/friend/limits.go, limit.go Limits.Gate):
\* lim is "up" or "limited", limUntil the reset the text named (or the rest). A
\* batch turn that hits the limit ends at once, Deferred, and stays in the
\* daemon's hand with its messages (held, daemon.go batchDone) and the friend is
\* limited (HitLimit); while limited no turn starts, message or wake check, so
\* every message stays pending, but pings are still answered by the daemon (Ping
\* is not a turn); once the reset has passed one wake turn is tried (Wake): it
\* answers and the friend is up again, or it still says limited and the next
\* reset is taken from its text; up again, the held turn is tried (Retry).
\*
\* The delivery (friend-e2e; daemon.go Run, read, take, settle; lanes.go):
\*   Messages are the friend's stream entries: pel the ones pending (sent, not
\*   acked), acked the ones acked. The daemon reads a pending message that is
\*   not already in its hand into the hand (Read; inHand is the code's
\*   loop.inHand, the entries in the hand or in a turn); a ping is answered and
\*   acked by the daemon and never goes into the hand (pingIn stays "none").
\*   Batch mode: a free session takes every message in the hand in one turn
\*   (StartBatch, loop.take), and its end settles them together (settle): exit
\*   0 acks them all in one ack; a provider's refusal leaves them pending,
\*   counted toward nothing, and K identical refusals in a row (streak, reason)
\*   mark the session broken; any other failure counts toward MaxDeliveries
\*   (failed) and the last one acks the message, given up, after it was read.
\*   A turn that has printed nothing for SilentStop ticks is stopped (Stop,
\*   loop.watch; quiet counts the ticks since it last printed, Print resets
\*   it): there is no fixed kill time, a turn that prints runs on. Broken, the
\*   daemon reads nothing into the hand and starts no turn of any kind until it
\*   restarts (Run's switch, case l.broken; read's peek).
\*   One-shot mode: each lane is its own session and holds one card (lcard)
\*   until its RESULT.md appears (cst "done"); a turn that ends without one
\*   hands the same card again, and after CardTurns such turns (tries) the card
\*   is set aside (cst "aside", laneDone). The messages in the hand ride along
\*   with a lane's card turn and are settled as a batch turn's are; a lane
\*   holding no card takes the next queued one, and with none takes nothing,
\*   messages included. A lane turn the provider rate-limited or held out of
\*   funds keeps its card, counted toward nothing, its messages pending
\*   (limitedTurn); the governor's pause (ratelimit.go) is lim here. The
\*   refusal streak is the daemon's, across its lanes, as the code keeps it.
\* Mode is chosen at Init from Modes (the friend row); a change of mode, which
\* waits for every turn to end, is not modelled, nor the deferral of a batch
\* turn by the pacing, a lane's wall cap by its tier (lane_cap.go), the lane
\* sessions' opening, or the reads.
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
\*   "keepinhand"     a turn that did not ack its messages keeps them marked in
\*                    hand, so the stream's claim never hands them in again:
\*                    NoMessageLost
\*   "ackonread"      a message is acked as it is read, as a ping is:
\*                    AckedOnlyRead
\*   "twoturns"       a wake check or a card turn is pushed into a session whose
\*                    turn is still running: OneTurnPerSession
\*   "deliverbroken"  a broken session is still delivered into: NoDeliveryWhenBroken
\*   "pinghanded"     a ping read off the stream goes into the hand as a
\*                    message: PingNeverDelivered
\*   "fixedkill"      a running turn is stopped whether it prints or not:
\*                    StopOnlyWhenSilent
\*   "asideearly"     a lane sets its card aside after one turn with no
\*                    RESULT.md: SetAsideAfterCardTurns
\*   "filebeforebeat" the beat runs before the file the folder check reads
\*                    exists: FileBeforeBeat

EXTENDS Naturals, FiniteSets

CONSTANTS Window, MaxTime, MaxPings, Broken,
          Msgs, Lanes, Cards, K, MaxDeliveries, SilentStop, Refusals, Modes

VARIABLES now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
          daemonPongs, turns, owed, lim, limUntil,
          mode, held, quiet, lastStop, pel, acked, hand, inHand, carry, failed, readm, pingIn,
          streak, reason, broken, lcard, lrun, lquiet, cst, tries,
          file, visible, fileBeat
linkvars == <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, seen, silentSaid, outages, answered,
              daemonPongs, owed>>
limvars == <<lim, limUntil>>
msgvars == <<pel, acked, hand, inHand, carry, failed, readm, pingIn>>
sessvars == <<streak, reason, broken>>
lanevars == <<lcard, lrun, lquiet, cst, tries>>
filevars == <<file, visible, fileBeat>>
vars == <<linkvars, turns, limvars, mode, held, quiet, lastStop, msgvars, sessvars, lanevars, filevars>>

NoNonce == 0
M == 1..Msgs
L == 1..Lanes
C == 1..Cards
\* carry[m] is who holds m in a turn: NoTurn none, a lane, or the batch session
NoTurn == 0
Batch == Lanes + 1
CardTurns == 2
\* a turn's end: exit 0, any other failure, or the provider's refusal r
Reason(o) == IF o = "refused1" THEN 1 ELSE IF o = "refused2" THEN 2 ELSE 0
EndOutcomes == {"ok", "failed"} \cup {o \in {"refused1", "refused2"} : Reason(o) <= Refusals}

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
  /\ turns \in 0..2
  /\ owed \in BOOLEAN
  /\ lim \in {"up", "limited"}
  /\ limUntil \in 0..MaxTime
  /\ mode \in Modes
  /\ held \in BOOLEAN
  /\ quiet \in 0..SilentStop
  /\ lastStop \in {"none", "silent", "printing"}
  /\ pel \subseteq M /\ acked \subseteq M /\ hand \subseteq M /\ inHand \subseteq M /\ readm \subseteq M
  /\ carry \in [M -> 0..Batch]
  /\ failed \in [M -> 0..MaxDeliveries]
  /\ pingIn \in {"none", "hand", "turn"}
  /\ streak \in 0..K
  /\ reason \in 0..Refusals
  /\ broken \in BOOLEAN
  /\ lcard \in [L -> 0..Cards]
  /\ lrun \in [L -> 0..2]
  /\ lquiet \in [L -> 0..SilentStop]
  /\ cst \in [C -> {"queued", "lane", "done", "aside"}]
  /\ tries \in [C -> 0..CardTurns]
  /\ file \in BOOLEAN
  /\ visible \in BOOLEAN
  /\ fileBeat \in BOOLEAN

Busy == turns > 0

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
  /\ daemonPongs = 0 /\ turns = 0 /\ owed = FALSE
  /\ lim = "up" /\ limUntil = 0
  /\ mode \in Modes /\ held = FALSE /\ quiet = 0 /\ lastStop = "none"
  /\ pel = M /\ acked = {} /\ hand = {} /\ inHand = {} /\ readm = {}
  /\ carry = [m \in M |-> NoTurn] /\ failed = [m \in M |-> 0] /\ pingIn = "none"
  /\ streak = 0 /\ reason = 0 /\ broken = FALSE
  /\ lcard = [l \in L |-> 0] /\ lrun = [l \in L |-> 0] /\ lquiet = [l \in L |-> 0]
  /\ cst = [c \in C |-> "queued"] /\ tries = [c \in C |-> 0]
  /\ file = FALSE /\ visible = FALSE /\ fileBeat = FALSE

\* The clock (machine.go Tick): a window without a ping makes the
\* coordinator silent, said once at that moment; a window challenged with
\* no pong makes the session deaf. A running turn that printed nothing this
\* tick is a tick quieter (loop.watch reads its output each step).
GoesSilent == conn = "connected" /\ now + 1 - lastPing >= Window
Quieter(q, running) == IF running /\ q < SilentStop THEN q + 1 ELSE q
Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ IF GoesSilent
       THEN /\ conn' = "silent" /\ silentFrom' = lastPing /\ outages' = outages + 1
            /\ silentSaid' = IF Broken = "neversilent" THEN silentSaid ELSE silentSaid + 1
       ELSE /\ UNCHANGED <<conn, silentFrom, outages>>
            /\ silentSaid' = IF Broken = "silenttwice" /\ conn = "silent" THEN silentSaid + 1 ELSE silentSaid
  /\ chal' = IF chal = "challenged" /\ now + 1 - asked >= Window /\ Broken # "neverdeaf" THEN "deaf" ELSE chal
  /\ quiet' = Quieter(quiet, Busy)
  /\ lquiet' = [l \in L |-> Quieter(lquiet[l], lrun[l] > 0)]
  /\ UNCHANGED <<lastPing, nonce, asked, pongs, seen, answered, daemonPongs, owed, turns, limvars>>
  /\ UNCHANGED <<mode, held, lastStop, msgvars, sessvars, lcard, lrun, cst, tries, filevars>>

\* A ping from the coordinator with a fresh nonce (machine.go Ping;
\* daemon.go loop.ping), read or peeked off the stream whatever the session
\* is doing: the daemon answers it at once (daemonPongs) and acks it, the
\* connection is back (said once; the push is "coordinator back", not
\* counted here), the session is challenged with this nonce whatever it was
\* before, deaf staying deaf until a pong; a wake check (wake) owes the
\* session a wake turn. The ping itself is never a turn and never in the
\* hand: the session sees the nonce only when a turn carries the pong line.
\* The witnesses let the daemon's pong to a wake check end the challenge, and
\* put the ping into the hand.
Ping(wake) ==
  /\ nonce < MaxPings
  /\ nonce' = nonce + 1
  /\ daemonPongs' = daemonPongs + 1
  /\ conn' = "connected" /\ lastPing' = now
  /\ chal' = IF wake /\ Broken = "daemonpongends" THEN "quiet"
            ELSE IF chal = "deaf" THEN "deaf" ELSE "challenged"
  /\ asked' = now
  /\ owed' = ((owed \/ wake) /\ chal' # "quiet")
  /\ pingIn' = IF Broken = "pinghanded" /\ pingIn = "none" /\ ~broken THEN "hand" ELSE pingIn
  /\ UNCHANGED <<now, silentFrom, pongs, seen, silentSaid, outages, answered, turns, limvars>>
  /\ UNCHANGED <<mode, held, quiet, lastStop, pel, acked, hand, inHand, carry, failed, readm, sessvars, lanevars, filevars>>

\* The step's read of the stream (daemon.go read): every pending message not
\* already in hand or in a turn goes into the hand in one read, when the
\* session can take them (batch: no turn running or held; one-shot: always,
\* the lanes run while it reads) and is not broken. A message whose turn did
\* not ack it is pending again, and the stream's claim hands it in again
\* (bus.ClaimAfter). The witness acks each as it reads it.
CanRead == ~broken /\ (mode = "oneshot" \/ (~Busy /\ ~held))
Read ==
  LET fresh == pel \ inHand
  IN /\ fresh # {}
     /\ CanRead
     /\ IF Broken = "ackonread"
          THEN /\ pel' = pel \ fresh /\ acked' = acked \cup fresh
               /\ UNCHANGED <<hand, inHand>>
          ELSE /\ hand' = hand \cup fresh /\ inHand' = inHand \cup fresh
               /\ UNCHANGED <<pel, acked>>
     /\ UNCHANGED <<linkvars, turns, limvars, mode, held, quiet, lastStop, carry, failed, readm, pingIn, sessvars, lanevars, filevars>>

\* What heads a turn while a challenge is open (loop.head): the pong line for
\* the current nonce, which pays an owed wake check.
HeadSeen == IF chal # "quiet" THEN seen \cup {nonce} ELSE seen
HeadOwed == IF chal # "quiet" THEN FALSE ELSE owed

\* A delivery may start: the session is not broken and the harness is up
\* (each witness lets one through).
Deliverable == (~broken \/ Broken = "deliverbroken") /\ (lim = "up" \/ Broken = "deliverlimited")
SessionFree == turns = 0 \/ (Broken = "twoturns" /\ turns < 2)

\* A turn carrying every message in the hand starts in the free batch session
\* (daemon.go startBatch, loop.take, Batch): one turn, one text, oldest first.
\* With Msgs = 0 the messages are not tracked and one is always waiting (the
\* link instance, which checks the connection and the challenge).
StartBatch ==
  /\ mode = "batch" /\ SessionFree /\ ~held
  /\ hand # {} \/ pingIn = "hand" \/ Msgs = 0
  /\ Deliverable
  /\ turns' = turns + 1 /\ quiet' = 0
  /\ carry' = [m \in M |-> IF m \in hand THEN Batch ELSE carry[m]]
  /\ readm' = readm \cup hand
  /\ hand' = {}
  /\ pingIn' = IF pingIn = "hand" THEN "turn" ELSE pingIn
  /\ seen' = HeadSeen /\ owed' = HeadOwed
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs>>
  /\ UNCHANGED <<limvars, mode, held, lastStop, pel, acked, inHand, failed, sessvars, lanevars, filevars>>

\* A wake check owed to a free session with no message waiting is pushed
\* in as its own turn holding only the pong line (daemon.go startWake). The
\* code pushes it only in a step that read the stream with the session free,
\* so it never jumps a message waiting in the hand.
WakeTurn ==
  /\ mode = "batch" /\ SessionFree /\ ~held /\ owed /\ chal # "quiet"
  /\ hand = {}
  /\ Deliverable
  /\ turns' = turns + 1 /\ quiet' = 0 /\ owed' = FALSE
  /\ seen' = seen \cup {nonce}
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs>>
  /\ UNCHANGED <<limvars, mode, held, lastStop, msgvars, sessvars, lanevars, filevars>>

\* The turn held by a deferral is tried again once the harness is up
\* (daemon.go Run, l.retry), the same text and the same messages.
Retry ==
  /\ mode = "batch" /\ held /\ SessionFree
  /\ Deliverable
  /\ turns' = turns + 1 /\ quiet' = 0 /\ held' = FALSE
  /\ UNCHANGED <<linkvars, limvars, mode, lastStop, msgvars, sessvars, lanevars, filevars>>

\* settle (daemon.go): what a turn's end o does to the messages S it carried
\* and to the refusal streak. Every one leaves the turn (inHand and carry);
\* exit 0 acks them all at once; a refusal counts toward nothing they own, and
\* the K-th identical one in a row breaks the session; any other failure
\* counts toward MaxDeliveries, and the messages at it are acked, given up.
\* The witness leaves the ones it does not ack marked in hand.
Settle(S, o) ==
  LET given == IF o = "failed" THEN {m \in S : failed[m] + 1 >= MaxDeliveries} ELSE {}
      done  == IF o = "ok" THEN S ELSE given
      r     == Reason(o)
      s1    == IF r = 0 THEN 0 ELSE IF r = reason THEN streak + 1 ELSE 1
  IN /\ pel' = pel \ done
     /\ acked' = acked \cup done
     /\ inHand' = IF Broken = "keepinhand" THEN inHand \ done ELSE inHand \ S
     /\ carry' = [m \in M |-> IF m \in S THEN NoTurn ELSE carry[m]]
     /\ failed' = [m \in M |-> IF m \in done THEN 0
                               ELSE IF m \in S /\ o = "failed" THEN failed[m] + 1 ELSE failed[m]]
     /\ streak' = IF s1 > K THEN K ELSE s1
     /\ reason' = r
     /\ broken' = (broken \/ s1 >= K)

\* The messages the batch session's turn carries.
InBatch == {m \in M : carry[m] = Batch}

\* The batch turn ends by itself with o (batchDone).
BatchEnds(o) ==
  /\ Busy
  /\ turns' = turns - 1
  /\ Settle(InBatch, o)
  /\ pingIn' = IF pingIn = "turn" THEN "none" ELSE pingIn
  /\ UNCHANGED <<linkvars, limvars, mode, held, quiet, lastStop, hand, readm, lanevars, filevars>>

\* The silence rule (loop.watch): a turn that has printed nothing for
\* SilentStop is stopped, its process group signalled, and its end is a
\* failure (stopped, never a refusal or a deferral). The witness stops a
\* turn that is printing. lastStop says what the last stop stopped.
Stopping(q) == q >= SilentStop \/ Broken = "fixedkill"
StopSaid(q) == IF q >= SilentStop THEN "silent" ELSE "printing"
Stop ==
  /\ Busy /\ Stopping(quiet)
  /\ turns' = turns - 1
  /\ lastStop' = StopSaid(quiet)
  /\ Settle(InBatch, "failed")
  /\ pingIn' = IF pingIn = "turn" THEN "none" ELSE pingIn
  /\ UNCHANGED <<linkvars, limvars, mode, held, quiet, hand, readm, lanevars, filevars>>

\* A running turn prints (the adapter's output watch, WithOutputSeen).
Print ==
  /\ Busy /\ quiet > 0
  /\ quiet' = 0
  /\ UNCHANGED <<linkvars, turns, limvars, mode, held, lastStop, msgvars, sessvars, lanevars, filevars>>

\* The batch turn hits the harness's usage limit or empty balance
\* (limit.go Limits.see, Gate): it ends at once, Deferred, kept in the
\* daemon's hand with its messages, and the friend is down until a reset
\* after now (the text's own, else the rest).
HitLimit ==
  /\ Busy /\ lim = "up" /\ now < MaxTime
  /\ turns' = turns - 1 /\ held' = TRUE
  /\ lim' = "limited"
  /\ \E u \in (now + 1)..MaxTime : limUntil' = u
  /\ UNCHANGED <<linkvars, mode, quiet, lastStop, msgvars, sessvars, lanevars, filevars>>

\* After the reset one wake turn is tried (limit.go Limits.Gate, a nonce the
\* session must answer): answered, the friend is up again; still limited, the
\* next reset is taken from the text. The try is no message turn: a message
\* goes in only after it answered.
Wake ==
  /\ lim = "limited" /\ ~Busy /\ now >= limUntil /\ Broken # "neverwake"
  /\ \/ lim' = "up" /\ UNCHANGED limUntil
     \/ /\ now < MaxTime /\ lim' = "limited" /\ \E u \in (now + 1)..MaxTime : limUntil' = u
  /\ UNCHANGED <<linkvars, turns, mode, held, quiet, lastStop, msgvars, sessvars, lanevars, filevars>>

\* The session answers with a nonce it has seen (machine.go Pong): the
\* current one ends the challenge; any other changes nothing. The witness
\* takes any seen nonce; answered records which one did.
Pong(n) ==
  /\ n \in seen
  /\ chal # "quiet"
  /\ (n = nonce \/ Broken = "stalepong")
  /\ chal' = "quiet" /\ pongs' = pongs + 1 /\ answered' = n /\ owed' = FALSE
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, nonce, asked, seen, silentSaid, outages, daemonPongs>>
  /\ UNCHANGED <<turns, limvars, mode, held, quiet, lastStop, msgvars, sessvars, lanevars, filevars>>

\* ---------------------------------------------------------------- one-shot lanes

\* The messages lane l's turn carries.
InLane(l) == {m \in M : carry[m] = l}

\* A free lane starts a card's turn (lanes.go laneStep): its card, else the
\* next queued card no lane holds; with none it takes nothing, and the
\* messages wait. The messages in the hand ride along, the pong line heads it.
LaneFree(l) == lrun[l] = 0 \/ (Broken = "twoturns" /\ lrun[l] < 2 /\ lcard[l] # 0)
LaneStart(l) ==
  /\ mode = "oneshot" /\ LaneFree(l)
  /\ Deliverable
  /\ \E c \in C :
       /\ IF lcard[l] # 0 THEN c = lcard[l] ELSE cst[c] = "queued"
       /\ lcard' = [lcard EXCEPT ![l] = c]
       /\ cst' = [cst EXCEPT ![c] = "lane"]
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] + 1] /\ lquiet' = [lquiet EXCEPT ![l] = 0]
  /\ carry' = [m \in M |-> IF m \in hand THEN l ELSE carry[m]]
  /\ readm' = readm \cup hand
  /\ hand' = {}
  /\ pingIn' = IF pingIn = "hand" THEN "turn" ELSE pingIn
  /\ seen' = HeadSeen /\ owed' = HeadOwed
  /\ UNCHANGED <<now, conn, lastPing, silentFrom, chal, nonce, asked, pongs, silentSaid, outages, answered, daemonPongs>>
  /\ UNCHANGED <<turns, limvars, mode, held, quiet, lastStop, pel, acked, inHand, failed, sessvars, tries, filevars>>

\* What a lane's turn end does to its card (laneDone): RESULT.md there, the
\* card is done and the lane free; none, one more turn, and at CardTurns the
\* card is set aside and the lane free. The witness sets it aside at once.
CardAfter(l, result) ==
  LET c == lcard[l]
      t == tries[c] + 1
  IN IF result
       THEN /\ cst' = [cst EXCEPT ![c] = "done"] /\ lcard' = [lcard EXCEPT ![l] = 0]
            /\ UNCHANGED tries
       ELSE /\ tries' = [tries EXCEPT ![c] = IF t > CardTurns THEN CardTurns ELSE t]
            /\ IF t >= CardTurns \/ Broken = "asideearly"
                 THEN cst' = [cst EXCEPT ![c] = "aside"] /\ lcard' = [lcard EXCEPT ![l] = 0]
                 ELSE UNCHANGED <<cst, lcard>>

\* Lane l's turn ends by itself with o, its card's RESULT.md written or not.
LaneEnds(l, o, result) ==
  /\ lrun[l] > 0
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] - 1]
  /\ Settle(InLane(l), o)
  /\ CardAfter(l, result)
  /\ pingIn' = IF pingIn = "turn" THEN "none" ELSE pingIn
  /\ UNCHANGED <<linkvars, turns, limvars, mode, held, quiet, lastStop, hand, readm, lquiet, filevars>>

\* The silence rule on a lane's turn: stopped, a failure, its card handed
\* again or set aside as any turn without a RESULT.md (or done, when the
\* session wrote it before it fell silent).
LaneStop(l, result) ==
  /\ lrun[l] > 0 /\ Stopping(lquiet[l])
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] - 1]
  /\ lastStop' = StopSaid(lquiet[l])
  /\ Settle(InLane(l), "failed")
  /\ CardAfter(l, result)
  /\ pingIn' = IF pingIn = "turn" THEN "none" ELSE pingIn
  /\ UNCHANGED <<linkvars, turns, limvars, mode, held, quiet, hand, readm, lquiet, filevars>>

LanePrint(l) ==
  /\ lrun[l] > 0 /\ lquiet[l] > 0
  /\ lquiet' = [lquiet EXCEPT ![l] = 0]
  /\ UNCHANGED <<linkvars, turns, limvars, mode, held, quiet, lastStop, msgvars, sessvars, lcard, lrun, cst, tries, filevars>>

\* Lane l's turn is rate-limited or refused out of funds (limitedTurn): the
\* card stays in the lane, counted toward nothing; the messages leave the turn
\* pending, counted toward nothing; the lanes pause (lim) until the reset.
LaneLimited(l) ==
  /\ lrun[l] > 0 /\ now < MaxTime
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] - 1]
  /\ inHand' = IF Broken = "keepinhand" THEN inHand ELSE inHand \ InLane(l)
  /\ carry' = [m \in M |-> IF carry[m] = l THEN NoTurn ELSE carry[m]]
  /\ pingIn' = IF pingIn = "turn" THEN "none" ELSE pingIn
  /\ lim' = "limited"
  /\ IF lim = "up" THEN \E u \in (now + 1)..MaxTime : limUntil' = u ELSE UNCHANGED limUntil
  /\ UNCHANGED <<linkvars, turns, mode, held, quiet, lastStop, pel, acked, hand, failed, readm, sessvars>>
  /\ UNCHANGED <<lcard, lquiet, cst, tries, filevars>>

\* The session writes one file under a walked root (outbox, inbox, jobs, or
\* the working directory). It exists; the folder check has not read it yet.
WriteFile ==
  /\ ~file
  /\ file' = TRUE
  /\ UNCHANGED <<linkvars, turns, limvars, mode, held, quiet, lastStop, msgvars, sessvars, lanevars, visible, fileBeat>>

\* The folder check reads that file, so it is visible to the beat
\* (activity.go NewestWrite; daemon.go Run calls Activity before Beat).
\* A file that is not there is not invented.
FolderCheck ==
  /\ file /\ ~visible
  /\ visible' = TRUE
  /\ UNCHANGED <<linkvars, turns, limvars, mode, held, quiet, lastStop, msgvars, sessvars, lanevars, file, fileBeat>>

\* The beat that depends on the file (daemon.go Beat, carrying the walk's
\* answer). The design runs it only once the folder check has seen the file.
\* The witness runs it while the file does not exist. A walk that finds
\* nothing still beats with the zero time; that beat is not this one.
BeatOnFile ==
  /\ ~fileBeat
  /\ IF Broken = "filebeforebeat" THEN ~file ELSE visible
  /\ fileBeat' = TRUE
  /\ UNCHANGED <<linkvars, turns, limvars, mode, held, quiet, lastStop, msgvars, sessvars, lanevars, file, visible>>

Next ==
  \/ Tick
  \/ \E wake \in BOOLEAN : Ping(wake)
  \/ Read
  \/ StartBatch
  \/ WakeTurn
  \/ Retry
  \/ \E o \in EndOutcomes : BatchEnds(o)
  \/ Stop
  \/ Print
  \/ HitLimit
  \/ Wake
  \/ \E n \in 1..MaxPings : Pong(n)
  \/ \E l \in L :
       \/ LaneStart(l)
       \/ \E o \in EndOutcomes, result \in BOOLEAN : LaneEnds(l, o, result)
       \/ \E result \in BOOLEAN : LaneStop(l, result)
       \/ LanePrint(l)
       \/ LaneLimited(l)
  \/ WriteFile
  \/ FolderCheck
  \/ BeatOnFile

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

\* A turn starts: in the batch session, or in a lane's.
TurnStarts == turns' > turns \/ \E l \in L : lrun'[l] > lrun[l]

\* No turn starts while the harness is limited (limits-mean-down-w-r.w1~15):
\* a message or a wake check never goes into a session that cannot answer, so
\* every message stays pending, counted toward nothing. Pings are no turn.
NoTurnWhileLimited == [][TurnStarts => lim = "up"]_vars

\* Limited ends: once limited, the friend is up again (one wake turn after the
\* reset answered); WF(Wake) and the finite clock (a reset is always after
\* now, and no later than MaxTime) make it so.
LimitedEnds == (lim = "limited") ~> (lim = "up")

\* No message is lost: every message is pending or acked, never both, and
\* one pending is in the hand, in exactly one turn, or free on the stream for
\* the claim to hand in again; what the daemon marks in hand is exactly what
\* its hand and its turns hold.
NoMessageLost ==
  /\ pel \cap acked = {} /\ pel \cup acked = M
  /\ inHand = hand \cup {m \in M : carry[m] # NoTurn}
  /\ \A m \in hand : carry[m] = NoTurn
  /\ inHand \subseteq pel

\* No message is acked unread: an acked message went into a turn first (a
\* turn that ended 0, or the last of MaxDeliveries that failed).
AckedOnlyRead == acked \subseteq readm

\* No two turns run on one session at once: the batch session's, a deferred
\* one held included, and each lane's.
OneTurnPerSession ==
  /\ turns + (IF held THEN 1 ELSE 0) <= 1
  /\ \A l \in L : lrun[l] <= 1

\* A broken session receives no delivery: once broken, no turn starts, in
\* the batch session or in a lane, until the daemon restarts.
NoDeliveryWhenBroken == [][broken => ~TurnStarts]_vars

\* An answered ping is never delivered as a message: it never goes into the
\* hand, so no turn ever carries one.
PingNeverDelivered == pingIn = "none"

\* The silent stop: a turn is stopped only after it has printed nothing for
\* SilentStop; one that prints runs on, however long.
StopOnlyWhenSilent == lastStop # "printing"

\* A lane holds its card until its RESULT.md appears or CardTurns turns ended
\* without one; a card is held by one lane at most; and a card set aside had
\* its CardTurns turns.
SetAsideAfterCardTurns ==
  /\ \A c \in C : cst[c] = "aside" => tries[c] = CardTurns
  /\ \A l \in L : lcard[l] # 0 => cst[lcard[l]] = "lane"
  /\ \A l1, l2 \in L : l1 # l2 /\ lcard[l1] # 0 => lcard[l1] # lcard[l2]

\* The file is visible before the beat that depends on it: the folder check
\* has read it (daemon.go Run walks, then beats; activity.go NewestWrite).
\* A walk that finds nothing still beats with the zero time; that beat is
\* not this one.
FileBeforeBeat ==
  [][(~fileBeat /\ fileBeat') => (file /\ visible)]_vars

\* The one liveness claimed beyond that: the clock is finite here, and
\* DeafAfterWindow already says an open challenge is younger than a window at
\* every state, so once the clock moves a window it is answered or deaf.

=============================================================================
