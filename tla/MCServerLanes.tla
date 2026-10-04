---------------------------- MODULE MCServerLanes ----------------------------
EXTENDS ServerLanes
\* safety: a beat, a read, three writes in line
MCCallers == {"b1", "r1", "w1", "w2", "w3"}
\* liveness: a read and two writes
MCCallersLive == {"r1", "w1", "w2"}
MCKindOf == [r \in MCCallers |->
               IF r = "b1" THEN "beat"
               ELSE IF r \in {"r1", "r2"} THEN "read" ELSE "write"]
MCKindOfLive == [r \in MCCallersLive |-> MCKindOf[r]]
=============================================================================
