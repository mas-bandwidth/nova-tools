---------------------------- MODULE Bus2Receipts ----------------------------
\* The receipts of nova-bus over its delivery machine (docs/SPEC-BUS.md,
\* message-receipts; pkg/bus/stages.go, redis.go; pkg/friend/daemon.go,
\* present.go). It EXTENDS Bus2 and adds the receipts beside the streams, so
\* Bus2's own cases read the machine as it was.
\*
\* The state it adds, per message m and recipient r: rcpt[m][r], the receipt
\* the store holds ("none", "delivered", "read", "acted"), moved only by the
\* rule Fwd (Store.Forward: only forward, only delivered starts one). Each
\* consumer c is a friend daemon: hand[c] the pairs <<m, r>> it has read and
\* not yet taken, turn[c] the ones in the session's turn, ackq[c] the ones a
\* turn acted on that it has not acked, done[c] its memory of the ones it
\* pushed into a turn that ended acted (lost when it crashes), and acts[m][r]
\* how many turns carrying m ended acted (a ghost).
\*
\* The actions: RRecv (recv stamps delivered, reads the receipt it found, and
\* drops-and-acks a pair the daemon knows was acted, else hands it to the
\* daemon), Take (the daemon pushes a handed pair into a turn), Print (the
\* session took the turn: read), Reply (the session sent a message naming it:
\* acted), TurnEnd (exit 0: acted, remembered, owed an ack), LoseStamp (exit 0
\* but the acted stamp failed: remembered, the receipt stays read), TurnFail
\* (the turn ran and failed: read, and handed in again, or given up and acked
\* at read), DAck, LoseAck (the XACK never landed: the message stays pending,
\* redelivered through RRecv after the daemon died), Present (the present
\* stamps the backlog delivered and acks it superseded), RCrash (a daemon dies
\* holding what it holds, and forgets).
\*
\* Broken = "none" is the design. The reversed witnesses are caught by one
\* property each; the third value is the code's own refusal, not a witness:
\*   "pushdup"         recv hands every message to the daemon, acted or not:
\*                     NoIdActedTwice
\*   "backstamp"       recv writes delivered over whatever the receipt held
\*                     (HSET, not the rule): ReceiptNeverMovesBack
\*   "losestamp"       the acted stamp failed and is only remembered
\*                     (daemon.go, oweActed), written again before the next
\*                     read once the store answers; with a crash and a lost
\*                     ack together the message is acted twice, the known gap
\*                     MCBus2ReceiptsKnownGap checks.

EXTENDS Bus2

VARIABLES rcpt, hand, turn, ackq, done, acts
rvars == <<rcpt, hand, turn, ackq, done, acts>>
allvars == <<vars, rvars>>

ReceiptStates == {"none", "delivered", "read", "acted"}
Pairs == Messages \X Recipients

Rank(s) == CASE s = "none" -> 0 [] s = "delivered" -> 1 [] s = "read" -> 2 [] s = "acted" -> 3

\* Store.Forward (pkg/bus/stages.go Forward; redis.go forwardLua): the
\* receipt holding cur asked to move to s.
Fwd(cur, s) == IF Rank(s) > Rank(cur) /\ (Rank(cur) > 0 \/ s = "delivered") THEN s ELSE cur

\* recv's delivered stamp (Bus.RecvKinds, delivered); the backstamp witness
\* writes it over whatever the receipt held.
Delivered(m, r) == IF Broken = "backstamp" THEN "delivered" ELSE Fwd(rcpt[m][r], "delivered")

\* The receipt recv found (Entry.Stage), before the delivered stamp.
Found(m, r) == rcpt[m][r]

RInit ==
  /\ Init
  /\ rcpt = [m \in Messages |-> [r \in Recipients |-> "none"]]
  /\ hand = [c \in Consumers |-> {}]
  /\ turn = [c \in Consumers |-> {}]
  /\ ackq = [c \in Consumers |-> {}]
  /\ done = [c \in Consumers |-> {}]
  /\ acts = [m \in Messages |-> [r \in Recipients |-> 0]]

RTypeOK ==
  /\ rcpt \in [Messages -> [Recipients -> ReceiptStates]]
  /\ hand \in [Consumers -> SUBSET Pairs]
  /\ turn \in [Consumers -> SUBSET Pairs]
  /\ ackq \in [Consumers -> SUBSET Pairs]
  /\ done \in [Consumers -> SUBSET Pairs]
  /\ acts \in [Messages -> [Recipients -> Nat]]

\* recv for r by c: the message it is handed, stamped delivered. A pair the
\* daemon knows was acted (its memory, or the receipt recv found) is dropped
\* and acked, never handed in; the pushdup witness hands every one in. A recv
\* that hands out a new message found no pending one a dead consumer held.
Received(m, r, c) ==
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Delivered(m, r)]
  /\ IF Broken # "pushdup" /\ (<<m, r>> \in done[c] \/ Found(m, r) = "acted")
       THEN /\ st' = [st EXCEPT ![m][r] = "acked"]
            /\ holder' = [holder EXCEPT ![m][r] = NoOne]
            /\ UNCHANGED <<hand, turn, ackq, done, acts>>
       ELSE /\ st' = [st EXCEPT ![m][r] = "pending"]
            /\ holder' = [holder EXCEPT ![m][r] = c]
            /\ hand' = [hand EXCEPT ![c] = @ \cup {<<m, r>>}]
            /\ UNCHANGED <<turn, ackq, done, acts>>
  /\ UNCHANGED <<log, to, alive, crashes>>

RRecv(r, c) ==
  \/ /\ c \in alive /\ Claimable(r, c) # {}
     /\ LET m == OldestClaimable(r, c) IN Received(m, r, c)
  \/ /\ c \in alive /\ (Claimable(r, c) = {} \/ Broken = "newfirst") /\ Has(r, "new")
     /\ LET m == Oldest(r, "new") IN Received(m, r, c)

\* The daemon's take (pkg/friend/daemon.go, take): the recv already dropped
\* the acted ones, so a handed pair goes straight into the session's turn.
Take(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in hand[c]
  /\ hand' = [hand EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ turn' = [turn EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ UNCHANGED vars
  /\ UNCHANGED <<rcpt, ackq, done, acts>>

\* The session took the turn: it printed (daemon.go, watch).
Print(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "read")]
  /\ UNCHANGED vars
  /\ UNCHANGED <<hand, turn, ackq, done, acts>>

\* The session sent a message naming it (re): acted, in the send's own step.
Reply(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "acted")]
  /\ UNCHANGED vars
  /\ UNCHANGED <<hand, turn, ackq, done, acts>>

\* The turn ended at exit 0 (daemon.go, settle): acted, remembered, owed an
\* ack.
TurnEnd(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ turn' = [turn EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "acted")]
  /\ acts' = [acts EXCEPT ![m][r] = @ + 1]
  /\ done' = [done EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ ackq' = [ackq EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ UNCHANGED vars
  /\ UNCHANGED hand

\* Exit 0 but the acted stamp failed (daemon.go, oweActed): remembered and
\* owed an ack, the receipt stays read (a read stamp is not owed; only acted
\* keeps a message from a second turn). With a crash and a lost ack the
\* message is redelivered and acted twice: the known gap.
LoseStamp(c, m, r) ==
  /\ Broken = "losestamp"
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ turn' = [turn EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ acts' = [acts EXCEPT ![m][r] = @ + 1]
  /\ done' = [done EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ ackq' = [ackq EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ UNCHANGED rcpt
  /\ UNCHANGED vars
  /\ UNCHANGED hand

\* The turn ran and failed: read; the claim hands it in again, or the message
\* is given up and acked at read (daemon.go, settle's failure arm).
TurnFail(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ turn' = [turn EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "read")]
  /\ \/ /\ hand' = [hand EXCEPT ![c] = @ \cup {<<m, r>>}]
        /\ UNCHANGED vars
        /\ UNCHANGED <<ackq, done, acts>>
     \/ /\ st' = [st EXCEPT ![m][r] = "acked"]
        /\ holder' = [holder EXCEPT ![m][r] = NoOne]
        /\ UNCHANGED <<hand, ackq, done, acts>>
  /\ UNCHANGED <<log, to, alive, crashes>>

DAck(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in ackq[c]
  /\ Ack(r, m)
  /\ ackq' = [ackq EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ UNCHANGED <<rcpt, hand, turn, done, acts>>

\* The XACK never landed: the daemon stops owing the ack, and the message
\* stays pending. The claim hands it in again only after the daemon died
\* (idle past ClaimAfter), so a live daemon keeps its message; the successor
\* redelivers it through RRecv, where the receipt found is read.
LoseAck(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in ackq[c]
  /\ ackq' = [ackq EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ UNCHANGED <<st, holder, log, to, alive, crashes>>
  /\ UNCHANGED <<rcpt, hand, turn, done, acts>>

\* The present (pkg/friend/present.go, startPresent): on a session start or
\* after a gap the daemon c stamps its whole backlog delivered and acks the
\* superseded messages. One action per backlog message; TLC checks every
\* order. A fresh message is claimed only after a dead holder's own came first.
Present(c, m, r) ==
  \/ \* the backlog stamp: a fresh message the present read is claimed and
     \* stamped delivered
     /\ c \in alive /\ st[m][r] = "new" /\ Claimable(r, c) = {}
     /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "delivered")]
     /\ st' = [st EXCEPT ![m][r] = "pending"]
     /\ holder' = [holder EXCEPT ![m][r] = c]
     /\ UNCHANGED <<log, to, alive, crashes>>
     /\ UNCHANGED <<hand, turn, ackq, done, acts>>
  \/ \* the superseded ack: a backlog message the present replaced is acked
     /\ c \in alive /\ st[m][r] = "pending" /\ holder[m][r] \in {c} \cup (Consumers \ alive)
     /\ Ack(r, m)
     /\ UNCHANGED <<rcpt, hand, turn, ackq, done, acts>>

\* A daemon dies holding what it holds: its memory goes with it, the store's
\* receipts stay.
RCrash(c) ==
  /\ Crash(c)
  /\ hand' = [hand EXCEPT ![c] = {}]
  /\ turn' = [turn EXCEPT ![c] = {}]
  /\ ackq' = [ackq EXCEPT ![c] = {}]
  /\ done' = [done EXCEPT ![c] = {}]
  /\ UNCHANGED <<rcpt, acts>>

RNext ==
  \/ \E m \in Messages : Send(m) /\ UNCHANGED rvars
  \/ \E r \in Recipients, c \in Consumers : RRecv(r, c)
  \/ \E c \in Consumers, m \in Messages, r \in Recipients :
       \/ Take(c, m, r) \/ Print(c, m, r) \/ Reply(c, m, r)
       \/ TurnEnd(c, m, r) \/ LoseStamp(c, m, r) \/ TurnFail(c, m, r)
       \/ DAck(c, m, r) \/ LoseAck(c, m, r) \/ Present(c, m, r)
  \/ \E c \in Consumers : RCrash(c)

\* The receipts over the delivery machine; safety only, so no fairness, and
\* a dead daemon stays dead (its successor is another consumer).
ReceiptSpec == RInit /\ [][RNext]_allvars

\* A receipt never moves back.
ReceiptNeverMovesBack ==
  [][\A m \in Messages, r \in Recipients : Rank(rcpt'[m][r]) >= Rank(rcpt[m][r])]_allvars

\* Acted implies delivered: a message with a receipt past none was taken off
\* its recipient's stream (pending, or acked since).
ActedImpliesDelivered ==
  \A m \in Messages, r \in Recipients :
    rcpt[m][r] # "none" => st[m][r] \in {"pending", "acked"}

\* No message id is acted twice: no two turns carrying it end acted.
NoIdActedTwice ==
  \A m \in Messages, r \in Recipients : acts[m][r] <= 1

=============================================================================
