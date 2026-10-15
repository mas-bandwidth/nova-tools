---------------------------- MODULE BenchGrant ----------------------------
\* Server-clock grants, an immutable first-expiry obligation, and one bus
\* commit to both recipients with the told mark. Extend and End first capture
\* a deadline already passed; neither erases an owed notification.
\* Scope: a stale watcher resumes within the bus once-key retention (seven
\* days). A paused sender beyond that retention is outside this finite model;
\* normal restarted watchers reread the persistent told mark.
EXTENDS Naturals, FiniteSets
CONSTANTS IDs, Limit, Broken
VARIABLES grants, now, changed, wake, bus, copies, lastDue, lastSuccess
vars == <<grants, now, changed, wake, bus, copies, lastDue, lastSuccess>>
Empty == [exists |-> FALSE, deadline |-> 0, ended |-> FALSE,
          owed |-> 0, told |-> FALSE]
Due(g) == g.exists /\ ~g.told /\
          (g.owed > 0 \/ (~g.ended /\ now >= g.deadline))
Owed(g) == IF g.owed > 0 THEN g.owed
           ELSE IF Due(g) THEN g.deadline ELSE 0
DueIDs == {i \in IDs : Due(grants[i])}
Init == /\ grants = [i \in IDs |-> Empty]
        /\ now = 0 /\ changed = FALSE /\ wake = FALSE
        /\ bus = {} /\ copies = [i \in IDs |-> 0]
        /\ lastDue = {} /\ lastSuccess = FALSE
Add(i) == /\ ~grants[i].exists /\ now < Limit
          /\ grants' = [grants EXCEPT ![i] = [Empty EXCEPT
                            !.exists = TRUE, !.deadline = now + 1]]
          /\ changed' = TRUE
          /\ UNCHANGED <<now, wake, bus, copies, lastDue, lastSuccess>>
Extend(i) == /\ grants[i].exists /\ ~grants[i].ended /\ now < Limit
             /\ grants' = [grants EXCEPT ![i].owed = Owed(grants[i]),
                                  ![i].deadline = now + 1]
             /\ changed' = TRUE
             /\ UNCHANGED <<now, wake, bus, copies, lastDue, lastSuccess>>
End(i) == /\ grants[i].exists /\ ~grants[i].ended
          /\ grants' = [grants EXCEPT ![i].owed = Owed(grants[i]),
                               ![i].ended = TRUE]
          /\ changed' = TRUE
          /\ UNCHANGED <<now, wake, bus, copies, lastDue, lastSuccess>>
Tick == /\ now < Limit /\ now' = now + 1
        /\ UNCHANGED <<grants, changed, wake, bus, copies, lastDue, lastSuccess>>
\* The transport's successful commit is one action, not told-before-send.
Send == /\ bus' = bus \cup DueIDs
        /\ copies' = [i \in IDs |-> IF i \in DueIDs /\ i \notin bus
                                      THEN copies[i] + 1 ELSE copies[i]]
        /\ grants' = [i \in IDs |-> IF i \in DueIDs
                             THEN [grants[i] EXCEPT !.told = TRUE]
                             ELSE grants[i]]
        /\ lastSuccess' = TRUE
SendLost == /\ Broken # "notimer"
            /\ UNCHANGED <<bus, copies>>
            /\ grants' = IF Broken = "toldfirst"
                    THEN [i \in IDs |-> IF i \in DueIDs
                            THEN [grants[i] EXCEPT !.told = TRUE]
                            ELSE grants[i]] ELSE grants
            /\ lastSuccess' = FALSE
Sweep == /\ lastDue' = DueIDs /\ (Send \/ SendLost)
Wake == /\ changed \/ (Broken # "notimer" /\ DueIDs # {})
        /\ Sweep /\ wake' = TRUE /\ changed' = FALSE
        /\ UNCHANGED now
Next == (\E i \in IDs : Add(i) \/ Extend(i) \/ End(i)) \/ Tick \/ Wake
\* Fair successful transport eventually commits an outstanding obligation.
GoodWake == Wake /\ lastSuccess'
Spec == Init /\ [][Next]_vars /\ WF_vars(Tick) /\ WF_vars(Wake)
             /\ WF_vars(GoodWake)
TypeOK == /\ now \in 0..Limit /\ bus \subseteq IDs
          /\ \A i \in IDs : copies[i] \in 0..1
NeverToldTwice == \A i \in IDs : copies[i] <= 1
\* One successful watcher event sends every due row without another clock step;
\* a failed transport is explicitly retried by the backstop wake.
PushedWithinOneEvent == lastSuccess => lastDue \subseteq bus
EveryExpiryIsPushed == \A i \in IDs : Due(grants[i]) ~> (i \in bus)
=============================================================================
