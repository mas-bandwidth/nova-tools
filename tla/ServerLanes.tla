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
(* The fault of 2026-10-04: every verb took the line, and a batch whose    *)
(* caller had gone was still run when its turn came, so beats waited       *)
(* behind ticks and writes and the line never drained.                     *)
(*                                                                         *)
(* The design is Broken = {}. Each other value is a reversed witness:      *)
(*   "beatonline"  a beat takes the line (the code before the lanes):      *)
(*                 BeatNeverWaitsForTheLine fails;                         *)
(*   "readonline"  a read takes the line: ReadWaitsOnlyForReads fails;     *)
(*   "rungone"     a write whose caller went is still run (a sync.Mutex    *)
(*                 that cannot be given up): GoneNeverRuns fails;          *)
(*   "landgate"    the in-server land keeps the line through its git and   *)
(*                 its tree gate: LandHoldsTheLineOnlyForStoreSteps fails. *)
(*                                                                         *)
(* The land lane (landloop.go, land.go): the in-server land takes the line *)
(* for its store steps alone, the read of the queue and the report, and    *)
(* runs its fetch, merges, ledger runs and tree gate (minutes on a cold    *)
(* build cache) with the line free, so a tick never waits for git          *)
(* (2026-10-04 4:30 PM ET: ticks past their 10 s deadline were suspected   *)
(* of waiting on the land; the stacks showed the land waiting on them).    *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS Callers, KindOf, MaxTicks, Broken

Kinds  == {"beat", "read", "write"}
Faults == {"beatonline", "readonline", "rungone", "landgate"}
ASSUME Broken \subseteq Faults /\ "tick" \notin Callers /\ "none" \notin Callers /\ "land" \notin Callers
ASSUME \A r \in Callers : KindOf[r] \in Kinds

VARIABLES line,     \* the line's holder: "none", "tick", "land" or a caller
          lphase,   \* the land: idle, read (a store step), git (fetch, merges, gate), report
          rline,    \* the read lane's holder: "none" or a caller
          pc,       \* each caller: idle, waiting, running, answered, dropped
          gone,     \* each caller: its client has gone away
          goneWait, \* ghost: it went while it was still waiting to start
          ran,      \* each caller: its verb ran (changed the sprint)
          ticks     \* ticks begun, bounded by MaxTicks

vars == <<line, lphase, rline, pc, gone, goneWait, ran, ticks>>

OnLine(r) == KindOf[r] = "write"
             \/ (KindOf[r] = "beat" /\ "beatonline" \in Broken)
             \/ (KindOf[r] = "read" /\ "readonline" \in Broken)

TypeOK ==
  /\ line \in {"none", "tick", "land"} \cup Callers
  /\ lphase \in {"idle", "read", "git", "report"}
  /\ rline \in {"none"} \cup Callers
  /\ pc \in [Callers -> {"idle", "waiting", "running", "answered", "dropped"}]
  /\ gone \in [Callers -> BOOLEAN]
  /\ goneWait \in [Callers -> BOOLEAN]
  /\ ran \in [Callers -> BOOLEAN]
  /\ ticks \in 0..MaxTicks

Init ==
  /\ line = "none" /\ lphase = "idle" /\ rline = "none"
  /\ pc = [r \in Callers |-> "idle"]
  /\ gone = [r \in Callers |-> FALSE]
  /\ goneWait = [r \in Callers |-> FALSE]
  /\ ran = [r \in Callers |-> FALSE]
  /\ ticks = 0

\* a caller sends its batch
Send(r) == /\ pc[r] = "idle"
           /\ pc' = [pc EXCEPT ![r] = "waiting"]
           /\ UNCHANGED <<lphase, line, rline, gone, goneWait, ran, ticks>>

\* the run loop takes the line for a tick, and gives it back
TickBegin == /\ line = "none" /\ ticks < MaxTicks
             /\ line' = "tick" /\ ticks' = ticks + 1
             /\ UNCHANGED <<lphase, rline, pc, gone, goneWait, ran>>
TickEnd == /\ line = "tick" /\ line' = "none"
           /\ UNCHANGED <<lphase, rline, pc, gone, goneWait, ran, ticks>>

\* a caller's client gives up (its deadline): it is gone, whatever its verb is doing
GiveUp(r) == /\ pc[r] \in {"waiting", "running"} /\ ~gone[r]
             /\ gone' = [gone EXCEPT ![r] = TRUE]
             /\ goneWait' = [goneWait EXCEPT ![r] = (pc[r] = "waiting")]
             /\ UNCHANGED <<lphase, line, rline, pc, ran, ticks>>

\* a verb on the line takes it (serialLock.LockCtx); the design never for a caller gone
TakeLine(r) == /\ pc[r] = "waiting" /\ OnLine(r) /\ line = "none"
               /\ (~gone[r] \/ "rungone" \in Broken)
               /\ line' = r /\ pc' = [pc EXCEPT ![r] = "running"]
               /\ UNCHANGED <<lphase, rline, gone, goneWait, ran, ticks>>

\* a caller gone while it waited for the line is dropped: its verbs answered not run
Drop(r) == /\ pc[r] = "waiting" /\ OnLine(r) /\ gone[r] /\ "rungone" \notin Broken
           /\ pc' = [pc EXCEPT ![r] = "dropped"]
           /\ UNCHANGED <<lphase, line, rline, gone, goneWait, ran, ticks>>

\* a beat starts on the beat lane: no line, no other beat waited for
BeatStart(r) == /\ pc[r] = "waiting" /\ KindOf[r] = "beat" /\ ~OnLine(r) /\ ~gone[r]
                /\ pc' = [pc EXCEPT ![r] = "running"]
                /\ UNCHANGED <<lphase, line, rline, gone, goneWait, ran, ticks>>

BeatDrop(r) == /\ pc[r] = "waiting" /\ KindOf[r] = "beat" /\ ~OnLine(r) /\ gone[r]
                /\ pc' = [pc EXCEPT ![r] = "dropped"]
                /\ UNCHANGED <<lphase, line, rline, gone, goneWait, ran, ticks>>

\* a read starts on the read lane: it waits for a read ahead of it alone, and is
\* dropped as a write is when its caller has gone first
ReadStart(r) == /\ pc[r] = "waiting" /\ KindOf[r] = "read" /\ ~OnLine(r) /\ rline = "none"
                /\ ~gone[r]
                /\ rline' = r /\ pc' = [pc EXCEPT ![r] = "running"]
                /\ UNCHANGED <<lphase, line, gone, goneWait, ran, ticks>>
ReadDrop(r) == /\ pc[r] = "waiting" /\ KindOf[r] = "read" /\ ~OnLine(r) /\ gone[r]
               /\ pc' = [pc EXCEPT ![r] = "dropped"]
               /\ UNCHANGED <<lphase, line, rline, gone, goneWait, ran, ticks>>

\* the verb runs and is answered; what it held is given back
Finish(r) == /\ pc[r] = "running"
             /\ ran' = [ran EXCEPT ![r] = TRUE]
             /\ pc' = [pc EXCEPT ![r] = "answered"]
             /\ line' = IF line = r THEN "none" ELSE line
             /\ rline' = IF rline = r THEN "none" ELSE rline
             /\ UNCHANGED <<lphase, gone, goneWait, ticks>>

\* the land lane: the line for the read of the queue, none for git and the gate, the
\* line again for the report; landgate keeps it throughout
LandRead == /\ lphase = "idle" /\ line = "none"
            /\ line' = "land" /\ lphase' = "read"
            /\ UNCHANGED <<rline, pc, gone, goneWait, ran, ticks>>
LandGit == /\ lphase = "read" /\ line = "land"
           /\ lphase' = "git" /\ line' = IF "landgate" \in Broken THEN "land" ELSE "none"
           /\ UNCHANGED <<rline, pc, gone, goneWait, ran, ticks>>
LandGitDone == /\ lphase = "git"
               /\ IF "landgate" \in Broken THEN line' = line ELSE line = "none" /\ line' = "land"
               /\ lphase' = "report"
               /\ UNCHANGED <<rline, pc, gone, goneWait, ran, ticks>>
LandReport == /\ lphase = "report" /\ line = "land"
              /\ line' = "none" /\ lphase' = "idle"
              /\ UNCHANGED <<rline, pc, gone, goneWait, ran, ticks>>
Land == LandRead \/ LandGit \/ LandGitDone \/ LandReport

Next ==
  \/ Land \/ TickBegin \/ TickEnd
  \/ \E r \in Callers : Send(r) \/ GiveUp(r) \/ TakeLine(r) \/ Drop(r)
                        \/ BeatStart(r) \/ BeatDrop(r) \/ ReadStart(r) \/ ReadDrop(r) \/ Finish(r)

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

\* The land holds the line for its store steps alone: never while its git and its tree
\* gate run, so a tick never waits for them.
LandHoldsTheLineOnlyForStoreSteps == line = "land" => lphase \in {"read", "report"}

\* Every batch sent is answered or dropped (its caller gone).
EveryBatchEnds == \A r \in Callers :
  (pc[r] = "waiting") ~> (pc[r] \in {"answered", "dropped"})
=============================================================================
