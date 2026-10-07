---------------------------- MODULE DeliveryLane ----------------------------
\* A friend's lane and the job directory it makes (internal/friend/stage.go Release
\* and Prune, lane_end.go releaseJob; docs/SPEC-FRIEND.md, what is scratch). The
\* branch in the mirror is the record. The job directory is scratch. This is not
\* the present: that machine is Delivery.tla.
\*
\* FinishedLaneLeavesNoJobDirectory: a lane that has ended, with its report
\* written and that head on origin, leaves no job directory.
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
  working  \* the card is still working

vars == <<running, report, origin, jobdir, working>>

TypeOK ==
  /\ running \in [Jobs -> BOOLEAN]
  /\ report \in [Jobs -> BOOLEAN]
  /\ origin \in [Jobs -> BOOLEAN]
  /\ jobdir \in [Jobs -> BOOLEAN]
  /\ working \in [Jobs -> BOOLEAN]

Init ==
  /\ running = [j \in Jobs |-> FALSE]
  /\ report = [j \in Jobs |-> FALSE]
  /\ origin = [j \in Jobs |-> FALSE]
  /\ jobdir = [j \in Jobs |-> FALSE]
  /\ working = [j \in Jobs |-> FALSE]

Begin(j) ==
  /\ ~running[j] /\ ~jobdir[j]
  /\ running' = [running EXCEPT ![j] = TRUE]
  /\ jobdir' = [jobdir EXCEPT ![j] = TRUE]
  /\ working' = [working EXCEPT ![j] = TRUE]
  /\ UNCHANGED <<report, origin>>

Confirm(j) ==
  /\ running[j] /\ ~origin[j]
  /\ origin' = [origin EXCEPT ![j] = TRUE]
  /\ UNCHANGED <<running, report, jobdir, working>>

WriteReport(j) ==
  /\ running[j] /\ ~report[j]
  /\ report' = [report EXCEPT ![j] = TRUE]
  /\ UNCHANGED <<running, origin, jobdir, working>>

\* The lane ends. A confirmed report takes the directory with it, unless the
\* broken twin keeps what it made.
End(j) ==
  /\ running[j]
  /\ running' = [running EXCEPT ![j] = FALSE]
  /\ IF report[j] /\ origin[j] /\ Broken # "keep"
       THEN jobdir' = [jobdir EXCEPT ![j] = FALSE]
       ELSE UNCHANGED jobdir
  /\ UNCHANGED <<report, origin, working>>

\* The card leaves working and the lane is already gone: the next tick sweeps
\* a directory the end left behind. A confirmed report is not left.
Leave(j) ==
  /\ working[j] /\ ~running[j]
  /\ working' = [working EXCEPT ![j] = FALSE]
  /\ IF report[j] /\ origin[j] /\ Broken # "keep"
       THEN jobdir' = [jobdir EXCEPT ![j] = FALSE]
       ELSE UNCHANGED jobdir
  /\ UNCHANGED <<running, report, origin>>

Next ==
  \E j \in Jobs: Begin(j) \/ Confirm(j) \/ WriteReport(j) \/ End(j) \/ Leave(j)

Spec == Init /\ [][Next]_vars

FinishedLaneLeavesNoJobDirectory ==
  \A j \in Jobs: (~running[j] /\ report[j] /\ origin[j]) => ~jobdir[j]

NoLaneLosesItsJob ==
  \A j \in Jobs: running[j] => jobdir[j]
=============================================================================
