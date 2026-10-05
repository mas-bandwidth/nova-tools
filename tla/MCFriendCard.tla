---------------------------- MODULE MCFriendCard ----------------------------
EXTENDS FriendCard, TLC

\* The friends are interchangeable, and so are the cards: the safety cases
\* check one representative of each permutation (SYMMETRY MCSym). The
\* liveness cases (SpecLive) run without it.
MCSym == Permutations(Friends) \cup Permutations(Cards)
=============================================================================
