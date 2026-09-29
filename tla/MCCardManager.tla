---------------------------- MODULE MCCardManager ----------------------------
\* The TLC instances. Function constants are defined here and substituted
\* in the .cfg files.
\*
\* The named instance (MCCardManager.cfg and the witnesses): three card ids
\* over two streams; c1 is code, c2 a non-PR kind (a read), c3 code and
\* depending on both, so it is met only by c1 landed and c2 done/completed.
\* Two epochs, two heads, two readers with a quorum of two, batches of one
\* to three entries.
\*
\* The four-card instance (MCCardManagerFour.cfg): c4 is c3's revised
\* definition under a fresh id with the same dependencies, the id a
\* replacement takes while c1, c2 and c3 are all admitted; one epoch, one
\* head, batches of one or two.
\*
\* Every config keeps TypeOK: the VIEW leaves the counters out, and TypeOK
\* is what makes TLC evaluate them before a state is written to its disk
\* queue (without it TLC 2.19 fails writing a lazily built function).
EXTENDS CardManager, Sequences
MCCards      == {"c1", "c2", "c3"}
MCCardsFour  == {"c1", "c2", "c3", "c4"}
MCStreams    == {"s1", "s2"}
MCStream     == [c \in MCCardsFour |-> IF c \in {"c1", "c3"} THEN "s1" ELSE "s2"]
MCKind       == [c \in MCCardsFour |-> IF c = "c2" THEN "read" ELSE "code"]
MCNonPR      == {"read"}
MCDeps       == [c \in MCCardsFour |-> IF c \in {"c3", "c4"} THEN {"c1", "c2"} ELSE {}]
MCDef        == [c \in MCCardsFour |-> "sha-" \o c]
MCReviewers  == {"r1", "r2"}
\* The observations really made. c1: at head 1 both readers accept and CI
\* is green, and the merge queue rejects the head; at head 2 both accept
\* and CI runs green, then red on a second run. c2 (no CI): at head 1 both
\* accept, then r2 changes its mind and rejects; at head 2 both accept.
\* c3 and c4: both accept and CI is green at head 1.
MCSeen == [c \in MCCardsFour |->
  CASE c = "c1" -> {<<"r1", "accept", 1>>, <<"r2", "accept", 1>>, <<"ci", "ok", 1>>,
                    <<"queue", "reject", 1>>,
                    <<"r1", "accept", 2>>, <<"r2", "accept", 2>>,
                    <<"ci", "ok", 2>>, <<"ci", "red", 2>>}
    [] c = "c2" -> {<<"r1", "accept", 1>>, <<"r2", "accept", 1>>, <<"r2", "reject", 1>>,
                    <<"r1", "accept", 2>>, <<"r2", "accept", 2>>}
    [] OTHER    -> {<<"r1", "accept", 1>>, <<"r2", "accept", 1>>, <<"ci", "ok", 1>>}]
MCObs == [c \in MCCardsFour |-> {<<o[1], o[2], o[3], MCDef[c]>> : o \in MCSeen[c]}]
=============================================================================
