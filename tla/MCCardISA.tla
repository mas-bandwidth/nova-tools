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
\* no external operand in this instance; MCCardISAExt has two
MCExt       == {}
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
MCExtLive       == {}
MCMaxHeadLive   == 1
MCCardISAHeadBoundLive == HeadBound(MCMaxHeadLive)

\* the external instance (layer 3): two wait cards on two external operands,
\* c1 on e1 (a pr merged) and c2 on e2 (a branch contains a sha), so a
\* cache read under the wrong operand ("onekey") releases one on the other's
\* answer
MCCardsExt     == {"c1", "c2"}
MCConsumersExt == {"k1"}
MCSlotsExt     == [k \in MCConsumersExt |-> 1]
MCDepsExt      == [c \in MCCardsExt |-> {}]
MCKindExt      == [c \in MCCardsExt |-> "wait"]
MCExtExt       == {"e1", "e2"}
MCWaitForExt   == [c \in MCCardsExt |-> IF c = "c1" THEN "e1" ELSE "e2"]

\* the external liveness instance: one wait card on one external operand
MCCardsExtLive   == {"c1"}
MCKindExtLive    == [c \in MCCardsExtLive |-> "wait"]
MCExtExtLive     == {"e1"}
MCWaitForExtLive == [c \in MCCardsExtLive |-> "e1"]
MCDepsExtLive    == [c \in MCCardsExtLive |-> {}]
========================================================================
