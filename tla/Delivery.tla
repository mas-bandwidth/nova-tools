---------------------------- MODULE Delivery ----------------------------
EXTENDS Naturals, Sequences
CONSTANTS Sessions, Initial, MaxSteps, Broken
VARIABLES active, target, nonce, proven, proofSession, deliveries, steps
vars == <<active, target, nonce, proven, proofSession, deliveries, steps>>
Init == /\ active = Initial /\ target = Initial /\ nonce = 1
        /\ proven = FALSE /\ proofSession = "" /\ deliveries = <<>> /\ steps = 0
Request(who, session) ==
 /\ steps < MaxSteps /\ who = "self" /\ session # target
 /\ target' = session /\ nonce' = nonce + 1 /\ proven' = FALSE /\ proofSession' = ""
 /\ steps' = steps + 1 /\ UNCHANGED <<active, deliveries>>
Answer(session, token) ==
 /\ steps < MaxSteps /\ token = nonce
 /\ IF session = target \/ Broken = "wrong-session"
       THEN /\ active' = target /\ proven' = TRUE /\ proofSession' = session
       ELSE /\ proven' = FALSE /\ proofSession' = session /\ UNCHANGED active
 /\ steps' = steps + 1 /\ UNCHANGED <<target, nonce, deliveries>>
Deliver ==
 /\ steps < MaxSteps /\ proven
 /\ deliveries' = Append(deliveries, [session |-> active, receipt |-> proofSession])
 /\ steps' = steps + 1 /\ UNCHANGED <<active, target, nonce, proven, proofSession>>
Next == \/ \E who \in {"self", "other"}, s \in Sessions: Request(who, s)
        \/ \E s \in Sessions, n \in 1..(MaxSteps+1): Answer(s, n)
        \/ Deliver
TypeOK == /\ active \in Sessions /\ target \in Sessions /\ proven \in BOOLEAN
          /\ nonce \in 1..(MaxSteps+1) /\ proofSession \in Sessions \cup {""}
          /\ steps \in 0..MaxSteps
DeliveryOnlyToProvenSession == \A i \in 1..Len(deliveries): deliveries[i].session = deliveries[i].receipt
SwitchAfterRoundTrip == proven => active = target /\ proofSession = target
Spec == Init /\ [][Next]_vars
=============================================================================
