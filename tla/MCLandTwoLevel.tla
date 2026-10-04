---------------------------- MODULE MCLandTwoLevel ----------------------------
\* The instance: three streams under the root (cmd/nova-sprint/land_tree_proof_functional_test.go
\* in small): s1 = a1, a2; s2 = b1, b2; s3 = c1. Every verdict of every card
\* (green, red, red once the base moves), every subset of the conflicts between
\* siblings (a1, a2) and across streams (a2, b1), b2 a no-op or not, and one move
\* of the base by another writer.
EXTENDS LandTwoLevel, TLC

MCSOrder == <<"s1", "s2", "s3">>
MCLane == ("s1" :> <<"a1", "a2">>) @@ ("s2" :> <<"b1", "b2">>) @@ ("s3" :> <<"c1">>)
MCClashPairs == {{"a1", "a2"}, {"a2", "b1"}}

\* the liveness instance in the records' budget: s1 and s2 alone
MCSOrderSmall == <<"s1", "s2">>
MCLaneSmall == ("s1" :> <<"a1", "a2">>) @@ ("s2" :> <<"b1", "b2">>)
=============================================================================
