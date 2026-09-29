------------------ MODULE MCMultiTableBatch ------------------
\* Bounded T1 schema-2 draft. Members are already physical prefix/id keys.
EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Tables, Members, T1, T2, T3, M1, M2, ScopeA, ScopeB,
          MaxParticipants, MaxMembers, MaxPlacementDeltas,
          MaxReceiptUnits, MaxRevision, MaxSteps
NoPlace == "no-place"
P1 == "p1"
P2 == "p2"
NoGuard == "no-guard"
FieldValues == {"ready","done","deny"}

ASSUME /\ Tables={T1,T2,T3} /\ Members={M1,M2}
       /\ T1#T2 /\ T1#T3 /\ T2#T3 /\ ScopeA#ScopeB
       /\ MaxParticipants \in 2..16 /\ MaxParticipants <= Cardinality(Tables)
       /\ MaxMembers \in 1..2
       /\ MaxPlacementDeltas \in 1..6 /\ MaxReceiptUnits \in 1..16
       /\ MaxRevision \in 2..3 /\ MaxSteps >= 4

VARIABLES tableLive, tableEpoch, tableRevision, present, memberRevision, memberField,
          placement, operations, receipts, tableRefs, outcome, returned,
          attempt, step
vars == <<tableLive,tableEpoch,tableRevision,present,memberRevision,memberField,
          placement,operations,receipts,tableRefs,outcome,returned,attempt,step>>
Store == <<tableLive,tableEpoch,tableRevision,present,memberRevision,memberField,
           placement,operations,receipts,tableRefs>>

Guard(t,e,r) == [table|->t,epoch|->e,expectedRevision|->r]
TargetSet(q) == {q.tables[i].table : i \in 1..Len(q.tables)}
Target(q,t) == CHOOSE g \in {q.tables[i] : i \in 1..Len(q.tables)}:g.table=t
Place(expected,after) == [expected|->expected,after|->after]
Member(m,absent,rev,want,after,places) ==
 [member|->m,expectAbsent|->absent,expectedRevision|->rev,
  expectedStatus|->want,afterStatus|->after,places|->places]
EntryIDs(q) == {q.members[i].member : i \in 1..Len(q.members)}
Entry(q,m) == CHOOSE e \in {q.members[i] : i \in 1..Len(q.members)}:e.member=m
Key(q) == <<q.scope,q.id>>
Recorded(q) == {r \in operations:r.key=Key(q)}

TwoNew == [t \in Tables |-> IF t \in {T1,T2} THEN Place(NoPlace,P1)
                             ELSE Place(NoPlace,NoPlace)]
TwoSame == [t \in Tables |-> IF t \in {T1,T2} THEN Place(P1,P1)
                              ELSE Place(NoPlace,NoPlace)]
ThreeNew == [t \in Tables |-> Place(NoPlace,P1)]
WrongPlace == [t \in Tables |-> IF t=T1 THEN Place(NoPlace,P2)
                               ELSE Place(NoPlace,NoPlace)]

TwoPlacement ==
 [scope|->ScopeA,id|->"op-two",bytes|->"raw-t1-t2",receiptUnits|->3,
  tables|-><<Guard(T1,0,0),Guard(T2,0,0)>>,
  members|-><<Member(M1,TRUE,0,NoGuard,"done",TwoNew)>>]
ExactReplay == TwoPlacement
Reordered ==
 [TwoPlacement EXCEPT !.bytes="raw-t2-t1",
                      !.tables = <<Guard(T2,0,0),Guard(T1,0,0)>>]
PayloadChanged ==
 [TwoPlacement EXCEPT !.bytes="raw-payload-changed",
 !.members = <<Member(M1,TRUE,0,NoGuard,"ready",TwoNew)>>]
ThreeTableCreate ==
 [scope|->ScopeA,id|->"op-three",bytes|->"raw-t1-t2-t3",receiptUnits|->4,
  tables|-><<Guard(T1,0,0),Guard(T2,0,0),Guard(T3,0,0)>>,
  members|-><<Member(M2,TRUE,0,NoGuard,"ready",ThreeNew)>>]
ThirdGuardStale ==
 [ThreeTableCreate EXCEPT !.id="op-third-stale",!.bytes="raw-third-stale"]
ReceiptTooLarge ==
 [TwoPlacement EXCEPT !.id="op-receipt-limit",!.bytes="raw-receipt-limit",
                      !.receiptUnits=MaxReceiptUnits+1]
NoopTwo ==
 [scope|->ScopeA,id|->"op-noop",bytes|->"raw-noop",receiptUnits|->2,
  tables|-><<Guard(T1,0,1),Guard(T2,0,1)>>,
  members|-><<Member(M1,FALSE,1,"done","done",TwoSame)>>]
FieldGuardStale ==
 [NoopTwo EXCEPT !.id="op-field-stale",!.bytes="raw-field-stale",
 !.members = <<Member(M1,FALSE,1,"ready","ready",TwoSame)>>]
PlaceGuardStale ==
 [NoopTwo EXCEPT !.id="op-place-stale",!.bytes="raw-place-stale",
 !.members = <<Member(M1,FALSE,1,"done","done",WrongPlace)>>]
ScopeBNoop ==
 [scope|->ScopeB,id|->"op-two",bytes|->"raw-scope-b",receiptUnits|->2,
  tables|-><<Guard(T1,0,1),Guard(T2,0,1)>>,
  members|-><<Member(M1,FALSE,1,"done","done",TwoSame)>>]
ConflictMember ==
 [scope|->ScopeA,id|->"op-duplicate",bytes|->"raw-duplicate",receiptUnits|->3,
  tables|-><<Guard(T1,0,0),Guard(T2,0,0)>>,
  members|-><<Member(M1,TRUE,0,NoGuard,"ready",TwoNew),
             Member(M1,TRUE,0,NoGuard,"done",TwoNew)>>]

ParticipantShape(q) ==
 /\ 2<=Len(q.tables) /\ Len(q.tables)<=MaxParticipants
 /\ Len(q.tables)=Cardinality(TargetSet(q))
DuplicatePhysicalMember(q) == Cardinality(EntryIDs(q))#Len(q.members)
PlacementPairs(q) == EntryIDs(q) \X TargetSet(q)
PlacementDeltas(q) ==
 {p \in PlacementPairs(q):
   LET m==p[1] IN LET t==p[2] IN
     Entry(q,m).places[t].after # placement[m][t]}
MemberOK(e,m) ==
 IF e.expectAbsent THEN /\ m \notin present /\ e.expectedRevision=0
                        /\ e.expectedStatus=NoGuard
 ELSE /\ m \in present /\ memberRevision[m]=e.expectedRevision
      /\ memberField[m]=e.expectedStatus
PlaceOK(q,e,m) ==
 \A t \in TargetSet(q):e.places[t].expected=placement[m][t]
Effective(q,m) ==
 LET e==Entry(q,m) IN
   e.afterStatus#memberField[m] \/
   (\E t \in TargetSet(q):e.places[t].after#placement[m][t])
ChangedMembers(q) == {m \in EntryIDs(q):Effective(q,m)}
AggregateOK(q) ==
 /\ ParticipantShape(q) /\ Len(q.members)<=MaxMembers
 /\ Cardinality(PlacementDeltas(q))<=MaxPlacementDeltas
 /\ q.receiptUnits<=MaxReceiptUnits
ManifestOK(q) ==
 /\ ~DuplicatePhysicalMember(q)
 /\ \A t \in TargetSet(q):
      /\ tableLive[t]
      /\ Target(q,t).epoch=tableEpoch[t]
      /\ Target(q,t).expectedRevision=tableRevision[t]
      /\ tableRevision[t]<MaxRevision
 /\ \A m \in EntryIDs(q):
      LET e==Entry(q,m) IN
       /\ MemberOK(e,m) /\ PlaceOK(q,e,m)
       /\ (e.expectAbsent => \A t \in TargetSet(q):e.places[t].expected=NoPlace)
       /\ (Effective(q,m) => memberRevision[m]<MaxRevision)
FreshOK(q) == AggregateOK(q) /\ ManifestOK(q)

NewPresent(q) == present \cup {m \in ChangedMembers(q):m \notin present}
NewPlacement(q) ==
 [m \in Members |-> [t \in Tables |->
   IF m \in EntryIDs(q) /\ t \in TargetSet(q)
   THEN Entry(q,m).places[t].after ELSE placement[m][t]]]
NewField(q) == [m \in Members |->
 IF m \in EntryIDs(q) THEN Entry(q,m).afterStatus ELSE memberField[m]]
NewMemberRevision(q) == [m \in Members |->
 IF m \in ChangedMembers(q) THEN memberRevision[m]+1 ELSE memberRevision[m]]
TableDelta(q) == [t \in TargetSet(q) |->
 [beforeEpoch|->tableEpoch[t],beforeRevision|->tableRevision[t],
  afterEpoch|->tableEpoch[t],afterRevision|->tableRevision[t]+1]]
MemberDelta(q) == [m \in EntryIDs(q) |->
 [beforeRevision|->memberRevision[m],afterRevision|->NewMemberRevision(q)[m],
  beforeStatus|->memberField[m],afterStatus|->NewField(q)[m]]]
PlacementDelta(q) == [p \in PlacementDeltas(q) |->
 [before|->placement[p[1]][p[2]],after|->Entry(q,p[1]).places[p[2]].after]]
Receipt(q) == [key|->Key(q),bytes|->q.bytes,targets|->TargetSet(q),
 tableDelta|->TableDelta(q),memberDelta|->MemberDelta(q),
 placementDelta|->PlacementDelta(q)]
Record(q) == [key|->Key(q),bytes|->q.bytes,result|->Receipt(q)]
References(q) == [i \in 1..Len(q.tables) |->
 [table|->q.tables[i].table,key|->Key(q),receiptKey|->Key(q)]]

Init ==
 /\ tableLive=[t \in Tables |-> TRUE]
 /\ tableEpoch=[t \in Tables |-> 0] /\ tableRevision=[t \in Tables |-> 0]
 /\ present={} /\ memberRevision=[m \in Members |-> 0]
 /\ memberField=[m \in Members |-> "ready"]
 /\ placement=[m \in Members |-> [t \in Tables |-> NoPlace]]
 /\ operations={} /\ receipts= <<>> /\ tableRefs= <<>>
 /\ outcome="initial" /\ returned="none" /\ attempt="none" /\ step=0

Accept(q) ==
 /\ Recorded(q)={} /\ FreshOK(q)
 /\ tableRevision'=[t \in Tables |->
    IF t \in TargetSet(q) THEN tableRevision[t]+1 ELSE tableRevision[t]]
 /\ UNCHANGED <<tableLive,tableEpoch>>
 /\ present'=NewPresent(q) /\ memberRevision'=NewMemberRevision(q)
 /\ memberField'=NewField(q) /\ placement'=NewPlacement(q)
 /\ operations'=operations \cup {Record(q)}
 /\ receipts'=Append(receipts,Receipt(q))
 /\ tableRefs'=tableRefs \o References(q)
 /\ outcome'="accepted" /\ returned'=Receipt(q) /\ attempt'=q
Refuse(q,why) ==
 /\ ~FreshOK(q) \/ DuplicatePhysicalMember(q)
 /\ UNCHANGED Store
 /\ outcome'=why /\ returned'="none" /\ attempt'=q
Replay(q) ==
 /\ Recorded(q)#{} /\ \E r \in Recorded(q):r.bytes=q.bytes
 /\ UNCHANGED Store /\ outcome'="replay"
 /\ returned'=(CHOOSE r \in Recorded(q):r.bytes=q.bytes).result /\ attempt'=q
Conflict(q) ==
 /\ Recorded(q)#{} /\ \A r \in Recorded(q):r.bytes#q.bytes
 /\ UNCHANGED Store /\ outcome'="op-conflict" /\ returned'="none" /\ attempt'=q
Why(q) ==
 IF DuplicatePhysicalMember(q) THEN "duplicate-member-conflict"
 ELSE IF ~ParticipantShape(q) THEN "participant-shape"
 ELSE IF q.receiptUnits>MaxReceiptUnits THEN "receipt-bound"
 ELSE "stale-or-invalid"
Apply(q) == IF Recorded(q)#{} THEN Replay(q) \/ Conflict(q)
 ELSE IF FreshOK(q) THEN Accept(q) ELSE Refuse(q,Why(q))

\* Narrow lifecycle abstraction: Drop/Recreate model only whether a target is
\* live for a fresh manifest.  They deliberately retain the runtime-visible
\* epoch/revision/history image so recorded replay can precede live guards.
\* They do not model member/placement cleanup, independent monotonic ordinary
\* revision writes, or emitted lifecycle events.
Drop(t) ==
 /\ t \in Tables /\ tableLive[t]
 /\ tableLive'=[tableLive EXCEPT ![t]=FALSE]
 /\ UNCHANGED <<tableEpoch,tableRevision,present,memberRevision,memberField,placement,
                operations,receipts,tableRefs>>
 /\ outcome'="table-drop" /\ returned'="none" /\ attempt'="none"
Recreate(t) ==
 /\ t \in Tables /\ ~tableLive[t]
 /\ tableLive'=[tableLive EXCEPT ![t]=TRUE]
 /\ UNCHANGED <<tableEpoch,tableRevision,present,memberRevision,memberField,placement,
                operations,receipts,tableRefs>>
 /\ outcome'="table-recreate" /\ returned'="none" /\ attempt'="none"
EarlyWrite(t) ==
 /\ t \in Tables /\ tableLive[t] /\ tableRevision[t]<MaxRevision
 /\ tableRevision'=[tableRevision EXCEPT ![t]=@+1]
 /\ UNCHANGED <<tableLive,tableEpoch,present,memberRevision,memberField,placement,
                operations,receipts,tableRefs>>
 /\ outcome'="ordinary" /\ returned'="none" /\ attempt'="none"

\* Negative witnesses below intentionally model forbidden implementation
\* paths.  Their configs check the named action properties for counterexamples.
FaultEarlyWrite(q) ==
 /\ Recorded(q)={} /\ ~FreshOK(q)
 /\ tableRevision[T3]#Target(q,T3).expectedRevision
 /\ tableRevision'=[t \in Tables |-> IF t=T1 THEN tableRevision[t]+1 ELSE tableRevision[t]]
 /\ UNCHANGED <<tableLive,tableEpoch,present,memberRevision,memberField,placement,
                operations,receipts,tableRefs>>
 /\ outcome'="stale-or-invalid" /\ returned'="none" /\ attempt'=q
FaultLateThirdGuard(q) ==
 /\ Recorded(q)={} /\ ~FreshOK(q)
 /\ tableRevision[T3]#Target(q,T3).expectedRevision
 /\ tableRevision'=[t \in Tables |->
    IF t \in TargetSet(q) THEN tableRevision[t]+1 ELSE tableRevision[t]]
 /\ UNCHANGED <<tableLive,tableEpoch>>
 /\ present'=NewPresent(q) /\ memberField'=NewField(q) /\ placement'=NewPlacement(q)
 /\ memberRevision'=NewMemberRevision(q)
 /\ operations'=operations \cup {Record(q)}
 /\ receipts'=Append(receipts,Receipt(q)) /\ tableRefs'=tableRefs \o References(q)
 /\ outcome'="accepted" /\ returned'=Receipt(q) /\ attempt'=q
FaultSharedRevisionTwice(q) ==
 /\ Recorded(q)={} /\ FreshOK(q)
 /\ tableRevision'=[t \in Tables |->
    IF t \in TargetSet(q) THEN tableRevision[t]+1 ELSE tableRevision[t]]
 /\ UNCHANGED <<tableLive,tableEpoch>>
 /\ present'=NewPresent(q) /\ memberField'=NewField(q) /\ placement'=NewPlacement(q)
 /\ memberRevision'=[m \in Members |->
    IF m \in ChangedMembers(q) THEN memberRevision[m]+2 ELSE memberRevision[m]]
 /\ operations'=operations \cup {Record(q)}
 /\ receipts'=Append(receipts,Receipt(q)) /\ tableRefs'=tableRefs \o References(q)
 /\ outcome'="accepted" /\ returned'=Receipt(q) /\ attempt'=q
FaultReplayDuplicate(q) ==
 /\ Recorded(q)#{} /\ \E r \in Recorded(q):r.bytes=q.bytes
 /\ UNCHANGED <<tableLive,tableEpoch,tableRevision,present,memberRevision,memberField,
                placement,operations,tableRefs>>
 /\ receipts'=Append(receipts,(CHOOSE r \in Recorded(q):r.bytes=q.bytes).result)
 /\ outcome'="replay"
 /\ returned'=(CHOOSE r \in Recorded(q):r.bytes=q.bytes).result /\ attempt'=q

TypeOK ==
 /\ tableLive \in [Tables -> BOOLEAN]
 /\ tableEpoch \in [Tables -> Nat] /\ tableRevision \in [Tables -> 0..MaxRevision]
 /\ present \subseteq Members /\ memberRevision \in [Members -> 0..MaxRevision]
 /\ memberField \in [Members -> FieldValues]
 /\ placement \in [Members -> [Tables -> {NoPlace,P1,P2}]]
 /\ \A i \in 1..Len(tableRefs):tableRefs[i].key=tableRefs[i].receiptKey
 /\ step \in 0..MaxSteps
AcceptedPrestateGuards ==
 [][outcome'="accepted" => FreshOK(attempt')]_vars
RefusalFullImageUnchanged ==
 [][outcome' \in {"participant-shape","receipt-bound",
 "duplicate-member-conflict","stale-or-invalid","op-conflict","replay"}
 => UNCHANGED Store]_vars
EveryTargetAdvancedOnce ==
 [][outcome'="accepted" =>
 /\ \A t \in TargetSet(attempt'):tableRevision'[t]=tableRevision[t]+1
 /\ \A t \in Tables \ TargetSet(attempt'):tableRevision'[t]=tableRevision[t]]_vars
SharedMemberRevisionSemantics ==
 [][outcome'="accepted" => \A m \in Members:
 memberRevision'[m]=memberRevision[m]+
  IF m \in ChangedMembers(attempt') THEN 1 ELSE 0]_vars
OneAggregateReceipt ==
 [][outcome'="accepted" =>
 /\ Len(receipts')=Len(receipts)+1
 /\ receipts'[Len(receipts')]=Receipt(attempt')
 /\ Len(tableRefs')=Len(tableRefs)+Len(attempt'.tables)]_vars
ReplayIsIdentity ==
 [][outcome'="replay" => /\ UNCHANGED Store
 /\ \E r \in operations:r.bytes=attempt'.bytes /\ returned'=r.result]_vars
NoopKeepsMemberRevision ==
 [][outcome'="accepted" /\ attempt'=NoopTwo =>
 memberRevision'[M1]=memberRevision[M1]]_vars
ReceiptComplete ==
 \A i \in 1..Len(receipts):
 /\ DOMAIN receipts[i].tableDelta=receipts[i].targets
 /\ \A m \in DOMAIN receipts[i].memberDelta:
  receipts[i].memberDelta[m].afterRevision>=receipts[i].memberDelta[m].beforeRevision

PositiveNext ==
 \/ /\ step=0 /\ Apply(ThreeTableCreate) /\ step'=1
 \/ /\ step=1 /\ Apply(ThreeTableCreate) /\ step'=2
PositiveSpec == Init /\ [][PositiveNext \/ (step=2 /\ UNCHANGED vars)]_vars /\ WF_vars(PositiveNext)
ThreeTableEventuallyAccepted == <>(outcome="accepted" /\ attempt=ThreeTableCreate)
ExactReplayEventually == <>(outcome="replay" /\ attempt=ThreeTableCreate)
TwoPlacementNext == /\ step=0 /\ Apply(TwoPlacement) /\ step'=1
TwoPlacementSpec == Init /\ [][TwoPlacementNext \/ (step=1 /\ UNCHANGED vars)]_vars /\ WF_vars(TwoPlacementNext)
TwoPlacementsOneMemberEventually ==
 <>(outcome="accepted" /\ attempt=TwoPlacement /\ memberRevision[M1]=1 /\
    placement[M1][T1]=P1 /\ placement[M1][T2]=P1)
NoopNext ==
 \/ /\ step=0 /\ Apply(TwoPlacement) /\ step'=1
 \/ /\ step=1 /\ Apply(NoopTwo) /\ step'=2
NoopSpec == Init /\ [][NoopNext \/ (step=2 /\ UNCHANGED vars)]_vars /\ WF_vars(NoopNext)
NoopEventuallyAccepted == <>(outcome="accepted" /\ attempt=NoopTwo)
LastGuardNext ==
 \/ /\ step=0 /\ EarlyWrite(T3) /\ step'=1
 \/ /\ step=1 /\ Apply(ThirdGuardStale) /\ step'=2
LastGuardSpec == Init /\ [][LastGuardNext \/ (step=2 /\ UNCHANGED vars)]_vars /\ WF_vars(LastGuardNext)
LastTableGuardEventuallyRefused == <>(outcome="stale-or-invalid" /\ attempt=ThirdGuardStale)
ReplayAfterDropNext ==
 \/ /\ step=0 /\ Apply(TwoPlacement) /\ step'=1
 \/ /\ step=1 /\ Drop(T2) /\ step'=2
 \/ /\ step=2 /\ Apply(ExactReplay) /\ step'=3
ReplayAfterDropSpec == Init /\ [][ReplayAfterDropNext \/ (step=3 /\ UNCHANGED vars)]_vars /\ WF_vars(ReplayAfterDropNext)
ReplayAfterDropEventually == <>(outcome="replay" /\ attempt=ExactReplay)
AggregateNext == /\ step=0 /\ Apply(ReceiptTooLarge) /\ step'=1
AggregateSpec == Init /\ [][AggregateNext \/ (step=1 /\ UNCHANGED vars)]_vars /\ WF_vars(AggregateNext)
ReceiptBoundEventuallyRefused == <>(outcome="receipt-bound" /\ attempt=ReceiptTooLarge)
FieldGuardNext ==
 \/ /\ step=0 /\ Apply(TwoPlacement) /\ step'=1
 \/ /\ step=1 /\ Apply(FieldGuardStale) /\ step'=2
FieldGuardSpec == Init /\ [][FieldGuardNext \/ (step=2 /\ UNCHANGED vars)]_vars /\ WF_vars(FieldGuardNext)
FieldGuardEventuallyRefused == <>(outcome="stale-or-invalid" /\ attempt=FieldGuardStale)
PlaceGuardNext ==
 \/ /\ step=0 /\ Apply(TwoPlacement) /\ step'=1
 \/ /\ step=1 /\ Apply(PlaceGuardStale) /\ step'=2
PlaceGuardSpec == Init /\ [][PlaceGuardNext \/ (step=2 /\ UNCHANGED vars)]_vars /\ WF_vars(PlaceGuardNext)
PlaceGuardEventuallyRefused == <>(outcome="stale-or-invalid" /\ attempt=PlaceGuardStale)
ScopeIsolationNext ==
 \/ /\ step=0 /\ Apply(TwoPlacement) /\ step'=1
 \/ /\ step=1 /\ Apply(ScopeBNoop) /\ step'=2
ScopeIsolationSpec == Init /\ [][ScopeIsolationNext \/ (step=2 /\ UNCHANGED vars)]_vars /\ WF_vars(ScopeIsolationNext)
SameIDOtherScopeEventuallyAccepted ==
 <>(outcome="accepted" /\ attempt=ScopeBNoop /\ Cardinality(operations)=2)
ReorderedNext ==
 \/ /\ step=0 /\ Apply(TwoPlacement) /\ step'=1
 \/ /\ step=1 /\ Apply(Reordered) /\ step'=2
ReorderedSpec == Init /\ [][ReorderedNext \/ (step=2 /\ UNCHANGED vars)]_vars /\ WF_vars(ReorderedNext)
ReorderedEventuallyConflicts == <>(outcome="op-conflict" /\ attempt=Reordered)
PayloadConflictNext ==
 \/ /\ step=0 /\ Apply(TwoPlacement) /\ step'=1
 \/ /\ step=1 /\ Apply(PayloadChanged) /\ step'=2
PayloadConflictSpec == Init /\ [][PayloadConflictNext \/ (step=2 /\ UNCHANGED vars)]_vars /\ WF_vars(PayloadConflictNext)
PayloadChangedEventuallyConflicts == <>(outcome="op-conflict" /\ attempt=PayloadChanged)
ConflictNext == /\ step=0 /\ Apply(ConflictMember) /\ step'=1
ConflictSpec == Init /\ [][ConflictNext \/ (step=1 /\ UNCHANGED vars)]_vars /\ WF_vars(ConflictNext)
DuplicateMemberEventuallyRefused ==
 <>(outcome="duplicate-member-conflict" /\ attempt=ConflictMember)

\* These four specs are expected to violate the property selected by their
\* respective MCMultiBatchFault*.cfg file.
FaultEarlyWriteNext ==
 \/ /\ step=0 /\ EarlyWrite(T3) /\ step'=1
 \/ /\ step=1 /\ FaultEarlyWrite(ThirdGuardStale) /\ step'=2
FaultEarlyWriteSpec == Init /\ [][FaultEarlyWriteNext \/ (step=2 /\ UNCHANGED vars)]_vars
FaultLateThirdGuardNext ==
 \/ /\ step=0 /\ EarlyWrite(T3) /\ step'=1
 \/ /\ step=1 /\ FaultLateThirdGuard(ThirdGuardStale) /\ step'=2
FaultLateThirdGuardSpec == Init /\ [][FaultLateThirdGuardNext \/ (step=2 /\ UNCHANGED vars)]_vars
FaultSharedRevisionTwiceNext == /\ step=0 /\ FaultSharedRevisionTwice(TwoPlacement) /\ step'=1
FaultSharedRevisionTwiceSpec == Init /\ [][FaultSharedRevisionTwiceNext \/ (step=1 /\ UNCHANGED vars)]_vars
FaultReplayDuplicateNext ==
 \/ /\ step=0 /\ Apply(TwoPlacement) /\ step'=1
 \/ /\ step=1 /\ FaultReplayDuplicate(ExactReplay) /\ step'=2
FaultReplayDuplicateSpec == Init /\ [][FaultReplayDuplicateNext \/ (step=2 /\ UNCHANGED vars)]_vars
=============================================================
