---------------- MODULE CompletionReportJournalBridge ----------------
(* One synthetic one-shot completion in lane_end.go. The native group is quiet
   before the frozen Ending and REPORT are published. Direct server Finish can
   commit before the coordinator finish-note journal and lanes.json clear. A
   lost Finish answer is not evidence that the server did not commit. The
   later outbox pass is fenced by the durable Started claim and must be
   idempotent when the server already finished the exact card. *)
EXTENDS Naturals

CONSTANTS SafeRelease, SafeDedup, SafeNotice
VARIABLE s
vars == <<s>>

Init == s = [up |-> TRUE, group |-> "live", frozen |-> FALSE,
             report |-> FALSE, server |-> "working", reply |-> "none",
             directTried |-> FALSE, directCommitted |-> FALSE,
             directWindow |-> FALSE,
             outboxTried |-> FALSE,
             notice |-> FALSE, startedDisk |-> TRUE, startedMem |-> TRUE,
             slot |-> "held", journalFails |-> 0, saveFails |-> 0,
             lostReply |-> FALSE, restarted |-> FALSE,
             restartedJournalDebt |-> FALSE, restartedSaveDebt |-> FALSE,
             commits |-> 0]

GroupQuiets == /\ s.group = "live"
               /\ s' = [s EXCEPT !.group = "quiet"]
FreezeEnding == /\ s.up /\ s.group = "quiet" /\ ~s.frozen
                /\ s.startedDisk
                /\ s' = [s EXCEPT !.frozen = TRUE]
WriteReport == /\ s.up /\ s.frozen /\ ~s.report
               /\ s' = [s EXCEPT !.report = TRUE,
                                   !.directWindow = TRUE]

(* One direct Finish attempt follows a newly written synthetic REPORT. On
   retry/restart endCard sees that REPORT and does not send this direct call
   again. A transport error can mean either committed/lost or no commit. *)
FinishCommits(lost) == /\ s.up /\ s.report /\ s.directWindow
                       /\ ~s.directTried
                       /\ s.server = "working"
                       /\ s' = [s EXCEPT !.server = "done",
                                           !.commits = 1,
                                           !.directTried = TRUE,
                                           !.directCommitted = TRUE,
                                           !.directWindow = FALSE,
                                           !.lostReply = lost,
                                           !.reply = IF lost THEN "unknown" ELSE "ack"]
FinishNoCommit == /\ s.up /\ s.report /\ s.directWindow
                  /\ ~s.directTried
                  /\ s.server = "working"
                  /\ s' = [s EXCEPT !.directTried = TRUE,
                                      !.directWindow = FALSE,
                                      !.reply = "unknown"]

JournalWriteFails == /\ s.up /\ s.report /\ ~s.directWindow
                     /\ s.startedDisk
                     /\ ~s.notice /\ s.journalFails = 0
                     /\ s' = [s EXCEPT !.journalFails = 1]
WriteNotice == /\ s.up /\ s.frozen /\ ~s.notice /\ s.startedDisk
               /\ ((s.report /\ ~s.directWindow) \/ ~SafeNotice)
               /\ s' = [s EXCEPT !.notice = TRUE]
StartedSaveFails == /\ s.up /\ s.notice /\ s.startedDisk
                    /\ s.saveFails = 0
                    /\ s' = [s EXCEPT !.saveFails = 1]
ClearStarted == /\ s.up /\ s.startedDisk /\ s.notice
                /\ s' = [s EXCEPT !.startedDisk = FALSE,
                                    !.startedMem = FALSE]
ReleaseLane == /\ s.up /\ s.slot = "held"
               /\ s.group = "quiet" /\ s.frozen /\ s.report
               /\ ((~s.startedDisk /\ s.notice) \/ ~SafeRelease)
               /\ s' = [s EXCEPT !.slot = "free"]

Crash == /\ s.up
         /\ s' = [s EXCEPT !.up = FALSE,
                             !.startedMem = FALSE,
                             !.directWindow = FALSE]
Restart == /\ ~s.up
           /\ s' = [s EXCEPT !.up = TRUE,
                               !.startedMem = s.startedDisk,
                               !.restarted = TRUE,
                               !.restartedJournalDebt =
                                   (s.restartedJournalDebt \/
                                    (s.journalFails = 1 /\ ~s.notice /\ s.startedDisk)),
                               !.restartedSaveDebt =
                                   (s.restartedSaveDebt \/
                                    (s.saveFails = 1 /\ s.notice /\ s.startedDisk))]

(* The outbox sees only a job with no Started debt. A second Finish on a Done
   row is a no-op under SafeDedup; the reversed switch records duplicate
   completion. The no-commit direct transport failure settles here. *)
OutboxFinish == /\ s.up /\ s.report /\ ~s.startedDisk /\ s.notice
                /\ ~s.outboxTried
                /\ s' = [s EXCEPT !.outboxTried = TRUE,
                                    !.server = "done",
                                    !.commits = IF s.server = "working" THEN 1
                                                 ELSE IF SafeDedup THEN s.commits
                                                 ELSE s.commits + 1]

Next == \/ GroupQuiets \/ FreezeEnding \/ WriteReport
        \/ (\E lost \in BOOLEAN : FinishCommits(lost)) \/ FinishNoCommit
        \/ JournalWriteFails \/ WriteNotice \/ StartedSaveFails
        \/ ClearStarted \/ ReleaseLane \/ Crash \/ Restart \/ OutboxFinish
Spec == Init /\ [][Next]_vars

TypeOK == /\ s.up \in BOOLEAN /\ s.group \in {"live", "quiet"}
          /\ s.frozen \in BOOLEAN /\ s.report \in BOOLEAN
          /\ s.server \in {"working", "done"}
          /\ s.reply \in {"none", "unknown", "ack"}
          /\ s.directTried \in BOOLEAN /\ s.directCommitted \in BOOLEAN
          /\ s.directWindow \in BOOLEAN
          /\ s.outboxTried \in BOOLEAN
          /\ s.notice \in BOOLEAN /\ s.startedDisk \in BOOLEAN
          /\ s.startedMem \in BOOLEAN /\ s.slot \in {"held", "free"}
          /\ s.journalFails \in 0..1 /\ s.saveFails \in 0..1
          /\ s.lostReply \in BOOLEAN /\ s.restarted \in BOOLEAN
          /\ s.restartedJournalDebt \in BOOLEAN
          /\ s.restartedSaveDebt \in BOOLEAN
          /\ s.commits \in 0..2
NoticeTruth == s.notice => (s.report /\ s.frozen)
NoEarlyCapacity == s.slot = "free" =>
                   (s.group = "quiet" /\ ~s.startedDisk /\ s.notice /\ s.report)
NoDuplicateCompletion == s.commits <= 1
ClaimRetainedUntilNotice == ~s.notice => s.startedDisk

(* These are expected invariant failures, each requiring its named causal
   path. They establish reachability, not fairness or eventual recovery. *)
WitnessLostReplyJournalRetry ==
    ~(s.lostReply /\ s.restartedJournalDebt /\ s.notice
      /\ ~s.startedDisk /\ s.slot = "free" /\ s.commits = 1)
WitnessDoneHeldWithoutNotice ==
    ~(s.lostReply /\ s.journalFails = 1 /\ s.server = "done"
      /\ s.startedDisk /\ ~s.notice /\ s.slot = "held")
WitnessSavedClearRetry ==
    ~(s.restartedSaveDebt /\ s.directCommitted /\ s.notice /\ s.commits = 1
      /\ ~s.startedDisk /\ s.slot = "free")
WitnessNoCommitOutbox ==
    ~(s.directTried /\ s.reply = "unknown" /\ ~s.lostReply
      /\ s.outboxTried /\ s.server = "done" /\ s.commits = 1)
=====================================================================
