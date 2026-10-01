----------------------------- MODULE MCMerger -----------------------------
\* The instance: two streams, three cards (s1 holds c1 then c2, s2 holds c3), a
\* batch of two.
EXTENDS Merger

MCStreamOf == [c \in {"c1", "c2", "c3"} |-> IF c = "c3" THEN "s2" ELSE "s1"]
MCOrder == [s \in {"s1", "s2"} |-> IF s = "s1" THEN <<"c1", "c2">> ELSE <<"c3">>]
=============================================================================
