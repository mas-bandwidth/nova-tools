-------------------------- MODULE MCFriendPresence --------------------------
EXTENDS FriendPresence

\* Two reversed witnesses for the batch turn fix (71c747f6ef):
\* MCFriendPresenceBrokenNoCap.cfg: back-to-back turns hold a deaf friend up forever
\* MCFriendPresenceBrokenBatter.cfg: turn counted from BatchTurn alone rather than the gate
=============================================================================
