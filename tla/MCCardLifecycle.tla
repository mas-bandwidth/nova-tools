------------------------- MODULE MCCardLifecycle -------------------------
EXTENDS CardLifecycle

MCCards == {"c1", "c2", "c3"}

\* c3 waits on c1
MCNeeds == [c \in MCCards |-> CASE c = "c3" -> {"c1"} [] OTHER -> {}]

MCWaived == [c \in MCCards |-> {}]

\* c1 is replaced by its twin c2
MCTwin == [c \in MCCards |-> CASE c = "c1" -> "c2" [] OTHER -> "none"]

MCBroken == {}
MCLanded == {"deallanded"}
MCTwoColumns == {"twocolumn"}
MCOpenNeed == {"openneed"}
MCNoTwin == {"notwin"}
=============================================================================
