-------------------------- MODULE MemberTable --------------------------
\* PROPOSED protocol, not today's Lua: one record location per table,
\* updated atomically with the owned cell. Extends the pinned baseline's
\* concrete state so every extra guard and record write is visible here.
EXTENDS TableMachine
VARIABLE place
mvars == <<vars,place>>
NoPlace == <<"none","none","none">>
Places(ds,ts,rs,bs,m,t) ==
 {c \in Owned(ts,rs,bs) : c[1]=t /\ \E e \in ds : e[1]=c /\ e[2]=m}
MemberInit ==
 /\ Init
 /\ place = [m \in Members |-> [t \in Tables |->
       IF Places(data,live,rows,binds,m,t)={} THEN NoPlace
       ELSE CHOOSE c \in Places(data,live,rows,binds,m,t) : TRUE]]
MemberTypeOK ==
 /\ TypeOK
 /\ place \in [Members -> [Tables -> Cells \cup {NoPlace}]]
 /\ \A m \in Members, t \in Tables : place[m][t] \in TableCells(t) \cup {NoPlace}
RecordSetLink == \A m \in Members, t \in Tables :
 Places(data,live,rows,binds,m,t) = (IF place[m][t]=NoPlace THEN {} ELSE {place[m][t]})
ExternalBindings(nb) == Targets(nb) \subseteq {External}
NoOwnedAlias == ExternalBindings(binds)
NoHiddenOwned == \A e \in data : e[1]=External \/ e[1] \in Owned(live,rows,binds)
ClearRecord(t) == [m \in Members |-> [place[m] EXCEPT ![t]=NoPlace]]
MemberRefuse(w,why) == Refuse(w,why) /\ UNCHANGED place

MemberAdd(w,c,m,s) ==
 IF Writable(c) /\ place[m][c[1]]=NoPlace
 THEN Add(w,c,m,s) /\ place'=[place EXCEPT ![m][c[1]]=c]
 ELSE MemberRefuse(w,"add-refused")
MemberRemove(w,c,m) ==
 IF Writable(c) /\ place[m][c[1]]=c
 THEN Remove(w,c,m) /\ place'=[place EXCEPT ![m][c[1]]=NoPlace]
 ELSE MemberRefuse(w,"remove-noop-or-refused")
MemberMove(w,c,d,m) ==
 IF Writable(c) /\ Writable(d) /\ place[m][c[1]]=c
 THEN Move(w,c,d,m) /\ place'=[place EXCEPT ![m][c[1]]=d]
 ELSE MemberRefuse(w,"move-refused")
\* Proposed lossless reshape: every currently owned entry remains owned
\* and visible in the new shape. Deleting an occupied row or rebinding an
\* occupied cell must first use the explicit member move/remove verb.
ShapeKeepsOwned(t,rs,bs) ==
 (Owned(live,rows,binds) \cap TableCells(t)) \cap {e[1]:e \in data}
   \subseteq Owned(live \cup {t},rs,bs)
MemberRowAdd(w,t,r,nb) ==
 LET rs == rows \cup {<<t,r>>}
     bs == {b \in binds : b[1] \notin RowCells(t,r)} \cup nb
 IN IF t \in live /\ ExternalBindings(nb) /\ ShapeKeepsOwned(t,rs,bs)
    THEN RowAdd(w,t,r,nb) /\ UNCHANGED place
    ELSE MemberRefuse(w,"row-add-refused")
MemberBind(w,t,keep,nb) ==
 LET rs == (rows \ TableRows(t)) \cup ({t} \X keep)
     bs == {b \in binds : b[1] \notin TableCells(t)} \cup nb
 IN IF ExternalBindings(nb) /\ ShapeKeepsOwned(t,rs,bs)
    THEN Bind(w,t,keep,nb) /\ UNCHANGED place
    ELSE MemberRefuse(w,"bind-refused")
MemberClear(w,t) ==
 IF t \in live /\ (Bound(binds) \cap TableCells(t))={}
 THEN Clear(w,t) /\ place'=ClearRecord(t)
 ELSE MemberRefuse(w,"clear-refused")
MemberDrop(w,t) == Drop(w,t) /\ place'=ClearRecord(t)
MemberRowDelete(w,t,r) ==
 /\ RowDelete(w,t,r)
 /\ place'=[m \in Members |-> [place[m] EXCEPT
      ![t]=IF @ \in RowCells(t,r) THEN NoPlace ELSE @]]
MemberCreate(w,t) == Create(w,t) /\ UNCHANGED place

MemberNext ==
 /\ (MaxSteps=0 \/ step < MaxSteps)
 /\ \E w \in Writers :
    \/ \E c \in Cells, m \in Members, s \in Scores : MemberAdd(w,c,m,s)
    \/ \E c \in Cells, m \in Members : MemberRemove(w,c,m)
    \/ \E c,d \in Cells, m \in Members : c[1]=d[1] /\ MemberMove(w,c,d,m)
    \/ \E t \in Tables : MemberClear(w,t) \/ MemberDrop(w,t) \/ MemberCreate(w,t)
    \/ \E t \in Tables, r \in Rows : MemberRowDelete(w,t,r)
    \/ \E t \in Tables, r \in Rows :
         \E nb \in BindingChoices(RowCells(t,r)) : MemberRowAdd(w,t,r,nb)
    \/ \E t \in Tables, keep \in SUBSET Rows :
         \E nb \in BindingChoices({t} \X keep \X Columns) : MemberBind(w,t,keep,nb)
MemberSpec == MemberInit /\ [][MemberNext \/ (step=MaxSteps /\ UNCHANGED mvars)]_mvars

\* Negative control: forgetting the record write during a real cell move
\* must fail RecordSetLink. It is not an allowed production transition.
BrokenMoveNext ==
 /\ (MaxSteps=0 \/ step < MaxSteps)
 /\ \E w \in Writers, c,d \in Cells, m \in Members :
       /\ c[1]=d[1] /\ c # d /\ Writable(c) /\ Writable(d)
       /\ place[m][c[1]]=c
       /\ Move(w,c,d,m)
       /\ UNCHANGED place
BrokenMoveSpec == MemberInit /\ [][BrokenMoveNext \/ (step=MaxSteps /\ UNCHANGED mvars)]_mvars

\* Mutation control: removing the external-only guard must be detected even
\* when the aliased cell has no members and the record link still holds.
BrokenAliasNext ==
 /\ (MaxSteps=0 \/ step < MaxSteps)
 /\ \E w \in Writers,t \in Tables,r \in Rows:
      \E nb \in BindingChoices(RowCells(t,r)):
       /\ ShapeKeepsOwned(t,rows \cup {<<t,r>>},
             {b \in binds:b[1] \notin RowCells(t,r)} \cup nb)
       /\ RowAdd(w,t,r,nb) /\ UNCHANGED place
BrokenAliasSpec == MemberInit /\ [][BrokenAliasNext \/ (step=MaxSteps /\ UNCHANGED mvars)]_mvars
=============================================================================
