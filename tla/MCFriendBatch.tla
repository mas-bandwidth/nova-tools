------------------------- MODULE MCFriendBatch --------------------------
EXTENDS FriendBatch

\* The batch turn of nova-tools#5540 (head 71c747f6ef); the models are FriendBatch.tla.
\* MCFriendBatch.cfg: the design, the deaf-session bound holds.
\* MCFriendBatchBrokenNoCap.cfg: no cap - a turn in the session holds a deaf
\*   friend up for ever (DeafSessionShownDownWithinBoundPlusCap).
\* MCFriendBatchBrokenBatter.cfg: the bound held from the turn being told rather
\*   than from the gate - the hung-check mutual hold (same property).
=============================================================================
