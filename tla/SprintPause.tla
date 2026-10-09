-------------------------- MODULE SprintPause --------------------------
EXTENDS Naturals, FiniteSets
CONSTANT Jobs
VARIABLES running, paused, queued, active, completed, runSeq, epoch,
          pausedCompletion, resumedAfterFinish, illegalAdmission
vars == <<running, paused, queued, active, completed, runSeq, epoch,
          pausedCompletion, resumedAfterFinish, illegalAdmission>>
Init == /\ running = TRUE /\ paused = FALSE
        /\ queued = Jobs /\ active = {} /\ completed = {}
        /\ runSeq = 1 /\ epoch = 1
        /\ pausedCompletion = FALSE /\ resumedAfterFinish = FALSE
        /\ illegalAdmission = FALSE
Pause == /\ running /\ ~paused /\ paused' = TRUE
         /\ UNCHANGED <<running, queued, active, completed, runSeq, epoch,
                       pausedCompletion, resumedAfterFinish, illegalAdmission>>
Unpause == /\ running /\ paused /\ paused' = FALSE
           /\ UNCHANGED <<running, queued, active, completed, runSeq, epoch,
                         pausedCompletion, resumedAfterFinish, illegalAdmission>>
Admit(j) == /\ running /\ ~paused /\ j \in queued
            /\ queued' = queued \ {j} /\ active' = active \cup {j}
            /\ illegalAdmission' = (illegalAdmission \/ paused)
            /\ resumedAfterFinish' = (resumedAfterFinish \/ pausedCompletion)
            /\ UNCHANGED <<running, paused, completed, runSeq, epoch, pausedCompletion>>
Complete(j) == /\ running /\ j \in active
               /\ active' = active \ {j} /\ completed' = completed \cup {j}
               /\ pausedCompletion' = (pausedCompletion \/ (paused /\ queued # {}))
               /\ UNCHANGED <<running, paused, queued, runSeq, epoch,
                             resumedAfterFinish, illegalAdmission>>
Stop == /\ running /\ running' = FALSE /\ paused' = FALSE
        /\ UNCHANGED <<queued, active, completed, runSeq, epoch,
                      pausedCompletion, resumedAfterFinish, illegalAdmission>>
Control == UNCHANGED vars
Next == Pause \/ Unpause \/ Stop \/ Control
        \/ (\E j \in Jobs: Admit(j) \/ Complete(j))
Spec == Init /\ [][Next]_vars
TypeOK == /\ running \in BOOLEAN /\ paused \in BOOLEAN
          /\ queued \subseteq Jobs /\ active \subseteq Jobs /\ completed \subseteq Jobs
          /\ runSeq = 1 /\ epoch = 1
          /\ pausedCompletion \in BOOLEAN /\ resumedAfterFinish \in BOOLEAN
          /\ illegalAdmission \in BOOLEAN
Disjoint == /\ queued \cap active = {} /\ queued \cap completed = {}
            /\ active \cap completed = {}
NoPausedAdmission == ~illegalAdmission
NoStoppedUnpause == ~running => ~paused
PauseKeepsClaims == [][Pause => UNCHANGED <<queued, active, completed, runSeq, epoch>>]_vars
ReachPausedFinishThenResumeWitness == ~resumedAfterFinish
=============================================================================
