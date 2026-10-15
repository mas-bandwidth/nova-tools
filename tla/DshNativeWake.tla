-------------------------- MODULE DshNativeWake --------------------------
\* Immutable publications, ordinary inbox hold, priority and uncertain claims.
\* This qualitative model excludes filesystem permissions, message persistence,
\* actual model reading and time bounds. Executing regressions cover the bridge.
\* Retarget is the follow file (session.json) moving the receiver to a newer
\* session: the old agent's pre-step listener is released, so its ordinary
\* hold can never clear and must not follow (receiver.mjs attach).
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES published, queued, claimed, delivered, attempted, ordinaryHold,
          idle, woke, modelProof, target, holdTarget
vars == <<published, queued, claimed, delivered, attempted, ordinaryHold,
          idle, woke, modelProof, target, holdTarget>>
Events == {"ordinary", "challenge"}
Init == /\ published = {} /\ queued = {} /\ claimed = {}
        /\ delivered = {} /\ attempted = {} /\ ordinaryHold = FALSE
        /\ idle = TRUE /\ woke = FALSE /\ modelProof = FALSE
        /\ target = 1 /\ holdTarget = 0
Publish(e) ==
    /\ e \in Events \ published
    /\ published' = published \cup {e}
    /\ queued' = queued \cup {e}
    /\ UNCHANGED <<claimed, delivered, attempted, ordinaryHold, idle, woke, modelProof, target, holdTarget>>
Claim(e) ==
    /\ e \in queued
    /\ (IF e = "challenge" THEN (Broken # "hide" \/ ~ordinaryHold) ELSE ~ordinaryHold)
    /\ queued' = queued \ {e}
    /\ claimed' = claimed \cup {e}
    /\ attempted' = attempted \cup {e}
    /\ ordinaryHold' = (ordinaryHold \/ e = "ordinary")
    /\ holdTarget' = IF e = "ordinary" THEN target ELSE holdTarget
    /\ UNCHANGED <<published, delivered, idle, woke, modelProof, target>>
Accept(e) ==
    /\ e \in claimed \ delivered
    /\ delivered' = delivered \cup {e}
    /\ idle' = FALSE
    /\ woke' = (woke \/ idle)
    /\ UNCHANGED <<published, queued, claimed, attempted, ordinaryHold, modelProof, target, holdTarget>>
\* Native exceptions retain the claim; there is no retry transition.
InboxClaim ==
    /\ ordinaryHold /\ "ordinary" \in delivered
    /\ holdTarget = target
    /\ ordinaryHold' = FALSE
    /\ holdTarget' = 0
    /\ UNCHANGED <<published, queued, claimed, delivered, attempted, idle, woke, modelProof, target>>
\* The friend starts a newer session; the producer rewrites the follow file.
\* Broken "inherit" keeps the old agent's hold on the new target.
Retarget ==
    /\ target = 1
    /\ target' = 2
    /\ ordinaryHold' = (Broken = "inherit" /\ ordinaryHold)
    /\ holdTarget' = IF Broken = "inherit" THEN holdTarget ELSE 0
    /\ UNCHANGED <<published, queued, claimed, delivered, attempted, idle, woke, modelProof>>
Next == (\E e \in Events: Publish(e) \/ Claim(e) \/ Accept(e)) \/ InboxClaim \/ Retarget
TypeOK == /\ published \subseteq Events /\ queued \subseteq published
          /\ claimed \subseteq published /\ delivered \subseteq claimed
          /\ attempted = claimed
          /\ ordinaryHold \in BOOLEAN /\ idle \in BOOLEAN
          /\ woke \in BOOLEAN /\ modelProof = FALSE
          /\ target \in {1, 2} /\ holdTarget \in {0, 1, 2}
NoResurrection == queued \cap claimed = {}
PriorityUnblocked == "challenge" \in queued => ENABLED Claim("challenge")
IdleWakeUnreachable == ~woke
\* An ordinary hold always belongs to the agent the receiver delivers to, so
\* it can clear; a hold left on a retired session would block ordinary forever.
HoldOnTarget == ordinaryHold => holdTarget = target
Spec == Init /\ [][Next]_vars
=============================================================================
