---------------------- MODULE NativeCardStartBoundary ----------------------
(* Bounded refinement of a one-shot native card start. RealExec's shell is
   blocked until the primary RUN and anchor are durable and the exact server
   StartAck is accepted. The auxiliary usage export has its own group identity.
   This is a state/order model, not a kernel, Redis, or provider proof. *)
EXTENDS Naturals, FiniteSets

CONSTANTS OverwritePrimary, ReleaseBeforeAck, SkipTupleCheck,
          GuardAuxRelease, UseHistoricalReplay, RefuseDoneReplay,
          AllowChangedReplay, SkipFreshPlan
VARIABLE s
vars == <<s>>

Bound == [card |-> "card", job |-> "card.g1", gen |-> 1,
          epoch |-> 1, actor |-> "friend"]
OtherTuples == {
  [Bound EXCEPT !.card = "other"],
  [Bound EXCEPT !.job = "card.g2"],
  [Bound EXCEPT !.gen = 2],
  [Bound EXCEPT !.epoch = 2],
  [Bound EXCEPT !.actor = "other"]
}

Init == s = [server |-> "ready", serverStarted |-> FALSE,
             current |-> Bound, op |-> "start-1", retryOp |-> "start-2",
             grantOp |-> "none", firstReplyLost |-> FALSE,
             firstUncommittedLost |-> FALSE,
             freshRetryUsed |-> FALSE, committedOp |-> "none",
             commitWasExact |-> FALSE, primary |-> "none",
             primaryReceipt |-> "none", anchor |-> FALSE,
             anchorReceipt |-> "none",
             aux |-> "none", auxPhysical |-> FALSE,
             auxReceipt |-> FALSE,
             ack |-> "none", replayArgs |-> Bound, replayConflict |-> FALSE,
             historyReplay |-> FALSE, replayedAfterDone |-> FALSE,
             grantValid |-> FALSE, redealt |-> FALSE,
             cleared |-> FALSE, paused |-> FALSE, stopped |-> FALSE,
             crashed |-> FALSE,
             gate |-> FALSE, nativeStarted |-> FALSE,
             historyUsedToLaunch |-> FALSE,
             outcome |-> "none", quietObserved |-> FALSE,
             stopDebt |-> FALSE, stopReturned |-> FALSE,
             finished |-> FALSE, capacityReleased |-> FALSE]

AuxSettled == s.aux \in {"none", "quiet"} /\ ~s.auxPhysical

(* The durable Started claim and immutable tuple/op predate the native launch.
   LaunchBlocked represents cmd.Start with a pipe-blocked shell, not work. *)
LaunchBlocked == /\ s.primary = "none" /\ ~s.stopped /\ ~s.paused
                 /\ AuxSettled
                 /\ s' = [s EXCEPT !.primary = "blocked"]
StorePrimary == /\ s.primary = "blocked" /\ s.primaryReceipt = "none"
                /\ s' = [s EXCEPT !.primaryReceipt = "primary"]
StartAnchor == /\ s.primary = "blocked" /\ s.primaryReceipt = "primary"
               /\ ~s.anchor
               /\ s' = [s EXCEPT !.anchor = TRUE,
                                !.anchorReceipt = "primary"]

(* Usage export is a separately anchored sibling. The broken switch reproduces
   calling the primary processStarted callback for the export. *)
LaunchAux == /\ (s.primary = "none" \/
                  (s.primary = "live" /\ s.anchor))
             /\ s.aux = "none"
             /\ ~s.finished /\ ~s.stopReturned /\ ~s.capacityReleased
             /\ s' = [s EXCEPT !.aux = "live", !.auxPhysical = TRUE,
                              !.auxReceipt = TRUE,
                              !.primaryReceipt = IF OverwritePrimary THEN "aux" ELSE @,
                              !.anchorReceipt = IF OverwritePrimary THEN "aux" ELSE @]
AuxQuiets == /\ s.aux = "live"
             /\ s' = [s EXCEPT !.aux = "quiet", !.auxPhysical = FALSE]
LoseAuxIdentity == /\ s.aux = "live"
                   /\ s' = [s EXCEPT !.aux = "unknown"]
UnknownAuxExits == /\ s.aux = "unknown" /\ s.auxPhysical
                   /\ s' = [s EXCEPT !.auxPhysical = FALSE]
VerifyAuxQuiet == /\ s.aux = "unknown" /\ ~s.auxPhysical
                  /\ s' = [s EXCEPT !.aux = "quiet"]

(* The server's pure Plan validates the canonical job, generation, epoch,
   actor and card. Commit is the Ready->Working linearization. A lost answer
   leaves the shell blocked; replay of the same stable op returns its result,
   even if a concurrent server finish has already changed Working to Done. *)
CommitStartAck == /\ s.primary = "blocked" /\ s.anchor
                  /\ ~s.crashed
                  /\ s.ack = "none"
                  /\ (s.server = "ready" \/ s.server = "working")
                  /\ ~s.paused /\ ~s.stopped
                  /\ (SkipTupleCheck \/ s.current = Bound)
                  /\ s' = [s EXCEPT !.server = "working",
                                   !.committedOp = s.op, !.ack = "unknown",
                                   !.serverStarted = TRUE,
                                   !.commitWasExact = (s.current = Bound)]
RefuseStartAck == /\ s.primary = "blocked" /\ s.anchor
                  /\ s.ack = "none"
                  /\ (s.paused \/ s.stopped \/ s.current # Bound \/
                      s.server \notin {"ready", "working"})
                  /\ s' = [s EXCEPT !.ack = "refused"]
LoseFirstReply == /\ s.ack = "unknown" /\ ~s.firstReplyLost
                  /\ s' = [s EXCEPT !.firstReplyLost = TRUE]
LoseUncommittedRequest == /\ s.ack = "none" /\ s.server = "ready"
                          /\ s.primary = "blocked" /\ s.anchor
                          /\ ~s.firstReplyLost
                          /\ s' = [s EXCEPT !.ack = "unknown",
                                           !.firstReplyLost = TRUE,
                                           !.firstUncommittedLost = TRUE]
ReplayStartAck == /\ s.ack = "unknown" /\ s.firstReplyLost
                  /\ ~s.historyReplay
                  /\ s.committedOp = s.op
                  /\ (s.server # "done" \/ ~RefuseDoneReplay)
                  /\ (s.replayArgs = Bound \/ AllowChangedReplay)
                  /\ s' = [s EXCEPT !.historyReplay = TRUE,
                                   !.replayedAfterDone = (s.server = "done")]
(* Same-op replay is a historical receipt. Only an initial live reply or a
   fresh caller op re-planned against the current row can open the shell. *)
DeliverFirstAck == /\ s.ack = "unknown" /\ ~s.firstReplyLost
                   /\ s.server = "working"
                   /\ s.current = Bound /\ s.serverStarted /\ ~s.stopped
                   /\ s' = [s EXCEPT !.ack = "accepted",
                                    !.grantValid = TRUE, !.grantOp = s.op]
FreshRetryAck == /\ s.ack = "unknown" /\ s.firstReplyLost
                 /\ (SkipFreshPlan \/
                      (s.server \in {"ready", "working"} /\
                       s.current = Bound /\
                       ~s.paused /\ ~s.stopped))
                 /\ s' = [s EXCEPT !.ack = "accepted",
                                  !.server = IF s.server = "ready"
                                            THEN "working" ELSE @,
                                  !.serverStarted = IF s.server \in {"ready", "working"}
                                                     THEN TRUE ELSE @,
                                  !.committedOp = IF s.committedOp = "none"
                                                 THEN s.retryOp ELSE @,
                                  !.commitWasExact = IF s.committedOp = "none"
                                                    THEN (s.current = Bound) ELSE @,
                                  !.grantValid = (s.server \in {"ready", "working"} /\
                                                 s.current = Bound /\
                                                 ~s.paused /\ ~s.stopped),
                                  !.grantOp = s.retryOp,
                                  !.freshRetryUsed = TRUE]
FreshRetryRefuse == /\ s.ack = "unknown" /\ s.firstReplyLost
                    /\ (s.server \notin {"ready", "working"} \/
                         s.current # Bound \/
                         s.paused \/ s.stopped)
                    /\ s' = [s EXCEPT !.ack = "refused",
                                     !.freshRetryUsed = TRUE]
ChooseChangedReplay(t) == /\ t \in OtherTuples /\ s.ack = "unknown"
                          /\ s.replayArgs = Bound /\ ~s.historyReplay
                          /\ s' = [s EXCEPT !.replayArgs = t]
RejectChangedReplay == /\ s.ack = "unknown" /\ s.replayArgs # Bound
                       /\ ~s.replayConflict
                       /\ s' = [s EXCEPT !.replayConflict = TRUE]

(* External finish/take before a fresh retry causes that fresh op to refuse.
   A cached same-op result is historical receipt, never launch authority.
   Later revocation after a fresh grant is asynchronous and handled by the
   native cancellation/return path, not a distributed instantaneous lease. *)
ExternalFinish == /\ s.server = "working" /\ s.primary = "blocked"
                  /\ s' = [s EXCEPT !.server = "done",
                                   !.serverStarted = FALSE]
ExternalRedeal == /\ s.server = "working" /\ s.primary = "blocked"
                  /\ s' = [s EXCEPT !.server = "ready",
                                   !.current = [Bound EXCEPT !.gen = 2],
                                   !.serverStarted = FALSE,
                                   !.redealt = TRUE]
ExternalClearEpoch == /\ s.server = "working" /\ s.primary = "blocked"
                      /\ s' = [s EXCEPT !.server = "ready",
                                       !.serverStarted = FALSE,
                                       !.current = [Bound EXCEPT !.epoch = 2],
                                       !.cleared = TRUE]
TakeIntoWorking == /\ s.server = "ready" /\ s.current = Bound
                   /\ ~s.serverStarted
                   /\ s' = [s EXCEPT !.server = "working"]

(* A different generation/epoch/actor/card/job is a stale attempt, not a
   filename-derived authorization. Reassignment occurs only while Ready. *)
ChangeReadyTuple(t) == /\ t \in OtherTuples /\ s.server = "ready"
                       /\ s.current = Bound
                       /\ s' = [s EXCEPT !.current = t]
Pause == /\ ~s.paused /\ ~s.stopped /\ s.server # "done"
         /\ s' = [s EXCEPT !.paused = TRUE]
Resume == /\ s.paused /\ ~s.stopped
          /\ s' = [s EXCEPT !.paused = FALSE]
Stop == /\ ~s.stopped /\ s.server # "done"
        /\ s' = [s EXCEPT !.stopped = TRUE,
                         !.stopDebt = (s.server = "working" /\
                                      s.current = Bound /\
                                      s.serverStarted /\
                                      s.committedOp # "none")]

(* The gate opens only after an accepted server ACK and verified original
   anchor. A broken early-release switch is the launch-before-ACK witness.
   A PAUSE after ACK does not revoke already-current work; STOP closes it. *)
ReleaseGate == /\ s.primary = "blocked" /\ s.anchor /\ ~s.gate
               /\ ~s.stopped /\ ~s.crashed
               /\ (s.ack = "accepted" \/ ReleaseBeforeAck \/
                    (UseHistoricalReplay /\ s.historyReplay))
               /\ s' = [s EXCEPT !.gate = TRUE, !.primary = "live",
                                !.nativeStarted = TRUE,
                                !.historyUsedToLaunch = (s.ack # "accepted" /\
                                                         s.historyReplay)]

(* Cancellation or gate-release failure after ACK leaves server Working but
   no native work. It must settle as failed, or as stop-return under STOP. *)
FailBlockedGate == /\ s.primary = "blocked" /\ s.server = "working"
                   /\ s.current = Bound /\ s.serverStarted
                   /\ s.committedOp # "none"
                   /\ ~s.crashed
                   /\ s' = [s EXCEPT !.primary = "quiet",
                                    !.outcome = "failed", !.quietObserved = TRUE]
CrashBeforeRelease == /\ s.primary = "blocked" /\ s.anchor
                      /\ ~s.crashed
                      /\ s' = [s EXCEPT !.crashed = TRUE]
RecoverBlocked == /\ s.primary = "blocked" /\ s.crashed
                  /\ s.primaryReceipt = "primary" /\ s.anchorReceipt = "primary"
                  /\ s' = [s EXCEPT !.primary = "quiet",
                                   !.quietObserved = TRUE,
                                   !.outcome = IF s.server = "working" /\
                                                 s.current = Bound /\
                                                 s.serverStarted /\
                                                 s.committedOp # "none"
                                              THEN "failed" ELSE @]
AbortBlocked == /\ s.primary = "blocked" /\ (s.stopped \/ s.ack = "refused")
                /\ s.primaryReceipt = "primary" /\ s.anchorReceipt = "primary"
                /\ s' = [s EXCEPT !.primary = "quiet",
                                 !.quietObserved = TRUE]
NativeFinishes == /\ s.primary = "live" /\ s.nativeStarted
                  /\ s.outcome = "none"
                  /\ s' = [s EXCEPT !.outcome = "success"]
PrimaryQuiets == /\ s.primary = "live"
                 /\ s' = [s EXCEPT !.primary = "quiet"]
ObserveQuiet == /\ s.primary = "quiet" /\ ~s.quietObserved
                 /\ s.primaryReceipt = "primary" /\ s.anchorReceipt = "primary"
                 /\ s' = [s EXCEPT !.quietObserved = TRUE]
(* An overwritten primary receipt can falsely certify the export group. *)
ObserveWrongGroup == /\ OverwritePrimary /\ s.aux = "quiet"
                     /\ s.primaryReceipt = "aux" /\ s.anchorReceipt = "aux"
                     /\ ~s.quietObserved
                     /\ s' = [s EXCEPT !.quietObserved = TRUE]

Finish == /\ s.server = "working" /\ ~s.stopped
          /\ s.current = Bound /\ s.serverStarted
          /\ s.committedOp # "none"
          /\ s.outcome # "none" /\ s.quietObserved
          /\ (AuxSettled \/ ~GuardAuxRelease)
          /\ s' = [s EXCEPT !.server = "done", !.finished = TRUE]
StopReturn == /\ s.server = "working" /\ s.stopped /\ s.stopDebt
              /\ s.current = Bound /\ s.serverStarted
              /\ s.committedOp # "none"
              /\ s.quietObserved
              /\ (AuxSettled \/ ~GuardAuxRelease)
              /\ s' = [s EXCEPT !.server = "done",
                               !.stopReturned = TRUE, !.stopDebt = FALSE]
ReleaseCapacity == /\ s.server = "done" /\ s.quietObserved
                   /\ (s.finished \/ s.stopReturned)
                   /\ ~s.capacityReleased
                   /\ (AuxSettled \/ ~GuardAuxRelease)
                   /\ s' = [s EXCEPT !.capacityReleased = TRUE]

Next == \/ LaunchBlocked \/ StorePrimary \/ StartAnchor
        \/ LaunchAux \/ AuxQuiets \/ LoseAuxIdentity
        \/ UnknownAuxExits \/ VerifyAuxQuiet
        \/ CommitStartAck \/ RefuseStartAck \/ LoseFirstReply
        \/ LoseUncommittedRequest
        \/ ReplayStartAck
        \/ DeliverFirstAck \/ FreshRetryAck \/ FreshRetryRefuse
        \/ \E t \in OtherTuples : ChooseChangedReplay(t)
        \/ RejectChangedReplay \/ ExternalFinish \/ ExternalRedeal
        \/ ExternalClearEpoch \/ TakeIntoWorking
        \/ \E t \in OtherTuples : ChangeReadyTuple(t)
        \/ Pause \/ Resume \/ Stop \/ ReleaseGate \/ FailBlockedGate
        \/ CrashBeforeRelease \/ RecoverBlocked
        \/ AbortBlocked
        \/ NativeFinishes \/ PrimaryQuiets \/ ObserveQuiet
        \/ ObserveWrongGroup \/ Finish \/ StopReturn \/ ReleaseCapacity
Spec == Init /\ [][Next]_vars

TypeOK == /\ s.server \in {"ready", "working", "done"}
          /\ s.serverStarted \in BOOLEAN
          /\ s.current \in OtherTuples \cup {Bound}
          /\ s.primary \in {"none", "blocked", "live", "quiet"}
          /\ s.primaryReceipt \in {"none", "primary", "aux"}
          /\ s.aux \in {"none", "live", "quiet", "unknown"}
          /\ s.ack \in {"none", "unknown", "accepted", "refused"}
          /\ s.outcome \in {"none", "success", "failed"}
PrimaryReceiptStable == (s.primary # "none") =>
                        (s.primaryReceipt # "aux" /\ s.anchorReceipt # "aux")
NoNativeBeforeAck == s.nativeStarted =>
                     (s.committedOp \in {s.op, s.retryOp} /\
                      s.ack = "accepted")
NoGateWithoutAnchor == s.gate =>
                       (s.anchor /\ s.anchorReceipt = "primary")
NoHistoricalLaunch == ~s.historyUsedToLaunch
CurrentGrantWasValid == s.ack = "accepted" => s.grantValid
FreshRetryUsesNewOp == s.freshRetryUsed /\ s.ack = "accepted" =>
                       (s.grantOp = s.retryOp /\ s.retryOp # s.op)
NoChangedReplayAccepted == s.historyReplay => s.replayArgs = Bound
DoneReplayEnabled == (s.ack = "unknown" /\ s.firstReplyLost /\
                      ~s.historyReplay /\
                      s.server = "done" /\
                      s.committedOp = s.op /\
                      s.replayArgs = Bound) => ENABLED ReplayStartAck
NoCompletionWhileAuxLive == (s.finished \/ s.stopReturned) => AuxSettled
NoAuxCapacityRelease == s.capacityReleased => AuxSettled
NoWorkAfterPreReleaseCrash == s.crashed => ~s.nativeStarted
ExactCommittedStart == (s.committedOp # "none") => s.commitWasExact
NoStopNativeStart == (s.stopped /\ ~s.nativeStarted) => ~s.gate
NoEarlyRelease == s.capacityReleased =>
                  (s.primary = "quiet" /\ s.quietObserved /\ s.server = "done")
NoFalseFinish == s.finished =>
                 (s.primary = "quiet" /\ s.outcome # "none" /\ s.server = "done")
NoFalseStopReturn == s.stopReturned =>
                     (s.stopped /\ s.primary = "quiet" /\ s.server = "done")
NoLocalSettlementWithoutAck == (s.finished \/ s.stopReturned) =>
                               s.committedOp # "none"

(* Reachability witnesses are expected invariant failures in dedicated cases. *)
WitnessFastTurn == ~(s.finished /\ s.nativeStarted /\ s.server = "done")
WitnessLostAckReplay == ~(s.historyReplay /\ s.committedOp = s.op)
WitnessFreshAfterLost == ~(s.firstReplyLost /\ s.freshRetryUsed /\
                           s.ack = "accepted")
WitnessFreshAfterUncommittedLost == ~(s.firstUncommittedLost /\
                                      s.freshRetryUsed /\
                                      s.committedOp = s.retryOp /\
                                      s.ack = "accepted")
WitnessFreshRefusesAfterTake == ~(s.firstReplyLost /\ s.freshRetryUsed /\
                                   s.redealt /\ s.server = "ready" /\
                                   s.current # Bound /\ s.ack = "refused")
WitnessFreshRefusesAfterClear == ~(s.firstReplyLost /\ s.freshRetryUsed /\
                                    s.cleared /\ s.server = "ready" /\
                                    s.current # Bound /\ s.ack = "refused")
WitnessAckThenNoWork == ~(s.server = "working" /\ s.outcome = "failed"
                            /\ ~s.nativeStarted)
WitnessStaleRefused == ~(s.current # Bound /\ s.ack = "refused")
WitnessStopDebtSettled == ~(s.stopReturned /\ ~s.stopDebt)
WitnessCrashRecovered == ~(s.crashed /\ s.finished /\ ~s.nativeStarted)
WitnessReplayAfterFinish == ~(s.replayedAfterDone /\ s.historyReplay)
WitnessChangedReplayConflict == ~(s.replayConflict /\ s.replayArgs # Bound)
WitnessPauseBeforeAck == ~(s.server = "ready" /\ s.paused
                            /\ s.ack = "refused")
WitnessPauseAfterAck == ~(s.server = "working" /\ s.paused
                           /\ s.gate)
=============================================================================
