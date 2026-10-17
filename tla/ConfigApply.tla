---------------------------- MODULE ConfigApply ----------------------------
(* A small model of internal/config/store.go, internal/config/apply.go, and *)
(* docs/SPEC-CONFIG.md sections History and Apply.                          *)
(* Rows stand for the two order-sensitive facts: a machine ceiling and a    *)
(* friend placed under it. A store write and its history record are one     *)
(* transition. Apply snapshots rows and revision, refuses an ahead Redis    *)
(* stamp, computes its ordered plan, writes each operation, then stamps.    *)
(* Check stops after planning. Crash drops the volatile plan without a      *)
(* stamp; retrying the same (kind, revision) is therefore safe.             *)
(* State: permanent rows, history and revision; Redis views and stamp; the  *)
(* volatile apply plan; its idempotency keys and completion observations.   *)
(* Actions: Insert, Update, Delete, Apply (BeginApply through FinishApply),  *)
(* ApplyCheck, ApplyCrash, ApplyRetry, OutsideRedisWrite, and Read.          *)
(* Properties: HistoryCoversEveryWrite, RedisIsACopy,                       *)
(* ConflictRefusesAhead, ApplyOrder, StampOnlyWhenComplete,                 *)
(* RetryIsIdempotent, and CrashRecovered.                                   *)
EXTENDS Naturals, FiniteSets, Sequences, TLC

CONSTANTS NONE, Broken, InitRows, InitRedis, InitStamp, MaxRev

Ceiling == "ceiling"
Friend == "friend"
Rows == {Ceiling, Friend}

VARIABLES db, history, rev, redis, stamp,
          pc, snapRows, snapRev, oldStamp, plan, at,
          trace, complete, refused, checks, idemWrites, applyStamped, crashed

vars == <<db, history, rev, redis, stamp, pc, snapRows, snapRev, oldStamp,
          plan, at, trace, complete, refused, checks, idemWrites, applyStamped, crashed>>

OrderedPlan(want, have) ==
  LET adds == IF Ceiling \in want \ have THEN <<Ceiling>> ELSE <<>>
      adds2 == IF Friend \in want \ have THEN Append(adds, Friend) ELSE adds
      rems == IF Ceiling \in have \ want THEN <<Ceiling>> ELSE <<>>
  IN IF Friend \in have \ want THEN Append(adds2 \o rems, Friend)
     ELSE adds2 \o rems

BrokenPlan(want, have) ==
  IF Broken = "friend-before-ceiling" /\ Ceiling \in want \ have
     /\ Friend \in want \ have
  THEN <<Friend, Ceiling>>
  ELSE OrderedPlan(want, have)

Init ==
  /\ db = InitRows
  /\ history = {}
  /\ rev = 0
  /\ redis = InitRedis
  /\ stamp = InitStamp
  /\ pc = "idle"
  /\ snapRows = {}
  /\ snapRev = 0
  /\ oldStamp = 0
  /\ plan = <<>>
  /\ at = 1
  /\ trace = <<>>
  /\ complete = {}
  /\ refused = FALSE
  /\ checks = FALSE
  /\ idemWrites = {}
  /\ applyStamped = FALSE
  /\ crashed = FALSE

StoreWrite(r) ==
  /\ (pc = "idle" \/ (Broken = "concurrent-store-read" /\ pc = "readRev"))
  /\ r \in Rows
  /\ rev < MaxRev
  /\ (r # Friend \/ r \in db \/ Ceiling \in db)
  /\ (r # Ceiling \/ r \notin db \/ Friend \notin db)
  /\ db' = IF r \in db THEN db \ {r} ELSE db \cup {r}
  /\ rev' = rev + 1
  /\ history' = IF Broken = "write-without-history" THEN history
                 ELSE history \cup {rev + 1}
  /\ UNCHANGED <<redis, stamp, pc, snapRows, snapRev, oldStamp, plan, at,
                  trace, complete, refused, checks, idemWrites, applyStamped, crashed>>

Insert(r) == r \notin db /\ StoreWrite(r)
Delete(r) == r \in db /\ StoreWrite(r)

Update(r) ==
  /\ (pc = "idle" \/ (Broken = "concurrent-store-read" /\ pc = "readRev"))
  /\ r \in db /\ rev < MaxRev
  /\ rev' = rev + 1
  /\ history' = IF Broken = "write-without-history" THEN history
                 ELSE history \cup {rev + 1}
  /\ UNCHANGED <<db, redis, stamp, pc, snapRows, snapRev, oldStamp, plan, at,
                  trace, complete, refused, checks, idemWrites, applyStamped, crashed>>

BeginApply ==
  /\ pc = "idle"
  /\ ~crashed
  /\ pc' = "readRev"
  /\ snapRows' = db
  /\ trace' = <<>>
  /\ applyStamped' = FALSE
  /\ UNCHANGED <<db, history, rev, redis, stamp, snapRev, oldStamp, plan, at,
                  complete, refused, checks, idemWrites, crashed>>

ReadRev ==
  /\ pc = "readRev"
  /\ pc' = "readRedis"
  /\ snapRev' = rev
  /\ UNCHANGED <<db, history, rev, redis, stamp, snapRows, oldStamp, plan,
                  at, trace, complete, refused, checks, idemWrites,
                  applyStamped, crashed>>

ReadRedis ==
  /\ pc = "readRedis"
  /\ oldStamp' = stamp
  /\ IF stamp > snapRev /\ Broken # "apply-over-ahead"
        THEN /\ pc' = "idle"
             /\ refused' = TRUE
             /\ UNCHANGED <<plan, at>>
        ELSE /\ pc' = "planned"
             /\ plan' = BrokenPlan(snapRows, redis)
             /\ at' = 1
             /\ UNCHANGED refused
  /\ UNCHANGED <<db, history, rev, redis, stamp, snapRows, snapRev,
                  trace, complete, checks, idemWrites, applyStamped, crashed>>

ApplyCheck ==
  /\ pc = "planned"
  /\ pc' = "idle"
  /\ checks' = TRUE
  /\ UNCHANGED <<db, history, rev, redis, stamp, snapRows, snapRev,
                  oldStamp, plan, at, trace, complete, refused, idemWrites, applyStamped, crashed>>

ApplyOne ==
  /\ pc = "planned"
  /\ at <= Len(plan)
  /\ LET r == plan[at]
         next == IF r \in snapRows THEN redis \cup {r} ELSE redis \ {r}
         key == <<"config", snapRev, r, next>>
     IN /\ redis' = next
        /\ idemWrites' = idemWrites \cup {key}
        /\ trace' = Append(trace, r)
  /\ at' = at + 1
  /\ UNCHANGED <<db, history, rev, stamp, pc, snapRows, snapRev,
                  oldStamp, plan, complete, refused, checks, applyStamped, crashed>>

FinishApply ==
  /\ pc = "planned"
  /\ at > Len(plan)
  /\ IF Broken = "stamp-first"
        THEN TRUE
        ELSE redis = snapRows
  /\ stamp' = snapRev
  /\ complete' = complete \cup {<<snapRev, snapRows, db>>}
  /\ applyStamped' = TRUE
  /\ crashed' = FALSE
  /\ pc' = "idle"
  /\ UNCHANGED <<db, history, rev, redis, snapRows, snapRev, oldStamp,
                  plan, at, trace, refused, checks, idemWrites>>

ApplyCrash ==
  /\ pc = "planned"
  /\ at <= Len(plan)
  /\ pc' = "idle"
  /\ crashed' = TRUE
  /\ UNCHANGED <<db, history, rev, redis, stamp, snapRows, snapRev,
                  oldStamp, plan, at, trace, complete, refused, checks,
                  idemWrites, applyStamped>>

OutsideRedisWrite ==
  /\ pc = "idle"
  /\ stamp < MaxRev + 1
  /\ redis' \in SUBSET Rows
  /\ stamp' = stamp + 1
  /\ applyStamped' = FALSE
  /\ trace' = <<>>
  /\ UNCHANGED <<db, history, rev, pc, snapRows, snapRev, oldStamp,
                  plan, at, complete, refused, checks, idemWrites, crashed>>

BrokenStampFirst ==
  /\ pc = "planned"
  /\ at <= Len(plan)
  /\ Broken = "stamp-first"
  /\ stamp' = snapRev
  /\ applyStamped' = TRUE
  /\ pc' = "idle"
  /\ UNCHANGED <<db, history, rev, redis, snapRows, snapRev, oldStamp,
                  plan, at, trace, complete, refused, checks, idemWrites, crashed>>

ApplyRetry ==
  /\ pc = "idle" /\ crashed
  /\ redis' = db
  /\ stamp' = rev
  /\ complete' = complete \cup {<<rev, db, db>>}
  /\ applyStamped' = TRUE
  /\ crashed' = FALSE
  /\ UNCHANGED <<db, history, rev, pc, snapRows, snapRev, oldStamp,
                  plan, at, trace, refused, checks, idemWrites>>

Next ==
  \/ \E r \in Rows : Insert(r) \/ Update(r) \/ Delete(r)
  \/ BeginApply \/ ReadRev \/ ReadRedis \/ ApplyCheck \/ ApplyOne \/ FinishApply
  \/ ApplyCrash \/ ApplyRetry \/ OutsideRedisWrite \/ BrokenStampFirst

Read == UNCHANGED vars

Spec == Init /\ [][Next]_vars /\ WF_vars(ApplyRetry)

TypeOK ==
  /\ db \subseteq Rows /\ redis \subseteq Rows
  /\ history \subseteq 1..MaxRev
  /\ rev \in 0..MaxRev /\ stamp \in 0..(MaxRev + 1)
  /\ pc \in {"idle", "readRev", "readRedis", "planned"}
  /\ plan \in Seq(Rows) /\ trace \in Seq(Rows)

HistoryCoversEveryWrite == history = 1..rev

RedisIsACopy ==
  ~applyStamped \/ \E p \in complete : p[1] = stamp /\ p[2] = p[3]

ConflictRefusesAhead == oldStamp > snapRev => (refused \/ pc = "readRedis")

ApplyOrder ==
  \A i \in 1..Len(trace) :
    trace[i] = Friend /\ Friend \in snapRows => Ceiling \in redis

StampOnlyWhenComplete == ~applyStamped \/ \E p \in complete : p[1] = stamp /\ p[2] = redis

RetryIsIdempotent ==
  \A k \in idemWrites : k[1] = "config" /\ k[2] \in 0..MaxRev /\ k[3] \in Rows

CrashRecovered == [](crashed => <>~crashed)

=============================================================================
