------------------------------ MODULE ConfigApply -----------------------------
\* pkg/config apply, from the code and from docs/SPEC-CONFIG.md
\* ("History", "Apply"). The store is pkg/config/store.go: every write
\* is one transaction that changes the row and appends its history row, and
\* a kind's revision is the greatest history id of that kind. Redis is
\* pkg/config/apply.go: a copy of the store, never the source. The
\* stamp is config:decl's rev:<kind> (pkg/config/redis.go). Plan diffs
\* the kind's rows against Redis and writes adds and sets in list order,
\* then removes by name. nova-config apply with no --kind runs kinds in
\* order: machines, then the fleet row, then friends (pkg/config/kind.go
\* KindNames and the Kinds registry, and cmd/nova-config/apply.go). Idem
\* (apply.go) is the key config:<kind>:<rev> carried on every write of
\* that apply.
\*
\* This instance keeps three kinds, machine, fleet and friend, because those
\* are the kinds the order rule names. A machine row is its ceiling (absent,
\* or a small positive value). The fleet row is absent or the store machine
\* it names. A friend row is absent or her desired slots: her row has no
\* machine field, and the machine her slots are charged to is the host her
\* own beat reports (pkg/config/kind.go, the friend row; pkg/config/redis.go,
\* charge and ns_capacity_desired), modelled as beat[friend], a mapping TLC
\* chooses. The stamp is a compare-and-set in a WATCH/MULTI (redis.go,
\* Stamp): DoStamp writes it only while it still reads the value read when
\* the apply started, and a direct Redis write may move a stamp at any time,
\* mid-apply included. Other kinds, field checks beyond the ref, the function
\* library, the tiers and the bench registry set, and a second store writer
\* are outside this instance. One writer changes the store, and only while no
\* apply is in progress.
\*
\* Variables:
\*   mRows, fRows     the store's machine and friend rows
\*   gRow             the store's one fleet row (the machine it names)
\*   beat             the machine each friend's beat reports (TLC's choice)
\*   history          the history rows, in the order written; the index is
\*                    the global history id
\*   mViews, fViews   Redis's views
\*   gView            Redis's view of the fleet row
\*   stamp            config:decl's revision per kind
\*   phase            idle, or one apply in progress
\*   stages, si, oi   the plan still to run: ops then stamp, per kind
\*   mPlan, gPlan, fPlan  the ops snapshotted when the apply starts
\*   prefix           the ops this apply has written, in order
\*   ceilAtStart      which machine ceilings Redis already held
\*   ahead0           which kinds' stamps were ahead of the revision read
\*   stampRead        the stamp of each kind as the apply read it
\*   stampMoved       the stamp step of this apply has run
\*   opsDone          that kind's ops in this apply are done (or there were
\*                    none)
\*   clean            a completed apply's copy has not been disturbed
\*   idemApplied      the revision last stamped for the kind
\*   wroteAhead       the apply wrote while the stamp it read was ahead, or
\*                    (blindstamp) stamped over a stamp that had moved
\*   owed             a crash left this kind's apply unfinished
\*   crashes, faults  how many crashes and direct Redis writes so far
\*   checked          an ApplyCheck has run since the last store write
\*   seen             a Read has run
\*
\* Actions: InsertMachine, UpdateMachine, DeleteMachine, InsertFleet,
\* UpdateFleet, InsertFriend, UpdateFriend, DeleteFriend (each refused on a
\* name taken or missing, on a ref that names no row, and on deleting a
\* machine a friend is charged to); StartApply, Advance (the writes and
\* the stamp); ApplyCheck (plan, write nothing); ApplyCrash (stop after a
\* proper prefix of the ops, no stamp); OutsideRedisWrite (Redis's stamp
\* moves ahead of the store, at any time, an apply in progress included);
\* Read (a view is read, nothing is written).
\*
\* Invariants, and the liveness:
\*   HistoryCoversEveryWrite  store.go, the store comment; SPEC-CONFIG
\*                            "History"
\*   RedisIsACopy             apply.go, the View comment; SPEC-CONFIG "Apply"
\*   ConflictRefusesAhead     apply.go Apply, the CONFLICT return, and
\*                            redis.go Stamp, the compare-and-set
\*   ApplyOrder               apply.go Plan; kind.go Kinds
\*   StampOnlyWhenComplete   apply.go Apply, the stamp after the ops
\*   RetryIsIdempotent        apply.go Idem, and a second apply of the same
\*                            Postgres
\*   CrashedApplyCompletes    a crashed apply is finished by the next apply
\*                            of its kind once crashes are exhausted and
\*                            no earlier kind's stamp is ahead
\*
\* Broken = "none" is the design. Each other value is one reversed witness,
\* and the invariant it breaks:
\*   "stampfirst"   the stamp runs before the ops    StampOnlyWhenComplete
\*   "friendfirst"  a friend is written before the   ApplyOrder
\*                  ceiling of the machine her beat
\*                  reports
\*   "nohistory"    a row changes with no history    HistoryCoversEveryWrite
\*   "overahead"    an apply writes while the stamp  ConflictRefusesAhead
\*                  it read is ahead
\*   "blindstamp"   a direct Redis write moves the   ConflictRefusesAhead
\*                  stamp mid-apply and the stamp is
\*                  written with no compare

EXTENDS Naturals, Sequences

CONSTANTS m1, m2, f1, f2, MaxRev, MaxFaults, MaxCrashes, Broken

MachineList == <<m1, m2>>
FriendList == <<f1, f2>>
Machines == {m1, m2}
Friends == {f1, f2}
Kinds == {"machine", "fleet", "friend"}
Absent == "absent"
FleetName == "fleet"
ASSUME Len(MachineList) = 2 /\ MachineList[1] # MachineList[2]
ASSUME Len(FriendList) = 2 /\ FriendList[1] # FriendList[2]
ASSUME MaxRev \in Nat /\ MaxFaults \in Nat /\ MaxCrashes \in Nat
ASSUME Broken \in {"none", "stampfirst", "friendfirst", "nohistory",
                   "overahead", "blindstamp"}

\* Idem is the marker apply.go builds: config:<kind>:<rev>.
Idem(k, r) == <<"config", k, r>>

VARIABLES
  mRows, fRows, gRow, beat, history, mViews, fViews, gView, stamp,
  phase, stages, si, oi, mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0,
  stampRead, stampMoved, opsDone, clean, idemApplied, wroteAhead, owed,
  crashes, faults, checked, seen

vars == <<mRows, fRows, gRow, beat, history, mViews, fViews, gView, stamp,
          phase, stages, si, oi, mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0,
          stampRead, stampMoved, opsDone, clean, idemApplied, wroteAhead, owed,
          crashes, faults, checked, seen>>

TypeOK ==
  /\ mRows \in [Machines -> 0..2]
  /\ fRows \in [Friends -> 0..2]
  /\ gRow \in Machines \cup {Absent}
  /\ beat \in [Friends -> Machines]
  /\ Len(history) <= MaxRev
  /\ \A i \in 1..Len(history) :
       /\ history[i].kind \in Kinds
       /\ history[i].op \in {"add", "set", "remove"}
       /\ history[i].kind = "machine" =>
            history[i].name \in Machines /\ history[i].value \in 0..2
       /\ history[i].kind = "fleet" =>
            history[i].name = FleetName /\ history[i].op \in {"add", "set"} /\
            history[i].value \in Machines
       /\ history[i].kind = "friend" =>
            history[i].name \in Friends /\ history[i].value \in 0..2
  /\ mViews \in [Machines -> 0..2]
  /\ fViews \in [Friends -> 0..2]
  /\ gView \in Machines \cup {Absent}
  /\ stamp \in [Kinds -> 0..(MaxRev + 1)]
  /\ phase \in {"idle", "applying"}
  /\ Len(stages) <= 6
  /\ \A i \in 1..Len(stages) :
       stages[i] \in {"mops", "gops", "fops", "mstamp", "gstamp", "fstamp"}
  /\ si \in 0..7
  /\ oi \in 0..3
  /\ Len(mPlan) <= 2 /\ Len(gPlan) <= 1 /\ Len(fPlan) <= 2 /\ Len(prefix) <= 5
  /\ ceilAtStart \in [Machines -> BOOLEAN]
  /\ ahead0 \in [Kinds -> BOOLEAN]
  /\ stampRead \in [Kinds -> 0..(MaxRev + 1)]
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

Rev(k) ==
  LET ids == {i \in 1..Len(history) : history[i].kind = k}
  IN IF ids = {} THEN 0 ELSE CHOOSE i \in ids : \A j \in ids : j <= i

ViewEq(k) == IF k = "machine" THEN mViews = mRows
             ELSE IF k = "fleet" THEN gView = gRow
             ELSE fViews = fRows

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
        IF i = 0 THEN [f \in Friends |-> 0]
        ELSE LET h == history[i]
                 prev == F[i - 1]
             IN IF h.kind # "friend" THEN prev
                ELSE IF h.op = "remove" THEN [prev EXCEPT ![h.name] = 0]
                ELSE [prev EXCEPT ![h.name] = h.value]
  IN F[Len(history)]

FoldG ==
  LET F[i \in 0..Len(history)] ==
        IF i = 0 THEN Absent
        ELSE LET h == history[i]
                 prev == F[i - 1]
             IN IF h.kind # "fleet" THEN prev
                ELSE h.value
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

GPlan ==
  LET Add == IF gRow # Absent /\ gView = Absent
             THEN <<Rec("fleet", FleetName, "add", gRow)>> ELSE None
      Set == IF gRow # Absent /\ gView # Absent /\ gRow # gView
             THEN <<Rec("fleet", FleetName, "set", gRow)>> ELSE None
  IN Add \o Set

FOne(f, op, v) == <<Rec("friend", f, op, v)>>

FriendPlan ==
  LET a == FriendList[1]
      b == FriendList[2]
      Add(f) == IF fRows[f] # 0 /\ fViews[f] = 0
                THEN FOne(f, "add", fRows[f]) ELSE None
      Set(f) == IF fRows[f] # 0 /\ fViews[f] # 0 /\ fRows[f] # fViews[f]
                THEN FOne(f, "set", fRows[f]) ELSE None
      Rem(f) == IF fRows[f] = 0 /\ fViews[f] # 0
                THEN FOne(f, "remove", 0) ELSE None
  IN Add(a) \o Set(a) \o Add(b) \o Set(b) \o Rem(a) \o Rem(b)

Ahead(k) == stamp[k] > Rev(k)

Want(k) ==
  /\ Broken = "overahead" \/ ~Ahead(k)
  /\ \/ (k = "machine" /\ Len(MachPlan) > 0)
     \/ (k = "fleet" /\ Len(GPlan) > 0)
     \/ (k = "friend" /\ Len(FriendPlan) > 0)
     \/ stamp[k] # Rev(k)

\* The command stops at the first kind whose stamp is ahead, and writes
\* nothing of that kind or of a later kind (cmd/nova-config/apply.go).
RefuseAll ==
  /\ Broken # "overahead"
  /\ Ahead(IF Broken = "friendfirst" THEN "friend" ELSE "machine")

Pair(k, a, b) == IF Want(k) THEN <<a, b>> ELSE None

BuildStages ==
  IF RefuseAll THEN None
  ELSE IF Broken = "friendfirst" THEN
    Pair("friend", "fops", "fstamp") \o Pair("fleet", "gops", "gstamp") \o
    Pair("machine", "mops", "mstamp")
  ELSE IF Broken = "stampfirst" THEN
    (IF Want("machine") THEN <<"mstamp", "mops">> ELSE None) \o
    (IF Want("fleet") THEN <<"gstamp", "gops">> ELSE None) \o
    (IF Want("friend") THEN <<"fstamp", "fops">> ELSE None)
  ELSE
    Pair("machine", "mops", "mstamp") \o Pair("fleet", "gops", "gstamp") \o
    Pair("friend", "fops", "fstamp")

KindOf(s) == IF s \in {"mops", "mstamp"} THEN "machine"
             ELSE IF s \in {"gops", "gstamp"} THEN "fleet"
             ELSE "friend"

Init ==
  /\ mRows = [m \in Machines |-> 0]
  /\ fRows = [f \in Friends |-> 0]
  /\ gRow = Absent
  /\ beat \in [Friends -> Machines]
  /\ history = <<>>
  /\ mViews = [m \in Machines |-> 0]
  /\ fViews = [f \in Friends |-> 0]
  /\ gView = Absent
  /\ stamp = [k \in Kinds |-> 0]
  /\ phase = "idle"
  /\ stages = <<>>
  /\ si = 0
  /\ oi = 0
  /\ mPlan = <<>>
  /\ gPlan = <<>>
  /\ fPlan = <<>>
  /\ prefix = <<>>
  /\ ceilAtStart = [m \in Machines |-> FALSE]
  /\ ahead0 = [k \in Kinds |-> FALSE]
  /\ stampRead = [k \in Kinds |-> 0]
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
  /\ UNCHANGED <<fRows, gRow, beat, mViews, fViews, gView, stamp, phase, stages, si, oi,
                 mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0, stampRead, stampMoved,
                 opsDone, idemApplied, wroteAhead, owed, crashes, faults, seen>>

UpdateMachine(m) ==
  /\ phase = "idle"
  /\ mRows[m] = 1
  /\ StoreRoom
  /\ mRows' = [mRows EXCEPT ![m] = 2]
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("machine", m, "set", 2))
  /\ clean' = [clean EXCEPT !["machine"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<fRows, gRow, beat, mViews, fViews, gView, stamp, phase, stages, si, oi,
                 mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0, stampRead, stampMoved,
                 opsDone, idemApplied, wroteAhead, owed, crashes, faults, seen>>

DeleteMachine(m) ==
  /\ phase = "idle"
  /\ mRows[m] # 0
  /\ \A f \in Friends : fRows[f] = 0 \/ beat[f] # m
  /\ gRow # m
  /\ StoreRoom
  /\ mRows' = [mRows EXCEPT ![m] = 0]
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("machine", m, "remove", 0))
  /\ clean' = [clean EXCEPT !["machine"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<fRows, gRow, beat, mViews, fViews, gView, stamp, phase, stages, si, oi,
                 mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0, stampRead, stampMoved,
                 opsDone, idemApplied, wroteAhead, owed, crashes, faults, seen>>

InsertFleet(h) ==
  /\ phase = "idle"
  /\ gRow = Absent
  /\ mRows[h] # 0
  /\ StoreRoom
  /\ gRow' = h
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("fleet", FleetName, "add", h))
  /\ clean' = [clean EXCEPT !["fleet"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<mRows, fRows, beat, mViews, fViews, gView, stamp, phase, stages, si, oi,
                 mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0, stampRead, stampMoved,
                 opsDone, idemApplied, wroteAhead, owed, crashes, faults, seen>>

UpdateFleet(h) ==
  /\ phase = "idle"
  /\ gRow # Absent
  /\ gRow # h
  /\ mRows[h] # 0
  /\ StoreRoom
  /\ gRow' = h
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("fleet", FleetName, "set", h))
  /\ clean' = [clean EXCEPT !["fleet"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<mRows, fRows, beat, mViews, fViews, gView, stamp, phase, stages, si, oi,
                 mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0, stampRead, stampMoved,
                 opsDone, idemApplied, wroteAhead, owed, crashes, faults, seen>>

\* A friend row carries her desired slots and no machine: the machine her
\* beat reports is the charge (kind.go, the friend row; redis.go, charge).
InsertFriend(f, s) ==
  /\ phase = "idle"
  /\ fRows[f] = 0
  /\ s \in 1..2
  /\ mRows[beat[f]] # 0
  /\ StoreRoom
  /\ fRows' = [fRows EXCEPT ![f] = s]
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("friend", f, "add", s))
  /\ clean' = [clean EXCEPT !["friend"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<mRows, gRow, beat, mViews, fViews, gView, stamp, phase, stages, si, oi,
                 mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0, stampRead, stampMoved,
                 opsDone, idemApplied, wroteAhead, owed, crashes, faults, seen>>

UpdateFriend(f, s) ==
  /\ phase = "idle"
  /\ fRows[f] \in 1..2
  /\ s \in 1..2
  /\ s # fRows[f]
  /\ mRows[beat[f]] # 0
  /\ StoreRoom
  /\ fRows' = [fRows EXCEPT ![f] = s]
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("friend", f, "set", s))
  /\ clean' = [clean EXCEPT !["friend"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<mRows, gRow, beat, mViews, fViews, gView, stamp, phase, stages, si, oi,
                 mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0, stampRead, stampMoved,
                 opsDone, idemApplied, wroteAhead, owed, crashes, faults, seen>>

DeleteFriend(f) ==
  /\ phase = "idle"
  /\ fRows[f] \in 1..2
  /\ StoreRoom
  /\ fRows' = [fRows EXCEPT ![f] = 0]
  /\ IF Broken = "nohistory" THEN UNCHANGED history
     ELSE history' = Append(history, Rec("friend", f, "remove", 0))
  /\ clean' = [clean EXCEPT !["friend"] = FALSE]
  /\ checked' = FALSE
  /\ UNCHANGED <<mRows, gRow, beat, mViews, fViews, gView, stamp, phase, stages, si, oi,
                 mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0, stampRead, stampMoved,
                 opsDone, idemApplied, wroteAhead, owed, crashes, faults, seen>>

\* ApplyCheck plans and reports, and writes nothing (apply.go, check set).
\* A stamp that is ahead refuses the command before the plan is used.
ApplyCheck ==
  /\ phase = "idle"
  /\ ~checked
  /\ ~RefuseAll
  /\ checked' = TRUE
  /\ UNCHANGED <<mRows, fRows, gRow, beat, history, mViews, fViews, gView, stamp, phase, stages,
                 si, oi, mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0, stampRead, stampMoved,
                 opsDone, clean, idemApplied, wroteAhead, owed, crashes, faults, seen>>

\* Read returns one view. It writes neither store (store.go) nor Redis
\* (apply.go Read).
Read ==
  /\ phase = "idle"
  /\ ~seen
  /\ seen' = TRUE
  /\ UNCHANGED <<mRows, fRows, gRow, beat, history, mViews, fViews, gView, stamp, phase, stages,
                 si, oi, mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0, stampRead, stampMoved,
                 opsDone, clean, idemApplied, wroteAhead, owed, crashes, faults, checked>>

\* Something writes Redis directly and moves a stamp ahead of the store.
\* It runs at any time, an apply in progress included, but not while a
\* crash is still owed, so the next apply of an owed kind is not overtaken
\* by a new ahead stamp.
OutsideRedisWrite(k) ==
  /\ ~owed["machine"] /\ ~owed["fleet"] /\ ~owed["friend"]
  /\ faults < MaxFaults
  /\ stamp[k] < MaxRev + 1
  /\ stamp' = [stamp EXCEPT ![k] = stamp[k] + 1]
  /\ faults' = faults + 1
  /\ clean' = [clean EXCEPT ![k] = FALSE]
  /\ UNCHANGED <<mRows, fRows, gRow, beat, history, mViews, fViews, gView, phase, stages, si, oi,
                 mPlan, gPlan, fPlan, prefix, ceilAtStart, ahead0, stampRead, stampMoved, opsDone,
                 idemApplied, wroteAhead, owed, crashes, checked, seen>>

StartApply ==
  /\ phase = "idle"
  /\ LET st == BuildStages IN
       /\ Len(st) > 0
       /\ stages' = st
  /\ phase' = "applying"
  /\ si' = 1
  /\ oi' = 1
  /\ mPlan' = MachPlan
  /\ gPlan' = GPlan
  /\ fPlan' = FriendPlan
  /\ prefix' = <<>>
  /\ ceilAtStart' = [m \in Machines |-> mViews[m] # 0]
  /\ ahead0' = [k \in Kinds |-> Ahead(k)]
  /\ stampRead' = stamp
  /\ stampMoved' = [k \in Kinds |-> FALSE]
  /\ opsDone' = [k \in Kinds |-> IF k = "machine" THEN Len(MachPlan) = 0
                                 ELSE IF k = "fleet" THEN Len(GPlan) = 0
                                 ELSE Len(FriendPlan) = 0]
  /\ UNCHANGED <<mRows, fRows, gRow, beat, history, mViews, fViews, gView, stamp, clean,
                 idemApplied, wroteAhead, owed, crashes, faults, checked, seen>>

WriteM(op) ==
  /\ mViews' = [mViews EXCEPT ![op.name] = IF op.op = "remove" THEN 0 ELSE op.value]
  /\ prefix' = Append(prefix, op)
  /\ wroteAhead' = (wroteAhead \/ ahead0["machine"])
  /\ clean' = [clean EXCEPT !["machine"] = FALSE]
  /\ UNCHANGED <<gView, fViews>>

WriteG(op) ==
  /\ gView' = op.value
  /\ prefix' = Append(prefix, op)
  /\ wroteAhead' = (wroteAhead \/ ahead0["fleet"])
  /\ clean' = [clean EXCEPT !["fleet"] = FALSE]
  /\ UNCHANGED <<mViews, fViews>>

WriteF(op) ==
  /\ fViews' = [fViews EXCEPT ![op.name] = IF op.op = "remove" THEN 0 ELSE op.value]
  /\ prefix' = Append(prefix, op)
  /\ wroteAhead' = (wroteAhead \/ ahead0["friend"])
  /\ clean' = [clean EXCEPT !["friend"] = FALSE]
  /\ UNCHANGED <<mViews, gView>>

\* The idempotency key of a write is Idem(kind, rev) for the revision read
\* at the start. The rev does not move during the apply (the store writer
\* waits), so every write of the kind carries the same key.
LeaveOps(k) ==
  /\ si' = si + 1
  /\ oi' = 1
  /\ opsDone' = [opsDone EXCEPT ![k] = TRUE]

DoOps(k, plan) ==
  /\ UNCHANGED <<stamp, stampRead, stampMoved, idemApplied>>
  /\ IF oi > Len(plan) THEN
       /\ LeaveOps(k)
       /\ UNCHANGED <<mViews, gView, fViews, prefix, wroteAhead, clean>>
     ELSE
       /\ IF k = "machine" THEN WriteM(plan[oi])
          ELSE IF k = "fleet" THEN WriteG(plan[oi])
          ELSE WriteF(plan[oi])
       /\ IF oi = Len(plan) THEN LeaveOps(k)
          ELSE /\ oi' = oi + 1
               /\ UNCHANGED <<si, opsDone>>

\* The stamp is a compare-and-set (redis.go Stamp): it is written only while
\* it still reads the value the apply read at its start. A direct Redis write
\* that moved it mid-apply is a conflict, and the command stops there, with
\* nothing of the kind stamped. The blindstamp witness drops the compare and
\* writes over the moved stamp.
DoStamp(k) ==
  LET compare == (Broken # "blindstamp") IN
  IF compare /\ stamp[k] # stampRead[k] THEN
       /\ stamp' = stamp
       /\ stampMoved' = stampMoved
       /\ clean' = [clean EXCEPT ![k] = FALSE]
       /\ idemApplied' = idemApplied
       /\ wroteAhead' = wroteAhead
       /\ si' = Len(stages) + 1
       /\ oi' = 1
       /\ UNCHANGED <<mViews, gView, fViews, prefix, opsDone>>
     ELSE
       /\ stamp' = [stamp EXCEPT ![k] = Rev(k)]
       /\ stampMoved' = [stampMoved EXCEPT ![k] = TRUE]
       /\ clean' = [clean EXCEPT ![k] = ViewEq(k)]
       /\ idemApplied' = [idemApplied EXCEPT ![k] = Rev(k)]
       /\ wroteAhead' = (wroteAhead \/ ahead0[k] \/
                          (~compare /\ stamp[k] # stampRead[k]))
       /\ si' = si + 1
       /\ oi' = 1
       /\ UNCHANGED <<mViews, gView, fViews, prefix, opsDone>>

Step ==
  /\ phase = "applying"
  /\ si \in 1..Len(stages)
  /\ LET s == stages[si] IN
       /\ \/ /\ s = "mops" /\ DoOps("machine", mPlan)
          \/ /\ s = "gops" /\ DoOps("fleet", gPlan)
          \/ /\ s = "fops" /\ DoOps("friend", fPlan)
          \/ /\ s = "mstamp" /\ DoStamp("machine")
          \/ /\ s = "gstamp" /\ DoStamp("fleet")
          \/ /\ s = "fstamp" /\ DoStamp("friend")
       /\ UNCHANGED <<mRows, fRows, gRow, beat, history, phase, stages, mPlan, gPlan, fPlan,
                      ceilAtStart, ahead0, stampRead, owed, crashes, faults, checked, seen>>

Finish ==
  /\ phase = "applying"
  /\ si = Len(stages) + 1
  /\ phase' = "idle"
  /\ owed' = [k \in Kinds |-> IF ViewEq(k) /\ stamp[k] = Rev(k) THEN FALSE ELSE owed[k]]
  /\ stampMoved' = [k \in Kinds |-> FALSE]
  /\ prefix' = <<>>
  /\ UNCHANGED <<mRows, fRows, gRow, beat, history, mViews, fViews, gView, stamp, stages, si, oi,
                 mPlan, gPlan, fPlan, ceilAtStart, ahead0, stampRead, opsDone, clean, idemApplied,
                 wroteAhead, crashes, faults, checked, seen>>

Advance == Step \/ Finish

\* The writer dies after a proper prefix of the plan. The stamp of the kind
\* in hand is not written by this step. Views already written stay written.
ApplyCrash ==
  /\ phase = "applying"
  /\ crashes < MaxCrashes
  /\ si \in 1..Len(stages)
  /\ LET k == KindOf(stages[si]) IN
       /\ ~ (ViewEq(k) /\ stamp[k] = Rev(k))
       /\ owed' = [owed EXCEPT ![k] = TRUE]
       /\ clean' = [clean EXCEPT ![k] = FALSE]
  /\ crashes' = crashes + 1
  /\ phase' = "idle"
  /\ stampMoved' = [k \in Kinds |-> FALSE]
  /\ prefix' = <<>>
  /\ UNCHANGED <<mRows, fRows, gRow, beat, history, mViews, fViews, gView, stamp, stages, si, oi,
                 mPlan, gPlan, fPlan, ceilAtStart, ahead0, stampRead, opsDone, idemApplied,
                 wroteAhead, faults, checked, seen>>

Next ==
  \/ \E m \in Machines : InsertMachine(m) \/ UpdateMachine(m) \/ DeleteMachine(m)
  \/ \E h \in Machines : InsertFleet(h) \/ UpdateFleet(h)
  \/ \E f \in Friends : \E s \in 1..2 : InsertFriend(f, s) \/ UpdateFriend(f, s)
  \/ \E f \in Friends : DeleteFriend(f)
  \/ ApplyCheck \/ Read
  \/ \E k \in Kinds : OutsideRedisWrite(k)
  \/ StartApply \/ Advance \/ ApplyCrash

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
HistoryCoversEveryWrite == mRows = FoldM /\ fRows = FoldF /\ gRow = FoldG

\* After a completed apply, Redis's views of that kind equal the store's
\* rows. clean is set when the stamp is written and cleared by a store
\* write, an outside write, or a crash.
RedisIsACopy == \A k \in Kinds : clean[k] => ViewEq(k)

\* An apply never writes when the stamp it read is ahead of the revision it
\* read (apply.go, the CONFLICT return), and the stamp it writes is a
\* compare-and-set against the value it read (redis.go Stamp): a direct
\* Redis write that moved the stamp mid-apply is refused, never stamped
\* over. The witnesses that apply anyway, or stamp over the moved value
\* (blindstamp), set wroteAhead.
ConflictRefusesAhead == ~wroteAhead

\* In any prefix of an apply's writes, a machine ceiling precedes the fleet
\* row that names it and the friend charged to it (a ceiling already in
\* Redis counts: an earlier apply wrote it), and a friend's removal is
\* followed only by removals.
IsPlace(op) == op.kind = "friend" /\ op.op \in {"add", "set"}
IsFrRem(op) == op.kind = "friend" /\ op.op = "remove"
IsFleetPlace(op) == op.kind = "fleet" /\ op.op \in {"add", "set"}
ApplyOrder ==
  \A i \in 1..Len(prefix) :
    /\ IsPlace(prefix[i]) =>
         \/ ceilAtStart[beat[prefix[i].name]]
         \/ \E j \in 1..(i - 1) :
              /\ prefix[j].kind = "machine"
              /\ prefix[j].name = beat[prefix[i].name]
              /\ prefix[j].op \in {"add", "set"}
    /\ IsFleetPlace(prefix[i]) =>
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
  /\ k = "fleet" => stamp["machine"] <= Rev("machine")
  /\ k = "friend" =>
       stamp["machine"] <= Rev("machine") /\ stamp["fleet"] <= Rev("fleet")

CrashedApplyCompletes == \A k \in Kinds : CanFinish(k) ~> ~owed[k]

=============================================================================
