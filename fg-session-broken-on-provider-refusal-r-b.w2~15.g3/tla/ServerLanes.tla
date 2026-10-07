---------------------------- MODULE ServerLanes ----------------------------
(***************************************************************************)
(* The sprint server's line of control and its lanes (cmd/nova-sprint      *)
(* serve.go serveCtx, servelanes.go, seriallock.go; docs/SPEC-SPRINT.md    *)
(* section 14, The server). One line (app.serial): the run loop's tick and *)
(* each batch of writes hold it in turn. A friend's beat runs on the beat  *)
(* lane and a read on the read lane, neither on the line; the read lane is *)
(* its own line, one read at a time. A caller can go away at any time      *)
(* (its client's deadline): a write that has not taken the line by then is *)
(* dropped, never run.                                                     *)
(*                                                                         *)
(* A read asked of a reader is held by a lease (the reader's read --begin  *)
(* starts it, its beat renews it, default 10 minutes); on start the server *)
(* keeps every read whose lease is live and only takes back reads whose     *)
(* lease lapsed.                                                           *)
(*                                                                         *)
(* The fault of 2026-10-04: every verb took the line, and a batch whose    *)
(* caller had gone was still run when its turn came, so beats waited       *)
(* behind ticks and writes and the line never drained.                     *)
(*                                                                         *)
(* The design is Broken = {}. Each other value is a reversed witness:      *)
(*   "beatonline"  a beat takes the line (the code before the lanes):      *)
(*                 BeatNeverWaitsForTheLine fails;                         *)
(*   "readonline"  a read takes the line: ReadWaitsOnlyForReads fails;     *)
(*   "rungone"     a write whose caller went is still run (a sync.Mutex    *)
(*                 that cannot be given up): GoneNeverRuns fails.          *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS Callers, KindOf, MaxTicks, Broken

Kinds  == {"beat", "read", "write"}
Faults == {"beatonline", "readonline", "rungone"}
ASSUME Broken \subseteq Faults /\ "tick" \notin Callers /\ "none" \notin Callers
ASSUME \A r \in Callers : KindOf[r] \in Kinds

VARIABLES line,      \* the line's holder: "none", "tick" or a caller
          rline,     \* the read lane's holder: "none" or a caller
          pc,        \* each caller: idle, waiting, running, answered, dropped
          gone,      \* each caller: its client has gone away
          goneWait,  \* ghost: it went while it was still waiting to start
          ran,       \* each caller: its verb ran (changed the sprint)
          ticks,     \* ticks begun, bounded by MaxTicks
          lease,     \* each caller: "none", "live" or "lapsed"
          restarts,  \* server restarts, bounded by 1
          takenBack, \* ghost: each caller: taken back on restart
          wasLapsed  \* ghost: each caller: lapsed when restart happened

vars == <<line, rline, pc, gone, goneWait, ran, ticks, lease, restarts, takenBack, wasLapsed>>

OnLine(r) == KindOf[r] = "write"
             \/ (KindOf[r] = "beat" /\ "beatonline" \in Broken)
             \/ (KindOf[r] = "read" /\ "readonline" \in Broken)

TypeOK ==
  /\ line \in {"none", "tick"} \cup Callers
  /\ rline \in {"none"} \cup Callers
  /\ pc \in [Callers -> {"idle", "waiting", "running", "answered", "dropped"}]
  /\ gone \in [Callers -> BOOLEAN]
  /\ goneWait \in [Callers -> BOOLEAN]
  /\ ran \in [Callers -> BOOLEAN]
  /\ ticks \in 0..MaxTicks
  /\ lease \in [Callers -> {"none", "live", "lapsed"}]
  /\ restarts \in 0..1
  /\ takenBack \in [Callers -> BOOLEAN]
  /\ wasLapsed \in [Callers -> BOOLEAN]

Init ==
  /\ line = "none" /\ rline = "none"
  /\ pc = [r \in Callers |-> "idle"]
  /\ gone = [r \in Callers |-> FALSE]
  /\ goneWait = [r \in Callers |-> FALSE]
  /\ ran = [r \in Callers |-> FALSE]
  /\ ticks = 0
  /\ lease = [r \in Callers |-> "none"]
  /\ restarts = 0
  /\ takenBack = [r \in Callers |-> FALSE]
  /\ wasLapsed = [r \in Callers |-> FALSE]

\* a caller sends its batch
Send(r) == /\ pc[r] = "idle"
           /\ pc' = [pc EXCEPT ![r] = "waiting"]
           /\ UNCHANGED <<line, rline, gone, goneWait, ran, ticks, lease, restarts, takenBack, wasLapsed>>

\* the run loop takes the line for a tick, and gives it back
TickBegin == /\ line = "none" /\ ticks < MaxTicks
             /\ line' = "tick" /\ ticks' = ticks + 1
             /\ UNCHANGED <<rline, pc, gone, goneWait, ran, lease, restarts, takenBack, wasLapsed>>
TickEnd == /\ line = "tick" /\ line' = "none"
           /\ UNCHANGED <<rline, pc, gone, goneWait, ran, ticks, lease, restarts, takenBack, wasLapsed>>

\* a caller's client gives up (its deadline): it is gone, whatever its verb is doing
GiveUp(r) == /\ pc[r] \in {"waiting", "running"} /\ ~gone[r]
             /\ gone' = [gone EXCEPT ![r] = TRUE]
             /\ goneWait' = [goneWait EXCEPT ![r] = (pc[r] = "waiting")]
             /\ UNCHANGED <<line, rline, pc, ran, ticks, lease, restarts, takenBack, wasLapsed>>

\* a verb on the line takes it (serialLock.LockCtx); the design never for a caller gone
TakeLine(r) == /\ pc[r] = "waiting" /\ OnLine(r) /\ line = "none"
               /\ (~gone[r] \/ "rungone" \in Broken)
               /\ line' = r /\ pc' = [pc EXCEPT ![r] = "running"]
               /\ UNCHANGED <<rline, gone, goneWait, ran, ticks, lease, restarts, takenBack, wasLapsed>>

\* a caller gone while it waited for the line is dropped: its verbs answered not run
Drop(r) == /\ pc[r] = "waiting" /\ OnLine(r) /\ gone[r] /\ "rungone" \notin Broken
           /\ pc' = [pc EXCEPT ![r] = "dropped"]
           /\ UNCHANGED <<line, rline, gone, goneWait, ran, ticks, lease, restarts, takenBack, wasLapsed>>

\* a beat starts on the beat lane: no line, no other beat waited for
BeatStart(r) == /\ pc[r] = "waiting" /\ KindOf[r] = "beat" /\ ~OnLine(r) /\ ~gone[r]
                /\ pc' = [pc EXCEPT ![r] = "running"]
                /\ UNCHANGED <<line, rline, gone, goneWait, ran, ticks, lease, restarts, takenBack, wasLapsed>>

BeatDrop(r) == /\ pc[r] = "waiting" /\ KindOf[r] = "beat" /\ ~OnLine(r) /\ gone[r]
                /\ pc' = [pc EXCEPT ![r] = "dropped"]
                /\ UNCHANGED <<line, rline, gone, goneWait, ran, ticks, lease, restarts, takenBack, wasLapsed>>

\* a read starts on the read lane: it waits for a read ahead of it alone, and is
\* dropped as a write is when its caller has gone first; its lease begins live
ReadStart(r) == /\ pc[r] = "waiting" /\ KindOf[r] = "read" /\ ~OnLine(r) /\ rline = "none"
                /\ ~gone[r]
                /\ rline' = r /\ pc' = [pc EXCEPT ![r] = "running"]
                /\ lease' = [lease EXCEPT ![r] = "live"]
                /\ UNCHANGED <<line, gone, goneWait, ran, ticks, restarts, takenBack, wasLapsed>>
ReadDrop(r) == /\ pc[r] = "waiting" /\ KindOf[r] = "read" /\ ~OnLine(r) /\ gone[r]
               /\ pc' = [pc EXCEPT ![r] = "dropped"]
               /\ lease' = [lease EXCEPT ![r] = "none"]
               /\ UNCHANGED <<line, rline, gone, goneWait, ran, ticks, restarts, takenBack, wasLapsed>>

\* a read's lease lapses while it is running (10 minutes have passed without renewal)
LapseLease(r) ==
  /\ restarts = 0
  /\ line = "none"
  /\ pc[r] = "running" /\ KindOf[r] = "read" /\ lease[r] = "live"
  /\ lease' = [lease EXCEPT ![r] = "lapsed"]
  /\ UNCHANGED <<line, rline, pc, gone, goneWait, ran, ticks, restarts, takenBack, wasLapsed>>

\* a server restart: transient locks are cleared, reads with a live lease are kept,
\* lapsed reads are taken back, and running writes/beats drop
Restart ==
  /\ restarts = 0
  /\ line = "none"
  /\ \E r \in Callers : KindOf[r] = "read" /\ pc[r] = "running"
  /\ restarts' = restarts + 1
  /\ line' = "none"
  /\ rline' = IF rline # "none" /\ KindOf[rline] = "read" /\ lease[rline] = "live"
              THEN rline ELSE "none"
  /\ pc' = [r \in Callers |->
              IF pc[r] = "running" THEN
                IF KindOf[r] = "read" THEN
                  IF lease[r] = "live" THEN "running" ELSE "dropped"
                ELSE "dropped"
              ELSE pc[r]]
  /\ takenBack' = [r \in Callers |->
                     takenBack[r] \/ (KindOf[r] = "read" /\ pc[r] = "running" /\ lease[r] = "lapsed")]
  /\ wasLapsed' = [r \in Callers |->
                     wasLapsed[r] \/ (KindOf[r] = "read" /\ pc[r] = "running" /\ lease[r] = "lapsed")]
  /\ lease' = [r \in Callers |->
                 IF pc[r] = "running" /\ KindOf[r] = "read" /\ lease[r] = "lapsed"
                 THEN "none" ELSE lease[r]]
  /\ UNCHANGED <<gone, goneWait, ran, ticks>>

\* the verb runs and is answered; what it held is given back
Finish(r) == /\ pc[r] = "running"
             /\ ran' = [ran EXCEPT ![r] = TRUE]
             /\ pc' = [pc EXCEPT ![r] = "answered"]
             /\ line' = IF line = r THEN "none" ELSE line
             /\ rline' = IF rline = r THEN "none" ELSE rline
             /\ lease' = [lease EXCEPT ![r] = "none"]
             /\ UNCHANGED <<gone, goneWait, ticks, restarts, takenBack, wasLapsed>>

Next ==
  \/ TickBegin \/ TickEnd
  \/ Restart
  \/ \E r \in Callers : Send(r) \/ GiveUp(r) \/ TakeLine(r) \/ Drop(r)
                        \/ BeatStart(r) \/ BeatDrop(r) \/ ReadStart(r) \/ ReadDrop(r) \/ Finish(r)
                        \/ LapseLease(r)

Fairness ==
  /\ WF_vars(TickEnd)
  /\ \A r \in Callers : /\ SF_vars(TakeLine(r)) /\ WF_vars(Drop(r))
                        /\ WF_vars(BeatStart(r)) /\ WF_vars(BeatDrop(r))
                        /\ SF_vars(ReadStart(r))
                        /\ WF_vars(ReadDrop(r)) /\ WF_vars(Finish(r))

Spec == Init /\ [][Next]_vars /\ Fairness

----------------------------------------------------------------------------
\* The line has one holder: a write runs only holding it, never during a tick.
WriteHoldsTheLine == \A r \in Callers :
  (pc[r] = "running" /\ KindOf[r] = "write") => line = r

\* A beat waits for nothing: whoever holds the line, a waiting beat whose caller is
\* there can start.
BeatNeverWaitsForTheLine == \A r \in Callers :
  (pc[r] = "waiting" /\ KindOf[r] = "beat" /\ ~gone[r]) => ENABLED BeatStart(r)

\* A read waits for another read alone: with the read lane free, a waiting read
\* whose caller is there can start whoever holds the line.
ReadWaitsOnlyForReads == \A r \in Callers :
  (pc[r] = "waiting" /\ KindOf[r] = "read" /\ rline = "none" /\ ~gone[r]) => ENABLED ReadStart(r)

\* A verb whose caller went before it started never runs.
GoneNeverRuns == \A r \in Callers : goneWait[r] => ~ran[r]

\* The lanes never hold the line.
LanesHoldNoLine == \A r \in Callers : KindOf[r] # "write" => line # r

\* A read with a live lease is never taken back.
LiveLeaseNeverTakenBack == \A r \in Callers :
  (KindOf[r] = "read" /\ lease[r] = "live") => ~takenBack[r]

\* Every lapsed read is taken back on restart.
EveryLapsedReadTakenBack == \A r \in Callers :
  (KindOf[r] = "read" /\ wasLapsed[r]) => takenBack[r]


\* Every batch sent is answered or dropped (its caller gone).
EveryBatchEnds == \A r \in Callers :
  (pc[r] = "waiting") ~> (pc[r] \in {"answered", "dropped"})
=============================================================================
