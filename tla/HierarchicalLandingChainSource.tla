---------------- MODULE HierarchicalLandingChainSource ----------------
EXTENDS Naturals, Sequences
CONSTANT Broken
VARIABLES cursor, facts
vars == <<cursor, facts>>
\* Real land/promote command checkpoints, projected by actual Git ancestry.
ChainObserved == <<[destination |-> "parent", contains |-> {"a","b"}, claims |-> {"a","b"}],
                   [destination |-> "sprint", contains |-> {"a","b","parent"}, claims |-> {"parent"}],
                   [destination |-> "dev", contains |-> {"a","b","parent","cut"}, claims |-> {"cut"}]>>
Receipts == IF Broken THEN <<[destination |-> "dev", contains |-> {}, claims |-> {"cut"}]>> ELSE ChainObserved
Init == cursor = 0 /\ facts = [destination |-> "parent", contains |-> {}, claims |-> {}]
Replay == /\ cursor < Len(Receipts)
          /\ cursor' = cursor+1 /\ facts' = Receipts[cursor+1]
Spec == Init /\ [][Replay]_vars /\ WF_vars(Replay)
ClaimsAreAncestors == facts.claims \subseteq facts.contains
Composition == /\ "parent" \in facts.contains => {"a","b"} \subseteq facts.contains
               /\ "cut" \in facts.contains => {"a","b","parent"} \subseteq facts.contains
ReplayFinishes == <> (cursor = Len(Receipts))
======================================================================
