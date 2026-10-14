---------------------------- MODULE LaneFault ----------------------------
(* A one-shot friend's lane fault (pkg/friend/daemon.go LaneFaultWatch, laneFaulted,
   laneFaultClearing; cmd/nova-friend/limit.go RefusalWatch, alikeLanes; docs/SPEC-FRIEND.md,
   the lanes). Lane refusals are watched: LaneFaultAlike identical real refusal lines in a row
   fault the lane, the fault file stands, and the lanes take no card. A line with no word in it
   (an empty line, a bare brace: BareLine) is no refusal: it is a read fault and counts toward
   nothing. A fault ends by a restart, a person's clear, or, the finding of 2026-10-14 (a friend's
   lanes locked on `refusal="{"` until a restart), by itself once it has stood Hold: the daemon's
   loop runs every second, a Hold of fifteen minutes, so time cannot pass the Hold with the
   fault standing and the clearing not done.

   CountBare = TRUE is the reversed witness of a bare line counted as a refusal (the old
   FirstErrorLine took the first line of a pretty-printed JSON error, "{"); SelfClear = FALSE the
   witness of a fault that only a restart or a person ends. WitnessFaulted and WitnessCleared are
   reversed witnesses: invariants written false in a state the design must reach, so TLC's
   counterexample is the proof it is reached. *)
EXTENDS Naturals

CONSTANTS Alike,      \* identical real refusals in a row that fault the lane (LaneFaultAlike)
          Hold,       \* ticks a fault stands before it clears itself (LaneFaultHold)
          MaxEvents,  \* bounds the refusal events a run sees
          CountBare,  \* a bare line counted as a refusal (the defect)
          SelfClear   \* the fault ends by itself after Hold

VARIABLES count,          \* identical real refusals counted in the current run
          faulted,        \* the lane is faulted: it takes no card
          file,           \* the fault file stands in the state directory
          age,            \* ticks the fault has stood (0..Hold, or Hold+1 meaning longer than the hold)
          events,         \* refusal events seen
          fromBare,       \* the fault was tripped counting a bare line
          clearedByHold   \* a fault has cleared itself

vars == <<count, faulted, file, age, events, fromBare, clearedByHold>>

TypeOK == /\ count \in 0..Alike
          /\ faulted \in BOOLEAN /\ file \in BOOLEAN
          /\ age \in 0..(Hold + 1)
          /\ events \in 0..MaxEvents
          /\ fromBare \in BOOLEAN /\ clearedByHold \in BOOLEAN

Init == /\ count = 0 /\ faulted = FALSE /\ file = FALSE /\ age = 0
        /\ events = 0 /\ fromBare = FALSE /\ clearedByHold = FALSE

\* the lanes meet a refusal line (bare: the line has no word in it); a faulted lane sees none
Meet(bare) ==
  /\ ~faulted /\ events < MaxEvents
  /\ events' = events + 1
  /\ IF bare /\ ~CountBare
     THEN UNCHANGED <<count, faulted, file, age, fromBare, clearedByHold>> \* a read fault: counted toward nothing
     ELSE IF count + 1 = Alike
          THEN /\ count' = 0 /\ faulted' = TRUE /\ file' = TRUE /\ age' = 0
               /\ fromBare' = bare /\ UNCHANGED clearedByHold
          ELSE /\ count' = count + 1
               /\ UNCHANGED <<faulted, file, age, fromBare, clearedByHold>>

Real == Meet(FALSE)
Bare == Meet(TRUE)

\* the daemon's loop steps: time passes only while the fault could not yet clear itself
Tick == /\ faulted /\ age < Hold + 1
        /\ ~(SelfClear /\ age = Hold)
        /\ age' = age + 1
        /\ UNCHANGED <<count, faulted, file, events, fromBare, clearedByHold>>

\* the fault that has stood Hold clears itself: the watch, the file (laneFaultClearing)
FaultClears == /\ SelfClear /\ faulted /\ age = Hold
               /\ faulted' = FALSE /\ file' = FALSE /\ count' = 0 /\ age' = 0
               /\ clearedByHold' = TRUE /\ fromBare' = FALSE
               /\ UNCHANGED events

\* a restart, or a person removing the fault file, ends it sooner
Restart == /\ faulted
           /\ faulted' = FALSE /\ file' = FALSE /\ count' = 0 /\ age' = 0 /\ fromBare' = FALSE
           /\ UNCHANGED <<events, clearedByHold>>

Ended == /\ events = MaxEvents /\ ~faulted /\ UNCHANGED vars

Next == Real \/ Bare \/ Tick \/ FaultClears \/ Restart \/ Ended

Fairness == WF_vars(Tick) /\ WF_vars(FaultClears)

Spec == Init /\ [][Next]_vars /\ Fairness

\* a bare line never faults a lane
BareNeverFaults == faulted => ~fromBare
\* the fault file is the fault, in every state
FileIsTheFault == file = faulted
\* a fault stands no longer than the hold when it can clear itself
HoldBounds == (SelfClear /\ faulted) => age <= Hold
\* every fault ends, without a person
FaultEnds == faulted ~> ~faulted

\* reversed witnesses: these are false in a state the design must reach
WitnessFaulted == ~faulted
WitnessCleared == ~clearedByHold
=============================================================================
