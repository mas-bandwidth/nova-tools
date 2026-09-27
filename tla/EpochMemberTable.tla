----------------------- MODULE EpochMemberTable -----------------------
\* PROPOSED epoch composition. Physical table identity is <<dimension,epoch>>.
\* MemberEpoch is immutable. The real record's place:<dimension> corresponds
\* to place[m][<<dimension,MemberEpoch[m]>>]; other generations stay NoPlace.
\* Advance changes the active namespace only (one INCR), retaining history.
EXTENDS MemberTable
CONSTANTS Epochs, Dimensions, MemberEpoch
VARIABLES activeEpoch, seenEpoch
xvars == <<mvars,activeEpoch,seenEpoch>>
FirstEpoch == 1
EpochInit ==
 /\ live=Tables
 /\ rows={<<t,r>> : t \in {x \in Tables:x[2]=FirstEpoch}, r \in Rows}
 /\ binds={} /\ data=Seed /\ step=0 /\ writer="initial" /\ op="initial"
 /\ checks=[k \in CheckNames |-> TRUE]
 /\ place=[m \in Members |-> [t \in Tables |->
       IF Places(data,live,rows,binds,m,t)={} THEN NoPlace
       ELSE CHOOSE c \in Places(data,live,rows,binds,m,t):TRUE]]
 /\ activeEpoch=FirstEpoch
 /\ seenEpoch=[w \in Writers |-> FirstEpoch]
EpochTypeOK ==
 /\ MemberTypeOK
 /\ activeEpoch \in Epochs
 /\ seenEpoch \in [Writers -> Epochs]
NoEpochLeak == \A e \in data : e[1]=External \/ e[1][1][2]=MemberEpoch[e[2]]
RecordEpochLink == \A m \in Members,t \in Tables :
 place[m][t]=NoPlace \/ t[2]=MemberEpoch[m]
OnePlacePerDimension == \A m \in Members,d \in Dimensions :
 Cardinality({c \in Owned(live,rows,binds):c[1][1]=d /\ \E e \in data:e[1]=c /\ e[2]=m}) <= 1
VisibleEntries == {e \in data:e[1] # External /\ e[1][1][2]=activeEpoch}
OldMembersInvisible == \A e \in VisibleEntries:MemberEpoch[e[2]]=activeEpoch
Current(w,t) == t[2]=activeEpoch /\ seenEpoch[w]=activeEpoch
EpochRefuse(w,why) == MemberRefuse(w,why) /\ UNCHANGED <<activeEpoch,seenEpoch>>
EpochAdd(w,c,m,s) ==
 IF Current(w,c[1]) /\ c[1][2]=MemberEpoch[m]
 THEN MemberAdd(w,c,m,s) /\ UNCHANGED <<activeEpoch,seenEpoch>>
 ELSE EpochRefuse(w,"epoch-add-refused")
EpochRemove(w,c,m) ==
 IF Current(w,c[1]) /\ c[1][2]=MemberEpoch[m]
 THEN MemberRemove(w,c,m) /\ UNCHANGED <<activeEpoch,seenEpoch>>
 ELSE EpochRefuse(w,"epoch-remove-refused")
EpochMove(w,c,d,m) ==
 IF Current(w,c[1]) /\ c[1][2]=MemberEpoch[m]
 THEN MemberMove(w,c,d,m) /\ UNCHANGED <<activeEpoch,seenEpoch>>
 ELSE EpochRefuse(w,"epoch-move-refused")
EpochClear(w,t) ==
 IF Current(w,t) THEN MemberClear(w,t) /\ UNCHANGED <<activeEpoch,seenEpoch>>
 ELSE EpochRefuse(w,"epoch-clear-refused")
EpochDrop(w,t) ==
 IF Current(w,t) THEN MemberDrop(w,t) /\ UNCHANGED <<activeEpoch,seenEpoch>>
 ELSE EpochRefuse(w,"epoch-drop-refused")
EpochCreate(w,t) ==
 IF Current(w,t) THEN MemberCreate(w,t) /\ UNCHANGED <<activeEpoch,seenEpoch>>
 ELSE EpochRefuse(w,"epoch-create-refused")
EpochRowDelete(w,t,r) ==
 IF Current(w,t) THEN MemberRowDelete(w,t,r) /\ UNCHANGED <<activeEpoch,seenEpoch>>
 ELSE EpochRefuse(w,"epoch-row-delete-refused")
EpochRowAdd(w,t,r,nb) ==
 IF Current(w,t) THEN MemberRowAdd(w,t,r,nb) /\ UNCHANGED <<activeEpoch,seenEpoch>>
 ELSE EpochRefuse(w,"epoch-row-add-refused")
EpochBind(w,t,keep,nb) ==
 IF Current(w,t) THEN MemberBind(w,t,keep,nb) /\ UNCHANGED <<activeEpoch,seenEpoch>>
 ELSE EpochRefuse(w,"epoch-bind-refused")
ReadEpoch(w) ==
 /\ MemberRefuse(w,"read-epoch")
 /\ UNCHANGED activeEpoch
 /\ seenEpoch'=[seenEpoch EXCEPT ![w]=activeEpoch]
Advance(w) ==
 /\ activeEpoch+1 \in Epochs
 /\ seenEpoch[w]=activeEpoch
 /\ MemberRefuse(w,"advance-epoch")
 /\ activeEpoch'=activeEpoch+1
 /\ UNCHANGED seenEpoch
\* Add/Move/shape changes validate the caller's observed namespace before
\* commit. After Advance, even that writer must refresh before mutating.
EpochNext ==
 /\ (MaxSteps=0 \/ step < MaxSteps)
 /\ \E w \in Writers:
    \/ ReadEpoch(w) \/ Advance(w)
    \/ \E c \in Cells,m \in Members,s \in Scores:EpochAdd(w,c,m,s)
    \/ \E c \in Cells,m \in Members:EpochRemove(w,c,m)
    \/ \E c,d \in Cells,m \in Members:c[1]=d[1] /\ EpochMove(w,c,d,m)
    \/ \E t \in Tables:EpochClear(w,t) \/ EpochDrop(w,t) \/ EpochCreate(w,t)
    \/ \E t \in Tables,r \in Rows:EpochRowDelete(w,t,r)
    \/ \E t \in Tables,r \in Rows:
         \E nb \in BindingChoices(RowCells(t,r)):EpochRowAdd(w,t,r,nb)
    \/ \E t \in Tables,keep \in SUBSET Rows:
         \E nb \in BindingChoices({t} \X keep \X Columns):EpochBind(w,t,keep,nb)
EpochSpec == EpochInit /\ [][EpochNext \/ (step=MaxSteps /\ UNCHANGED xvars)]_xvars

\* Negative control: omit the member epoch guard, allowing a record from
\* another generation into the current table while keeping its set link.
BrokenEpochNext ==
 /\ (MaxSteps=0 \/ step < MaxSteps)
 /\ \E w \in Writers,c \in Cells,m \in Members,s \in Scores:
       /\ Current(w,c[1])
       /\ MemberAdd(w,c,m,s)
       /\ UNCHANGED <<activeEpoch,seenEpoch>>
BrokenEpochSpec == EpochInit /\ [][BrokenEpochNext \/ (step=MaxSteps /\ UNCHANGED xvars)]_xvars

\* These temporal safety checks constrain actual before/after data, not
\* merely the computed placement count in each state.
UserState == <<live,rows,binds,data,place>>
StaleWritesRefuse == [][
 (IF writer' \in Writers THEN seenEpoch[writer'] # activeEpoch ELSE FALSE)
 /\ op' \notin {"read-epoch","advance-epoch"} => UNCHANGED UserState]_xvars
EpochAdvanceKeepsHistory == [][activeEpoch' # activeEpoch => UNCHANGED UserState]_xvars
AdvanceStartsEmpty == [][activeEpoch' # activeEpoch =>
 ~\E e \in data':e[1] # External /\ e[1][1][2]=activeEpoch']_xvars
OldEpochFrozen == [][\A t \in Tables:t[2]<activeEpoch =>
 /\ At(data',TableCells(t))=At(data,TableCells(t))
 /\ {r \in rows':r[1]=t}={r \in rows:r[1]=t}
 /\ (t \in live' <=> t \in live)
 /\ {b \in binds':b[1][1]=t}={b \in binds:b[1][1]=t}
 /\ \A m \in Members:place'[m][t]=place[m][t]]_xvars

\* Mutation control: a stale writer calls the otherwise-correct member
\* protocol after Advance, modifying the retained historical generation.
BrokenStaleNext ==
 /\ (MaxSteps=0 \/ step<MaxSteps)
 /\ \E w \in Writers:
    \/ Advance(w)
    \/ \E c \in Cells,m \in Members,s \in Scores:
         /\ seenEpoch[w] # activeEpoch /\ c[1][2]=MemberEpoch[m]
         /\ MemberAdd(w,c,m,s) /\ UNCHANGED <<activeEpoch,seenEpoch>>
BrokenStaleSpec == EpochInit /\ [][BrokenStaleNext \/ (step=MaxSteps /\ UNCHANGED xvars)]_xvars
=============================================================================
