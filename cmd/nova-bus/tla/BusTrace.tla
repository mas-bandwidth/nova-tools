------------------------------ MODULE BusTrace ------------------------------
(* The code bound to the model: every walk trace_bus_test.go took over the real hook and the fake store *)
(* (BusLog.tla, written by that test and held equal to what the code does) is a behaviour of Bus.tla:   *)
(* its first state is Init, and each step is the action it names, ending in the state the walk read    *)
(* off the store and the hook. Reversed witness: BrokenNewFirst, the same walk over a store whose read  *)
(* of the pending list returns nothing, so a restarted hook reads new entries with one still pending    *)
(* (BusTraceBrokenNewFirst.cfg): it must be refused.                                                    *)
EXTENDS Bus, BusLog, Sequences

CONSTANT Log

VARIABLES w, l

Is(r) ==
  /\ sent = r.sent /\ delivered = r.delivered /\ pend = r.pend /\ acked = r.acked /\ up = r.up
  /\ mode = r.mode /\ cur = r.cur /\ stage = r.stage /\ crashes = r.crashes /\ fails = r.fails
  /\ skip = {} /\ printed = r.printed /\ badPrint = r.badPrint

Becomes(r) ==
  /\ sent' = r.sent /\ delivered' = r.delivered /\ pend' = r.pend /\ acked' = r.acked /\ up' = r.up
  /\ mode' = r.mode /\ cur' = r.cur /\ stage' = r.stage /\ crashes' = r.crashes /\ fails' = r.fails
  /\ skip' = {} /\ printed' = r.printed /\ badPrint' = r.badPrint

Act(a) ==
  CASE a = "Send" -> Send
    [] a = "ReadPending" -> ReadPending
    [] a = "ReadNew" -> ReadNew
    [] a = "Print" -> Print
    [] a = "FailWake" -> FailWake
    [] a = "Ack" -> Ack
    [] a = "Crash" -> Crash
    [] a = "Restart" -> Restart
    [] OTHER -> FALSE

TraceInit == w \in 1..Len(Log) /\ l = 1 /\ Init

TraceNext ==
  /\ l < Len(Log[w]) /\ l' = l + 1 /\ UNCHANGED w
  /\ Act(Log[w][l + 1].action) /\ Becomes(Log[w][l + 1])

\* The walk starts at Init, and every step it took is one the model takes.
TraceAccepted ==
  /\ l = 1 => Log[w][1].action = "Init" /\ Is(Log[w][1])
  /\ l = Len(Log[w]) \/ ENABLED TraceNext
=============================================================================
