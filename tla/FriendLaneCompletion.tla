---------------------- MODULE FriendLaneCompletion ----------------------
(* One one-shot lane and its durable Started, frozen ending, REPORT, and
   finish-note journal. A returned leader is not group-exit proof. A crash
   preserves the durable claim/end/report/journal and loses the volatile slot;
   the replacement counts Started as reserved capacity until settlement.
   The native RESULT proves completion, not a LAND verdict. The report action
   abstracts EndReport's unchanged FAIL/HOLD policy as the frozen exit detail.
   This model does not represent arbitrary shell programs or sprint approval. *)
EXTENDS Naturals

CONSTANTS SafeReport, SafeRelease, SafeReplay

VARIABLES result, group, started, frozen, report, journal, slot, up,
          stateFail, reportFail, restarted, stopped
vars == <<result, group, started, frozen, report, journal, slot, up,
          stateFail, reportFail, restarted, stopped>>

Init == /\ result = FALSE /\ group = "live" /\ started = TRUE
        /\ frozen = "none" /\ report = "none" /\ journal = FALSE
        /\ slot = "held" /\ up = TRUE /\ stateFail = 0
        /\ reportFail = 0 /\ restarted = FALSE /\ stopped = FALSE

NativeResult == /\ up /\ ~stopped /\ ~result
                /\ result' = TRUE
                /\ UNCHANGED <<group, started, frozen, report, journal,
                                slot, up, stateFail, reportFail, restarted, stopped>>

FreezeWriteFails == /\ up /\ result /\ frozen = "none" /\ stateFail = 0
                    /\ stateFail' = 1
                    /\ UNCHANGED <<result, group, started, frozen, report,
                                    journal, slot, up, reportFail, restarted, stopped>>

Freeze == /\ up /\ result /\ started /\ frozen = "none"
          /\ frozen' = "exit7"
          /\ UNCHANGED <<result, group, started, report, journal, slot,
                          up, stateFail, reportFail, restarted, stopped>>

GroupExits == /\ group = "live" /\ group' = "quiet"
              /\ UNCHANGED <<result, started, frozen, report, journal,
                              slot, up, stateFail, reportFail, restarted, stopped>>

ReportWriteFails == /\ up /\ frozen # "none" /\ group = "quiet"
                    /\ report = "none" /\ reportFail = 0
                    /\ reportFail' = 1
                    /\ UNCHANGED <<result, group, started, frozen, report,
                                    journal, slot, up, stateFail, restarted, stopped>>

WriteReport == /\ up /\ frozen # "none" /\ report = "none"
               /\ (group = "quiet" \/ ~SafeReport)
               /\ report' = frozen
               /\ UNCHANGED <<result, group, started, frozen, journal,
                               slot, up, stateFail, reportFail, restarted, stopped>>

Journal == /\ up /\ report # "none" /\ ~journal
           /\ journal' = TRUE
           /\ UNCHANGED <<result, group, started, frozen, report, slot,
                           up, stateFail, reportFail, restarted, stopped>>

ClearStarted == /\ up /\ started /\ (journal \/ ~SafeRelease)
                /\ started' = FALSE
                /\ UNCHANGED <<result, group, frozen, report, journal,
                                slot, up, stateFail, reportFail, restarted, stopped>>

ReleaseLane == /\ up /\ ~started /\ slot = "held"
               /\ slot' = "free"
               /\ UNCHANGED <<result, group, started, frozen, report,
                               journal, up, stateFail, reportFail, restarted, stopped>>

Crash == /\ up /\ up' = FALSE
         /\ UNCHANGED <<result, group, started, frozen, report, journal,
                         slot, stateFail, reportFail, restarted, stopped>>

Restart == /\ ~up /\ up' = TRUE /\ restarted' = TRUE
           /\ frozen' = IF ~SafeReplay /\ result /\ report = "none"
                         THEN "gone" ELSE frozen
           /\ UNCHANGED <<result, group, started, report, journal, slot,
                           stateFail, reportFail, stopped>>

Stop == /\ up /\ ~stopped /\ stopped' = TRUE
        /\ UNCHANGED <<result, group, started, frozen, report, journal,
                        slot, up, stateFail, reportFail, restarted>>

Next == \/ NativeResult \/ FreezeWriteFails \/ Freeze \/ GroupExits
        \/ ReportWriteFails \/ WriteReport \/ Journal \/ ClearStarted
        \/ ReleaseLane \/ Crash \/ Restart \/ Stop
SafetySpec == Init /\ [][Next]_vars

(* Bounded reachable recovery: state and REPORT writes each fail once; the
   daemon crashes, replays the same end, waits for group exit, journals the
   note, clears Started and releases capacity. Fairness applies only to this
   constrained trace, not an unavailable disk or a group that never exits. *)
RetryNext == \/ NativeResult
             \/ (result /\ FreezeWriteFails)
             \/ (stateFail = 1 /\ Freeze)
             \/ (frozen # "none" /\ ~restarted /\ Crash)
             \/ Restart
             \/ (restarted /\ GroupExits)
             \/ (group = "quiet" /\ ReportWriteFails)
             \/ (reportFail = 1 /\ WriteReport)
             \/ Journal \/ ClearStarted \/ ReleaseLane
RetrySpec == Init /\ [][RetryNext]_vars /\ WF_vars(RetryNext)

(* A completed and quiescent native result remains completion after the
   machine's STOP word. It is not turned into a stop-return on the live path. *)
StopNext == \/ NativeResult \/ Freeze \/ GroupExits
            \/ (frozen # "none" /\ group = "quiet" /\ Stop)
            \/ (stopped /\ WriteReport)
            \/ Journal \/ ClearStarted \/ ReleaseLane
StopSpec == Init /\ [][StopNext]_vars /\ WF_vars(StopNext)

TypeOK == /\ result \in BOOLEAN /\ group \in {"live", "quiet"}
          /\ started \in BOOLEAN /\ frozen \in {"none", "exit7", "gone"}
          /\ report \in {"none", "exit7", "gone"} /\ journal \in BOOLEAN
          /\ slot \in {"held", "free"} /\ up \in BOOLEAN
          /\ stateFail \in 0..1 /\ reportFail \in 0..1
          /\ restarted \in BOOLEAN /\ stopped \in BOOLEAN
NoLostCompletion == result => (started \/ (report # "none" /\ journal))
ReportQuiescent == report = "none" \/ group = "quiet"
ReportMatchesNative == report = "none" \/ report = "exit7"
NoPrematureRelease == slot = "free" => (~started /\ report # "none" /\ journal)
EventuallyReleased == <> (slot = "free" /\ journal)
StoppedCompletionSettles == <> (stopped /\ slot = "free" /\ report = "exit7")
=========================================================================
