------------------------- MODULE DeliverySession --------------------------
\* The session binding (docs/SPEC-FRIEND.md, "Session binding and joining";
\* internal/friend/session_binding.go, session_adapter.go, presence.go): a
\* delivery goes only to the conversation that answered the current nonce, and
\* a friend points the daemon at another conversation only by a self-addressed
\* request, a fresh nonce and that conversation's answer. A generic bus message
\* cannot prove a target; a receipt from another conversation proves nothing.
\*
\* The state the code owns: active (the conversation the adapter delivers into),
\* target (the conversation the binding wants), nonce (the open challenge),
\* proven (whether the target answered the current nonce), proofSession (the
\* conversation that answered), deliveries (what ordinary delivery recorded, each
\* entry its session and the receipt that licensed it) and steps. The outside: a
\* request (the friend herself, Request), an answer carrying the nonce (Answer),
\* and ordinary delivery (Deliver).
\*
\* Broken = "none" is the design. The other value is a reversed witness:
\*   "wrong-session" an answer from another conversation proves the target:
\*                   DeliveryOnlyToProvenSession

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

\* every ordinary delivery went to the conversation that supplied its receipt
DeliveryOnlyToProvenSession == \A i \in 1..Len(deliveries): deliveries[i].session = deliveries[i].receipt

\* a proven target is the active conversation the round trip started for
SwitchAfterRoundTrip == proven => active = target /\ proofSession = target

Spec == Init /\ [][Next]_vars
=============================================================================
