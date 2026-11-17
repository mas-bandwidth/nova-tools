----------------------------- MODULE Arbitrator -----------------------------
(* Central client/server resource arbitrator (cmd/nova-reserve/engine.go,    *)
(* docs/RESOURCES.md §2-§8). Models multi-host all-or-nothing vector         *)
(* admission, fleet-any vs host-pinned queueing, immediate CI grants with    *)
(* priority-ordered logical pause and unpause ahead of the queue, timed soft *)
(* operator claims, per-source round-robin fairness with head-of-line        *)
(* reservation, 0-debit nested sub-leases with wait-for cycle refusal        *)
(* (RESERVE DEADLOCK), and the 15s server-restart reconnect grace window.    *)
EXTENDS Integers, FiniteSets, Sequences

CONSTANTS
  NumWork,            \* number of work actors (3 in model check, 4 in trace)
  MaxStarts,          \* request cycles per actor (1 in model check, >=1 in trace)
  MaxRestarts,        \* server restarts (1 in model check, 0 in liveness)
  BugOverCap,         \* witness: check CPU only, ignoring memory capacity
  BugCIQueues,        \* witness: queue CI when host is full
  BugVictimOrder,     \* witness: pause highest-priority/oldest victim instead of lowest/youngest
  BugCutPause,        \* witness: admit queued waiter ahead of fitting paused lease
  BugSoftClaim,       \* witness: ignore active soft claim on new admissions
  BugRoundRobin,      \* witness: ignore per-source round-robin cursor
  BugHoLStarve,       \* witness: bypass head-of-line waiter with smaller request
  BugSubleaseDebit,   \* witness: grant sublease exceeding parent's remaining grant
  BugDeadlockQueue,   \* witness: queue a request that closes a wait-for cycle
  BugReconnectAdmit   \* witness: admit new non-CI work during reconnect window

Hosts   == 1..2
Res     == {"cpu", "mem"}
WorkIds == 1..NumWork
CIId    == NumWork + 1
Procs   == 1..(NumWork + 1)
Prios   == 1..2

Cap == [h \in Hosts |-> [cpu |-> 2, mem |-> 2]]
ZeroVec == [r \in Res |-> 0]

VARIABLES
  state,         \* [Procs -> {"idle", "queued", "held", "refused_deadlock"}]
  mode,          \* [Procs -> {"none", "any", "host"}]
  reqHost,       \* [Procs -> 0..2] (0 when mode = "any")
  hostOf,        \* [Procs -> 0..2] (assigned host when state = "held")
  needs,         \* [Procs -> [Res -> 0..2]]
  prio,          \* [Procs -> 1..3] (3 is CI)
  source,        \* [Procs -> 0..2] (1, 2 for peers; 0 for CI)
  parent,        \* [Procs -> 0..4] (0 when top-level, else enclosing WorkId)
  sublease,      \* [Procs -> BOOLEAN]
  paused,        \* [Procs -> BOOLEAN]
  seq,           \* [Procs -> Nat] (arrival/grant order stamp)
  nextSeq,       \* Nat counter
  starts,        \* [Procs -> 0..MaxStarts]
  claim,         \* [Hosts -> [Res -> 0..2]] (active timed soft person claim)
  claimsUsed,    \* 0..MaxStarts
  rrLast,        \* [Prios -> 0..2] (last source granted in each priority tier)
  reconnecting,  \* BOOLEAN: TRUE during 15s server-restart reconnect window
  inFlight,      \* SUBSET WorkIds: pre-restart leases not yet reconnected
  restartSeq,    \* seq watermark when ServerRestart occurred
  restarts       \* 0..MaxRestarts

vars == <<state, mode, reqHost, hostOf, needs, prio, source, parent, sublease,
          paused, seq, nextSeq, starts, claim, claimsUsed, rrLast,
          reconnecting, inFlight, restartSeq, restarts>>

\* --- Resource Accounting Helpers --------------------------------------------

SumSet(S, fn(_)) ==
  LET RECURSIVE add(_)
      add(T) == IF T = {} THEN 0
                ELSE LET x == CHOOSE y \in T : TRUE IN fn(x) + add(T \ {x})
  IN add(S)

TopUnpausedOn(h) ==
  {p \in Procs : state[p] = "held" /\ p \notin inFlight /\ hostOf[p] = h /\ ~sublease[p] /\ ~paused[p]}

Used(h, r) == SumSet(TopUnpausedOn(h), LAMBDA p : needs[p][r])

SubUsed(par, r) ==
  SumSet({c \in WorkIds : state[c] = "held" /\ c \notin inFlight /\ sublease[c] /\ parent[c] = par},
         LAMBDA c : needs[c][r])

FitsSublease(par, reqN, rh) ==
  /\ par \in WorkIds
  /\ state[par] = "held"
  /\ par \notin inFlight
  /\ ~paused[par]
  /\ (rh = 0 \/ rh = hostOf[par])
  /\ IF BugSubleaseDebit
     THEN TRUE
     ELSE \A r \in Res : SubUsed(par, r) + reqN[r] <= needs[par][r]

FitsHostRaw(h, reqN, includeClaim) ==
  IF BugOverCap
  THEN Used(h, "cpu") + (IF includeClaim /\ ~BugSoftClaim THEN claim[h]["cpu"] ELSE 0) + reqN["cpu"] <= Cap[h]["cpu"]
  ELSE \A r \in Res :
         Used(h, r) + (IF includeClaim /\ ~BugSoftClaim THEN claim[h][r] ELSE 0) + reqN[r] <= Cap[h][r]

FitsNewOn(h, reqN) == FitsHostRaw(h, reqN, TRUE)
FitsUnpauseOn(h, reqN) == FitsHostRaw(h, reqN, FALSE)

PausedSet(h) == {p \in WorkIds : state[p] = "held" /\ p \notin inFlight /\ hostOf[p] = h /\ paused[p]}

AnyPausedFits(h) == \E p \in PausedSet(h) : FitsUnpauseOn(h, needs[p])

\* --- AND-OR Wait-For Reduction & Cycle Detection (matches engine.go) --------

CandidateHosts(m, rh) == IF m = "any" THEN Hosts ELSE {rh}

HasChildIn(par, p, StuckW) == \E w \in StuckW : par[w] = p

FitsAfterUnstuck(m, rh, reqN, par, StuckW) ==
  \E ch \in CandidateHosts(m, rh) :
    \A r \in Res :
      SumSet({p \in WorkIds :
                /\ state[p] = "held"
                /\ p \notin inFlight
                /\ hostOf[p] = ch
                /\ ~sublease[p]
                /\ HasChildIn(par, p, StuckW)},
             LAMBDA p : needs[p][r]) + reqN[r] <= Cap[ch][r]

HasDeadlock(st, md, rh, nd, par) ==
  LET s0 == {q \in WorkIds : st[q] = "queued"}
      s1 == {q \in s0 : ~FitsAfterUnstuck(md[q], rh[q], nd[q], par, s0)}
      s2 == {q \in s1 : ~FitsAfterUnstuck(md[q], rh[q], nd[q], par, s1)}
      s3 == {q \in s2 : ~FitsAfterUnstuck(md[q], rh[q], nd[q], par, s2)}
  IN s3 /= {}

WouldCycle(p, m, rh, reqN, par) ==
  /\ par /= 0
  /\ HasDeadlock([state   EXCEPT ![p] = "queued"],
                 [mode    EXCEPT ![p] = m],
                 [reqHost EXCEPT ![p] = rh],
                 [needs   EXCEPT ![p] = reqN],
                 [parent  EXCEPT ![p] = par])

\* --- Queue Ordering (Priority, Source Round-Robin, Head-of-Line) ------------

Queued == {q \in WorkIds : state[q] = "queued"}

BetterWaiter(rr, a, b) ==
  \/ prio[a] > prio[b]
  \/ /\ prio[a] = prio[b]
     /\ ~BugRoundRobin
     /\ source[a] /= source[b]
     /\ rr[prio[a]] /= 0
     /\ source[a] /= rr[prio[a]]
     /\ source[b] = rr[prio[a]]
  \/ /\ prio[a] = prio[b]
     /\ (BugRoundRobin \/ source[a] = source[b] \/ rr[prio[a]] = 0 \/
         (source[a] /= rr[prio[a]] /\ source[b] /= rr[prio[a]]))
     /\ seq[a] < seq[b]

HasActiveAncestorOn(parID, h) ==
  /\ parID \in WorkIds
  /\ state[parID] = "held"
  /\ parID \notin inFlight
  /\ ~sublease[parID]
  /\ ~paused[parID]
  /\ hostOf[parID] = h

BlockedByHoLVec(rr, pr, s, sq, reqN, parID, h) ==
  IF BugHoLStarve \/ HasActiveAncestorOn(parID, h) THEN FALSE
  ELSE \E lead \in Queued :
         /\ seq[lead] /= sq
         /\ ((mode[lead] = "host" /\ reqHost[lead] = h) \/ (mode[lead] = "any" /\ h = 1))
         /\ (prio[lead] > pr
             \/ (prio[lead] = pr /\ ~BugRoundRobin /\ source[lead] /= s /\ rr[pr] /= 0 /\ source[lead] /= rr[pr] /\ s = rr[pr])
             \/ (prio[lead] = pr /\ (BugRoundRobin \/ source[lead] = s \/ rr[pr] = 0 \/ (source[lead] /= rr[pr] /\ s /= rr[pr])) /\ seq[lead] < sq))
         /\ ~FitsNewOn(h, needs[lead])
         /\ \E r \in Res : Used(h, r) + claim[h][r] + reqN[r] + needs[lead][r] > Cap[h][r]

BlockedByHoL(rr, q, h) ==
  BlockedByHoLVec(rr, prio[q], source[q], seq[q], needs[q], parent[q], h)

HostBetter(h1, h2, reqN) ==
  LET f1 == Cap[h1]["cpu"] - Used(h1, "cpu") - claim[h1]["cpu"] - reqN["cpu"]
      f2 == Cap[h2]["cpu"] - Used(h2, "cpu") - claim[h2]["cpu"] - reqN["cpu"]
      n1 == Cardinality(TopUnpausedOn(h1))
      n2 == Cardinality(TopUnpausedOn(h2))
  IN \/ f1 > f2
     \/ (f1 = f2 /\ n1 < n2)
     \/ (f1 = f2 /\ n1 = n2 /\ h1 <= h2)

EligibleGrant(q, h) ==
  /\ q \in Queued
  /\ h \in CandidateHosts(mode[q], reqHost[q])
  /\ (BugReconnectAdmit \/ ~reconnecting)
  /\ (BugCutPause \/ PausedSet(h) = {})
  /\ FitsNewOn(h, needs[q])
  /\ ~BlockedByHoL(rrLast, q, h)
  /\ ~\E other \in Queued :
       /\ other /= q
       /\ BetterWaiter(rrLast, other, q)
       /\ \E ch \in CandidateHosts(mode[other], reqHost[other]) :
            /\ (BugCutPause \/ PausedSet(ch) = {})
            /\ FitsNewOn(ch, needs[other])
            /\ ~BlockedByHoL(rrLast, other, ch)

\* --- CI Victim Selection & Unpause Order ------------------------------------

RECURSIVE DisplaceCI(_, _, _)
DisplaceCI(curPaused, h, ciN) ==
  LET used(r) == SumSet({p \in WorkIds :
                          state[p] = "held" /\ p \notin inFlight /\ hostOf[p] = h /\ ~sublease[p] /\ ~curPaused[p]},
                        LAMBDA p : needs[p][r])
                 + (IF state[CIId] = "held" /\ hostOf[CIId] = h THEN needs[CIId][r] ELSE 0)
      over == {r \in Res : used(r) + ciN[r] > Cap[h][r]}
      cands == {p \in WorkIds :
                  /\ state[p] = "held"
                  /\ p \notin inFlight
                  /\ hostOf[p] = h
                  /\ ~sublease[p]
                  /\ ~curPaused[p]
                  /\ \E r \in over : needs[p][r] > 0}
  IN IF over = {} \/ cands = {}
     THEN curPaused
     ELSE LET pick == CHOOSE v \in cands :
                        IF BugVictimOrder
                        THEN \A o \in cands : prio[v] > prio[o] \/ (prio[v] = prio[o] /\ seq[v] <= seq[o])
                        ELSE \A o \in cands : prio[v] < prio[o] \/ (prio[v] = prio[o] /\ seq[v] >= seq[o])
          IN DisplaceCI([curPaused EXCEPT ![pick] = TRUE], h, ciN)

BestPausedFitting(h) ==
  {p \in PausedSet(h) :
     /\ FitsUnpauseOn(h, needs[p])
     /\ \A o \in PausedSet(h) :
          FitsUnpauseOn(h, needs[o]) => (prio[p] > prio[o] \/ (prio[p] = prio[o] /\ seq[p] <= seq[o]))}

\* --- Actions ----------------------------------------------------------------

ActorSource(p) == IF p \in {1, 3} THEN 1 ELSE 2

Init ==
  /\ state = [p \in Procs |-> "idle"]
  /\ mode = [p \in Procs |-> "none"]
  /\ reqHost = [p \in Procs |-> 0]
  /\ hostOf = [p \in Procs |-> 0]
  /\ needs = [p \in Procs |-> ZeroVec]
  /\ prio = [p \in Procs |-> IF p = CIId THEN 3 ELSE 1]
  /\ source = [p \in Procs |-> IF p = CIId THEN 0 ELSE ActorSource(p)]
  /\ parent = [p \in Procs |-> 0]
  /\ sublease = [p \in Procs |-> FALSE]
  /\ paused = [p \in Procs |-> FALSE]
  /\ seq = [p \in Procs |-> 0]
  /\ nextSeq = 1
  /\ starts = [p \in Procs |-> 0]
  /\ claim = [h \in Hosts |-> ZeroVec]
  /\ claimsUsed = 0
  /\ rrLast = [pr \in Prios |-> 0]
  /\ reconnecting = FALSE
  /\ inFlight = {}
  /\ restartSeq = 0
  /\ restarts = 0

RequestWork(p, m, rh, cNeed, mNeed, pr, par) ==
  /\ p \in WorkIds
  /\ state[p] = "idle"
  /\ starts[p] < MaxStarts
  /\ (m = "any" /\ rh = 0) \/ (m = "host" /\ rh \in Hosts)
  /\ cNeed \in 1..2 /\ mNeed \in 1..2
  /\ pr \in Prios
  /\ par \in (IF p \in {2, 3} THEN {0, 1} ELSE {0})
  /\ par /= p
  /\ (par /= 0 => (state[par] = "held" /\ par \notin inFlight /\ ~sublease[par]))
  /\ LET reqN == [cpu |-> cNeed, mem |-> mNeed]
         s    == ActorSource(p)
     IN
     IF par /= 0 /\ FitsSublease(par, reqN, rh)
     THEN /\ state' = [state EXCEPT ![p] = "held"]
          /\ mode' = [mode EXCEPT ![p] = m]
          /\ reqHost' = [reqHost EXCEPT ![p] = rh]
          /\ hostOf' = [hostOf EXCEPT ![p] = hostOf[par]]
          /\ needs' = [needs EXCEPT ![p] = reqN]
          /\ prio' = [prio EXCEPT ![p] = pr]
          /\ source' = [source EXCEPT ![p] = s]
          /\ parent' = [parent EXCEPT ![p] = par]
          /\ sublease' = [sublease EXCEPT ![p] = TRUE]
          /\ paused' = [paused EXCEPT ![p] = FALSE]
          /\ seq' = [seq EXCEPT ![p] = nextSeq]
          /\ nextSeq' = nextSeq + 1
          /\ starts' = [starts EXCEPT ![p] = @ + 1]
          /\ UNCHANGED <<claim, claimsUsed, rrLast, reconnecting, inFlight, restartSeq, restarts>>
     ELSE IF ~BugDeadlockQueue /\ WouldCycle(p, m, rh, reqN, par)
     THEN /\ state' = [state EXCEPT ![p] = "refused_deadlock"]
          /\ mode' = [mode EXCEPT ![p] = m]
          /\ reqHost' = [reqHost EXCEPT ![p] = rh]
          /\ needs' = [needs EXCEPT ![p] = reqN]
          /\ prio' = [prio EXCEPT ![p] = pr]
          /\ source' = [source EXCEPT ![p] = s]
          /\ parent' = [parent EXCEPT ![p] = par]
          /\ starts' = [starts EXCEPT ![p] = @ + 1]
          /\ UNCHANGED <<hostOf, sublease, paused, seq, nextSeq, claim, claimsUsed, rrLast, reconnecting, inFlight, restartSeq, restarts>>
     ELSE IF /\ (BugReconnectAdmit \/ ~reconnecting)
             /\ \E ch \in CandidateHosts(m, rh) :
                  /\ (BugCutPause \/ PausedSet(ch) = {})
                  /\ FitsNewOn(ch, reqN)
                  /\ ~BlockedByHoLVec(rrLast, pr, s, nextSeq, reqN, par, ch)
     THEN LET okH == {ch \in CandidateHosts(m, rh) :
                        /\ (BugCutPause \/ PausedSet(ch) = {})
                        /\ FitsNewOn(ch, reqN)
                        /\ ~BlockedByHoLVec(rrLast, pr, s, nextSeq, reqN, par, ch)}
              ch  == CHOOSE ch \in okH : \A o \in okH : HostBetter(ch, o, reqN)
          IN /\ state' = [state EXCEPT ![p] = "held"]
             /\ mode' = [mode EXCEPT ![p] = m]
             /\ reqHost' = [reqHost EXCEPT ![p] = rh]
             /\ hostOf' = [hostOf EXCEPT ![p] = ch]
             /\ needs' = [needs EXCEPT ![p] = reqN]
             /\ prio' = [prio EXCEPT ![p] = pr]
             /\ source' = [source EXCEPT ![p] = s]
             /\ parent' = [parent EXCEPT ![p] = par]
             /\ sublease' = [sublease EXCEPT ![p] = FALSE]
             /\ paused' = [paused EXCEPT ![p] = FALSE]
             /\ seq' = [seq EXCEPT ![p] = nextSeq]
             /\ nextSeq' = nextSeq + 1
             /\ starts' = [starts EXCEPT ![p] = @ + 1]
             /\ rrLast' = [rrLast EXCEPT ![pr] = s]
             /\ UNCHANGED <<claim, claimsUsed, reconnecting, inFlight, restartSeq, restarts>>
     ELSE /\ state' = [state EXCEPT ![p] = "queued"]
          /\ mode' = [mode EXCEPT ![p] = m]
          /\ reqHost' = [reqHost EXCEPT ![p] = rh]
          /\ hostOf' = [hostOf EXCEPT ![p] = 0]
          /\ needs' = [needs EXCEPT ![p] = reqN]
          /\ prio' = [prio EXCEPT ![p] = pr]
          /\ source' = [source EXCEPT ![p] = s]
          /\ parent' = [parent EXCEPT ![p] = par]
          /\ sublease' = [sublease EXCEPT ![p] = FALSE]
          /\ paused' = [paused EXCEPT ![p] = FALSE]
          /\ seq' = [seq EXCEPT ![p] = nextSeq]
          /\ nextSeq' = nextSeq + 1
          /\ starts' = [starts EXCEPT ![p] = @ + 1]
          /\ UNCHANGED <<claim, claimsUsed, rrLast, reconnecting, inFlight, restartSeq, restarts>>

RequestCI(h, cNeed, mNeed) ==
  /\ state[CIId] = "idle"
  /\ starts[CIId] < MaxStarts
  /\ h \in Hosts
  /\ cNeed \in 1..2 /\ mNeed \in 1..2
  /\ LET ciN == [cpu |-> cNeed, mem |-> mNeed] IN
     IF BugCIQueues /\ ~\A r \in Res : Used(h, r) + ciN[r] <= Cap[h][r]
     THEN /\ state' = [state EXCEPT ![CIId] = "queued"]
          /\ mode' = [mode EXCEPT ![CIId] = "host"]
          /\ reqHost' = [reqHost EXCEPT ![CIId] = h]
          /\ hostOf' = [hostOf EXCEPT ![CIId] = 0]
          /\ needs' = [needs EXCEPT ![CIId] = ciN]
          /\ seq' = [seq EXCEPT ![CIId] = nextSeq]
          /\ nextSeq' = nextSeq + 1
          /\ starts' = [starts EXCEPT ![CIId] = @ + 1]
          /\ UNCHANGED <<prio, source, parent, sublease, paused, claim, claimsUsed, rrLast, reconnecting, inFlight, restartSeq, restarts>>
     ELSE /\ state' = [state EXCEPT ![CIId] = "held"]
          /\ mode' = [mode EXCEPT ![CIId] = "host"]
          /\ reqHost' = [reqHost EXCEPT ![CIId] = h]
          /\ hostOf' = [hostOf EXCEPT ![CIId] = h]
          /\ needs' = [needs EXCEPT ![CIId] = ciN]
          /\ paused' = DisplaceCI(paused, h, ciN)
          /\ seq' = [seq EXCEPT ![CIId] = nextSeq]
          /\ nextSeq' = nextSeq + 1
          /\ starts' = [starts EXCEPT ![CIId] = @ + 1]
          /\ UNCHANGED <<prio, source, parent, sublease, claim, claimsUsed, rrLast, reconnecting, inFlight, restartSeq, restarts>>

UnpauseStep(p) ==
  /\ p \in WorkIds
  /\ \E h \in Hosts : p \in BestPausedFitting(h)
  /\ paused' = [paused EXCEPT ![p] = FALSE]
  /\ UNCHANGED <<state, mode, reqHost, hostOf, needs, prio, source, parent, sublease,
                 seq, nextSeq, starts, claim, claimsUsed, rrLast, reconnecting, inFlight, restartSeq, restarts>>

AdmitQueued(q, h) ==
  /\ (BugCutPause \/ \A ch \in Hosts : BestPausedFitting(ch) = {})
  /\ EligibleGrant(q, h)
  /\ state' = [state EXCEPT ![q] = "held"]
  /\ hostOf' = [hostOf EXCEPT ![q] = h]
  /\ sublease' = [sublease EXCEPT ![q] = FALSE]
  /\ paused' = [paused EXCEPT ![q] = FALSE]
  /\ rrLast' = [rrLast EXCEPT ![prio[q]] = source[q]]
  /\ UNCHANGED <<mode, reqHost, needs, prio, source, parent, seq, nextSeq, starts, claim, claimsUsed, reconnecting, inFlight, restartSeq, restarts>>

Release(p) ==
  /\ state[p] = "held"
  /\ p \notin inFlight
  /\ ~\E c \in WorkIds : state[c] \in {"held", "queued"} /\ parent[c] = p
  /\ state' = [state EXCEPT ![p] = "idle"]
  /\ mode' = [mode EXCEPT ![p] = "none"]
  /\ reqHost' = [reqHost EXCEPT ![p] = 0]
  /\ hostOf' = [hostOf EXCEPT ![p] = 0]
  /\ needs' = [needs EXCEPT ![p] = ZeroVec]
  /\ parent' = [parent EXCEPT ![p] = 0]
  /\ sublease' = [sublease EXCEPT ![p] = FALSE]
  /\ paused' = [paused EXCEPT ![p] = FALSE]
  /\ seq' = [seq EXCEPT ![p] = 0]
  /\ UNCHANGED <<prio, source, nextSeq, starts, claim, claimsUsed, rrLast, reconnecting, inFlight, restartSeq, restarts>>

ClearRefused(p) ==
  /\ state[p] = "refused_deadlock"
  /\ state' = [state EXCEPT ![p] = "idle"]
  /\ mode' = [mode EXCEPT ![p] = "none"]
  /\ reqHost' = [reqHost EXCEPT ![p] = 0]
  /\ needs' = [needs EXCEPT ![p] = ZeroVec]
  /\ parent' = [parent EXCEPT ![p] = 0]
  /\ seq' = [seq EXCEPT ![p] = 0]
  /\ UNCHANGED <<hostOf, prio, source, sublease, paused, nextSeq, starts, claim, claimsUsed, rrLast, reconnecting, inFlight, restartSeq, restarts>>

PlaceClaim(h, amt) ==
  /\ h = 1
  /\ claimsUsed < MaxStarts
  /\ claim[h] = ZeroVec
  /\ amt \in {1, 2}
  /\ claim' = [claim EXCEPT ![h] = [cpu |-> amt, mem |-> amt]]
  /\ claimsUsed' = claimsUsed + 1
  /\ UNCHANGED <<state, mode, reqHost, hostOf, needs, prio, source, parent, sublease,
                 paused, seq, nextSeq, starts, rrLast, reconnecting, inFlight, restartSeq, restarts>>

ExpireClaim(h) ==
  /\ claim[h] /= ZeroVec
  /\ claim' = [claim EXCEPT ![h] = ZeroVec]
  /\ UNCHANGED <<state, mode, reqHost, hostOf, needs, prio, source, parent, sublease,
                 paused, seq, nextSeq, starts, claimsUsed, rrLast, reconnecting, inFlight, restartSeq, restarts>>

ServerRestart ==
  /\ ~reconnecting
  /\ restarts < MaxRestarts
  /\ Queued = {}
  /\ state[CIId] = "idle"
  /\ \A h \in Hosts : claim[h] = ZeroVec
  /\ LET live == {p \in WorkIds : state[p] = "held"} IN
     /\ live /= {}
     /\ reconnecting' = TRUE
     /\ inFlight' = live
     /\ restartSeq' = nextSeq
     /\ restarts' = restarts + 1
     /\ rrLast' = [pr \in Prios |-> 0]
     /\ UNCHANGED <<state, mode, reqHost, hostOf, needs, prio, source, parent, sublease,
                    paused, seq, nextSeq, starts, claim, claimsUsed>>

Reconnect(p) ==
  /\ reconnecting
  /\ p \in inFlight
  /\ LET h == hostOf[p]
         fits == sublease[p] \/ paused[p] \/
                 (\A r \in Res : Used(h, r) + needs[p][r] <= Cap[h][r])
     IN /\ paused' = [paused EXCEPT ![p] = IF fits THEN @ ELSE TRUE]
        /\ inFlight' = inFlight \ {p}
        /\ UNCHANGED <<state, mode, reqHost, hostOf, needs, prio, source, parent, sublease,
                       seq, nextSeq, starts, claim, claimsUsed, rrLast, reconnecting, restartSeq, restarts>>

EndReconnect ==
  /\ reconnecting
  /\ inFlight = {}
  /\ reconnecting' = FALSE
  /\ UNCHANGED <<state, mode, reqHost, hostOf, needs, prio, source, parent, sublease,
                 paused, seq, nextSeq, starts, claim, claimsUsed, rrLast, inFlight, restartSeq, restarts>>

Done ==
  /\ ~reconnecting
  /\ inFlight = {}
  /\ \A h \in Hosts : claim[h] = ZeroVec
  /\ \A p \in Procs : state[p] = "idle"
  /\ UNCHANGED vars

Next ==
  \/ \E p \in WorkIds, mr \in {<<"any", 0>>, <<"host", 1>>},
        cm \in {<<1, 1>>, <<1, 2>>, <<2, 2>>}, pr \in Prios, par \in {0, 1} :
       /\ (par = 0 \/ p = 2)
       /\ (pr = 1 \/ p = 1)
       /\ (mr = <<"host", 1>> \/ p \in {1, 2})
       /\ (cm /= <<1, 2>> \/ ((MaxRestarts > 0 \/ BugOverCap) /\ p \in {1, 2} /\ mr = <<"host", 1>> /\ par = 0 /\ pr = 1))
       /\ RequestWork(p, mr[1], mr[2], cm[1], cm[2], pr, par)
  \/ \E c \in {1, 2} : (c = 2 \/ MaxRestarts > 0 \/ BugVictimOrder \/ BugCutPause) /\ RequestCI(1, c, c)
  \/ \E p \in WorkIds : UnpauseStep(p)
  \/ \E q \in WorkIds, h \in Hosts : AdmitQueued(q, h)
  \/ \E p \in Procs : Release(p)
  \/ \E p \in WorkIds : ClearRefused(p)
  \/ (restarts = 0 /\ (\A p \in Procs : state[p] = "idle") /\ PlaceClaim(1, 2))
  \/ \E h \in Hosts : ExpireClaim(h)
  \/ ServerRestart
  \/ \E p \in WorkIds : Reconnect(p)
  \/ EndReconnect
  \/ Done

Fairness ==
  /\ \A p \in WorkIds : WF_vars(UnpauseStep(p))
  /\ \A q \in WorkIds, h \in Hosts : WF_vars(AdmitQueued(q, h))
  /\ \A p \in Procs : WF_vars(Release(p))
  /\ \A p \in WorkIds : WF_vars(ClearRefused(p))
  /\ \A h \in Hosts : WF_vars(ExpireClaim(h))
  /\ \A p \in WorkIds : WF_vars(Reconnect(p))
  /\ WF_vars(EndReconnect)

Spec == Init /\ [][Next]_vars
LiveSpec == Spec /\ Fairness

\* --- Safety Invariants, Action Properties & Temporal Liveness ---------------

NeverOver ==
  \A h \in Hosts, r \in Res : Used(h, r) <= Cap[h][r]

SubleaseWithinParent ==
  \A p \in WorkIds :
    (state[p] = "held" /\ ~sublease[p] /\ p \notin inFlight) =>
      \A r \in Res : SubUsed(p, r) <= needs[p][r]

CINeverWaits == state[CIId] /= "queued"

NoWaitCycle ==
  ~HasDeadlock(state, mode, reqHost, needs, parent)

ReconnectPreservesLeases ==
  (reconnecting /\ inFlight /= {}) =>
    \A p \in WorkIds :
      (state[p] = "held" /\ ~sublease[p] /\ p \notin inFlight) => seq[p] < restartSeq

VictimOrder ==
  [][(state[CIId] = "idle" /\ state'[CIId] = "held") =>
       LET h == hostOf'[CIId]
           newlyPaused == {p \in WorkIds : ~paused[p] /\ paused'[p]}
           keptActive  == {p \in WorkIds : state[p] = "held" /\ p \notin inFlight /\ hostOf[p] = h /\ ~sublease[p] /\ ~paused'[p]}
       IN \A v \in newlyPaused, k \in keptActive :
            (\E r \in Res : needs[k][r] > 0 /\ needs'[CIId][r] > 0) =>
              (prio[v] < prio[k] \/ (prio[v] = prio[k] /\ seq[v] > seq[k]))]_vars

UnpauseBeforeQueue ==
  [][\A q \in WorkIds, h \in Hosts :
       (state[q] /= "held" /\ state'[q] = "held" /\ ~sublease'[q] /\ hostOf'[q] = h) =>
         ~AnyPausedFits(h)]_vars

SoftClaimBlocksNew ==
  [][\A q \in WorkIds, h \in Hosts :
       (state[q] /= "held" /\ state'[q] = "held" /\ ~sublease'[q] /\ hostOf'[q] = h) =>
         \A r \in Res : Used(h, r) + claim[h][r] + needs'[q][r] <= Cap[h][r]]_vars

SourceRoundRobin ==
  [][\A q \in Queued, h \in Hosts :
       (state'[q] = "held" /\ hostOf'[q] = h /\ rrLast[prio[q]] = source[q]) =>
         ~\E o \in Queued :
            /\ prio[o] = prio[q]
            /\ source[o] /= source[q]
            /\ \E ch \in CandidateHosts(mode[o], reqHost[o]) :
                 /\ PausedSet(ch) = {}
                 /\ FitsNewOn(ch, needs[o])
                 /\ ~BlockedByHoL(rrLast, o, ch)]_vars

HoLReserved ==
  [][\A q \in WorkIds, h \in Hosts :
       (state[q] /= "held" /\ state'[q] = "held" /\ ~sublease'[q] /\ hostOf'[q] = h /\ ~HasActiveAncestorOn(parent'[q], h)) =>
         ~\E lead \in Queued :
            /\ lead /= q
            /\ ((mode[lead] = "host" /\ reqHost[lead] = h) \/ (mode[lead] = "any" /\ h = 1))
            /\ (prio[lead] > prio'[q]
                \/ (prio[lead] = prio'[q] /\ source[lead] /= source'[q] /\ rrLast[prio'[q]] /= 0 /\ source[lead] /= rrLast[prio'[q]] /\ source'[q] = rrLast[prio'[q]])
                \/ (prio[lead] = prio'[q] /\ (source[lead] = source'[q] \/ rrLast[prio'[q]] = 0 \/ (source[lead] /= rrLast[prio'[q]] /\ source'[q] /= rrLast[prio'[q]])) /\ seq[lead] < seq'[q]))
            /\ ~FitsNewOn(h, needs[lead])
            /\ \E r \in Res : Used(h, r) + claim[h][r] + needs'[q][r] + needs[lead][r] > Cap[h][r]]_vars

NoStarvation == \A p \in WorkIds : [](state[p] = "queued" => <>(state[p] /= "queued"))
=============================================================================
