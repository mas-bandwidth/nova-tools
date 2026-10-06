------------------------------ MODULE Delivery ------------------------------
(* The daemon's delivery of pending messages as one envelope
   (internal/friend/daemon.go, startBatch and Envelope; docs/SPEC-FRIEND.md,
   the loop). Messages arrive on the friend's stream in order (a smaller
   number is older). When the session is free and something is pending, one
   turn starts carrying the envelope: every pending message, oldest first,
   up to Cap (the adapter's text limit, counted in messages here); the rest
   stay pending and are the next turn. Exit 0 acks exactly what the turn
   carried; a failure acks none of it, and the messages are held until their
   claim opens (bus.Recv, ClaimAfter) and then taken again.
   Take is the rule under test: "oldest" is the daemon; "newest" (a turn that
   takes the youngest first) and "one" (a message per turn, the daemon before
   this change) are reversed witnesses. The supersede rule and the answered
   ping drop messages before the envelope is cut and are below this grain. *)
EXTENDS FiniteSets, Naturals

CONSTANTS N, Cap, MaxFail, Take

Msgs == 1..N

VARIABLES arrived,  \* the messages on the stream so far: 1..arrived
          acked,    \* the messages acked
          inTurn,   \* what the running turn carries ({}: the session is free)
          owed,     \* what was pending when the running turn started
          turns,    \* turns started
          ackTurn,  \* the turn whose exit 0 acked each message (0: none yet)
          fails,    \* turns that failed
          held,     \* messages of a failed turn whose claim has not opened yet
          failed    \* every message that was ever in a failed turn

vars == <<arrived, acked, inTurn, owed, turns, ackTurn, fails, held, failed>>

Min(a, b) == IF a < b THEN a ELSE b

Pending == ((1..arrived) \ acked) \ held

\* the envelope: the Cap oldest pending messages (Take = "oldest")
Envelope(p) ==
    CASE Take = "oldest" -> {m \in p : Cardinality({k \in p : k < m}) < Cap}
      [] Take = "newest" -> {m \in p : Cardinality({k \in p : k > m}) < Cap}
      [] Take = "one"    -> {m \in p : Cardinality({k \in p : k < m}) < 1}

Init == /\ arrived = 0
        /\ acked = {}
        /\ inTurn = {}
        /\ owed = {}
        /\ turns = 0
        /\ ackTurn = [m \in Msgs |-> 0]
        /\ fails = 0
        /\ held = {}
        /\ failed = {}

\* a message lands on the stream, at any time, a turn running or not
Arrive == /\ arrived < N
          /\ arrived' = arrived + 1
          /\ UNCHANGED <<acked, inTurn, owed, turns, ackTurn, fails, held, failed>>

\* the session is free: one turn takes the envelope
Start == /\ inTurn = {}
         /\ Pending # {}
         /\ inTurn' = Envelope(Pending)
         /\ owed' = Pending
         /\ turns' = turns + 1
         /\ UNCHANGED <<arrived, acked, ackTurn, fails, held, failed>>

\* the turn exits 0: everything it carried is acked, together
Accept == /\ inTurn # {}
          /\ acked' = acked \cup inTurn
          /\ ackTurn' = [m \in Msgs |-> IF m \in inTurn THEN turns ELSE ackTurn[m]]
          /\ inTurn' = {}
          /\ owed' = {}
          /\ UNCHANGED <<arrived, turns, fails, held, failed>>

\* the turn fails: nothing is acked, all of it stays pending, held by its claim
Fail == /\ inTurn # {}
        /\ fails < MaxFail
        /\ fails' = fails + 1
        /\ held' = held \cup inTurn
        /\ failed' = failed \cup inTurn
        /\ inTurn' = {}
        /\ owed' = {}
        /\ UNCHANGED <<arrived, acked, turns, ackTurn>>

\* the claims of the held messages open: they are pending again
Reopen == /\ held # {}
          /\ held' = {}
          /\ UNCHANGED <<arrived, acked, inTurn, owed, turns, ackTurn, fails, failed>>

\* everything has arrived and is acked: the end, not a deadlock
Done == /\ arrived = N
        /\ acked = Msgs
        /\ UNCHANGED vars

Next == Arrive \/ Start \/ Accept \/ Fail \/ Reopen \/ Done

Spec == Init /\ [][Next]_vars /\ WF_vars(Arrive) /\ WF_vars(Start) /\ WF_vars(Accept) /\ WF_vars(Reopen)

TypeOK == /\ arrived \in 0..N
          /\ acked \subseteq Msgs
          /\ inTurn \subseteq Msgs
          /\ owed \subseteq Msgs
          /\ ackTurn \in [Msgs -> Nat]
          /\ fails \in 0..MaxFail
          /\ held \subseteq Msgs
          /\ failed \subseteq Msgs

\* no message is delivered in a later turn than a younger one, among the
\* messages no failed turn carried (a failed turn's messages wait out their
\* claim, and a younger one may go first meanwhile: NoYoungerFirstAcrossFailure
\* is the gap, MCDeliveryFailureReorders its witness)
NoYoungerFirst == \A a, b \in acked \ failed : a < b => ackTurn[a] <= ackTurn[b]

NoYoungerFirstAcrossFailure == \A a, b \in acked : a < b => ackTurn[a] <= ackTurn[b]

\* a turn takes the whole pending set, up to the cap: no message waits a
\* turn per older message
EnvelopeTakesAll == inTurn # {} => Cardinality(inTurn) = Min(Cap, Cardinality(owed))

\* only an accepted turn acks, and a turn never carries an acked message
AckOnlyAccepted == /\ inTurn \cap acked = {}
                   /\ \A m \in Msgs : (m \in acked) <=> (ackTurn[m] > 0)

\* every message is delivered and acked in the end
Delivered == \A m \in Msgs : <>(m \in acked)
=============================================================================
