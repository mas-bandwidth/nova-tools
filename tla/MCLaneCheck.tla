----------------------------- MODULE MCLaneCheck -----------------------------
EXTENDS LaneCheck

Symmetry == Permutations(Cards)
\* The counters never enable an action. Both translate by the same amount in
\* the correct machine and reset together; only their difference is observed
\* by CountedOncePerSpan. This quotient retains every invariant and transition,
\* including a broken increment and the reset check. It removes no lane state,
\* judgment, event ordering, clock value or epoch.
MCView == <<clock, column, ran, beatAge, beatRunning, seatAge, seatRunning,
            epoch, open, previous, countEpoch, suppressed - spans,
            noRise, noMiss, noStale, epochRestartOK>>
=============================================================================
