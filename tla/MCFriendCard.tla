---------------------------- MODULE MCFriendCard ----------------------------
EXTENDS FriendCard, TLC

\* The small instance: two friends, three cards, two tiers, one lane each.
\* Everything is a string so the config's values and the module's operators
\* denote the same things; the design treats tiers as opaque labels.
MCFriends == {"f1", "f2"}
MCCards == {"c1", "c2", "c3"}
MCCards2 == {"c1", "c2"}
MCCards1 == {"c1"}
MCTiers == {"flash", "pro"}
MCCardTier == [c \in MCCards |->
                 IF c = "c1" THEN "flash" ELSE IF c = "c2" THEN "pro" ELSE "flash"]
MCCardTier2 == [c \in MCCards2 |-> IF c = "c1" THEN "flash" ELSE "pro"]
MCCardTier1 == [c \in MCCards1 |-> "flash"]
MCFriendTiers == [f \in MCFriends |->
                     IF f = "f1" THEN {"flash", "pro"} ELSE {"flash"}]
MCWidth == [f \in MCFriends |-> 1]

\* The friends are interchangeable and so are the cards: the safety cases check
\* one representative of each permutation (SYMMETRY MCSym). The liveness cases
\* (SpecLive) run without it, since TLC's symmetry reduction is sound for
\* reachability and not relied on for the temporal properties.
MCSym == Permutations(Friends) \cup Permutations(Cards)
=============================================================================
