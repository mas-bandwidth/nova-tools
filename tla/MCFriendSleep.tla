----------------------------- MODULE MCFriendSleep -----------------------------
EXTENDS Naturals, FiniteSets, Sequences
CONSTANT Broken
\* ---------------------------------------------------- sleep/delivery projection
\* Friend.tla's Spec is the awake challenge projection, including A2 RepeatPing.
\* SleepSpec below is a separate finite delivery/sleep projection. Neither
\* alone proves their cross-layer integration; the Go daemon tests do that.
\* Three ordered stream IDs stand for a dedicated daemon consumer's pending
\* ownership. The per-friend singleton and configured coordinator are assumptions.
\* coordIDs describes matching From metadata, NOT authenticated identities.
\* LocalWake is the trusted local operator. No payload bytes, paging, Redis
\* stale-claim rule, interactive consumer or native session delivery is modeled.
\* In particular >1000-entry recovery is a Go helper obligation, not this proof.

SleepIDs == 1..3
SleepMaxStarts == 6
SleepMaxEvents == 2
SleepMaxFailures == 2
NoJob == 0
FirstID(ids) == CHOOSE i \in ids : \A j \in ids : i <= j

VARIABLES asleep, pending, available, received, running, deferred, priority, coordIDs,
          starts, failures, failedEnds, done, restartCount, sleepConn,
          eventCount, syntheticPushes, asleepStarts, asleepPushes,
          wakeAuthorityOK, selectionOK
sleepVars == <<asleep, pending, available, received, running, deferred, priority, coordIDs,
               starts, failures, failedEnds, done, restartCount, sleepConn,
               eventCount, syntheticPushes, asleepStarts, asleepPushes,
               wakeAuthorityOK, selectionOK>>

SleepInit ==
  /\ asleep = FALSE
  /\ pending = {} /\ available = {} /\ received = {} /\ done = {}
  /\ running = NoJob /\ deferred = NoJob /\ priority = NoJob
  /\ coordIDs \in {{2}, {2, 3}}
  /\ starts = <<>> /\ failedEnds = <<>>
  /\ failures = [i \in SleepIDs |-> 0]
  /\ restartCount = 0 /\ sleepConn = "connected"
  /\ eventCount = 0 /\ syntheticPushes = 0
  /\ asleepStarts = 0 /\ asleepPushes = 0
  /\ wakeAuthorityOK = TRUE /\ selectionOK = TRUE

Sleep ==
  /\ ~asleep
  /\ asleep' = TRUE
  /\ UNCHANGED <<pending, available, received, running, deferred, priority, coordIDs,
                 starts, failures, failedEnds, done, restartCount, sleepConn,
                 eventCount, syntheticPushes, asleepStarts, asleepPushes,
                 wakeAuthorityOK, selectionOK>>

LocalWake ==
  /\ asleep
  /\ asleep' = FALSE
  /\ UNCHANGED <<pending, available, received, running, deferred, priority, coordIDs,
                 starts, failures, failedEnds, done, restartCount, sleepConn,
                 eventCount, syntheticPushes, asleepStarts, asleepPushes,
                 wakeAuthorityOK, selectionOK>>

\* Receive a tiny ordered batch. The first coordinator entry is the wake
\* job; the rest join pending in ID order. A WAKE subject on another sender
\* has no authority (subjects are deliberately absent from this projection).
ReceiveBatch(k) ==
  LET next == Cardinality(received) + 1
      incoming == next..(next + k - 1)
      coordinators == incoming \cap coordIDs
      authorized == coordinators # {}
      wakes == asleep /\ (authorized \/ Broken = "otherwakes")
  IN /\ k \in 1..2 /\ next + k - 1 <= 3
     /\ pending' = pending \cup incoming
     /\ available' = available \cup incoming
     /\ received' = received \cup incoming
     /\ asleep' = IF wakes THEN FALSE ELSE asleep
     /\ priority' = IF wakes /\ authorized THEN FirstID(coordinators) ELSE priority
     /\ wakeAuthorityOK' = (wakeAuthorityOK /\ (~wakes \/ authorized))
     /\ failures' = IF asleep /\ ~authorized /\ Broken = "holdcounts"
                     THEN [failures EXCEPT ![next] = @ + 1] ELSE failures
     /\ UNCHANGED <<running, deferred, coordIDs, starts, failedEnds, done,
                    restartCount, sleepConn, eventCount, syntheticPushes,
                    asleepStarts, asleepPushes, selectionOK>>

\* A receive or recovery wake waits for an already-started turn. A deferred
\* turn is not running; it competes in pending by its original ID after wake.
ExpectedJob == IF priority # NoJob THEN priority ELSE FirstID(available)
StartTurn(i) ==
  /\ running = NoJob /\ available # {} /\ Len(starts) < SleepMaxStarts
  /\ (~asleep \/ Broken = "sleepstarts")
  /\ i \in available
  /\ (i = ExpectedJob \/ Broken = "heldfirst")
  /\ running' = i
  /\ deferred' = IF deferred = i THEN NoJob ELSE deferred
  /\ starts' = Append(starts, i)
  /\ asleepStarts' = asleepStarts + (IF asleep THEN 1 ELSE 0)
  /\ selectionOK' = (selectionOK /\ (i = ExpectedJob))
  /\ UNCHANGED <<asleep, pending, available, received, priority, coordIDs, failures,
                 failedEnds, done, restartCount, sleepConn, eventCount,
                 syntheticPushes, asleepPushes, wakeAuthorityOK>>

DeferTurn ==
  /\ running # NoJob
  /\ deferred' = running /\ running' = NoJob
  /\ UNCHANGED <<asleep, pending, available, received, priority, coordIDs, starts,
                 failures, failedEnds, done, restartCount, sleepConn,
                 eventCount, syntheticPushes, asleepStarts, asleepPushes,
                 wakeAuthorityOK, selectionOK>>

\* Completion may happen after Sleep. Only an actual failed turn consumes
\* the failure budget; a hold or deferral never does. Successful/given-up
\* jobs leave pending. A failure below the give-up limit leaves Redis pending
\* but is not immediately locally requeued; restart can make it available.
\* Fifteen-minute stale claims are outside this layer. The wake priority lasts
\* until its turn completes (including failure), or through a deferral.
CompleteTurn(ok) ==
  LET retire == ok \/ failures[running] + 1 >= SleepMaxFailures
  IN /\ running # NoJob /\ ok \in BOOLEAN
     /\ failures' = IF ok THEN failures ELSE [failures EXCEPT ![running] = @ + 1]
     /\ failedEnds' = IF ok THEN failedEnds ELSE Append(failedEnds, running)
     /\ pending' = IF retire THEN pending \ {running} ELSE pending
     /\ available' = available \ {running}
     /\ done' = IF retire THEN done \cup {running} ELSE done
     /\ priority' = IF priority = running THEN NoJob ELSE priority
     /\ running' = NoJob
     /\ UNCHANGED <<asleep, received, deferred, coordIDs, starts,
                    restartCount, sleepConn, eventCount, syntheticPushes,
                    asleepStarts, asleepPushes, wakeAuthorityOK, selectionOK>>

\* Restart reads the CURRENT durable bit; no historical sleep intent exists.
\* Claimed pending entries and the durable wake barrier survive; a stopped
\* turn can be retried, so no exactly-once native delivery is promised.
\* The barrier is one entry ID in SessionState, not another durable queue.
Restart ==
  /\ restartCount < 1
  /\ restartCount' = restartCount + 1
  /\ running' = NoJob /\ deferred' = NoJob
  /\ priority' = IF Broken = "lostbarrier" THEN NoJob ELSE priority
  /\ available' = pending
  /\ UNCHANGED <<asleep, pending, received, coordIDs, starts, failures,
                 failedEnds, done, sleepConn, eventCount, syntheticPushes,
                 asleepStarts, asleepPushes, wakeAuthorityOK, selectionOK>>

RecoverCoordinator ==
  LET candidates == IF Broken = "replaybarrier" THEN available \cap coordIDs
                    ELSE (available \cap coordIDs) \ {priority}
      selected == FirstID(candidates)
  IN /\ asleep /\ candidates # {}
     /\ asleep' = FALSE /\ priority' = selected
     /\ wakeAuthorityOK' = (wakeAuthorityOK /\ (selected # priority))
     /\ UNCHANGED <<pending, available, received, running, deferred, coordIDs, starts,
                    failures, failedEnds, done, restartCount, sleepConn,
                    eventCount, syntheticPushes, asleepStarts, asleepPushes,
                    selectionOK>>

\* Connection state is independent. A silent/back machine event changes no
\* sleep state and pushes nothing while asleep; it is not a wake message.
ConnectionEvent ==
  /\ eventCount < SleepMaxEvents
  /\ eventCount' = eventCount + 1
  /\ sleepConn' = IF sleepConn = "connected" THEN "silent" ELSE "connected"
  /\ syntheticPushes' = syntheticPushes + (IF ~asleep \/ Broken = "sleeppush" THEN 1 ELSE 0)
  /\ asleepPushes' = asleepPushes + (IF asleep /\ Broken = "sleeppush" THEN 1 ELSE 0)
  /\ UNCHANGED <<asleep, pending, available, received, running, deferred, priority,
                 coordIDs, starts, failures, failedEnds, done, restartCount,
                 asleepStarts, wakeAuthorityOK, selectionOK>>

SleepNext ==
  \/ Sleep \/ LocalWake \/ Restart \/ RecoverCoordinator \/ ConnectionEvent
  \/ \E k \in 1..2 : ReceiveBatch(k)
  \/ \E i \in SleepIDs : StartTurn(i)
  \/ DeferTurn
  \/ \E ok \in BOOLEAN : CompleteTurn(ok)
SleepSpec == SleepInit /\ [][SleepNext]_sleepVars

SleepTypeOK ==
  /\ asleep \in BOOLEAN /\ pending \subseteq SleepIDs
  /\ available \subseteq pending
  /\ received \subseteq SleepIDs /\ done \subseteq SleepIDs
  /\ running \in SleepIDs \cup {NoJob}
  /\ deferred \in SleepIDs \cup {NoJob} /\ priority \in SleepIDs \cup {NoJob}
  /\ coordIDs \in {{2}, {2, 3}}
  /\ starts \in Seq(SleepIDs) /\ Len(starts) <= SleepMaxStarts
  /\ failures \in [SleepIDs -> 0..SleepMaxFailures]
  /\ failedEnds \in Seq(SleepIDs) /\ Len(failedEnds) <= SleepMaxStarts
  /\ restartCount \in 0..1 /\ sleepConn \in {"connected", "silent"}
  /\ eventCount \in 0..SleepMaxEvents /\ syntheticPushes \in 0..SleepMaxEvents
  /\ asleepStarts \in 0..SleepMaxStarts /\ asleepPushes \in 0..SleepMaxEvents
  /\ wakeAuthorityOK \in BOOLEAN /\ selectionOK \in BOOLEAN
PendingConserved == pending \cup done = received /\ pending \cap done = {}
ActiveIsPending == running = NoJob \/ running \in pending
PriorityIsPending == priority = NoJob \/ priority \in available \cap coordIDs
DeferredIsAvailable == deferred = NoJob \/ deferred \in available
\* Local override is trusted; auto-wake needs coordinator routing metadata.
\* A remembered active barrier is not a new wake trigger after local Sleep.
\* After a completed non-Deferred failed turn clears the barrier, its still
\* pending coordinator entry can be redelivered and wake again: at-least-once,
\* not universal freshness. No watermark/authentication guarantee is claimed.
OnlyTrustedWake == wakeAuthorityOK
WakeThenFIFO == selectionOK
NoStartWhileAsleep == asleepStarts = 0
NoSyntheticWhileAsleep == asleepPushes = 0
OnlyFailedTurnsCharge ==
  \A i \in SleepIDs : failures[i] = Cardinality({n \in 1..Len(failedEnds) : failedEnds[n] = i})
RestartKeepsDurableSleep == [][(restartCount' > restartCount) => asleep' = asleep]_sleepVars
RestartKeepsWakeBarrier == [][(restartCount' > restartCount) => priority' = priority]_sleepVars
\* No fairness/liveness/exhaustion/native-delivery claim: this finite layer
\* proves safety only after an actual approved TLC run, not by writing it.

=============================================================================
