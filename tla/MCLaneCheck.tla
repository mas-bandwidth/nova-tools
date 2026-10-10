----------------------------- MODULE MCLaneCheck -----------------------------
EXTENDS LaneCheck

\* The two friend-level keys obey the same filter; permuting their names
\* preserves their distinct identities and every subset offered by a tick.
Symmetry == Permutations(Cards) \cup Permutations(FriendKeys)
\* The counters never enable an action. Both translate by the same amount in
\* the correct machine and reset together; only their difference is observed
\* by CountedOncePerSpan. This quotient retains every invariant and transition,
\* including a broken increment and the reset check. It removes no lane state,
\* judgment, event ordering, clock value or epoch.
\* A finished card never returns. An expired report never becomes fresh by
\* advancing time. Discard only those dead names in the view (the stale-beat
\* mutation keeps expired names), and the age of a report naming no card.
\* Existing runs never restart here. At-cap and finished cards have identical
\* future lane behavior; NoCap keeps expired active cards distinct. Ready and
\* working use the same LiveLane test once WorkDeadline's age is supplied, so
\* their labels are interchangeable in this lane-check-only machine.
Inert(c) == column[c] = "done" \/ (ran[c] = Cap /\ Broken # "NoCap")
CardView == [c \in Cards |-> IF Inert(c) THEN Cap + 1 ELSE ran[c]]
ReportView(age, names) ==
    LET relevant == {c \in names : ~Inert(c)}
        expired == age > FriendLaneLive /\ Broken # "StaleBeat"
    IN IF relevant = {} \/ expired THEN <<FriendLaneLive + 1, {}>>
       ELSE <<age, relevant>>
MCView == <<clock, CardView, ReportView(beatAge, beatRunning),
            ReportView(seatAge, seatRunning),
            epoch, open, previous, countEpoch, suppressed - spans,
            noRise, noMiss, noStale, epochRestartOK>>
=============================================================================
