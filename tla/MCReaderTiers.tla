---------------------------- MODULE MCReaderTiers ----------------------------
\* The instances of ReaderTiers.tla (tla/CASES.tsv, group readertiers): three
\* readers, "fast" reading flash only and a and b every tier, two tiers, one pro
\* card that needs two different readers. The fast reader's tiers change (reader set), so
\* a pro read may stand on it from before the change, and a or b going down
\* leaves the card with one pro reader up: the few-readers judgment.
EXTENDS ReaderTiers

MCReaders == {"fast", "a", "b"}
MCTiers == {"flash", "pro"}
MCCards == {"p1"}
MCTier == [c \in MCCards |-> "pro"]
MCNeed == [c \in MCCards |-> 2]
MCInitTiers == [r \in MCReaders |-> IF r = "fast" THEN {"flash"} ELSE MCTiers]
=============================================================================
