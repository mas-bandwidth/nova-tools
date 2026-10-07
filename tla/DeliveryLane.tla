---------------------------- MODULE DeliveryLane ----------------------------
\* A friend's lane and the job directory it makes (internal/friend/stage.go Release
\* and Prune, lane_end.go releaseJob; docs/SPEC-FRIEND.md, what is scratch). The
\* branch in the mirror is the record. The job directory is scratch. This is not
\* the present: that machine is Delivery.tla.
\*
\* FinishedLaneLeavesNoJobDirectory: a lane that has ended, with its report
\* written and that head on origin, leaves no job directory after its pending
\* cleanup is finalized. Fair cleanup must eventually finalize it.
\* NoLaneLosesItsJob: a lane that is still running still has its directory.
\*
\* Broken = "keep" is the machinery that left the directory after the lane ended.
\* FinishedLaneLeavesNoJobDirectory fails.
EXTENDS FiniteSets

CONSTANTS Jobs, Broken

ASSUME Broken \in {"none", "keep"}

VARIABLES
  running, \* the lane is in the job
  report,  \* the report is written
  origin,  \* the report's head is on origin
  jobdir,  \* jobs/<job> is there
  pending, \* queued asynchronous cleanup; not yet finalized
  working  \* the card is still working

vars == <<running, report, origin, jobdir, pending, working>>

TypeOK ==
  /\ running \in [Jobs -> BOOLEAN]
  /\ report \in [Jobs -> BOOLEAN]
  /\ origin \in [Jobs -> BOOLEAN]
  /\ jobdir \in [Jobs -> BOOLEAN]
  /\ pending \in [Jobs -> BOOLEAN]
  /\ working \in [Jobs -> BOOLEAN]

Init ==
  /\ running = [j \in Jobs |-> FALSE]
  /\ report = [j \in Jobs |-> FALSE]
  /\ origin = [j \in Jobs |-> FALSE]
  /\ jobdir = [j \in Jobs |-> FALSE]
  /\ pending = [j \in Jobs |-> FALSE]
  /\ working = [j \in Jobs |-> FALSE]

Begin(j) ==
  /\ ~running[j] /\ ~jobdir[j] /\ ~pending[j]
  /\ running' = [running EXCEPT ![j] = TRUE]
  /\ jobdir' = [jobdir EXCEPT ![j] = TRUE]
  /\ working' = [working EXCEPT ![j] = TRUE]
  /\ UNCHANGED <<report, origin, pending>>

Confirm(j) ==
  /\ running[j] /\ ~origin[j]
  /\ origin' = [origin EXCEPT ![j] = TRUE]
  /\ UNCHANGED <<running, report, jobdir, pending, working>>

WriteReport(j) ==
  /\ running[j] /\ ~report[j]
  /\ report' = [report EXCEPT ![j] = TRUE]
  /\ UNCHANGED <<running, origin, jobdir, pending, working>>

\* The lane ends. A confirmed report takes the directory with it, unless the
\* broken twin keeps what it made.
End(j) ==
  /\ running[j]
  /\ running' = [running EXCEPT ![j] = FALSE]
  /\ pending' = [pending EXCEPT ![j] = report[j] /\ origin[j] /\ Broken # "keep"]
  /\ UNCHANGED <<report, origin, jobdir, working>>

Cleanup(j) ==
  /\ pending[j] /\ ~running[j]
  /\ jobdir' = [jobdir EXCEPT ![j] = FALSE]
  /\ pending' = [pending EXCEPT ![j] = FALSE]
  /\ UNCHANGED <<running, report, origin, working>>

Leave(j) ==
  /\ working[j] /\ ~running[j]
  /\ working' = [working EXCEPT ![j] = FALSE]
  /\ UNCHANGED <<running, report, origin, jobdir, pending>>

Next ==
  \E j \in Jobs: Begin(j) \/ Confirm(j) \/ WriteReport(j) \/ End(j) \/ Leave(j) \/ Cleanup(j)

Spec == Init /\ [][Next]_vars /\ \A j \in Jobs: WF_vars(Cleanup(j))

FinishedLaneLeavesNoJobDirectory ==
  \A j \in Jobs: (~running[j] /\ report[j] /\ origin[j] /\ ~pending[j]) => ~jobdir[j]

CleanupCompletes == \A j \in Jobs: pending[j] ~> ~jobdir[j]

NoLaneLosesItsJob ==
  \A j \in Jobs: running[j] => jobdir[j]
=============================================================================
