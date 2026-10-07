------------------------- MODULE MCCardMachine -------------------------
\* The TLC instance: two cards (c2 depends on c1), two consumers of one
\* slot each, three copies per card. Constants that are functions cannot
\* be written in the .cfg; they are defined here and substituted.
EXTENDS CardMachine
MCCards     == {"c1", "c2"}
MCConsumers == {"k1", "k2"}
MCSlots     == [k \in MCConsumers |-> 1]
MCDeps      == [c \in MCCards |-> IF c = "c2" THEN {"c1"} ELSE {}]
MCHeadBound == HeadBound(2)
=======================================================================
