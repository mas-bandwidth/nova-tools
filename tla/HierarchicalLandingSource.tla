-------------------- MODULE HierarchicalLandingSource --------------------
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES cursor, facts
vars == <<cursor, facts>>
\* Projection of actual bare-origin Git proof and promoter marker/store writes.
\* All observations bypass any idealized Record guard.
SourceObserved == <<[dev |-> FALSE, marker |-> FALSE, store |-> FALSE, opens |-> 1, recovered |-> FALSE],
                    [dev |-> TRUE, marker |-> TRUE, store |-> TRUE, opens |-> 1, recovered |-> FALSE],
                    [dev |-> TRUE, marker |-> TRUE, store |-> TRUE, opens |-> 1, recovered |-> TRUE]>>
Receipts == IF Broken = "ancestry" THEN <<[dev |-> FALSE, marker |-> TRUE, store |-> TRUE, opens |-> 1, recovered |-> FALSE],
                    [dev |-> TRUE, marker |-> TRUE, store |-> TRUE, opens |-> 1, recovered |-> TRUE]>> ELSE IF Broken = "duplicate" THEN <<[dev |-> TRUE, marker |-> TRUE, store |-> TRUE, opens |-> 2, recovered |-> TRUE]>> ELSE SourceObserved
Init == cursor = 0 /\ facts = [dev |-> FALSE, marker |-> FALSE, store |-> FALSE, opens |-> 1, recovered |-> FALSE]
Replay == /\ cursor < Len(Receipts)
          /\ cursor' = cursor + 1 /\ facts' = Receipts[cursor+1]
Spec == Init /\ [][Replay]_vars /\ WF_vars(Replay)
TruthfulSource == (facts.marker \/ facts.store) => facts.dev
OnePR == facts.opens <= 1
ReplayFinishes == <> (cursor = Len(Receipts))
=============================================================================
