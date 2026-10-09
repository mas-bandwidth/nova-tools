------------------------ MODULE ControlSessionRecovery ------------------------
(* One durable lane open intent, one aggregate primary/inspection debt, and at
   most two attempts. Quiet is qualified process-group proof, never a timeout.
   NativeSeedStart retains its primary RUN until journal-free verified settlement. *)
EXTENDS Naturals
CONSTANT Fault
VARIABLE s
vars == <<s>>
Quiet(x) == x \in {"none", "absent", "quiet"}
GroupsQuiet == Quiet(s.primary) /\ Quiet(s.aux)
ControlDebt == s.intent \/ ~GroupsQuiet
Available == s.owner /\ s.loaded /\ s.busy = "none"
ValidID == s.savedID = "sid"
Init == s = [owner |-> TRUE, loaded |-> TRUE, mode |-> "oneshot", harness |-> "opencode",
    intent |-> FALSE, openArmed |-> FALSE, job |-> 0, savedID |-> "none", bound |-> FALSE,
    primary |-> "absent", aux |-> "none", busy |-> "none",
    returned |-> FALSE, returnedID |-> "none", resultJob |-> 0,
    calls |-> 0, seeds |-> 0, inspections |-> 0, message |-> FALSE,
    batch |-> FALSE, crashed |-> FALSE, unknownSeen |-> FALSE,
    bad |-> {}, seen |-> {}]

SaveIntent == Available /\ ~s.intent /\ ~s.bound /\ GroupsQuiet
    /\ s.calls < 2 /\ s.job < 2
    /\ s' = [s EXCEPT !.intent = TRUE, !.openArmed = TRUE, !.job = @ + 1,
                     !.savedID = "none"]
BeginOpen == Available /\ s.harness = "opencode" /\ s.intent /\ s.savedID = "none"
    /\ s.openArmed /\ s.primary = "absent" /\ Quiet(s.aux) /\ s.calls < 2
    /\ s' = [s EXCEPT !.busy = "open", !.openArmed = FALSE, !.calls = @ + 1,
                     !.returned = FALSE, !.returnedID = "none"]
NativeSeedStart == s.owner /\ s.busy = "open" /\ ~s.returned
    /\ s.intent /\ s.primary = "absent" /\ Quiet(s.aux) /\ s.seeds = 0
    /\ s' = [s EXCEPT !.primary = "live", !.seeds = @ + 1]
BeginInspection == s.owner /\ s.busy \in {"open", "message"}
    /\ ~s.returned /\ s.inspections < 1 /\ Quiet(s.aux)
    /\ s' = [s EXCEPT !.aux = "live", !.inspections = @ + 1]
ReturnKnownID == s.owner /\ s.busy = "open" /\ ~s.returned /\ s.seeds = 1
    /\ s' = [s EXCEPT !.returned = TRUE, !.returnedID = "sid",
        !.resultJob = s.job, !.seen = @ \cup {"id-with-error"}]
ReturnNoID == s.owner /\ s.busy = "open" /\ ~s.returned
    /\ s' = [s EXCEPT !.returned = TRUE, !.returnedID = "none",
                     !.resultJob = s.job]
ReturnWrongJob == s.owner /\ s.busy = "open" /\ ~s.returned /\ s.seeds = 1
    /\ s' = [s EXCEPT !.returned = TRUE, !.returnedID = "sid",
                     !.resultJob = 0]
SaveReturnedID == s.owner /\ s.loaded /\ s.intent /\ s.returned
    /\ s.returnedID = "sid" /\ s.resultJob = s.job
    /\ s' = [s EXCEPT !.savedID = "sid"]
SaveFailure == s.owner /\ s.loaded /\ s.intent
    /\ ((s.returned /\ s.returnedID = "sid") \/ ValidID)
    /\ s' = [s EXCEPT !.seen = @ \cup {"save-failed"}]
VerifyPrimaryQuiet == s.primary \in {"live", "unknown"}
    /\ s' = [s EXCEPT !.primary = "quiet"]
VerifyAuxQuiet == s.aux \in {"live", "unknown"}
    /\ s' = [s EXCEPT !.aux = "quiet"]
ReceiptUnknown == ~s.unknownSeen /\ s.aux \in {"live", "quiet"}
    /\ s' = [s EXCEPT !.aux = "unknown", !.unknownSeen = TRUE]
PrimaryReceiptUnknown == ~s.unknownSeen /\ s.primary \in {"live", "quiet"}
    /\ s' = [s EXCEPT !.primary = "unknown", !.unknownSeen = TRUE]
CollectSettledReceipts == Available /\ ~s.intent /\ GroupsQuiet
    /\ s' = [s EXCEPT !.primary = "absent", !.aux = "none"]
BindSession == s.owner /\ s.loaded /\ s.harness = "opencode"
    /\ s.intent /\ ValidID /\ GroupsQuiet
    /\ s' = [s EXCEPT !.bound = TRUE, !.intent = FALSE, !.openArmed = FALSE,
        !.savedID = "none", !.busy = "none", !.returned = FALSE,
        !.returnedID = "none", !.seen = IF s.crashed
            THEN @ \cup {"adopted"} ELSE @ \cup {"bound"}]
ClearNeverSeeded == s.owner /\ s.loaded /\ s.harness = "opencode" /\ s.intent /\ s.savedID = "none"
    /\ s.primary = "absent" /\ Quiet(s.aux)
    /\ (s.returned \/ s.busy = "none")
    /\ s' = [s EXCEPT !.intent = FALSE, !.openArmed = FALSE, !.busy = "none",
        !.returned = FALSE, !.returnedID = "none",
        !.seen = @ \cup {"safe-clear"}]
Crash == s.owner /\ ~s.crashed
    /\ s' = [s EXCEPT !.owner = FALSE, !.loaded = FALSE, !.openArmed = FALSE,
        !.busy = "none", !.returned = FALSE, !.returnedID = "none",
        !.resultJob = 0, !.crashed = TRUE]
Restart == ~s.owner /\ s' = [s EXCEPT !.owner = TRUE]
SwitchHarness == s.owner /\ ~s.loaded /\ s.intent /\ s.harness = "opencode"
    /\ s' = [s EXCEPT !.harness = "other"]
LoadKnown == s.owner /\ ~s.loaded /\ s' = [s EXCEPT !.loaded = TRUE]
LoadError == s.owner /\ ~s.loaded
    /\ s' = [s EXCEPT !.seen = @ \cup {"load-error"}]
LoadMalformedID == s.owner /\ ~s.loaded /\ ValidID
    /\ s' = [s EXCEPT !.savedID = "malformed", !.loaded = TRUE,
                     !.seen = @ \cup {"malformed"}]
SelectBatch == s.mode = "oneshot" /\ s' = [s EXCEPT !.mode = "batch"]
ObserveAdmissionHold == Available /\ ControlDebt
    /\ s' = [s EXCEPT !.seen = @ \cup {IF s.mode = "batch"
          THEN "batch-held" ELSE "control-held"}]
BeginBatch == Available /\ s.mode = "batch" /\ ~ControlDebt /\ ~s.batch
    /\ s' = [s EXCEPT !.batch = TRUE]
BeginMessage == Available /\ s.bound /\ ~ControlDebt /\ ~s.message
    /\ s' = [s EXCEPT !.message = TRUE, !.busy = "message",
        !.primary = "live", !.aux = "none", !.inspections = 0,
        !.returned = FALSE]
ReturnMessage == s.owner /\ s.busy = "message" /\ ~s.returned
    /\ s' = [s EXCEPT !.returned = TRUE]
SettleMessage == s.owner /\ s.busy = "message" /\ s.returned /\ GroupsQuiet
    /\ s' = [s EXCEPT !.busy = "none", !.returned = FALSE,
                     !.seen = @ \cup {"message-drained"}]

(* Reversed source boundaries, enabled only in their diagnostic cases. *)
BadDiscardID == Fault = "discard-id" /\ s.returned /\ s.returnedID = "sid"
    /\ s.resultJob = s.job /\ s.savedID = "none"
    /\ s' = [s EXCEPT !.returnedID = "none", !.bad = @ \cup {"lost-id"}]
BadRepeatSeed == Fault = "repeat-seed" /\ Available /\ s.intent
    /\ s.savedID = "none" /\ s.primary = "quiet" /\ s.seeds = 1
    /\ s' = [s EXCEPT !.seeds = 2, !.bad = @ \cup {"repeat"}]
BadEarlyBind == Fault = "early-bind" /\ s.owner /\ s.loaded
    /\ s.intent /\ ValidID /\ ~GroupsQuiet
    /\ s' = [s EXCEPT !.bound = TRUE, !.intent = FALSE,
                     !.bad = @ \cup {"release"}]
BadClearRUN == Fault = "clear-run" /\ Available /\ s.intent
    /\ s.savedID = "none" /\ s.primary # "absent"
    /\ s' = [s EXCEPT !.intent = FALSE, !.bad = @ \cup {"clear"}]
BadMessage == Fault = "message-debt" /\ Available /\ s.bound /\ ControlDebt
    /\ s' = [s EXCEPT !.bad = @ \cup {"admission"}]
BadBatch == Fault = "batch-debt" /\ Available /\ s.mode = "batch" /\ ControlDebt
    /\ s' = [s EXCEPT !.batch = TRUE, !.bad = @ \cup {"admission"}]
BadUnjournaledSeed == Fault = "unjournaled" /\ Available /\ ~s.intent
    /\ ~s.bound /\ s.seeds = 0
    /\ s' = [s EXCEPT !.seeds = 1, !.primary = "live",
                     !.bad = @ \cup {"journal"}]
BadWrongJob == Fault = "wrong-job" /\ s.intent /\ s.returnedID = "sid"
    /\ s.resultJob # s.job
    /\ s' = [s EXCEPT !.savedID = "sid", !.bad = @ \cup {"identity"}]
BadMalformedBind == Fault = "malformed-id" /\ s.owner /\ s.loaded
    /\ s.intent /\ s.savedID = "malformed" /\ GroupsQuiet
    /\ s' = [s EXCEPT !.bound = TRUE, !.intent = FALSE,
                     !.bad = @ \cup {"identity"}]
BadHarnessBind == Fault = "harness-mismatch" /\ s.owner /\ s.loaded
    /\ s.harness = "other" /\ s.intent /\ ValidID /\ GroupsQuiet
    /\ s' = [s EXCEPT !.bound = TRUE, !.intent = FALSE,
                     !.bad = @ \cup {"identity"}]
BadUnknownGC == Fault = "unknown-gc" /\ s.owner /\ ~s.loaded
    /\ s.intent /\ s.primary = "quiet" /\ Quiet(s.aux)
    /\ "load-error" \in s.seen
    /\ s' = [s EXCEPT !.primary = "absent", !.bad = @ \cup {"clear"}]
BadJournalGC == Fault = "journal-gc" /\ Available /\ s.intent
    /\ s.primary = "quiet" /\ Quiet(s.aux)
    /\ s' = [s EXCEPT !.primary = "absent", !.bad = @ \cup {"clear"}]
BadFailedLoadClear == Fault = "failed-load" /\ s.owner /\ ~s.loaded /\ s.intent
    /\ "load-error" \in s.seen
    /\ s' = [s EXCEPT !.intent = FALSE, !.bad = @ \cup {"clear"}]
Next == SaveIntent \/ BeginOpen \/ NativeSeedStart \/ BeginInspection
    \/ ReturnKnownID \/ ReturnNoID \/ ReturnWrongJob \/ SaveReturnedID \/ SaveFailure
    \/ VerifyPrimaryQuiet \/ VerifyAuxQuiet \/ ReceiptUnknown \/ PrimaryReceiptUnknown
    \/ CollectSettledReceipts \/ BindSession
    \/ ClearNeverSeeded \/ Crash \/ Restart \/ SwitchHarness \/ LoadKnown \/ LoadError \/ LoadMalformedID
    \/ SelectBatch \/ ObserveAdmissionHold \/ BeginBatch \/ BeginMessage
    \/ ReturnMessage \/ SettleMessage \/ BadDiscardID \/ BadRepeatSeed
    \/ BadEarlyBind \/ BadClearRUN \/ BadMessage \/ BadBatch \/ BadUnjournaledSeed
    \/ BadWrongJob \/ BadMalformedBind \/ BadFailedLoadClear \/ BadJournalGC \/ BadHarnessBind \/ BadUnknownGC
Spec == Init /\ [][Next]_vars
TypeOK == s \in [owner: BOOLEAN, loaded: BOOLEAN, mode: {"oneshot", "batch"},
    harness: {"opencode", "other"},
    intent: BOOLEAN, openArmed: BOOLEAN, job: 0..2, savedID: {"none", "sid", "malformed"}, bound: BOOLEAN,
    primary: {"absent", "live", "quiet", "unknown"},
    aux: {"none", "live", "quiet", "unknown"}, busy: {"none", "open", "message"},
    returned: BOOLEAN, returnedID: {"none", "sid"}, resultJob: 0..2,
    calls: 0..2, seeds: 0..2, inspections: 0..1, message: BOOLEAN, batch: BOOLEAN,
    crashed: BOOLEAN, unknownSeen: BOOLEAN,
    bad: SUBSET {"lost-id", "repeat", "release", "clear", "admission", "journal", "identity"},
    seen: SUBSET {"id-with-error", "save-failed", "adopted", "bound", "safe-clear",
                 "load-error", "malformed", "batch-held", "control-held", "message-drained"}]
ReturnedIDPreserved == "lost-id" \notin s.bad
AtMostOneSeed == s.seeds <= 1
NoReleaseBeforeQuiet == "release" \notin s.bad
NoAmbiguousClear == "clear" \notin s.bad
NoAdmissionWithControlDebt == "admission" \notin s.bad
NoUnjournaledSeed == "journal" \notin s.bad
ExactValidIdentity == "identity" \notin s.bad
SeedOwnershipRetained == s.seeds > 0 => (s.intent \/ s.bound)
ArmedIntentOwned == s.openArmed => s.intent
Safety == ArmedIntentOwned /\ ReturnedIDPreserved /\ AtMostOneSeed /\ NoReleaseBeforeQuiet
    /\ NoAmbiguousClear /\ NoAdmissionWithControlDebt /\ NoUnjournaledSeed
    /\ ExactValidIdentity /\ SeedOwnershipRetained
WitnessRecoveredErrorID == ~("adopted" \in s.seen /\ "id-with-error" \in s.seen
                            /\ s.unknownSeen /\ s.bound)
WitnessFailedSave == ~("save-failed" \in s.seen /\ s.intent /\ ~s.bound)
WitnessSafeRetry == ~("safe-clear" \in s.seen /\ s.calls = 2 /\ s.seeds = 1 /\ s.bound)
WitnessMessageRestartHold == ~(s.crashed /\ s.message /\ s.loaded
                              /\ "control-held" \in s.seen /\ ~GroupsQuiet)
WitnessBatchRestartHold == ~(s.crashed /\ s.loaded /\ s.intent
                            /\ "batch-held" \in s.seen)
WitnessHarnessMismatchHeld == ~(s.loaded /\ s.harness = "other" /\ s.intent
                                /\ ValidID /\ GroupsQuiet /\ ~s.bound)
WitnessMalformedHold == ~("malformed" \in s.seen /\ s.intent /\ ~s.bound /\ GroupsQuiet)
=============================================================================
