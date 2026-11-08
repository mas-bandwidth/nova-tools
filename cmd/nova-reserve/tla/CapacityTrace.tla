---------------------------- MODULE CapacityTrace ----------------------------
(* The code bound to the model: every walk of cmd/nova-reserve/trace_test.go (CapacityLog.tla, written by     *)
(* that test and held equal to what the code does) is a behaviour of Capacity.tla: from Init, each record is  *)
(* the action it names, by its process, ending in the state recorded. TraceAccepted fails at the first record *)
(* the model refuses. Reversed witness: BrokenAllOver (CapacityTraceBrokenAllOver.cfg), the same code over a  *)
(* lock table that grants all exclusive over a shared holder, so a set stores a new capacity under a running  *)
(* holder: it must be refused at that write.                                                                  *)
EXTENDS Capacity, CapacityLog, Sequences

CONSTANT Log

VARIABLES w, l

Is(r) ==
  /\ pc = r.pc /\ capFile = r.capFile /\ slotEx = r.slotEx /\ gateEx = r.gateEx /\ gateSh = r.gateSh
  /\ allEx = r.allEx /\ allSh = r.allSh /\ scan = r.scan /\ mine = r.mine /\ adm = r.adm /\ want = r.want
  /\ ask = r.ask /\ crashes = r.crashes /\ probeEx = 0

Becomes(r) ==
  /\ pc' = r.pc /\ capFile' = r.capFile /\ slotEx' = r.slotEx /\ gateEx' = r.gateEx /\ gateSh' = r.gateSh
  /\ allEx' = r.allEx /\ allSh' = r.allSh /\ scan' = r.scan /\ mine' = r.mine /\ adm' = r.adm /\ want' = r.want
  /\ ask' = r.ask /\ crashes' = r.crashes

\* The record's action as Next takes it.
Act(r) ==
  LET p == r.p IN
  Q(CASE r.a = "Start"      -> Start(p)
      [] r.a = "SGate"      -> SGate(p)
      [] r.a = "SRead"      -> SRead(p)
      [] r.a = "SClaimRead" -> SClaimRead(p)
      [] r.a = "SClaimSeen" -> SClaimSeen(p)
      [] r.a = "SClaimOK"   -> SClaimOK(p)
      [] r.a = "SClaimBusy" -> SClaimBusy(p)
      [] r.a = "SClaim2"    -> SClaim2(p)
      [] r.a = "SScan"      -> SScan(p)
      [] r.a = "SAll"       -> SAll(p)
      [] r.a = "SFinish"    -> SFinish(p)
      [] r.a = "XGate"      -> XGate(p)
      [] r.a = "XAll"       -> XAll(p)
      [] r.a = "XFinish"    -> XFinish(p)
      [] r.a = "TGate"      -> TGate(p)
      [] r.a = "TAll"       -> TAll(p)
      [] r.a = "Crash"      -> Crash(p)
      [] OTHER              -> FALSE)

TraceInit == w \in 1..Len(Log) /\ l = 1 /\ Init /\ Is(Log[w][1])

TraceNext == l < Len(Log[w]) /\ l' = l + 1 /\ UNCHANGED w /\ Act(Log[w][l + 1]) /\ Becomes(Log[w][l + 1])

\* Every record is accepted: the walk is at its end or its next record is a step.
TraceAccepted == l = Len(Log[w]) \/ ENABLED TraceNext
=============================================================================
