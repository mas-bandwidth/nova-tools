--------------------------- MODULE MCSprintTables ---------------------------
\* The small instances: two streams, three primaries (p2 needs p1, both in s1;
\* p3 in s2), or two (p1 in s1, p3 in s2); two fleet members, four readers,
\* attempts bounded at two. Members and readers are model values given in the
\* configurations.
EXTENDS SprintTables, TLC

MCStreams == {"s1", "s2"}
MCPrimaries == {"p1", "p2", "p3"}
MCPrimaries2 == {"p1", "p3"}
MCPrimaries1 == {"p3"}
MCStreamOf == [p \in Primaries |-> IF p = "p3" THEN "s2" ELSE "s1"]
MCNeeds == [p \in Primaries |-> IF p = "p2" THEN {"p1"} ELSE {}]
\* No needs: p1 and p2 can be queued in s1 together.
MCNoNeeds == [p \in Primaries |-> {}]
\* Needs across streams: p3 (s2) needs p1 (s1) too.
MCNeedsCross == [p \in Primaries |-> IF p \in {"p2", "p3"} THEN {"p1"} ELSE {}]
\* A cycle across streams, which add must refuse.
MCNeedsCycle == [p \in Primaries |-> CASE p = "p1" -> {"p3"} [] p = "p3" -> {"p1"} [] OTHER -> {}]
MCNone == {}
\* The sentinel instance: g (s1) needs p1; p3 (s2) needs g.
MCPrimariesG == {"p1", "g", "p3"}
MCNeedsG == [p \in Primaries |-> CASE p = "g" -> {"p1"} [] p = "p3" -> {"g"} [] OTHER -> {}]
MCSentinels == {"g"}
\* The positional instance, all in s1: p1, p2 admitted; the sentinel g is
\* inserted between them, and p0 may be added in front of g.
MCPrimariesPos == {"p1", "p0", "g", "p2"}
MCScorePos == [p \in Primaries |-> CASE p = "p1" -> 1 [] p = "p0" -> 2 [] p = "g" -> 3 [] OTHER -> 4]
MCPosAdmitted == {"p1", "p2"}
\* The smaller positional instance, without p0.
MCPrimariesPos3 == {"p1", "g", "p2"}
MCScore0 == [p \in Primaries |-> CASE p = "p1" -> 1 [] p = "p2" -> 2 [] p = "p3" -> 3 [] p = "g" -> 4]
\* Admission: every primary before the first step, or p2 admitted by add.
MCAllAdmitted == Primaries
MCLateAdd == {"p1", "p3"}
\* For the safety runs members and readers are symmetric.
Sym == Permutations(Members) \cup Permutations(Readers)
=============================================================================
