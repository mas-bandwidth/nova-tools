----------------------------- MODULE ServerLine -----------------------------
\* The sprint server's serialization (cmd/nova-sprint serve.go serveCtx, lanes.go
\* serveOne and lockWithin; docs/SPEC-SPRINT.md section 14, The server, "A verb is
\* answered within a second"). The server has one line of control: a tick of the run
\* loop, a landing's step and a worker's write each hold it alone. Beside it are two
\* lanes, beats and reads, each its own lock over its own view of the store. A batch's
\* verbs run in order; each takes the lock of its kind for itself alone (a write the
\* line, a beat the beats lane, a read the reads lane) and frees it when it ends. A
\* verb waits for its lock at most W ticks of the server's clock; at W it is answered
\* busy and every later verb of its batch with it, having run nothing. Its abandoned
\* take stays in the lock's order and, when it comes, frees the lock at once (a ghost).
\* A sender may go at any time; a verb of a gone sender is not begun.
\*
\* State: line and lane, who holds each lock; pc, phase, waited, ans, ran, the
\* batches' progress; gone, the senders gone; ghosts, the abandoned takes; ticks and
\* lands, how many times the run loop's tick and the land loop have held the line
\* (bounded so the instance is finite); goneRan, a history flag set when a verb begins
\* for a sender already gone.
\*
\* Broken = "none" is the design. Every other value is a reversed witness, each
\* caught by one invariant:
\*   "oneline"       beats and reads take the line (the server before lanes.go): a
\*                   beat waits for a tick or a landing's step
\*                   (BeatsAndReadsWaitOnlyForTheirLane)
\*   "batchhold"     a batch takes the line once for all its verbs (serveFrom before
\*                   this change): the line is held between a batch's verbs
\*                   (LineHeldOnlyForOneWrite)
\*   "nobound"       a verb waits for its lock for as long as it is held
\*                   (BoundedWait)
\*   "rungone"       a verb is begun for a sender that has gone
\*                   (NothingRunsForAGoneSender)
\*   "busycontinues" a batch goes on past a verb answered busy
\*                   (NoVerbRunsAfterABusyOne)
\*   "ghostruns"     the abandoned take runs its verb when it comes
\*                   (AnUnrunAnswerRanNothing)
\* ReachBeatWhileLandHolds is a reach witness: the design answers a beat while a
\* landing's step holds the line (TestVerbAnsweredWhileLandInProgress), so the
\* invariant that says it never does fails.

EXTENDS Naturals, Sequences, FiniteSets

CONSTANTS Batches, W, MaxTicks, MaxLands, Broken

B == DOMAIN Batches
Kinds == {"write", "beat", "read"}
LaneKinds == {"beat", "read"}
Locks == {"line", "beats", "reads"}
Free == "free"

VARIABLES line, lane, pc, phase, waited, gone, ans, ran, ghosts, ticks, lands, goneRan
vars == <<line, lane, pc, phase, waited, gone, ans, ran, ghosts, ticks, lands, goneRan>>

Len_(b) == Len(Batches[b])
Kind(b) == Batches[b][pc[b]]

\* the lock a verb of kind k takes
LockOf(k) ==
  IF k = "write" \/ Broken \in {"oneline", "batchhold"} THEN "line"
  ELSE IF k = "beat" THEN "beats" ELSE "reads"

Held(l) == IF l = "line" THEN line ELSE lane[l]

\* the locks' holders after l is set to v
SetLock(l, v) ==
  IF l = "line" THEN line' = v /\ UNCHANGED lane
  ELSE lane' = [lane EXCEPT ![l] = v] /\ UNCHANGED line

Cap == IF Broken = "nobound" THEN W + 1 ELSE W

TypeOK ==
  /\ line \in {Free, "tick", "land"} \cup B
  /\ lane \in [{"beats", "reads"} -> {Free} \cup B]
  /\ \A b \in B : pc[b] \in 1..(Len_(b) + 1)
  /\ phase \in [B -> {"wait", "run", "done"}]
  /\ waited \in [B -> 0..(W + 1)]
  /\ gone \in [B -> BOOLEAN]
  /\ \A b \in B : ans[b] \in Seq({"ok", "busy", "gone"})
  /\ \A b \in B : ran[b] \subseteq 1..Len_(b)
  /\ goneRan \in BOOLEAN

Init ==
  /\ line = Free
  /\ lane = [l \in {"beats", "reads"} |-> Free]
  /\ pc = [b \in B |-> 1]
  /\ phase = [b \in B |-> "wait"]
  /\ waited = [b \in B |-> 0]
  /\ gone = [b \in B |-> FALSE]
  /\ ans = [b \in B |-> <<>>]
  /\ ran = [b \in B |-> {}]
  /\ ghosts = {}
  /\ ticks = 0
  /\ lands = 0
  /\ goneRan = FALSE

\* b's verb may take its lock: it is free, or (batchhold) b holds it from its last verb
CanTake(b) ==
  LET l == LockOf(Kind(b)) IN
  Held(l) = Free \/ (Broken = "batchhold" /\ Held(l) = b)

\* the verb takes its lock and runs; a gone sender's verb is not begun
Take(b) ==
  /\ phase[b] = "wait"
  /\ CanTake(b)
  /\ Broken = "rungone" \/ ~gone[b]
  /\ SetLock(LockOf(Kind(b)), b)
  /\ phase' = [phase EXCEPT ![b] = "run"]
  /\ waited' = [waited EXCEPT ![b] = 0]
  /\ ran' = [ran EXCEPT ![b] = @ \cup {pc[b]}]
  /\ goneRan' = (goneRan \/ gone[b])
  /\ UNCHANGED <<pc, gone, ans, ghosts, ticks, lands>>

\* the verb ends, answered, and frees its lock (batchhold: not before the batch ends)
End(b) ==
  /\ phase[b] = "run"
  /\ LET l == LockOf(Kind(b))
         last == pc[b] = Len_(b)
     IN /\ IF Broken = "batchhold" /\ ~last THEN UNCHANGED <<line, lane>> ELSE SetLock(l, Free)
        /\ phase' = [phase EXCEPT ![b] = IF last THEN "done" ELSE "wait"]
  /\ ans' = [ans EXCEPT ![b] = Append(@, "ok")]
  /\ pc' = [pc EXCEPT ![b] = @ + 1]
  /\ UNCHANGED <<waited, gone, ran, ghosts, ticks, lands, goneRan>>

\* the rest of b's batch answered a, from its verb pc on
Rest(b, a) == [i \in 1..(Len_(b) - pc[b] + 1) |-> a]

\* the take a verb gave up on, when its lock is held: it stays in the lock's order
Ghost(b) ==
  LET l == LockOf(Kind(b)) IN
  IF Held(l) # Free /\ Held(l) # b THEN ghosts \cup {<<b, pc[b], l>>} ELSE ghosts

\* the server's clock moves: every verb that waits has waited one tick more
Clock ==
  /\ \E b \in B : phase[b] = "wait" /\ ~CanTake(b) /\ waited[b] < Cap
  /\ waited' = [b \in B |-> IF phase[b] = "wait" /\ ~CanTake(b) /\ waited[b] < Cap THEN waited[b] + 1 ELSE waited[b]]
  /\ UNCHANGED <<line, lane, pc, phase, gone, ans, ran, ghosts, ticks, lands, goneRan>>

\* a verb that waited W is answered busy, and the rest of its batch with it
Busy(b) ==
  /\ Broken # "nobound"
  /\ phase[b] = "wait"
  /\ waited[b] = W
  /\ ghosts' = Ghost(b)
  /\ waited' = [waited EXCEPT ![b] = 0]
  /\ IF Broken = "busycontinues" /\ pc[b] < Len_(b)
     THEN /\ ans' = [ans EXCEPT ![b] = Append(@, "busy")]
          /\ pc' = [pc EXCEPT ![b] = @ + 1]
          /\ UNCHANGED phase
     ELSE /\ ans' = [ans EXCEPT ![b] = @ \o Rest(b, "busy")]
          /\ pc' = [pc EXCEPT ![b] = Len_(b) + 1]
          /\ phase' = [phase EXCEPT ![b] = "done"]
  /\ UNCHANGED <<line, lane, gone, ran, ticks, lands, goneRan>>

\* the sender goes (its connection closes): at any time before its batch is answered
Leave(b) ==
  /\ phase[b] # "done"
  /\ ~gone[b]
  /\ gone' = [gone EXCEPT ![b] = TRUE]
  /\ UNCHANGED <<line, lane, pc, phase, waited, ans, ran, ghosts, ticks, lands, goneRan>>

\* a waiting verb of a gone sender, and the rest of its batch, run nothing
Gone(b) ==
  /\ Broken # "rungone"
  /\ phase[b] = "wait"
  /\ gone[b]
  /\ ghosts' = Ghost(b)
  /\ ans' = [ans EXCEPT ![b] = @ \o Rest(b, "gone")]
  /\ pc' = [pc EXCEPT ![b] = Len_(b) + 1]
  /\ phase' = [phase EXCEPT ![b] = "done"]
  /\ waited' = [waited EXCEPT ![b] = 0]
  /\ UNCHANGED <<line, lane, gone, ran, ticks, lands, goneRan>>

\* an abandoned take comes: it takes the free lock and frees it at once, running nothing
\* (ghostruns: it runs the verb it was taken for)
GhostPass(g) ==
  /\ g \in ghosts
  /\ Held(g[3]) = Free
  /\ ghosts' = ghosts \ {g}
  /\ ran' = IF Broken = "ghostruns" THEN [ran EXCEPT ![g[1]] = @ \cup {g[2]}] ELSE ran
  /\ UNCHANGED <<line, lane, pc, phase, waited, gone, ans, ticks, lands, goneRan>>

\* the run loop's tick and a landing's step each hold the line, for as long as they take
TickTake == line = Free /\ ticks < MaxTicks /\ line' = "tick" /\ UNCHANGED <<lane, pc, phase, waited, gone, ans, ran, ghosts, ticks, lands, goneRan>>
TickEnd == line = "tick" /\ line' = Free /\ ticks' = ticks + 1 /\ UNCHANGED <<lane, pc, phase, waited, gone, ans, ran, ghosts, lands, goneRan>>
LandTake == line = Free /\ lands < MaxLands /\ line' = "land" /\ UNCHANGED <<lane, pc, phase, waited, gone, ans, ran, ghosts, ticks, lands, goneRan>>
LandEnd == line = "land" /\ line' = Free /\ lands' = lands + 1 /\ UNCHANGED <<lane, pc, phase, waited, gone, ans, ran, ghosts, ticks, goneRan>>

Next ==
  \/ \E b \in B : Take(b) \/ End(b) \/ Busy(b) \/ Leave(b) \/ Gone(b)
  \/ \E g \in ghosts : GhostPass(g)
  \/ Clock
  \/ TickTake \/ TickEnd \/ LandTake \/ LandEnd

Fairness ==
  /\ \A b \in B : WF_vars(Take(b)) /\ WF_vars(End(b)) /\ WF_vars(Busy(b)) /\ WF_vars(Gone(b))
  /\ WF_vars(Clock)
  /\ WF_vars(TickEnd) /\ WF_vars(LandEnd)
  /\ \A b \in B : \A i \in 1..Len_(b) : \A l \in Locks : WF_vars(GhostPass(<<b, i, l>>))

Spec == Init /\ [][Next]_vars /\ Fairness

\* ---- what the design keeps ----

\* a verb waits for its lock at most W ticks of the server's clock
BoundedWait == \A b \in B : waited[b] <= W

\* a beat or a read that cannot run waits only for a verb of its own kind running on its
\* lane: never for a tick, a landing's step or a worker's write
BeatsAndReadsWaitOnlyForTheirLane ==
  \A b \in B :
    (phase[b] = "wait" /\ Kind(b) \in LaneKinds /\ ~CanTake(b))
      => LET h == Held(LockOf(Kind(b))) IN
         h \in B /\ phase[h] = "run" /\ Kind(h) = Kind(b)

\* the line is held by a batch only while one write of it runs
LineHeldOnlyForOneWrite ==
  line \in B => (phase[line] = "run" /\ Kind(line) = "write")

\* no verb is begun for a sender that has gone
NothingRunsForAGoneSender == ~goneRan

\* after a verb answered busy, no later verb of its batch ran or was answered ok
NoVerbRunsAfterABusyOne ==
  \A b \in B : \A i \in 1..Len(ans[b]) :
    ans[b][i] = "busy" => \A j \in (i + 1)..Len_(b) : j \notin ran[b] /\ (j <= Len(ans[b]) => ans[b][j] # "ok")

\* a verb answered busy or gone ran nothing, before or after its answer
AnUnrunAnswerRanNothing ==
  \A b \in B : \A i \in 1..Len(ans[b]) : ans[b][i] \in {"busy", "gone"} => i \notin ran[b]

\* every batch is answered, every verb of it
EveryBatchAnswered == \A b \in B : <>(phase[b] = "done" /\ Len(ans[b]) = Len_(b))

\* ---- reach witness ----

\* says a beat never runs while a landing's step holds the line; the design breaks it
ReachBeatWhileLandHolds == ~(line = "land" /\ \E b \in B : phase[b] = "run" /\ Kind(b) = "beat")

=============================================================================
