----------------------------- MODULE Reach ------------------------------
\* nova-friend reach (docs/SPEC-FRIEND.md, Reach; cmd/nova-friend/reach.go).
\* The ladder is bus, then push, then window, from From (the --from flag, which
\* starts partway up). A proof stops it, at any time inside the bound, not only
\* at it. failed is the state after every step from From was left with no
\* proof; refused is the window step without the accessibility permission
\* (exit 2). A push step whose daemon is down or whose push fails is left at
\* once, before the bound.
\*
\* Broken names the reversed witnesses, each caught by one property below:
\*   "stepafterproof"  a step taken after a proof: NoStepAfterProof
\*   "fromtriedall"    tried claims every step after a --from start:
\*                     FailedOnlyAfterAllThree

EXTENDS Naturals

CONSTANTS Bound, From, Broken

ASSUME Bound \in Nat /\ Bound >= 1
ASSUME From \in {"bus", "push", "window"}
ASSUME Broken \in {"none", "stepafterproof", "fromtriedall"}

Steps == {"bus", "push", "window"}

\* The steps the ladder walks from From: the ladder's suffix at From.
StepsFrom == IF From = "bus" THEN {"bus", "push", "window"}
             ELSE IF From = "push" THEN {"push", "window"}
             ELSE {"window"}

VARIABLES step, proof, clock, tried

vars == <<step, proof, clock, tried>>

Init ==
    /\ step = From
    /\ proof = FALSE
    /\ clock = 0
    /\ tried = {}

Tick ==
    /\ step \in Steps
    /\ clock < Bound
    /\ clock' = clock + 1
    /\ UNCHANGED <<step, proof, tried>>

\* A proof stops the ladder, at any time inside the bound (waitReach polls for
\* it before the step times out), not only at it.
Prove ==
    /\ step \in Steps
    /\ clock < Bound
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

\* A step left with no proof at its bound.
Leave ==
    /\ step \in Steps
    /\ clock = Bound
    /\ Climb

\* A push whose daemon is down or whose push fails is left at once, before the
\* bound (reach.go: the push's NONE continues to the window step).
ClimbAtOnce ==
    /\ step = "push"
    /\ tried' = tried \union {step}
    /\ step' = "window"
    /\ clock' = 0
    /\ UNCHANGED proof

\* The window step without the accessibility permission refuses (exit 2), an
\* end the ladder otherwise lacks.
Refuse ==
    /\ step = "window"
    /\ step' = "refused"
    /\ UNCHANGED <<proof, clock, tried>>

\* The reversed witness: a step taken after a proof.
StepAfterProof ==
    /\ Broken = "stepafterproof"
    /\ proof = TRUE
    /\ step = "ok"
    /\ step' = "push"
    /\ clock' = 0
    /\ UNCHANGED <<proof, tried>>

\* The reversed witness: tried claims every step after a --from start.
FromTriedAll ==
    /\ Broken = "fromtriedall"
    /\ step \in Steps
    /\ tried' = Steps
    /\ step' = "failed"
    /\ UNCHANGED <<proof, clock>>

Next ==
    \/ Tick
    \/ Prove
    \/ Leave
    \/ ClimbAtOnce
    \/ Refuse
    \/ StepAfterProof
    \/ FromTriedAll
    \/ (step \in {"ok", "failed", "refused"} /\ UNCHANGED vars)

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ step \in (Steps \union {"ok", "failed", "refused"})
    /\ proof \in BOOLEAN
    /\ clock \in 0..Bound
    /\ tried \subseteq Steps

NoStepAfterProof == proof = TRUE => step = "ok"

EveryStepBounded == clock <= Bound

\* failed only after every step from From was left with no proof.
FailedOnlyAfterAllThree == step = "failed" => tried = StepsFrom

=============================================================================
