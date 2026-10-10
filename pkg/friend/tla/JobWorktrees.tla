---------------------------- MODULE JobWorktrees ----------------------------
(* A job is a git worktree of its repository's one mirror, and finished jobs'
   worktrees are pruned by the daemon's inbox cleanup (internal/friend/stage.go
   Stager.Stage, Stager.Prune, pruneStep; docs/SPEC-FRIEND.md "jobs are
   worktrees of one mirror"). A card is dealt onto her row and leaves it; the
   reconcile writes a held card's brief into her inbox and retires the brief of
   one that left (unless a lane still runs it), then prunes: every job not live
   (held, run by a lane, being staged) whose brief is not in her inbox is
   finished, and the finished past Cap are removed, at most PerPass a pass. A
   stage adds the worktree on the card's branch, created at the base or taken as
   it stands; a lane is handed a held, staged card and commits on its branch. A
   prune removes the worktree and keeps the branch.

   Broken names a reversed witness:
     "async"      -- the prune on a goroutine of its own, its finished set read
                     when it began and removed when it ends (the first cut of
                     this change): a job dealt again and handed to a lane
                     meanwhile loses its checkout, NoLaneLosesItsCheckout fails
     "dropbranch" -- a prune that deletes the branch too: WorkKept fails
     "nolimit"    -- a prune that removes past Cap: CapKept fails *)
EXTENDS Integers, FiniteSets

CONSTANTS Jobs, Cap, PerPass, Broken

ASSUME Broken \in {"none", "async", "dropbranch", "nolimit"}
ASSUME Cap \in Nat /\ PerPass \in Nat \ {0}

VARIABLES held,     \* the card is on her row
          inbox,    \* inbox/<job>/BRIEF.md is there
          lane,     \* a lane runs the card
          staging,  \* a stage of the job is under way
          wt,       \* jobs/<job>/repo, a worktree of the mirror, and its JOB.md
          branch,   \* the card's branch in the mirror: "none", "base", "work"
          worked,   \* ghost: a lane committed on the branch
          pending,  \* async only: the finished set a prune under way read, or {} with none
          busy      \* async only: a prune is under way

vars == <<held, inbox, lane, staging, wt, branch, worked, pending, busy>>

TypeOK == /\ held \in [Jobs -> BOOLEAN]
          /\ inbox \in [Jobs -> BOOLEAN]
          /\ lane \in [Jobs -> BOOLEAN]
          /\ staging \in [Jobs -> BOOLEAN]
          /\ wt \in [Jobs -> BOOLEAN]
          /\ branch \in [Jobs -> {"none", "base", "work"}]
          /\ worked \in [Jobs -> BOOLEAN]
          /\ pending \subseteq Jobs
          /\ busy \in BOOLEAN

Init == /\ held = [j \in Jobs |-> FALSE]
        /\ inbox = [j \in Jobs |-> FALSE]
        /\ lane = [j \in Jobs |-> FALSE]
        /\ staging = [j \in Jobs |-> FALSE]
        /\ wt = [j \in Jobs |-> FALSE]
        /\ branch = [j \in Jobs |-> "none"]
        /\ worked = [j \in Jobs |-> FALSE]
        /\ pending = {}
        /\ busy = FALSE

Min(a, b) == IF a < b THEN a ELSE b

\* the finished jobs as a prune reads them, the inbox i just reconciled
Finished(i) == {j \in Jobs : wt[j] /\ ~held[j] /\ ~lane[j] /\ ~staging[j] /\ ~i[j]}

\* how many of f a prune removes
Removes(f) == IF Broken = "nolimit" THEN Min(PerPass, Cardinality(f))
              ELSE Min(PerPass, IF Cardinality(f) > Cap THEN Cardinality(f) - Cap ELSE 0)

\* the worktrees of r removed; the branches kept (dropbranch: deleted)
Remove(r) == /\ wt' = [j \in Jobs |-> IF j \in r THEN FALSE ELSE wt[j]]
             /\ branch' = [j \in Jobs |-> IF j \in r /\ Broken = "dropbranch" THEN "none" ELSE branch[j]]

\* outside: the coordinator deals the card to her, or takes it back / it lands
Deal(j)  == /\ ~held[j] /\ held' = [held EXCEPT ![j] = TRUE]
            /\ UNCHANGED <<inbox, lane, staging, wt, branch, worked, pending, busy>>
Leave(j) == /\ held[j] /\ held' = [held EXCEPT ![j] = FALSE]
            /\ UNCHANGED <<inbox, lane, staging, wt, branch, worked, pending, busy>>

\* inboxStep: SyncInbox, then pruneStep in the same loop step
Reconcile ==
  LET i == [j \in Jobs |-> IF held[j] THEN TRUE ELSE IF lane[j] THEN inbox[j] ELSE FALSE]
      f == Finished(i)
  IN /\ inbox' = i
     /\ IF Broken = "async"
          THEN /\ IF busy
                    THEN UNCHANGED <<pending, busy>>
                    ELSE \E r \in SUBSET f : Cardinality(r) = Removes(f) /\ pending' = r /\ busy' = TRUE
               /\ UNCHANGED <<wt, branch>>
          ELSE /\ \E r \in SUBSET f : Cardinality(r) = Removes(f) /\ Remove(r)
               /\ UNCHANGED <<pending, busy>>
     /\ UNCHANGED <<held, lane, staging, worked>>

\* async only: the prune's goroutine removes what it read when it began
PruneEnd == /\ busy /\ Remove(pending) /\ pending' = {} /\ busy' = FALSE
            /\ UNCHANGED <<held, inbox, lane, staging, worked>>

\* stageStep: a held card's brief there, its job not staged
BeginStage(j) == /\ held[j] /\ inbox[j] /\ ~wt[j] /\ ~staging[j]
                 /\ staging' = [staging EXCEPT ![j] = TRUE]
                 /\ UNCHANGED <<held, inbox, lane, wt, branch, worked, pending, busy>>
\* Stager.Stage: worktree add -b at the base, or of the branch as it stands
EndStage(j) == /\ staging[j]
               /\ staging' = [staging EXCEPT ![j] = FALSE]
               /\ wt' = [wt EXCEPT ![j] = TRUE]
               /\ branch' = [branch EXCEPT ![j] = IF @ = "none" THEN "base" ELSE @]
               /\ UNCHANGED <<held, inbox, lane, worked, pending, busy>>

\* nextCard: a held card, its brief there and its job staged
Hand(j) == /\ held[j] /\ inbox[j] /\ wt[j] /\ ~lane[j]
           /\ lane' = [lane EXCEPT ![j] = TRUE]
           /\ UNCHANGED <<held, inbox, staging, wt, branch, worked, pending, busy>>
Work(j) == /\ lane[j] /\ wt[j] /\ branch[j] # "none"
           /\ branch' = [branch EXCEPT ![j] = "work"]
           /\ worked' = [worked EXCEPT ![j] = TRUE]
           /\ UNCHANGED <<held, inbox, lane, staging, wt, pending, busy>>
LaneEnd(j) == /\ lane[j] /\ lane' = [lane EXCEPT ![j] = FALSE]
              /\ UNCHANGED <<held, inbox, staging, wt, branch, worked, pending, busy>>

Next == \/ Reconcile \/ PruneEnd
        \/ \E j \in Jobs : Deal(j) \/ Leave(j) \/ BeginStage(j) \/ EndStage(j)
                           \/ Hand(j) \/ Work(j) \/ LaneEnd(j)

Spec == Init /\ [][Next]_vars

\* a lane's checkout is never removed under it
NoLaneLosesItsCheckout == \A j \in Jobs : lane[j] => wt[j]

\* a commit on a job's branch is never lost to a prune
WorkKept == \A j \in Jobs : worked[j] => branch[j] = "work"

\* a prune never takes the finished below Cap
CapKept == [][LET k == Cardinality({j \in Jobs : wt[j] /\ ~wt'[j]})
              IN k > 0 => Cardinality(Finished(inbox')) - k >= Cap]_vars

\* a held job is never pruned
HeldKept == [][\A j \in Jobs : held[j] /\ wt[j] => wt'[j]]_vars
=============================================================================
