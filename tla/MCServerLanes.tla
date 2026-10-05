---------------------------- MODULE MCServerLanes ----------------------------
EXTENDS ServerLanes
\* safety: a beat, two reads (one waits for the other), two writes (one waits for the other)
MCCallers == {"b1", "r1", "r2", "w1", "w2"}
\* liveness: a beat, a read and two writes, a tick
MCCallersLive == {"b1", "r1", "w1", "w2"}
MCKindOf == [r \in MCCallers |->
               IF r = "b1" THEN "beat"
               ELSE IF r \in {"r1", "r2"} THEN "read" ELSE "write"]
MCKindOfLive == [r \in MCCallersLive |-> MCKindOf[r]]
=============================================================================
