------------------------------ MODULE MCLandPass ------------------------------
\* The instance: three streams in priority order, two files; the first two streams touch
\* the same file (they meet when both land in one pass), the third touches the other
\* (disjoint from both); the oracle over every tree is enumerated at Init, so every way
\* a tree can be green alone and red combined is a behaviour.
EXTENDS LandPass, TLC

CONSTANTS s1, s2, s3

MCStreams == {s1, s2, s3}
MCOrder == <<s1, s2, s3>>
MCFiles == {"f1", "f2"}
MCTouches == [s \in MCStreams |-> IF s = s3 THEN {"f2"} ELSE {"f1"}]
=============================================================================
