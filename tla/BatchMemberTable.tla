------------------------ MODULE BatchMemberTable ------------------------
\* Proposed atomic batch protocol over EpochMemberTable's owned cells, place
\* record, physical table identity and active epoch. The byte strings below
\* abstract canonical encoded requests: equality means exact byte equality.
EXTENDS EpochMemberTable, Sequences
CONSTANTS BatchTable, MoveMember, CreateMember, GuardMember,
          FromCell, MoveCell, CreateCell, GuardCell, BatchActor
VARIABLES present, memberRevision, memberFields, tableRevision,
          operations, receipts, outcome, returned, attempt, cellType, permitted
bvars == <<xvars,present,memberRevision,memberFields,tableRevision,
           operations,receipts,outcome,returned,attempt,cellType,permitted>>
FieldNames == {"status","token"}
\* A reserved abstract value, distinct from every supported field string.
NoValue == "absent-sentinel"
Values == {"","ready","done","permit","new","deny"} \cup {NoValue}
NoField == "none"
MaxRevision == 3
MoveEntry == [id |-> MoveMember, absent |-> FALSE, revision |-> 0,
              source |-> FromCell, target |-> MoveCell,
              guardField |-> "status", guardKind |-> "equals", guardValues |-> {"ready"},
              setField |-> "status", setValue |-> "done", score |-> 1, change |-> TRUE]
CreateEntry == [id |-> CreateMember, absent |-> TRUE, revision |-> 0,
                source |-> NoPlace, target |-> CreateCell,
                guardField |-> NoField, guardKind |-> "none", guardValues |-> {},
                setField |-> "status", setValue |-> "new", score |-> 1, change |-> TRUE]
GuardEntry == [id |-> GuardMember, absent |-> FALSE, revision |-> 0,
               source |-> GuardCell, target |-> GuardCell,
               guardField |-> "token", guardKind |-> "equals", guardValues |-> {"permit"},
               setField |-> NoField, setValue |-> NoValue, score |-> 2, change |-> FALSE]
AllEntries == <<MoveEntry,CreateEntry,GuardEntry>>
BatchEntries(n) == [i \in 1..n |-> AllEntries[i]]
Bytes(n) == <<"canonical-one","canonical-two","canonical-three">>[n]
Request(n,id,bytes,digest,rev,epoch) ==
 [table |-> BatchTable, epoch |-> epoch, revision |-> rev,
  id |-> id, actor |-> BatchActor, members |-> BatchEntries(n),
  bytes |-> bytes, digest |-> digest]
Normal(n) == Request(n,"op-main",Bytes(n),"digest-main",0,1)
Collision(n) == [Normal(n) EXCEPT !.members[1].guardValues={"done"},
                              !.bytes="different-canonical-bytes"]
BadPostGuard == [Normal(1) EXCEPT !.members[1].guardValues={"done"},
                                   !.bytes="canonical-postguard"]
BadMemberRevision == [Normal(1) EXCEPT !.revision=1,
                                        !.members[1].guardValues={"done"},
                                        !.bytes="canonical-member-revision"]
BadMoveRevision == [Normal(1) EXCEPT !.revision=1,
                                     !.members[1].target=FromCell,
                                     !.bytes="canonical-move-revision"]
Noop == Request(1,"op-noop","canonical-noop","digest-noop",0,1)
NoopEntry == [GuardEntry EXCEPT !.guardKind="one_of", !.guardValues={"permit","ready"}]
NoopRequest == [Noop EXCEPT !.members= <<NoopEntry>>]
InteractingRequest ==
 [Request(3,"op-interact","canonical-interacting","digest-interacting",0,1)
  EXCEPT !.members[2].target=FromCell,
         !.members[3].target=CreateCell, !.members[3].change=TRUE,
         !.members[3].setField="token", !.members[3].setValue=NoValue]
DuplicateRequest ==
 [Request(2,"op-duplicate","canonical-duplicate","digest-duplicate",0,1)
  EXCEPT !.members[2]=MoveEntry]
AbsentEntry == [GuardEntry EXCEPT !.guardField="status", !.guardKind="absent",
                                  !.guardValues={}]
AbsentRequest == [Request(1,"op-absent","canonical-absent","digest-absent",0,1)
                 EXCEPT !.members= <<AbsentEntry>>]
EmptyEntry == [GuardEntry EXCEPT !.revision=1, !.guardField="status",
                               !.guardKind="equals", !.guardValues={""}]
EmptyRequest == [Request(1,"op-empty","canonical-empty","digest-empty",1,1)
                EXCEPT !.members= <<EmptyEntry>>]
SecondTable == <<BatchTable[1],2>>
SecondCell == <<SecondTable,FromCell[2],FromCell[3]>>
SecondCreateEntry == [CreateEntry EXCEPT !.id=GuardMember, !.target=SecondCell]
SecondRequest ==
 [Request(1,"op-epoch-two","canonical-epoch-two","digest-epoch-two",1,2)
  EXCEPT !.table=SecondTable, !.members= <<SecondCreateEntry>>]
Key(q) == <<q.table,q.epoch,q.id>>
Recorded(q) == {r \in operations:r.key=Key(q)}
Record(q,rc) == [key |-> Key(q), bytes |-> q.bytes,
                 digest |-> q.digest, result |-> rc]
EntryIDs(q) == {q.members[i].id:i \in 1..Len(q.members)}
Changed(q) == {q.members[i].id:i \in {j \in 1..Len(q.members):q.members[j].change}}
Entry(q,m) == CHOOSE e \in {q.members[i]:i \in 1..Len(q.members)}:e.id=m
PlacementOK(q,e) ==
 IF e.absent THEN e.id \notin present /\ place[e.id][q.table]=NoPlace
 ELSE e.id \in present /\ place[e.id][q.table]=e.source
GuardOKIn(fs,e) ==
 IF e.guardKind="none" THEN e.guardField=NoField
 ELSE IF e.guardKind="absent" THEN fs[e.id][e.guardField]=NoValue
 ELSE IF e.guardKind="equals" THEN
      Cardinality(e.guardValues)=1 /\ fs[e.id][e.guardField] \in e.guardValues
 ELSE e.guardKind="one_of" /\ e.guardValues#{} /\
      fs[e.id][e.guardField] \in e.guardValues
GuardOK(e) == GuardOKIn(memberFields,e)
EntryOK(q,e) ==
 /\ PlacementOK(q,e)
 /\ (e.change => MemberEpoch[e.id]=q.epoch)
 /\ (e.absent \/ memberRevision[e.id]=e.revision)
 /\ GuardOK(e)
 /\ (e.absent => e.score \in Scores)
 /\ (e.change /\ ~e.absent =>
      cellType[e.source]="zset" /\ permitted[e.source])
 /\ (e.change => e.target \in Owned(live,rows,binds))
 /\ (e.change => cellType[e.target]="zset" /\ permitted[e.target])
 /\ (e.change => memberRevision[e.id]<MaxRevision)
ManifestOK(q) ==
 /\ 1<=Len(q.members) /\ Len(q.members)<=3
 /\ Cardinality(EntryIDs(q))=Len(q.members)
 /\ q.table \in Tables
 /\ RecordSetLink /\ NoHiddenOwned
 /\ \A i \in 1..Len(q.members):EntryOK(q,q.members[i])
 /\ tableRevision[q.table]<MaxRevision
FreshOK(q) ==
 /\ q.epoch=activeEpoch /\ q.table[2]=activeEpoch
 /\ q.revision=tableRevision[q.table]
 /\ ManifestOK(q)
\* All expressions below read the same unprimed state. No entry can observe
\* another entry's writes. Move preserves the source score.
ScoreAt(e) == CHOOSE s \in Scores:<<e.source,e.id,s>> \in data
NewPlace(q) == [m \in Members |-> [place[m] EXCEPT
    ![q.table]=IF m \in Changed(q) THEN Entry(q,m).target ELSE @]]
NewData(q) ==
 {e \in data:~(e[2] \in Changed(q) /\
                  e[1] \in (Owned(live,rows,binds) \cap TableCells(q.table)))} \cup
 {<<Entry(q,m).target,m,
    IF Entry(q,m).absent THEN Entry(q,m).score ELSE ScoreAt(Entry(q,m))>>:m \in Changed(q)}
NewFields(q) == [m \in Members |->
 IF m \in Changed(q) /\ Entry(q,m).setField # NoField
 THEN [memberFields[m] EXCEPT ![Entry(q,m).setField]=Entry(q,m).setValue]
 ELSE memberFields[m]]
NewMemberRevision(q) == [m \in Members |->
 IF m \in Changed(q) THEN memberRevision[m]+1 ELSE memberRevision[m]]
NewPresent(q) == present \cup Changed(q)
Delta(q) == [m \in EntryIDs(q) |->
 [beforePlace |-> place[m][q.table], afterPlace |-> NewPlace(q)[m][q.table],
  beforeRevision |-> memberRevision[m], afterRevision |-> NewMemberRevision(q)[m],
  beforeFields |-> memberFields[m], afterFields |-> NewFields(q)[m]]]
Receipt(q) ==
 [key |-> Key(q), bytes |-> q.bytes, digest |-> q.digest,
  actor |-> q.actor, beforeRevision |-> tableRevision[q.table],
  afterRevision |-> tableRevision[q.table]+1,
  changed |-> Changed(q), selected |-> EntryIDs(q), delta |-> Delta(q),
  changedCount |-> Cardinality(Changed(q)),
  guardCount |-> Cardinality(EntryIDs(q) \ Changed(q)),
  selectedCount |-> Cardinality(EntryIDs(q)),
  kind |-> IF Changed(q)={} THEN "noop" ELSE "changed"]
BatchInit ==
 /\ EpochInit
 /\ present={m \in Members: \E e \in Seed:e[2]=m /\ e[1]#External}
 /\ memberRevision=[m \in Members |-> 0]
 /\ memberFields=[m \in Members |->
      [f \in FieldNames |->
       IF m=MoveMember /\ f="status" THEN "ready"
       ELSE IF m=GuardMember /\ f="token" THEN "permit" ELSE NoValue]]
 /\ tableRevision=[t \in Tables |-> 0]
 /\ operations={} /\ receipts= <<>> /\ outcome="initial"
 /\ returned="none" /\ attempt="none"
 /\ cellType=[c \in Cells |-> "zset"]
 /\ permitted=[c \in Cells |-> TRUE]
BatchTypeOK ==
 /\ EpochTypeOK
 /\ present \subseteq Members
 /\ memberRevision \in [Members -> 0..MaxRevision]
 /\ memberFields \in [Members -> [FieldNames -> Values]]
 /\ tableRevision \in [Tables -> 0..MaxRevision]
 /\ cellType \in [Cells -> {"zset","wrong"}]
 /\ permitted \in [Cells -> BOOLEAN]
\* Operation and receipt records are checked by the relational invariants below.
BatchCommit(q,kind,ds,ps,fs,ms,pr,tr,os,rs,result) ==
 /\ Commit(BatchActor,kind,live,rows,binds,ds,TRUE,TRUE,TRUE,TRUE)
 /\ place'=ps /\ UNCHANGED <<activeEpoch,seenEpoch>>
 /\ present'=pr /\ memberRevision'=ms /\ memberFields'=fs
 /\ tableRevision'=tr /\ operations'=os /\ receipts'=rs
 /\ outcome'=result /\ attempt'=q
 /\ returned'=IF result="accepted" THEN Receipt(q)
               ELSE IF result="replay" THEN (CHOOSE r \in Recorded(q):r.bytes=q.bytes).result
               ELSE "none"
 /\ UNCHANGED <<cellType,permitted>>
Accept(q) ==
 /\ Recorded(q)={} /\ FreshOK(q)
 /\ BatchCommit(q,"batch-accepted",NewData(q),NewPlace(q),NewFields(q),
      NewMemberRevision(q),NewPresent(q),
      [tableRevision EXCEPT ![q.table]=@+1],
      operations \cup {Record(q,Receipt(q))},Append(receipts,Receipt(q)),"accepted")
RefuseBatch(q,why) ==
 /\ BatchCommit(q,"batch-refused",data,place,memberFields,memberRevision,
      present,tableRevision,operations,receipts,why)
Replay(q) ==
 /\ Recorded(q)#{} /\ \E r \in Recorded(q):r.bytes=q.bytes
 /\ BatchCommit(q,"batch-retry",data,place,memberFields,memberRevision,
      present,tableRevision,operations,receipts,"replay")
Conflict(q) ==
 /\ Recorded(q)#{} /\ \A r \in Recorded(q):r.bytes#q.bytes
 /\ RefuseBatch(q,"conflict")
Apply(q) ==
 IF Recorded(q)#{} THEN Replay(q) \/ Conflict(q)
 ELSE IF FreshOK(q) THEN Accept(q) ELSE RefuseBatch(q,"stale-or-invalid")
\* An ordinary writer uses the same revision helper, even though the original
\* per-verb model remains separately checkable without these new variables.
OrdinaryFieldWrite(m) ==
 /\ m \in present /\ memberRevision[m]<MaxRevision
 /\ tableRevision[BatchTable]<MaxRevision
 /\ activeEpoch=BatchTable[2] /\ seenEpoch[BatchActor]=activeEpoch
 /\ Commit(BatchActor,"ordinary-field",live,rows,binds,data,TRUE,TRUE,TRUE,TRUE)
 /\ UNCHANGED <<place,activeEpoch,seenEpoch,present,operations,receipts,
                  cellType,permitted>>
 /\ memberFields'=[memberFields EXCEPT ![m]["status"]=
      IF @="ready" THEN "done" ELSE "ready"]
 /\ memberRevision'=[memberRevision EXCEPT ![m]=@+1]
 /\ tableRevision'=[tableRevision EXCEPT ![BatchTable]=@+1]
 /\ outcome'="ordinary" /\ returned'="none" /\ attempt'="none"
\* These wrappers retain the inherited member protocol's physical
\* set/record transition and add the table-owned revision helper.
OrdinaryAdd ==
 /\ CreateMember \notin present
 /\ Writable(CreateCell) /\ MemberEpoch[CreateMember]=activeEpoch
 /\ memberRevision[CreateMember]<MaxRevision
 /\ tableRevision[BatchTable]<MaxRevision
 /\ EpochAdd(BatchActor,CreateCell,CreateMember,1)
 /\ present'=present \cup {CreateMember}
 /\ memberRevision'=[memberRevision EXCEPT ![CreateMember]=@+1]
 /\ tableRevision'=[tableRevision EXCEPT ![BatchTable]=@+1]
 /\ UNCHANGED <<memberFields,operations,receipts,cellType,permitted>>
 /\ outcome'="ordinary" /\ returned'="none" /\ attempt'="none"
OrdinaryRemove ==
 /\ MoveMember \in present /\ place[MoveMember][BatchTable]=FromCell
 /\ Writable(FromCell) /\ MemberEpoch[MoveMember]=activeEpoch
 /\ memberRevision[MoveMember]<MaxRevision
 /\ tableRevision[BatchTable]<MaxRevision
 /\ EpochRemove(BatchActor,FromCell,MoveMember)
 /\ memberRevision'=[memberRevision EXCEPT ![MoveMember]=@+1]
 /\ tableRevision'=[tableRevision EXCEPT ![BatchTable]=@+1]
 /\ UNCHANGED <<present,memberFields,operations,receipts,cellType,permitted>>
 /\ outcome'="ordinary" /\ returned'="none" /\ attempt'="none"
OrdinaryMove ==
 /\ MoveMember \in present /\ place[MoveMember][BatchTable]=FromCell
 /\ Writable(FromCell) /\ Writable(MoveCell)
 /\ MemberEpoch[MoveMember]=activeEpoch
 /\ memberRevision[MoveMember]<MaxRevision
 /\ tableRevision[BatchTable]<MaxRevision
 /\ EpochMove(BatchActor,FromCell,MoveCell,MoveMember)
 /\ memberRevision'=[memberRevision EXCEPT ![MoveMember]=@+1]
 /\ tableRevision'=[tableRevision EXCEPT ![BatchTable]=@+1]
 /\ UNCHANGED <<present,memberFields,operations,receipts,cellType,permitted>>
 /\ outcome'="ordinary" /\ returned'="none" /\ attempt'="none"
OrdinarySetEmpty ==
 /\ activeEpoch=BatchTable[2] /\ seenEpoch[BatchActor]=activeEpoch
 /\ memberFields[GuardMember]["status"]=NoValue
 /\ memberRevision[GuardMember]<MaxRevision
 /\ tableRevision[BatchTable]<MaxRevision
 /\ Commit(BatchActor,"ordinary-field",live,rows,binds,data,TRUE,TRUE,TRUE,TRUE)
 /\ UNCHANGED <<place,activeEpoch,seenEpoch,present,operations,receipts,
                  cellType,permitted>>
 /\ memberFields'=[memberFields EXCEPT ![GuardMember]["status"]=""]
 /\ memberRevision'=[memberRevision EXCEPT ![GuardMember]=@+1]
 /\ tableRevision'=[tableRevision EXCEPT ![BatchTable]=@+1]
 /\ outcome'="ordinary" /\ returned'="none" /\ attempt'="none"
BatchAdvance ==
 /\ Advance(BatchActor)
 /\ UNCHANGED <<present,memberRevision,memberFields,tableRevision,
                  operations,receipts,cellType,permitted>>
 /\ outcome'="advance" /\ returned'="none" /\ attempt'="none"
BatchNext ==
 /\ (MaxSteps=0 \/ step<MaxSteps)
 /\ (\/ \E n \in 1..3:Apply(Normal(n))
     \/ Apply(NoopRequest)
     \/ Apply(AbsentRequest)
     \/ Apply(EmptyRequest)
     \/ Apply(DuplicateRequest)
     \/ Apply(InteractingRequest)
     \/ OrdinaryFieldWrite(GuardMember)
     \/ OrdinarySetEmpty
     \/ OrdinaryAdd
     \/ OrdinaryRemove
     \/ OrdinaryMove
     \/ BatchAdvance)
BatchSpec == BatchInit /\ [][BatchNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars
\* A second full-size instance maps one physical member to epoch 2. It
\* retains an epoch-1 member in Seed, then refreshes, declares the new
\* generation's two rows, and accepts a current-epoch create in one FCALL.
SecondRefresh ==
 /\ ReadEpoch(BatchActor)
 /\ UNCHANGED <<present,memberRevision,memberFields,tableRevision,
                  operations,receipts,cellType,permitted>>
 /\ outcome'="refresh" /\ returned'="none" /\ attempt'="none"
SecondBind ==
 /\ Current(BatchActor,SecondTable)
 /\ EpochBind(BatchActor,SecondTable,Rows,{})
 /\ UNCHANGED <<present,memberRevision,memberFields,operations,receipts,
                  cellType,permitted>>
 /\ tableRevision'=[tableRevision EXCEPT ![SecondTable]=@+1]
 /\ outcome'="shape" /\ returned'="none" /\ attempt'="none"
SecondNext ==
 \/ (op="initial" /\ BatchAdvance)
 \/ (op="advance-epoch" /\ SecondRefresh)
 \/ (op="read-epoch" /\ SecondBind)
 \/ (op="bind" /\ Apply(SecondRequest))
SecondSpec == BatchInit /\
 [][SecondNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars /\
 WF_bvars(SecondNext)
SecondEventuallyAccepted == <> (op="batch-accepted" /\ attempt.table=SecondTable)
SecondWriteKeepsOldImage == [][op'="batch-accepted" /\ attempt'.table=SecondTable =>
 /\ At(data',TableCells(BatchTable))=At(data,TableCells(BatchTable))
 /\ tableRevision'[BatchTable]=tableRevision[BatchTable]
 /\ \A m \in Members:
      /\ place'[m][BatchTable]=place[m][BatchTable]
      /\ (MemberEpoch[m]=1 =>
            memberRevision'[m]=memberRevision[m] /\
            memberFields'[m]=memberFields[m])]_bvars
\* Store image includes every application field, placement/index, revision,
\* operation record and receipt. Refusal and replay are read-only on it.
Store == <<live,rows,binds,data,place,present,memberRevision,memberFields,
           tableRevision,operations,receipts,cellType,permitted>>
RefusalAndReplayReadOnly == [][op' \in {"batch-refused","batch-retry"} =>
 UNCHANGED Store]_bvars
AcceptedGuardsWerePrestate == [][op'="batch-accepted" =>
 FreshOK(attempt')]_bvars
OneReceiptPerAccept == [][op'="batch-accepted" =>
 Len(receipts')=Len(receipts)+1]_bvars
NoReceiptOnRefusal == [][op'="batch-refused" => receipts'=receipts]_bvars
OperationKeysUnique == \A a,b \in operations:a.key=b.key => a=b
ReceiptKeysUnique == \A i,j \in 1..Len(receipts):
 receipts[i].key=receipts[j].key => i=j
RecordReceiptLink == \A r \in operations:
 \E i \in 1..Len(receipts):receipts[i]=r.result
CompleteReceipt == \A i \in 1..Len(receipts):
 LET rc == receipts[i]
 IN /\ DOMAIN rc.delta=rc.selected
    /\ rc.changed \subseteq rc.selected
    /\ rc.changedCount=Cardinality(rc.changed)
    /\ rc.guardCount=Cardinality(rc.selected \ rc.changed)
    /\ rc.selectedCount=Cardinality(rc.selected)
    /\ \A m \in rc.selected:
       /\ (m \in rc.changed <=> rc.delta[m].afterRevision=rc.delta[m].beforeRevision+1)
       /\ (m \notin rc.changed => rc.delta[m].afterPlace=rc.delta[m].beforePlace
                                     /\ rc.delta[m].afterFields=rc.delta[m].beforeFields)
    /\ rc.afterRevision=rc.beforeRevision+1
AcceptedReceiptExact == [][op'="batch-accepted" /\ Recorded(attempt')={} =>
 /\ receipts'[Len(receipts')]=Receipt(attempt')
 /\ data'=NewData(attempt') /\ place'=NewPlace(attempt')
 /\ memberFields'=NewFields(attempt')
 /\ memberRevision'=NewMemberRevision(attempt')]_bvars
\* Detect a missing ordinary-writer increment by comparing an actual field
\* change with the revision delta in that same transition.
OrdinaryKinds == {"ordinary-field","add","remove","move"}
OrdinaryAdvancesRevision == [][op' \in OrdinaryKinds =>
 \A m \in Members:
 memberRevision'[m]=memberRevision[m]+
    (IF place'[m]#place[m] \/ memberFields'[m]#memberFields[m] THEN 1 ELSE 0)]_bvars
OrdinaryAdvancesTable == [][op' \in OrdinaryKinds =>
 tableRevision'[BatchTable]=tableRevision[BatchTable]+1]_bvars
OrdinaryPlacementStalesPrepared ==
 [][op' \in {"add","remove","move"} =>
    tableRevision'[BatchTable] # Normal(1).revision]_bvars
BatchAdvancesTableOnce == [][op'="batch-accepted" =>
 tableRevision'[attempt'.table]=tableRevision[attempt'.table]+1]_bvars
\* Full equality, rather than digest equality, is the retry discriminator.
RetryIdentity == [][op'="batch-retry" =>
 /\ outcome'="replay" /\ UNCHANGED Store
 /\ \E r \in Recorded(attempt'):r.bytes=attempt'.bytes /\ returned'=r.result]_bvars
RecordedRetryIsReplay == [][op' \in {"batch-retry","batch-accepted"} /\
 Recorded(attempt')#{} /\
 (\E r \in Recorded(attempt'):r.bytes=attempt'.bytes)
 => op'="batch-retry"]_bvars
\* Fault setup is an external store condition injected before the modeled call.
\* Its step is explicit so the late failure occurs after a real first write.
FaultSetup(kind) ==
 /\ op="initial" /\ Refuse(BatchActor,"fault-setup")
 /\ UNCHANGED <<place,activeEpoch,seenEpoch,present,memberRevision,
                  tableRevision,operations,receipts>>
 /\ memberFields'=IF kind="guard"
      THEN [memberFields EXCEPT ![GuardMember]["token"]="deny"]
      ELSE memberFields
 /\ cellType'=IF kind="type" THEN [cellType EXCEPT ![CreateCell]="wrong"]
      ELSE cellType
 /\ permitted'=IF kind="permission"
      THEN [permitted EXCEPT ![CreateCell]=FALSE] ELSE permitted
 /\ outcome'="setup" /\ returned'="none" /\ attempt'="none"
PartialRefusal(q) ==
 /\ op="fault-setup" /\ FreshOK(Normal(1)) /\ ~ManifestOK(q)
 /\ BatchCommit(q,"batch-refused",NewData(Normal(1)),NewPlace(Normal(1)),
      NewFields(Normal(1)),NewMemberRevision(Normal(1)),NewPresent(Normal(1)),
      tableRevision,operations,receipts,"late-invalid")
ForcedAccept(q) ==
 /\ Recorded(q)={}
 /\ BatchCommit(q,"batch-accepted",NewData(q),NewPlace(q),NewFields(q),
      NewMemberRevision(q),NewPresent(q),
      [tableRevision EXCEPT ![q.table]=@+1],
      operations \cup {Record(q,Receipt(q))},Append(receipts,Receipt(q)),"accepted")
BadPostGuardNext ==
 /\ op="initial"
 /\ ~GuardOK(BadPostGuard.members[1])
 /\ GuardOKIn(NewFields(BadPostGuard),BadPostGuard.members[1])
 /\ ForcedAccept(BadPostGuard)
BadDuplicateNext ==
 /\ op="initial"
 /\ LET q == Normal(1)
         ds == data \cup {<<MoveCell,MoveMember,1>>}
     IN BatchCommit(q,"batch-accepted",ds,NewPlace(q),NewFields(q),
          NewMemberRevision(q),NewPresent(q),
          [tableRevision EXCEPT ![q.table]=@+1],
          operations \cup {Record(q,Receipt(q))},Append(receipts,Receipt(q)),"accepted")
BadStaleEpochNext == BatchAdvance \/
 (op="advance-epoch" /\ activeEpoch#Normal(1).epoch /\ ForcedAccept(Normal(1)))
BadStaleTableNext ==
 (op="initial" /\ OrdinaryAdd) \/
 (op="add" /\ tableRevision[BatchTable]#Normal(1).revision /\
  ForcedAccept(Normal(1)))
BadStaleMemberNext ==
 (op="initial" /\ OrdinaryFieldWrite(MoveMember)) \/
 (op="ordinary-field" /\
  memberRevision[MoveMember]#BadMemberRevision.members[1].revision /\
  ForcedAccept(BadMemberRevision))
BadStaleMoveRevisionNext ==
 (op="initial" /\ OrdinaryMove) \/
 (op="move" /\
  LET q == BadMoveRevision
      actual == [q EXCEPT !.members[1].source=MoveCell]
  IN /\ tableRevision[BatchTable]=q.revision
     /\ memberRevision[MoveMember]#q.members[1].revision
     /\ place[MoveMember][BatchTable]#q.members[1].source
     /\ BatchCommit(q,"batch-accepted",NewData(actual),NewPlace(actual),
          NewFields(actual),NewMemberRevision(actual),NewPresent(actual),
          [tableRevision EXCEPT ![q.table]=@+1],
          operations \cup {Record(q,Receipt(q))},
          Append(receipts,Receipt(q)),"accepted"))
BadOrdinaryMoveRevisionNext ==
 /\ op="initial" /\ place[MoveMember][BatchTable]=FromCell
 /\ EpochMove(BatchActor,FromCell,MoveCell,MoveMember)
 /\ tableRevision'=[tableRevision EXCEPT ![BatchTable]=@+1]
 /\ UNCHANGED <<present,memberRevision,memberFields,operations,receipts,
                  cellType,permitted>>
 /\ outcome'="ordinary" /\ returned'="none" /\ attempt'="none"
BadOrdinaryRevisionNext ==
 /\ op="initial" /\ memberFields[GuardMember]["status"]=NoValue
 /\ Commit(BatchActor,"ordinary-field",live,rows,binds,data,TRUE,TRUE,TRUE,TRUE)
 /\ UNCHANGED <<place,activeEpoch,seenEpoch,present,memberRevision,
                  operations,receipts,cellType,permitted>>
 /\ memberFields'=[memberFields EXCEPT ![GuardMember]["status"]="ready"]
 /\ tableRevision'=[tableRevision EXCEPT ![BatchTable]=@+1]
 /\ outcome'="ordinary" /\ returned'="none" /\ attempt'="none"
BadLostReplyNext ==
 (op="initial" /\ Accept(Normal(1))) \/
 (op="batch-accepted" /\
  LET q == Normal(1)
  IN BatchCommit(q,"batch-retry",data,place,memberFields,memberRevision,
       present,[tableRevision EXCEPT ![BatchTable]=@+1],operations,
       Append(receipts,Receipt(q)),"replay"))
BadHashCollisionNext ==
 (op="initial" /\ Accept(Normal(1))) \/
 (op="batch-accepted" /\
  LET q == Collision(1)
  IN /\ Recorded(q)#{} /\ Refuse(BatchActor,"batch-retry")
     /\ UNCHANGED <<place,activeEpoch,seenEpoch,present,memberRevision,
                      memberFields,tableRevision,operations,receipts,
                      cellType,permitted>>
     /\ outcome'="replay" /\ attempt'=q
     /\ returned'=(CHOOSE r \in Recorded(q):TRUE).result)
BadReceiptNext ==
 /\ op="initial"
 /\ LET q == Normal(2)
         first == Normal(1)
     IN BatchCommit(q,"batch-accepted",NewData(first),NewPlace(first),
          NewFields(first),NewMemberRevision(first),NewPresent(first),
          [tableRevision EXCEPT ![q.table]=@+1],
          operations \cup {Record(q,Receipt(q))},
          Append(receipts,Receipt(q)),"accepted")
BadStaleRetryNext ==
 (op="initial" /\ Accept(Normal(1))) \/
 (op="batch-accepted" /\ BatchAdvance) \/
 (op="advance-epoch" /\
  LET q == Normal(1)
  IN BatchCommit(q,"batch-accepted",data,place,memberFields,memberRevision,
       present,tableRevision,operations,receipts,"accepted"))
BoundedBad(next) == next \/ (step=MaxSteps /\ UNCHANGED bvars)
BrokenLateGuardSpec == BatchInit /\ [][BoundedBad(FaultSetup("guard") \/ PartialRefusal(Normal(3)))]_bvars
BrokenLateTypeSpec == BatchInit /\ [][BoundedBad(FaultSetup("type") \/ PartialRefusal(Normal(2)))]_bvars
BrokenLatePermissionSpec == BatchInit /\ [][BoundedBad(FaultSetup("permission") \/ PartialRefusal(Normal(2)))]_bvars
BrokenPostGuardSpec == BatchInit /\ [][BoundedBad(BadPostGuardNext)]_bvars
BrokenDuplicateSpec == BatchInit /\ [][BoundedBad(BadDuplicateNext)]_bvars
BrokenStaleEpochSpec == BatchInit /\ [][BoundedBad(BadStaleEpochNext)]_bvars
BrokenStaleTableSpec == BatchInit /\ [][BoundedBad(BadStaleTableNext)]_bvars
BrokenStaleMemberSpec == BatchInit /\ [][BoundedBad(BadStaleMemberNext)]_bvars
BrokenStaleMoveRevisionSpec == BatchInit /\ [][BoundedBad(BadStaleMoveRevisionNext)]_bvars
BrokenOrdinaryMoveRevisionSpec == BatchInit /\ [][BoundedBad(BadOrdinaryMoveRevisionNext)]_bvars
BrokenOrdinaryRevisionSpec == BatchInit /\ [][BoundedBad(BadOrdinaryRevisionNext)]_bvars
BrokenLostReplySpec == BatchInit /\ [][BoundedBad(BadLostReplyNext)]_bvars
BrokenHashCollisionSpec == BatchInit /\ [][BoundedBad(BadHashCollisionNext)]_bvars
BrokenReceiptSpec == BatchInit /\ [][BoundedBad(BadReceiptNext)]_bvars
BrokenStaleRetrySpec == BatchInit /\ [][BoundedBad(BadStaleRetryNext)]_bvars
=============================================================================
