------------------------------ MODULE ReaderTiers ------------------------------
\* No existing model covers this ask. ReadsByRoom places a read on a reader
\* with room and has no tiers. DirtyTick models a reader up, away and a
\* returned read asked again in place, and has no tiers either. This module is
\* the small model of the rule those two do not state.
\*
\* A reader whose tier set is empty reads every tier. Ask places a card only
\* on a reader who reads the card's tier, and only when enough such readers
\* exist (one for flash, two for pro). The invariant: no read is ever asked
\* of a reader outside the primary's tier.
\*
\* The instance is the module. It is not a cfg in the TLC case list: a cfg
\* with no bench record fails the record gate, and this check is a small TLC
\* run of the module itself.

EXTENDS Naturals, FiniteSets

Readers == {"rf", "ra1", "ra2"}
Cards == {"cf", "cp"}

tierOf == [cf |-> "flash", cp |-> "pro"]
\* rf reads flash only. An empty set is every tier.
readerTiers == [rf |-> {"flash"}, ra1 |-> {}, ra2 |-> {}]
AllTiers == {"flash", "pro"}

Need(c) == IF tierOf[c] = "flash" THEN 1 ELSE 2
Reads(r) == IF readerTiers[r] = {} THEN AllTiers ELSE readerTiers[r]

VARIABLE asked

Init == asked = [r \in Readers |-> {}]

Eligible(c) == {r \in Readers : tierOf[c] \in Reads(r) /\ c \notin asked[r]}

Ask(c) ==
  /\ Cardinality(Eligible(c)) >= Need(c)
  /\ \E r \in Eligible(c) :
       asked' = [asked EXCEPT ![r] = @ \cup {c}]

\* Too few readers of the card's tier: the few-readers refusal. Nothing is asked.
Refuse(c) ==
  /\ Cardinality(Eligible(c)) < Need(c)
  /\ UNCHANGED asked

Next == \E c \in Cards : Ask(c) \/ Refuse(c)

Spec == Init /\ [][Next]_asked

NoReadOutsideTier ==
  \A r \in Readers : \A c \in asked[r] : tierOf[c] \in Reads(r)

=============================================================================
