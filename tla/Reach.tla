------------------------------- MODULE Reach -------------------------------
\* The reach ladder (docs/SPEC-FRIEND.md, Reach; cmd/nova-friend/reach.go climb).
\* One silent friend. The steps are bus, then push, then window. A proof ends
\* the ladder at ok. The bound with no proof climbs. failed is only after
\* every step. The clock of a step never passes StepBound.
\*
\* A skipped push and an accessibility refusal are outside this model. The
\* skip is a guard before the push action. The refusal is exit 2 and does
\* not enter failed.
\*
\* Broken:
\*   "none"             the design
\*   "step-after-proof" a step is taken after a proof (breaks NoStepAfterProof)

EXTENDS Naturals

CONSTANTS StepBound, Broken

VARIABLES step, proof, clock, tried

vars == <<step, proof, clock, tried>>

Steps == {"bus", "push", "window"}

TypeOK ==
  /\ step \in (Steps \cup {"ok", "failed"})
  /\ proof \in BOOLEAN
  /\ clock \in 0..StepBound
  /\ tried \subseteq Steps

Init ==
  /\ step = "bus"
  /\ proof = FALSE
  /\ clock = 0
  /\ tried = {}

Tick ==
  /\ step \in Steps
  /\ ~proof
  /\ clock < StepBound
  /\ clock' = clock + 1
  /\ UNCHANGED <<step, proof, tried>>

SeeProof ==
  /\ step \in Steps
  /\ ~proof
  /\ clock <= StepBound
  /\ proof' = TRUE
  /\ step' = "ok"
  /\ UNCHANGED <<clock, tried>>

NextOf(s) ==
  CASE s = "bus" -> "push"
    [] s = "push" -> "window"
    [] s = "window" -> "failed"

Climb ==
  /\ step \in Steps
  /\ ~proof
  /\ clock = StepBound
  /\ step' = NextOf(step)
  /\ clock' = 0
  /\ tried' = tried \cup {step}
  /\ UNCHANGED proof

\* The reversed witness: the ladder moves again after a proof.
StepAfterProof ==
  /\ Broken = "step-after-proof"
  /\ proof
  /\ step = "ok"
  /\ step' = "push"
  /\ UNCHANGED <<proof, clock, tried>>

Next == Tick \/ SeeProof \/ Climb \/ StepAfterProof

Spec == Init /\ [][Next]_vars

NoStepAfterProof == proof => step = "ok"

EveryStepBounded == clock <= StepBound

FailedOnlyAfterAllThree == step = "failed" => tried = Steps

=============================================================================
