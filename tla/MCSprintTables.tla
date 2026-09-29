--------------------------- MODULE MCSprintTables ---------------------------
\* The small instance: two streams, three primaries (p2 needs p1, both in s1;
\* p3 in s2), two fleet members, three readers, attempts bounded at two.
EXTENDS SprintTables

MCStreamOf == [p \in Primaries |-> IF p = "p3" THEN "s2" ELSE "s1"]
MCNeeds == [p \in Primaries |-> IF p = "p2" THEN {"p1"} ELSE {}]
MCScore0 == [p \in Primaries |-> CASE p = "p1" -> 1 [] p = "p2" -> 2 [] p = "p3" -> 3]
AllFixes == {"cutatstart", "redeal", "stopbyresume", "pendingblocks", "readsexhausted"}
=============================================================================
