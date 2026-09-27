-------------------------- MODULE TableMachine --------------------------
\* CURRENT behavior of nova-tools table.lua at f77458853af46fdbbafd6881a4b46006431f266f.
\* Successful functions are atomic steps. Error preflight is assumed, not
\* proved here. Duplicate placement and lossy bind are deliberately present.
EXTENDS Naturals, FiniteSets
CONSTANTS Tables, Rows, Columns, Members, Writers, Scores, External,
          MaxSteps, AllowAliases, Seed
Cells == Tables \X Rows \X Columns
Keys == Cells \cup {External}
Entries == Keys \X Members \X Scores
VARIABLES live, rows, binds, data, step, writer, op, checks
vars == <<live, rows, binds, data, step, writer, op, checks>>
Shape(ts,rs) == {c \in Cells : c[1] \in ts /\ <<c[1],c[2]>> \in rs}
Active == Shape(live,rows)
Bound(bs) == {b[1] : b \in bs}
Targets(bs) == {b[2] : b \in bs}
Owned(ts,rs,bs) == Shape(ts,rs) \ Bound(bs)
At(ds,ks) == {e \in ds : e[1] \in ks}
MembersAt(ds,ks) == {e[2] : e \in At(ds,ks)}
Erase(ds,ks) == ds \ At(ds,ks)
EraseMember(ds,k,m) == {e \in ds : ~(e[1]=k /\ e[2]=m)}
Put(ds,k,m,s) == EraseMember(ds,k,m) \cup {<<k,m,s>>}
RowCells(t,r) == {c \in Cells : c[1]=t /\ c[2]=r}
TableCells(t) == {c \in Cells : c[1]=t}
TableRows(t) == {r \in rows : r[1]=t}
Writable(c) == c \in Owned(live,rows,binds)
BindingChoices(cs) == {{}} \cup
  {{<<c,k>>} : c \in cs, k \in (IF AllowAliases THEN Keys ELSE {External})}
\* Workload bound: at most one binding introduced per RowAdd/Bind call.
\* Several bindings can accumulate across RowAdd calls.
CheckNames == {"move","bound","clear","dropBound","bind","external"}
TypeOK ==
 /\ live \subseteq Tables
 /\ rows \subseteq Tables \X Rows
 /\ \A r \in rows : r[1] \in live
 /\ binds \subseteq Cells \X Keys
 /\ Bound(binds) \subseteq Active
 /\ \A a,b \in binds : a[1]=b[1] => a[2]=b[2]
 /\ data \subseteq Entries
 /\ \A a,b \in data : (a[1]=b[1] /\ a[2]=b[2]) => a[3]=b[3]
 /\ step \in 0..MaxSteps
 /\ writer \in Writers \cup {"initial"}
 /\ checks \in [CheckNames -> BOOLEAN]
Init ==
 /\ live = Tables
 /\ rows = Tables \X Rows
 /\ binds = {}
 /\ data = Seed
 /\ step = 0
 /\ writer = "initial"
 /\ op = "initial"
 /\ checks = [k \in CheckNames |-> TRUE]
\* Sticky audit flags retain action postcondition violations for TLC.
Commit(w,kind,ts,rs,bs,ds,moveGood,clearGood,dropGood,bindGood) ==
 /\ live'=ts /\ rows'=rs /\ binds'=bs /\ data'=ds
 /\ step'=(IF MaxSteps=0 THEN 0 ELSE step+1) /\ writer'=w /\ op'=kind
 /\ checks' = [checks EXCEPT
      !["move"] = @ /\ moveGood,
      !["bound"] = @ /\ (kind \in {"add","remove","move"} =>
                           At(ds,Targets(binds))=At(data,Targets(binds))),
      !["clear"] = @ /\ clearGood,
      !["dropBound"] = @ /\ dropGood,
      !["bind"] = @ /\ bindGood,
      !["external"] = @ /\ (At(ds,{External})=At(data,{External}))]
Refuse(w,kind) == Commit(w,kind,live,rows,binds,data,TRUE,TRUE,TRUE,TRUE)
\* T.cell / T.writecell: bound or absent targets refuse before mutation.
Add(w,c,m,s) ==
 IF Writable(c)
 THEN Commit(w,"add",live,rows,binds,Put(data,c,m,s),TRUE,TRUE,TRUE,TRUE)
 ELSE Refuse(w,"add-refused")
Remove(w,c,m) ==
 IF Writable(c)
 THEN Commit(w,"remove",live,rows,binds,EraseMember(data,c,m),TRUE,TRUE,TRUE,TRUE)
 ELSE Refuse(w,"remove-refused")
Move(w,c,d,m) ==
 LET es == {e \in data : e[1]=c /\ e[2]=m}
 IN IF Writable(c) /\ Writable(d) /\ es # {}
 THEN LET e == CHOOSE x \in es : TRUE
          ds == Put(EraseMember(data,c,m),d,m,e[3])
      IN Commit(w,"move",live,rows,binds,ds,
           <<d,m,e[3]>> \in ds /\ (c=d \/ ~\E x \in ds : x[1]=c /\ x[2]=m),
           TRUE,TRUE,TRUE)
 ELSE Refuse(w,"move-refused")
\* Current cell move is between columns of the SAME row only.
\* T.remove deletes unbound owned keys even when another view aliases them.
DeleteRows(w,kind,t,removed,drop) ==
 LET cs == {c \in Active : <<c[1],c[2]>> \in removed}
     ds == Erase(data,cs \ Bound(binds))
     rs == rows \ removed
     bs == {b \in binds : b[1] \notin cs}
     ts == IF drop THEN live \ {t} ELSE live
 IN Commit(w,kind,ts,rs,bs,ds,TRUE,
      IF kind="clear" THEN t \in ts /\ ~\E c \in Shape(ts,rs) : c[1]=t ELSE TRUE,
      IF drop THEN At(ds,Targets(binds))=At(data,Targets(binds)) ELSE TRUE,TRUE)
Clear(w,t) ==
 IF t \notin live \/ (Bound(binds) \cap TableCells(t)) # {}
 THEN Refuse(w,"clear-refused")
 ELSE DeleteRows(w,"clear",t,TableRows(t),FALSE)
Drop(w,t) ==
 IF t \notin live THEN Refuse(w,"drop-refused")
 ELSE DeleteRows(w,"drop",t,TableRows(t),TRUE)
RowDelete(w,t,r) ==
 IF t \notin live THEN Refuse(w,"row-delete-refused")
 ELSE IF <<t,r>> \notin rows THEN Refuse(w,"row-delete-noop")
 ELSE DeleteRows(w,"row-delete",t,{<<t,r>>},FALSE)
Create(w,t) == Commit(w,"create",live \cup {t},rows,binds,data,TRUE,TRUE,TRUE,TRUE)
\* ns_table_row_add rewrites bindings, retains physical cell sets.
RowAdd(w,t,r,nb) ==
 IF t \notin live THEN Refuse(w,"row-add-refused")
 ELSE LET bs == {b \in binds : b[1] \notin RowCells(t,r)} \cup nb
      IN Commit(w,"row-add",live,rows \cup {<<t,r>>},bs,data,TRUE,TRUE,TRUE,TRUE)
\* ns_table_bind rewrites kept rows, removes omitted rows, creates if absent.
Bind(w,t,keep,nb) ==
 LET rs == (rows \ TableRows(t)) \cup ({t} \X keep)
     gone == {c \in Active : c[1]=t /\ c[2] \notin keep}
     bs == {b \in binds : b[1] \notin TableCells(t)} \cup nb
     ds == Erase(data,gone \ Bound(binds))
     ts == live \cup {t}
 IN Commit(w,"bind",ts,rs,bs,ds,TRUE,TRUE,TRUE,
      MembersAt(data,Owned(live,rows,binds) \cap TableCells(t))
       \subseteq MembersAt(ds,Owned(ts,rs,bs) \cap TableCells(t)))
Next ==
 /\ (MaxSteps=0 \/ step < MaxSteps)
 /\ \E w \in Writers :
    \/ \E c \in Cells, m \in Members, s \in Scores : Add(w,c,m,s)
    \/ \E c \in Cells, m \in Members : Remove(w,c,m)
    \/ \E c \in Cells, col \in Columns, m \in Members : Move(w,c,<<c[1],c[2],col>>,m)
    \/ \E t \in Tables : Clear(w,t) \/ Drop(w,t) \/ Create(w,t)
    \/ \E t \in Tables, r \in Rows : RowDelete(w,t,r)
    \/ \E t \in Tables, r \in Rows : \E nb \in BindingChoices(RowCells(t,r)) : RowAdd(w,t,r,nb)
    \/ \E t \in Tables, keep \in SUBSET Rows :
       \E nb \in BindingChoices({t} \X keep \X Columns) : Bind(w,t,keep,nb)
\* MaxSteps=0 explores to a fixed point; positive values bound calls.
\* Terminal stutter makes the explicit depth bound non-deadlocking.
BoundedNext == Next \/ (step=MaxSteps /\ UNCHANGED vars)
Spec == Init /\ [][Next]_vars
BoundedSpec == Init /\ [][BoundedNext]_vars
RetainedBindNext ==
 /\ (MaxSteps=0 \/ step < MaxSteps)
 /\ \E w \in Writers, t \in Tables :
       \E nb \in BindingChoices(TableCells(t)) : Bind(w,t,Rows,nb)
RetainedBindSpec == Init /\ [][RetainedBindNext \/ (step=MaxSteps /\ UNCHANGED vars)]_vars
OnePlacePerTable == \A t \in Tables, m \in Members :
 Cardinality({c \in Owned(live,rows,binds) : c[1]=t /\ \E e \in data : e[1]=c /\ e[2]=m}) <= 1
OnePlaceAcrossTables == \A m \in Members :
 Cardinality({c \in Owned(live,rows,binds) : \E e \in data : e[1]=c /\ e[2]=m}) <= 1
OneTablePerMember == \A m \in Members :
 Cardinality({t \in Tables : \E c \in Owned(live,rows,binds) : c[1]=t /\ \E e \in data : e[1]=c /\ e[2]=m}) <= 1
MovePreservesScore == checks["move"]
CellWritesPreserveBoundSets == checks["bound"]
ClearKeepsDefinition == checks["clear"]
DropPreservesBound == checks["dropBound"]
BindPreservesOwned == checks["bind"]
ExternalUntouched == checks["external"]
=============================================================================
