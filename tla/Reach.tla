------------------------------ MODULE Reach ------------------------------
\* nova-friend reach (docs/SPEC-FRIEND.md, Reach; cmd/nova-friend/reach.go).
\* The ladder is bus, then push, then window. A proof stops it. Each step's
\* clock stays inside Bound. failed is only the state after all three steps
\* were left with no proof. Proof is a choice at the bound, not a duty: a
\* fairness that forces it would make the ladder unable to climb.
\*
\* Broken names the reversed witness: a step taken after a proof. That breaks
\* NoStepAfterProof and nothing else.

EXTENDS Naturals

CONSTANTS Bound, Broken

ASSUME Bound \in Nat /\ Bound >= 1
ASSUME Broken \in BOOLEAN

Steps == {"bus", "push", "window"}

VARIABLES step, proof, clock, tried

vars == <<step, proof, clock, tried>>

Init ==
    /\ step = "bus"
    /\ proof = FALSE
    /\ clock = 0
    /\ tried = {}

Tick ==
    /\ step \in Steps
    /\ clock < Bound
    /\ clock' = clock + 1
    /\ UNCHANGED <<step, proof, tried>>

Prove ==
    /\ proof' = TRUE
    /\ step' = "ok"
    /\ UNCHANGED <<clock, tried>>

Climb ==
    /\ proof' = FALSE
    /\ tried' = tried \union {step}
    /\ \/ /\ step = "window"
          /\ step' = "failed"
          /\ UNCHANGED clock
       \/ /\ step # "window"
          /\ step' = IF step = "bus" THEN "push" ELSE "window"
          /\ clock' = 0

Leave ==
    /\ step \in Steps
    /\ clock = Bound
    /\ \/ Prove
       \/ Climb

\* The reversed witness: a step after a proof. Disabled when Broken is FALSE.
StepAfterProof ==
    /\ Broken
    /\ proof = TRUE
    /\ step = "ok"
    /\ step' = "push"
    /\ clock' = 0
    /\ UNCHANGED <<proof, tried>>

Next ==
    \/ Tick
    \/ Leave
    \/ StepAfterProof
    \/ (step \in {"ok", "failed"} /\ UNCHANGED vars)

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ step \in (Steps \union {"ok", "failed"})
    /\ proof \in BOOLEAN
    /\ clock \in 0..Bound
    /\ tried \subseteq Steps

NoStepAfterProof == proof = TRUE => step = "ok"

EveryStepBounded == clock <= Bound

FailedOnlyAfterAllThree == step = "failed" => tried = Steps

=============================================================================
