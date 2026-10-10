----------------------------- MODULE MCLaneCheck -----------------------------
EXTENDS LaneCheck

Symmetry == Permutations(Cards)
\* The counters never enable an action. Both translate by the same amount in
\* the correct machine and reset together; only their difference is observed
\* by CountedOncePerSpan. This quotient retains every invariant and transition,
\* including a broken increment and the reset check. It removes no lane state,
\* judgment, event ordering, clock value or epoch.
\* A finished card never returns. An expired report never becomes fresh by
\* advancing time. Discard only those dead names in the view (the stale-beat
\* mutation keeps expired names), and the age of a report naming no card.
ReportView(age, names) ==
    LET relevant == {c \in names : column[c] # "done"}
        expired == age > FriendLaneLive /\ Broken # "StaleBeat"
    IN IF relevant = {} \/ expired THEN <<FriendLaneLive + 1, {}>>
       ELSE <<age, relevant>>
MCView == <<clock, column, ran, ReportView(beatAge, beatRunning),
            ReportView(seatAge, seatRunning),
            epoch, open, previous, countEpoch, suppressed - spans,
            noRise, noMiss, noStale, epochRestartOK>>
=============================================================================
