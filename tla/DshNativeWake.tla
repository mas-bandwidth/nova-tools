-------------------------- MODULE DshNativeWake --------------------------
\* Immutable publications, ordinary inbox hold, priority and uncertain claims.
\* This qualitative model excludes filesystem permissions, message persistence,
\* actual model reading and time bounds. Executing regressions cover the bridge.
EXTENDS Naturals, FiniteSets
CONSTANT Broken
VARIABLES published, queued, claimed, delivered, attempted, ordinaryHold,
          idle, woke, modelProof
vars == <<published, queued, claimed, delivered, attempted, ordinaryHold,
          idle, woke, modelProof>>
Events == {"ordinary", "challenge"}
Init == /\ published = {} /\ queued = {} /\ claimed = {}
        /\ delivered = {} /\ attempted = {} /\ ordinaryHold = FALSE
        /\ idle = TRUE /\ woke = FALSE /\ modelProof = FALSE
Publish(e) ==
    /\ e \in Events \ published
    /\ published' = published \cup {e}
    /\ queued' = queued \cup {e}
    /\ UNCHANGED <<claimed, delivered, attempted, ordinaryHold, idle, woke, modelProof>>
Claim(e) ==
    /\ e \in queued
    /\ (IF e = "challenge" THEN (Broken # "hide" \/ ~ordinaryHold) ELSE ~ordinaryHold)
    /\ queued' = queued \ {e}
    /\ claimed' = claimed \cup {e}
    /\ attempted' = attempted \cup {e}
    /\ ordinaryHold' = (ordinaryHold \/ e = "ordinary")
    /\ UNCHANGED <<published, delivered, idle, woke, modelProof>>
Accept(e) ==
    /\ e \in claimed \ delivered
    /\ delivered' = delivered \cup {e}
    /\ idle' = FALSE
    /\ woke' = (woke \/ idle)
    /\ UNCHANGED <<published, queued, claimed, attempted, ordinaryHold, modelProof>>
\* Native exceptions retain the claim; there is no retry transition.
InboxClaim ==
    /\ ordinaryHold /\ "ordinary" \in delivered
    /\ ordinaryHold' = FALSE
    /\ UNCHANGED <<published, queued, claimed, delivered, attempted, idle, woke, modelProof>>
Next == (\E e \in Events: Publish(e) \/ Claim(e) \/ Accept(e)) \/ InboxClaim
TypeOK == /\ published \subseteq Events /\ queued \subseteq published
          /\ claimed \subseteq published /\ delivered \subseteq claimed
          /\ attempted = claimed
          /\ ordinaryHold \in BOOLEAN /\ idle \in BOOLEAN
          /\ woke \in BOOLEAN /\ modelProof = FALSE
NoResurrection == queued \cap claimed = {}
PriorityUnblocked == "challenge" \in queued => ENABLED Claim("challenge")
IdleWakeUnreachable == ~woke
Spec == Init /\ [][Next]_vars
=============================================================================
