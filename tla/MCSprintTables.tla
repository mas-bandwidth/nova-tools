--------------------------- MODULE MCSprintTables ---------------------------
\* The small instance: two streams, three primaries (p2 needs p1, both in s1;
\* p3 in s2), two fleet members, three readers, attempts bounded at two.
\* Members and readers are model values given in the configurations.
EXTENDS SprintTables, TLC

MCStreams == {"s1", "s2"}
MCPrimaries == {"p1", "p2", "p3"}
MCStreamOf == [p \in Primaries |-> IF p = "p3" THEN "s2" ELSE "s1"]
MCNeeds == [p \in Primaries |-> IF p = "p2" THEN {"p1"} ELSE {}]
MCScore0 == [p \in Primaries |-> CASE p = "p1" -> 1 [] p = "p2" -> 2 [] p = "p3" -> 3]
\* Admission: all three before the first step, or p2 admitted by add later.
MCAllAdmitted == MCPrimaries
MCLateAdd == {"p1", "p3"}
\* The model's proposed fixes, and the design as decided without them.
AllFixes == {"readsexhausted"}
AsDecided == {}
\* For the safety runs members and readers are symmetric.
Sym == Permutations(Members) \cup Permutations(Readers)
=============================================================================
