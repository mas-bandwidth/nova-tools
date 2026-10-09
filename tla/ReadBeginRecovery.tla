--------------------------- MODULE ReadBeginRecovery ---------------------------
(* One read lease, one stable BEGIN op and one bounded retry/restart. BEGIN
   reserves capacity until ACTIVE or settlement/STOP takes ownership. Fresh
   queue authority includes the head although the stable op excludes it. *)
EXTENDS Naturals
CONSTANTS SkipIntent, ChangeReplayOp, SkipFreshQueue, SkipHeadMatch,
          ForgetBeginCapacity, ClearBeforeActive, IgnoreEvidenceCleanup,
          IgnoreStopOwner
VARIABLE s
vars == <<s>>

Init == s = [server |-> "asked", lease |-> "base",
             queue |-> "asked", queueLease |-> "base", known |-> TRUE,
             revision |-> 1, lastTry |-> 0, nativeAuthority |-> TRUE,
             alive |-> TRUE, restarted |-> FALSE, crashedOnce |-> FALSE,
             paused |-> FALSE, stopped |-> FALSE,
             grant |-> FALSE, grantExact |-> FALSE,
             intent |-> FALSE, intentSaveFailed |-> FALSE,
             intentOp |-> "begin-base", requests |-> 0,
             requestOp |-> "none", requestDurable |-> TRUE,
             requestExact |-> TRUE, reply |-> "none",
             cachedAccepted |-> FALSE, lost |-> FALSE, acceptedLost |-> FALSE, replayed |-> FALSE,
             replayAfterRestart |-> FALSE, restartLostIntent |-> FALSE,
             preparing |-> FALSE, active |-> FALSE, activeGrant |-> FALSE,
             activeSaveFailed |-> FALSE, clearFailed |-> FALSE,
             clearedWithoutActive |-> FALSE, cleanupOverEvidence |-> FALSE,
             headHeld |-> FALSE, native |-> "none", quietObserved |-> FALSE,
             settlement |-> FALSE, verdictSent |-> FALSE,
             verdictBehindStop |-> FALSE,
             stopProof |-> FALSE, stopOwned |-> FALSE, debtOwed |-> FALSE,
             stopSaveFailed |-> FALSE, stopApplied |-> FALSE,
             otherWork |-> FALSE, workBehindBegin |-> FALSE]

LeaseEvidence == s.active \/ s.settlement
Quiet == s.native = "none" \/ (s.native = "quiet" /\ s.quietObserved)
Reservation == s.intent \/ s.active \/ s.settlement \/ s.debtOwed \/
               s.preparing \/ (s.known /\ s.queue = "reading")
FreshQueue == s.known /\ s.revision > s.lastTry
ExactHead == s.queueLease = "base"

RefreshQueue == /\ s.alive /\ s.revision < 3 /\ ~s.grant
                /\ s' = [s EXCEPT !.revision = @ + 1,
                                 !.queue = s.server, !.queueLease = s.lease,
                                 !.known = ~(s.intent /\ s.lease = "head-changed" /\ ~SkipHeadMatch),
                                 !.headHeld = (s.headHeld \/ (s.intent /\ s.lease = "head-changed"))]
QueueFailure == /\ s.alive /\ s.known /\ ~s.grant
                /\ s' = [s EXCEPT !.known = FALSE]
ChangeHead == /\ s.lease = "base" /\ s.intent
              /\ s' = [s EXCEPT !.lease = "head-changed"]
NewGeneration == /\ s.lease = "base" /\ s.intent
                 /\ s' = [s EXCEPT !.lease = "new-generation", !.server = "asked"]
LoseNativeAuthority == /\ s.nativeAuthority
                       /\ s' = [s EXCEPT !.nativeAuthority = FALSE]
Pause == /\ ~s.paused /\ ~s.stopped
         /\ s' = [s EXCEPT !.paused = TRUE]
Resume == /\ s.paused /\ ~s.stopped
          /\ s' = [s EXCEPT !.paused = FALSE]
Stop == /\ ~s.stopped
        /\ s' = [s EXCEPT !.stopped = TRUE]

(* beginRead checks control and a newer queue, then saves before sending.
   grant captures this source check; it is not an instantaneous remote lease. *)
PrepareBegin == /\ s.alive /\ ~s.grant /\ s.requests < 2
                /\ s.reply \in {"none", "lost", "refused", "held"}
                /\ ~s.active /\ ~s.settlement /\ ~s.stopOwned /\ ~s.otherWork
                /\ ~s.paused /\ ~s.stopped /\ s.nativeAuthority
                /\ (SkipFreshQueue \/ FreshQueue)
                /\ (SkipHeadMatch \/ ExactHead)
                /\ (s.intent \/ s.queue = "asked")
                /\ s.queue \in {"asked", "reading"}
                /\ s' = [s EXCEPT !.grant = TRUE, !.lastTry = s.revision,
                                 !.grantExact = (FreshQueue /\ ExactHead)]
SaveIntent == /\ s.grant /\ ~s.intent
              /\ s' = [s EXCEPT !.intent = TRUE]
FailIntentSave == /\ s.grant /\ ~s.intent /\ ~s.intentSaveFailed
                  /\ s' = [s EXCEPT !.intentSaveFailed = TRUE, !.grant = FALSE]
SendBegin == /\ s.alive /\ s.grant /\ (s.intent \/ SkipIntent)
             /\ s' = [s EXCEPT !.requests = @ + 1,
                              !.requestOp = IF ChangeReplayOp /\ s.requests = 1
                                            THEN "begin-changed" ELSE s.intentOp,
                              !.requestDurable = s.intent,
                              !.requestExact = s.grantExact,
                              !.reply = "pending", !.grant = FALSE,
                              !.replayed = s.cachedAccepted,
                              !.replayAfterRestart = (s.restarted /\ s.cachedAccepted /\ s.acceptedLost)]
CommitBegin == /\ s.requests > 0 /\ s.reply \in {"pending", "lost"}
               /\ s.requestOp = s.intentOp /\ ~s.cachedAccepted
               /\ s.server = "asked" /\ s.lease = "base"
               /\ ~s.paused /\ ~s.stopped
               /\ s' = [s EXCEPT !.server = "reading", !.cachedAccepted = TRUE,
                                !.acceptedLost = (s.acceptedLost \/ s.reply = "lost")]
LoseBeginReply == /\ s.alive /\ s.reply = "pending"
                  /\ s' = [s EXCEPT !.reply = "lost", !.lost = TRUE,
                                   !.acceptedLost = (s.acceptedLost \/ s.cachedAccepted)]
RefuseBeginReply == /\ s.alive /\ s.reply = "pending"
                    /\ s' = [s EXCEPT !.reply = "refused"]
AcceptBeginReply == /\ s.alive /\ s.reply = "pending" /\ s.cachedAccepted
                    /\ s.requestOp = s.intentOp
                    /\ s' = [s EXCEPT !.reply = "accepted", !.preparing = TRUE]

SaveActive == /\ s.alive /\ s.preparing /\ ~s.active
              /\ s' = [s EXCEPT !.active = TRUE, !.activeGrant = TRUE, !.preparing = FALSE]
FailActiveSave == /\ s.alive /\ s.preparing /\ ~s.activeSaveFailed
                  /\ s' = [s EXCEPT !.activeSaveFailed = TRUE, !.preparing = FALSE,
                                   !.reply = "held"]
(* Recovered held ACTIVE requires fresh canonical same-lease queue permission.
   This captures the startRead retry check; later local control still fences exec. *)
RecoverActive == /\ s.alive /\ s.active /\ ~s.activeGrant /\ s.native = "none"
                 /\ s.known /\ ExactHead /\ s.queue = "reading"
                 /\ ~s.paused /\ ~s.stopped /\ ~s.stopOwned
                 /\ ~s.settlement /\ ~s.otherWork /\ s.nativeAuthority
                 /\ s' = [s EXCEPT !.activeGrant = TRUE]
ClearIntent == /\ s.alive /\ s.intent
               /\ ((s.active /\ s.activeGrant) \/ (ClearBeforeActive /\ s.reply = "accepted"))
               /\ s' = [s EXCEPT !.intent = FALSE,
                                !.clearedWithoutActive = ~s.active]
FailIntentClear == /\ s.alive /\ s.intent /\ s.active /\ ~s.clearFailed
                   /\ s' = [s EXCEPT !.clearFailed = TRUE]
LaunchRead == /\ s.alive /\ s.active /\ s.activeGrant /\ ~s.intent /\ s.native = "none"
              /\ ~s.paused /\ ~s.stopped /\ ~s.stopOwned
              /\ s.nativeAuthority /\ ~s.settlement
              /\ s' = [s EXCEPT !.native = "live"]
NativeExit == /\ s.native = "live"
              /\ s' = [s EXCEPT !.native = "quiet"]
ObserveQuiet == /\ s.native = "quiet" /\ ~s.quietObserved
                /\ s' = [s EXCEPT !.quietObserved = TRUE]
SaveSettlement == /\ s.alive /\ s.active /\ s.native = "quiet" /\ Quiet
                  /\ ~s.settlement /\ (~s.stopOwned \/ IgnoreStopOwner)
                  /\ s' = [s EXCEPT !.settlement = TRUE]
ClearSettledActive == /\ s.alive /\ s.active /\ s.settlement /\ Quiet
                      /\ s' = [s EXCEPT !.active = FALSE]
SendSettlement == /\ s.alive /\ s.settlement /\ ~s.active /\ Quiet
                  /\ (~s.stopOwned \/ IgnoreStopOwner) /\ ~s.verdictSent
                  /\ s.server = "reading" /\ s.lease = "base"
                  /\ s' = [s EXCEPT !.server = "done", !.verdictSent = TRUE,
                                   !.verdictBehindStop = s.stopOwned]
ConfirmSettlement == /\ s.alive /\ s.settlement /\ s.verdictSent
                     /\ s' = [s EXCEPT !.settlement = FALSE]

(* BEGIN-only accepted STOP: save no-child proof before durable STOP debt.
   Existing settlement wins; a STOP-owned lease never publishes a verdict. *)
SaveStopProof == /\ s.alive /\ s.stopped /\ s.intent /\ ~LeaseEvidence
                 /\ s.known /\ s.queue = "reading" /\ ExactHead
                 /\ s.native = "none" /\ ~s.stopProof
                 /\ s' = [s EXCEPT !.stopProof = TRUE]
SaveStopDebt == /\ s.alive /\ s.stopped /\ (s.stopProof \/ s.active)
                /\ ~s.settlement /\ ~s.stopOwned
                /\ s' = [s EXCEPT !.stopOwned = TRUE, !.debtOwed = TRUE,
                                 !.preparing = FALSE]
FailStopDebtSave == /\ s.alive /\ s.stopped /\ (s.stopProof \/ s.active)
                    /\ ~s.settlement /\ ~s.stopOwned /\ ~s.stopSaveFailed
                    /\ s' = [s EXCEPT !.stopSaveFailed = TRUE]
ApplyStopReturn == /\ s.alive /\ s.debtOwed /\ Quiet /\ ~s.stopApplied
                   /\ (s.stopProof \/ s.active)
                   /\ s' = [s EXCEPT !.stopApplied = TRUE,
                                    !.server = "asked", !.lease = "new-generation"]
ConfirmStopReturn == /\ s.alive /\ s.stopApplied /\ s.debtOwed
                     /\ s' = [s EXCEPT !.debtOwed = FALSE]
ClearStoppedActive == /\ s.alive /\ s.stopOwned /\ ~s.debtOwed /\ Quiet /\ s.active
                      /\ s' = [s EXCEPT !.active = FALSE]

CleanupStaleIntent == /\ s.alive /\ s.intent /\ s.known
                      /\ (s.queue = "done" \/ s.queueLease = "new-generation")
                      /\ (~LeaseEvidence \/ IgnoreEvidenceCleanup)
                      /\ s' = [s EXCEPT !.intent = FALSE,
                                       !.cleanupOverEvidence = LeaseEvidence]
AdmitOtherWork == /\ s.alive /\ s.known /\ ~s.otherWork /\ ~s.grant
                  /\ (ForgetBeginCapacity \/ ~s.intent)
                  /\ ~s.active /\ ~s.settlement /\ ~s.debtOwed /\ ~s.preparing
                  /\ s.queue # "reading"
                  /\ s' = [s EXCEPT !.otherWork = TRUE, !.workBehindBegin = s.intent]
Crash == /\ s.alive /\ ~s.crashedOnce
         /\ s' = [s EXCEPT !.alive = FALSE, !.crashedOnce = TRUE,
                          !.known = FALSE, !.revision = 0, !.lastTry = 0,
                          !.grant = FALSE, !.activeGrant = FALSE, !.preparing = FALSE,
                          !.restartLostIntent = (s.intent /\ ~s.active /\ s.cachedAccepted /\ s.acceptedLost),
                          !.reply = "none"]
Restart == /\ ~s.alive
           /\ s' = [s EXCEPT !.alive = TRUE, !.restarted = TRUE]

Next == \/ RefreshQueue \/ QueueFailure \/ ChangeHead \/ NewGeneration
        \/ LoseNativeAuthority \/ Pause \/ Resume \/ Stop
        \/ PrepareBegin \/ SaveIntent \/ FailIntentSave \/ SendBegin
        \/ CommitBegin \/ LoseBeginReply \/ RefuseBeginReply \/ AcceptBeginReply
        \/ SaveActive \/ FailActiveSave \/ RecoverActive \/ ClearIntent \/ FailIntentClear
        \/ LaunchRead \/ NativeExit \/ ObserveQuiet
        \/ SaveSettlement \/ ClearSettledActive \/ SendSettlement \/ ConfirmSettlement
        \/ SaveStopProof \/ SaveStopDebt \/ FailStopDebtSave
        \/ ApplyStopReturn \/ ConfirmStopReturn \/ ClearStoppedActive
        \/ CleanupStaleIntent \/ AdmitOtherWork \/ Crash \/ Restart
Spec == Init /\ [][Next]_vars

TypeOK == /\ s.server \in {"asked", "reading", "done"}
          /\ s.lease \in {"base", "head-changed", "new-generation"}
          /\ s.queue \in {"asked", "reading", "done"}
          /\ s.queueLease \in {"base", "head-changed", "new-generation"}
          /\ s.revision \in 0..3 /\ s.lastTry \in 0..3 /\ s.requests \in 0..2
          /\ s.reply \in {"none", "pending", "lost", "refused", "accepted", "held"}
          /\ s.native \in {"none", "live", "quiet"}
IntentBeforeRPC == s.requests > 0 => s.requestDurable
StableReplayOp == s.requests > 0 => s.requestOp = s.intentOp
FreshLeaseBeforeRPC == s.requests > 0 => s.requestExact
BeginCapacityHeld == ~s.workBehindBegin
ActiveBeforeBeginClear == ~s.clearedWithoutActive
EvidenceWinsCleanup == ~s.cleanupOverEvidence
StopOwnsVerdict == ~s.verdictBehindStop
NativeNeedsOwnership == s.native = "live" => s.active

WitnessLostRestartLaunch == ~(s.restartLostIntent /\ s.replayAfterRestart /\ s.native = "live")
WitnessBeginCapacityHeld == ~(s.intent /\ s.cachedAccepted /\ s.reply = "lost" /\ ~s.otherWork)
WitnessSameHeadChangeHeld == ~(s.intent /\ s.headHeld /\ ~s.known /\ s.native = "none")
WitnessActiveSaveRefused == ~(s.intent /\ s.activeSaveFailed /\ ~s.active /\ s.native = "none")
WitnessIntentSaveRefused == ~(s.intentSaveFailed /\ ~s.intent /\ s.requests = 0)
WitnessClearFailureHeld == ~(s.intent /\ s.active /\ s.clearFailed /\ s.native = "none")
WitnessLostStopReturned == ~(s.acceptedLost /\ s.stopProof /\ s.stopOwned /\
                            s.stopApplied /\ ~s.debtOwed /\ s.native = "none")
WitnessStopSaveRefused == ~(s.intent /\ s.stopProof /\ s.stopSaveFailed /\ ~s.stopOwned)
WitnessEvidenceBlocksCleanup == ~(s.intent /\ LeaseEvidence /\ s.known /\ s.queueLease = "new-generation")
WitnessSettlementOwnsStop == ~(s.stopped /\ s.verdictSent /\ ~s.stopOwned /\ Quiet)
=============================================================================
