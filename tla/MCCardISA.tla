--------------------------- MODULE MCCardISA ---------------------------
\* The TLC instance of CardISA. Constants that are functions cannot be
\* written in the .cfg; they are defined here and substituted (the pattern
\* MCCardMachine uses).
EXTENDS CardISA

\* c1 is a wait card behind the coordinator's release; c2 is a think card
\* that waits for c1 to land (today's DEPENDS-ON, the operand a card id).
MCCards     == {"c1", "c2"}
MCConsumers == {"k1"}
MCSlots     == [k \in MCConsumers |-> 1]
MCDeps      == [c \in MCCards |-> {}]
MCKind      == [c \in MCCards |-> IF c = "c1" THEN "wait" ELSE "think"]
MCWaitFor   == [c \in MCCards |-> IF c = "c1" THEN "release" ELSE "c1"]
MCMaxBase   == 2
\* the PR head is unbounded; TLC explores up to MCMaxHead
MCMaxHead   == 1
MCCardISAHeadBound == HeadBound(MCMaxHead)

\* the liveness instance: one wait card behind the coordinator's release
MCCardsLive     == {"c1"}
MCConsumersLive == {"k1"}
MCSlotsLive     == [k \in MCConsumersLive |-> 1]
MCDepsLive      == [c \in MCCardsLive |-> {}]
MCKindLive      == [c \in MCCardsLive |-> "wait"]
MCWaitForLive   == [c \in MCCardsLive |-> "release"]
MCMaxHeadLive   == 1
MCCardISAHeadBoundLive == HeadBound(MCMaxHeadLive)
========================================================================
