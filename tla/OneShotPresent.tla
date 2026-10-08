------------------------- MODULE OneShotPresent -------------------------
\* internal/friend/present.go startPresent(false): requests remain owed before
\* explicit native acceptance; status compacts. Acted receipts survive lost ACK.
EXTENDS Naturals, FiniteSets
CONSTANTS MaxMsgs, Broken
Ids == 1..MaxMsgs
VARIABLES kinds, pending, acted, count, due, session, acceptedBeforeRestart, witness
vars == <<kinds, pending, acted, count, due, session, acceptedBeforeRestart, witness>>
Init ==
  /\ kinds = [i \in Ids |-> "none"] /\ pending = {} /\ acted = {}
  /\ count = [i \in Ids |-> 0] /\ due = TRUE /\ session = "none"
  /\ acceptedBeforeRestart = FALSE /\ witness = FALSE
Send(i, k) ==
  /\ kinds[i] = "none"
  /\ kinds' = [kinds EXCEPT ![i] = k] /\ pending' = pending \cup {i}
  /\ UNCHANGED <<acted, count, due, session, acceptedBeforeRestart, witness>>
Restart ==
  /\ ~due /\ due' = TRUE /\ session' = "none"
  /\ acceptedBeforeRestart' = (\E i \in acted : kinds[i] = "request")
  /\ UNCHANGED <<kinds, pending, acted, count, witness>>
Present ==
  /\ due /\ due' = FALSE
  /\ pending' = IF Broken = "supersede" THEN {}
                 ELSE {i \in pending : kinds[i] = "request" /\ i \notin acted}
  /\ witness' = (witness \/ acceptedBeforeRestart)
  /\ UNCHANGED <<kinds, acted, count, session, acceptedBeforeRestart>>
OpenSession ==
  /\ ~due /\ session = "none" /\ session' = "ses_1"
  /\ UNCHANGED <<kinds, pending, acted, count, due, acceptedBeforeRestart, witness>>
\* Native DeliverTo requires the exact OpenSession target, then the accepted
\* receipt is stamped; a lost stream ACK leaves pending until the next present.
Accept(i) ==
  /\ ~due /\ session = "ses_1" /\ i \in pending
  /\ i \notin acted \/ Broken = "redeliver"
  /\ count[i] < 2 /\ count' = [count EXCEPT ![i] = @ + 1]
  /\ acted' = acted \cup {i}
  /\ \E lost \in BOOLEAN : pending' = IF lost THEN pending ELSE pending \ {i}
  /\ UNCHANGED <<kinds, due, session, acceptedBeforeRestart, witness>>
Next == (\E i \in Ids, k \in {"request", "status"} : Send(i, k))
        \/ Restart \/ Present \/ OpenSession \/ (\E i \in Ids : Accept(i))
Spec == Init /\ [][Next]_vars
TypeOK ==
  /\ kinds \in [Ids -> {"none", "request", "status"}]
  /\ pending \subseteq Ids /\ acted \subseteq Ids /\ count \in [Ids -> 0..2]
  /\ due \in BOOLEAN /\ session \in {"none", "ses_1"}
  /\ acceptedBeforeRestart \in BOOLEAN /\ witness \in BOOLEAN
UnacceptedRequestOwed == {i \in Ids : kinds[i] = "request" /\ i \notin acted} \subseteq pending
AcceptedOnce == \A i \in Ids : count[i] <= 1
ReachAcceptedThenRestartWitness == ~witness
=============================================================================
