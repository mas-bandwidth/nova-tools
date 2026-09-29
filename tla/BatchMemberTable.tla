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
EmptyFields == [f \in FieldNames |-> NoValue]
Values == {"","ready","done","permit","new","deny"} \cup {NoValue}
NoField == "none"
MaxRevision == 3
\* The checked instance uses Scores={1,2}; zero marks no placed score.
NoScore == 0
ASSUME NoScore \notin Scores
MoveEntry == [id |-> MoveMember, absent |-> FALSE,
              hasRevision |-> TRUE, revision |-> 0,
              source |-> FromCell, target |-> MoveCell,
              guardField |-> "status", guardKind |-> "equals", guardValues |-> {"ready"},
              setField |-> "status", setValue |-> "done", score |-> 1,
              scoreChange |-> FALSE, remove |-> FALSE,
              removeSupplied |-> FALSE, unsetField |-> NoField, change |-> TRUE]
CreateEntry == [id |-> CreateMember, absent |-> TRUE,
                hasRevision |-> FALSE, revision |-> 0,
                source |-> NoPlace, target |-> CreateCell,
                guardField |-> NoField, guardKind |-> "none", guardValues |-> {},
                setField |-> "status", setValue |-> "new", score |-> 1,
                scoreChange |-> TRUE, remove |-> FALSE,
                removeSupplied |-> FALSE, unsetField |-> NoField, change |-> TRUE]
GuardEntry == [id |-> GuardMember, absent |-> FALSE,
               hasRevision |-> TRUE, revision |-> 0,
               source |-> GuardCell, target |-> GuardCell,
               guardField |-> "token", guardKind |-> "equals", guardValues |-> {"permit"},
               setField |-> NoField, setValue |-> NoValue, score |-> 2,
               scoreChange |-> FALSE, remove |-> FALSE,
               removeSupplied |-> FALSE, unsetField |-> NoField, change |-> FALSE]
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
         !.members[3].unsetField="token"]
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
\* The ordinary writer first advances m1's revision to one. The following
\* request intentionally omits its member revision guard, but retains the
\* current table revision, source placement and field guard.
ScoreMoveRequest ==
 [Request(1,"op-score-move","canonical-score-move","digest-score-move",1,1)
  EXCEPT !.members[1].hasRevision=FALSE,
         !.members[1].guardValues={"done"},
         !.members[1].setField=NoField, !.members[1].setValue=NoValue,
         !.members[1].score=2, !.members[1].scoreChange=TRUE]
RemoveRequest ==
 [Request(1,"op-remove","canonical-remove","digest-remove",2,1)
  EXCEPT !.members[1].revision=2,
         !.members[1].source=MoveCell, !.members[1].target=NoPlace,
         !.members[1].guardValues={"done"},
         !.members[1].setField=NoField, !.members[1].setValue=NoValue,
         !.members[1].remove=TRUE, !.members[1].removeSupplied=TRUE]
SameCellRequest ==
 [Request(1,"op-same-cell","canonical-same-cell","digest-same-cell",2,1)
  EXCEPT !.members[1].revision=2,
         !.members[1].source=MoveCell, !.members[1].target=MoveCell,
         !.members[1].guardValues={"done"},
         !.members[1].setField=NoField, !.members[1].setValue=NoValue,
         !.members[1].score=2, !.members[1].scoreChange=TRUE]
EarlyRemoveRequest ==
 [Request(1,"op-early-remove","canonical-early-remove","digest-early-remove",1,1)
  EXCEPT !.members[1].revision=1,
         !.members[1].source=MoveCell, !.members[1].target=NoPlace,
         !.members[1].guardValues={"done"},
         !.members[1].setField=NoField, !.members[1].setValue=NoValue,
         !.members[1].remove=TRUE, !.members[1].removeSupplied=TRUE]
UnplacedFieldRequest ==
 [Request(1,"op-unplaced-field","canonical-unplaced-field","digest-unplaced-field",2,1)
  EXCEPT !.members[1].revision=2,
         !.members[1].source=NoPlace, !.members[1].target=NoPlace,
         !.members[1].guardValues={"done"},
         !.members[1].setField="status", !.members[1].setValue="ready"]
AlreadyUnplacedRemoveRequest ==
 [Request(1,"op-unplaced-remove","canonical-unplaced-remove","digest-unplaced-remove",2,1)
  EXCEPT !.members[1].revision=2,
         !.members[1].source=NoPlace, !.members[1].target=NoPlace,
         !.members[1].guardValues={"done"},
         !.members[1].setField=NoField, !.members[1].setValue=NoValue,
         !.members[1].remove=TRUE, !.members[1].removeSupplied=TRUE]
\* Existing unplaced records support field edits, but cannot be moved into a
\* cell. Check both omitted score (formerly an undefined ScoreAt) and explicit.
UnplacedMoveRequest ==
 [UnplacedFieldRequest EXCEPT !.id="op-unplaced-move",
    !.bytes="canonical-unplaced-move", !.members[1].target=FromCell]
UnplacedMoveWithScore ==
 [UnplacedMoveRequest EXCEPT !.id="op-unplaced-move-score",
    !.bytes="canonical-unplaced-move-score", !.members[1].scoreChange=TRUE]
\* Two accepted moves reach the configured revision limit of two; the next
\* otherwise-valid request must refuse. These are real batch actions, without
\* the synthetic field writers used by some older interference scenarios.
ReturnMoveRequest ==
 [Normal(1) EXCEPT !.id="op-return", !.bytes="canonical-return",
    !.revision=1, !.members[1].revision=1,
    !.members[1].source=MoveCell, !.members[1].target=FromCell,
    !.members[1].guardValues={"done"}, !.members[1].setValue="ready"]
OverflowRequest ==
 [Normal(1) EXCEPT !.id="op-overflow", !.bytes="canonical-overflow",
    !.revision=2, !.members[1].revision=2]
RevisionNoop(n) ==
 [NoopRequest EXCEPT !.id= <<"noop-one","noop-two","noop-overflow">>[n],
    !.bytes= <<"bytes-one","bytes-two","bytes-overflow">>[n], !.revision=n-1]
RemoveFalseRequest ==
 [Normal(1) EXCEPT !.id="op-remove-false", !.bytes="canonical-remove-false",
                   !.members[1].removeSupplied=TRUE]
SetUnsetSameRequest ==
 [Normal(1) EXCEPT !.id="op-set-unset", !.bytes="canonical-set-unset",
                   !.members[1].unsetField="status"]
Key(q) == <<q.table,q.epoch,q.id>>
Recorded(q) == {r \in operations:r.key=Key(q)}
Record(q,rc) == [key |-> Key(q), bytes |-> q.bytes,
                 digest |-> q.digest, result |-> rc]
EntryIDs(q) == {q.members[i].id:i \in 1..Len(q.members)}
Entry(q,m) == CHOOSE e \in {q.members[i]:i \in 1..Len(q.members)}:e.id=m
\* An expected source may be stale in a reversed witness. Read the actual
\* indexed pre-state cell, so even the faulty receipt remains evaluable.
ScoreAt(e) ==
 IF e.source=NoPlace THEN NoScore
 ELSE IF place[e.id][e.source[1]]=NoPlace THEN NoScore
 ELSE CHOOSE s \in Scores:
  <<place[e.id][e.source[1]],e.id,s>> \in data
DesiredScore(e) == IF e.absent \/ e.scoreChange THEN e.score ELSE ScoreAt(e)
EffectiveChange(e) ==
 IF ~e.change THEN FALSE
 ELSE IF e.absent \/ e.remove THEN TRUE
 ELSE e.target#e.source \/
      (IF e.scoreChange THEN e.score#ScoreAt(e) ELSE FALSE) \/
      (IF e.setField#NoField
       THEN memberFields[e.id][e.setField]#e.setValue ELSE FALSE) \/
      (IF e.unsetField#NoField
       THEN memberFields[e.id][e.unsetField]#NoValue ELSE FALSE)
Changed(q) == {q.members[i].id:i \in {j \in 1..Len(q.members):EffectiveChange(q.members[j])}}
RequestedChanges(q) ==
 {q.members[i].id:i \in {j \in 1..Len(q.members):q.members[j].change}}
Created(q) == {m \in Changed(q):Entry(q,m).absent}
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
 /\ (e.absent => ~e.hasRevision)
 /\ (e.change => MemberEpoch[e.id]=q.epoch)
 /\ (e.absent \/ ~e.hasRevision \/ memberRevision[e.id]=e.revision)
 /\ GuardOK(e)
 /\ e.removeSupplied=e.remove
 /\ (e.setField=NoField \/ e.unsetField#e.setField)
 /\ (~e.remove \/ (~e.absent /\ e.change /\ e.target=NoPlace))
 /\ (~e.absent \/ (~e.remove /\ e.change /\ e.target#NoPlace))
 /\ (~e.remove \/ e.source#NoPlace)
 /\ (e.change /\ ~e.absent /\ ~e.remove /\ e.target#NoPlace =>
      e.source#NoPlace)
 /\ (~e.scoreChange \/ (e.change /\ ~e.remove /\
                         (e.absent \/ e.source#NoPlace) /\ e.score \in Scores))
 /\ (e.absent => e.score \in Scores)
 /\ (e.change /\ ~e.absent /\ e.source#NoPlace =>
      cellType[e.source]="zset" /\ permitted[e.source])
 /\ (e.change /\ e.target#NoPlace => e.target \in Owned(live,rows,binds))
 /\ (e.change /\ e.target#NoPlace => cellType[e.target]="zset" /\ permitted[e.target])
 /\ (EffectiveChange(e) => memberRevision[e.id]<MaxRevision)
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
\* another entry's writes. An omitted move score preserves its source score.
NewPlace(q) == [m \in Members |-> [place[m] EXCEPT
    ![q.table]=IF m \in Changed(q) THEN Entry(q,m).target ELSE @]]
NewData(q) ==
 {e \in data:~(e[2] \in Changed(q) /\
                  e[1] \in (Owned(live,rows,binds) \cap TableCells(q.table)))} \cup
 {<<Entry(q,m).target,m,DesiredScore(Entry(q,m))>>:
    m \in {x \in Changed(q):Entry(q,x).target#NoPlace}}
NewFields(q) == [m \in Members |->
 IF m \in Changed(q)
 THEN LET e == Entry(q,m)
          base == IF e.absent THEN EmptyFields ELSE memberFields[m]
          afterSet == IF e.setField#NoField
                      THEN [base EXCEPT ![e.setField]=e.setValue]
                      ELSE base
      IN IF e.unsetField#NoField
         THEN [afterSet EXCEPT ![e.unsetField]=NoValue]
         ELSE afterSet
 ELSE memberFields[m]]
NewMemberRevision(q) == [m \in Members |->
 IF m \in Changed(q) THEN memberRevision[m]+1 ELSE memberRevision[m]]
NewPresent(q) == present \cup Created(q)
BeforeScore(q,m) == IF place[m][q.table]=NoPlace THEN NoScore ELSE ScoreAt(Entry(q,m))
AfterScore(q,m) == IF NewPlace(q)[m][q.table]=NoPlace THEN NoScore
                   ELSE IF m \in Changed(q) THEN DesiredScore(Entry(q,m))
                   ELSE BeforeScore(q,m)
Delta(q) == [m \in EntryIDs(q) |->
 [beforePlace |-> place[m][q.table], afterPlace |-> NewPlace(q)[m][q.table],
  beforeRevision |-> memberRevision[m], afterRevision |-> NewMemberRevision(q)[m],
  beforeScore |-> BeforeScore(q,m), afterScore |-> AfterScore(q,m),
  beforeFields |-> memberFields[m], afterFields |-> NewFields(q)[m]]]
Receipt(q) ==
 [key |-> Key(q), bytes |-> q.bytes, digest |-> q.digest,
  actor |-> q.actor, beforeRevision |-> tableRevision[q.table],
  afterRevision |-> tableRevision[q.table]+1,
  changed |-> Changed(q), selected |-> EntryIDs(q), delta |-> Delta(q),
  changedCount |-> Cardinality(Changed(q)),
  guardCount |-> Cardinality(EntryIDs(q) \ RequestedChanges(q)),
  selectedCount |-> Cardinality(EntryIDs(q)),
  kind |-> IF Changed(q)={} THEN "noop" ELSE "changed"]
BatchInitWithMemberRevision(revisions) ==
 /\ EpochInit
 /\ present={m \in Members: \E e \in Seed:e[2]=m /\ e[1]#External}
 /\ memberRevision=revisions
 /\ memberFields=[m \in Members |->
      [f \in FieldNames |->
       IF m \in present /\ m=MoveMember /\ f="status" THEN "ready"
       ELSE IF m \in present /\ m=GuardMember /\ f="token" THEN "permit"
       ELSE NoValue]]
 /\ tableRevision=[t \in Tables |-> 0]
 /\ operations={} /\ receipts= <<>> /\ outcome="initial"
 /\ returned="none" /\ attempt="none"
 /\ cellType=[c \in Cells |-> "zset"]
 /\ permitted=[c \in Cells |-> TRUE]
BatchInit == BatchInitWithMemberRevision([m \in Members |-> 0])
\* The runtime counters are separate stored values. This input fixture does
\* not claim reachability from the all-zero seed: it isolates a member at its
\* limit while the table has room, so the table guard cannot mask this guard.
SeededMemberOverflowInit == BatchInitWithMemberRevision(
 [m \in Members |-> IF m=MoveMember THEN MaxRevision ELSE 0])
MemberOverflowRequest ==
 [Normal(1) EXCEPT !.id="op-member-overflow", !.bytes="bytes-member-overflow",
    !.members[1].revision=MaxRevision]
\* The replay harness starts from the same constrained finite fixture as TLC.
\* Flatten the inherited state so a trace comparison cannot omit a field.
BatchReplayInit == BatchInit
BatchReplayState ==
 <<live,rows,binds,data,place,activeEpoch,seenEpoch,present,
   memberRevision,memberFields,tableRevision,operations,receipts,
   outcome,returned,attempt,cellType,permitted>>
BatchTypeOK ==
 /\ EpochTypeOK
 /\ present \subseteq Members
 /\ memberRevision \in [Members -> 0..MaxRevision]
 /\ memberFields \in [Members -> [FieldNames -> Values]]
 /\ tableRevision \in [Tables -> 0..MaxRevision]
 /\ cellType \in [Cells -> {"zset","wrong"}]
 /\ permitted \in [Cells -> BOOLEAN]
AbsentRecordsHaveNoFields ==
 \A m \in Members \ present:memberFields[m]=EmptyFields
CreatedReceiptStartsEmpty == [][op'="batch-accepted" =>
 \A m \in Created(attempt'):
 returned'.delta[m].beforeFields=EmptyFields]_bvars
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
\* Synthetic interference: table.lua has no revision-bumping field-write verb.
\* Direct application HSET does not bump these revisions. These two field
\* actions test a hypothetical cooperating writer, not current HSET behavior.
\* See README's explicit scope decision and follow-up.
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
     \/ Apply(ScoreMoveRequest)
     \/ Apply(RemoveRequest)
     \/ Apply(SameCellRequest)
     \/ Apply(EarlyRemoveRequest)
     \/ Apply(UnplacedFieldRequest)
     \/ Apply(AlreadyUnplacedRemoveRequest)
     \/ Apply(UnplacedMoveRequest)
     \/ Apply(UnplacedMoveWithScore)
     \/ Apply(RemoveFalseRequest)
     \/ Apply(SetUnsetSameRequest)
     \/ OrdinaryFieldWrite(MoveMember)
     \/ OrdinaryFieldWrite(GuardMember)
     \/ OrdinarySetEmpty
     \/ OrdinaryAdd
     \/ OrdinaryRemove
     \/ OrdinaryMove
     \/ BatchAdvance)
BatchSpec == BatchInit /\ [][BatchNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars
\* The prepared move omits its member revision after an ordinary writer has
\* advanced that revision. The request still checks the table revision and
\* source placement. Two short instances share this prefix: one removes the
\* placement but retains the record; the other performs an accepted no-op.
ExtendedPrefix ==
 \/ (step=0 /\ OrdinaryFieldWrite(MoveMember))
 \/ (step=1 /\ Apply(ScoreMoveRequest))
ExtendedRemoveNext == ExtendedPrefix \/ (step=2 /\ Apply(RemoveRequest))
ExtendedNoopNext == ExtendedPrefix \/ (step=2 /\ Apply(SameCellRequest))
ExtendedRemoveSpec == BatchInit /\
 [][ExtendedRemoveNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars /\
 WF_bvars(ExtendedRemoveNext)
ExtendedNoopSpec == BatchInit /\
 [][ExtendedNoopNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars /\
 WF_bvars(ExtendedNoopNext)
UnplacedFieldNext ==
 \/ (step=0 /\ Apply(Normal(1)))
 \/ (step=1 /\ Apply(EarlyRemoveRequest))
 \/ (step=2 /\ Apply(UnplacedFieldRequest))
UnplacedFieldSpec == BatchInit /\
 [][UnplacedFieldNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars /\
 WF_bvars(UnplacedFieldNext)
UnplacedFieldEventuallyAccepted ==
 <> (op="batch-accepted" /\ attempt.id=UnplacedFieldRequest.id /\
     place[MoveMember][BatchTable]=NoPlace /\ MoveMember \in present /\
     memberFields[MoveMember]["status"]="ready")
AlreadyUnplacedRemoveNext ==
 \/ (step=0 /\ Apply(Normal(1)))
 \/ (step=1 /\ Apply(EarlyRemoveRequest))
 \/ (step=2 /\ Apply(AlreadyUnplacedRemoveRequest))
AlreadyUnplacedRemoveSpec == BatchInit /\
 [][AlreadyUnplacedRemoveNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars /\
 WF_bvars(AlreadyUnplacedRemoveNext)
AlreadyUnplacedRemoveEventuallyRefused ==
 <> (op="batch-refused" /\ attempt.id=AlreadyUnplacedRemoveRequest.id)
UnplacedMoveNext ==
 \/ (step=0 /\ Apply(Normal(1)))
 \/ (step=1 /\ Apply(EarlyRemoveRequest))
 \/ (step=2 /\ Apply(UnplacedMoveRequest))
 \/ (step=3 /\ Apply(UnplacedMoveWithScore))
UnplacedMoveSpec == BatchInit /\
 [][UnplacedMoveNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars /\
 WF_bvars(UnplacedMoveNext)
UnplacedMovesEventuallyRefused ==
 /\ <> (op="batch-refused" /\ attempt.id=UnplacedMoveRequest.id)
 /\ <> (op="batch-refused" /\ attempt.id=UnplacedMoveWithScore.id)
UnplacedMoveRequiresPlacement == [][op'="batch-accepted" =>
 \A i \in 1..Len(attempt'.members):
 LET e == attempt'.members[i]
 IN (e.change /\ ~e.absent /\ ~e.remove /\ e.target#NoPlace) =>
    e.source#NoPlace]_bvars
OverflowPrefix ==
 \/ (step=0 /\ Apply(Normal(1)))
 \/ (step=1 /\ Apply(ReturnMoveRequest))
OverflowNext == OverflowPrefix \/ (step=2 /\ Apply(OverflowRequest))
OverflowSpec == BatchInit /\
 [][OverflowNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars /\
 WF_bvars(OverflowNext)
OverflowEventuallyRefused ==
 <> (op="batch-refused" /\ attempt.id=OverflowRequest.id /\
     tableRevision[BatchTable]=MaxRevision /\
     memberRevision[MoveMember]=MaxRevision /\ Len(receipts)=2)
TableOverflowNext ==
 \E n \in 1..3:step=n-1 /\ Apply(RevisionNoop(n))
TableOverflowSpec == BatchInit /\
 [][TableOverflowNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars /\
 WF_bvars(TableOverflowNext)
TableOverflowEventuallyRefused ==
 <> (op="batch-refused" /\ attempt.id=RevisionNoop(3).id /\
     tableRevision[BatchTable]=MaxRevision /\
     (\A m \in Members:memberRevision[m]=0) /\ Len(receipts)=2)
MemberOverflowNext == step=0 /\ Apply(MemberOverflowRequest)
MemberOverflowSpec == SeededMemberOverflowInit /\
 [][MemberOverflowNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars /\
 WF_bvars(MemberOverflowNext)
MemberOverflowEventuallyRefused ==
 <> (op="batch-refused" /\ attempt.id=MemberOverflowRequest.id /\
     memberRevision[MoveMember]=MaxRevision /\
     tableRevision[BatchTable]=0 /\ receipts= <<>>)
RevisionWithinBounds ==
 /\ \A m \in Members:memberRevision[m]<=MaxRevision
 /\ \A t \in Tables:tableRevision[t]<=MaxRevision
ExtendedRemoveEventuallyAccepted ==
 <> (op="batch-accepted" /\ attempt.id=RemoveRequest.id)
ExtendedNoopEventuallyAccepted ==
 <> (op="batch-accepted" /\ attempt.id=SameCellRequest.id /\
     returned.kind="noop" /\ memberRevision[MoveMember]=2)
MalformedNext ==
 \/ (step=0 /\ Apply(RemoveFalseRequest))
 \/ (step=1 /\ Apply(SetUnsetSameRequest))
MalformedSpec == BatchInit /\
 [][MalformedNext \/ (step=MaxSteps /\ UNCHANGED bvars)]_bvars /\
 WF_bvars(MalformedNext)
MalformedEventuallyRefused ==
 <> (op="batch-refused" /\ attempt.id=SetUnsetSameRequest.id)
RemoveRetainsMemberRecord == [][op'="batch-accepted" /\
 attempt'.id=RemoveRequest.id =>
 /\ MoveMember \in present' /\ place'[MoveMember][BatchTable]=NoPlace
 /\ memberRevision'[MoveMember]=memberRevision[MoveMember]+1]_bvars
SameCellNoopKeepsMemberRevision == [][op'="batch-accepted" /\
 attempt'.id=SameCellRequest.id =>
 /\ memberRevision'[MoveMember]=memberRevision[MoveMember]
 /\ tableRevision'[BatchTable]=tableRevision[BatchTable]+1
 /\ receipts'[Len(receipts')].kind="noop"]_bvars
ScoreChangeCommitted == [][op'="batch-accepted" /\
 attempt'.id=ScoreMoveRequest.id =>
 <<MoveCell,MoveMember,2>> \in data']_bvars
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
FreshValidNeverRefused == [][op'="batch-refused" =>
 ~(Recorded(attempt')={} /\ FreshOK(attempt'))]_bvars
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
    /\ rc.changedCount+rc.guardCount<=rc.selectedCount
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
BadMissingRevisionNext ==
 (step=0 /\ OrdinaryFieldWrite(MoveMember)) \/
 (step=1 /\ FreshOK(ScoreMoveRequest) /\
  ~ScoreMoveRequest.members[1].hasRevision /\
  RefuseBatch(ScoreMoveRequest,"missing-member-revision"))
BadScorePreservedNext ==
 (step=0 /\ OrdinaryFieldWrite(MoveMember)) \/
 (step=1 /\
  LET q == ScoreMoveRequest
      actual == [q EXCEPT !.members[1].scoreChange=FALSE]
  IN /\ FreshOK(q)
     /\ BatchCommit(q,"batch-accepted",NewData(actual),NewPlace(q),
          NewFields(q),NewMemberRevision(q),NewPresent(q),
          [tableRevision EXCEPT ![q.table]=@+1],
          operations \cup {Record(q,Receipt(q))},
          Append(receipts,Receipt(q)),"accepted"))
BadRemoveLeavesCellNext ==
 ExtendedPrefix \/
 (step=2 /\
  LET q == RemoveRequest
  IN /\ FreshOK(q)
     /\ BatchCommit(q,"batch-accepted",data,NewPlace(q),
          NewFields(q),NewMemberRevision(q),NewPresent(q),
          [tableRevision EXCEPT ![q.table]=@+1],
          operations \cup {Record(q,Receipt(q))},
          Append(receipts,Receipt(q)),"accepted"))
BadRemoveDeletesRecordNext ==
 ExtendedPrefix \/
 (step=2 /\
  LET q == RemoveRequest
  IN /\ FreshOK(q)
     /\ BatchCommit(q,"batch-accepted",NewData(q),NewPlace(q),
          NewFields(q),NewMemberRevision(q),NewPresent(q) \ {MoveMember},
          [tableRevision EXCEPT ![q.table]=@+1],
          operations \cup {Record(q,Receipt(q))},
          Append(receipts,Receipt(q)),"accepted"))
BadSameCellRevisionNext ==
 ExtendedPrefix \/
 (step=2 /\
  LET q == SameCellRequest
      wrong == [memberRevision EXCEPT ![MoveMember]=@+1]
  IN /\ FreshOK(q)
     /\ BatchCommit(q,"batch-accepted",NewData(q),NewPlace(q),
          NewFields(q),wrong,NewPresent(q),
          [tableRevision EXCEPT ![q.table]=@+1],
          operations \cup {Record(q,Receipt(q))},
          Append(receipts,Receipt(q)),"accepted"))
BadRemoveFalseAcceptedNext ==
 op="initial" /\ ForcedAccept(RemoveFalseRequest)
BadSetUnsetAcceptedNext ==
 op="initial" /\ ForcedAccept(SetUnsetSameRequest)
BadAlreadyUnplacedRemoveNext ==
 (step=0 /\ Apply(Normal(1))) \/
 (step=1 /\ Apply(EarlyRemoveRequest)) \/
 (step=2 /\ ForcedAccept(AlreadyUnplacedRemoveRequest))
BadUnplacedMoveNext ==
 (step=0 /\ Apply(Normal(1))) \/
 (step=1 /\ Apply(EarlyRemoveRequest)) \/
 (step=2 /\ ForcedAccept(UnplacedMoveRequest))
BadOverflowNext == OverflowPrefix \/ (step=2 /\ ForcedAccept(OverflowRequest))
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
BrokenMissingRevisionSpec == BatchInit /\ [][BoundedBad(BadMissingRevisionNext)]_bvars
BrokenScorePreservedSpec == BatchInit /\ [][BoundedBad(BadScorePreservedNext)]_bvars
BrokenRemoveLeavesCellSpec == BatchInit /\ [][BoundedBad(BadRemoveLeavesCellNext)]_bvars
BrokenRemoveDeletesRecordSpec == BatchInit /\ [][BoundedBad(BadRemoveDeletesRecordNext)]_bvars
BrokenSameCellRevisionSpec == BatchInit /\ [][BoundedBad(BadSameCellRevisionNext)]_bvars
BrokenRemoveFalseAcceptedSpec == BatchInit /\ [][BoundedBad(BadRemoveFalseAcceptedNext)]_bvars
BrokenSetUnsetAcceptedSpec == BatchInit /\ [][BoundedBad(BadSetUnsetAcceptedNext)]_bvars
BrokenMemberOverflowSpec == SeededMemberOverflowInit /\
 [][BoundedBad(step=0 /\ ForcedAccept(MemberOverflowRequest))]_bvars
BrokenOverflowSpec == BatchInit /\ [][BoundedBad(BadOverflowNext)]_bvars
BrokenUnplacedMoveSpec == BatchInit /\ [][BoundedBad(BadUnplacedMoveNext)]_bvars
BrokenAlreadyUnplacedRemoveSpec == BatchInit /\ [][BoundedBad(BadAlreadyUnplacedRemoveNext)]_bvars
=============================================================================
