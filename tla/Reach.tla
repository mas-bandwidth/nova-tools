-------------------------------- MODULE Reach --------------------------------
\* nova-friend reach (internal/friend/reach.go Reach.Run; docs/SPEC-FRIEND.md,
\* Reach): the ladder that gets a silent friend's attention. Each step (bus,
\* push, window) takes its effect, or is skipped (the daemon is not up, no
\* window is named), then reads the bus for proof, sleeping between reads, up
\* to Bound sleeps. Proof ends the ladder ok; no proof at the bound climbs to
\* the next step; after the last step the ladder has failed.
\*
\* The state: at, the step the ladder is on or its end; phase, what the step
\* does next ("effect", "read" or "slept", the last a sleep after which the
\* bus must be read again); waited, the step's sleeps; arrived, whether the
\* session's proof is on the bus; considered, the steps taken or skipped;
\* stepAfterProof, a ghost set when a step's effect is taken with the proof
\* already on the bus. The outside: the session's proof arrives at any time
\* while the ladder reads or sleeps. Abstracted away: the instant between the
\* read that found no proof and the next step's effect, read and effect being
\* one step here; a proof landing in that instant is answered by the next read.
\*
\* The design, Broken = {}: every decision to climb is taken on a read made
\* after the last sleep.
\* Reversed witness:
\*   "stepafterproof"  the step climbs when its time is up after a sleep,
\*                     without reading the bus first: a proof that arrived
\*                     during that sleep is followed by the next step's
\*                     effect (NoStepAfterProof).
EXTENDS Naturals

CONSTANTS Bound, Broken

StepSet == {"bus", "push", "window"}
Skippable == {"push", "window"}

VARIABLES at, phase, waited, arrived, considered, stepAfterProof
vars == <<at, phase, waited, arrived, considered, stepAfterProof>>

Above(s) == IF s = "bus" THEN "push" ELSE IF s = "push" THEN "window" ELSE "failed"

TypeOK ==
    /\ at \in StepSet \cup {"ok", "failed"}
    /\ phase \in {"effect", "read", "slept"}
    /\ waited \in 0..Bound
    /\ arrived \in BOOLEAN
    /\ considered \subseteq StepSet
    /\ stepAfterProof \in BOOLEAN

Init ==
    /\ at = "bus"
    /\ phase = "effect"
    /\ waited = 0
    /\ arrived = FALSE
    /\ considered = {}
    /\ stepAfterProof = FALSE

\* The step's effect: a message sent, or typed into the window.
Effect ==
    /\ at \in StepSet /\ phase = "effect"
    /\ phase' = "read" /\ waited' = 0
    /\ considered' = considered \cup {at}
    /\ stepAfterProof' = (stepAfterProof \/ arrived)
    /\ UNCHANGED <<at, arrived>>

\* The step cannot be taken here: REACH SKIP, and the next step at once.
Skip ==
    /\ at \in Skippable /\ phase = "effect"
    /\ at' = Above(at) /\ phase' = "effect" /\ waited' = 0
    /\ considered' = considered \cup {at}
    /\ UNCHANGED <<arrived, stepAfterProof>>

\* One read of the bus: proof ends the ladder (REACH PROOF); none at the bound
\* climbs (REACH NONE); none before it sleeps.
Read ==
    /\ at \in StepSet /\ phase = "read"
    /\ \/ /\ arrived
          /\ at' = "ok" /\ UNCHANGED <<phase, waited>>
       \/ /\ ~arrived /\ waited = Bound
          /\ at' = Above(at) /\ phase' = "effect" /\ waited' = 0
       \/ /\ ~arrived /\ waited < Bound
          /\ phase' = "slept" /\ waited' = waited + 1 /\ UNCHANGED at
    /\ UNCHANGED <<arrived, considered, stepAfterProof>>

Wake ==
    /\ at \in StepSet /\ phase = "slept"
    /\ phase' = "read"
    /\ UNCHANGED <<at, waited, arrived, considered, stepAfterProof>>

BrokenClimb ==
    /\ "stepafterproof" \in Broken
    /\ at \in StepSet /\ phase = "slept" /\ waited = Bound
    /\ at' = Above(at) /\ phase' = "effect" /\ waited' = 0
    /\ UNCHANGED <<arrived, considered, stepAfterProof>>

\* The outside: the session answers.
Arrive ==
    /\ at \in StepSet /\ phase \in {"read", "slept"} /\ ~arrived
    /\ arrived' = TRUE
    /\ UNCHANGED <<at, phase, waited, considered, stepAfterProof>>

Done ==
    /\ at \in {"ok", "failed"}
    /\ UNCHANGED vars

Ladder == Effect \/ Skip \/ Read \/ Wake \/ BrokenClimb

Next == Ladder \/ Arrive \/ Done

Spec == Init /\ [][Next]_vars /\ WF_vars(Ladder)

NoStepAfterProof == ~stepAfterProof

EveryStepBounded == at \in StepSet => waited <= Bound

FailedOnlyAfterAllThree == at = "failed" => considered = StepSet

OkOnlyOnProof == at = "ok" => arrived

Ends == <>(at \in {"ok", "failed"})
==============================================================================
