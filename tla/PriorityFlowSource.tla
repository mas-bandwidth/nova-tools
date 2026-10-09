------------------------- MODULE PriorityFlowSource -------------------------
EXTENDS PriorityFlow
\* Emitted from TickDeal's fleet-create receipts; TestPriorityFlowSourceBridge
\* reruns the source fixture and refuses a drift of this observation.
SourceObserved == <<"f", "b", "c", "h", "r", "n", "l">>
SourceAdmissions == IF Broken = "reverse" THEN <<"l", "n", "r", "h", "f", "c", "b">> ELSE IF Broken = "blocked" THEN Append(SourceObserved,"f") ELSE SourceObserved
ReplayNext == \E o \in Owners :
    /\ Len(admissions) < Len(SourceAdmissions)
    /\ AdmitObserved(SourceAdmissions[Len(admissions)+1],o,FALSE)
ReplaySpec == Init /\ [][ReplayNext]_vars /\ WF_vars(ReplayNext)
ReplayComplete == Len(admissions) = Len(SourceAdmissions) => admissions = SourceAdmissions
ReplayFinishes == <> (Len(admissions) = Len(SourceAdmissions))
=============================================================================
