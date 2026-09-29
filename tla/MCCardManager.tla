---------------------------- MODULE MCCardManager ----------------------------
\* The TLC instance: four card ids over two streams. c1 is code, c2 is a
\* non-PR kind (a read), c3 is code and depends on both, so it is met only
\* by c1 landed and c2 done/completed; c4 is c3's revised definition under
\* a fresh id (same dependencies), the id a replacement can take. Two readers, a quorum of two,
\* two heads, batches of one or two entries. Function constants are
\* defined here and substituted in the .cfg.
EXTENDS CardManager, Sequences
MCCards     == {"c1", "c2", "c3", "c4"}
MCStreams   == {"s1", "s2"}
MCStream    == [c \in MCCards |-> IF c \in {"c1", "c3"} THEN "s1" ELSE "s2"]
MCKind      == [c \in MCCards |-> IF c = "c2" THEN "read" ELSE "code"]
MCNonPR     == {"read"}
MCDeps      == [c \in MCCards |-> IF c \in {"c3", "c4"} THEN {"c1", "c2"} ELSE {}]
MCDef       == [c \in MCCards |-> "sha-" \o c]
MCReviewers == {"r1", "r2"}
=============================================================================
