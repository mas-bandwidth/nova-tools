------------------------- MODULE TokenCapRecovery -------------------------
(* One card/session, two representative token buckets, one restart. Polling
   observes cumulative usage; spend can exceed cap before a poll. No hard bound. *)
EXTENDS Naturals
CONSTANTS PositiveCap, InitialKnown, ZeroBaseline, LegacyStarted, Broken
VARIABLE s
vars == <<s>>
InitialBase == IF ZeroBaseline THEN [input |-> 0, output |-> 0] ELSE [input |-> 1, output |-> 1]
Total(t) == t.input + t.output
AtLeast(t,b) == t.input >= b.input /\ t.output >= b.output
Init == s = [up |-> ~LegacyStarted, crashed |-> LegacyStarted, adopted |-> FALSE,
             stopped |-> FALSE, claimed |-> LegacyStarted, admissionHeld |-> FALSE,
             base |-> InitialBase, baseKnown |-> InitialKnown,
             saved |-> LegacyStarted, savedBase |-> InitialBase,
             savedKnown |-> (InitialKnown /\ ~LegacyStarted), savedSession |-> "session",
             current |-> InitialBase, currentKnown |-> InitialKnown, session |-> "session",
             run |-> IF LegacyStarted THEN "live" ELSE "none", primaryQuiet |-> FALSE,
             aux |-> "none", report |-> "none", workerReport |-> "none",
             held |-> FALSE, cancel |-> FALSE, finalChecked |-> FALSE,
             settled |-> FALSE, acked |-> FALSE, released |-> FALSE,
             saveFailed |-> FALSE, badClaim |-> FALSE, badObservation |-> FALSE,
             badSettlement |-> FALSE, observedFault |-> FALSE,
             baselineReset |-> FALSE, faultOrigin |-> "none", faultKind |-> "none",
             stoppedAtFinal |-> FALSE, restoredCapChecked |-> FALSE, faultAfterAdopt |-> FALSE]

RefuseBaseline == /\ s.up /\ ~s.claimed /\ PositiveCap /\ ~s.currentKnown
                  /\ ~s.admissionHeld
                  /\ s' = [s EXCEPT !.admissionHeld = TRUE]
Claim == /\ s.up /\ ~s.claimed /\ ~s.released /\ ~s.stopped
         /\ (~PositiveCap \/ s.currentKnown \/ Broken = "unknown-claim")
         /\ s' = [s EXCEPT !.claimed = TRUE, !.base = s.current,
                          !.baseKnown = s.currentKnown,
                          !.badClaim = (PositiveCap /\ ~s.currentKnown)]
SaveStarted == /\ s.up /\ s.claimed /\ ~s.saved
               /\ s' = [s EXCEPT !.saved = TRUE, !.savedBase = s.base,
                                !.savedKnown = s.baseKnown, !.savedSession = s.session]
FailStartedSave == /\ s.up /\ s.claimed /\ ~s.saved /\ ~s.saveFailed
                   /\ s' = [s EXCEPT !.saveFailed = TRUE, !.claimed = FALSE]
Launch == /\ s.up /\ s.claimed /\ s.saved /\ s.run = "none" /\ ~s.stopped
          /\ ~s.held /\ (~PositiveCap \/ s.savedKnown \/ Broken = "unknown-claim")
          /\ s' = [s EXCEPT !.run = "live"]
Spend == /\ s.run = "live" /\ s.currentKnown /\ Total(s.current) < 6
         /\ s' = [s EXCEPT !.current.output = @ + 1, !.finalChecked = FALSE]
LoseUsage == /\ s.run \in {"live", "quiet"} /\ s.currentKnown
             /\ s' = [s EXCEPT !.currentKnown = FALSE, !.finalChecked = FALSE]
RegressBucket == /\ s.run \in {"live", "quiet"} /\ s.currentKnown
                 /\ s.current.input > 0
                 /\ s' = [s EXCEPT !.current.input = 0, !.current.output = 3,
                                  !.finalChecked = FALSE]
ChangeSession == /\ s.saved /\ s.session = "session"
                 /\ s' = [s EXCEPT !.session = "foreign", !.finalChecked = FALSE]
WorkerReport(r) == /\ s.run = "live" /\ s.report = "none" /\ r \in {"hold", "land"}
                  /\ s' = [s EXCEPT !.report = r, !.workerReport = r]
SpawnAux == /\ s.run = "live" /\ s.aux = "none"
            /\ s' = [s EXCEPT !.aux = "unknown"]
ProveAuxQuiet == /\ s.aux = "unknown"
                 /\ s' = [s EXCEPT !.aux = "quiet"]
Quiet == s.run = "quiet" /\ s.primaryQuiet /\ s.aux \in {"none", "quiet"}
NativeExit == /\ s.run = "live"
              /\ s' = [s EXCEPT !.run = "quiet"]
ProvePrimaryQuiet == /\ s.run = "quiet" /\ ~s.primaryQuiet
                     /\ s' = [s EXCEPT !.primaryQuiet = TRUE]
Stop == /\ ~s.stopped
        /\ s' = [s EXCEPT !.stopped = TRUE, !.cancel = (s.cancel \/ s.run = "live")]
ActualFault == PositiveCap /\ (~s.savedKnown \/ ~s.currentKnown \/
                s.session # s.savedSession \/ ~AtLeast(s.current,s.savedBase) \/
                (AtLeast(s.current,s.savedBase) /\ Total(s.current)-Total(s.savedBase) >= 2))
ObservedFault(base,known) == PositiveCap /\ (~known \/ ~s.currentKnown \/
                  s.session # s.savedSession \/
                  (~AtLeast(s.current,base) /\ Broken # "ignore-regression") \/
                  (AtLeast(s.current,base) /\ Total(s.current)-Total(base) >= 2))
ApplyCheck(final) ==
    /\ s.up /\ s.saved /\ ~s.settled
    /\ IF final THEN s.run = "quiet" /\ s.report # "hold"
       ELSE s.run = "live" /\ (~s.crashed \/ s.adopted)
    /\ LET base == IF final THEN s.savedBase ELSE s.base
           known == IF final THEN s.savedKnown ELSE s.baseKnown
           fault == ObservedFault(base,known) /\ ~(Broken = "ignore-missing" /\ ~s.currentKnown)
           kind == CASE ~known -> "baseline"
                        [] ~s.currentKnown -> "missing"
                        [] s.session # s.savedSession -> "session"
                        [] ~AtLeast(s.current,base) -> "regression"
                        [] OTHER -> "overcap"
           report == IF fault /\ (s.report = "none" \/ Broken = "overwrite-report") THEN "hold" ELSE s.report
       IN s' = [s EXCEPT !.held = (s.held \/ fault),
                        !.cancel = (s.cancel \/ (fault /\ s.run = "live")),
                        !.report = report, !.finalChecked = (s.finalChecked \/ final),
                        !.badObservation = (s.badObservation \/ (ActualFault /\ ~fault /\ ~s.held)),
                        !.observedFault = (s.observedFault \/ fault),
                        !.faultKind = IF fault THEN kind ELSE s.faultKind,
                        !.faultAfterAdopt = (s.faultAfterAdopt \/ (s.adopted /\ fault)),
                        !.stoppedAtFinal = (s.stoppedAtFinal \/ (final /\ s.stopped /\ fault)),
                        !.restoredCapChecked = (s.restoredCapChecked \/
                           (fault /\ s.adopted /\ kind = "overcap")),
                        !.faultOrigin = IF fault THEN IF final THEN "final" ELSE "poll" ELSE s.faultOrigin]
Poll == ~s.held /\ ApplyCheck(FALSE)
CheckExistingHold == /\ s.up /\ s.saved /\ ~s.settled
                     /\ s.run = "quiet" /\ s.report = "hold"
                     /\ s' = [s EXCEPT !.finalChecked = TRUE]
FinalCheck == ApplyCheck(TRUE) \/ CheckExistingHold
Settle == /\ s.up /\ s.saved /\ ~s.settled /\ Quiet
          /\ (s.finalChecked \/ Broken = "skip-final")
          /\ ((~s.held /\ ~ActualFault) \/ s.report = "hold" \/ Broken = "skip-final")
          /\ s' = [s EXCEPT !.settled = TRUE,
                           !.badSettlement = (ActualFault /\ s.report # "hold")]
AckSettlement == /\ s.settled /\ ~s.acked
                 /\ s' = [s EXCEPT !.acked = TRUE]
Release == /\ s.saved /\ ~s.released
           /\ ((Quiet /\ s.acked) \/ Broken = "early-release")
           /\ s' = [s EXCEPT !.released = TRUE, !.claimed = FALSE, !.saved = FALSE]
Crash == /\ s.up /\ ~s.crashed /\ s.saved /\ ~s.released
         /\ s' = [s EXCEPT !.up = FALSE, !.crashed = TRUE, !.baseKnown = FALSE,
                          !.base = [input |-> 0, output |-> 0], !.finalChecked = FALSE]
Restart == /\ ~s.up /\ s.crashed
           /\ s' = [s EXCEPT !.up = TRUE]
Adopt == /\ s.up /\ s.crashed /\ ~s.adopted /\ s.saved
         /\ s' = [s EXCEPT !.base = IF Broken = "reset-adopt" THEN s.current ELSE s.savedBase,
                          !.baseKnown = IF Broken = "reset-adopt" THEN s.currentKnown
                                       ELSE s.savedKnown /\ s.session = s.savedSession,
                          !.baselineReset = (Broken = "reset-adopt" /\ s.current # s.savedBase),
                          !.adopted = TRUE]
Next == RefuseBaseline \/ Claim \/ SaveStarted \/ FailStartedSave \/ Launch \/ Spend \/
        LoseUsage \/ RegressBucket \/ ChangeSession \/ SpawnAux \/ ProveAuxQuiet \/
        NativeExit \/ ProvePrimaryQuiet \/ Stop \/ Poll \/ FinalCheck \/ Settle \/
        AckSettlement \/ Release \/ Crash \/ Restart \/ Adopt \/
        (\E r \in {"hold", "land"} : WorkerReport(r))
Spec == Init /\ [][Next]_vars
TypeOK == /\ s.run \in {"none", "live", "quiet"}
          /\ s.aux \in {"none", "unknown", "quiet"}
          /\ s.report \in {"none", "hold", "land"}
          /\ s.current.input \in 0..1 /\ s.current.output \in 0..6
NoUnknownClaim == ~s.badClaim
BaselinePreserved == ~s.baselineReset
FaultStops == ~s.badObservation
NoUnsafeSettlement == ~s.badSettlement
ReportPreserved == s.workerReport # "none" => s.report = s.workerReport
CapacityAfterQuiet == s.released => Quiet /\ s.acked
WitnessKnownZero == ~(ZeroBaseline /\ s.savedKnown /\ s.run = "live")
WitnessUnknownRefusal == ~(s.admissionHeld /\ ~s.claimed /\ ~s.saved /\ s.report = "none")
WitnessRestartSpendHeld == ~(s.adopted /\ s.restoredCapChecked /\ s.held /\ s.currentKnown /\
    AtLeast(s.current,s.savedBase) /\ Total(s.current)-Total(s.savedBase) >= 2)
WitnessMissingLiveDebt == ~(s.held /\ s.faultKind = "missing" /\ ~s.currentKnown /\ s.run = "live" /\ s.saved /\ ~s.released)
WitnessRegressingHeld == ~(s.held /\ s.faultKind = "regression" /\ s.currentKnown /\ ~AtLeast(s.current,s.savedBase))
WitnessNonHoldRetained == ~(s.report = "land" /\ s.held /\ Quiet /\ s.saved /\ ~s.settled)
WitnessStopFinalHold == ~(s.stoppedAtFinal /\ s.report = "hold" /\ s.faultOrigin = "final" /\ s.settled)
WitnessUncappedWatch == ~(~PositiveCap /\ ~s.currentKnown /\ s.run = "live" /\ ~s.held)
WitnessSoftOvershoot == ~(PositiveCap /\ s.run = "live" /\ ~s.cancel /\ ~s.held /\
    AtLeast(s.current,s.savedBase) /\ Total(s.current)-Total(s.savedBase) > 2)
WitnessSyntheticRecoveryHold == ~(s.crashed /\ ~s.adopted /\ s.report = "hold" /\ s.settled)
WitnessLegacyRecoveryHold == ~(LegacyStarted /\ s.adopted /\ s.faultAfterAdopt /\ s.report = "hold" /\ s.settled)
=============================================================================
