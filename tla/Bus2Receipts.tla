---------------------------- MODULE Bus2Receipts ----------------------------
\* The receipts of nova-bus over its delivery machine (docs/SPEC-BUS.md,
\* message-receipts; internal/bus/stages.go, internal/friend/daemon.go). It
\* EXTENDS Bus2 and adds the receipts beside the streams, so Bus2's own
\* cases read the machine as it was.
\*
\* The state it adds, per message m and recipient r: rcpt[m][r], the
\* receipt the store holds ("none", "delivered", "read", "acted"), moved
\* only by the rule Fwd (Store.Forward: only forward, only delivered starts
\* one). Each consumer c is a friend daemon: hand[c] the pairs <<m, r>> it
\* has read and not yet taken, turn[c] the ones in the session's turn,
\* ackq[c] the ones a turn acted on that it has not acked, done[c] its
\* memory of the ones it pushed into a turn that ended acted (lost when it
\* crashes), and acts[m][r] how many turns carrying m ended acted (a ghost).
\*
\* The actions: RRecv (recv stamps delivered and hands the message to the
\* daemon), Take (the daemon drops a message it knows was acted, by its
\* store receipt, acking it, else pushes it into a turn), Print
\* (the session took the turn: read), Reply (the session sent a message
\* naming it: acted), TurnEnd (exit 0: acted, remembered, owed an ack),
\* TurnFail (the turn ran and failed: read, and the claim hands it in
\* again), DAck, LoseAck (the XACK never landed: the claim hands it in
\* again), RCrash (a daemon dies holding what it holds, and forgets).
\*
\* Broken = "none" is the design; four reversed witnesses, each caught by one
\* property below:
\*   "pushdup"         the take pushes every message it is handed, acted or
\*                     not: NoIdActedTwice
\*   "backstamp"       recv overwrites the receipt: ReceiptNeverMovesBack
\*   "earlyread"       take stamps read before acceptance: ReadOnlyAfterAcceptance
\*   "replyundelivered" a reply starts acted without delivery: ActedImpliesDelivered

EXTENDS Bus2

VARIABLES rcpt, hand, turn, ackq, done, acts, accepted
rvars == <<rcpt, hand, turn, ackq, done, acts, accepted>>
allvars == <<vars, rvars>>

RInit ==
  /\ Init
  /\ rcpt = [m \in Messages |-> [r \in Recipients |-> "none"]]
  /\ hand = [c \in Consumers |-> {}]
  /\ turn = [c \in Consumers |-> {}]
  /\ ackq = [c \in Consumers |-> {}]
  /\ done = [c \in Consumers |-> {}]
  /\ acts = [m \in Messages |-> [r \in Recipients |-> 0]]
  /\ accepted = {}

ReceiptStates == {"none", "delivered", "read", "acted"}
Pairs == Messages \X Recipients

Rank(s) == CASE s = "none" -> 0 [] s = "delivered" -> 1 [] s = "read" -> 2 [] s = "acted" -> 3

\* Store.Forward (internal/bus/stages.go Forward; redis.go forwardLua): the
\* receipt holding cur asked to move to s.
Fwd(cur, s) == IF Rank(s) > Rank(cur) /\ (Rank(cur) > 0 \/ s = "delivered") THEN s ELSE cur

RTypeOK ==
  /\ rcpt \in [Messages -> [Recipients -> ReceiptStates]]
  /\ hand \in [Consumers -> SUBSET Pairs]
  /\ turn \in [Consumers -> SUBSET Pairs]
  /\ ackq \in [Consumers -> SUBSET Pairs]
  /\ done \in [Consumers -> SUBSET Pairs]
  /\ acts \in [Messages -> [Recipients -> Nat]]
  /\ accepted \subseteq Pairs

\* recv's delivered stamp (Bus.RecvKinds, delivered); the backstamp witness
\* writes it over whatever the receipt held.
Delivered(m, r) == IF Broken = "backstamp" THEN "delivered" ELSE Fwd(rcpt[m][r], "delivered")

Took(m, r, c) ==
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Delivered(m, r)]
  /\ hand' = [hand EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ UNCHANGED <<turn, ackq, done, acts, accepted>>

\* nova-bus recv by the daemon c: the message it is handed, stamped delivered.
RRecv(r, c) ==
  \/ RecvPending(r, c) /\ Took(OldestClaimable(r, c), r, c)
  \/ RecvNew(r, c) /\ Took(Oldest(r, "new"), r, c)

\* The daemon's take (internal/friend/daemon.go, read): a message whose receipt recv found
\* acted (Entry.Stage), is dropped and acked, never pushed in; any other goes
\* into the session's turn. The pushdup witness pushes every one.
Take(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in hand[c]
  /\ hand' = [hand EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ IF Broken # "pushdup" /\ rcpt[m][r] = "acted"
       THEN Ack(r, m) /\ UNCHANGED <<rcpt, turn, ackq, done, acts, accepted>>
       ELSE /\ turn' = [turn EXCEPT ![c] = @ \cup {<<m, r>>}]
            /\ UNCHANGED vars
            /\ rcpt' = IF Broken = "earlyread"
                         THEN [rcpt EXCEPT ![m][r] = Fwd(@, "read")] ELSE rcpt
            /\ UNCHANGED <<ackq, done, acts, accepted>>

\* The adapter accepted the turn (daemon.go, watch).
Accept(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "read")]
  /\ accepted' = accepted \cup {<<m, r>>}
  /\ UNCHANGED vars
  /\ UNCHANGED <<hand, turn, ackq, done, acts>>

\* The session sent a message naming it (re): acted, in the send's own step.
ReceiptReply(m, r) ==
  /\ Reply(r, m)
  /\ rcpt' = [rcpt EXCEPT ![m][r] =
       IF Broken = "replyundelivered" THEN "acted" ELSE Fwd(@, "acted")]
  /\ UNCHANGED <<hand, turn, ackq, done, acts, accepted>>

\* The turn ended at exit 0 (daemon.go, settle): acted, remembered, owed an ack.
TurnEnd(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ turn' = [turn EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ rcpt' = [rcpt EXCEPT ![m][r] = Fwd(@, "acted")]
  /\ acts' = [acts EXCEPT ![m][r] = @ + 1]
  /\ done' = [done EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ ackq' = [ackq EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ UNCHANGED vars
  /\ accepted' = accepted \cup {<<m, r>>}
  /\ UNCHANGED hand

\* The turn ran and failed: read, and the claim hands it in again.
TurnFail(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in turn[c]
  /\ turn' = [turn EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ hand' = [hand EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ UNCHANGED vars
  /\ UNCHANGED <<rcpt, ackq, done, acts, accepted>>

DAck(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in ackq[c]
  /\ Ack(r, m)
  /\ ackq' = [ackq EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ UNCHANGED <<rcpt, hand, turn, done, acts, accepted>>

\* The XACK never landed: the message stays pending, and the claim hands it
\* in again to the same daemon. An outside event, bounded with the crashes.
LoseAck(c, m, r) ==
  /\ c \in alive /\ <<m, r>> \in ackq[c] /\ crashes < MaxCrashes
  /\ ackq' = [ackq EXCEPT ![c] = @ \ {<<m, r>>}]
  /\ hand' = [hand EXCEPT ![c] = @ \cup {<<m, r>>}]
  /\ crashes' = crashes + 1
  /\ UNCHANGED <<st, holder, log, to, alive>>
  /\ UNCHANGED <<rcpt, turn, done, acts, accepted>>

\* A daemon dies holding what it holds: its memory goes with it, the store's
\* receipts stay.
RCrash(c) ==
  /\ Crash(c)
  /\ hand' = [hand EXCEPT ![c] = {}]
  /\ turn' = [turn EXCEPT ![c] = {}]
  /\ ackq' = [ackq EXCEPT ![c] = {}]
  /\ done' = [done EXCEPT ![c] = {}]
  /\ UNCHANGED <<rcpt, acts, accepted>>

RNext ==
  \/ \E m \in Messages : Send(m) /\ UNCHANGED rvars
  \/ \E r \in Recipients, c \in Consumers : RRecv(r, c)
  \/ \E c \in Consumers, m \in Messages, r \in Recipients :
       \/ Take(c, m, r) \/ Accept(c, m, r)
       \/ TurnEnd(c, m, r) \/ TurnFail(c, m, r) \/ DAck(c, m, r) \/ LoseAck(c, m, r)
  \/ \E c \in Consumers : RCrash(c)
  \/ \E m \in Messages, r \in Recipients : ReceiptReply(m, r)

\* The receipts over the delivery machine; safety only, so no fairness, and
\* a dead daemon stays dead (its successor is another consumer).
ReceiptSpec == RInit /\ [][RNext]_allvars

\* A receipt never moves back.
ReadOnlyAfterAcceptance ==
  \A m \in Messages, r \in Recipients : rcpt[m][r] = "read" => <<m, r>> \in accepted

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
