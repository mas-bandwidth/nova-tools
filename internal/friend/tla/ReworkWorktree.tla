---------------------------- MODULE ReworkWorktree ----------------------------
(* A rework lane starts in the last worktree (internal/friend/stage.go, Stager.keep;
   docs/SPEC-FRIEND.md "A rework starts in the last worktree"; card
   a-rework-lane-starts-in-the-last-worktree.w1, 2026-10-06). One card, two attempts:
   attempt 1 is staged on friend F1 and worked there (committed and pushed, or left with
   uncommitted changes), and ends with its report. The coordinator reworks it with a fix,
   which may name files outside the card's PATHS, and the deal gives attempt 2 to a friend.

   Attempt 2's stage keeps attempt 1's worktree when it may (the same friend, attempt 1
   ended, its tree clean, the fix naming no new files): the tree is moved, never copied,
   into attempt 2's job on its new branch, and the lane is handed the fix as its brief's
   first line. Otherwise it stages afresh: at attempt 1's pushed (carried) head when there
   is one, else at the base.

   Mapped to the code:
     Stage1   = Stager.Stage of attempt 1 (a fresh clone)
     Commit1  = the lane commits and pushes (the head carried to the next attempt)
     Dirty1   = the lane leaves uncommitted changes in its tree
     End1     = the lane's REPORT.md (outbox/<job>/REPORT.md)
     Rework   = nova-sprint rework, then the friends' deal of attempt 2 to friend f
     Keep     = Stager.keep: the kept tree's checks, checkout -b, the rename, JOB.md
     Fresh    = Stager.Stage's clone, at Packet.Carry when the mirror holds it
     Hand     = nextCard (Card.Fix from JOB.md) and CardText / LanePrompt

   Broken names a reversed witness:
     "today" -- every rework stages afresh (the daemon before this card): KeptWhenItMay fails
     "dirty" -- a tree with uncommitted changes is kept: KeptOnlyWhenSafe fails
     "copy"  -- the tree is linked into the new job, not moved: OneJobPerTree fails
     "nofix" -- the lane is handed the kept job without the fix first: KeptLaneHasFixFirst *)
EXTENDS Naturals, FiniteSets

CONSTANTS Friends, F1, Broken

ASSUME F1 \in Friends
ASSUME Broken \in {"none", "today", "dirty", "copy", "nofix"}

VARIABLES
  tree1,     \* attempt 1's worktree: "none", "clean", "dirty", "moved"
  pushed1,   \* attempt 1 pushed a head (the carried head)
  ended1,    \* attempt 1's report is there
  reworked,  \* attempt 2 is dealt
  f2,        \* the friend attempt 2 is dealt to
  newFiles,  \* the rework's fix names files outside PATHS
  stage2,    \* attempt 2's stage: "none", "kept", "fresh"
  start2,    \* where attempt 2's checkout starts: "none", "kept", "carry", "base"
  keptDirty, \* ghost: the tree kept had uncommitted changes
  holders,   \* the jobs whose checkout is attempt 1's tree
  handed2,   \* attempt 2's lane was handed the card
  fixFirst   \* its text began with the fix

vars == <<tree1, pushed1, ended1, reworked, f2, newFiles, stage2, start2, keptDirty, holders, handed2, fixFirst>>

TypeOK ==
  /\ tree1 \in {"none", "clean", "dirty", "moved"}
  /\ pushed1 \in BOOLEAN /\ ended1 \in BOOLEAN /\ reworked \in BOOLEAN
  /\ f2 \in Friends \cup {"none"}
  /\ newFiles \in BOOLEAN
  /\ stage2 \in {"none", "kept", "fresh"}
  /\ start2 \in {"none", "kept", "carry", "base"}
  /\ keptDirty \in BOOLEAN
  /\ holders \subseteq {1, 2}
  /\ handed2 \in BOOLEAN /\ fixFirst \in BOOLEAN

Init ==
  /\ tree1 = "none" /\ pushed1 = FALSE /\ ended1 = FALSE /\ reworked = FALSE
  /\ f2 = "none" /\ newFiles = FALSE /\ stage2 = "none" /\ start2 = "none"
  /\ keptDirty = FALSE /\ holders = {} /\ handed2 = FALSE /\ fixFirst = FALSE

Stage1 ==
  /\ tree1 = "none"
  /\ tree1' = "clean" /\ holders' = {1}
  /\ UNCHANGED <<pushed1, ended1, reworked, f2, newFiles, stage2, start2, keptDirty, handed2, fixFirst>>

Commit1 ==
  /\ tree1 \in {"clean", "dirty"} /\ ~ended1
  /\ tree1' = "clean" /\ pushed1' = TRUE
  /\ UNCHANGED <<ended1, reworked, f2, newFiles, stage2, start2, keptDirty, holders, handed2, fixFirst>>

Dirty1 ==
  /\ tree1 = "clean" /\ ~ended1
  /\ tree1' = "dirty"
  /\ UNCHANGED <<pushed1, ended1, reworked, f2, newFiles, stage2, start2, keptDirty, holders, handed2, fixFirst>>

End1 ==
  /\ tree1 \in {"clean", "dirty"} /\ ~ended1
  /\ ended1' = TRUE
  /\ UNCHANGED <<tree1, pushed1, reworked, f2, newFiles, stage2, start2, keptDirty, holders, handed2, fixFirst>>

Rework(f, nf) ==
  /\ ended1 /\ ~reworked
  /\ reworked' = TRUE /\ f2' = f /\ newFiles' = nf
  /\ UNCHANGED <<tree1, pushed1, ended1, stage2, start2, keptDirty, holders, handed2, fixFirst>>

\* Stager.keep's checks: the kept tree is attempt 1's on this friend, its attempt ended,
\* its tracked files clean (Broken "dirty": not checked), and the fix names no new files.
MayKeep ==
  /\ f2 = F1 /\ ended1 /\ ~newFiles
  /\ tree1 = "clean" \/ (Broken = "dirty" /\ tree1 = "dirty")

Keep ==
  /\ reworked /\ stage2 = "none"
  /\ Broken # "today"
  /\ MayKeep
  /\ stage2' = "kept" /\ start2' = "kept"
  /\ keptDirty' = (tree1 = "dirty")
  /\ tree1' = "moved"
  /\ holders' = IF Broken = "copy" THEN holders \cup {2} ELSE {2}
  /\ UNCHANGED <<pushed1, ended1, reworked, f2, newFiles, handed2, fixFirst>>

Fresh ==
  /\ reworked /\ stage2 = "none"
  /\ ~MayKeep \/ Broken = "today"
  /\ stage2' = "fresh"
  /\ start2' = IF pushed1 THEN "carry" ELSE "base"
  /\ UNCHANGED <<tree1, pushed1, ended1, reworked, f2, newFiles, keptDirty, holders, handed2, fixFirst>>

Hand ==
  /\ stage2 # "none" /\ ~handed2
  /\ handed2' = TRUE
  /\ fixFirst' = (stage2 = "kept" /\ Broken # "nofix")
  /\ UNCHANGED <<tree1, pushed1, ended1, reworked, f2, newFiles, stage2, start2, keptDirty, holders>>

\* the card's two attempts are over: nothing more happens to it
Done == handed2 /\ UNCHANGED vars

Next ==
  \/ Stage1 \/ Commit1 \/ Dirty1 \/ End1
  \/ \E f \in Friends, nf \in BOOLEAN : Rework(f, nf)
  \/ Keep \/ Fresh \/ Hand \/ Done

Spec == Init /\ [][Next]_vars /\ WF_vars(Stage1) /\ WF_vars(End1)
          /\ WF_vars(Keep) /\ WF_vars(Fresh) /\ WF_vars(Hand)

\* A tree is kept only when it may be: the same friend, attempt 1 ended, no new files, and
\* no uncommitted changes carried into the new attempt.
KeptOnlyWhenSafe == stage2 = "kept" => (f2 = F1 /\ ended1 /\ ~newFiles /\ ~keptDirty)

\* A rework that may keep the tree keeps it: it never re-learns the tree from a fresh clone.
KeptWhenItMay == stage2 = "fresh" => ~(f2 = F1 /\ ~newFiles /\ tree1 = "clean")

\* One worktree is one job's: moved, never shared.
OneJobPerTree == Cardinality(holders) <= 1

\* A lane opened in a kept tree is told the fix first.
KeptLaneHasFixFirst == (handed2 /\ stage2 = "kept") => fixFirst

\* A fresh stage after a pushed attempt starts from the carried head, never the bare base.
FreshFromTheCarriedHead == (stage2 = "fresh" /\ pushed1) => start2 = "carry"

\* Every rework is staged and its lane handed.
EveryReworkIsHanded == reworked ~> handed2
=============================================================================
