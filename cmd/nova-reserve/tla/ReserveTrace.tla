---------------------------- MODULE ReserveTrace ----------------------------
(* Binds cmd/nova-reserve/ledger.go and main.go to Reserve.tla: every walk in *)
(* ReserveLog.tla (recorded by TestTracesAreBehavioursOfTheModels off the     *)
(* real on-disk JSON records and lock table) must be a valid behaviour of     *)
(* Reserve.tla. Reversed witnesses BrokenOverCap and BrokenCutPause must fail *)
(* TraceAccepted.                                                             *)
EXTENDS Reserve, ReserveLog, Sequences

CONSTANT Log

VARIABLES w, l

Is(r) ==
  /\ pc = r.pc /\ kind = r.kind /\ prio = r.prio
  /\ reqNeeds = r.reqNeeds /\ reqTag = r.reqTag /\ stepsLeft = r.stepsLeft
  /\ inFlight = r.inFlight /\ starts = r.starts /\ seq = r.seq
  /\ holdSeq = r.holdSeq /\ lineSeq = r.lineSeq /\ lockHeld = r.lockHeld
  /\ cap = r.cap /\ crashes = r.crashes /\ sets = r.sets
  /\ wasPausedStep = r.wasPausedStep

Becomes(r) ==
  /\ pc' = r.pc /\ kind' = r.kind /\ prio' = r.prio
  /\ reqNeeds' = r.reqNeeds /\ reqTag' = r.reqTag /\ stepsLeft' = r.stepsLeft
  /\ inFlight' = r.inFlight /\ starts' = r.starts /\ seq' = r.seq
  /\ holdSeq' = r.holdSeq /\ lineSeq' = r.lineSeq /\ lockHeld' = r.lockHeld
  /\ cap' = r.cap /\ crashes' = r.crashes /\ sets' = r.sets
  /\ wasPausedStep' = r.wasPausedStep

Act(r) ==
  LET p == r.p IN
  CASE r.a = "AcquireWork"  -> AcquireWork(p)
    [] r.a = "AcquireCI"    -> AcquireCI(p)
    [] r.a = "WakeWaiter"   -> WakeWaiter(p)
    [] r.a = "FinishStep"   -> FinishStep(p)
    [] r.a = "ContinueStep" -> ContinueStep(p)
    [] r.a = "Timeout"      -> Timeout(p)
    [] r.a = "Crash"        -> Crash(p)
    [] r.a = "Reap"         -> Reap
    [] r.a = "SetCap"       -> SetCap
    [] OTHER                -> FALSE

TraceInit == w \in 1..Len(Log) /\ l = 1 /\ Init /\ Is(Log[w][1])

TraceNext == l < Len(Log[w]) /\ l' = l + 1 /\ UNCHANGED w /\ Act(Log[w][l + 1]) /\ Becomes(Log[w][l + 1])

TraceAccepted == l = Len(Log[w]) \/ ENABLED TraceNext
=============================================================================
