------------------------------- MODULE Friend -------------------------------
\* nova-friend's daemon delivery and lanes machine (docs/SPEC-FRIEND.md;
\* pkg/friend/daemon.go, pkg/friend/lanes.go, pkg/friend/limit.go).
\*
\* A harness at its usage limit or out of credits is down until the reset
\* (limits-mean-down-w-r.w1~15; internal/friend/limits.go, limit.go Limits.Gate):
\* lim is "up" or "limited", limUntil the reset the text named (or the rest). A
\* batch turn that hits the limit ends at once, Deferred, and stays in the
\* daemon's hand with its messages (held, daemon.go batchDone) and the friend is
\* limited (HitLimit); while limited no batch turn starts, so every message
\* stays pending; once the reset has passed the limit clears (ResetLimit) and
\* the held turn is tried (Retry).
\*
\* The delivery (friend-e2e; daemon.go Run, read, take, settle; lanes.go):
\*   Messages are the friend's stream entries: pel the ones pending (sent, not
\*   acked), acked the ones acked. The daemon reads a pending message that is
\*   not already in its hand into the hand (Read; inHand is the code's
\*   loop.inHand, the entries in the hand or in a turn).
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
\*   is set aside (cst "aside", laneDone). A lane turn needs the session
\*   unbroken but not the harness up: the lanes bypass the limit gate
\*   (gatedLanes), so a lane turn starts while the harness is limited and a
\*   turn that hits one sends the friend down all the same (limitedTurn). A
\*   harness fault (exit 0, no report) keeps the card in the lane's hand,
\*   counted toward nothing (cst "lane", tries unchanged); a turn at the card's
\*   cap sets it aside at once (cst "capped"), whatever its tries. The messages
\*   in the hand ride along with a lane's card turn and are settled as a batch
\*   turn's are; a lane holding no card takes the next queued one, and with
\*   none takes nothing, messages included. A lane turn the provider
\*   rate-limited or held out of funds keeps its card, counted toward nothing,
\*   its messages pending (limitedTurn); the governor's pause (ratelimit.go) is
\*   lim here. The refusal streak is the daemon's, across its lanes, as the
\*   code keeps it.
\* Mode is chosen at Init from Modes (the friend row); a change of mode, which
\* waits for every turn to end, is not modelled, nor the deferral of a batch
\* turn by the pacing, the lane sessions' opening, or the reads.
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* each caught by one property below:
\*   "deliverlimited" a turn starts while the harness is limited: NoTurnWhileLimited
\*   "keepinhand"     a turn that did not ack its messages keeps them marked in
\*                    hand, so the stream's claim never hands them in again:
\*                    NoMessageLost
\*   "ackonread"      a message is acked as it is read:
\*                    AckedOnlyRead
\*   "twoturns"       a card turn is pushed into a session whose
\*                    turn is still running: OneTurnPerSession
\*   "deliverbroken"  a broken session is still delivered into: NoDeliveryWhenBroken
\*   "fixedkill"      a running turn is stopped whether it prints or not:
\*                    StopOnlyWhenSilent
\*   "asideearly"     a lane sets its card aside after one turn with no
\*                    RESULT.md and no cap: SetAsideAfterCardTurns
\*   "faultcounts"    a harness fault counts toward the card's tries:
\*                    SetAsideAfterCardTurns

EXTENDS Naturals, FiniteSets

CONSTANTS MaxTime, Broken,
          Msgs, Lanes, Cards, K, MaxDeliveries, SilentStop, Refusals, Modes

VARIABLES now, turns, lim, limUntil,
          mode, held, quiet, lastStop, pel, acked, hand, inHand, carry, failed, readm,
          streak, reason, broken, lcard, lrun, lquiet, cst, tries
limvars == <<lim, limUntil>>
msgvars == <<pel, acked, hand, inHand, carry, failed, readm>>
sessvars == <<streak, reason, broken>>
lanevars == <<lcard, lrun, lquiet, cst, tries>>
vars == <<now, turns, limvars, mode, held, quiet, lastStop, msgvars, sessvars, lanevars>>

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
  /\ turns \in 0..2
  /\ lim \in {"up", "limited"}
  /\ limUntil \in 0..MaxTime
  /\ mode \in Modes
  /\ held \in BOOLEAN
  /\ quiet \in 0..SilentStop
  /\ lastStop \in {"none", "silent", "printing"}
  /\ pel \subseteq M /\ acked \subseteq M /\ hand \subseteq M /\ inHand \subseteq M /\ readm \subseteq M
  /\ carry \in [M -> 0..Batch]
  /\ failed \in [M -> 0..MaxDeliveries]
  /\ streak \in 0..K
  /\ reason \in 0..Refusals
  /\ broken \in BOOLEAN
  /\ lcard \in [L -> 0..Cards]
  /\ lrun \in [L -> 0..2]
  /\ lquiet \in [L -> 0..SilentStop]
  /\ cst \in [C -> {"queued", "lane", "done", "aside", "capped"}]
  /\ tries \in [C -> 0..CardTurns]

Busy == turns > 0

Init ==
  /\ now = 0
  /\ turns = 0
  /\ lim = "up" /\ limUntil = 0
  /\ mode \in Modes /\ held = FALSE /\ quiet = 0 /\ lastStop = "none"
  /\ pel = M /\ acked = {} /\ hand = {} /\ inHand = {} /\ readm = {}
  /\ carry = [m \in M |-> NoTurn] /\ failed = [m \in M |-> 0]
  /\ streak = 0 /\ reason = 0 /\ broken = FALSE
  /\ lcard = [l \in L |-> 0] /\ lrun = [l \in L |-> 0] /\ lquiet = [l \in L |-> 0]
  /\ cst = [c \in C |-> "queued"] /\ tries = [c \in C |-> 0]

\* A running turn that printed nothing this tick is a tick quieter
\* (loop.watch reads its output each step).
Quieter(q, running) == IF running /\ q < SilentStop THEN q + 1 ELSE q
Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ quiet' = Quieter(quiet, Busy)
  /\ lquiet' = [l \in L |-> Quieter(lquiet[l], lrun[l] > 0)]
  /\ UNCHANGED <<turns, limvars, mode, held, lastStop, msgvars, sessvars, lcard, lrun, cst, tries>>

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
     /\ UNCHANGED <<now, turns, limvars, mode, held, quiet, lastStop, carry, failed, readm, sessvars, lanevars>>

\* A delivery may start: the session is not broken and the harness is up
\* (each witness lets one through).
Deliverable == (~broken \/ Broken = "deliverbroken") /\ (lim = "up" \/ Broken = "deliverlimited")

\* A lane's turn may start without the harness being up (gatedLanes.OpenSession
\* and DeliverTo go straight to the harness, not through the gate): a lane turn
\* needs the session unbroken, but not the limit. Its output is still read for
\* a limit (Watch), so a turn that hits one sends the friend down all the same
\* (LaneLimited).
LaneDeliverable == ~broken \/ Broken = "deliverbroken"

SessionFree == turns = 0 \/ (Broken = "twoturns" /\ turns < 2)

\* A turn carrying every message in the hand starts in the free batch session
\* (daemon.go startBatch, loop.take, Batch): one turn, one text, oldest first.
\* With Msgs = 0 the messages are not tracked and one is always waiting.
StartBatch ==
  /\ mode = "batch" /\ SessionFree /\ ~held
  /\ hand # {} \/ Msgs = 0
  /\ Deliverable
  /\ turns' = turns + 1 /\ quiet' = 0
  /\ carry' = [m \in M |-> IF m \in hand THEN Batch ELSE carry[m]]
  /\ readm' = readm \cup hand
  /\ hand' = {}
  /\ UNCHANGED <<now, limvars, mode, held, lastStop, pel, acked, inHand, failed, sessvars, lanevars>>

\* The turn held by a deferral is tried again once the harness is up
\* (daemon.go Run, l.retry), the same text and the same messages.
Retry ==
  /\ mode = "batch" /\ held /\ SessionFree
  /\ Deliverable
  /\ turns' = turns + 1 /\ quiet' = 0 /\ held' = FALSE
  /\ UNCHANGED <<now, limvars, mode, lastStop, msgvars, sessvars, lanevars>>

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
  /\ UNCHANGED <<now, limvars, mode, held, quiet, lastStop, hand, readm, lanevars>>

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
  /\ UNCHANGED <<now, limvars, mode, held, quiet, hand, readm, lanevars>>

\* A running turn prints (the adapter's output watch, WithOutputSeen).
Print ==
  /\ Busy /\ quiet > 0
  /\ quiet' = 0
  /\ UNCHANGED <<now, turns, limvars, mode, held, lastStop, msgvars, sessvars, lanevars>>

\* The batch turn hits the harness's usage limit or empty balance
\* (limit.go Limits.see, Gate): it ends at once, Deferred, kept in the
\* daemon's hand with its messages, and the friend is down until a reset
\* after now (the text's own, else the rest).
HitLimit ==
  /\ Busy /\ lim = "up" /\ now < MaxTime
  /\ turns' = turns - 1 /\ held' = TRUE
  /\ lim' = "limited"
  /\ \E u \in (now + 1)..MaxTime : limUntil' = u
  /\ UNCHANGED <<now, mode, quiet, lastStop, msgvars, sessvars, lanevars>>

\* Once the reset has passed, the limit clears.
ResetLimit ==
  /\ lim = "limited" /\ ~Busy /\ now >= limUntil
  /\ lim' = "up"
  /\ UNCHANGED <<now, turns, limUntil, mode, held, quiet, lastStop, msgvars, sessvars, lanevars>>

\* ---------------------------------------------------------------- one-shot lanes

\* The messages lane l's turn carries.
InLane(l) == {m \in M : carry[m] = l}

\* A free lane starts a card's turn (lanes.go laneStep): its card, else the
\* next queued card no lane holds; with none it takes nothing, and the
\* messages wait. The messages in the hand ride along.
LaneFree(l) == lrun[l] = 0 \/ (Broken = "twoturns" /\ lrun[l] < 2 /\ lcard[l] # 0)
LaneStart(l) ==
  /\ mode = "oneshot" /\ LaneFree(l)
  /\ LaneDeliverable
  /\ \E c \in C :
       /\ IF lcard[l] # 0 THEN c = lcard[l] ELSE cst[c] = "queued"
       /\ lcard' = [lcard EXCEPT ![l] = c]
       /\ cst' = [cst EXCEPT ![c] = "lane"]
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] + 1] /\ lquiet' = [lquiet EXCEPT ![l] = 0]
  /\ carry' = [m \in M |-> IF m \in hand THEN l ELSE carry[m]]
  /\ readm' = readm \cup hand
  /\ hand' = {}
  /\ UNCHANGED <<now, turns, limvars, mode, held, quiet, lastStop, pel, acked, inHand, failed, sessvars, tries>>

\* What a lane's turn end does to its card (laneDone): RESULT.md there, the
\* card is done and the lane free; a harness fault (exit 0, no report) keeps
\* the card in the lane's hand counted toward nothing; a turn at the card's
\* cap sets it aside at once (cst "capped"); else one more turn, and at
\* CardTurns the card is set aside and the lane free. The witnesses set it
\* aside after one turn whatever the cap ("asideearly") and count a fault
\* toward the tries ("faultcounts").
CardOutcomes == {"done", "failed", "fault", "capped"}

CardAfter(l, e) ==
  LET c == lcard[l]
      t == tries[c] + 1
      step == IF t > CardTurns THEN CardTurns ELSE t
  IN CASE e = "done" ->
       /\ cst' = [cst EXCEPT ![c] = "done"] /\ lcard' = [lcard EXCEPT ![l] = 0]
       /\ UNCHANGED tries
     [] e = "fault" ->
       /\ UNCHANGED <<cst, lcard>>
       /\ IF Broken = "faultcounts"
            THEN tries' = [tries EXCEPT ![c] = step]
            ELSE UNCHANGED tries
     [] e = "capped" ->
       /\ cst' = [cst EXCEPT ![c] = "capped"] /\ lcard' = [lcard EXCEPT ![l] = 0]
       /\ tries' = [tries EXCEPT ![c] = step]
     [] OTHER ->
       /\ tries' = [tries EXCEPT ![c] = step]
       /\ IF t >= CardTurns \/ Broken = "asideearly"
            THEN cst' = [cst EXCEPT ![c] = "aside"] /\ lcard' = [lcard EXCEPT ![l] = 0]
            ELSE UNCHANGED <<cst, lcard>>

\* Lane l's turn ends by itself with o, its card ended e (RESULT.md, a failed
\* turn, a harness fault, or the card's cap).
LaneEnds(l, o, e) ==
  /\ lrun[l] > 0
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] - 1]
  /\ Settle(InLane(l), o)
  /\ CardAfter(l, e)
  /\ UNCHANGED <<now, turns, limvars, mode, held, quiet, lastStop, hand, readm, lquiet>>

\* The silence rule on a lane's turn: stopped, a failure, its card handed
\* again or set aside as any turn without a RESULT.md (or done, when the
\* session wrote it before it fell silent). A stopped turn is no fault and
\* no cap, so the card ends done or failed only.
LaneStop(l, result) ==
  /\ lrun[l] > 0 /\ Stopping(lquiet[l])
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] - 1]
  /\ lastStop' = StopSaid(lquiet[l])
  /\ Settle(InLane(l), "failed")
  /\ CardAfter(l, IF result THEN "done" ELSE "failed")
  /\ UNCHANGED <<now, turns, limvars, mode, held, quiet, hand, readm, lquiet>>

LanePrint(l) ==
  /\ lrun[l] > 0 /\ lquiet[l] > 0
  /\ lquiet' = [lquiet EXCEPT ![l] = 0]
  /\ UNCHANGED <<now, turns, limvars, mode, held, quiet, lastStop, msgvars, sessvars, lcard, lrun, cst, tries>>

\* Lane l's turn is rate-limited or refused out of funds (limitedTurn): the
\* card stays in the lane, counted toward nothing; the messages leave the turn
\* pending, counted toward nothing; the lanes pause (lim) until the reset.
LaneLimited(l) ==
  /\ lrun[l] > 0 /\ now < MaxTime
  /\ lrun' = [lrun EXCEPT ![l] = lrun[l] - 1]
  /\ inHand' = IF Broken = "keepinhand" THEN inHand ELSE inHand \ InLane(l)
  /\ carry' = [m \in M |-> IF carry[m] = l THEN NoTurn ELSE carry[m]]
  /\ lim' = "limited"
  /\ IF lim = "up" THEN \E u \in (now + 1)..MaxTime : limUntil' = u ELSE UNCHANGED limUntil
  /\ UNCHANGED <<now, turns, mode, held, quiet, lastStop, pel, acked, hand, failed, readm, sessvars>>
  /\ UNCHANGED <<lcard, lquiet, cst, tries>>

Next ==
  \/ Tick
  \/ Read
  \/ StartBatch
  \/ Retry
  \/ \E o \in EndOutcomes : BatchEnds(o)
  \/ Stop
  \/ Print
  \/ HitLimit
  \/ ResetLimit
  \/ \E l \in L :
       \/ LaneStart(l)
       \/ \E o \in EndOutcomes, e \in CardOutcomes : LaneEnds(l, o, e)
       \/ \E result \in BOOLEAN : LaneStop(l, result)
       \/ LanePrint(l)
       \/ LaneLimited(l)

Spec == Init /\ [][Next]_vars

\* ---------------------------------------------------------------- the rules

\* A turn starts: in the batch session, or in a lane's.
TurnStarts == turns' > turns \/ \E l \in L : lrun'[l] > lrun[l]

\* No batch turn starts while the harness is limited (limits-mean-down-w-r.w1~15):
\* a message never goes into a session that cannot answer, so every message
\* stays pending, counted toward nothing. A lane's turn is exempt: the lanes
\* bypass the gate, and a lane turn that hits a limit sends the friend Down all
\* the same (LaneLimited).
NoTurnWhileLimited == [][(turns' > turns) => lim = "up"]_vars

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

\* The silent stop: a turn is stopped only after it has printed nothing for
\* SilentStop; one that prints runs on, however long.
StopOnlyWhenSilent == lastStop # "printing"

\* A lane holds its card until its RESULT.md appears or CardTurns turns ended
\* without one; a card is held by one lane at most; and a card set aside by
\* its turns had its CardTurns turns (one set aside by its cap is "capped",
\* whatever its tries). A card a lane still holds has not had its CardTurns
\* turns: a harness fault counts toward nothing, so it never sets a card
\* aside.
SetAsideAfterCardTurns ==
  /\ \A c \in C : cst[c] = "aside" => tries[c] = CardTurns
  /\ \A c \in C : cst[c] = "lane" => tries[c] < CardTurns
  /\ \A l \in L : lcard[l] # 0 => cst[lcard[l]] = "lane"
  /\ \A l1, l2 \in L : l1 # l2 /\ lcard[l1] # 0 => lcard[l1] # lcard[l2]

=============================================================================
