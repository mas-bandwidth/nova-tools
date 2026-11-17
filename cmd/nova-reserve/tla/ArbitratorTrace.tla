--------------------------- MODULE ArbitratorTrace ---------------------------
(* Binds cmd/nova-reserve/engine.go to Arbitrator.tla: every walk in          *)
(* ArbitratorLog.tla (recorded by TestTracesAreBehavioursOfTheModels directly *)
(* off the deterministic Go Engine) must be a valid behaviour of              *)
(* Arbitrator.tla. Reversed witnesses BrokenVictimOrder, BrokenRoundRobin,    *)
(* and BrokenDeadlockQueue must fail TraceAccepted.                           *)
EXTENDS Arbitrator, ArbitratorLog, Sequences

VARIABLES w, l

Is(r) ==
  /\ state = r.state /\ mode = r.mode /\ reqHost = r.reqHost /\ hostOf = r.hostOf
  /\ needs = r.needs /\ prio = r.prio /\ source = r.source /\ parent = r.parent
  /\ sublease = r.sublease /\ paused = r.paused /\ seq = r.seq /\ nextSeq = r.nextSeq
  /\ starts = r.starts /\ claim = r.claim /\ claimsUsed = r.claimsUsed
  /\ rrLast = r.rrLast /\ reconnecting = r.reconnecting /\ inFlight = r.inFlight
  /\ restartSeq = r.restartSeq /\ restarts = r.restarts

Becomes(r) ==
  /\ state' = r.state /\ mode' = r.mode /\ reqHost' = r.reqHost /\ hostOf' = r.hostOf
  /\ needs' = r.needs /\ prio' = r.prio /\ source' = r.source /\ parent' = r.parent
  /\ sublease' = r.sublease /\ paused' = r.paused /\ seq' = r.seq /\ nextSeq' = r.nextSeq
  /\ starts' = r.starts /\ claim' = r.claim /\ claimsUsed' = r.claimsUsed
  /\ rrLast' = r.rrLast /\ reconnecting' = r.reconnecting /\ inFlight' = r.inFlight
  /\ restartSeq' = r.restartSeq /\ restarts' = r.restarts

Act(r) ==
  CASE r.op = "RequestWork"   -> RequestWork(r.p, r.m, r.rh, r.c, r.mm, r.pr, r.par)
    [] r.op = "RequestCI"     -> RequestCI(r.rh, r.c, r.mm)
    [] r.op = "UnpauseStep"   -> UnpauseStep(r.p)
    [] r.op = "AdmitQueued"   -> AdmitQueued(r.p, r.rh)
    [] r.op = "Release"       -> Release(r.p)
    [] r.op = "ClearRefused"  -> ClearRefused(r.p)
    [] r.op = "PlaceClaim"    -> PlaceClaim(r.rh, r.c)
    [] r.op = "ExpireClaim"   -> ExpireClaim(r.rh)
    [] r.op = "ServerRestart" -> ServerRestart
    [] r.op = "Reconnect"     -> Reconnect(r.p)
    [] r.op = "EndReconnect"  -> EndReconnect
    [] OTHER                  -> FALSE

TraceInit == w \in 1..Len(TraceWalks) /\ l = 1 /\ Init /\ Is(TraceWalks[w][1])

TraceNext == l < Len(TraceWalks[w]) /\ l' = l + 1 /\ UNCHANGED w /\ Act(TraceWalks[w][l + 1]) /\ Becomes(TraceWalks[w][l + 1])

TraceSpec == TraceInit /\ [][TraceNext]_<<vars, w, l>>

TraceAccepted == l = Len(TraceWalks[w]) \/ ENABLED TraceNext
=============================================================================
