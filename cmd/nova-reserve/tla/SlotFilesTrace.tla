---------------------------- MODULE SlotFilesTrace ----------------------------
(* The code bound to the model: every walk of cmd/nova-reserve/trace_test.go (SlotFilesLog.tla, written by    *)
(* that test and held equal to what the code does) is a behaviour of SlotFiles.tla: from Init, each record is *)
(* the action it names, by its process, ending in the state recorded. TraceAccepted fails at the first record *)
(* the model refuses. Reversed witness: BrokenNoLock (SlotFilesTraceBrokenNoLock.cfg), the same code over a    *)
(* lock table that grants a held slot, must be refused at that scan.                                          *)
EXTENDS SlotFiles, SlotFilesLog, Sequences

CONSTANT Log

VARIABLES w, l

Is(r) ==
  /\ pc = r.pc /\ scan = r.scan /\ mine = r.mine /\ slotEx = r.slotEx /\ gateEx = r.gateEx /\ gateSh = r.gateSh
  /\ allEx = r.allEx /\ allSh = r.allSh /\ rounds = r.rounds /\ crashes = r.crashes /\ cmd = r.cmd

Becomes(r) ==
  /\ pc' = r.pc /\ scan' = r.scan /\ mine' = r.mine /\ slotEx' = r.slotEx /\ gateEx' = r.gateEx /\ gateSh' = r.gateSh
  /\ allEx' = r.allEx /\ allSh' = r.allSh /\ rounds' = r.rounds /\ crashes' = r.crashes /\ cmd' = r.cmd

\* The record's action as Next takes it.
Act(r) ==
  LET p == r.p IN
  CASE r.a = "Crash"    -> Crash(p) /\ cmd' = IF Broken = "orphan" THEN cmd ELSE cmd \ {p}
    [] r.a = "Start"    -> Start(p) /\ Commands
    [] r.a = "SGate"    -> SGate(p) /\ Commands
    [] r.a = "SScan"    -> SScan(p) /\ Commands
    [] r.a = "SAll"     -> SAll(p) /\ Commands
    [] r.a = "SFinish"  -> SFinish(p) /\ Commands
    [] r.a = "XGate"    -> XGate(p) /\ Commands
    [] r.a = "XAll"     -> XAll(p) /\ Commands
    [] r.a = "XTimeout" -> XTimeout(p) /\ Commands
    [] r.a = "XFinish"  -> XFinish(p) /\ Commands
    [] OTHER            -> FALSE

TraceInit == w \in 1..Len(Log) /\ l = 1 /\ Init /\ Is(Log[w][1])

TraceNext == l < Len(Log[w]) /\ l' = l + 1 /\ UNCHANGED w /\ Act(Log[w][l + 1]) /\ Becomes(Log[w][l + 1])

\* Every record is accepted: the walk is at its end or its next record is a step.
TraceAccepted == l = Len(Log[w]) \/ ENABLED TraceNext
=============================================================================
