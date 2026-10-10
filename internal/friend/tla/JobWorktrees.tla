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

   A rework (New, the attempt after Old's, on the same friend) whose fix names no
   new files (fixable) starts in Old's worktree, moved whole into New's job
   (git worktree move: one tree, one job), when Old's attempt is over (no lane,
   not held, its brief retired) and its tree is there; else it is staged afresh.
   The last worktree of a card (Old until New has one, then New) is kept by
   every prune until the card lands (Land, an outside event: the coordinator
   lands or drops it) or a count cap is passed; no clock takes one, so a rework
   after any delay still finds its tree.

   Broken names a reversed witness:
     "async"      -- the prune on a goroutine of its own, its finished set read
                     when it began and removed when it ends (the first cut of
                     this change): a job dealt again and handed to a lane
                     meanwhile loses its checkout, NoLaneLosesItsCheckout fails
     "dropbranch" -- a prune that deletes the branch too: WorkKept fails
     "nolimit"    -- a prune that removes past Cap: CapKept fails
     "nospare"    -- a prune that does not spare the last worktree of a card:
                     LastKept fails
     "keeplive"   -- the rework takes Old's tree while Old's lane still runs:
                     NoLaneLosesItsCheckout fails
     "fresh"      -- every rework staged afresh, the kept tree never taken:
                     KeptWhenItMay fails *)
EXTENDS Integers, FiniteSets

CONSTANTS Jobs, Cap, PerPass, Broken, Old, New

ASSUME Broken \in {"none", "async", "dropbranch", "nolimit", "nospare", "keeplive", "fresh"}
ASSUME Old \in Jobs /\ New \in Jobs /\ Old # New
ASSUME Cap \in Nat /\ PerPass \in Nat \ {0}

VARIABLES held,     \* the card is on her row
          inbox,    \* inbox/<job>/BRIEF.md is there
          lane,     \* a lane runs the card
          staging,  \* a stage of the job is under way
          wt,       \* jobs/<job>/repo, a worktree of the mirror, and its JOB.md
          branch,   \* the card's branch in the mirror: "none", "base", "work"
          worked,   \* ghost: a lane committed on the branch
          pending,  \* async only: the finished set a prune under way read, or {} with none
          busy,     \* async only: a prune is under way
          fixable,  \* New's fix names no file outside its PATHS
          kept,     \* New's checkout is Old's tree, moved
          landed    \* the card of Old and New landed

rw == <<fixable, kept, landed>>
vars == <<held, inbox, lane, staging, wt, branch, worked, pending, busy, fixable, kept, landed>>

TypeOK == /\ held \in [Jobs -> BOOLEAN]
          /\ inbox \in [Jobs -> BOOLEAN]
          /\ lane \in [Jobs -> BOOLEAN]
          /\ staging \in [Jobs -> BOOLEAN]
          /\ wt \in [Jobs -> BOOLEAN]
          /\ branch \in [Jobs -> {"none", "base", "work"}]
          /\ worked \in [Jobs -> BOOLEAN]
          /\ pending \subseteq Jobs
          /\ busy \in BOOLEAN
          /\ fixable \in BOOLEAN /\ kept \in BOOLEAN /\ landed \in BOOLEAN

Init == /\ held = [j \in Jobs |-> FALSE]
        /\ inbox = [j \in Jobs |-> FALSE]
        /\ lane = [j \in Jobs |-> FALSE]
        /\ staging = [j \in Jobs |-> FALSE]
        /\ wt = [j \in Jobs |-> FALSE]
        /\ branch = [j \in Jobs |-> "none"]
        /\ worked = [j \in Jobs |-> FALSE]
        /\ pending = {}
        /\ busy = FALSE
        /\ fixable = FALSE /\ kept = FALSE /\ landed = FALSE

Min(a, b) == IF a < b THEN a ELSE b

\* the last worktree of the card, until it lands: Old's until New has one, then New's
Spared(j) == /\ ~landed /\ j \in {Old, New} /\ wt[j]
             /\ (j = Old => ~wt[New])

\* the finished jobs as a prune reads them, the inbox i just reconciled
Finished(i) == {j \in Jobs : /\ wt[j] /\ ~held[j] /\ ~lane[j] /\ ~staging[j] /\ ~i[j]
                             /\ (Broken = "nospare" \/ ~Spared(j))}

\* how many of f a prune removes
Removes(f) == IF Broken = "nolimit" THEN Min(PerPass, Cardinality(f))
              ELSE Min(PerPass, IF Cardinality(f) > Cap THEN Cardinality(f) - Cap ELSE 0)

\* the worktrees of r removed; the branches kept (dropbranch: deleted)
Remove(r) == /\ wt' = [j \in Jobs |-> IF j \in r THEN FALSE ELSE wt[j]]
             /\ branch' = [j \in Jobs |-> IF j \in r /\ Broken = "dropbranch" THEN "none" ELSE branch[j]]

\* outside: the coordinator deals the card to her, or takes it back / it lands
Deal(j)  == /\ ~held[j] /\ held' = [held EXCEPT ![j] = TRUE]
            /\ UNCHANGED <<inbox, lane, staging, wt, branch, worked, pending, busy, kept, landed>>
            /\ IF j = New /\ ~kept THEN fixable' \in BOOLEAN ELSE UNCHANGED fixable
            /\ (j = New => ~landed)
Leave(j) == /\ held[j] /\ held' = [held EXCEPT ![j] = FALSE]
            /\ UNCHANGED <<inbox, lane, staging, wt, branch, worked, pending, busy, rw>>
\* the card lands or is dropped (an outside event; no clock, so ReworkKeptMax caps how many): no attempt of it is dealt again
Land == /\ ~landed /\ ~held[New] /\ ~lane[New] /\ ~staging[New] /\ landed' = TRUE
        /\ UNCHANGED <<held, inbox, lane, staging, wt, branch, worked, pending, busy, fixable, kept>>

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
     /\ UNCHANGED <<held, lane, staging, worked, rw>>

\* async only: the prune's goroutine removes what it read when it began
PruneEnd == /\ busy /\ Remove(pending) /\ pending' = {} /\ busy' = FALSE
            /\ UNCHANGED <<held, inbox, lane, staging, worked, rw>>

\* stageStep: a held card's brief there, its job not staged
BeginStage(j) == /\ held[j] /\ inbox[j] /\ ~wt[j] /\ ~staging[j]
                 /\ staging' = [staging EXCEPT ![j] = TRUE]
                 /\ UNCHANGED <<held, inbox, lane, wt, branch, worked, pending, busy, rw>>
\* Stager.keep may take Old's tree for New: Old's attempt over (not held, its
\* brief retired, and no lane still in the checkout). In the code that check is
\* laneInCheckout: the loop's jobs, or a fresh running mark. A report and a
\* retired brief do not say the lane has left.
OldOver == IF Broken = "keeplive" THEN TRUE ELSE ~held[Old] /\ ~lane[Old] /\ ~inbox[Old] /\ ~staging[Old]
MayKeep == /\ fixable /\ wt[Old] /\ OldOver /\ branch[New] = "none"
\* Stager.keep: Old's worktree moved into New's job, on New's branch at Old's head
KeepStage == /\ staging[New] /\ MayKeep /\ Broken # "fresh"
             /\ staging' = [staging EXCEPT ![New] = FALSE]
             /\ wt' = [wt EXCEPT ![New] = TRUE, ![Old] = FALSE]
             /\ branch' = [branch EXCEPT ![New] = IF branch[Old] = "work" THEN "work" ELSE "base"]
             /\ kept' = TRUE
             /\ UNCHANGED <<held, inbox, lane, worked, pending, busy, fixable, landed>>
\* Stager.Stage: worktree add -b at the base (a rework: at the carried head), or of the
\* branch as it stands
EndStage(j) == /\ staging[j]
               /\ (j = New /\ Broken # "fresh" => ~MayKeep)
               /\ staging' = [staging EXCEPT ![j] = FALSE]
               /\ wt' = [wt EXCEPT ![j] = TRUE]
               /\ branch' = [branch EXCEPT ![j] = IF @ = "none" THEN "base" ELSE @]
               /\ IF j = New THEN kept' = FALSE ELSE UNCHANGED kept
               /\ UNCHANGED <<held, inbox, lane, worked, pending, busy, fixable, landed>>

\* nextCard: a held card, its brief there and its job staged
Hand(j) == /\ held[j] /\ inbox[j] /\ wt[j] /\ ~lane[j]
           /\ lane' = [lane EXCEPT ![j] = TRUE]
           /\ UNCHANGED <<held, inbox, staging, wt, branch, worked, pending, busy, rw>>
Work(j) == /\ lane[j] /\ wt[j] /\ branch[j] # "none"
           /\ branch' = [branch EXCEPT ![j] = "work"]
           /\ worked' = [worked EXCEPT ![j] = TRUE]
           /\ UNCHANGED <<held, inbox, lane, staging, wt, pending, busy, rw>>
LaneEnd(j) == /\ lane[j] /\ lane' = [lane EXCEPT ![j] = FALSE]
              /\ UNCHANGED <<held, inbox, staging, wt, branch, worked, pending, busy, rw>>

Next == \/ Reconcile \/ PruneEnd \/ Land \/ KeepStage
        \/ \E j \in Jobs : Deal(j) \/ Leave(j) \/ BeginStage(j) \/ EndStage(j)
                           \/ Hand(j) \/ Work(j) \/ LaneEnd(j)

Spec == Init /\ [][Next]_vars

\* a lane's checkout is never removed under it
NoLaneLosesItsCheckout == \A j \in Jobs : lane[j] => wt[j]

\* a commit on a job's branch is never lost to a prune
WorkKept == \A j \in Jobs : worked[j] => branch[j] = "work"

\* a prune never takes the finished below Cap
\* the worktrees a step removed by a prune: a tree moved into the rework is not removed
Pruned == {j \in Jobs : wt[j] /\ ~wt'[j]} \ (IF ~kept /\ kept' THEN {Old} ELSE {})

CapKept == [][LET k == Cardinality(Pruned)
              IN k > 0 => Cardinality(Finished(inbox')) - k >= Cap]_vars

\* a held job is never pruned, nor its tree moved
HeldKept == [][\A j \in Jobs : held[j] /\ wt[j] => wt'[j]]_vars
\* the last worktree of a card is never pruned before it lands: it leaves only by
\* moving into the rework
LastKept == [][\A j \in Jobs : Spared(j) /\ ~wt'[j] => j = Old /\ wt'[New] /\ kept']_vars

\* a rework that may start in the kept tree does: never staged afresh beside it
KeptWhenItMay == [][staging[New] /\ ~staging'[New] /\ MayKeep => kept']_vars

\* the kept tree is taken only for a fix that names no new files
KeptOnlyWhenFixable == kept => fixable
=============================================================================
