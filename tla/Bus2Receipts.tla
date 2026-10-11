---------------------------- MODULE Bus2Receipts ----------------------------
\* The receipts of nova-bus over its delivery machine (docs/SPEC-BUS.md,
\* message-receipts; pkg/bus/stages.go, pkg/bus/redis.go, pkg/friend/daemon.go). It
\* EXTENDS Bus2 and adds the receipts beside the streams, so Bus2's own
\* cases read the machine as it was.
\*
\* The state it adds, per message m and recipient r: rcpt[m][r], the
\* receipt the store holds ("none", "delivered", "read", "acted"), moved
\* only by the rule Fwd (Store.Forward: only forward, only delivered starts
\* one). Each consumer c is a friend daemon: hand[c] the pairs <<m, r>> it
\* has read and not yet taken, with seen[c][<<m, r>>] the receipt recv found
\* for it (Entry.Stage: read at recv, not at take), turn[c] the ones in the
\* session's turn, ackq[c] the ones a turn acted on that it has not acked,
\* done[c] its memory of the ones it pushed into a turn that ended acted
\* (lost when it crashes), failed[c][<<m, r>>] how many times the turn it
\* carried failed in a row (a bound gives it up, acked at "read"), and
\* acts[m][r] how many turns carrying m ended acted (a ghost).
\*
\* The actions: RRecv (recv stamps delivered and hands the message to the
\* daemon, reading the receipt it held: Entry.Stage), Take (the daemon drops
\* a message it knows was acted, by its memory or by the receipt recv found,
\* acking it, else pushes it into a turn), Print (the session took the turn:
\* read), Reply (the session sent a message naming it: acted), TurnEnd (exit
\* 0: acted, remembered, owed an ack), LoseStamp (exit 0 whose acted stamp
\* the store refused: only recorded, the receipt stays read, the turn still
\* acted, remembered and owed an ack), TurnFail (the turn ran and failed:
\* read, and the claim hands it in again until the bound gives it up, acked
\* at "read"), Present (the present stamps the backlog delivered), DAck,
\* LoseAck (the XACK never landed: the claim hands it in again), RCrash (a
\* daemon dies holding what it holds, and forgets).
\*
\* Broken = "none" is the design; two reversed witnesses, each caught by one
\* property below:
\*   "pushdup"         the take pushes every message it is handed, acted or
\*                     not: NoIdActedTwice
\*   "backstamp"       recv writes delivered over whatever the receipt held
\*                     (HSET, not the rule): ReceiptNeverMovesBack
\*
\* The known gap, checked by MCBus2ReceiptsKnownGap rather than hidden: a
\* failed acted stamp (LoseStamp) and a crash that loses the ack and the
\* memory together act one message twice, so NoIdActedTwice fails with a
\* crash and holds without one.

EXTENDS Bus2

VARIABLES rcpt, hand, turn, ackq, done, acts, seen, failed
rvars == <<rcpt, hand, turn, ackq, done, acts, seen, failed>>
allvars == <<vars, rvars>>

MaxDeliveries == 3

RInit ==
  /\ Init
  /\ rcpt = [m \in Messages |-> [r \in Recipients |-> "none"]]
  /\ hand = [c \in Consumers |-> {}]
  /\ turn = [c \in Consumers |-> {}]
  /\ ackq = [c \in Consumers |-> {}]
  /\ done = [c \in Consumers |-> {}]
  /\ acts = [m \in Messages |-> [r \in Recipients |-> 0]]
  /\ seen = [c \in Consumers |-> [p \in Pairs |-> "none"]]
  /\ failed = [c \in Consumers |-> [p \in Pairs |-> 0]]

ReceiptStates == {"none", "delivered", "read", "acted"}
Pairs == Messages \X Recipients

Rank(s) == CASE s = "none" -> 0 [] s = "delivered" -> 1 [] s = "read" -> 2 [] s = "acted" -> 3

\* Store.Forward (pkg/bus/stages.go Forward; redis.go forwardLua): the
\* receipt holding cur asked to move to s.
Fwd(cur, s) == IF Rank(s) > Rank(cur) /\ (Rank(cur) > 0 \/ s = "delivered") THEN s ELSE cur

RTypeOK ==
  /\ rcpt \in [Messages -> [Recipients -> ReceiptStates]]
  /\ hand \in [Consumers -> SUBSET Pairs]
  /\ turn \in [Consumers -> SUBSET Pairs]
  /\ ackq \in [Consumers -> SUBSET Pairs]
  /\ done \in [Consumers -> SUBSET Pairs]
  /\ acts \in [Messages -> [Recipients -> Nat]]
  /\ seen \in [Consumers -> [Pairs -> ReceiptStates]]
  /\ failed \in [Consumers -> [Pairs -> 0..MaxDeliveries]]

\* recv's delivered stamp (Bus.RecvKinds, delivered); the backstamp witness
\* writes it over whatever the receipt held.
Delivered(m, r) == IF Broken = "backstamp" THEN "delivered" ELSE Fwd(rcpt[m][r], "delivered")

\* The receipt recv found for the message it hands c: Entry.Stage, the
\* receipt before the delivered stamp.
RecvStage(m, r) == rcpt[m][r]

Took(m, r, c) ==
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Delivered(m, r)]
  /\ hand' = [hand EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ seen' = [seen EXCEPT ![c][<<m, r>>] = RecvStage(m, r)]
  /\ UNCHANGED <<turn, ackq, done, acts, failed>>

\* nova-bus recv by the daemon c: the message it is handed, stamped delivered.
RRecv(r, c) ==
  \/ RecvPending(r, c) /\ Took(OldestClaimable(r, c), r, c)
  \/ RecvNew(r, c) /\ Took(Oldest(r, "new"), r, c)

\* The daemon's take (pkg/friend/daemon.go, read): a message it pushed into a
\* turn that ended acted (its memory), or whose receipt recv found acted
\* (Entry.Stage), is dropped and acked, never pushed in; any other goes into
\* the session's turn. The pushdup witness pushes every one.
Take(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in hand[c]
  /\ hand' = [hand EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ IF Broken # "pushdup" /\ (<<m, r>> \in done[c] \/ seen[c][<<m, r>>] = "acted")
       THEN Ack(r, m) /\ UNCHANGED <<rcpt, turn, ackq, done, acts, seen, failed>>
       ELSE /\ turn' = [turn EXCEPT ![c] = @ \cup {<<m, r>>}]
            /\ UNCHANGED vars
            /\ UNCHANGED <<rcpt, ackq, done, acts, seen, failed>>

\* The session took the turn: it printed (daemon.go, watch).
Print(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "read")]
  /\ UNCHANGED vars
  /\ UNCHANGED <<hand, turn, ackq, done, acts, seen, failed>>

\* The session sent a message naming it (re): acted, in the send's own step.
Reply(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "acted")]
  /\ UNCHANGED vars
  /\ UNCHANGED <<hand, turn, ackq, done, acts, seen, failed>>

\* The turn ended at exit 0 (daemon.go, settle): acted, remembered, owed an ack.
TurnEnd(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ turn' = [turn EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "acted")]
  /\ acts' = [acts EXCEPT ![m][r] = @ + 1]
  /\ done' = [done EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ ackq' = [ackq EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ failed' = [failed EXCEPT ![c][<<m, r>>] = 0]
  /\ UNCHANGED vars
  /\ UNCHANGED <<hand, seen>>

\* Exit 0 whose acted stamp the store refused: the failed stamp is only
\* recorded (oweActed), the receipt stays read, and the turn still ended
\* acted: remembered, owed an ack.
LoseStamp(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ turn' = [turn EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "read")]
  /\ acts' = [acts EXCEPT ![m][r] = @ + 1]
  /\ done' = [done EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ ackq' = [ackq EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ failed' = [failed EXCEPT ![c][<<m, r>>] = 0]
  /\ UNCHANGED vars
  /\ UNCHANGED <<hand, seen>>

\* The turn ran and failed: read, and the claim hands it in again; at the
\* bound the message is given up, acked with its receipt at "read".
TurnFail(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ turn' = [turn EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "read")]
  /\ failed' = [failed EXCEPT ![c][<<m, r>>] = failed[c][<<m, r>>] + 1]
  /\ IF failed'[c][<<m, r>>] >= MaxDeliveries
       THEN Ack(r, m) /\ UNCHANGED <<hand, ackq, done, acts, seen>>
       ELSE /\ hand' = [hand EXCEPT ![c] = @ \cup {<<m, r>>}]
            /\ UNCHANGED vars
            /\ UNCHANGED <<ackq, done, acts, seen>>

\* The present stamps the backlog delivered (present.go, startPresent): a
\* message taken off the stream the raw snapshot read, given the delivered
\* receipt recv would have written.
Present(r, m) ==
  /\ st[m][r] = "pending"
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "delivered")]
  /\ UNCHANGED vars
  /\ UNCHANGED <<hand, turn, ackq, done, acts, seen, failed>>

DAck(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in ackq[c]
  /\ Ack(r, m)
  /\ ackq' = [ackq EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ UNCHANGED <<rcpt, hand, turn, done, acts, seen, failed>>

\* The XACK never landed: the message stays pending, and the claim hands it
\* in again to the same daemon. An outside event, bounded with the crashes.
LoseAck(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in ackq[c] /\ crashes < MaxCrashes
  /\ ackq' = [ackq EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ hand' = [hand EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ seen' = [seen EXCEPT ![c][<<m, r>>] = rcpt[m][r]]
  /\ crashes' = crashes + 1
  /\ UNCHANGED <<st, holder, log, to, alive>>
  /\ UNCHANGED <<rcpt, turn, done, acts, failed>>

\* A daemon dies holding what it holds: its memory goes with it, the store's
\* receipts stay.
RCrash(c) ==
  /\ Crash(c)
  /\ hand' = [hand EXCEPT ![c] = {}]
  /\ turn' = [turn EXCEPT ![c] = {}]
  /\ ackq' = [ackq EXCEPT ![c] = {}]
  /\ done' = [done EXCEPT ![c] = {}]
  /\ failed' = [failed EXCEPT ![c] = [p \in Pairs |-> 0]]
  /\ UNCHANGED <<rcpt, acts, seen>>

RNext ==
  \/ \E m \in Messages : Send(m) /\ UNCHANGED rvars
  \/ \E r \in Recipients, c \in Consumers : RRecv(r, c)
  \/ \E r \in Recipients, m \in Messages : Present(r, m)
  \/ \E c \in Consumers, m \in Messages, r \in Recipients :
       \/ Take(c, m, r) \/ Print(c, m, r) \/ Reply(c, m, r)
       \/ TurnEnd(c, m, r) \/ LoseStamp(c, m, r) \/ TurnFail(c, m, r)
       \/ DAck(c, m, r) \/ LoseAck(c, m, r)
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
