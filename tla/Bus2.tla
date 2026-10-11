-------------------------------- MODULE Bus2 --------------------------------
\* nova-bus's delivery machine (docs/SPEC-BUS.md; pkg/bus/bus.go: Send, Recv,
\* RecvKinds, AckEntry and Ack; pkg/bus/redis.go: the Redis commands behind
\* them, XADD in one MULTI/EXEC, XGROUP CREATE, XAUTOCLAIM, XREADGROUP and
\* XACK).
\*
\* The state the code owns, per message m and recipient r: st[m][r], what
\* r's stream and group say of m ("none": not on the stream; "new": on the
\* stream, never delivered; "pending": delivered to a reader and not
\* acked; "acked"); holder[m][r], who holds it pending; and log, the order
\* the messages were sent in (the log stream, and each recipient's stream in
\* the same order). The outside: to[m], who each message is for, chosen at the
\* start (its to and cc, folded into one set, pkg/bus/bus.go:327); alive, the
\* readers that are up.
\*
\* The code has one consumer name for every reader (pkg/bus/bus.go:51), so
\* who holds an entry is told by its idle time, never by a name: a reader
\* that died, or one that has been idle past ClaimAfter (pkg/bus/bus.go:45),
\* is the same to the store. holder[m][r] = NoOne is that: no live reader
\* holds the entry -- it is not pending, or its holder has been idle past
\* ClaimAfter and any reader may claim it.
\*
\* The actions are the verbs and the outside events: Send (one message to
\* every recipient's stream and the log at once), Recv (a reader of r takes
\* the oldest pending message whose holder is dead or has stalled, else the
\* oldest new one), Ack (by r, of a message it was handed; again is a no-op),
\* Stall (a live holder idle past ClaimAfter becomes claimable by any reader:
\* its holder is NoOne), Crash (a reader dies holding what it holds),
\* Restart. The rules about one step (what a recv found, what an ack moved)
\* are action properties over st and st'.
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* each caught by one property below:
\*   "partial"         send writes the streams one by one and may stop
\*                     between them (no MULTI/EXEC): OnEveryStreamOrNone
\*   "newfirst"        recv reads new entries before it claims pending ones:
\*                     PendingBeforeNew
\*   "steal"           recv claims a pending entry whoever holds it and
\*                     however briefly (XAUTOCLAIM with min-idle 0): a second
\*                     reader takes a message a live one is delivering, so
\*                     one message is delivered twice: HeldStaysHeld
\*   "claimfresh"      recv claims a pending entry before its holder has been
\*                     idle for ClaimAfter (the claim's min-idle is not
\*                     respected): HeldStaysHeld
\*   "ackundelivered"  ack takes any id on the stream, delivered or not:
\*                     AckOnlyDelivered
\*   "ackreopens"      a second ack puts the message back on the stream as
\*                     new (an ack that deletes and re-adds): AckedStaysAcked

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Recipients, Messages, Consumers, MaxCrashes, Broken

VARIABLES st, holder, log, to, alive, crashes
vars == <<st, holder, log, to, alive, crashes>>

States == {"none", "new", "pending", "acked"}
NoOne == "-"

TypeOK ==
  /\ st \in [Messages -> [Recipients -> States]]
  /\ holder \in [Messages -> [Recipients -> Consumers \cup {NoOne}]]
  /\ to \in [Messages -> SUBSET Recipients]
  /\ alive \subseteq Consumers
  /\ crashes \in 0..MaxCrashes

Sent == {m \in Messages : \E i \in 1..Len(log) : log[i] = m}
Position(m) == CHOOSE i \in 1..Len(log) : log[i] = m

\* The oldest message of r in state s: first on the stream.
Oldest(r, s) ==
  CHOOSE m \in Messages :
    /\ st[m][r] = s
    /\ \A n \in Messages : st[n][r] = s => Position(m) <= Position(n)
Has(r, s) == \E m \in Messages : st[m][r] = s
\* The pending messages a reader of r may claim: the ones whose holder is
\* dead or has been idle past ClaimAfter (NoOne). The steal witness claims
\* one whoever holds it, and claimfresh one before ClaimAfter.
Claimable(r, c) ==
  {m \in Messages : st[m][r] = "pending" /\ (holder[m][r] \notin alive \/ Broken = "steal" \/ Broken = "claimfresh")}
OldestClaimable(r, c) ==
  CHOOSE m \in Claimable(r, c) : \A n \in Claimable(r, c) : Position(m) <= Position(n)

Init ==
  /\ st = [m \in Messages |-> [r \in Recipients |-> "none"]]
  /\ holder = [m \in Messages |-> [r \in Recipients |-> NoOne]]
  /\ log = <<>>
  /\ to \in [Messages -> (SUBSET Recipients) \ {{}}]
  /\ alive = Consumers
  /\ crashes = 0

\* nova-bus send: one entry on every recipient's stream and the log, in
\* one transaction. The partial witness writes some subset of the streams.
Send(m) ==
  /\ m \notin Sent
  /\ \E got \in SUBSET to[m] :
       /\ (Broken = "partial" \/ got = to[m])
       /\ st' = [st EXCEPT ![m] = [r \in Recipients |-> IF r \in got THEN "new" ELSE @[r]]]
  /\ log' = Append(log, m)
  /\ UNCHANGED <<holder, to, alive, crashes>>

\* nova-bus recv for r by reader c: the oldest claimable pending message
\* (a dead or stalled holder), else the oldest new one.
RecvPending(r, c) ==
  /\ c \in alive
  /\ Claimable(r, c) # {}
  /\ holder' = [holder EXCEPT ![OldestClaimable(r, c)][r] = c]
  /\ UNCHANGED <<st, log, to, alive, crashes>>

RecvNew(r, c) ==
  /\ c \in alive
  /\ (Claimable(r, c) = {} \/ Broken = "newfirst")
  /\ Has(r, "new")
  /\ LET m == Oldest(r, "new") IN
       /\ st' = [st EXCEPT ![m][r] = "pending"]
       /\ holder' = [holder EXCEPT ![m][r] = c]
  /\ UNCHANGED <<log, to, alive, crashes>>

Recv(r, c) == RecvPending(r, c) \/ RecvNew(r, c)

\* nova-bus ack by r of m: pending becomes acked; acked again changes
\* nothing (XACK of an entry not in the PEL is 0).
Ack(r, m) ==
  /\ \/ st[m][r] = "pending"
     \/ st[m][r] = "acked"
     \/ (st[m][r] = "new" /\ Broken = "ackundelivered")
  /\ IF st[m][r] = "acked" /\ Broken = "ackreopens"
       THEN st' = [st EXCEPT ![m][r] = "new"]
       ELSE st' = [st EXCEPT ![m][r] = "acked"]
  /\ holder' = [holder EXCEPT ![m][r] = NoOne]
  /\ UNCHANGED <<log, to, alive, crashes>>

\* A live holder idle past ClaimAfter becomes claimable by any reader: the
\* entry's holder is NoOne, and a reader that died or stalled is the same to
\* the store (the code's XAUTOCLAIM with min-idle ClaimAfter).
Stall(m, r) ==
  /\ st[m][r] = "pending"
  /\ holder[m][r] \in alive
  /\ holder' = [holder EXCEPT ![m][r] = NoOne]
  /\ UNCHANGED <<st, log, to, alive, crashes>>

\* A reader dies holding what it holds: the store keeps it pending.
Crash(c) ==
  /\ c \in alive /\ crashes < MaxCrashes
  /\ alive' = alive \ {c}
  /\ crashes' = crashes + 1
  /\ UNCHANGED <<st, holder, log, to>>

Restart(c) ==
  /\ c \notin alive
  /\ alive' = alive \cup {c}
  /\ UNCHANGED <<st, holder, log, to, crashes>>

Next ==
  \/ \E m \in Messages : Send(m)
  \/ \E r \in Recipients, c \in Consumers : Recv(r, c)
  \/ \E r \in Recipients, m \in Messages : Ack(r, m)
  \/ \E m \in Messages, r \in Recipients : Stall(m, r)
  \/ \E c \in Consumers : Crash(c) \/ Restart(c)

\* Fairness, per action: every recipient keeps reading new messages and
\* acking what it was handed, and a dead reader comes back; a crash and a
\* stall are never owed, and crashes are bounded.
Fairness ==
  /\ \A r \in Recipients, c \in Consumers : WF_vars(RecvNew(r, c))
  /\ \A r \in Recipients, m \in Messages : WF_vars(Ack(r, m))
  /\ \A c \in Consumers : WF_vars(Restart(c))

Spec == Init /\ [][Next]_vars /\ Fairness

\* ---------------------------------------------------------------- the rules

\* A message is on every one of its recipients' streams, or on none: a
\* sent message is reachable by every recipient it names.
OnEveryStreamOrNone ==
  \A m \in Messages, r \in Recipients :
    (m \in Sent /\ r \in to[m]) => st[m][r] # "none"

\* Every message on a stream was sent, and to that recipient: nothing is
\* delivered that was not sent.
OnStreamWasSent ==
  \A m \in Messages, r \in Recipients :
    st[m][r] # "none" => (m \in Sent /\ r \in to[m])

\* Nothing is lost: a sent message is acked, or still on the stream for
\* recv to hand out (new or pending).
NothingLost ==
  \A m \in Messages, r \in Recipients :
    (m \in Sent /\ r \in to[m]) => st[m][r] \in {"new", "pending", "acked"}

\* A recv that hands out a new message found nothing pending for that
\* recipient that a dead reader held, or that had stalled past ClaimAfter:
\* after a crash or a stall, what that reader held comes first.
PendingBeforeNew ==
  [][\A r \in Recipients :
       (\E m \in Messages : st[m][r] = "new" /\ st'[m][r] = "pending") =>
         (\A m \in Messages : st[m][r] = "pending" => holder[m][r] \in alive)]_vars

\* A message held by a live reader stays with it while held under
\* ClaimAfter: it is delivered once while held, and only an ack, a stall
\* (the holder idle past ClaimAfter) or the holder's death moves it. The
\* steal and claimfresh witnesses let a second reader take it.
HeldStaysHeld ==
  [][\A m \in Messages, r \in Recipients :
       (st[m][r] = "pending" /\ holder[m][r] \in alive /\ st'[m][r] = "pending") =>
         holder'[m][r] \in {holder[m][r], NoOne}]_vars

\* Only a delivered message is acked: nothing goes from new to acked.
AckOnlyDelivered ==
  [][\A m \in Messages, r \in Recipients : st[m][r] = "new" => st'[m][r] # "acked"]_vars

\* Ack is idempotent: once acked, acked (the second ack changes nothing).
AckedStaysAcked ==
  [][\A m \in Messages, r \in Recipients : st[m][r] = "acked" => st'[m][r] = "acked"]_vars

\* Liveness: every sent message is acked by every recipient it names, once
\* the crashes stop (they are bounded) and some reader keeps reading.
EveryMessageIsAcked ==
  \A m \in Messages, r \in Recipients :
    (m \in Sent /\ r \in to[m]) ~> (st[m][r] = "acked")

=============================================================================
