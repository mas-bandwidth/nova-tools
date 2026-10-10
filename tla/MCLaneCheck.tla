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
\* A working card at its cap cannot be taken again and has the same future
\* lane behavior as a finished card. Keep it distinct for NoCap, where that
\* guard is deliberately absent. Ready cards retain their clocks: Take resets
\* them, so they can become live again. No event is disabled by this quotient.
Inert(c) == column[c] = "done" \/
            (column[c] = "working" /\ ran[c] = Cap /\ Broken # "NoCap")
CardView == [c \in Cards |-> IF Inert(c) THEN <<"inert", 0>> ELSE <<column[c], ran[c]>>]
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
