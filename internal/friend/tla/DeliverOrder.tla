---------------------------- MODULE DeliverOrder ----------------------------
(* The daemon's duty, in order (internal/friend/delivery.go, stage.go;
   docs/SPEC-FRIEND.md "the delivery order"). Every card on her row is
   delivered: its job staged (a checkout and JOB.md) and its BRIEF.md written.
   Stagers are the hands that stage (her daemon, a second daemon beside it,
   the coordinator's nova-sprint deliver); each takes the job's lock, claims
   its directory with a stage mark (or finds a mark there), clones, and lets
   go; a stage may fail and let go with its mark left, and the next stage
   resumes it. A directory with no mark is another hand's and is never staged
   over. Her runner starts a lane on a card within seconds of its BRIEF.md;
   a lane that meets no checkout is held (the HOLDs of 2026-10-05). With
   RunnerStages her runner stages its own jobs after it reads the brief, and
   her daemon writes the brief alone.
   Reversed witnesses: StageFirst = FALSE, the daemon before this change
   (brief first, stage after: a lane meets no checkout); Claims = FALSE, a
   stage that clones over whatever directory it finds (a second hand stages
   a job twice). *)
EXTENDS FiniteSets, Naturals

CONSTANTS Cards, Stagers, RunnerStages, StageFirst, Claims

Runner == "runner"
None == "none"

VARIABLES dir,      \* jobs/<job>: "none", "mark" (a stage's, no JOB.md yet), "runner" (the runner's, no JOB.md yet), "staged" (JOB.md there)
          lock,     \* the job's lock: a stager, or None
          stagedBy, \* the hands that cloned a checkout into the job
          brief,    \* inbox/<job>/BRIEF.md is there
          lane      \* "idle", "run" (started on a staged job), "held" (started with no checkout)

vars == <<dir, lock, stagedBy, brief, lane>>

Init == /\ dir = [c \in Cards |-> "none"]
        /\ lock = [c \in Cards |-> None]
        /\ stagedBy = [c \in Cards |-> {}]
        /\ brief = [c \in Cards |-> FALSE]
        /\ lane = [c \in Cards |-> "idle"]

\* a stager takes the job's lock (a lock held by another is StartedElsewhere: nothing done)
Lock(s, c) == /\ lock[c] = None /\ dir[c] # "staged"
              /\ lock' = [lock EXCEPT ![c] = s]
              /\ UNCHANGED <<dir, stagedBy, brief, lane>>

\* with the lock: a directory not there is made with the mark; one with a mark is resumed;
\* one with no mark is another hand's, and the stage lets go
Claim(s, c) == /\ Claims /\ lock[c] = s
               /\ IF dir[c] \in {"none", "mark"}
                    THEN /\ dir' = [dir EXCEPT ![c] = "mark"]
                         /\ UNCHANGED lock
                    ELSE /\ lock' = [lock EXCEPT ![c] = None]
                         /\ UNCHANGED dir
               /\ UNCHANGED <<stagedBy, brief, lane>>

Clone(s, c) == /\ lock[c] = s
               /\ IF Claims THEN dir[c] = "mark" ELSE dir[c] # "staged"
               /\ dir' = [dir EXCEPT ![c] = "staged"]
               /\ stagedBy' = [stagedBy EXCEPT ![c] = @ \cup {s}]
               /\ lock' = [lock EXCEPT ![c] = None]
               /\ UNCHANGED <<brief, lane>>

\* a stage that fails (a fetch, a stop) lets go, its mark left for the next
Fail(s, c) == /\ lock[c] = s
              /\ lock' = [lock EXCEPT ![c] = None]
              /\ UNCHANGED <<dir, stagedBy, brief, lane>>

\* the daemon writes the brief: once the job is staged (StageFirst), or alone for a runner that stages
Write(c) == /\ ~brief[c]
            /\ RunnerStages \/ ~StageFirst \/ dir[c] = "staged"
            /\ brief' = [brief EXCEPT ![c] = TRUE]
            /\ UNCHANGED <<dir, lock, stagedBy, lane>>

\* her runner, when it stages its own: on the brief, a directory made where none is, then its JOB.md
RunnerStage(c) == /\ RunnerStages /\ brief[c] /\ dir[c] = "none"
                  /\ dir' = [dir EXCEPT ![c] = "runner"]
                  /\ stagedBy' = [stagedBy EXCEPT ![c] = @ \cup {Runner}]
                  /\ UNCHANGED <<lock, brief, lane>>
RunnerDone(c) == /\ dir[c] = "runner"
                 /\ dir' = [dir EXCEPT ![c] = "staged"]
                 /\ UNCHANGED <<lock, stagedBy, brief, lane>>

\* her runner starts a lane on a brief within seconds of it (one that stages its own, once it has)
Lane(c) == /\ brief[c] /\ lane[c] = "idle"
           /\ RunnerStages => dir[c] = "staged"
           /\ lane' = [lane EXCEPT ![c] = IF dir[c] = "staged" THEN "run" ELSE "held"]
           /\ UNCHANGED <<dir, lock, stagedBy, brief>>

Next == \E c \in Cards :
          \/ Write(c) \/ RunnerStage(c) \/ RunnerDone(c) \/ Lane(c)
          \/ \E s \in Stagers : Lock(s, c) \/ Claim(s, c) \/ Clone(s, c) \/ Fail(s, c)

\* a stage that fails is tried again (StageRetryEvery), and failures do not go on for ever:
\* the claim and the clone are strongly fair, enabled again after each failure
Spec == /\ Init /\ [][Next]_vars
        /\ \A c \in Cards : /\ WF_vars(Write(c)) /\ WF_vars(RunnerStage(c)) /\ WF_vars(RunnerDone(c))
                            /\ \A s \in Stagers : WF_vars(Lock(s, c)) /\ SF_vars(Claim(s, c)) /\ SF_vars(Clone(s, c))

TypeOK == /\ dir \in [Cards -> {"none", "mark", "runner", "staged"}]
          /\ lock \in [Cards -> Stagers \cup {None}]
          /\ stagedBy \in [Cards -> SUBSET (Stagers \cup {Runner})]
          /\ brief \in [Cards -> BOOLEAN]
          /\ lane \in [Cards -> {"idle", "run", "held"}]

\* no lane meets a brief with no checkout
LaneMeetsCheckout == \A c \in Cards : lane[c] # "held"
\* no job is staged by two hands
NoDoubleStage == \A c \in Cards : Cardinality(stagedBy[c]) <= 1
\* a brief the daemon wrote first stands on a staged job
BriefOnCheckout == StageFirst /\ ~RunnerStages => \A c \in Cards : brief[c] => dir[c] = "staged"

\* every card is delivered: staged, and its brief written
Delivered == \A c \in Cards : <>(dir[c] = "staged" /\ brief[c])
=============================================================================
