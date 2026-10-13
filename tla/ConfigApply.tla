------------------------------ MODULE ConfigApply -----------------------------
\* internal/config apply, from the code and from docs/SPEC-CONFIG.md
\* ("History", "Apply"). The store is internal/config/store.go: every write
\* is one transaction that changes the row and appends its history row, and
\* a kind's revision is the greatest history id of that kind. Redis is
\* internal/config/apply.go: a copy of the store, never the source. The
\* stamp is config:decl's rev:<kind> (internal/config/redis.go). Plan diffs
\* the kind's rows against Redis and writes adds and sets in list order,
\* then removes by name. nova-config apply with no --kind runs kinds in
\* order, machines before friends (internal/config/kind.go KindNames, and
\* cmd/nova-config/apply.go). Idem (apply.go) is the key config:<kind>:<rev>
\* carried on every write of that apply.
\*
\* This instance keeps two kinds, machine and friend, because those are the
\* kinds the order rule names: a machine ceiling is written before a friend
\* placed on it, and a friend's removal follows the other ops. A machine row
\* is its ceiling (absent, or a small positive value). A friend row is
\* absent or the machine it is placed on. Other kinds, field checks beyond
\* the ref and the referenced-row refusal, the function library, and a
\* second store writer are outside this instance. One writer changes the
\* store, and only while no apply is in progress.
\*
\* Variables:
\*   mRows, fRows     the store's rows
\*   history          the history rows, in the order written; the index is
\*                    the global history id
\*   mViews, fViews   Redis's views
\*   stamp            config:decl's revision per kind
\*   phase            idle, or one apply in progress
\*   stages, si, oi   the plan still to run: ops then stamp, per kind
\*   mPlan, fPlan     the ops snapshotted when the apply starts
\*   prefix           the ops this apply has written, in order
\*   ceilAtStart      which machine ceilings Redis already held
\*   ahead0           which kinds' stamps were ahead of the revision read
\*   stampMoved       the stamp step of this apply has run
\*   opsDone          that kind's ops in this apply are done (or there were
\*                    none)
\*   clean            a completed apply's copy has not been disturbed
\*   idemApplied      the revision last stamped for the kind
\*   wroteAhead       a write ran while the stamp read was ahead
\*   owed             a crash left this kind's apply unfinished
\*   crashes, faults  how many crashes and direct Redis writes so far
\*   checked          an ApplyCheck has run since the last store write
\*   seen             a Read has run
\*
\*   refusedRows      the rows this apply did not write: a write the runtime
\*                    refused (CEILING, an actor without the role, a row
\*                    in use), a row whose ref names one of those, and the
\*                    pending rows of a kind stopped by CONFLICT
\*   refusals         how many runtime refusals so far (bounded)
\*   planned          the kinds this apply has stages for
\*   attempted        the kinds whose ops this apply has started
\*   crashedRun       this apply ended by a crash, or by a store error
\*                    (exit 2)
\*   storeErred       a store or connection error that is not a refusal
\*                    happened in this apply
\*   sRow, sGit       the sprint row in the store: its coordinator (a ref to
\*                    a friend) and one other field (git_identity), present
\*                    only when WithSprint
\*   sView            Redis's sprint row: coordinator and git_identity; its
\*                    coordinator is the live seat
\*   seatMoved        the seat verb has moved the live coordinator (once)
\*   sOutcome, sDue   what this apply did with the sprint row, and whether
\*                    the ref rule on the row actually written refuses it
\*
\* Actions: InsertMachine, UpdateMachine, DeleteMachine, InsertFriend,
\* UpdateFriend, DeleteFriend (each refused on a name taken or missing, on
\* a ref that names no row, and on deleting a machine a friend names);
\* StartApply, Advance (the writes and the stamp; each write is written or
\* refused, and a refusal never stops the apply); StoreError (a write fails
\* with an error that is not a refusal: the run stops at once, exit 2); ApplyCheck (plan, write
\* nothing); ApplyCrash (stop after a proper prefix of the ops, no stamp);
\* OutsideRedisWrite (Redis's stamp moves ahead of the store);
\* Read (a view is read, nothing is written).
\*
\* Invariants, and the liveness:
\*   HistoryCoversEveryWrite  store.go, the store comment; SPEC-CONFIG
\*                            "History"
\*   RedisIsACopy             apply.go, the View comment; SPEC-CONFIG "Apply"
\*   ConflictRefusesAhead     apply.go Apply, the CONFLICT return
\*   ApplyOrder               apply.go Plan; kind.go KindNames
\*   StampOnlyWhenComplete   apply.go Apply, the stamp after the ops
\*   RetryIsIdempotent        apply.go Idem, and a second apply of the same
\*                            Postgres
\*   EveryPlannedKindIsAttemptedUnlessCrashed
\*                            cmd/nova-config/apply.go runApply, the kind
\*                            loop goes on past a refusal
\*   StoreErrorStopsTheRun    cmd/nova-config/apply.go runApply, a store
\*                            error is never classed as a refusal
\*   StampedKindHasNoRefusal  apply.go apply, no stamp when an op was
\*                            refused
\*   CrashedApplyCompletes    a crashed apply is finished by the next apply
\*                            of its kind once crashes are exhausted and
\*                            no earlier kind's stamp is ahead
\*
\* Broken = "none" is the design. Each other value is one reversed witness,
\* and the invariant it breaks:
\*   "stampfirst"   the stamp runs before the ops    StampOnlyWhenComplete
\*   "friendfirst"  a friend is written before the   ApplyOrder
\*                  ceiling it is placed on
\*   "nohistory"    a row changes with no history    HistoryCoversEveryWrite
\*   "overahead"    an apply writes while the stamp  ConflictRefusesAhead
\*                  it read is ahead
\*   "stopatrefusal" the apply stops at the first    EveryPlannedKindIsAttemptedUnlessCrashed
\*                  refused write (v1.2.16's apply)
\*   "stamprefused" a kind with a refused write is   StampedKindHasNoRefusal
\*                  stamped
\*   "heldjudgedonrow" the ref rule judges the stored SprintRefusedOnlyForItsWrittenRef
\*                  row, not the row written with the
\*                  live seat held (the second cut of
\*                  PR 5662)
\*   "swallowerr"   a store error after a refusal    StoreErrorStopsTheRun
\*                  in the same kind is classed as a
\*                  refusal and the run goes on (the
\*                  first cut of PR 5662)

EXTENDS Naturals, Sequences

CONSTANTS m1, m2, f1, f2, MaxRev, MaxFaults, MaxCrashes, MaxRefusals, Broken,
          WithSprint

MachineList == <<m1, m2>>
FriendList == <<f1, f2>>
Machines == {m1, m2}
Friends == {f1, f2}
Kinds == {"machine", "friend"}
Absent == "absent"
ASSUME Len(MachineList) = 2 /\ MachineList[1] # MachineList[2]
ASSUME Len(FriendList) = 2 /\ FriendList[1] # FriendList[2]
ASSUME MaxRev \in Nat /\ MaxFaults \in Nat /\ MaxCrashes \in Nat /\ MaxRefusals \in Nat
ASSUME Broken \in {"none", "stampfirst", "friendfirst", "nohistory", "overahead",
                    "stopatrefusal", "stamprefused", "swallowerr",
                    "heldjudgedonrow"}
ASSUME WithSprint \in BOOLEAN

\* Idem is the marker apply.go builds: config:<kind>:<rev>.
Idem(k, r) == <<"config", k, r>>

VARIABLES
  mRows, fRows, history, mViews, fViews, stamp,
  phase, stages, si, oi, mPlan, fPlan, prefix, ceilAtStart, ahead0,
  stampMoved, opsDone, clean, idemApplied, wroteAhead, owed,
  crashes, faults, checked, seen,
  refusedRows, refusals, planned, attempted, crashedRun, storeErred,
  sRow, sGit, sView, seatMoved, sOutcome, sDue

vars == <<mRows, fRows, history, mViews, fViews, stamp,
          phase, stages, si, oi, mPlan, fPlan, prefix, ceilAtStart, ahead0,
          stampMoved, opsDone, clean, idemApplied, wroteAhead, owed,
          crashes, faults, checked, seen,
          refusedRows, refusals, planned, attempted, crashedRun, storeErred,
          sRow, sGit, sView, seatMoved, sOutcome, sDue>>

\* The variables the refusal rule adds; a step that does not touch them
\* leaves them as they are.
rvars == <<refusedRows, refusals, planned, attempted, crashedRun, storeErred,
           sRow, sGit, sView, seatMoved, sOutcome, sDue>>

TypeOK ==
  /\ mRows \in [Machines -> 0..2]
  /\ fRows \in [Friends -> Machines \cup {Absent}]
  /\ Len(history) <= MaxRev
  /\ \A i \in 1..Len(history) :
       /\ history[i].kind \in Kinds \cup {"sprint"}
       /\ history[i].op \in {"add", "set", "remove"}
       /\ history[i].kind = "sprint" => history[i].value \in Friends
       /\ history[i].kind = "machine" =>
            history[i].name \in Machines /\ history[i].value \in 0..2
       /\ history[i].kind = "friend" =>
            history[i].name \in Friends /\
            (history[i].value \in Machines \/ history[i].value = Absent)
  /\ mViews \in [Machines -> 0..2]
  /\ fViews \in [Friends -> Machines \cup {Absent}]
  /\ stamp \in [Kinds -> 0..(MaxRev + 1)]
  /\ phase \in {"idle", "applying"}
  /\ Len(stages) <= 5
  /\ \A i \in 1..Len(stages) : stages[i] \in {"mops", "mstamp", "fops", "fstamp", "sops"}
  /\ si \in 0..6
  /\ oi \in 0..3
  /\ Len(mPlan) <= 2 /\ Len(fPlan) <= 2 /\ Len(prefix) <= 4
  /\ ceilAtStart \in [Machines -> BOOLEAN]
  /\ ahead0 \in [Kinds -> BOOLEAN]
  /\ stampMoved \in [Kinds -> BOOLEAN]
  /\ opsDone \in [Kinds -> BOOLEAN]
  /\ clean \in [Kinds -> BOOLEAN]
  /\ idemApplied \in [Kinds -> 0..MaxRev]
  /\ wroteAhead \in BOOLEAN
  /\ owed \in [Kinds -> BOOLEAN]
  /\ crashes \in 0..MaxCrashes
  /\ faults \in 0..MaxFaults
  /\ checked \in BOOLEAN
  /\ seen \in BOOLEAN
  /\ refusedRows \subseteq (Machines \cup Friends)
  /\ refusals \in 0..MaxRefusals
  /\ planned \in [Kinds -> BOOLEAN]
  /\ attempted \in [Kinds -> BOOLEAN]
  /\ crashedRun \in BOOLEAN
  /\ storeErred \in BOOLEAN
  /\ sRow \in Friends \cup {Absent}
  /\ sGit \in 0..1
  /\ sView \in [coord : Friends \cup {Absent}, git : 0..1]
  /\ seatMoved \in BOOLEAN
  /\ sOutcome \in {"none", "written", "refused"}
  /\ sDue \in BOOLEAN

Rev(k) ==
  LET ids == {i \in 1..Len(history) : history[i].kind = k}
  IN IF ids = {} THEN 0 ELSE CHOOSE i \in ids : \A j \in ids : j <= i

ViewEq(k) == IF k = "machine" THEN mViews = mRows ELSE fViews = fRows

FoldM ==
  LET F[i \in 0..Len(history)] ==
        IF i = 0 THEN [m \in Machines |-> 0]
        ELSE LET h == history[i]
                 prev == F[i - 1]
             IN IF h.kind # "machine" THEN prev
                ELSE IF h.op = "remove" THEN [prev EXCEPT ![h.name] = 0]
                ELSE [prev EXCEPT ![h.name] = h.value]
  IN F[Len(history)]

FoldF ==
  LET F[i \in 0..Len(history)] ==
        IF i = 0 THEN [f \in Friends |-> Absent]
        ELSE LET h == history[i]
                 prev == F[i - 1]
             IN IF h.kind # "friend" THEN prev
                ELSE IF h.op = "remove" THEN [prev EXCEPT ![h.name] = Absent]
                ELSE [prev EXCEPT ![h.name] = h.value]
  IN F[Len(history)]

Rec(k, n, o, v) == [kind |-> k, name |-> n, op |-> o, value |-> v]

One(m, op, v) == <<Rec("machine", m, op, v)>>
None == <<>>

MachPlan ==
  LET a == MachineList[1]
      b == MachineList[2]
      Add(m) == IF mRows[m] # 0 /\ mViews[m] = 0 THEN One(m, "add", mRows[m]) ELSE None
      Set(m) == IF mRows[m] # 0 /\ mViews[m] # 0 /\ mRows[m] # mViews[m]
                THEN One(m, "set", mRows[m]) ELSE None
      Rem(m) == IF mRows[m] = 0 /\ mViews[m] # 0 THEN One(m, "remove", 0) ELSE None
  IN Add(a) \o Set(a) \o Add(b) \o Set(b) \o Rem(a) \o Rem(b)

FOne(f, op, v) == <<Rec("friend", f, op, v)>>

FriendPlan ==
  LET a == FriendList[1]
      b == FriendList[2]
      Add(f) == IF fRows[f] # Absent /\ fViews[f] = Absent
                THEN FOne(f, "add", fRows[f]) ELSE None
      Set(f) == IF fRows[f] # Absent /\ fViews[f] # Absent /\ fRows[f] # fViews[f]
                THEN FOne(f, "set", fRows[f]) ELSE None
      Rem(f) == IF fRows[f] = Absent /\ fViews[f] # Absent
                THEN FOne(f, "remove", Absent) ELSE None
  IN Add(a) \o Set(a) \o Add(b) \o Set(b) \o Rem(a) \o Rem(b)

Ahead(k) == stamp[k] > Rev(k)

Want(k) ==
  /\ Broken = "overahead" \/ ~Ahead(k)
  /\ \/ (k = "machine" /\ Len(MachPlan) > 0)
     \/ (k = "friend" /\ Len(FriendPlan) > 0)
     \/ stamp[k] # Rev(k)

\* A kind whose stamp is ahead is refused CONFLICT and writes nothing; the
\* command goes on to every later kind (cmd/nova-config/apply.go runApply).
\* Want leaves it out of the stages, and StartApply counts its pending rows
\* as refused, so a later row that names one is refused too.

Pair(k, a, b) == IF Want(k) THEN <<a, b>> ELSE None

\* The sprint row is one more kind after friends (kind.go KindNames), modelled
\* by its one op: a set when any field differs from Redis (apply.go Plan).
\* Its stamp is outside the instance.
SprintOp == WithSprint /\ sRow # Absent /\ (sRow # sView.coord \/ sGit # sView.git)
SprintStage == IF SprintOp THEN <<"sops">> ELSE None

BuildStages ==
  IF Broken = "friendfirst" THEN
    Pair("friend", "fops", "fstamp") \o Pair("machine", "mops", "mstamp") \o SprintStage
  ELSE IF Broken = "stampfirst" THEN
    (IF Want("machine") THEN <<"mstamp", "mops">> ELSE None) \o
    (IF Want("friend") THEN <<"fstamp", "fops">> ELSE None) \o SprintStage
  ELSE
    Pair("machine", "mops", "mstamp") \o Pair("friend", "fops", "fstamp") \o SprintStage

KindOf(s) == CASE s \in {"mops", "mstamp"} -> "machine"
               [] s \in {"fops", "fstamp"} -> "friend"
               [] OTHER -> "sprint"

\* The rows of kind k this apply refused.
RefusedOf(k) == refusedRows \cap (IF k = "machine" THEN Machines ELSE Friends)

\* The machine rows a CONFLICT kind leaves unwritten: its pending adds and
\* sets (apply.go apply plans before it refuses CONFLICT, so the caller can
\* name them).
PendingMachines ==
  {MachPlan[i].name : i \in {j \in 1..Len(MachPlan) : MachPlan[j].op # "remove"}}

\* A row whose ref names a row refused earlier in this run is itself
\* refused, never written half-referenced (apply.go refRefusal), but only
\* when the named row is absent from Redis (its refused op was an add) or
\* the op changes the ref field itself. The friend names its machine: an add
\* is refused only on an absent machine; a set always changes the machine.
RefRefused(op) ==
  /\ op.kind = "friend"
  /\ op.op # "remove"
  /\ op.value \in refusedRows
  /\ (mViews[op.value] = 0 \/ op.op = "set")

Init ==
  /\ mRows = [m \in Machines |-> 0]
  /\ fRows = [f \in Friends |-> Absent]
  /\ history = <<>>
  /\ mViews = [m \in Machines |-> 0]
  /\ fViews = [f \in Friends |-> Absent]
  /\ stamp = [k \in Kinds |-> 0]
  /\ phase = "idle"
  /\ stages = <<>>
  /\ si = 0
  /\ oi = 0
  /\ mPlan = <<>>
  /\ fPlan = <<>>
  /\ prefix = <<>>
  /\ ceilAtStart = [m \in Machines |-> FALSE]
  /\ ahead0 = [k \in Kinds |-> FALSE]
  /\ stampMoved = [k \in Kinds |-> FALSE]
  /\ opsDone = [k \in Kinds |-> TRUE]
  /\ clean = [k \in Kinds |-> TRUE]
  /\ idemApplied = [k \in Kinds |-> 0]
  /\ wroteAhead = FALSE
  /\ owed = [k \in Kinds |-> FALSE]
  /\ crashes = 0
  /\ faults = 0
  /\ checked = FALSE
  /\ seen = FALSE
  /\ refusedRows = {}
  /\ refusals = 0
  /\ planned = [k \in Kinds |-> FALSE]
  /\ attempted = [k \in Kinds |-> FALSE]
  /\ crashedRun = FALSE
  /\ storeErred = FALSE
  /\ sRow = Absent
  /\ sGit = 0
  /\ sView = [coord |-> Absent, git |-> 0]
  /\ seatMoved = FALSE
  /\ sOutcome = "none"
  /\ sDue = FALSE

\* A store write appends one history row, unless the witness removes that
\* append. The guards are the refusals PlanWrite names.
StoreRoom == Broken = "nohistory" \/ Len(history) < MaxRev

InsertMachine(m) ==
  /\ phase = "idle"
  /\ mRows[m] = 0
  /\ StoreRoom
  /\ mRows' = [mRows EXCEPT ![m] = 1]
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("machine", m, "add", 1))
  /\ clean' = [clean EXCEPT !["machine"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<fRows, mViews, fViews, stamp, phase, stages, si, oi, mPlan,
                 fPlan, prefix, ceilAtStart, ahead0, stampMoved, opsDone,
                 idemApplied, wroteAhead, owed, crashes, faults, seen>>
  /\ UNCHANGED rvars

UpdateMachine(m) ==
  /\ phase = "idle"
  /\ mRows[m] = 1
  /\ StoreRoom
  /\ mRows' = [mRows EXCEPT ![m] = 2]
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("machine", m, "set", 2))
  /\ clean' = [clean EXCEPT !["machine"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<fRows, mViews, fViews, stamp, phase, stages, si, oi, mPlan,
                 fPlan, prefix, ceilAtStart, ahead0, stampMoved, opsDone,
                 idemApplied, wroteAhead, owed, crashes, faults, seen>>
  /\ UNCHANGED rvars

DeleteMachine(m) ==
  /\ phase = "idle"
  /\ mRows[m] # 0
  /\ \A f \in Friends : fRows[f] # m
  /\ StoreRoom
  /\ mRows' = [mRows EXCEPT ![m] = 0]
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("machine", m, "remove", 0))
  /\ clean' = [clean EXCEPT !["machine"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<fRows, mViews, fViews, stamp, phase, stages, si, oi, mPlan,
                 fPlan, prefix, ceilAtStart, ahead0, stampMoved, opsDone,
                 idemApplied, wroteAhead, owed, crashes, faults, seen>>
  /\ UNCHANGED rvars

InsertFriend(f, h) ==
  /\ phase = "idle"
  /\ fRows[f] = Absent
  /\ mRows[h] # 0
  /\ StoreRoom
  /\ fRows' = [fRows EXCEPT ![f] = h]
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("friend", f, "add", h))
  /\ clean' = [clean EXCEPT !["friend"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<mRows, mViews, fViews, stamp, phase, stages, si, oi, mPlan,
                 fPlan, prefix, ceilAtStart, ahead0, stampMoved, opsDone,
                 idemApplied, wroteAhead, owed, crashes, faults, seen>>
  /\ UNCHANGED rvars

UpdateFriend(f, h) ==
  /\ phase = "idle"
  /\ fRows[f] \in Machines
  /\ mRows[h] # 0
  /\ h # fRows[f]
  /\ StoreRoom
  /\ fRows' = [fRows EXCEPT ![f] = h]
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("friend", f, "set", h))
  /\ clean' = [clean EXCEPT !["friend"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<mRows, mViews, fViews, stamp, phase, stages, si, oi, mPlan,
                 fPlan, prefix, ceilAtStart, ahead0, stampMoved, opsDone,
                 idemApplied, wroteAhead, owed, crashes, faults, seen>>
  /\ UNCHANGED rvars

DeleteFriend(f) ==
  /\ phase = "idle"
  /\ fRows[f] # Absent
  /\ sRow # f
  /\ StoreRoom
  /\ fRows' = [fRows EXCEPT ![f] = Absent]
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("friend", f, "remove", Absent))
  /\ clean' = [clean EXCEPT !["friend"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<mRows, mViews, fViews, stamp, phase, stages, si, oi, mPlan,
                 fPlan, prefix, ceilAtStart, ahead0, stampMoved, opsDone,
                 idemApplied, wroteAhead, owed, crashes, faults, seen>>
  /\ UNCHANGED rvars

\* The sprint row names a friend as its coordinator, with git_identity set
\* in the same write (store.go: the ref must name a row).
SetSprint(f) ==
  /\ WithSprint
  /\ phase = "idle"
  /\ fRows[f] # Absent
  /\ sRow # f
  /\ Len(history) < MaxRev
  /\ sRow' = f
  /\ sGit' = 1
  /\ history' = Append(history, Rec("sprint", "sprint", "set", f))
  /\ checked' = FALSE
  /\ UNCHANGED <<mRows, fRows, mViews, fViews, stamp, phase, stages, si, oi,
                 mPlan, fPlan, prefix, ceilAtStart, ahead0, stampMoved, opsDone,
                 clean, idemApplied, wroteAhead, owed, crashes, faults, seen,
                 refusedRows, refusals, planned, attempted, crashedRun,
                 storeErred, sView, seatMoved, sOutcome, sDue>>

\* nova-sprint's seat verb moves the live coordinator in Redis, once; a
\* publish never moves it back (apply.go reportSeatHold).
MoveSeat(f) ==
  /\ WithSprint
  /\ phase = "idle"
  /\ ~seatMoved
  /\ sView.coord # f
  /\ sView' = [sView EXCEPT !.coord = f]
  /\ seatMoved' = TRUE
  /\ UNCHANGED <<mRows, fRows, history, mViews, fViews, stamp, phase, stages,
                 si, oi, mPlan, fPlan, prefix, ceilAtStart, ahead0, stampMoved,
                 opsDone, clean, idemApplied, wroteAhead, owed, crashes, faults,
                 checked, seen, refusedRows, refusals, planned, attempted,
                 crashedRun, storeErred, sRow, sGit, sOutcome, sDue>>

\* ApplyCheck plans and reports, and writes nothing (apply.go, check set).
\* A stamp that is ahead refuses the command before the plan is used.
\* A kind ahead is said CONFLICT and the check goes on to the next kind.
ApplyCheck ==
  /\ phase = "idle"
  /\ ~checked
  /\ checked' = TRUE
  /\ UNCHANGED <<mRows, fRows, history, mViews, fViews, stamp, phase, stages,
                 si, oi, mPlan, fPlan, prefix, ceilAtStart, ahead0, stampMoved,
                 opsDone, clean, idemApplied, wroteAhead, owed, crashes,
                 faults, seen>>
  /\ UNCHANGED rvars

\* Read returns one view. It writes neither store (store.go) nor Redis
\* (apply.go Read).
Read ==
  /\ phase = "idle"
  /\ ~seen
  /\ seen' = TRUE
  /\ UNCHANGED <<mRows, fRows, history, mViews, fViews, stamp, phase, stages,
                 si, oi, mPlan, fPlan, prefix, ceilAtStart, ahead0, stampMoved,
                 opsDone, clean, idemApplied, wroteAhead, owed, crashes,
                 faults, checked>>
  /\ UNCHANGED rvars

\* Something writes Redis directly and moves a stamp ahead of the store.
\* It does not run while a crash is still owed, so the next apply of an
\* owed kind is not overtaken by a new ahead stamp.
OutsideRedisWrite(k) ==
  /\ phase = "idle"
  /\ ~owed["machine"] /\ ~owed["friend"]
  /\ faults < MaxFaults
  /\ stamp[k] < MaxRev + 1
  /\ stamp' = [stamp EXCEPT ![k] = stamp[k] + 1]
  /\ faults' = faults + 1
  /\ clean' = [clean EXCEPT ![k] = FALSE]
  /\ UNCHANGED <<mRows, fRows, history, mViews, fViews, phase, stages, si, oi,
                 mPlan, fPlan, prefix, ceilAtStart, ahead0, stampMoved, opsDone,
                 idemApplied, wroteAhead, owed, crashes, checked, seen>>
  /\ UNCHANGED rvars

StartApply ==
  /\ phase = "idle"
  /\ LET st == BuildStages IN
       /\ Len(st) > 0
       /\ stages' = st
  /\ phase' = "applying"
  /\ si' = 1
  /\ oi' = 1
  /\ mPlan' = MachPlan
  /\ fPlan' = FriendPlan
  /\ prefix' = <<>>
  /\ ceilAtStart' = [m \in Machines |-> mViews[m] # 0]
  /\ ahead0' = [k \in Kinds |-> Ahead(k)]
  /\ stampMoved' = [k \in Kinds |-> FALSE]
  /\ opsDone' = [k \in Kinds |-> IF k = "machine" THEN Len(MachPlan) = 0
                                 ELSE Len(FriendPlan) = 0]
  /\ refusedRows' = IF Ahead("machine") /\ Broken # "overahead"
                    THEN PendingMachines ELSE {}
  /\ planned' = [k \in Kinds |-> \E i \in 1..Len(stages') : KindOf(stages'[i]) = k]
  /\ attempted' = [k \in Kinds |-> FALSE]
  /\ crashedRun' = FALSE
  /\ storeErred' = FALSE
  /\ sOutcome' = "none"
  /\ sDue' = FALSE
  /\ UNCHANGED <<mRows, fRows, history, mViews, fViews, stamp, clean,
                 idemApplied, wroteAhead, owed, crashes, faults, checked, seen,
                 refusals, sRow, sGit, sView, seatMoved>>

WriteM(op) ==
  /\ mViews' = [mViews EXCEPT ![op.name] = IF op.op = "remove" THEN 0 ELSE op.value]
  /\ prefix' = Append(prefix, op)
  /\ wroteAhead' = (wroteAhead \/ ahead0["machine"])
  /\ clean' = [clean EXCEPT !["machine"] = FALSE]
  /\ UNCHANGED fViews

WriteF(op) ==
  /\ fViews' = [fViews EXCEPT ![op.name] = IF op.op = "remove" THEN Absent ELSE op.value]
  /\ prefix' = Append(prefix, op)
  /\ wroteAhead' = (wroteAhead \/ ahead0["friend"])
  /\ clean' = [clean EXCEPT !["friend"] = FALSE]
  /\ UNCHANGED mViews

\* The idempotency key of a write is Idem(kind, rev) for the revision read
\* at the start. The rev does not move during the apply (the store writer
\* waits), so every write of the kind carries the same key.
LeaveOps(k) ==
  /\ si' = si + 1
  /\ oi' = 1
  /\ opsDone' = [opsDone EXCEPT ![k] = TRUE]

NextOp(k, plan) ==
  IF oi = Len(plan) THEN LeaveOps(k)
  ELSE /\ oi' = oi + 1
       /\ UNCHANGED <<si, opsDone>>

\* One op of the kind: written, or refused. A refusal (the runtime's, at most
\* MaxRefusals in a behaviour, or the ref rule's) writes nothing, names the
\* row, and the apply goes on to the next op (apply.go apply, the op loop).
\* The witness "stopatrefusal" is v1.2.16: the apply ends at the refusal.
DoOps(k, plan) ==
  /\ attempted' = [attempted EXCEPT ![k] = TRUE]
  /\ UNCHANGED <<stamp, stampMoved, idemApplied, planned, crashedRun, storeErred,
                 sRow, sGit, sView, seatMoved, sOutcome, sDue>>
  /\ IF oi > Len(plan) THEN
       /\ LeaveOps(k)
       /\ UNCHANGED <<mViews, fViews, prefix, wroteAhead, clean, phase,
                      refusedRows, refusals>>
     ELSE
       LET op == plan[oi] IN
       \/ /\ ~RefRefused(op)
          /\ IF k = "machine" THEN WriteM(op) ELSE WriteF(op)
          /\ NextOp(k, plan)
          /\ UNCHANGED <<phase, refusedRows, refusals>>
       \/ /\ RefRefused(op) \/ refusals < MaxRefusals
          /\ refusedRows' = refusedRows \cup {op.name}
          /\ refusals' = IF RefRefused(op) THEN refusals ELSE refusals + 1
          /\ UNCHANGED <<mViews, fViews, prefix, wroteAhead, clean>>
          /\ IF Broken = "stopatrefusal"
             THEN /\ phase' = "idle"
                  /\ UNCHANGED <<si, oi, opsDone>>
             ELSE /\ NextOp(k, plan)
                  /\ UNCHANGED phase

\* A kind with a refused op is not stamped: its revision stays behind, so
\* status says so and the next apply retries only the diff (apply.go apply).
\* The witness "stamprefused" stamps it anyway.
DoStamp(k) ==
  /\ IF RefusedOf(k) # {} /\ Broken # "stamprefused"
     THEN UNCHANGED <<stamp, stampMoved, clean, idemApplied, wroteAhead>>
     ELSE /\ stamp' = [stamp EXCEPT ![k] = Rev(k)]
          /\ stampMoved' = [stampMoved EXCEPT ![k] = TRUE]
          /\ clean' = [clean EXCEPT ![k] = ViewEq(k)]
          /\ idemApplied' = [idemApplied EXCEPT ![k] = Rev(k)]
          /\ wroteAhead' = (wroteAhead \/ ahead0[k])
  /\ si' = si + 1
  /\ oi' = 1
  /\ UNCHANGED <<mViews, fViews, prefix, opsDone, phase, rvars>>

\* The row the sprint op actually writes (apply.go reportSeatHold): a live
\* coordinator the row disagrees with is held, every other field is written.
WrittenCoord == IF sView.coord # Absent /\ sView.coord # sRow THEN sView.coord ELSE sRow

\* The ref rule on a sprint row naming c (apply.go refRefusal): c was refused
\* in this run, and is absent from Redis or the write changes the ref.
SprintRefRefused(c) == c \in refusedRows /\ (fViews[c] = Absent \/ c # sView.coord)

\* The sprint op: refused by the ref rule judged on the row written, or
\* written. The witness "heldjudgedonrow" judges the stored row instead.
DoSprint ==
  /\ LET judged == IF Broken = "heldjudgedonrow" THEN sRow ELSE WrittenCoord
     IN IF SprintRefRefused(judged)
        THEN /\ sOutcome' = "refused"
             /\ UNCHANGED sView
        ELSE /\ sOutcome' = "written"
             /\ sView' = [coord |-> WrittenCoord, git |-> sGit]
  /\ sDue' = SprintRefRefused(WrittenCoord)
  /\ si' = si + 1
  /\ oi' = 1
  /\ UNCHANGED <<stamp, stampMoved, clean, idemApplied, wroteAhead, mViews,
                 fViews, prefix, opsDone, phase, refusedRows, refusals, planned,
                 attempted, crashedRun, storeErred, sRow, sGit, seatMoved>>

Step ==
  /\ phase = "applying"
  /\ si \in 1..Len(stages)
  /\ LET s == stages[si] IN
       /\ \/ /\ s = "mops" /\ DoOps("machine", mPlan)
          \/ /\ s = "fops" /\ DoOps("friend", fPlan)
          \/ /\ s = "mstamp" /\ DoStamp("machine")
          \/ /\ s = "fstamp" /\ DoStamp("friend")
          \/ /\ s = "sops" /\ DoSprint
       /\ UNCHANGED <<mRows, fRows, history, stages, mPlan, fPlan,
                      ceilAtStart, ahead0, owed, crashes, faults, checked, seen>>

Finish ==
  /\ phase = "applying"
  /\ si = Len(stages) + 1
  /\ phase' = "idle"
  /\ owed' = [k \in Kinds |-> IF ViewEq(k) /\ stamp[k] = Rev(k) THEN FALSE ELSE owed[k]]
  /\ stampMoved' = [k \in Kinds |-> FALSE]
  /\ prefix' = <<>>
  /\ UNCHANGED <<mRows, fRows, history, mViews, fViews, stamp, stages, si, oi,
                 mPlan, fPlan, ceilAtStart, ahead0, opsDone, clean, idemApplied,
                 wroteAhead, crashes, faults, checked, seen>>
  /\ UNCHANGED rvars

Advance == Step \/ Finish

\* The writer dies after a proper prefix of the plan. The stamp of the kind
\* in hand is not written by this step. Views already written stay written.
ApplyCrash ==
  /\ phase = "applying"
  /\ crashes < MaxCrashes
  /\ si \in 1..Len(stages)
  /\ stages[si] # "sops"
  /\ LET k == KindOf(stages[si]) IN
       /\ ~ (ViewEq(k) /\ stamp[k] = Rev(k))
       /\ owed' = [owed EXCEPT ![k] = TRUE]
       /\ clean' = [clean EXCEPT ![k] = FALSE]
  /\ crashes' = crashes + 1
  /\ phase' = "idle"
  /\ crashedRun' = TRUE
  /\ UNCHANGED <<refusedRows, refusals, planned, attempted, storeErred,
                 sRow, sGit, sView, seatMoved, sOutcome, sDue>>
  /\ stampMoved' = [k \in Kinds |-> FALSE]
  /\ prefix' = <<>>
  /\ UNCHANGED <<mRows, fRows, history, mViews, fViews, stamp, stages, si, oi,
                 mPlan, fPlan, ceilAtStart, ahead0, opsDone, idemApplied,
                 wroteAhead, faults, checked, seen>>

\* A write fails with an error that is not a refusal (a store or connection
\* error). The run stops there, exit 2, as a crash does: the kind is owed and
\* no later kind is written (cmd/nova-config/apply.go runApply, the first
\* case of the kind switch). It shares the crash budget. The witness
\* "swallowerr" is the first cut of PR 5662: after a refusal in the same
\* kind, the error is classed as one more refusal and the run goes on.
StoreError ==
  /\ phase = "applying"
  /\ crashes < MaxCrashes
  /\ si \in 1..Len(stages)
  /\ stages[si] \in {"mops", "fops"}
  /\ LET k == KindOf(stages[si])
         plan == IF k = "machine" THEN mPlan ELSE fPlan
     IN /\ oi <= Len(plan)
        /\ crashes' = crashes + 1
        /\ storeErred' = TRUE
        /\ IF Broken = "swallowerr" /\ RefusedOf(k) # {}
           THEN /\ refusedRows' = refusedRows \cup {plan[oi].name}
                /\ NextOp(k, plan)
                /\ UNCHANGED <<phase, crashedRun, owed, clean>>
           ELSE /\ phase' = "idle"
                /\ crashedRun' = TRUE
                /\ owed' = [owed EXCEPT ![k] = TRUE]
                /\ clean' = [clean EXCEPT ![k] = FALSE]
                /\ UNCHANGED <<refusedRows, si, oi, opsDone>>
  /\ UNCHANGED <<mRows, fRows, history, mViews, fViews, stamp, stages, mPlan,
                 fPlan, prefix, ceilAtStart, ahead0, stampMoved, idemApplied,
                 wroteAhead, faults, checked, seen, refusals, planned,
                 attempted, sRow, sGit, sView, seatMoved, sOutcome, sDue>>

Next ==
  \/ \E m \in Machines : InsertMachine(m) \/ UpdateMachine(m) \/ DeleteMachine(m)
  \/ \E f \in Friends : \E h \in Machines : InsertFriend(f, h) \/ UpdateFriend(f, h)
  \/ \E f \in Friends : DeleteFriend(f) \/ SetSprint(f) \/ MoveSeat(f)
  \/ ApplyCheck \/ Read
  \/ \E k \in Kinds : OutsideRedisWrite(k)
  \/ StartApply \/ Advance \/ ApplyCrash \/ StoreError

\* Fairness is the next apply: once a crash has left a kind owed, and no
\* further crash is left, the apply that is enabled runs to its stamp.
\* Crashes and outside writes are not fair. Outside writes run only while
\* nothing is owed, so they do not overtake that next apply. A kind whose
\* stamp is already ahead is refused, and the liveness does not claim it.
Spec == Init /\ [][Next]_vars /\ WF_vars(StartApply) /\ WF_vars(Advance)

-----------------------------------------------------------------------------
\* Every change of a row has one history row (store.go; SPEC-CONFIG History).
\* The rows are the fold of the history, so a change with no history row, or
\* a history row with no change, does not occur.
HistoryCoversEveryWrite == mRows = FoldM /\ fRows = FoldF

\* After a completed apply, Redis's views of that kind equal the store's
\* rows. clean is set when the stamp is written and cleared by a store
\* write, an outside write, or a crash.
RedisIsACopy == \A k \in Kinds : clean[k] => ViewEq(k)

\* An apply never writes when the stamp it read is ahead of the revision it
\* read (apply.go, the CONFLICT return). The witness that applies anyway
\* sets wroteAhead.
ConflictRefusesAhead == ~wroteAhead

\* In any prefix of an apply's writes, a machine ceiling precedes a friend
\* placed on it (a ceiling already in Redis counts: an earlier apply wrote
\* it), and a friend's removal is followed only by removals.
IsPlace(op) == op.kind = "friend" /\ op.op \in {"add", "set"}
IsFrRem(op) == op.kind = "friend" /\ op.op = "remove"
ApplyOrder ==
  \A i \in 1..Len(prefix) :
    /\ IsPlace(prefix[i]) =>
         \/ ceilAtStart[prefix[i].value]
         \/ \E j \in 1..(i - 1) :
              /\ prefix[j].kind = "machine"
              /\ prefix[j].name = prefix[i].value
              /\ prefix[j].op \in {"add", "set"}
    /\ IsFrRem(prefix[i]) =>
         \A j \in (i + 1)..Len(prefix) : prefix[j].op = "remove"

\* The revision stamp is written only after that kind's ops (apply.go).
StampOnlyWhenComplete == \A k \in Kinds : stampMoved[k] => opsDone[k]

\* Applying a (kind, rev) that is already the stamped copy leaves Redis as
\* that one apply left it (apply.go Idem; a second apply of the same
\* Postgres).
RetryIsIdempotent ==
  \A k \in Kinds :
    clean[k] /\ idemApplied[k] = Rev(k) => stamp[k] = Rev(k) /\ ViewEq(k)

\* A crashed kind is completed by the next apply, once crashes and outside
\* writes are exhausted and the command is not refused for an earlier kind.
CanFinish(k) ==
  /\ owed[k]
  /\ crashes = MaxCrashes
  /\ stamp[k] <= Rev(k)
  /\ k = "friend" => stamp["machine"] <= Rev("machine")

CrashedApplyCompletes == \A k \in Kinds : CanFinish(k) ~> ~owed[k]

\* A refusal never stops a later kind: once an apply has ended other than
\* by a crash or a store error (exit 2), every kind it had stages for was
\* attempted (cmd/nova-config apply.go runApply). This is stronger than
\* "every kind after a refused kind": no planned kind is ever skipped.
EveryPlannedKindIsAttemptedUnlessCrashed ==
  (phase = "idle" /\ ~crashedRun) => \A k \in Kinds : planned[k] => attempted[k]

\* A stamped kind has no refused op in the apply that stamped it (apply.go
\* apply: a kind with any refusal is not stamped).
StampedKindHasNoRefusal == \A k \in Kinds : stampMoved[k] => RefusedOf(k) = {}

\* A store error is never classed as a refusal: the step it happens in ends
\* the run, exit 2 (cmd/nova-config apply.go runApply). The excuse the
\* invariant above gives a crashed run is earned only by stopping.
StoreErrorStopsTheRun == storeErred => (phase = "idle" /\ crashedRun)

\* The sprint row is refused only by the ref rule on the row it would write:
\* with the live seat held, a refused coordinator in the stored row does not
\* refuse it, and its git_identity lands (apply.go refRefusal on the row
\* reportSeatHold returns).
SprintRefusedOnlyForItsWrittenRef == sOutcome = "refused" => sDue

=============================================================================
