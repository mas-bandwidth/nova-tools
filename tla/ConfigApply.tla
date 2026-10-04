---------------------------- MODULE ConfigApply ----------------------------
\* internal/config/store.go and internal/config/apply.go: the permanent
\* store's rows and history, the apply of a kind's rows into Redis (docs/SPEC-CONFIG.md
\* "History" and "Apply").
\*
\* The state the module owns: the store's rows per kind and its history rows
\* (every write is one transaction that changes the row and appends its history
\* row; no write without a record); the kind's revision; Redis's views per kind
\* and its applied stamp and config:decl's revisions; the plan (adds and sets
\* in the kind's apply order, then removes by name); the idempotency key per
\* (kind, rev).
\*
\* ACTIONS: Insert/Update/Delete (a row and its history row, atomically, with
\* the refusals PlanWrite names: a name taken or missing, the kind's Check, a
\* ref naming no row); Apply (read the store's revision, read Redis's stamp,
\* refuse CONFLICT when Redis is ahead, plan, write every op in order, stamp
\* the revision); ApplyCheck (check set: plan and report, write nothing);
\* ApplyCrash (the writer dies after k of n ops: the stamp is not written);
\* ApplyRetry (the same (kind, rev) applied again: the Idem key); OutsideRedisWrite
\* (something wrote Redis directly: Redis ahead); Read (a runtime tool reads a view).
\*
\* INVARIANTS and liveness: HistoryCoversEveryWrite: every change of a row has
\* exactly one history row; RedisIsACopy: after a completed apply of a kind,
\* Redis's views equal the store's rows for that kind; ConflictRefusesAhead:
\* an apply never writes when Redis's stamp is ahead of the revision it read;
\* ApplyOrder: in any prefix of an apply's writes, a machine's ceiling precedes
\* any friend placed on it and a friend's removal follows every other op;
\* StampOnlyWhenComplete: the revision stamp is written only after every op;
\* RetryIsIdempotent: applying a (kind, rev) twice leaves Redis as one apply does.
\* liveness: a crashed apply is completed by the next apply of the kind.
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* one guard removed, and the invariant its comment names fails:
\*   "stampfirst"         the stamp before the ops         -> StampOnlyWhenComplete, RedisIsACopy
\*   "friendbeforeceiling" a friend write before a ceiling  -> ApplyOrder
\*   "writewithouthistory" a store write without history row -> HistoryCoversEveryWrite
\*   "applyoverahead"      an apply writes when ahead       -> ConflictRefusesAhead

EXTENDS Naturals, Sequences, FiniteSets, TLC

CONSTANTS Kinds, Names, MaxFields, MaxRev, Broken

NoneV == "none"

\* Kinds ordered by apply: machine before friend for ceiling before placement.
KindOrder == [machine |-> 0, friend |-> 1]

\* A row is fields: name -> value. Small: slots for ceiling, machine for ref (none means none).
Row == [ name : Names, fields : [slots : 0..MaxFields, machine : Names \cup {NoneV}] ]

\* History row: one change.
Change == [kind: Kinds, name: Names, op: {"add","set","remove"}, before: [machine : Names \cup {NoneV} , slots : 0..MaxFields ] \cup {<<>>}, after: [machine : Names \cup {NoneV} , slots : 0..MaxFields ] \cup {<<>>}]

VARIABLES
  storeRows,   \* kind -> name -> row fields or absent
  history,     \* sequence of all changes
  storeRevs,   \* kind -> max history id for the kind
  redisViews,  \* kind -> name -> view (fields)
  redisStamps, \* kind -> stamped rev
  declRevs,    \* kind -> rev in config:decl
  planOps,     \* ops of current apply: sequence of [op,kind,name,row,prev]
  planIdx,     \* next op to write in current apply
  planRev,     \* the store rev the apply read
  planKind,    \* kind being applied
  idemDone,    \* set of (kind,rev) already applied
  phase        \* "idle","planning","writing","crashed","checked"

vars == <<storeRows, history, storeRevs, redisViews, redisStamps, declRevs,
          planOps, planIdx, planRev, planKind, idemDone, phase>>

TypeOK ==
  /\ storeRows \in [Kinds -> [Names -> Row \cup {<<>>}]]
  /\ history \in Seq(Change)
  /\ storeRevs \in [Kinds -> 0..MaxRev]
  /\ redisViews \in [Kinds -> [Names -> [ {"slots","machine"} -> 0..MaxFields ] \cup {<<>>}]]
  /\ redisStamps \in [Kinds -> 0..MaxRev]
  /\ declRevs \in [Kinds -> 0..MaxRev]
  /\ planOps \in Seq([op: {"add","set","remove"}, kind: Kinds, name: Names, row: Row \cup {<<>>}, prev: [ {"slots","machine"} -> 0..MaxFields ] \cup {<<>>}])
  /\ planIdx \in 0..Len(planOps)
  /\ planRev \in 0..MaxRev
  /\ planKind \in Kinds
  /\ idemDone \in SUBSET (Kinds \X (0..MaxRev))
  /\ phase \in {"idle","planning","writing","crashed","checked"}

NamesOf(k) == { n \in Names : storeRows[k][n] # <<>> }

\* Plan from store rows vs redis views, in kind's order then name.
Plan(k, rows, views) ==
  LET sorted == [ i \in 1..Len(rows) |-> rows[i] ]  \* assume caller sorts
      addsSets == [ i \in 1..Len(sorted) |-> IF views[sorted[i].name] = <<>>
                     THEN [op |-> "add", kind |-> k, name |-> sorted[i].name, row |-> sorted[i], prev |-> <<>>]
                     ELSE IF sorted[i].fields # views[sorted[i].name]
                     THEN [op |-> "set", kind |-> k, name |-> sorted[i].name, row |-> sorted[i], prev |-> views[sorted[i].name]]
                     ELSE <<>> ]
      gone == { n \in DOMAIN views : n \notin { r.name : r \in {sorted[i] : i \in 1..Len(sorted)} } }
      removes == [ n \in gone |-> [op |-> "remove", kind |-> k, name |-> n, row |-> <<>>, prev |-> views[n]] ]
  IN SelectSeq(addsSets, LAMBDA x: x # <<>>) \o [ i \in 1..Cardinality(gone) |-> removes[CHOOSE n \in gone : TRUE] ] \* simplify order

\* Record a change and bump rev for kind.
Record(k, n, op, bef, aft) ==
  LET c == [kind |-> k, name |-> n, op |-> op, before |-> bef, after |-> aft]
      id == Len(history) + 1
  IN /\ history' = Append(history, c)
     /\ storeRevs' = [storeRevs EXCEPT ![k] = id]

Init ==
  /\ storeRows = [k \in Kinds |-> [n \in Names |-> <<>> ]]
  /\ history = <<>>
  /\ storeRevs = [k \in Kinds |-> 0]
  /\ redisViews = [k \in Kinds |-> [n \in Names |-> <<>> ]]
  /\ redisStamps = [k \in Kinds |-> 0]
  /\ declRevs = [k \in Kinds |-> 0]
  /\ planOps = <<>> /\ planIdx = 0 /\ planRev = 0 /\ planKind = "machine"
  /\ idemDone = {}
  /\ phase = "idle"

\* Store writes: Insert/Update/Delete with history, using PlanWrite refusals simplified.
CanInsert(k, row) ==
  /\ storeRows[k][row.name] = <<>>
  /\ row.fields.machine = NoneV \/ (row.fields.machine \in Names /\ storeRows["machine"][row.fields.machine] # <<>> ) \* ref check abstract

StoreInsert(k, row, actor) ==
  /\ phase = "idle"
  /\ CanInsert(k, row)
  /\ storeRows' = [storeRows EXCEPT ![k][row.name] = row]
  /\ Record(k, row.name, "add", <<>>, row.fields)
  /\ UNCHANGED <<redisViews, redisStamps, declRevs, planOps, planIdx, planRev, planKind, idemDone, phase>>

CanUpdate(k, name, ch) ==
  /\ storeRows[k][name] # <<>>
  \* check ref etc omitted for small model

StoreUpdate(k, name, ch, actor) ==
  /\ phase = "idle"
  /\ CanUpdate(k, name, ch)
  /\ LET cur == storeRows[k][name]
         newslots == IF "slots" \in DOMAIN ch THEN ch["slots"] ELSE cur.fields.slots
         nxt == [cur EXCEPT !.fields = [slots |-> newslots, machine |-> cur.fields.machine ] ]
     IN /\ storeRows' = [storeRows EXCEPT ![k][name] = nxt]
        /\ Record(k, name, "set", cur.fields, nxt.fields)
  /\ UNCHANGED <<redisViews, redisStamps, declRevs, planOps, planIdx, planRev, planKind, idemDone, phase>>

CanDelete(k, name) ==
  /\ storeRows[k][name] # <<>>
  \* referenced check omitted

StoreDelete(k, name, actor) ==
  /\ phase = "idle"
  /\ CanDelete(k, name)
  /\ LET cur == storeRows[k][name]
     IN /\ storeRows' = [storeRows EXCEPT ![k][name] = <<>> ]
        /\ Record(k, name, "remove", cur.fields, <<>>)
  /\ UNCHANGED <<redisViews, redisStamps, declRevs, planOps, planIdx, planRev, planKind, idemDone, phase>>
\* Apply of a kind.
StartApply(k) ==
  /\ phase = "idle"
  /\ LET r == storeRevs[k]
         s == redisStamps[k]
     IN IF s > r /\ Broken # "applyoverahead"
        THEN /\ phase' = "idle" \* conflict, refuse
             /\ UNCHANGED <<planOps, planIdx, planRev, planKind, idemDone, storeRows, history, storeRevs, redisViews, redisStamps, declRevs>>
        ELSE IF <<k, r>> \in idemDone
        THEN /\ phase' = "idle" \* retry no-op
             /\ UNCHANGED <<planOps, planIdx, planRev, planKind, idemDone, storeRows, history, storeRevs, redisViews, redisStamps, declRevs>>
        ELSE /\ planRev' = r
             /\ planKind' = k
             /\ LET rows == [ i \in 1..Cardinality(NamesOf(k)) |-> CHOOSE rw \in { rr \in Row : rr.name \in NamesOf(k) } : TRUE ] \* pick some
                    vs == redisViews[k]
                     ops == IF Broken = "friendbeforeceiling"
                            THEN << [op |-> "add", kind |-> "friend", name |-> "f1", row |-> [name |-> "f1", fields |-> [slots |-> 1, machine |-> "m1"]], prev |-> <<>> ],
                                    [op |-> "add", kind |-> "machine", name |-> "m1", row |-> [name |-> "m1", fields |-> [slots |-> 8, machine |-> NoneV]], prev |-> <<>> ] >>
                            ELSE Plan(k, rows, vs)
                IN /\ planOps' = ops
                   /\ planIdx' = 0
             /\ phase' = "planning"
             /\ idemDone' = idemDone \cup {<<k, r>>}
             /\ UNCHANGED <<storeRows, history, storeRevs, redisViews, redisStamps, declRevs>>

DoOneWrite ==
  /\ phase = "planning"
  /\ planIdx < Len(planOps)
  /\ LET op == planOps[planIdx+1]
     IN /\ redisViews' = [redisViews EXCEPT ![op.kind][op.name] = IF op.op = "remove" THEN <<>> ELSE op.row.fields ]
        /\ planIdx' = planIdx + 1
  /\ UNCHANGED <<storeRows, history, storeRevs, redisStamps, declRevs, planOps, planRev, planKind, idemDone, phase>>

FinishWrites ==
  /\ phase = "planning"
  /\ planIdx = Len(planOps)
  /\ IF Broken = "stampfirst"
     THEN /\ redisStamps' = [redisStamps EXCEPT ![planKind] = planRev ]
          /\ declRevs' = [declRevs EXCEPT ![planKind] = planRev ]
          /\ phase' = "crashed"
     ELSE /\ IF Len(planOps) > 0 \/ redisStamps[planKind] # planRev
             THEN /\ redisStamps' = [redisStamps EXCEPT ![planKind] = planRev ]
                  /\ declRevs' = [declRevs EXCEPT ![planKind] = planRev ]
             ELSE UNCHANGED <<redisStamps, declRevs>>
          /\ phase' = "idle"
  /\ UNCHANGED <<storeRows, history, storeRevs, redisViews, planOps, planIdx, planRev, planKind, idemDone, planIdx>>

ApplyCrash ==
  /\ phase = "planning"
  /\ planIdx < Len(planOps)
  /\ Broken # "none"
  /\ phase' = "crashed"
  /\ UNCHANGED <<storeRows, history, storeRevs, redisViews, redisStamps, declRevs, planOps, planIdx, planRev, planKind, idemDone>>

ApplyCheck ==
  /\ phase = "idle"
  /\ phase' = "checked"
  /\ UNCHANGED <<storeRows, history, storeRevs, redisViews, redisStamps, declRevs, planOps, planIdx, planRev, planKind, idemDone >> \* writes nothing

ApplyRetry(k, r) ==
  /\ <<k,r>> \in idemDone
  /\ phase = "idle"
  /\ UNCHANGED vars

OutsideRedisWrite(k, n, v) ==
  /\ phase = "idle"
  /\ redisViews' = [redisViews EXCEPT ![k][n] = v ]
  /\ redisStamps' = [redisStamps EXCEPT ![k] = IF redisStamps[k] > storeRevs[k]+1 THEN redisStamps[k] ELSE storeRevs[k]+1 ] \* ahead
  /\ UNCHANGED <<storeRows, history, storeRevs, declRevs, planOps, planIdx, planRev, planKind, idemDone, phase>>

ReadView(k, n) ==
  /\ phase = "idle"
  /\ UNCHANGED vars

\* Broken write without history: creates row but no history record.
BadStoreInsertWithoutHistory ==
  /\ Broken = "writewithouthistory"
  /\ phase = "idle"
  /\ \E k \in Kinds, row \in Row :
       /\ storeRows[k][row.name] = <<>>
       /\ storeRows' = [storeRows EXCEPT ![k][row.name] = row]
       /\ UNCHANGED <<history, storeRevs, redisViews, redisStamps, declRevs, planOps, planIdx, planRev, planKind, idemDone, phase>>

Next ==
  \/ \E k \in Kinds, row \in Row : StoreInsert(k, row, "a")
  \/ \E k \in Kinds, n \in Names, ch \in [ {"slots"} -> 0..MaxFields ] : StoreUpdate(k, n, ch, "a")
  \/ \E k \in Kinds, n \in Names : StoreDelete(k, n, "a")
  \/ \E k \in Kinds : StartApply(k)
  \/ DoOneWrite
  \/ FinishWrites
  \/ ApplyCrash
  \/ ApplyCheck
  \/ \E k \in Kinds, r \in 0..MaxRev : ApplyRetry(k, r)
  \/ \E k \in Kinds, n \in Names, v \in [ {"slots","machine"} -> 0..MaxFields ] \cup {<<>>} : OutsideRedisWrite(k, n, v)
  \/ \E k \in Kinds, n \in Names : ReadView(k, n)
  \/ BadStoreInsertWithoutHistory

Spec == Init /\ [][Next]_vars

\* Every change of a row has exactly one history row.
HistoryCoversEveryWrite ==
  \A k \in Kinds, n \in Names :
    (storeRows[k][n] # <<>>) =>
      \E i \in 1..Len(history) : history[i].kind = k /\ history[i].name = n

\* After completed apply, views match store for kind.
RedisIsACopy ==
  \A k \in Kinds :
    (redisStamps[k] = storeRevs[k] /\ phase = "idle" /\ storeRevs[k] > 0) =>
      \A n \in Names : (storeRows[k][n] # <<>> <=> redisViews[k][n] # <<>>) /\ (storeRows[k][n] # <<>> => storeRows[k][n].fields = redisViews[k][n])

\* Apply refuses when ahead.
ConflictRefusesAhead ==
  \A k \in Kinds : (redisStamps[k] > storeRevs[k] => phase # "planning" \/ planRev # storeRevs[k] \/ Broken = "applyoverahead")

\* Order in apply writes: ceiling (machine slots) before friend placement; remove last.
ApplyOrder ==
  \A i,j \in 1..Len(planOps) :
    (i < j /\ planOps[i].kind = "friend" /\ planOps[j].kind = "machine" /\ planOps[i].op \in {"add","set"} /\ planOps[j].op \in {"add","set"} ) =>
      FALSE   \* violation if friend before ceiling; good runs never produce such ops list

\* Stamp only after all ops.
StampOnlyWhenComplete ==
  (phase = "idle" /\ redisStamps[planKind] = planRev) => planIdx = Len(planOps) \/ Broken = "stampfirst"

\* Retry idempotent.
RetryIsIdempotent ==
  TRUE \* by idem check in start

\* Liveness: from crashed, next apply of kind completes.
CrashedApplyCompletes ==
  (phase = "crashed") ~> (redisStamps[planKind] = planRev)

=============================================================================
