------------------------------ MODULE Reserve ------------------------------
\* nova-reserve resource ledger, priority displacement, between-command pause,
\* arrival-order waiter line, live capacity updates, and OS flock crash reaping.
\*
\* A machine has HasTag (optional hardware tag) and cap[r] whole-number units
\* for each resource r in Resources. Each hold or waiter owns an OS flock on its
\* monotonic <seq>.lock file; Crash(p) closes p's descriptor in the kernel
\* (lockHeld), and every critical section under <dir>/mutex reaps dead locks and
\* reconciles fitting paused holds before admitting any waiter or new request.
\*
\* Broken = "none" is the design. Reversed witnesses, each must fail:
\*   "overcap"       CI acquires without displacing holds: NeverOver fails
\*   "cpuonly"       CI displaces only for cpu, ignoring mem: NeverOver fails
\*   "ciqueues"      CI waits in line when the machine is full: CINeverWaits fails
\*   "wrongvictim"   CI pauses higher-priority / older hold first: VictimOrder fails
\*   "cutpause"      a waiter or new arrival takes spare room while a hold stays paused: UnpauseFirst fails
\*   "nobetween"     a paused multi-step consumer drops its pause between steps: UnpauseFirst fails
\*   "wrongunpause"  unpause resumes lowest-priority paused hold first: UnpauseFirst fails
\*   "skiphead"      a smaller later waiter passes the head of line: ArrivalOrder fails
\*   "queueneverfit" a request that can never fit queues instead of refusing: NeverFitRefused fails
\*   "leak"          a crashed holder's lock is never freed: AllSettle fails
EXTENDS Naturals, FiniteSets

CONSTANTS Procs, Resources, MaxCap, InitCap, HasTag, MaxPrio, MaxStarts, MaxCrash, MaxSet, Broken

VARIABLES pc, kind, prio, reqNeeds, reqTag, stepsLeft, inFlight,
          starts, seq, holdSeq, lineSeq, lockHeld, cap, crashes, sets, wasPausedStep

vars == <<pc, kind, prio, reqNeeds, reqTag, stepsLeft, inFlight,
          starts, seq, holdSeq, lineSeq, lockHeld, cap, crashes, sets, wasPausedStep>>

States == {"idle", "wait", "paused", "run", "between", "done"}
DefaultInitCap == [r \in Resources |-> MaxCap]
BaseNeeds == [r \in Resources |-> 1]
MaxSeqVal == 2 * MaxStarts

TypeOK ==
  /\ DOMAIN pc = Procs /\ \A p \in Procs : pc[p] \in States
  /\ DOMAIN kind = Procs /\ \A p \in Procs : kind[p] \in {"work", "ci"}
  /\ DOMAIN prio = Procs /\ \A p \in Procs : prio[p] \in 1..MaxPrio
  /\ DOMAIN reqNeeds = Procs /\ \A p \in Procs :
       DOMAIN reqNeeds[p] = Resources /\ \A r \in Resources : reqNeeds[p][r] \in 1..MaxCap + 1
  /\ DOMAIN reqTag = Procs /\ \A p \in Procs : reqTag[p] \in BOOLEAN
  /\ DOMAIN stepsLeft = Procs /\ \A p \in Procs : stepsLeft[p] \in 0..1
  /\ DOMAIN inFlight = Procs /\ \A p \in Procs : inFlight[p] \in BOOLEAN
  /\ starts \in 0..MaxStarts
  /\ seq \in 0..MaxSeqVal
  /\ DOMAIN holdSeq = Procs /\ \A p \in Procs : holdSeq[p] \in 0..MaxSeqVal
  /\ DOMAIN lineSeq = Procs /\ \A p \in Procs : lineSeq[p] \in 0..MaxSeqVal
  /\ lockHeld \subseteq Procs
  /\ DOMAIN cap = Resources /\ \A r \in Resources : cap[r] \in 1..MaxCap
  /\ crashes \in 0..MaxCrash
  /\ sets \in 0..MaxSet
  /\ wasPausedStep \subseteq Procs

Init ==
  /\ pc = [p \in Procs |-> "idle"]
  /\ kind = [p \in Procs |-> "work"]
  /\ prio = [p \in Procs |-> 1]
  /\ reqNeeds = [p \in Procs |-> BaseNeeds]
  /\ reqTag = [p \in Procs |-> FALSE]
  /\ stepsLeft = [p \in Procs |-> 0]
  /\ inFlight = [p \in Procs |-> FALSE]
  /\ starts = 0
  /\ seq = 0
  /\ holdSeq = [p \in Procs |-> 0]
  /\ lineSeq = [p \in Procs |-> 0]
  /\ lockHeld = {}
  /\ cap = InitCap
  /\ crashes = 0
  /\ sets = 0
  /\ wasPausedStep = {}

RECURSIVE SumSet(_, _, _)
SumSet(S, needsFn, r) ==
  IF S = {} THEN 0
  ELSE LET x == CHOOSE p \in S : TRUE IN needsFn[x][r] + SumSet(S \ {x}, needsFn, r)

LiveHolds == {p \in Procs : holdSeq[p] > 0 /\ (p \in lockHeld \/ pc[p] = "between")}
LiveUnpaused == {p \in LiveHolds : pc[p] = "run"}
LiveCI == {p \in LiveUnpaused : kind[p] = "ci"}
LivePaused == {p \in LiveHolds : pc[p] \in {"paused", "between"}}
LiveLine == {p \in Procs : lineSeq[p] > 0 /\ p \in lockHeld}

FitsMachine(n, tag, c) == (~tag \/ HasTag) /\ (\A r \in Resources : n[r] <= c[r])

FitsFree(n, U, needsFn, c) == \A r \in Resources : SumSet(U, needsFn, r) + n[r] <= c[r]

IsLineHead(p, L) == p \in L /\ (\A q \in L : lineSeq[p] <= lineSeq[q])

CheckResources ==
  CASE Broken = "overcap" -> {}
    [] Broken = "cpuonly" -> {"cpu"} \cap Resources
    [] OTHER              -> Resources

RECURSIVE Displace(_, _, _, _, _, _)
Displace(U, ciSet, ciNeeds, needsFn, prioFn, hseqFn) ==
  LET over == {r \in CheckResources :
                 SumSet(U, needsFn, r) + SumSet(ciSet, needsFn, r) + ciNeeds[r] > cap[r]}
      cands == {q \in U : \E r \in over : needsFn[q][r] > 0}
  IN IF cands = {} THEN U
     ELSE LET v == IF Broken = "wrongvictim"
                   THEN CHOOSE q \in cands : \A o \in cands :
                          prioFn[q] > prioFn[o] \/ (prioFn[q] = prioFn[o] /\ hseqFn[q] <= hseqFn[o])
                   ELSE CHOOSE q \in cands : \A o \in cands :
                          prioFn[q] < prioFn[o] \/ (prioFn[q] = prioFn[o] /\ hseqFn[q] >= hseqFn[o])
          IN Displace(U \ {v}, ciSet, ciNeeds, needsFn, prioFn, hseqFn)

RECURSIVE UnpauseSet(_, _, _, _, _, _)
UnpauseSet(P, U, needsFn, prioFn, hseqFn, c) ==
  IF P = {} THEN U
  ELSE LET best == IF Broken = "wrongunpause"
                   THEN CHOOSE q \in P : \A o \in P :
                          prioFn[q] < prioFn[o] \/ (prioFn[q] = prioFn[o] /\ hseqFn[q] >= hseqFn[o])
                   ELSE CHOOSE q \in P : \A o \in P :
                          prioFn[q] > prioFn[o] \/ (prioFn[q] = prioFn[o] /\ hseqFn[q] <= hseqFn[o])
           nextU == IF FitsFree(needsFn[best], U, needsFn, c) THEN U \cup {best} ELSE U
       IN UnpauseSet(P \ {best}, nextU, needsFn, prioFn, hseqFn, c)

ReapHoldSeq(hs, lk, pcFn) ==
  [q \in Procs |-> IF q \in lk \/ pcFn[q] = "between" THEN hs[q] ELSE 0]

ReapLineSeq(ls, lk) ==
  [q \in Procs |-> IF q \in lk THEN ls[q] ELSE 0]

CleanKind(pcFn, kFn) == [q \in Procs |-> IF pcFn[q] \in {"idle", "done"} THEN "work" ELSE kFn[q]]
CleanPrio(pcFn, pFn) == [q \in Procs |-> IF pcFn[q] \in {"idle", "done"} THEN 1 ELSE pFn[q]]
CleanNeeds(pcFn, nFn) == [q \in Procs |-> IF pcFn[q] \in {"idle", "done"} THEN BaseNeeds ELSE nFn[q]]
CleanTag(pcFn, tFn) == [q \in Procs |-> IF pcFn[q] \in {"idle", "done"} THEN FALSE ELSE tFn[q]]
CleanSteps(pcFn, sFn) == [q \in Procs |-> IF pcFn[q] \in {"idle", "done"} THEN 0 ELSE sFn[q]]

ApplyReconcile(hs, pcFn, infFn, wpSet, needsFn, prioFn, c) ==
  LET u0 == {q \in Procs : hs[q] > 0 /\ pcFn[q] = "run"}
      p0 == {q \in Procs : hs[q] > 0 /\ pcFn[q] = "paused"}
      u1 == UnpauseSet(p0, u0, needsFn, prioFn, hs, c)
      resumed == u1 \ u0
  IN [pc |-> [q \in Procs |-> IF q \in resumed THEN "run" ELSE pcFn[q]],
      inf |-> [q \in Procs |-> IF q \in resumed THEN TRUE ELSE infFn[q]],
      wp |-> wpSet \ resumed]

\* --- Actions ---------------------------------------------------------------

AcquireWorkWith(p, pr, n, tag, nw, st) ==
  /\ pc[p] = "idle" /\ starts < MaxStarts
  /\ LET hs0 == ReapHoldSeq(holdSeq, lockHeld, pc)
         ls0 == ReapLineSeq(lineSeq, lockHeld)
         rec == ApplyReconcile(hs0, pc, inFlight, wasPausedStep, reqNeeds, prio, cap)
         needs1 == [reqNeeds EXCEPT ![p] = n]
         prio1 == [prio EXCEPT ![p] = pr]
         kind1 == [kind EXCEPT ![p] = "work"]
         tag1 == [reqTag EXCEPT ![p] = tag]
         st1 == [stepsLeft EXCEPT ![p] = st]
         u0 == {q \in Procs : hs0[q] > 0 /\ rec.pc[q] = "run"}
         p0 == {q \in Procs : hs0[q] > 0 /\ rec.pc[q] \in {"paused", "between"}}
         l0 == {q \in Procs : ls0[q] > 0}
         canGrant == /\ (Broken = "cutpause" \/ p0 = {})
                     /\ (Broken = "skiphead" \/ l0 = {})
                     /\ FitsFree(n, u0, needs1, cap)
         pcOut == IF ~FitsMachine(n, tag, cap) THEN
                    IF Broken = "queueneverfit" THEN [rec.pc EXCEPT ![p] = "wait"]
                    ELSE [rec.pc EXCEPT ![p] = "done"]
                  ELSE IF canGrant THEN [rec.pc EXCEPT ![p] = "run"]
                  ELSE IF nw THEN [rec.pc EXCEPT ![p] = "done"]
                  ELSE [rec.pc EXCEPT ![p] = "wait"]
     IN
     /\ starts' = starts + 1
     /\ pc' = pcOut
     /\ kind' = CleanKind(pcOut, kind1)
     /\ prio' = CleanPrio(pcOut, prio1)
     /\ reqNeeds' = CleanNeeds(pcOut, needs1)
     /\ reqTag' = CleanTag(pcOut, tag1)
     /\ stepsLeft' = CleanSteps(pcOut, st1)
     /\ wasPausedStep' = rec.wp
     /\ IF pcOut[p] = "done" THEN
          /\ seq' = seq
          /\ holdSeq' = hs0 /\ lineSeq' = ls0
          /\ inFlight' = rec.inf /\ lockHeld' = lockHeld
        ELSE IF pcOut[p] = "run" THEN
          /\ seq' = seq + 1
          /\ holdSeq' = [hs0 EXCEPT ![p] = seq + 1] /\ lineSeq' = ls0
          /\ inFlight' = [rec.inf EXCEPT ![p] = TRUE]
          /\ lockHeld' = lockHeld \cup {p}
        ELSE
          /\ seq' = seq + 1
          /\ holdSeq' = hs0 /\ lineSeq' = [ls0 EXCEPT ![p] = seq + 1]
          /\ inFlight' = rec.inf
          /\ lockHeld' = lockHeld \cup {p}
  /\ UNCHANGED <<cap, crashes, sets>>

AcquireWork(p) ==
  \E pr \in 1..MaxPrio, n \in [Resources -> 1..MaxCap + 1],
     tag \in BOOLEAN, nw \in BOOLEAN, st \in 0..1 :
     AcquireWorkWith(p, pr, n, tag, nw, st)

AcquireCIWith(p, n, tag) ==
  /\ pc[p] = "idle" /\ starts < MaxStarts
  /\ LET hs0 == ReapHoldSeq(holdSeq, lockHeld, pc)
         ls0 == ReapLineSeq(lineSeq, lockHeld)
         rec == ApplyReconcile(hs0, pc, inFlight, wasPausedStep, reqNeeds, prio, cap)
         needs1 == [reqNeeds EXCEPT ![p] = n]
         prio1 == [prio EXCEPT ![p] = MaxPrio]
         kind1 == [kind EXCEPT ![p] = "ci"]
         tag1 == [reqTag EXCEPT ![p] = tag]
         st1 == [stepsLeft EXCEPT ![p] = 0]
         u0 == {q \in Procs : hs0[q] > 0 /\ rec.pc[q] = "run"}
         ci0 == {q \in u0 : kind[q] = "ci"}
         work0 == u0 \ ci0
         kept == Displace(work0, ci0, n, needs1, prio1, hs0)
         displaced == work0 \ kept
         pcOut == IF ~FitsMachine(n, tag, cap) THEN [rec.pc EXCEPT ![p] = "done"]
                  ELSE IF Broken = "ciqueues" /\ ~FitsFree(n, u0, needs1, cap) THEN [rec.pc EXCEPT ![p] = "wait"]
                  ELSE [q \in Procs |->
                          IF q = p THEN "run"
                          ELSE IF q \in displaced THEN "paused"
                          ELSE rec.pc[q]]
     IN
     /\ (~FitsMachine(n, tag, cap) \/ FitsFree(n, ci0, needs1, cap)) \* R39a: concurrent CI jobs fit
     /\ starts' = starts + 1
     /\ pc' = pcOut
     /\ kind' = CleanKind(pcOut, kind1)
     /\ prio' = CleanPrio(pcOut, prio1)
     /\ reqNeeds' = CleanNeeds(pcOut, needs1)
     /\ reqTag' = CleanTag(pcOut, tag1)
     /\ stepsLeft' = CleanSteps(pcOut, st1)
     /\ wasPausedStep' = rec.wp
     /\ IF pcOut[p] = "done" THEN
          /\ seq' = seq
          /\ holdSeq' = hs0 /\ lineSeq' = ls0
          /\ inFlight' = rec.inf /\ lockHeld' = lockHeld
        ELSE IF pcOut[p] = "wait" THEN
          /\ seq' = seq + 1
          /\ holdSeq' = hs0 /\ lineSeq' = [ls0 EXCEPT ![p] = seq + 1]
          /\ inFlight' = rec.inf /\ lockHeld' = lockHeld \cup {p}
        ELSE
          /\ seq' = seq + 1
          /\ holdSeq' = [hs0 EXCEPT ![p] = seq + 1] /\ lineSeq' = ls0
          /\ inFlight' = [rec.inf EXCEPT ![p] = TRUE]
          /\ lockHeld' = lockHeld \cup {p}
  /\ UNCHANGED <<cap, crashes, sets>>

AcquireCI(p) ==
  \E n \in [Resources -> 1..MaxCap + 1], tag \in BOOLEAN : AcquireCIWith(p, n, tag)

WakeWaiter(p) ==
  /\ pc[p] = "wait" /\ p \in lockHeld
  /\ LET hs0 == ReapHoldSeq(holdSeq, lockHeld, pc)
         ls0 == ReapLineSeq(lineSeq, lockHeld)
         rec == ApplyReconcile(hs0, pc, inFlight, wasPausedStep, reqNeeds, prio, cap)
         u1 == {q \in Procs : hs0[q] > 0 /\ rec.pc[q] = "run"}
         p1 == {q \in Procs : hs0[q] > 0 /\ rec.pc[q] \in {"paused", "between"}}
         l1 == {q \in Procs : ls0[q] > 0}
     IN
     IF ~FitsMachine(reqNeeds[p], reqTag[p], cap) THEN
       LET pcOut == [rec.pc EXCEPT ![p] = "done"] IN
       /\ seq' = seq
       /\ holdSeq' = hs0 /\ lineSeq' = [ls0 EXCEPT ![p] = 0]
       /\ pc' = pcOut
       /\ kind' = CleanKind(pcOut, kind) /\ prio' = CleanPrio(pcOut, prio)
       /\ reqNeeds' = CleanNeeds(pcOut, reqNeeds) /\ reqTag' = CleanTag(pcOut, reqTag)
       /\ stepsLeft' = CleanSteps(pcOut, stepsLeft)
       /\ inFlight' = rec.inf /\ lockHeld' = lockHeld \ {p} /\ wasPausedStep' = rec.wp
     ELSE
       LET pcOut == [rec.pc EXCEPT ![p] = "run"] IN
       /\ (Broken = "cutpause" \/ p1 = {})
       /\ (Broken = "skiphead" \/ IsLineHead(p, l1))
       /\ FitsFree(reqNeeds[p], u1, reqNeeds, cap)
       /\ seq' = seq + 1
       /\ holdSeq' = [hs0 EXCEPT ![p] = seq + 1]
       /\ lineSeq' = [ls0 EXCEPT ![p] = 0]
       /\ pc' = pcOut
       /\ UNCHANGED <<kind, prio, reqNeeds, reqTag, stepsLeft>>
       /\ inFlight' = [rec.inf EXCEPT ![p] = TRUE]
       /\ lockHeld' = lockHeld /\ wasPausedStep' = rec.wp
  /\ UNCHANGED <<starts, cap, crashes, sets>>

FinishStep(p) ==
  /\ inFlight[p] /\ p \in lockHeld
  /\ LET lk1 == lockHeld \ {p}
         st == stepsLeft[p]
         wasPaused == (pc[p] = "paused")
         keepBetween == (st = 1 /\ wasPaused /\ Broken # "nobetween")
         pcMid == [pc EXCEPT ![p] =
                     IF keepBetween THEN "between"
                     ELSE IF st = 1 THEN "idle"
                     ELSE "done"]
         hsMid == ReapHoldSeq(
                    [holdSeq EXCEPT ![p] = IF keepBetween THEN holdSeq[p] ELSE 0],
                    lk1, pcMid)
         lsMid == ReapLineSeq(lineSeq, lk1)
         infMid == [inFlight EXCEPT ![p] = FALSE]
         wpMid == IF st = 1 /\ wasPaused THEN wasPausedStep \cup {p} ELSE wasPausedStep \ {p}
         rec == ApplyReconcile(hsMid, pcMid, infMid, wpMid, reqNeeds, prio, cap)
         pcOut == rec.pc
     IN
     /\ pc' = pcOut
     /\ kind' = CleanKind(pcOut, kind) /\ prio' = CleanPrio(pcOut, prio)
     /\ reqNeeds' = CleanNeeds(pcOut, reqNeeds) /\ reqTag' = CleanTag(pcOut, reqTag)
     /\ stepsLeft' = CleanSteps(pcOut, [stepsLeft EXCEPT ![p] = 0])
     /\ lockHeld' = lk1 /\ holdSeq' = hsMid /\ lineSeq' = lsMid
     /\ inFlight' = rec.inf /\ wasPausedStep' = rec.wp
  /\ UNCHANGED <<starts, seq, cap, crashes, sets>>

ContinueStep(p) ==
  /\ pc[p] = "between"
  /\ LET hs0 == ReapHoldSeq(holdSeq, lockHeld, pc)
         ls0 == ReapLineSeq(lineSeq, lockHeld)
     IN
     IF ~FitsMachine(reqNeeds[p], reqTag[p], cap) THEN
       LET pcOut == [pc EXCEPT ![p] = "done"] IN
       /\ holdSeq' = [hs0 EXCEPT ![p] = 0] /\ lineSeq' = ls0
       /\ pc' = pcOut
       /\ kind' = CleanKind(pcOut, kind) /\ prio' = CleanPrio(pcOut, prio)
       /\ reqNeeds' = CleanNeeds(pcOut, reqNeeds) /\ reqTag' = CleanTag(pcOut, reqTag)
       /\ stepsLeft' = CleanSteps(pcOut, stepsLeft)
       /\ inFlight' = inFlight /\ lockHeld' = lockHeld
       /\ wasPausedStep' = wasPausedStep \ {p}
     ELSE
       LET lk1 == lockHeld \cup {p}
           pcMid == [pc EXCEPT ![p] = "paused"]
           rec == ApplyReconcile(hs0, pcMid, inFlight, wasPausedStep, reqNeeds, prio, cap)
       IN
       /\ holdSeq' = hs0 /\ lineSeq' = ls0 /\ lockHeld' = lk1
       /\ pc' = rec.pc /\ inFlight' = rec.inf /\ wasPausedStep' = rec.wp
       /\ UNCHANGED <<kind, prio, reqNeeds, reqTag, stepsLeft>>
  /\ UNCHANGED <<starts, seq, cap, crashes, sets>>

Timeout(p) ==
  /\ p \in lockHeld
  /\ (pc[p] = "wait" \/ (pc[p] = "paused" /\ ~inFlight[p]))
  /\ LET lk1 == lockHeld \ {p}
         pcMid == [pc EXCEPT ![p] = "done"]
         hsMid == ReapHoldSeq([holdSeq EXCEPT ![p] = 0], lk1, pcMid)
         lsMid == ReapLineSeq([lineSeq EXCEPT ![p] = 0], lk1)
         rec == ApplyReconcile(hsMid, pcMid, inFlight, wasPausedStep \ {p}, reqNeeds, prio, cap)
         pcOut == rec.pc
     IN
     /\ pc' = pcOut
     /\ kind' = CleanKind(pcOut, kind) /\ prio' = CleanPrio(pcOut, prio)
     /\ reqNeeds' = CleanNeeds(pcOut, reqNeeds) /\ reqTag' = CleanTag(pcOut, reqTag)
     /\ stepsLeft' = CleanSteps(pcOut, stepsLeft)
     /\ lockHeld' = lk1 /\ holdSeq' = hsMid /\ lineSeq' = lsMid
     /\ inFlight' = rec.inf /\ wasPausedStep' = rec.wp
  /\ UNCHANGED <<starts, seq, cap, crashes, sets>>

Reap ==
  /\ \E q \in Procs : (holdSeq[q] > 0 \/ lineSeq[q] > 0) /\ q \notin lockHeld /\ pc[q] # "between"
  /\ LET hs0 == ReapHoldSeq(holdSeq, lockHeld, pc)
         ls0 == ReapLineSeq(lineSeq, lockHeld)
         rec == ApplyReconcile(hs0, pc, inFlight, wasPausedStep, reqNeeds, prio, cap)
     IN
     /\ holdSeq' = hs0 /\ lineSeq' = ls0
     /\ pc' = rec.pc /\ inFlight' = rec.inf /\ wasPausedStep' = rec.wp
  /\ UNCHANGED <<kind, prio, reqNeeds, reqTag, stepsLeft, starts, seq, lockHeld, cap, crashes, sets>>

SetCapWith(newCap) ==
  /\ sets < MaxSet /\ newCap # cap
  /\ LET hs0 == ReapHoldSeq(holdSeq, lockHeld, pc)
         ls0 == ReapLineSeq(lineSeq, lockHeld)
         rec == ApplyReconcile(hs0, pc, inFlight, wasPausedStep, reqNeeds, prio, newCap)
     IN
     /\ cap' = newCap /\ sets' = sets + 1
     /\ holdSeq' = hs0 /\ lineSeq' = ls0
     /\ pc' = rec.pc /\ inFlight' = rec.inf /\ wasPausedStep' = rec.wp
  /\ UNCHANGED <<kind, prio, reqNeeds, reqTag, stepsLeft, starts, seq, lockHeld, crashes>>

SetCap == \E newCap \in [Resources -> 1..MaxCap] : SetCapWith(newCap)

Crash(p) ==
  /\ p \in lockHeld /\ crashes < MaxCrash
  /\ LET pcOut == [pc EXCEPT ![p] = "done"] IN
     /\ crashes' = crashes + 1
     /\ pc' = pcOut
     /\ kind' = CleanKind(pcOut, kind) /\ prio' = CleanPrio(pcOut, prio)
     /\ reqNeeds' = CleanNeeds(pcOut, reqNeeds) /\ reqTag' = CleanTag(pcOut, reqTag)
     /\ stepsLeft' = CleanSteps(pcOut, stepsLeft)
     /\ inFlight' = [inFlight EXCEPT ![p] = FALSE]
     /\ wasPausedStep' = wasPausedStep \ {p}
     /\ lockHeld' = IF Broken = "leak" THEN lockHeld ELSE lockHeld \ {p}
  /\ UNCHANGED <<starts, seq, holdSeq, lineSeq, cap, sets>>

IdleStutter ==
  /\ \A p \in Procs : pc[p] \in {"idle", "done"}
  /\ \A p \in Procs : (holdSeq[p] = 0 /\ lineSeq[p] = 0)
  /\ UNCHANGED vars

Next ==
  \/ \E p \in Procs :
       \/ AcquireWork(p) \/ AcquireCI(p) \/ WakeWaiter(p)
       \/ FinishStep(p) \/ ContinueStep(p) \/ Timeout(p) \/ Crash(p)
  \/ Reap
  \/ SetCap
  \/ IdleStutter

Fairness ==
  /\ WF_vars(Reap)
  /\ \A p \in Procs :
       /\ WF_vars(FinishStep(p))
       /\ WF_vars(ContinueStep(p))
       /\ WF_vars(WakeWaiter(p))

SafetySpec == Init /\ [][Next]_vars

Spec == SafetySpec /\ Fairness

\* --- Safety & Liveness Properties (SPEC-V3 §12.1–12.8) ---------------------

NeverOver ==
  MaxSet = 0 => \A r \in Resources : SumSet(LiveUnpaused, reqNeeds, r) <= cap[r]

CINeverWaits ==
  \A p \in Procs : kind[p] = "ci" => pc[p] \notin {"wait", "paused", "between"}

PausedNeedsIntact ==
  [][\A p \in Procs :
       (pc[p] \in {"run", "paused", "between"} /\ pc'[p] \in {"run", "paused", "between"})
       => reqNeeds'[p] = reqNeeds[p]]_vars

VictimOrder ==
  [][\A v \in Procs :
       (pc[v] = "run" /\ pc'[v] = "paused") =>
         \A u \in Procs :
           (u # v /\ pc[u] = "run" /\ pc'[u] = "run" /\ kind[u] # "ci"
            /\ (\A r \in Resources : reqNeeds[u][r] >= reqNeeds[v][r]))
           => (prio[v] < prio[u] \/ (prio[v] = prio[u] /\ holdSeq[v] > holdSeq[u]))]_vars

UnpauseFirst ==
  [][/\ \A q \in Procs :
          (pc[q] \in {"idle", "wait"} /\ pc'[q] = "run" /\ kind'[q] # "ci")
          => (LivePaused' = {} /\ wasPausedStep' = {})
     /\ \A q \in Procs :
          (pc[q] = "paused" /\ pc'[q] = "run") =>
            \A hi \in Procs :
              (hi # q /\ pc[hi] = "paused" /\ pc'[hi] = "paused"
               /\ (\A r \in Resources : reqNeeds[hi][r] <= reqNeeds[q][r]))
              => (prio[q] > prio[hi] \/ (prio[q] = prio[hi] /\ holdSeq[q] < holdSeq[hi]))]_vars

ArrivalOrder ==
  [][/\ \A q \in Procs :
          (pc[q] = "wait" /\ pc'[q] = "run") => IsLineHead(q, LiveLine)
     /\ \A q \in Procs :
          (pc[q] = "idle" /\ pc'[q] = "run" /\ kind'[q] # "ci") => LiveLine = {}]_vars

NeverFitRefused ==
  MaxSet = 0 => \A p \in Procs :
    pc[p] \in {"wait", "paused", "run", "between"} => FitsMachine(reqNeeds[p], reqTag[p], cap)

CommandHasLock ==
  \A p \in Procs : inFlight[p] => (p \in lockHeld /\ holdSeq[p] > 0 /\ pc[p] \in {"run", "paused"})

AllSettle ==
  \A p \in Procs : (pc[p] \in {"wait", "paused", "between", "run"}) ~> (pc[p] \in {"idle", "done"})
=============================================================================
