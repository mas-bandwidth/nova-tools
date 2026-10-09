----------------------- MODULE NativeLaunchHeldBridge -----------------------
(* A bounded refinement of the native card start's local durable gate. The
   server StartAck is the work-start linearization, but the provider remains
   pipe-blocked until this daemon durably clears the exact active Started
   record's LaunchHeld flag under its latest local PAUSE/STOP state. The
   primary RUN/anchor and auxiliary inspection have separate quiet proofs.
   This finite machine is not a Redis, filesystem or kernel proof. *)
EXTENDS Naturals

CONSTANTS ReleaseBeforeClear, SkipActiveRun, SkipPauseAtClear,
          UseHistoricalAnswer, SkipPriorQuiet, SkipHeldDurability,
          SkipCallbackLiveness, SkipContextAtRelease
VARIABLE s
vars == <<s>>

AuxSettled == s.aux \in {"none", "quiet"}
OwnedCurrent == s.tupleCurrent /\ s.callbackRun = s.activeRun

Init == s = [server |-> "ready", serverStarted |-> FALSE,
             tupleCurrent |-> TRUE, attempt |-> 0,
             activeRun |-> 0, callbackRun |-> 0,
             callbackAlive |-> FALSE, clearCallbackAlive |-> FALSE,
             held |-> FALSE, heldMemory |-> FALSE,
             heldSaveFailed |-> FALSE, hadHoldSaveFailure |-> FALSE,
             preAckError |-> "none", hadNoChildError |-> FALSE,
             hadPhysicalError |-> FALSE,
             launchWasDurable |-> FALSE, primary |-> "none",
             primaryReceipt |-> FALSE, anchor |-> FALSE,
             aux |-> "none", quietObserved |-> FALSE,
             priorQuiet |-> TRUE,
             reply |-> "none", committed |-> FALSE,
             lost |-> FALSE, freshUsed |-> FALSE,
             wasPausedWhileHeld |-> FALSE, retryFromPause |-> FALSE,
             history |-> FALSE, historyUsedForClear |-> FALSE,
             paused |-> FALSE, stopped |-> FALSE, crashed |-> FALSE,
             hadCrash |-> FALSE, clearSaveFailed |-> FALSE,
             clearDone |-> FALSE, clearExact |-> FALSE,
             clearModeOK |-> FALSE, clearAckKnown |-> FALSE,
             gate |-> FALSE, nativeStarted |-> FALSE,
             canceledAfterClear |-> FALSE,
             releasedWhileHeld |-> FALSE,
             releasedAfterExpired |-> FALSE,
             staleRefused |-> FALSE, stopDebt |-> FALSE,
             outcome |-> "none", finished |-> FALSE,
             stopReturned |-> FALSE, capacityReleased |-> FALSE]

(* SaveHeld is durable before the first child is created. A retried child keeps
   the same Started.RunID; a different callback RunID is a stale owner. *)
SaveHeld == /\ s.attempt = 0 /\ ~s.paused /\ ~s.stopped
            /\ s' = [s EXCEPT !.held = TRUE, !.heldMemory = TRUE,
                             !.attempt = 1,
                             !.activeRun = 1, !.callbackRun = 1]
RetrySaveHeld == /\ s.attempt = 1 /\ s.heldMemory /\ ~s.held
                 /\ s.server = "working" /\ s.outcome = "none"
                 /\ ~s.finished /\ ~s.stopReturned /\ ~s.capacityReleased
                 /\ ~s.paused /\ ~s.stopped
                 /\ s' = [s EXCEPT !.held = TRUE,
                                  !.heldSaveFailed = FALSE]
LaunchBlocked == /\ s.held /\ s.preAckError = "none"
                 /\ s.primary = "none" /\ s.attempt = 1
                 /\ s' = [s EXCEPT !.primary = "blocked",
                                  !.callbackAlive = TRUE,
                                  !.launchWasDurable = s.held]
StorePrimary == /\ s.primary = "blocked" /\ ~s.primaryReceipt
                /\ s' = [s EXCEPT !.primaryReceipt = TRUE]
StartAnchor == /\ s.primary = "blocked" /\ s.primaryReceipt /\ ~s.anchor
               /\ s.preAckError = "none"
               /\ s' = [s EXCEPT !.anchor = TRUE]

(* An ordinary error can precede the callback. ENOENT or an inspection
   refusal leaves no child/RUN; receipt absence plus PID=0 is checked before
   treating it as quiet. A failure after RUN but before anchor retains the
   physical group debt, without manufacturing an anchor or quiet proof. *)
PreAckNoChildError == /\ s.held /\ s.primary = "none"
                      /\ s.preAckError = "none" /\ s.attempt = 1
                      /\ s' = [s EXCEPT !.preAckError = "no-child",
                                       !.hadNoChildError = TRUE]
VerifyNoChildQuiet == /\ s.preAckError = "no-child"
                      /\ s.primary = "none"
                      /\ ~s.primaryReceipt /\ ~s.anchor /\ AuxSettled
                      /\ s' = [s EXCEPT !.primary = "quiet",
                                       !.quietObserved = TRUE]
PreAckPhysicalError == /\ s.held /\ s.primary = "blocked"
                       /\ s.primaryReceipt /\ ~s.anchor
                       /\ s.preAckError = "none"
                       /\ s' = [s EXCEPT !.preAckError = "physical",
                                        !.hadPhysicalError = TRUE,
                                        !.callbackAlive = FALSE]

(* The source's exact pure Plan is abstracted by tupleCurrent and Started.
   Commit may stamp a current Ready card or an unstarted Working card. *)
CommitAck == /\ s.primary = "blocked" /\ s.anchor
             /\ s.reply = "none" /\ ~s.paused /\ ~s.stopped
             /\ s.tupleCurrent /\ s.server \in {"ready", "working"}
             /\ s' = [s EXCEPT !.server = "working",
                              !.serverStarted = TRUE,
                              !.committed = TRUE, !.reply = "unknown"]
LoseUncommitted == /\ s.primary = "blocked" /\ s.anchor
                   /\ s.reply = "none" /\ ~s.lost
                   /\ s' = [s EXCEPT !.reply = "unknown", !.lost = TRUE]
LoseCommittedReply == /\ s.reply = "unknown" /\ s.committed /\ ~s.lost
                      /\ s' = [s EXCEPT !.lost = TRUE]
HistoricalReplay == /\ s.lost /\ s.committed /\ ~s.history
                    /\ s' = [s EXCEPT !.history = TRUE]
DirectAnswer == /\ s.reply = "unknown" /\ ~s.lost /\ s.committed
                /\ ~s.crashed
                /\ s' = [s EXCEPT !.reply = "accepted"]
FreshPlan == /\ s.reply = "unknown" /\ s.lost
             /\ ~s.crashed
             /\ ~s.paused /\ ~s.stopped /\ s.tupleCurrent
             /\ s.server \in {"ready", "working"}
             /\ s' = [s EXCEPT !.server = "working",
                              !.serverStarted = TRUE,
                              !.committed = TRUE,
                              !.reply = "accepted", !.freshUsed = TRUE]
RefuseFresh == /\ s.reply = "unknown" /\ s.lost
               /\ ~s.crashed
               /\ (s.paused \/ s.stopped \/ ~s.tupleCurrent \/
                    s.server \notin {"ready", "working"})
               /\ s' = [s EXCEPT !.reply = "refused",
                                !.freshUsed = TRUE]

Pause == /\ ~s.paused /\ ~s.stopped
         /\ s' = [s EXCEPT !.paused = TRUE,
                          !.wasPausedWhileHeld = (s.wasPausedWhileHeld \/
                                                   (s.held /\ s.primary = "blocked"))]
Resume == /\ s.paused /\ ~s.stopped
          /\ s' = [s EXCEPT !.paused = FALSE]
Stop == /\ ~s.stopped
        /\ s' = [s EXCEPT !.stopped = TRUE,
                         !.stopDebt = (s.server = "working" /\
                                      s.serverStarted /\ s.committed)]
CancelOldRun == /\ s.primary = "blocked" /\ s.attempt = 1
                /\ s.callbackRun = s.activeRun
                /\ s' = [s EXCEPT !.activeRun = 2,
                                 !.tupleCurrent = FALSE]
ExpireCallback == /\ s.primary = "blocked" /\ s.anchor
                  /\ s.reply = "accepted" /\ s.held /\ s.callbackAlive
                  /\ s' = [s EXCEPT !.callbackAlive = FALSE]

(* The main-loop clear is a second durable transition. A historical reply
   cannot authorize it. Latest PAUSE/STOP, active run, owner and exact card
   must still match; a failed SaveLanes leaves held true. *)
ClearHeld == /\ s.held /\ s.primary = "blocked" /\ s.anchor
             /\ ~s.crashed
             /\ s.committed /\ s.server = "working" /\ s.serverStarted
             /\ (SkipActiveRun \/ OwnedCurrent)
             /\ (SkipCallbackLiveness \/ s.callbackAlive)
             /\ (SkipPauseAtClear \/ (~s.paused /\ ~s.stopped))
             /\ (s.reply = "accepted" \/
                  (UseHistoricalAnswer /\ s.history))
             /\ s' = [s EXCEPT !.held = FALSE, !.clearDone = TRUE,
                              !.heldMemory = FALSE,
                              !.clearSaveFailed = FALSE,
                              !.clearExact = OwnedCurrent,
                              !.clearCallbackAlive = s.callbackAlive,
                              !.clearModeOK = (~s.paused /\ ~s.stopped),
                              !.clearAckKnown = (s.reply = "accepted"),
                              !.historyUsedForClear = (s.reply # "accepted")]
FailClearSave == /\ s.held /\ s.primary = "blocked"
                 /\ s.reply = "accepted" /\ ~s.crashed
                 /\ ~s.clearSaveFailed
                 /\ s' = [s EXCEPT !.clearSaveFailed = TRUE]
CancelAfterClear == /\ s.clearDone /\ ~s.held
                    /\ s.primary = "blocked" /\ s.callbackAlive
                    /\ ~s.crashed
                    /\ s' = [s EXCEPT !.callbackAlive = FALSE,
                                     !.canceledAfterClear = TRUE]
ReleaseGate == /\ s.primary = "blocked" /\ s.anchor
               /\ s.committed /\ ~s.crashed /\ ~s.stopped
               /\ (SkipContextAtRelease \/ s.callbackAlive)
               /\ (~s.held \/ ReleaseBeforeClear)
               /\ s' = [s EXCEPT !.primary = "live", !.gate = TRUE,
                                !.nativeStarted = TRUE,
                                !.releasedWhileHeld = s.held,
                                !.releasedAfterExpired = ~s.callbackAlive]

LaunchAux == /\ s.aux = "none" /\ s.primary \in {"blocked", "live"}
             /\ s' = [s EXCEPT !.aux = "live"]
LoseAuxIdentity == /\ s.aux = "live"
                   /\ s' = [s EXCEPT !.aux = "unknown"]
AuxQuiets == /\ s.aux = "live"
             /\ s' = [s EXCEPT !.aux = "quiet"]
VerifyAuxQuiet == /\ s.aux = "unknown"
                  /\ s' = [s EXCEPT !.aux = "quiet"]

(* PAUSE closes a still-held blocked child. A second child may be prepared
   only after primary and auxiliary quiet proof. It uses a new caller op. *)
AbortHeld == /\ s.primary = "blocked" /\ s.held
             /\ (s.paused \/ s.stopped \/ ~OwnedCurrent \/
                  s.reply = "refused" \/ s.crashed \/ ~s.callbackAlive)
             /\ s.primaryReceipt /\ s.anchor
             /\ s' = [s EXCEPT !.primary = "quiet",
                              !.callbackAlive = FALSE,
                              !.quietObserved = TRUE,
                              !.staleRefused = ~OwnedCurrent]
RetryChild == /\ (s.held \/ (SkipHeldDurability /\ s.heldMemory))
              /\ s.attempt = 1
              /\ s.server \in {"ready", "working"} /\ s.outcome = "none"
              /\ ~s.finished /\ ~s.stopReturned /\ ~s.capacityReleased
              /\ ~s.paused /\ ~s.stopped /\ ~s.crashed
              /\ s.primary \in {"blocked", "quiet"}
              /\ (SkipPriorQuiet \/
                   (s.primary = "quiet" /\ s.quietObserved /\ AuxSettled))
              /\ s' = [s EXCEPT !.attempt = 2,
                               !.tupleCurrent = TRUE,
                               !.retryFromPause = s.wasPausedWhileHeld,
                               !.wasPausedWhileHeld = FALSE,
                               !.primary = "blocked",
                               !.callbackAlive = TRUE,
                               !.nativeStarted = FALSE,
                               !.gate = FALSE,
                               !.primaryReceipt = FALSE,
                               !.anchor = FALSE,
                               !.quietObserved = FALSE,
                               !.clearDone = FALSE,
                               !.clearExact = FALSE,
                               !.clearCallbackAlive = FALSE,
                               !.clearModeOK = FALSE,
                               !.clearAckKnown = FALSE,
                               !.reply = "none", !.lost = FALSE,
                               !.preAckError = "none",
                               !.freshUsed = FALSE,
                               !.canceledAfterClear = FALSE,
                               !.history = FALSE,
                               !.launchWasDurable = s.held,
                               !.priorQuiet = (s.primary = "quiet" /\
                                               s.quietObserved /\ AuxSettled)]
(* Between turns, PAUSE can find the previous child quiet while this card is
   still owned. markLaunchHeld then restores the hold. A failed save leaves it
   in memory only; holdNativeStart retries before the next child. *)
FailRehold == /\ ~s.held /\ s.clearDone /\ s.primary = "quiet"
              /\ s.quietObserved /\ AuxSettled /\ s.nativeStarted
              /\ s.outcome = "none"
              /\ s.server = "working" /\ ~s.finished
              /\ ~s.stopReturned /\ ~s.capacityReleased
              /\ s.attempt = 1
              /\ s.paused
              /\ s' = [s EXCEPT !.heldMemory = TRUE,
                               !.heldSaveFailed = TRUE,
                               !.hadHoldSaveFailure = TRUE]
Rehold == /\ ~s.held /\ s.clearDone /\ s.primary = "quiet"
          /\ s.quietObserved /\ AuxSettled /\ s.nativeStarted
          /\ s.outcome = "none" /\ s.attempt = 1
          /\ s.server = "working" /\ ~s.finished
          /\ ~s.stopReturned /\ ~s.capacityReleased
          /\ ~s.heldSaveFailed
          /\ ~s.paused /\ ~s.stopped /\ ~s.crashed
          /\ s' = [s EXCEPT !.held = TRUE, !.heldMemory = TRUE]
CrashBeforeClear == /\ s.held /\ s.primary = "blocked" /\ s.anchor
                    /\ ~s.crashed
                    /\ s' = [s EXCEPT !.crashed = TRUE,
                                     !.callbackAlive = FALSE,
                                     !.hadCrash = TRUE]
CrashAfterClear == /\ ~s.held /\ s.clearDone
                   /\ s.primary = "blocked" /\ ~s.crashed
                   /\ s' = [s EXCEPT !.crashed = TRUE,
                                    !.callbackAlive = FALSE,
                                    !.hadCrash = TRUE]
(* If PAUSE lifts before a failed between-turn hold save is retried, a crash
   loses only the in-memory marker. The durable clear remains; a quiet run is
   settled failed on restart rather than blindly admitted again. *)
CrashDirtyHold == /\ s.hadHoldSaveFailure /\ s.heldMemory /\ ~s.held
                  /\ s.primary = "quiet" /\ s.quietObserved /\ AuxSettled
                  /\ ~s.paused /\ ~s.stopped /\ ~s.crashed
                  /\ s' = [s EXCEPT !.crashed = TRUE,
                                   !.callbackAlive = FALSE,
                                   !.hadCrash = TRUE,
                                   !.heldMemory = FALSE]
RestartHeld == /\ s.crashed /\ s.held
               /\ s.primary = "quiet" /\ s.quietObserved /\ AuxSettled
               /\ s' = [s EXCEPT !.crashed = FALSE]
RecoverAfterClear == /\ s.crashed /\ s.clearDone
                     /\ s.primary = "blocked" /\ s.primaryReceipt /\ s.anchor
                     /\ s' = [s EXCEPT !.primary = "quiet",
                                      !.quietObserved = TRUE,
                                      !.outcome = "failed"]
RecoverDirtyUnheld == /\ s.crashed /\ s.hadHoldSaveFailure /\ ~s.held
                      /\ ~s.heldMemory /\ s.primary = "quiet"
                      /\ s.quietObserved /\ AuxSettled /\ ~s.paused
                      /\ s.outcome = "none"
                      /\ s' = [s EXCEPT !.outcome = "failed"]
AbortCleared == /\ s.stopped /\ s.clearDone
                /\ s.primary = "blocked" /\ s.primaryReceipt /\ s.anchor
                /\ s' = [s EXCEPT !.primary = "quiet",
                                 !.quietObserved = TRUE]
AbortCanceledClear == /\ s.clearDone /\ ~s.held
                      /\ s.primary = "blocked" /\ ~s.callbackAlive
                      /\ ~s.crashed /\ ~s.stopped
                      /\ s.primaryReceipt /\ s.anchor
                      /\ s' = [s EXCEPT !.primary = "quiet",
                                       !.quietObserved = TRUE,
                                       !.outcome = "failed"]
(* A saved clear can precede a failed physical pipe release. The child did
   not execute provider code; only a verified quiet group permits a failed
   outcome on the already-Working card. *)
PostClearGateFails == /\ s.clearDone /\ ~s.held
                      /\ s.primary = "blocked" /\ s.primaryReceipt /\ s.anchor
                      /\ ~s.crashed /\ ~s.stopped /\ ~s.nativeStarted
                      /\ s' = [s EXCEPT !.primary = "quiet",
                                       !.quietObserved = TRUE]
NativeFinishes == /\ s.primary = "live" /\ s.nativeStarted
                  /\ s.outcome = "none"
                  /\ s' = [s EXCEPT !.outcome = "success"]
TurnQuietsWithCard == /\ s.primary = "live" /\ s.nativeStarted
                      /\ s.outcome = "none" /\ ~s.paused /\ ~s.stopped
                      /\ s' = [s EXCEPT !.primary = "quiet",
                                       !.quietObserved = TRUE]
PrimaryQuiets == /\ s.primary = "live"
                 /\ s' = [s EXCEPT !.primary = "quiet",
                                  !.quietObserved = TRUE]
FailedBlockedLaunch == /\ s.primary = "quiet" /\ s.quietObserved
                       /\ s.committed /\ s.server = "working"
                       /\ s.clearDone /\ ~s.held
                       /\ ~s.nativeStarted
                       /\ s.outcome = "none" /\ ~s.paused /\ ~s.stopped
                       /\ s' = [s EXCEPT !.outcome = "failed"]
Finish == /\ s.server = "working" /\ ~s.stopped
          /\ s.committed /\ s.outcome # "none"
          /\ s.primary = "quiet" /\ s.quietObserved /\ AuxSettled
          /\ s' = [s EXCEPT !.server = "done", !.finished = TRUE,
                           !.held = FALSE]
StopReturn == /\ s.stopped /\ s.stopDebt
              /\ s.server = "working" /\ s.primary = "quiet"
              /\ s.quietObserved /\ AuxSettled
              /\ s' = [s EXCEPT !.server = "done",
                               !.stopReturned = TRUE,
                               !.stopDebt = FALSE, !.held = FALSE]
ReleaseCapacity == /\ s.server = "done"
                   /\ (s.finished \/ s.stopReturned)
                   /\ s.primary = "quiet" /\ s.quietObserved /\ AuxSettled
                   /\ ~s.capacityReleased
                   /\ s' = [s EXCEPT !.capacityReleased = TRUE]

Next == \/ SaveHeld \/ RetrySaveHeld \/ FailRehold \/ Rehold
        \/ LaunchBlocked \/ StorePrimary \/ StartAnchor
        \/ PreAckNoChildError \/ VerifyNoChildQuiet \/ PreAckPhysicalError
        \/ CommitAck \/ LoseUncommitted \/ LoseCommittedReply
        \/ HistoricalReplay \/ DirectAnswer \/ FreshPlan \/ RefuseFresh
        \/ Pause \/ Resume \/ Stop \/ CancelOldRun \/ ExpireCallback
        \/ ClearHeld
        \/ FailClearSave \/ CancelAfterClear \/ ReleaseGate
        \/ LaunchAux \/ LoseAuxIdentity
        \/ AuxQuiets \/ VerifyAuxQuiet \/ AbortHeld \/ RetryChild
        \/ CrashBeforeClear \/ CrashAfterClear \/ CrashDirtyHold
        \/ RestartHeld \/ RecoverAfterClear \/ RecoverDirtyUnheld
        \/ AbortCleared \/ AbortCanceledClear \/ PostClearGateFails
        \/ NativeFinishes \/ TurnQuietsWithCard \/ PrimaryQuiets
        \/ FailedBlockedLaunch
        \/ Finish \/ StopReturn \/ ReleaseCapacity
Spec == Init /\ [][Next]_vars

TypeOK == /\ s.server \in {"ready", "working", "done"}
          /\ s.primary \in {"none", "blocked", "live", "quiet"}
          /\ s.aux \in {"none", "live", "unknown", "quiet"}
          /\ s.reply \in {"none", "unknown", "accepted", "refused"}
          /\ s.preAckError \in {"none", "no-child", "physical"}
          /\ s.attempt \in {0, 1, 2}
          /\ s.activeRun \in {0, 1, 2}
          /\ s.callbackRun \in {0, 1, 2}
NoNativeBeforeDurableClear == s.nativeStarted =>
                              (s.committed /\ s.clearDone /\ ~s.releasedWhileHeld)
NoLaunchWithoutDurableHold == s.primary \in {"blocked", "live"} =>
                              s.launchWasDurable
NoClearWrongRun == s.clearDone => s.clearExact
NoClearExpiredCallback == s.clearDone => s.clearCallbackAlive
NoReleaseAfterExpiredContext == ~s.releasedAfterExpired
NoClearDuringPauseStop == s.clearDone => s.clearModeOK
NoClearFromHistory == ~s.historyUsedForClear
NoClearWithoutCurrentAnswer == s.clearDone => s.clearAckKnown
FailedClearSaveRetainsHeld == (s.clearSaveFailed /\ s.server = "working") => s.held
NoRetryBeforeQuiet == s.attempt = 2 => s.priorQuiet
NoCapacityBeforeQuiet == s.capacityReleased =>
                         (s.server = "done" /\ s.primary = "quiet" /\
                          s.quietObserved /\ AuxSettled)
NoFalseFinish == s.finished => (s.committed /\ s.outcome # "none" /\
                                s.server = "done")
NoPreAckErrorSettlement == s.preAckError # "none" =>
                           (s.held /\ ~s.finished /\
                            ~s.capacityReleased /\ ~s.nativeStarted)
NoFalseStopReturn == s.stopReturned => (s.stopped /\ s.server = "done")
NoWorkAfterCrashBeforeClear == s.crashed /\ ~s.clearDone => ~s.nativeStarted

(* Each dedicated witness is expected to violate its negated property. *)
WitnessPauseHoldsAck == ~(s.paused /\ s.committed /\ s.held /\
                           s.primary = "blocked" /\ ~s.nativeStarted)
WitnessPauseAfterClearMayLaunch == ~(s.paused /\ s.clearDone /\
                                     s.nativeStarted /\ s.clearModeOK)
WitnessResumeFreshLaunch == ~(s.attempt = 2 /\ s.nativeStarted /\
                               s.freshUsed /\ s.retryFromPause)
WitnessStaleClearRefused == ~(s.staleRefused /\ s.clearDone = FALSE)
WitnessExpiredCallbackRefused == ~(s.reply = "accepted" /\
                                   ~s.callbackAlive /\ s.held /\
                                   s.primary = "blocked")
WitnessStopDebtSettled == ~s.stopReturned
WitnessCrashBeforeHeld == ~(s.crashed /\ s.held /\ ~s.nativeStarted)
WitnessCrashBeforeRecovered == ~(s.hadCrash /\ s.attempt = 2 /\
                                  s.nativeStarted)
WitnessCrashAfterClearFailed == ~(s.crashed /\ s.clearDone /\ s.finished /\
                                   s.outcome = "failed" /\ ~s.nativeStarted)
WitnessClearSaveFailsHeld == ~(s.clearSaveFailed /\ s.held /\
                                ~s.nativeStarted)
WitnessCanceledClearSettled == ~(s.finished /\ s.outcome = "failed" /\
                                  ~s.nativeStarted /\ ~s.callbackAlive /\
                                  s.clearDone /\ s.canceledAfterClear)
WitnessFailedHoldSaveRetry == ~(s.hadHoldSaveFailure /\ s.held /\
                                 s.attempt = 2 /\ s.primary = "blocked" /\
                                 s.launchWasDurable)
WitnessDirtyHoldCrashFailed == ~(s.hadHoldSaveFailure /\ s.crashed /\
                                  s.finished /\ s.outcome = "failed" /\
                                  ~s.held)
WitnessFastTurn == ~(s.finished /\ s.nativeStarted /\
                     s.outcome = "success")
WitnessLostFreshClear == ~(s.lost /\ s.freshUsed /\ s.clearDone)
WitnessPreAckNoChildRetry == ~(s.hadNoChildError /\ s.attempt = 2 /\
                               s.primary = "blocked" /\ s.held /\
                               ~s.committed)
WitnessPreAckPhysicalHeld == ~(s.hadPhysicalError /\ s.primary = "blocked" /\
                               s.held /\ ~s.committed /\ ~s.finished)
=============================================================================
