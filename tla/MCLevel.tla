------------------------------- MODULE MCLevel -------------------------------
\* The instances of Level.tla (tla/CASES.tsv, group level).
\*
\* THREE: three members of widths 2, 1 and 3 (their rooms and backlogs differ),
\* three ready cards, every up order, every start of the deal's index, a member
\* starting with zero to two cards outside its queue (so a width lowered under
\* its working cards is in the set), a card with at most one refuser.
\*
\* FOUR: the wedge's shape (nova-tools#5122): four members of widths 3, 3, 3 and
\* 1, the narrow one holding four or five working cards, the others two or
\* three, three ready cards, the members up in name order.
EXTENDS Level

MCOrder3 == <<"a", "b", "c">>
MCCards3 == <<"c1", "c2", "c3">>
MCWidth3 == [m \in {"a", "b", "c"} |-> CASE m = "a" -> 2 [] m = "b" -> 1 [] OTHER -> 3]
MCWorks3 == [m \in {"a", "b", "c"} |-> 0..2]
MCUpAll3 == {o \in [1..3 -> {"a", "b", "c"}] : {o[i] : i \in 1..3} = {"a", "b", "c"}}

MCOrder4 == <<"a", "s", "t", "u">>
MCWidth4 == [m \in {"a", "s", "t", "u"} |-> IF m = "u" THEN 1 ELSE 3]
MCWorks4 == [m \in {"a", "s", "t", "u"} |-> IF m = "u" THEN 4..5 ELSE 2..3]
MCUpName4 == {MCOrder4}
=============================================================================
