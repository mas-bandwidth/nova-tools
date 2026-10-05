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
\* receipt.go MarkReceipts and Acted, internal/friend/daemon.go read and
\* settle): receipt[m][r], r's receipt of m in the hash beside its stream
\* ("none", "delivered", "read", "acted"), kept in the store, so a crash
\* forgets none of it; taken[m][r], whether the delivery r's reader now
\* holds has been through the daemon's take; turn[m][r], the consumer whose
\* session's turn carries m (NoOne: none); timesActed[m][r], how many turns
\* carrying m ended acted, the count the rule bounds.
\*
\* The actions are the verbs and the outside events: Send (one message to
\* every recipient's stream and the log at once), Recv (a consumer of r takes
\* the oldest pending message whose holder is dead, else the oldest new one;
\* a live holder keeps its message: the code tells a dead holder by the
\* entry's idle time past ClaimAfter; a recv marks the receipt delivered),
\* Take (the daemon's take of what its reader was handed: a delivery whose
\* receipt is acted is dropped and acked, else pushed into a turn, read),
\* EndTurn (the turn ended at exit 0: acted), Ack
\* (by r, of a message it was handed; again is a no-op), Crash (a consumer
\* dies holding what it holds, and its turn with it), Restart. The rules about one step (what a
\* recv found, what an ack moved) are action properties over st and st'.
\*
\* The instances. MCBus2.cfg, the design with every rule, is one recipient,
\* three messages and two consumers: with the receipts two recipients of
\* three messages is past ten minutes on a bench, over the 110 s budget
\* (measured on a bench, 2026-10-05; one recipient is 52 s). The recipients
\* share nothing but the send, whose all-or-none is the partial witness's
\* to catch, and that witness keeps two. The acttwice witness is one
\* recipient of one message: the redelivery needs no other.
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
\*   "acttwice"        a take that pushes a redelivered id (it never asks
\*                     the acted receipt): after a crash between a turn's
\*                     end and its ack, the claim hands the message in
\*                     again and a second turn acts on it: NoMessageActedTwice

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Recipients, Messages, Consumers, MaxCrashes, Broken

VARIABLES st, holder, log, to, alive, crashes, receipt, taken, turn, timesActed
vars == <<st, holder, log, to, alive, crashes, receipt, taken, turn, timesActed>>

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
  /\ taken \in [Messages -> [Recipients -> BOOLEAN]]
  /\ turn \in [Messages -> [Recipients -> Consumers \cup {NoOne}]]
  /\ timesActed \in [Messages -> [Recipients -> Nat]]

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

Init ==
  /\ st = [m \in Messages |-> [r \in Recipients |-> "none"]]
  /\ holder = [m \in Messages |-> [r \in Recipients |-> NoOne]]
  /\ log = <<>>
  /\ to \in [Messages -> (SUBSET Recipients) \ {{}}]
  /\ alive = Consumers
  /\ crashes = 0
  /\ receipt = [m \in Messages |-> [r \in Recipients |-> "none"]]
  /\ taken = [m \in Messages |-> [r \in Recipients |-> FALSE]]
  /\ turn = [m \in Messages |-> [r \in Recipients |-> NoOne]]
  /\ timesActed = [m \in Messages |-> [r \in Recipients |-> 0]]

\* nova-bus2 send: one entry on every recipient's stream and the log, in
\* one transaction. The partial witness writes some subset of the streams.
Send(m) ==
  /\ m \notin Sent
  /\ \E got \in SUBSET to[m] :
       /\ (Broken = "partial" \/ got = to[m])
       /\ st' = [st EXCEPT ![m] = [r \in Recipients |-> IF r \in got THEN "new" ELSE @[r]]]
  /\ log' = Append(log, m)
  /\ UNCHANGED <<holder, to, alive, crashes, receipt, taken, turn, timesActed>>

\* nova-bus2 recv for r by consumer c: the oldest claimable pending
\* message, else the oldest new one.
RecvPending(r, c) ==
  /\ c \in alive
  /\ Claimable(r, c) # {}
  /\ LET m == OldestClaimable(r, c) IN
       /\ holder' = [holder EXCEPT ![m][r] = c]
       /\ receipt' = [receipt EXCEPT ![m][r] = IF @ = "none" THEN "delivered" ELSE @]
       /\ taken' = [taken EXCEPT ![m][r] = FALSE]
  /\ UNCHANGED <<st, log, to, alive, crashes, turn, timesActed>>

RecvNew(r, c) ==
  /\ c \in alive
  /\ (Claimable(r, c) = {} \/ Broken = "newfirst")
  /\ Has(r, "new")
  /\ LET m == Oldest(r, "new") IN
       /\ st' = [st EXCEPT ![m][r] = "pending"]
       /\ holder' = [holder EXCEPT ![m][r] = c]
       /\ receipt' = [receipt EXCEPT ![m][r] = IF @ = "none" THEN "delivered" ELSE @]
       /\ taken' = [taken EXCEPT ![m][r] = FALSE]
  /\ UNCHANGED <<log, to, alive, crashes, turn, timesActed>>

Recv(r, c) == RecvPending(r, c) \/ RecvNew(r, c)

\* The daemon's take of a delivery its reader holds (daemon.go read): an
\* acted receipt (Acted, or the loop's own memory) is a duplicate, dropped
\* and acked; anything else is pushed into a turn, and the turn starting is
\* the read receipt. The acttwice witness never asks.
Take(r, m) ==
  /\ st[m][r] = "pending"
  /\ holder[m][r] \in alive
  /\ ~taken[m][r]
  /\ turn[m][r] = NoOne
  /\ taken' = [taken EXCEPT ![m][r] = TRUE]
  /\ IF receipt[m][r] = "acted" /\ Broken # "acttwice"
       THEN /\ st' = [st EXCEPT ![m][r] = "acked"]
            /\ holder' = [holder EXCEPT ![m][r] = NoOne]
            /\ UNCHANGED <<turn, receipt>>
       ELSE /\ turn' = [turn EXCEPT ![m][r] = holder[m][r]]
            /\ receipt' = [receipt EXCEPT ![m][r] = IF @ = "acted" THEN @ ELSE "read"]
            /\ UNCHANGED <<st, holder>>
  /\ UNCHANGED <<log, to, alive, crashes, timesActed>>

\* The turn carrying m ends at exit 0 (daemon.go settle): acted, before the
\* ack, which is Ack's step; a crash between the two leaves m pending.
EndTurn(r, m) ==
  /\ turn[m][r] \in alive
  /\ turn' = [turn EXCEPT ![m][r] = NoOne]
  /\ receipt' = [receipt EXCEPT ![m][r] = "acted"]
  /\ timesActed' = [timesActed EXCEPT ![m][r] = @ + 1]
  /\ UNCHANGED <<st, holder, log, to, alive, crashes, taken>>

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
  /\ UNCHANGED <<log, to, alive, crashes, receipt, taken, turn, timesActed>>

\* A consumer dies holding what it holds: the store keeps it pending and
\* its receipts; the turn its session ran ends unacted.
Crash(c) ==
  /\ c \in alive /\ crashes < MaxCrashes
  /\ alive' = alive \ {c}
  /\ crashes' = crashes + 1
  /\ turn' = [m \in Messages |-> [r \in Recipients |-> IF turn[m][r] = c THEN NoOne ELSE turn[m][r]]]
  /\ UNCHANGED <<st, holder, log, to, receipt, taken, timesActed>>

Restart(c) ==
  /\ c \notin alive
  /\ alive' = alive \cup {c}
  /\ UNCHANGED <<st, holder, log, to, crashes, receipt, taken, turn, timesActed>>

Next ==
  \/ \E m \in Messages : Send(m)
  \/ \E r \in Recipients, c \in Consumers : Recv(r, c)
  \/ \E r \in Recipients, m \in Messages : Take(r, m) \/ EndTurn(r, m) \/ Ack(r, m)
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
ReceiptRank(s) ==
  CASE s = "none" -> 0
    [] s = "delivered" -> 1
    [] s = "read" -> 2
    [] s = "acted" -> 3

ReceiptNeverMovesBack ==
  [][\A m \in Messages, r \in Recipients :
       ReceiptRank(receipt'[m][r]) >= ReceiptRank(receipt[m][r])]_vars

\* Acted implies delivered: a receipt past none is of a message r's reader
\* took off its stream.
ActedImpliesDelivered ==
  \A m \in Messages, r \in Recipients :
    receipt[m][r] = "acted" => st[m][r] \in {"pending", "acked"}

\* The idempotent take: no turn acts on a message id twice, however often
\* the claim hands it in.
NoMessageActedTwice ==
  \A m \in Messages, r \in Recipients :
    timesActed[m][r] <= 1

=============================================================================
