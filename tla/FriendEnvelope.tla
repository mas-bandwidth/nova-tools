----------------------------- MODULE FriendEnvelope ------------------------------
(* The daemon's delivery of pending messages as one envelope
   (pkg/friend/daemon.go, startBatch and Envelope; docs/SPEC-FRIEND.md,
   the loop). Messages arrive on the friend's stream in order (a smaller
   number is older). When the session is free and something is pending, one
   turn starts carrying the envelope: every pending message, oldest first,
   up to Cap (the adapter's text limit, counted in messages here); the rest
   stay pending and are the next turn. Exit 0 is one step and its ack the
   next: a failing ack leaves the messages pending, a succeeding one acks
   exactly what the turn carried. A failure acks none of it, and the messages
   are held until their claim opens (bus.Recv, ClaimAfter) and then taken
   again; a message that fails MaxDeliveries times is given up, acked with
   the failure on the record, never taken again.
   The base this models as it is: before the envelope, Batch already took
   every message in hand, oldest first, up to MaxBatch messages and
   BatchBytes, the rest the next turn, acked together at exit 0; at this
   grain that is Take = "oldest" with Cap = MaxBatch. The change keeps that
   set rule, keeps the MaxBatch read cap while a present is owed (the read
   stops at MaxBatch, daemon.go read; below this grain as the present flag),
   and moves the turn cap to the deliverer's text limit (TextLimit), the
   first message always taken (Cap >= 1), the rest named in the envelope; its
   line format (id, from, time, age, subject) is below this grain.
   Take is the rule under test: "oldest" is the daemon, before and after;
   "newest" (a turn that takes the youngest first) and "one" (a message per
   turn, the form the 2026-10-04 measurement saw, never the base's rule) are
   reversed witnesses. Broken = "giveupearly" is a witness that gives up a
   failed message before MaxDeliveries. The answered ping is acked before it
   is in hand, and the supersede rule acts on the daemon's own notices, never
   on a message on the stream: neither is in this model. *)
EXTENDS FiniteSets, Naturals

CONSTANTS N, Cap, MaxDeliveries, MaxBatch, MaxAckLoss, Take, Broken

ASSUME Cap >= 1 \* the first message always goes in
ASSUME MaxDeliveries >= 1
ASSUME MaxBatch >= 1

Msgs == 1..N

Min(a, b) == IF a < b THEN a ELSE b

\* the give-up bound: a message that fails this many times is given up; the
\* "giveupearly" witness lowers it to the first failure
GiveUpBound == IF Broken = "giveupearly" THEN 1 ELSE MaxDeliveries

VARIABLES arrived,  \* the messages on the stream so far: 1..arrived
          acked,    \* the messages acked (accepted, or given up at the bound)
          inTurn,   \* what the running turn carries ({}: the session is free)
          owed,     \* what was pending when the running turn started
          turns,    \* turns started
          ackTurn,  \* the turn whose ack acked each message (0: none yet)
          held,     \* messages of a failed turn whose claim has not opened yet
          deliveries, \* how many failed turns carried each message
          exited,   \* messages of an exited-0 turn whose ack is pending
          giveUp,   \* messages that failed at the bound, awaiting their ack
          present,  \* a present is owed (the read caps at MaxBatch)
          ackLosses \* how many exit-0 acks have failed (bounded)

vars == <<arrived, acked, inTurn, owed, turns, ackTurn, held, deliveries, exited, giveUp, present, ackLosses>>

\* the turn cap: the deliverer's text limit, capped at MaxBatch while a
\* present is owed (the read stops at MaxBatch then, daemon.go read)
TurnCap == IF present THEN Min(Cap, MaxBatch) ELSE Cap

Failed == {m \in Msgs : deliveries[m] > 0}

Pending == (1..arrived) \ (acked \cup held \cup exited \cup giveUp)

\* the envelope: the TurnCap oldest pending messages (Take = "oldest")
Envelope(p) ==
    CASE Take = "oldest" -> {m \in p : Cardinality({k \in p : k < m}) < TurnCap}
      [] Take = "newest" -> {m \in p : Cardinality({k \in p : k > m}) < TurnCap}
      [] Take = "one"    -> {m \in p : Cardinality({k \in p : k < m}) < 1}

Init == /\ arrived = 0
        /\ acked = {}
        /\ inTurn = {}
        /\ owed = {}
        /\ turns = 0
        /\ ackTurn = [m \in Msgs |-> 0]
        /\ held = {}
        /\ deliveries = [m \in Msgs |-> 0]
        /\ exited = {}
        /\ giveUp = {}
        /\ present = FALSE
        /\ ackLosses = 0

\* a message lands on the stream, at any time, a turn running or not
Arrive == /\ arrived < N
          /\ arrived' = arrived + 1
          /\ UNCHANGED <<acked, inTurn, owed, turns, ackTurn, held, deliveries, exited, giveUp, present, ackLosses>>

\* the session is free: one turn takes the envelope
Start == /\ inTurn = {}
         /\ exited = {}
         /\ giveUp = {}
         /\ Pending # {}
         /\ inTurn' = Envelope(Pending)
         /\ owed' = Pending
         /\ turns' = turns + 1
         /\ UNCHANGED <<arrived, acked, ackTurn, held, deliveries, exited, giveUp, present, ackLosses>>

\* the turn exits 0: it is over, its ack is the next step
Exit == /\ inTurn # {}
        /\ exited' = inTurn
        /\ inTurn' = {}
        /\ owed' = {}
        /\ UNCHANGED <<arrived, acked, turns, ackTurn, held, deliveries, giveUp, present, ackLosses>>

\* the ack lands: everything the turn carried is acked, together
Ack == /\ exited # {}
       /\ acked' = acked \cup exited
       /\ ackTurn' = [m \in Msgs |-> IF m \in exited THEN turns ELSE ackTurn[m]]
       /\ exited' = {}
       /\ UNCHANGED <<arrived, inTurn, owed, turns, held, deliveries, giveUp, present, ackLosses>>

\* the ack fails: the messages stay pending, and are taken again (bounded)
AckFail == /\ exited # {}
           /\ ackLosses < MaxAckLoss
           /\ ackLosses' = ackLosses + 1
           /\ exited' = {}
           /\ UNCHANGED <<arrived, acked, inTurn, owed, turns, ackTurn, held, deliveries, giveUp, present>>

\* the turn fails: nothing is acked; each message's deliveries rise, and a
\* message at the bound is given up, the rest held by its claim
Fail == /\ inTurn # {}
        /\ LET below == {m \in inTurn : deliveries[m] + 1 < GiveUpBound}
               atbound == {m \in inTurn : deliveries[m] + 1 >= GiveUpBound}
           IN /\ deliveries' = [m \in Msgs |-> IF m \in inTurn THEN deliveries[m] + 1 ELSE deliveries[m]]
              /\ held' = held \cup below
              /\ giveUp' = giveUp \cup atbound
              /\ inTurn' = {}
              /\ owed' = {}
              /\ UNCHANGED <<arrived, acked, turns, ackTurn, exited, present, ackLosses>>

\* the given-up messages are acked, with the failure on the record
GiveUp == /\ giveUp # {}
          /\ acked' = acked \cup giveUp
          /\ ackTurn' = [m \in Msgs |-> IF m \in giveUp THEN turns ELSE ackTurn[m]]
          /\ giveUp' = {}
          /\ UNCHANGED <<arrived, inTurn, owed, turns, held, deliveries, exited, present, ackLosses>>

\* the claims of the held messages open: they are pending again
Reopen == /\ held # {}
          /\ held' = {}
          /\ UNCHANGED <<arrived, acked, inTurn, owed, turns, ackTurn, deliveries, exited, giveUp, present, ackLosses>>

\* a present becomes owed (a session start, a new session id, her own ask)
PresentOwed == /\ inTurn = {}
              /\ exited = {}
              /\ giveUp = {}
              /\ ~present
              /\ present' = TRUE
              /\ UNCHANGED <<arrived, acked, inTurn, owed, turns, ackTurn, held, deliveries, exited, giveUp, ackLosses>>

\* the present is delivered: the read cap lifts
PresentDone == /\ inTurn = {}
              /\ exited = {}
              /\ giveUp = {}
              /\ present
              /\ present' = FALSE
              /\ UNCHANGED <<arrived, acked, inTurn, owed, turns, ackTurn, held, deliveries, exited, giveUp, ackLosses>>

\* everything has arrived and is acked: the end, not a deadlock
Done == /\ arrived = N
        /\ acked = Msgs
        /\ UNCHANGED vars

Next == Arrive \/ Start \/ Exit \/ Ack \/ AckFail \/ Fail \/ GiveUp \/ Reopen
        \/ PresentOwed \/ PresentDone \/ Done

Spec == Init /\ [][Next]_vars /\ WF_vars(Arrive) /\ WF_vars(Start) /\ WF_vars(Exit)
        /\ WF_vars(Ack) /\ WF_vars(GiveUp) /\ WF_vars(Reopen)

TypeOK == /\ arrived \in 0..N
          /\ acked \subseteq Msgs
          /\ inTurn \subseteq Msgs
          /\ owed \subseteq Msgs
          /\ ackTurn \in [Msgs -> Nat]
          /\ held \subseteq Msgs
          /\ deliveries \in [Msgs -> 0..MaxDeliveries]
          /\ exited \subseteq Msgs
          /\ giveUp \subseteq Msgs
          /\ present \in BOOLEAN
          /\ ackLosses \in 0..MaxAckLoss

\* no message is delivered in a later turn than a younger one, among the
\* messages no failed turn carried (a failed turn's messages wait out their
\* claim, and a younger one may go first meanwhile: NoYoungerFirstAcrossFailure
\* is the gap, MCFriendEnvelopeFailureReorders its witness)
NoYoungerFirst == \A a, b \in acked \ Failed : a < b => ackTurn[a] <= ackTurn[b]

NoYoungerFirstAcrossFailure == \A a, b \in acked : a < b => ackTurn[a] <= ackTurn[b]

\* a turn takes the whole pending set, up to the cap: no message waits a
\* turn per older message
EnvelopeTakesAll == inTurn # {} => Cardinality(inTurn) = Min(TurnCap, Cardinality(owed))

\* only an accepted or given-up turn acks, and a turn never carries an
\* acked message
AckOnlyAccepted == /\ inTurn \cap acked = {}
                   /\ \A m \in Msgs : (m \in acked) <=> (ackTurn[m] > 0)

\* a message is given up only at the bound, never before MaxDeliveries
GivenUpOnlyAtTheBound == \A m \in giveUp : deliveries[m] >= MaxDeliveries

\* every message is delivered and acked in the end
Delivered == \A m \in Msgs : <>(m \in acked)
=============================================================================
