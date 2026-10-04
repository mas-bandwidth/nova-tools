---------------------------- MODULE DecideShadow ----------------------------
EXTENDS Naturals, FiniteSets
CONSTANTS Items, Budget, Identity, BreakAlias, BreakBudget, BreakTruth, BreakSend
VARIABLES reserved, sent, responses, recorded, labelled, calls, requestLabels
vars == <<reserved, sent, responses, recorded, labelled, calls, requestLabels>>
Init == /\ reserved = {} /\ sent = {} /\ responses = {}
        /\ recorded = {} /\ labelled = {} /\ calls = [i \in Items |-> 0]
        /\ requestLabels = [i \in Items |-> FALSE]
Reserve(i) == /\ i \notin reserved /\ (BreakBudget \/ Cardinality(reserved) < Budget)
              /\ (BreakAlias \/ (\A j \in reserved: Identity[j] # Identity[i]))
              /\ reserved' = reserved \cup {i}
              /\ UNCHANGED <<sent, responses, recorded, labelled, calls, requestLabels>>
Send(i) == /\ i \in reserved /\ (BreakSend \/ i \notin sent)
           /\ sent' = sent \cup {i} /\ calls' = [calls EXCEPT ![i] = @ + 1]
           /\ requestLabels' = [requestLabels EXCEPT ![i] = (i \in labelled)]
           /\ UNCHANGED <<reserved, responses, recorded, labelled>>
Response(i) == /\ i \in sent /\ responses' = responses \cup {i}
               /\ UNCHANGED <<reserved, sent, recorded, labelled, calls, requestLabels>>
Record(i) == /\ i \in responses /\ recorded' = recorded \cup {i}
             /\ UNCHANGED <<reserved, sent, responses, labelled, calls, requestLabels>>
Label(i) == /\ (BreakTruth \/ i \in recorded) /\ labelled' = labelled \cup {i}
            /\ UNCHANGED <<reserved, sent, responses, recorded, calls, requestLabels>>
\* Restart does not release a reservation or repeat a possibly sent request.
Restart == UNCHANGED vars
Next == (\E i \in Items: Reserve(i) \/ Send(i) \/ Response(i) \/ Record(i) \/ Label(i)) \/ Restart
Spec == Init /\ [][Next]_vars
NoLabelsInRequest == \A i \in Items: ~requestLabels[i]
\* Items are operation IDs; Identity is task/full-head/prompt-version.
NoDuplicateCalls == \A i \in Items: calls[i] <= 1
                   /\ \A i, j \in Items: (i # j /\ Identity[i] = Identity[j]) => (calls[i] = 0 \/ calls[j] = 0)
ReservationBudget == Cardinality(reserved) <= Budget
TruthAfterDecision == labelled \subseteq recorded /\ recorded \subseteq responses
=============================================================================
