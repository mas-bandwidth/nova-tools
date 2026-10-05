---------------------------- MODULE MCReadsByRoom ----------------------------
\* The instances of ReadsByRoom.tla (tla/CASES.tsv, group readsbyroom): two
\* readers on machines of widths 2 and 1, two pro cards (two reads each), two
\* attempts a card: the narrow reader holds one read, so the cards contend for
\* its lane and a finder may find itself without room.
EXTENDS ReadsByRoom

MCReaders == {"r1", "r2"}
MCWidth == [r \in MCReaders |-> IF r = "r1" THEN 2 ELSE 1]
MCCards == {"p1", "p2"}
MCNeed == [c \in MCCards |-> 2]
=============================================================================
