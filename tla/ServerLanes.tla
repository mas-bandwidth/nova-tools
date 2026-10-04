---------------------------- MODULE ServerLanes ----------------------------
(***************************************************************************)
(* The sprint server's line of control and its lanes (cmd/nova-sprint      *)
(* serve.go serveCtx, servelanes.go; internal/sprint control_line.go       *)
(* ControlLine; docs/SPEC-SPRINT.md section 14, The server, and "The       *)
(* tick's turn"). One line: the run loop's tick and each batch of writes   *)
(* hold it in turn. The writes take it in the order they asked (a ticket   *)
(* each); the tick asks, and takes the line as soon as no write waits, or  *)
(* next, before the writes waiting, once its turn is due. A friend's beat  *)
(* runs on the beat lane and a read on the read lane (its own line, one    *)
(* read at a time), neither on the line. A caller can go away at any time  *)
(* (its client's deadline): a verb that has not started by then is        *)
(* dropped, never run, and a write gives its ticket up, so neither the     *)
(* writes behind it nor the tick wait for it.                              *)
(*                                                                         *)
(* The faults of 2026-10-04: every verb took the line; a batch whose       *)
(* caller had gone was still run when its turn came; the tick queued       *)
(* behind every batch waiting.                                             *)
(*                                                                         *)
(* The design is Broken = {}. Each other value is a reversed witness:      *)
(*   "beatonline"  a beat takes the line: BeatNeverWaitsForTheLine fails;  *)
(*   "readonline"  a read takes the line: ReadWaitsOnlyForReads fails;     *)
(*   "rungone"     a write whose caller went keeps its ticket and is run   *)
(*                 (a sync.Mutex that cannot be given up): GoneNeverRuns;  *)
(*   "noskip"      a write whose caller went keeps its ticket and is not   *)
(*                 run (a give-up that does not step over the ticket):     *)
(*                 NoTicketBlocksTheLine fails;                            *)
(*   "tickqueues"  the tick waits behind every write waiting (the stall    *)
(*                 of 12:52 PM): TickTakesItsTurn fails.                   *)
(***************************************************************************)
EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Callers, KindOf, MaxTicks, Broken

Kinds  == {"beat", "read", "write"}
Faults == {"beatonline", "readonline", "rungone", "noskip", "tickqueues"}
ASSUME Broken \subseteq Faults /\ "tick" \notin Callers /\ "none" \notin Callers
ASSUME \A r \in Callers : KindOf[r] \in Kinds

VARIABLES line,      \* the line's holder: "none", "tick" or a caller
          rline,     \* the read lane's holder: "none" or a caller
          q,         \* the tickets of the verbs waiting for the line, in order
          pc,        \* each caller: idle, waiting, running, answered, dropped
          gone,      \* each caller: its client has gone away
          goneWait,  \* ghost: it went while it was still waiting to start
          ran,       \* each caller: its verb ran (changed the sprint)
          tickWants, \* the run loop asks for the line
          turnDue,   \* the tick's turn is due (the batches have had their share)
          overtaken, \* ghost: a write took the line while the tick's turn was due
          ticks      \* ticks begun, bounded by MaxTicks

vars == <<line, rline, q, pc, gone, goneWait, ran, tickWants, turnDue, overtaken, ticks>>

OnLine(r) == KindOf[r] = "write"
             \/ (KindOf[r] = "beat" /\ "beatonline" \in Broken)
             \/ (KindOf[r] = "read" /\ "readonline" \in Broken)

InQ(r) == \E i \in 1..Len(q) : q[i] = r
Without(s, r) == SelectSeq(s, LAMBDA x : x # r)

TypeOK ==
  /\ line \in {"none", "tick"} \cup Callers
  /\ rline \in {"none"} \cup Callers
  /\ q \in Seq(Callers)
  /\ pc \in [Callers -> {"idle", "waiting", "running", "answered", "dropped"}]
  /\ gone \in [Callers -> BOOLEAN]
  /\ goneWait \in [Callers -> BOOLEAN]
  /\ ran \in [Callers -> BOOLEAN]
  /\ tickWants \in BOOLEAN /\ turnDue \in BOOLEAN /\ overtaken \in BOOLEAN
  /\ ticks \in 0..MaxTicks

Init ==
  /\ line = "none" /\ rline = "none" /\ q = <<>>
  /\ pc = [r \in Callers |-> "idle"]
  /\ gone = [r \in Callers |-> FALSE]
  /\ goneWait = [r \in Callers |-> FALSE]
  /\ ran = [r \in Callers |-> FALSE]
  /\ tickWants = FALSE /\ turnDue = TRUE /\ overtaken = FALSE
  /\ ticks = 0

Rest == <<rline, gone, goneWait, ran, tickWants, turnDue, overtaken, ticks>>

\* a caller sends its batch; a verb on the line takes a ticket
Send(r) == /\ pc[r] = "idle"
           /\ pc' = [pc EXCEPT ![r] = "waiting"]
           /\ q' = IF OnLine(r) THEN Append(q, r) ELSE q
           /\ UNCHANGED <<line, rline, gone, goneWait, ran, tickWants, turnDue, overtaken, ticks>>

\* the run loop asks for the line (TickLock)
TickAsk == /\ ~tickWants /\ line # "tick" /\ ticks < MaxTicks
           /\ tickWants' = TRUE
           /\ UNCHANGED <<line, rline, q, pc, gone, goneWait, ran, turnDue, overtaken, ticks>>

\* time passes: the batches have had the line for the tick's turn
TurnComes == /\ ~turnDue /\ line # "tick"
             /\ turnDue' = TRUE
             /\ UNCHANGED <<line, rline, q, pc, gone, goneWait, ran, tickWants, overtaken, ticks>>

\* the tick takes the line: when no write waits, or (the design) once its turn is due
TickBegin == /\ tickWants /\ line = "none"
             /\ (q = <<>> \/ (turnDue /\ "tickqueues" \notin Broken))
             /\ line' = "tick" /\ tickWants' = FALSE /\ ticks' = ticks + 1
             /\ UNCHANGED <<rline, q, pc, gone, goneWait, ran, turnDue, overtaken>>
TickEnd == /\ line = "tick"
           /\ line' = "none" /\ turnDue' = FALSE
           /\ UNCHANGED <<rline, q, pc, gone, goneWait, ran, tickWants, overtaken, ticks>>

\* a caller's client gives up (its deadline): a ticket waiting is given up and stepped
\* over (LockCtx, abandon), unless a fault keeps it
GiveUp(r) == /\ pc[r] \in {"waiting", "running"} /\ ~gone[r]
             /\ gone' = [gone EXCEPT ![r] = TRUE]
             /\ goneWait' = [goneWait EXCEPT ![r] = (pc[r] = "waiting")]
             /\ IF pc[r] = "waiting" /\ OnLine(r) /\ Broken \cap {"rungone", "noskip"} = {}
                  THEN /\ q' = Without(q, r) /\ pc' = [pc EXCEPT ![r] = "dropped"]
                  ELSE UNCHANGED <<q, pc>>
             /\ UNCHANGED <<line, rline, ran, tickWants, turnDue, overtaken, ticks>>

\* the write first in line takes the line, unless the tick's turn is due and it asks
TakeLine(r) == /\ pc[r] = "waiting" /\ OnLine(r) /\ line = "none"
               /\ q # <<>> /\ Head(q) = r
               /\ (~gone[r] \/ "rungone" \in Broken)
               /\ (~(tickWants /\ turnDue) \/ "tickqueues" \in Broken)
               /\ line' = r /\ q' = Tail(q)
               /\ pc' = [pc EXCEPT ![r] = "running"]
               /\ overtaken' = (overtaken \/ (tickWants /\ turnDue))
               /\ UNCHANGED <<rline, gone, goneWait, ran, tickWants, turnDue, ticks>>

\* a beat starts on the beat lane: no line, no other beat waited for; one whose caller
\* went first is dropped
BeatStart(r) == /\ pc[r] = "waiting" /\ KindOf[r] = "beat" /\ ~OnLine(r) /\ ~gone[r]
                /\ pc' = [pc EXCEPT ![r] = "running"]
                /\ UNCHANGED <<line, q>> /\ UNCHANGED Rest
\* a read starts on the read lane: it waits for a read ahead of it alone
ReadStart(r) == /\ pc[r] = "waiting" /\ KindOf[r] = "read" /\ ~OnLine(r) /\ rline = "none"
                /\ ~gone[r]
                /\ rline' = r /\ pc' = [pc EXCEPT ![r] = "running"]
                /\ UNCHANGED <<line, q, gone, goneWait, ran, tickWants, turnDue, overtaken, ticks>>
LaneDrop(r) == /\ pc[r] = "waiting" /\ ~OnLine(r) /\ gone[r]
               /\ pc' = [pc EXCEPT ![r] = "dropped"]
               /\ UNCHANGED <<line, q>> /\ UNCHANGED Rest

\* the verb runs and is answered; what it held is given back
Finish(r) == /\ pc[r] = "running"
             /\ ran' = [ran EXCEPT ![r] = TRUE]
             /\ pc' = [pc EXCEPT ![r] = "answered"]
             /\ line' = IF line = r THEN "none" ELSE line
             /\ rline' = IF rline = r THEN "none" ELSE rline
             /\ UNCHANGED <<q, gone, goneWait, tickWants, turnDue, overtaken, ticks>>

Next ==
  \/ TickAsk \/ TurnComes \/ TickBegin \/ TickEnd
  \/ \E r \in Callers : Send(r) \/ GiveUp(r) \/ TakeLine(r)
                        \/ BeatStart(r) \/ ReadStart(r) \/ LaneDrop(r) \/ Finish(r)

Fairness ==
  /\ WF_vars(TickEnd) /\ WF_vars(TurnComes) /\ SF_vars(TickBegin)
  /\ \A r \in Callers : /\ SF_vars(TakeLine(r)) /\ WF_vars(BeatStart(r))
                        /\ SF_vars(ReadStart(r)) /\ WF_vars(LaneDrop(r)) /\ WF_vars(Finish(r))

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

\* No ticket given up stands first in line: the writes behind it, and the tick, never
\* wait for a caller that has gone.
NoTicketBlocksTheLine == q # <<>> => ~gone[Head(q)]

\* Once the tick's turn is due and it asks, no write takes the line before it.
TickTakesItsTurn == ~overtaken

\* Every batch sent is answered or dropped, and every tick asked for begins.
EveryBatchEnds == \A r \in Callers :
  (pc[r] = "waiting") ~> (pc[r] \in {"answered", "dropped"})
TickRuns == tickWants ~> (line = "tick")
=============================================================================
