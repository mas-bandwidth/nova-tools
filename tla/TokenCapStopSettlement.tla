---------------------- MODULE TokenCapStopSettlement ----------------------
EXTENDS Naturals
CONSTANT Broken
VARIABLE s
vars == <<s>>

\* One already-owned positive-cap DSH work card. UsageUnsafe is a projection
\* oracle, not a parser, clock, or spend ceiling. The worker can leave a report
\* after STOP has made a durable return debt for the still-live native turn.
Init == s = [up |-> TRUE, crashed |-> FALSE, restarted |-> FALSE,
             mode |-> "running", started |-> TRUE, reservation |-> TRUE,
             memory |-> TRUE, turnReturned |-> FALSE, primary |-> "live",
             primaryProof |-> FALSE, aux |-> "unknown", auxProof |-> FALSE,
             report |-> "none", workerReport |-> "none", unsafe |-> FALSE,
             usageHeld |-> FALSE, observed |-> FALSE, finalOnlyFault |-> FALSE, cancel |-> FALSE,
             owed |-> FALSE, ended |-> FALSE, ack |-> FALSE, debtCleared |-> FALSE,
             published |-> "none", liveDrop |-> FALSE, unsafeDrop |-> FALSE,
             checkedAfterRestart |-> FALSE, releaseCalled |-> FALSE]

Quiet == s.primary = "quiet" /\ s.primaryProof /\ s.aux = "quiet" /\ s.auxProof
UnsafeNonHold == s.unsafe /\ s.report = "land"
CapacityHeld == s.started \/ s.reservation

Stop == /\ s.up /\ s.mode = "running"
        /\ s' = [s EXCEPT !.mode = "stopped", !.cancel = TRUE,
                         !.owed = (s.owed \/ s.report = "none")]
WorkerReport(r) == /\ s.up /\ s.primary = "live" /\ s.report = "none"
                   /\ r \in {"land", "hold"}
                   /\ s' = [s EXCEPT !.report = r, !.workerReport = r]
UsageFault == /\ s.up /\ s.started /\ ~s.observed
              /\ s' = [s EXCEPT !.unsafe = TRUE, !.usageHeld = TRUE,
                               !.observed = TRUE, !.cancel = TRUE,
                               !.report = IF s.report = "none" THEN "hold" ELSE s.report]
FinalUsageFault == /\ s.up /\ s.started /\ Quiet /\ ~s.observed
                   /\ s.report = "land"
                   /\ s' = [s EXCEPT !.unsafe = TRUE, !.observed = TRUE, !.finalOnlyFault = TRUE]
TurnReturns == /\ s.up /\ s.memory /\ ~s.turnReturned
               /\ s' = [s EXCEPT !.turnReturned = TRUE]
PrimaryExits == /\ s.primary = "live"
                /\ s' = [s EXCEPT !.primary = "quiet"]
AuxExits == /\ s.aux = "unknown"
            /\ s' = [s EXCEPT !.aux = "quiet"]
ProvePrimary == /\ s.up /\ s.primary = "quiet" /\ ~s.primaryProof
                /\ s' = [s EXCEPT !.primaryProof = TRUE]
ProveAux == /\ s.up /\ s.aux = "quiet" /\ ~s.auxProof
            /\ s' = [s EXCEPT !.auxProof = TRUE]

\* stopStep's idle lane path invokes releaseHeld only when the live token HOLD
\* is compatible with the existing report. This drops memory, not Started.
ReleaseHeld == /\ s.up /\ s.mode = "stopped" /\ s.memory /\ s.turnReturned
               /\ (~(s.usageHeld /\ s.report # "hold") \/ Broken \in {"live", "both"})
               /\ s' = [s EXCEPT !.memory = FALSE, !.reservation = FALSE,
                                !.owed = TRUE, !.releaseCalled = TRUE,
                                !.liveDrop = (s.liveDrop \/ (s.usageHeld /\ s.report # "hold"))]

\* Native quiet can make the return Ended even while unsafe REPORT keeps the
\* canonical Started fence. This is not permission to ACK or publish it.
MarkEnded == /\ s.up /\ s.owed /\ s.started /\ Quiet /\ ~s.ended
             /\ s' = [s EXCEPT !.ended = TRUE]
RejectUnsafeSettlement == /\ s.up /\ s.owed /\ s.started /\ Quiet
                          /\ UnsafeNonHold /\ ~s.checkedAfterRestart /\ s.restarted
                          /\ s' = [s EXCEPT !.checkedAfterRestart = TRUE]
SettleStopDebt == /\ s.up /\ s.owed /\ s.started /\ Quiet /\ s.ended
                  /\ (~UnsafeNonHold \/ Broken \in {"settle", "both"})
                  /\ s' = [s EXCEPT !.started = FALSE,
                                   !.unsafeDrop = (s.unsafeDrop \/ UnsafeNonHold)]
ReturnAck == /\ s.up /\ s.owed /\ s.ended /\ ~s.ack
             /\ (~s.started \/ Broken = "ack")
             /\ s' = [s EXCEPT !.ack = TRUE]
ClearAckDebt == /\ s.up /\ s.ack /\ s.owed
                /\ s' = [s EXCEPT !.owed = FALSE, !.debtCleared = TRUE]

\* A cached holder row still says Working: Started is the independent local
\* outbox exclusion. Authority freshness/branch/head checks are outside scope.
Outbox == /\ s.up /\ s.report # "none" /\ s.published = "none"
          /\ Quiet /\ s.turnReturned
          /\ (~s.started \/ Broken = "outbox")
          /\ s' = [s EXCEPT !.published = s.report]
ReleaseReservation == /\ s.up /\ s.reservation /\ ~s.started /\ Quiet /\ s.ack
                      /\ s' = [s EXCEPT !.reservation = FALSE, !.memory = FALSE]
Crash == /\ s.up /\ ~s.crashed /\ s.started
         /\ s' = [s EXCEPT !.up = FALSE, !.crashed = TRUE,
                          !.memory = FALSE, !.reservation = FALSE,
                          !.usageHeld = FALSE, !.primaryProof = FALSE, !.auxProof = FALSE]
Restart == /\ ~s.up /\ s.crashed
           /\ s' = [s EXCEPT !.up = TRUE, !.restarted = TRUE]

Next == Stop \/ UsageFault \/ FinalUsageFault \/ TurnReturns \/ PrimaryExits \/ AuxExits \/
        ProvePrimary \/ ProveAux \/ ReleaseHeld \/ MarkEnded \/
        RejectUnsafeSettlement \/ SettleStopDebt \/ ReturnAck \/ ClearAckDebt \/
        Outbox \/ ReleaseReservation \/ Crash \/ Restart \/
        (\E r \in {"land", "hold"} : WorkerReport(r))
Spec == Init /\ [][Next]_vars
TypeOK == /\ s.mode \in {"running", "stopped"}
          /\ s.primary \in {"live", "quiet"} /\ s.aux \in {"unknown", "quiet"}
          /\ s.report \in {"none", "land", "hold"}
          /\ s.published \in {"none", "land", "hold"}
          /\ s.workerReport \in {"none", "land", "hold"}
NoUnsafeStartedRemoval == ~s.unsafeDrop
NoLiveHoldRelease == ~s.liveDrop
NoUnsafeAck == UnsafeNonHold => ~s.ack
NoUnsafePublication == UnsafeNonHold => s.published # "land"
ReportPreserved == s.workerReport # "none" => s.report = s.workerReport
UnsafeDebtKeepsCapacity == UnsafeNonHold => CapacityHeld

WitnessLiveHoldRetained == ~(s.mode = "stopped" /\ s.turnReturned /\ s.usageHeld /\
    UnsafeNonHold /\ s.memory /\ s.reservation /\ s.started /\ Quiet)
WitnessRestoredEndedRetained == ~(s.restarted /\ s.checkedAfterRestart /\ UnsafeNonHold /\
    s.started /\ s.owed /\ s.ended /\ ~s.ack /\ ~s.debtCleared /\ s.published = "none")
WitnessHoldStopDrain == ~(s.mode = "stopped" /\ s.observed /\ s.report = "hold" /\
    s.ack /\ s.debtCleared /\ ~CapacityHeld /\ s.published = "hold")
WitnessSafeStopDrain == ~(s.mode = "stopped" /\ ~s.observed /\ s.report = "land" /\
    s.ack /\ s.debtCleared /\ ~CapacityHeld /\ s.published = "land")
WitnessEndedBeforeAuxProof == ~(s.owed /\ s.primaryProof /\ ~s.auxProof /\ ~s.ended /\ s.started)
WitnessFinalOnlyHeld == ~(s.finalOnlyFault /\ UnsafeNonHold /\ ~s.usageHeld /\
    s.releaseCalled /\ s.ended /\ s.started /\ s.owed /\ ~s.ack)
WitnessBaselineBypassPublished == ~(s.releaseCalled /\ s.liveDrop /\ s.unsafeDrop /\ UnsafeNonHold /\
    s.ack /\ s.debtCleared /\ s.published = "land")
=============================================================================
