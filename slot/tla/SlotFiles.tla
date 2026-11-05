------------------------------ MODULE SlotFiles ------------------------------
\* nova-slot as the code lays it out on disk (slot/main.go taker.step,
\* acquire, release): the lock files gate, all and slot1..slotN in one
\* directory, each an OS lock (flock or LockFileEx) the kernel holds for a
\* process and lets go of when it dies, however it dies.
\*
\* A shared taker, once per poll: gate shared (fails while an exclusive taker
\* holds it), then slot1..slotN in order until one is free (the scan index is
\* the code's loop counter), then all shared, then lets go of the gate; on any
\* failure it lets go of what it took and polls again. An exclusive taker:
\* gate exclusive, then, holding it across polls, all exclusive, which is free
\* only when no shared holder is left; it may time out while it waits and give
\* the gate back. A run ends by finishing or by a crash (any time past idle),
\* and either way every lock of the process is let go.
\*
\* Each process takes at most Rounds times: the liveness here is "a waiter
\* eventually runs if holders finish" for a finite load. There is no arrival
\* order, so an endless stream of takers may pass one waiter (no FIFO line).
\*
\* Broken = "none" is the design. Reversed witnesses, each must fail:
\*   "nolock"  a shared taker takes the scanned slot without locking it:
\*             DistinctSlots fails, and so do AtMostN and the refinement
\*   "noall"   a shared taker never takes all: ExclusiveAlone fails
\*   "nogate"  an exclusive taker skips the gate: ExclusiveBarsShared fails
\*   "leak"    a crashed holder's locks are never let go (a PID file, a
\*             daemon's table): a waiter never runs (EveryWaiterRuns)
EXTENDS Naturals, FiniteSets

CONSTANTS Procs, N, Rounds, MaxCrash, Broken

None == "none"
Waiting == {"sgate", "sscan", "sall", "xgate", "xall"}

VARIABLES pc, scan, mine, slotEx, gateEx, gateSh, allEx, allSh, rounds, crashes

vars == <<pc, scan, mine, slotEx, gateEx, gateSh, allEx, allSh, rounds, crashes>>

TypeOK ==
  /\ pc \in [Procs -> {"idle", "dead"} \cup Waiting \cup {"srun", "xrun"}]
  /\ scan \in [Procs -> 1..N + 1] /\ mine \in [Procs -> 0..N]
  /\ slotEx \in [1..N -> Procs \cup {None}]
  /\ gateEx \in Procs \cup {None} /\ gateSh \subseteq Procs
  /\ allEx \in Procs \cup {None} /\ allSh \subseteq Procs
  /\ rounds \in [Procs -> 0..Rounds] /\ crashes \in 0..MaxCrash

Init ==
  /\ pc = [p \in Procs |-> "idle"] /\ scan = [p \in Procs |-> 1] /\ mine = [p \in Procs |-> 0]
  /\ slotEx = [k \in 1..N |-> None]
  /\ gateEx = None /\ gateSh = {} /\ allEx = None /\ allSh = {}
  /\ rounds = [p \in Procs |-> 0] /\ crashes = 0

Start(p) ==
  /\ pc[p] = "idle" /\ rounds[p] < Rounds
  /\ \E m \in {"sgate", "xgate"} : pc' = [pc EXCEPT ![p] = m]
  /\ rounds' = [rounds EXCEPT ![p] = @ + 1]
  /\ UNCHANGED <<scan, mine, slotEx, gateEx, gateSh, allEx, allSh, crashes>>

\* --- shared ---------------------------------------------------------------

SGate(p) ==
  /\ pc[p] = "sgate" /\ gateEx = None
  /\ gateSh' = gateSh \cup {p}
  /\ scan' = [scan EXCEPT ![p] = 1]
  /\ pc' = [pc EXCEPT ![p] = "sscan"]
  /\ UNCHANGED <<mine, slotEx, gateEx, allEx, allSh, rounds, crashes>>

SScan(p) ==
  /\ pc[p] = "sscan"
  /\ LET k == scan[p] IN
     IF k > N THEN \* every slot taken: let go of the gate, poll again
        /\ gateSh' = gateSh \ {p} /\ pc' = [pc EXCEPT ![p] = "sgate"]
        /\ UNCHANGED <<scan, mine, slotEx>>
     ELSE IF Broken = "nolock" THEN
        /\ mine' = [mine EXCEPT ![p] = k] /\ pc' = [pc EXCEPT ![p] = "sall"]
        /\ UNCHANGED <<scan, slotEx, gateSh>>
     ELSE IF slotEx[k] = None THEN
        /\ slotEx' = [slotEx EXCEPT ![k] = p] /\ mine' = [mine EXCEPT ![p] = k]
        /\ pc' = [pc EXCEPT ![p] = "sall"]
        /\ UNCHANGED <<scan, gateSh>>
     ELSE
        /\ scan' = [scan EXCEPT ![p] = k + 1]
        /\ UNCHANGED <<pc, mine, slotEx, gateSh>>
  /\ UNCHANGED <<gateEx, allEx, allSh, rounds, crashes>>

FreeSlot(p) == [k \in 1..N |-> IF slotEx[k] = p THEN None ELSE slotEx[k]]

SAll(p) ==
  /\ pc[p] = "sall"
  /\ gateSh' = gateSh \ {p}
  /\ IF Broken = "noall" THEN
        /\ pc' = [pc EXCEPT ![p] = "srun"] /\ UNCHANGED <<allSh, slotEx, mine>>
     ELSE IF allEx = None THEN
        /\ allSh' = allSh \cup {p} /\ pc' = [pc EXCEPT ![p] = "srun"]
        /\ UNCHANGED <<slotEx, mine>>
     ELSE
        /\ slotEx' = FreeSlot(p) /\ mine' = [mine EXCEPT ![p] = 0]
        /\ pc' = [pc EXCEPT ![p] = "sgate"] /\ UNCHANGED allSh
  /\ UNCHANGED <<scan, gateEx, allEx, rounds, crashes>>

SFinish(p) ==
  /\ pc[p] = "srun"
  /\ slotEx' = FreeSlot(p) /\ mine' = [mine EXCEPT ![p] = 0] /\ allSh' = allSh \ {p}
  /\ pc' = [pc EXCEPT ![p] = "idle"]
  /\ UNCHANGED <<scan, gateEx, gateSh, allEx, rounds, crashes>>

\* --- exclusive ------------------------------------------------------------

XGate(p) ==
  /\ pc[p] = "xgate"
  /\ IF Broken = "nogate" THEN UNCHANGED gateEx
     ELSE gateEx = None /\ gateSh = {} /\ gateEx' = p
  /\ pc' = [pc EXCEPT ![p] = "xall"]
  /\ UNCHANGED <<scan, mine, slotEx, gateSh, allEx, allSh, rounds, crashes>>

XAll(p) ==
  /\ pc[p] = "xall" /\ allEx = None /\ allSh = {}
  /\ allEx' = p /\ pc' = [pc EXCEPT ![p] = "xrun"]
  /\ UNCHANGED <<scan, mine, slotEx, gateEx, gateSh, allSh, rounds, crashes>>

XTimeout(p) ==
  /\ pc[p] = "xall"
  /\ gateEx' = IF gateEx = p THEN None ELSE gateEx
  /\ pc' = [pc EXCEPT ![p] = "idle"]
  /\ UNCHANGED <<scan, mine, slotEx, gateSh, allEx, allSh, rounds, crashes>>

XFinish(p) ==
  /\ pc[p] = "xrun"
  /\ allEx' = None /\ gateEx' = IF gateEx = p THEN None ELSE gateEx
  /\ pc' = [pc EXCEPT ![p] = "idle"]
  /\ UNCHANGED <<scan, mine, slotEx, gateSh, allSh, rounds, crashes>>

\* --- the kernel -----------------------------------------------------------

\* A process dies anywhere past idle; the kernel lets go of every lock it had.
Crash(p) ==
  /\ pc[p] \notin {"idle", "dead"} /\ crashes < MaxCrash
  /\ crashes' = crashes + 1
  /\ IF Broken = "leak" THEN
        /\ pc' = [pc EXCEPT ![p] = "dead"]
        /\ UNCHANGED <<mine, slotEx, gateEx, gateSh, allEx, allSh>>
     ELSE
        /\ pc' = [pc EXCEPT ![p] = "idle"] /\ mine' = [mine EXCEPT ![p] = 0]
        /\ slotEx' = FreeSlot(p)
        /\ gateEx' = (IF gateEx = p THEN None ELSE gateEx) /\ gateSh' = gateSh \ {p}
        /\ allEx' = (IF allEx = p THEN None ELSE allEx) /\ allSh' = allSh \ {p}
  /\ UNCHANGED <<scan, rounds>>

Step(p) == SGate(p) \/ SScan(p) \/ SAll(p) \/ SFinish(p) \/ XGate(p) \/ XAll(p) \/ XFinish(p)

Next == \E p \in Procs : Start(p) \/ Step(p) \/ XTimeout(p) \/ Crash(p)

\* Every process keeps taking its own steps; starting, timing out and dying
\* are never forced.
Fairness == \A p \in Procs :
  /\ WF_vars(SGate(p)) /\ WF_vars(SScan(p)) /\ WF_vars(SAll(p)) /\ WF_vars(SFinish(p))
  /\ WF_vars(XGate(p)) /\ WF_vars(XAll(p)) /\ WF_vars(XFinish(p))

Spec == Init /\ [][Next]_vars /\ Fairness

\* --- what must hold -------------------------------------------------------

SharedRun == {p \in Procs : pc[p] = "srun"}
ExclRun == {p \in Procs : pc[p] = "xrun"}

DistinctSlots == \A p, q \in SharedRun : p # q => mine[p] # mine[q]
AtMostN == Cardinality(SharedRun) <= N
ExclusiveAlone == ExclRun # {} => (SharedRun = {} /\ Cardinality(ExclRun) = 1)
\* An exclusive taker past the gate keeps every new shared taker out.
ExclusiveBarsShared ==
  (\E x \in Procs : pc[x] \in {"xall", "xrun"}) => \A q \in Procs : pc[q] \notin {"sscan", "sall"}
\* The layout: a locked slot file belongs to the process that scanned to it.
SlotOwned == \A k \in 1..N : slotEx[k] # None => mine[slotEx[k]] = k

EveryWaiterRuns == \A p \in Procs :
  pc[p] \in Waiting ~> pc[p] \in {"srun", "xrun", "idle", "dead"}

Count == INSTANCE SlotCount WITH shared <- SharedRun, excl <- ExclRun, Broken <- "none"
Refines == Count!Spec
=============================================================================
