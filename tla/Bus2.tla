-------------------------------- MODULE Bus2 --------------------------------
\* nova-bus2's delivery machine (docs/SPEC-BUS2.md; internal/bus2/bus2.go:
\* Send, Recv, AckEntry and Ack; the Redis commands behind them are XADD in
\* one MULTI/EXEC, XGROUP CREATE, XAUTOCLAIM, XREADGROUP and XACK).
\*
\* The state the code owns, per message m and recipient r: st[m][r], what
\* r's stream and group say of m ("none": not on the stream; "new": on the
\* stream, never delivered; "pending": delivered to a consumer and not
\* acked; "acked"); holder[m][r], the consumer that holds it pending; and
\* log, the order the messages were sent in (the log stream, and each
\* recipient's stream in the same order). The outside: to[m], who each
\* message is for, chosen at the start; alive, the consumers that are up.
\*
\* The receipts (docs/SPEC-BUS.md, message-receipts.w1; internal/bus/
\* receipt.go MarkReceipts, Advance and Acted, redis.go forwardScript,
\* internal/friend/daemon.go read and settle): receipt[m][r], r's receipt of m
\* in the hash beside its stream ("none", "delivered", "read", "acted"), kept
\* in the store, so a crash forgets none of it, and written only by the
\* store's one forward step (Advance: only forward, and only delivered starts
\* one); inTurn[m][r], whether a session's turn carrying m runs (its consumer
\* is m's holder: a claim moves only what a dead consumer held, and a crash
\* ends the turn); timesActed[m][r], how many turns carrying m ended acted,
\* the count the idempotent take bounds. Two variables exist only for the
\* reversed witnesses and stay constant in the design: memo[m][r], the
\* consumer whose process remembers that a turn acted on m (the memdrop
\* witness's in-memory set), and wpend[m][r], a receipt a writer read the
\* hash for and has not yet written (the readwrite witness's second step).
\*
\* The actions are the verbs and the outside events: Send (one message to
\* every recipient's stream and the log at once), Recv (a consumer of r takes
\* the oldest pending message whose holder is dead, else the oldest new one;
\* a live holder keeps its message: the code tells a dead holder by the
\* entry's idle time past ClaimAfter; a recv marks the receipt delivered),
\* Take (the daemon's take of a delivery its reader holds: the store's acted
\* receipt drops it and acks it, else the adapter accepts a turn carrying
\* it, and that step, and no earlier one, marks it read), EndTurn (the turn
\* ended at exit 0: acted), Reply (r's session sends a message whose re
\* names m: acted, in the send's transaction, when r's reader was handed m),
\* Ack (by r, of a message it was handed; again is a no-op), Crash (a
\* consumer dies holding what it holds, and its turn with it), Restart. The
\* rules about one step (what a recv found, what an ack moved, what moved a
\* receipt) are action properties over the primed and unprimed state.
\*
\* The instances. MCBus2.cfg, the design with every rule, is two
\* recipients, three messages and two consumers, the instance the delivery
\* machine was checked on before the receipts; the receipts' witnesses are
\* one recipient (the receipts of two recipients share nothing), the
\* duplicate ones of one message: the redelivery needs no other.
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
\*   "ackundelivered"  ack takes any id on the stream, delivered or not:
\*                     AckOnlyDelivered
\*   "ackreopens"      a second ack puts the message back on the stream as
\*                     new (an ack that deletes and re-adds): AckedStaysAcked
\*   "acttwice"        a take that never asks whether m was acted: the
\*                     delivery a turn acted on, still unacked or handed in
\*                     again after a crash, goes into a second turn:
\*                     NoMessageActedTwice
\* The four of the receipts' invariants, one each:
\*   "readearly"       (1) the daemon marks read when it sees the delivery,
\*                     before the adapter accepts a turn (Look): the receipt
\*                     says read and no turn carries m: ReadOnlyWhenAccepted
\*   "memdrop"         (2) the drop asks the daemon's memory of acted ids,
\*                     not the store: the crash that forgets it is followed
\*                     by the claim that hands m in again, and a second turn
\*                     acts on it: NoMessageActedTwice
\*   "readwrite"       (3) the take's read mark is a read of the hash and a
\*                     separate write (attempt 4's HGETALL, then HSET): the
\*                     reply's acted lands between them and the late write
\*                     moves the receipt back: ReceiptNeverMovesBack
\*   "replyundelivered" (4) the reply's mark is a blind HSET of acted: an
\*                     answer to a message r's reader was never handed is
\*                     acted with nothing delivered: ActedImpliesDelivered

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Recipients, Messages, Consumers, MaxCrashes, Broken

VARIABLES st, holder, log, to, alive, crashes, receipt, inTurn, timesActed, memo, wpend
vars == <<st, holder, log, to, alive, crashes, receipt, inTurn, timesActed, memo, wpend>>

States == {"none", "new", "pending", "acked"}
ReceiptStates == {"none", "delivered", "read", "acted"}
NoOne == "-"

TypeOK ==
  /\ st \in [Messages -> [Recipients -> States]]
  /\ holder \in [Messages -> [Recipients -> Consumers \cup {NoOne}]]
  /\ to \in [Messages -> SUBSET Recipients]
  /\ alive \subseteq Consumers
  /\ crashes \in 0..MaxCrashes
  /\ receipt \in [Messages -> [Recipients -> ReceiptStates]]
  /\ inTurn \in [Messages -> [Recipients -> BOOLEAN]]
  /\ timesActed \in [Messages -> [Recipients -> Nat]]
  /\ memo \in [Messages -> [Recipients -> Consumers \cup {NoOne}]]
  /\ wpend \in [Messages -> [Recipients -> ReceiptStates \cup {NoOne}]]

Sent == {m \in Messages : \E i \in 1..Len(log) : log[i] = m}
Position(m) == CHOOSE i \in 1..Len(log) : log[i] = m

\* The oldest message of r in state s: first on the stream.
Oldest(r, s) ==
  CHOOSE m \in Messages :
    /\ st[m][r] = s
    /\ \A n \in Messages : st[n][r] = s => Position(m) <= Position(n)
Has(r, s) == \E m \in Messages : st[m][r] = s
\* The pending messages a consumer c of r may claim: the ones whose holder
\* is dead (idle past ClaimAfter, in the code).
Claimable(r, c) ==
  {m \in Messages : st[m][r] = "pending" /\ (holder[m][r] \notin alive \/ Broken = "steal")}
OldestClaimable(r, c) ==
  CHOOSE m \in Claimable(r, c) : \A n \in Claimable(r, c) : Position(m) <= Position(n)

\* A receipt moves only forward: none, delivered, read, acted.
ReceiptRank(s) ==
  CASE s = "none" -> 0
    [] s = "delivered" -> 1
    [] s = "read" -> 2
    [] s = "acted" -> 3

\* The store's one forward step (receipt.go Advance, redis.go forwardScript):
\* the receipt cur with s written over it.
Advance(cur, s) ==
  IF ReceiptRank(cur) >= ReceiptRank(s) \/ (cur = "none" /\ s # "delivered") THEN cur ELSE s

Init ==
  /\ st = [m \in Messages |-> [r \in Recipients |-> "none"]]
  /\ holder = [m \in Messages |-> [r \in Recipients |-> NoOne]]
  /\ log = <<>>
  /\ to \in [Messages -> (SUBSET Recipients) \ {{}}]
  /\ alive = Consumers
  /\ crashes = 0
  /\ receipt = [m \in Messages |-> [r \in Recipients |-> "none"]]
  /\ inTurn = [m \in Messages |-> [r \in Recipients |-> FALSE]]
  /\ timesActed = [m \in Messages |-> [r \in Recipients |-> 0]]
  /\ memo = [m \in Messages |-> [r \in Recipients |-> NoOne]]
  /\ wpend = [m \in Messages |-> [r \in Recipients |-> NoOne]]

\* nova-bus2 send: one entry on every recipient's stream and the log, in
\* one transaction. The partial witness writes some subset of the streams.
Send(m) ==
  /\ m \notin Sent
  /\ \E got \in SUBSET to[m] :
       /\ (Broken = "partial" \/ got = to[m])
       /\ st' = [st EXCEPT ![m] = [r \in Recipients |-> IF r \in got THEN "new" ELSE @[r]]]
  /\ log' = Append(log, m)
  /\ UNCHANGED <<holder, to, alive, crashes, receipt, inTurn, timesActed, memo, wpend>>

\* nova-bus2 recv for r by consumer c: the oldest claimable pending
\* message, else the oldest new one; either marks r's receipt delivered.
RecvPending(r, c) ==
  /\ c \in alive
  /\ Claimable(r, c) # {}
  /\ LET m == OldestClaimable(r, c) IN
       /\ holder' = [holder EXCEPT ![m][r] = c]
       /\ receipt' = [receipt EXCEPT ![m][r] = Advance(@, "delivered")]
  /\ UNCHANGED <<st, log, to, alive, crashes, inTurn, timesActed, memo, wpend>>

RecvNew(r, c) ==
  /\ c \in alive
  /\ (Claimable(r, c) = {} \/ Broken = "newfirst")
  /\ Has(r, "new")
  /\ LET m == Oldest(r, "new") IN
       /\ st' = [st EXCEPT ![m][r] = "pending"]
       /\ holder' = [holder EXCEPT ![m][r] = c]
       /\ receipt' = [receipt EXCEPT ![m][r] = Advance(@, "delivered")]
  /\ UNCHANGED <<log, to, alive, crashes, inTurn, timesActed, memo, wpend>>

Recv(r, c) == RecvPending(r, c) \/ RecvNew(r, c)

\* Whether the take drops m: the store's acted receipt (Bus.Acted); the
\* memdrop witness asks the holder's memory, the acttwice witness nothing.
Dropped(r, m) ==
  CASE Broken = "acttwice" -> FALSE
    [] Broken = "memdrop" -> memo[m][r] = holder[m][r]
    [] OTHER -> receipt[m][r] = "acted"

\* The daemon's take of a delivery its reader holds (daemon.go read, then
\* the turn): dropped and acked, or the adapter accepts a turn carrying it,
\* the step that marks it read. The readwrite witness reads the hash here
\* and writes later (Write).
Take(r, m) ==
  /\ st[m][r] = "pending"
  /\ holder[m][r] \in alive
  /\ ~inTurn[m][r]
  /\ wpend[m][r] = NoOne
  /\ IF Dropped(r, m)
       THEN /\ st' = [st EXCEPT ![m][r] = "acked"]
            /\ holder' = [holder EXCEPT ![m][r] = NoOne]
            /\ UNCHANGED <<inTurn, receipt, wpend>>
       ELSE /\ inTurn' = [inTurn EXCEPT ![m][r] = TRUE]
            /\ IF Broken = "readwrite" /\ Advance(receipt[m][r], "read") # receipt[m][r]
                 THEN /\ wpend' = [wpend EXCEPT ![m][r] = "read"]
                      /\ UNCHANGED receipt
                 ELSE /\ receipt' = [receipt EXCEPT ![m][r] = Advance(@, "read")]
                      /\ UNCHANGED wpend
            /\ UNCHANGED <<st, holder>>
  /\ UNCHANGED <<log, to, alive, crashes, timesActed, memo>>

\* The readwrite witness's second step: the write of what was read, blind.
Write(r, m) ==
  /\ wpend[m][r] # NoOne
  /\ receipt' = [receipt EXCEPT ![m][r] = wpend[m][r]]
  /\ wpend' = [wpend EXCEPT ![m][r] = NoOne]
  /\ UNCHANGED <<st, holder, log, to, alive, crashes, inTurn, timesActed, memo>>

\* The readearly witness: the daemon marks read when it sees the delivery,
\* no turn accepted.
Look(r, m) ==
  /\ Broken = "readearly"
  /\ st[m][r] = "pending"
  /\ holder[m][r] \in alive
  /\ ~inTurn[m][r]
  /\ receipt[m][r] = "delivered"
  /\ receipt' = [receipt EXCEPT ![m][r] = "read"]
  /\ UNCHANGED <<st, holder, log, to, alive, crashes, inTurn, timesActed, memo, wpend>>

\* The turn carrying m ends at exit 0 (daemon.go settle): acted, before the
\* ack, which is Ack's step; a crash between the two leaves m pending.
EndTurn(r, m) ==
  /\ inTurn[m][r]
  /\ wpend[m][r] = NoOne
  /\ inTurn' = [inTurn EXCEPT ![m][r] = FALSE]
  /\ receipt' = [receipt EXCEPT ![m][r] = Advance(@, "acted")]
  /\ timesActed' = [timesActed EXCEPT ![m][r] = @ + 1]
  /\ memo' = IF Broken = "memdrop" THEN [memo EXCEPT ![m][r] = holder[m][r]] ELSE memo
  /\ UNCHANGED <<st, holder, log, to, alive, crashes, wpend>>

\* r's session sends a message whose re names m (receipt.go owe, a Forward
\* mark in the send's MULTI/EXEC): acted, when r's reader was handed m. The
\* session may name any message it has seen sent to r (in the log, by peek);
\* the replyundelivered witness stamps acted whatever the receipt.
Reply(r, m) ==
  /\ m \in Sent
  /\ r \in to[m]
  /\ receipt[m][r] # "acted"
  /\ (receipt[m][r] # "none" \/ Broken = "replyundelivered")
  /\ receipt' = [receipt EXCEPT ![m][r] = "acted"]
  /\ UNCHANGED <<st, holder, log, to, alive, crashes, inTurn, timesActed, memo, wpend>>

\* nova-bus2 ack by r of m: pending becomes acked; acked again changes
\* nothing (XACK of an entry not in the PEL is 0).
Ack(r, m) ==
  /\ \/ st[m][r] = "pending"
     \/ st[m][r] = "acked"
     \/ (st[m][r] = "new" /\ Broken = "ackundelivered")
  /\ IF st[m][r] = "acked" /\ Broken = "ackreopens"
       THEN st' = [st EXCEPT ![m][r] = "new"]
       ELSE st' = [st EXCEPT ![m][r] = "acked"]
  /\ holder' = [holder EXCEPT ![m][r] = NoOne]
  /\ UNCHANGED <<log, to, alive, crashes, receipt, inTurn, timesActed, memo, wpend>>

\* A consumer dies holding what it holds: the store keeps it pending and
\* its receipts; the turn its session ran ends unacted, and what its
\* process held (a write not made, its memory) is gone.
Crash(c) ==
  /\ c \in alive /\ crashes < MaxCrashes
  /\ alive' = alive \ {c}
  /\ crashes' = crashes + 1
  /\ inTurn' = [m \in Messages |-> [r \in Recipients |-> inTurn[m][r] /\ holder[m][r] # c]]
  /\ memo' = [m \in Messages |-> [r \in Recipients |-> IF memo[m][r] = c THEN NoOne ELSE memo[m][r]]]
  /\ wpend' = [m \in Messages |-> [r \in Recipients |-> IF holder[m][r] = c THEN NoOne ELSE wpend[m][r]]]
  /\ UNCHANGED <<st, holder, log, to, receipt, timesActed>>

Restart(c) ==
  /\ c \notin alive
  /\ alive' = alive \cup {c}
  /\ UNCHANGED <<st, holder, log, to, crashes, receipt, inTurn, timesActed, memo, wpend>>

Next ==
  \/ \E m \in Messages : Send(m)
  \/ \E r \in Recipients, c \in Consumers : Recv(r, c)
  \/ \E r \in Recipients, m \in Messages :
       Take(r, m) \/ EndTurn(r, m) \/ Reply(r, m) \/ Ack(r, m) \/ Write(r, m) \/ Look(r, m)
  \/ \E c \in Consumers : Crash(c) \/ Restart(c)

\* Fairness, per action: every recipient keeps reading new messages and
\* acking what it was handed, and a dead consumer comes back; a crash is
\* never owed, and crashes are bounded.
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
\* recipient that a dead consumer held: after a crash, what the dead
\* consumer held comes first.
PendingBeforeNew ==
  [][\A r \in Recipients :
       (\E m \in Messages : st[m][r] = "new" /\ st'[m][r] = "pending") =>
         (\A m \in Messages : st[m][r] = "pending" => holder[m][r] \in alive)]_vars

\* A message held by a live consumer stays with it: it is delivered once
\* while held, and only an ack or the holder's death moves it.
HeldStaysHeld ==
  [][\A m \in Messages, r \in Recipients :
       (st[m][r] = "pending" /\ holder[m][r] \in alive /\ st'[m][r] = "pending") =>
         holder'[m][r] = holder[m][r]]_vars

\* Only a delivered message is acked: nothing goes from new to acked.
AckOnlyDelivered ==
  [][\A m \in Messages, r \in Recipients : st[m][r] = "new" => st'[m][r] # "acked"]_vars

\* Ack is idempotent: once acked, acked (the second ack changes nothing).
AckedStaysAcked ==
  [][\A m \in Messages, r \in Recipients : st[m][r] = "acked" => st'[m][r] = "acked"]_vars

\* Liveness: every sent message is acked by every recipient it names, once
\* the crashes stop (they are bounded) and some consumer keeps reading.
EveryMessageIsAcked ==
  \A m \in Messages, r \in Recipients :
    (m \in Sent /\ r \in to[m]) ~> (st[m][r] = "acked")


\* A receipt moves only forward: none, delivered, read, acted.
ReceiptNeverMovesBack ==
  [][\A m \in Messages, r \in Recipients :
       ReceiptRank(receipt'[m][r]) >= ReceiptRank(receipt[m][r])]_vars

\* Acted implies delivered: an acted receipt, by a turn's end or by a
\* reply, is of a message r's reader took off its stream.
ActedImpliesDelivered ==
  \A m \in Messages, r \in Recipients :
    receipt[m][r] = "acted" => st[m][r] \in {"pending", "acked"}

\* Read means the session took it: a receipt becomes read only in the step
\* the adapter accepts a turn carrying the message.
ReadOnlyWhenAccepted ==
  [][\A m \in Messages, r \in Recipients :
       (receipt[m][r] # "read" /\ receipt'[m][r] = "read") => inTurn'[m][r]]_vars

\* The idempotent take: no turn acts on a message id twice, however often
\* the claim hands it in.
NoMessageActedTwice ==
  \A m \in Messages, r \in Recipients :
    timesActed[m][r] <= 1

=============================================================================
