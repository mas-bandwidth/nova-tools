------------------------------ MODULE Capacity ------------------------------
\* How nova-reserve chooses N, as the code lays it out on disk (reserve/main.go
\* taker.step, storeCapacity, scan, setCapacity): the file capacity beside the
\* lock files gate, all and slot1..slotMaxSlots in one directory. SlotFiles.tla
\* checks the locks for a fixed N; this module checks that N is the machine's,
\* one value for every caller, and never the caller's own --slots.
\*
\* capFile is the file: 0 when absent, else the slot count it holds. Each
\* process takes once, as one of:
\*   shared     gate shared; read capFile. Absent: let go of the gate and
\*              claim (gate exclusive, then all exclusive, both without
\*              waiting, polled until taken); holding both, write --slots (or Default) if the file
\*              is still absent; let go; take again. Present: a --slots that
\*              differs is refused (exit 2); else scan slot1..slot<capFile>
\*              and take all shared, as in SlotFiles.tla.
\*   exclusive  gate exclusive, then all exclusive; capFile is not read.
\*   set        gate exclusive, then all exclusive, both without waiting;
\*              holding both, write the new value; else refused (exit 1).
\* want[p] is a shared taker's --slots (0: none given); ask[p] is set's value.
\* A process dies anywhere and the kernel lets go of its locks; the file is
\* written in one write, under the locks, so a crash leaves it whole.
\*
\* Broken = "none" is the design. Reversed witnesses, each must fail:
\*   "percaller"     a taker counts its own --slots, never the file (the bug
\*                   this module was written for: a --slots 1 holder and a
\*                   --slots 2 caller both run): AtMostCap fails
\*   "racyfirst"     a first taker writes the file holding only the gate
\*                   shared, so two first takers both write: OneCapacity fails
\*   "setwhileheld"  set writes without taking gate and all: OneCapacity fails
EXTENDS Naturals, FiniteSets

CONSTANTS Procs, MaxSlots, Default, InitCaps, MaxCrash, Broken

None == "none"
Waiting == {"sgate", "sread", "swrite", "sclaim", "sclaim2", "sscan", "sall",
            "xgate", "xall", "tgate", "tall"}
Run == {"srun", "xrun"}

VARIABLES pc, capFile, slotEx, gateEx, gateSh, allEx, allSh, scan, mine, adm, want, ask, crashes

vars == <<pc, capFile, slotEx, gateEx, gateSh, allEx, allSh, scan, mine, adm, want, ask, crashes>>

TypeOK ==
  /\ pc \in [Procs -> {"idle", "done"} \cup Waiting \cup Run]
  /\ capFile \in 0..MaxSlots
  /\ slotEx \in [1..MaxSlots -> Procs \cup {None}]
  /\ gateEx \in Procs \cup {None} /\ gateSh \subseteq Procs
  /\ allEx \in Procs \cup {None} /\ allSh \subseteq Procs
  /\ scan \in [Procs -> 1..MaxSlots + 1] /\ mine \in [Procs -> 0..MaxSlots]
  /\ adm \in [Procs -> 0..MaxSlots]
  /\ want \in [Procs -> 0..MaxSlots] /\ ask \in [Procs -> 0..MaxSlots]
  /\ crashes \in 0..MaxCrash

Init ==
  /\ pc = [p \in Procs |-> "idle"] /\ capFile \in InitCaps
  /\ slotEx = [k \in 1..MaxSlots |-> None]
  /\ gateEx = None /\ gateSh = {} /\ allEx = None /\ allSh = {}
  /\ scan = [p \in Procs |-> 1] /\ mine = [p \in Procs |-> 0] /\ adm = [p \in Procs |-> 0]
  /\ want = [p \in Procs |-> 0] /\ ask = [p \in Procs |-> 0] /\ crashes = 0

Locks == <<slotEx, gateEx, gateSh, allEx, allSh>>

\* A process starts as a shared taker with some --slots (or none), an
\* exclusive taker, or a set with some value.
Start(p) ==
  /\ pc[p] = "idle"
  /\ \/ \E n \in 0..MaxSlots : pc' = [pc EXCEPT ![p] = "sgate"] /\ want' = [want EXCEPT ![p] = n] /\ UNCHANGED ask
     \/ pc' = [pc EXCEPT ![p] = "xgate"] /\ UNCHANGED <<want, ask>>
     \/ \E n \in 1..MaxSlots : pc' = [pc EXCEPT ![p] = "tgate"] /\ ask' = [ask EXCEPT ![p] = n] /\ UNCHANGED want
  /\ UNCHANGED <<capFile, Locks, scan, mine, adm, crashes>>

Stored(p) == IF want[p] = 0 THEN Default ELSE want[p]

\* --- shared ---------------------------------------------------------------

SGate(p) ==
  /\ pc[p] = "sgate" /\ gateEx = None
  /\ gateSh' = gateSh \cup {p} /\ pc' = [pc EXCEPT ![p] = "sread"]
  /\ UNCHANGED <<capFile, slotEx, gateEx, allEx, allSh, scan, mine, adm, want, ask, crashes>>

\* readCapacity under the gate, shared: set and a first taker's write hold the
\* gate exclusive, so the value cannot change until this taker lets go.
SRead(p) ==
  /\ pc[p] = "sread"
  /\ CASE Broken = "percaller" /\ want[p] # 0 ->
            /\ adm' = [adm EXCEPT ![p] = want[p]] /\ scan' = [scan EXCEPT ![p] = 1]
            /\ pc' = [pc EXCEPT ![p] = "sscan"] /\ UNCHANGED gateSh
       [] capFile = 0 /\ Broken = "racyfirst" ->
            /\ pc' = [pc EXCEPT ![p] = "swrite"] /\ UNCHANGED <<gateSh, adm, scan>>
       [] capFile = 0 ->
            /\ gateSh' = gateSh \ {p} /\ pc' = [pc EXCEPT ![p] = "sclaim"] /\ UNCHANGED <<adm, scan>>
       [] want[p] # 0 /\ want[p] # capFile -> \* refused: exit 2, nothing held
            /\ gateSh' = gateSh \ {p} /\ pc' = [pc EXCEPT ![p] = "done"] /\ UNCHANGED <<adm, scan>>
       [] OTHER ->
            /\ adm' = [adm EXCEPT ![p] = capFile] /\ scan' = [scan EXCEPT ![p] = 1]
            /\ pc' = [pc EXCEPT ![p] = "sscan"] /\ UNCHANGED gateSh
  /\ UNCHANGED <<capFile, slotEx, gateEx, allEx, allSh, mine, want, ask, crashes>>

\* "racyfirst" only: the write with no exclusive lock, whatever is there now.
SWrite(p) ==
  /\ pc[p] = "swrite"
  /\ capFile' = Stored(p) /\ gateSh' = gateSh \ {p} /\ pc' = [pc EXCEPT ![p] = "sgate"]
  /\ UNCHANGED <<slotEx, gateEx, allEx, allSh, scan, mine, adm, want, ask, crashes>>

ClaimFree == gateEx = None /\ gateSh = {}

SClaimOK(p) ==
  /\ pc[p] = "sclaim" /\ ClaimFree
  /\ gateEx' = p /\ pc' = [pc EXCEPT ![p] = "sclaim2"]
  /\ UNCHANGED <<capFile, slotEx, gateSh, allEx, allSh, scan, mine, adm, want, ask, crashes>>

\* A busy gate leaves the claimer at sclaim: it polls the claim again, never
\* the gate shared (taker.claiming). Going back to sgate instead lets two
\* first takers keep each other's claim out forever; TLC found that loop.

\* all exclusive; if taken, write unless another first taker already did,
\* then take again; if busy, claim again.
SClaim2(p) ==
  /\ pc[p] = "sclaim2"
  /\ IF allEx = None /\ allSh = {} THEN
        /\ capFile' = IF capFile = 0 THEN Stored(p) ELSE capFile
        /\ pc' = [pc EXCEPT ![p] = "sgate"]
     ELSE UNCHANGED capFile /\ pc' = [pc EXCEPT ![p] = "sclaim"]
  /\ gateEx' = None
  /\ UNCHANGED <<slotEx, gateSh, allEx, allSh, scan, mine, adm, want, ask, crashes>>

\* The layout: slot1..slot<adm[p]> in order, the first free one.
SScan(p) ==
  /\ pc[p] = "sscan"
  /\ LET k == scan[p] IN
     IF k > adm[p] THEN
        /\ gateSh' = gateSh \ {p} /\ pc' = [pc EXCEPT ![p] = "sgate"]
        /\ UNCHANGED <<scan, mine, slotEx>>
     ELSE IF slotEx[k] = None THEN
        /\ slotEx' = [slotEx EXCEPT ![k] = p] /\ mine' = [mine EXCEPT ![p] = k]
        /\ pc' = [pc EXCEPT ![p] = "sall"] /\ UNCHANGED <<scan, gateSh>>
     ELSE
        /\ scan' = [scan EXCEPT ![p] = k + 1] /\ UNCHANGED <<pc, mine, slotEx, gateSh>>
  /\ UNCHANGED <<capFile, gateEx, allEx, allSh, adm, want, ask, crashes>>

FreeSlot(p) == [k \in 1..MaxSlots |-> IF slotEx[k] = p THEN None ELSE slotEx[k]]

SAll(p) ==
  /\ pc[p] = "sall"
  /\ gateSh' = gateSh \ {p}
  /\ IF allEx = None THEN
        /\ allSh' = allSh \cup {p} /\ pc' = [pc EXCEPT ![p] = "srun"] /\ UNCHANGED <<slotEx, mine>>
     ELSE
        /\ slotEx' = FreeSlot(p) /\ mine' = [mine EXCEPT ![p] = 0]
        /\ pc' = [pc EXCEPT ![p] = "sgate"] /\ UNCHANGED allSh
  /\ UNCHANGED <<capFile, gateEx, allEx, scan, adm, want, ask, crashes>>

SFinish(p) ==
  /\ pc[p] = "srun"
  /\ slotEx' = FreeSlot(p) /\ mine' = [mine EXCEPT ![p] = 0] /\ allSh' = allSh \ {p}
  /\ adm' = [adm EXCEPT ![p] = 0] /\ pc' = [pc EXCEPT ![p] = "done"]
  /\ UNCHANGED <<capFile, gateEx, gateSh, allEx, scan, want, ask, crashes>>

\* --- exclusive ------------------------------------------------------------

XGate(p) ==
  /\ pc[p] = "xgate" /\ ClaimFree
  /\ gateEx' = p /\ pc' = [pc EXCEPT ![p] = "xall"]
  /\ UNCHANGED <<capFile, slotEx, gateSh, allEx, allSh, scan, mine, adm, want, ask, crashes>>

XAll(p) ==
  /\ pc[p] = "xall" /\ allEx = None /\ allSh = {}
  /\ allEx' = p /\ pc' = [pc EXCEPT ![p] = "xrun"]
  /\ UNCHANGED <<capFile, slotEx, gateEx, gateSh, allSh, scan, mine, adm, want, ask, crashes>>

XFinish(p) ==
  /\ pc[p] = "xrun"
  /\ allEx' = None /\ gateEx' = None /\ pc' = [pc EXCEPT ![p] = "done"]
  /\ UNCHANGED <<capFile, slotEx, gateSh, allSh, scan, mine, adm, want, ask, crashes>>

\* --- set ------------------------------------------------------------------

TGate(p) ==
  /\ pc[p] = "tgate"
  /\ CASE Broken = "setwhileheld" ->
            capFile' = ask[p] /\ pc' = [pc EXCEPT ![p] = "done"] /\ UNCHANGED gateEx
       [] ClaimFree ->
            gateEx' = p /\ pc' = [pc EXCEPT ![p] = "tall"] /\ UNCHANGED capFile
       [] OTHER -> \* refused: capacity in use, exit 1
            pc' = [pc EXCEPT ![p] = "done"] /\ UNCHANGED <<capFile, gateEx>>
  /\ UNCHANGED <<slotEx, gateSh, allEx, allSh, scan, mine, adm, want, ask, crashes>>

TAll(p) ==
  /\ pc[p] = "tall"
  /\ capFile' = IF allEx = None /\ allSh = {} THEN ask[p] ELSE capFile
  /\ gateEx' = None /\ pc' = [pc EXCEPT ![p] = "done"]
  /\ UNCHANGED <<slotEx, gateSh, allEx, allSh, scan, mine, adm, want, ask, crashes>>

\* --- the kernel -----------------------------------------------------------

Crash(p) ==
  /\ pc[p] \notin {"idle", "done"} /\ crashes < MaxCrash
  /\ crashes' = crashes + 1 /\ pc' = [pc EXCEPT ![p] = "done"]
  /\ slotEx' = FreeSlot(p) /\ mine' = [mine EXCEPT ![p] = 0] /\ adm' = [adm EXCEPT ![p] = 0]
  /\ gateEx' = (IF gateEx = p THEN None ELSE gateEx) /\ gateSh' = gateSh \ {p}
  /\ allEx' = (IF allEx = p THEN None ELSE allEx) /\ allSh' = allSh \ {p}
  /\ UNCHANGED <<capFile, scan, want, ask>>

Next == \E p \in Procs :
  \/ Start(p) \/ SGate(p) \/ SRead(p) \/ SWrite(p) \/ SClaimOK(p)
  \/ SClaim2(p) \/ SScan(p) \/ SAll(p) \/ SFinish(p)
  \/ XGate(p) \/ XAll(p) \/ XFinish(p) \/ TGate(p) \/ TAll(p) \/ Crash(p)

\* A lock that is free again and again is taken at last (strong fairness): a
\* poller tries once a second, and holders come and go.
Fairness == \A p \in Procs :
  /\ SF_vars(SGate(p)) /\ SF_vars(SClaimOK(p)) /\ SF_vars(XGate(p)) /\ SF_vars(XAll(p))
  /\ WF_vars(SRead(p)) /\ WF_vars(SWrite(p)) /\ WF_vars(SClaim2(p))
  /\ WF_vars(SScan(p)) /\ WF_vars(SAll(p)) /\ WF_vars(SFinish(p)) /\ WF_vars(XFinish(p))
  /\ WF_vars(TGate(p)) /\ WF_vars(TAll(p))

Spec == Init /\ [][Next]_vars /\ Fairness

\* --- what must hold -------------------------------------------------------

SharedRun == {p \in Procs : pc[p] = "srun"}
ExclRun == {p \in Procs : pc[p] = "xrun"}

\* At most the machine's capacity of shared holders, whoever asked for what.
AtMostCap == Cardinality(SharedRun) <= capFile
\* Every shared holder was let in under the capacity the file holds now.
OneCapacity == \A p \in SharedRun : adm[p] = capFile
\* The layout: a holder's slot is one of slot1..slot<capFile>, its own.
InCapacity == \A p \in SharedRun : mine[p] \in 1..capFile /\ slotEx[mine[p]] = p
DistinctSlots == \A p, q \in SharedRun : p # q => mine[p] # mine[q]
ExclusiveAlone == ExclRun # {} => (SharedRun = {} /\ Cardinality(ExclRun) = 1)
\* Once stored, the capacity is never lost.
StaysStored == [][capFile # 0 => capFile' # 0]_vars

EveryTakerEnds == \A p \in Procs : pc[p] \in Waiting ~> pc[p] \in Run \cup {"done"}
=============================================================================
