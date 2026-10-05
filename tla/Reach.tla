------------------------------- MODULE Reach -------------------------------
\* The reach ladder in internal/friend/reach.go. A rung starts, waits no more
\* than Bound ticks for a nonce-bearing proof, then climbs. Proof is terminal:
\* there is no side effect after it. Broken="stepafterproof" is the reversed
\* witness for that rule.
EXTENDS Naturals

CONSTANTS Bound, Broken
ASSUME Bound >= 1

VARIABLES step, waited, proof, proofStep, result
vars == <<step, waited, proof, proofStep, result>>

TypeOK == /\ step \in {"bus", "push", "window"}
          /\ waited \in 0..Bound
          /\ proof \in BOOLEAN
          /\ proofStep \in {"none", "bus", "push", "window"}
          /\ result \in {"running", "ok", "failed"}

Init == /\ step = "bus" /\ waited = 0 /\ proof = FALSE /\ proofStep = "none" /\ result = "running"

Proof == /\ result = "running"
         /\ proof' = TRUE /\ proofStep' = step /\ result' = "ok"
	         /\ UNCHANGED <<step, waited>>

Wait == /\ result = "running" /\ waited < Bound
        /\ waited' = waited + 1
        /\ UNCHANGED <<step, proof, proofStep, result>>

Advance == /\ result = "running" /\ waited = Bound
           /\ step # "window"
           /\ step' = IF step = "bus" THEN "push" ELSE "window"
           /\ waited' = 0
           /\ UNCHANGED <<proof, proofStep, result>>

Fail == /\ result = "running" /\ step = "window" /\ waited = Bound
        /\ result' = "failed"
        /\ UNCHANGED <<step, waited, proof, proofStep>>

BrokenAdvanceAfterProof == /\ Broken = "stepafterproof" /\ result = "ok"
                           /\ step # "window"
                           /\ step' = IF step = "bus" THEN "push" ELSE "window"
                           /\ UNCHANGED <<waited, proof, proofStep, result>>

Terminal == result # "running" /\ UNCHANGED vars
Next == Proof \/ Wait \/ Advance \/ Fail \/ BrokenAdvanceAfterProof \/ Terminal
Spec == Init /\ [][Next]_vars

NoStepAfterProof == proof => step = proofStep
EveryStepBounded == result = "running" => waited <= Bound
FailedOnlyAfterAllThree == result = "failed" => step = "window" /\ waited = Bound
=============================================================================
