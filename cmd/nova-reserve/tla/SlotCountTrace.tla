---------------------------- MODULE SlotCountTrace ----------------------------
(* The code bound to the guarantee: the walks of cmd/nova-reserve/trace_test.go projected onto SlotCount.tla  *)
(* (SlotCountLog.tla: shared is every process holding a slot, excl every one holding all exclusive) are its    *)
(* behaviours. Reversed witness: BrokenNoLock (SlotCountTraceBrokenNoLock.cfg), three holders under a capacity *)
(* of two over a lock table that grants a held slot, must be refused at the third.                            *)
EXTENDS SlotCount, SlotCountLog, Sequences

CONSTANT Log

VARIABLES w, l

Act(r) ==
  CASE r.a = "AcquireShared"    -> AcquireShared(r.p)
    [] r.a = "AcquireExclusive" -> AcquireExclusive(r.p)
    [] r.a = "Release"          -> Release(r.p)
    [] OTHER                    -> FALSE

TraceInit == w \in 1..Len(Log) /\ l = 1 /\ Init /\ shared = Log[w][1].shared /\ excl = Log[w][1].excl

TraceNext ==
  /\ l < Len(Log[w]) /\ l' = l + 1 /\ UNCHANGED w
  /\ Act(Log[w][l + 1]) /\ shared' = Log[w][l + 1].shared /\ excl' = Log[w][l + 1].excl

TraceAccepted == l = Len(Log[w]) \/ ENABLED TraceNext
=============================================================================
