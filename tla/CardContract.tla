---------------------------- MODULE CardContract ----------------------------
\* The card contract's finish (docs/SPEC-CARD-CONTRACT.md), written beside
\* internal/member's Judge and cmd/nova-swarm's frame: what a launch is
\* staged from, how its child ends, the member's push, and the one judgment
\* that turns them into the finish the sprint is told.
\*
\* THE STATE, per card.
\*   ph        the launch: new, running (staged, the child at work), ended
\*             (the child's end is written), pushed (the member's push is
\*             made), done (judged or reaped)
\*   att       the attempt: 1, or 2 (a rework of an attempt before)
\*   prev      attempt 2 only: whether the attempt before's head reached
\*             origin (it finished ok, its push landed)
\*   from      what staging checked out: base, prevhead (the previous pushed
\*             head) or prevbranch (the previous attempt's branch by name)
\*   commit    the child made a commit no origin branch held
\*   shape     RESULT.md has the contract's shape
\*   verdict   the result's verdict: ok or notdone
\*   push      the member's push: none (not yet), ok, refused, nocommit
\*   claim     held, or moved (a clear, a redeal, a drop, a return)
\*   fin       the finish: none, ok, failed, reaped
\*   rep       what the sprint was told: none, ok, failed
\*
\* BROKEN names a reversed witness, a rule as it was or as it could be
\* written wrong, that TLC must break: "today" (ok unless the push was
\* refused: the finish of 2026-09-30 that sent a card with no commit to
\* review), "noshape" (ok without the result's shape), "pushignored" (ok on
\* the child's word with the push refused), "reapreported" (a reaped launch
\* reported failed), "branchname" (attempt 2 staged from the previous
\* attempt's branch by name, pushed or not). "none" is the design.
\*
\* WHAT IS NOT MODELLED. The shims' parsing of each command (unit and
\* functional tests hold it), the pull request (it never changes the finish),
\* reads (a read's verdict is reported as the reader gives it), more than two
\* attempts, the store's own refusal of a stale finish (SprintEvents and
\* DirtyTick hold it).
EXTENDS FiniteSets

CONSTANTS Cards, Broken

VARIABLES ph, att, prev, from, commit, shape, verdict, push, claim, fin, rep

vars == <<ph, att, prev, from, commit, shape, verdict, push, claim, fin, rep>>

Init ==
  /\ att \in [Cards -> {1, 2}]
  /\ prev \in [Cards -> BOOLEAN]
  /\ \A c \in Cards : att[c] = 1 => ~prev[c]
  /\ ph = [c \in Cards |-> "new"]
  /\ from = [c \in Cards |-> "none"]
  /\ commit = [c \in Cards |-> FALSE]
  /\ shape = [c \in Cards |-> FALSE]
  /\ verdict = [c \in Cards |-> "none"]
  /\ push = [c \in Cards |-> "none"]
  /\ claim = [c \in Cards |-> "held"]
  /\ fin = [c \in Cards |-> "none"]
  /\ rep = [c \in Cards |-> "none"]

\* Staging (cmd/nova-swarm frameOf, internal/swarm StageCard): attempt 2 starts
\* from the previous pushed head when there is one, else from the base.
StageFrom(c) ==
  IF att[c] = 1 THEN "base"
  ELSE IF Broken = "branchname" THEN "prevbranch"
  ELSE IF prev[c] THEN "prevhead" ELSE "base"

Stage(c) ==
  /\ ph[c] = "new"
  /\ ph' = [ph EXCEPT ![c] = "running"]
  /\ from' = [from EXCEPT ![c] = StageFrom(c)]
  /\ UNCHANGED <<att, prev, commit, shape, verdict, push, claim, fin, rep>>

\* The child ends: any commit, any result, any verdict.
ChildEnds(c) ==
  /\ ph[c] = "running"
  /\ ph' = [ph EXCEPT ![c] = "ended"]
  /\ \E k \in BOOLEAN, s \in BOOLEAN, v \in {"ok", "notdone"} :
       /\ commit' = [commit EXCEPT ![c] = k]
       /\ shape' = [shape EXCEPT ![c] = s]
       /\ verdict' = [verdict EXCEPT ![c] = v]
  /\ UNCHANGED <<att, prev, from, push, claim, fin, rep>>

\* The member's push (cmd/nova-swarm gitPusher.Push): a commit is pushed or
\* refused; no commit is nothing to push.
Push(c) ==
  /\ ph[c] = "ended" /\ claim[c] = "held"
  /\ ph' = [ph EXCEPT ![c] = "pushed"]
  /\ \E p \in IF commit[c] THEN {"ok", "refused"} ELSE {"nocommit"} :
       push' = [push EXCEPT ![c] = p]
  /\ UNCHANGED <<att, prev, from, commit, shape, verdict, claim, fin, rep>>

\* THE ONE JUDGMENT (internal/member Judge).
JudgeOf(c) ==
  CASE Broken = "today" -> IF push[c] = "refused" THEN "failed" ELSE "ok"
    [] Broken = "noshape" -> IF push[c] = "ok" /\ verdict[c] = "ok" THEN "ok" ELSE "failed"
    [] Broken = "pushignored" -> IF shape[c] /\ verdict[c] = "ok" /\ commit[c] THEN "ok" ELSE "failed"
    [] OTHER -> IF shape[c] /\ verdict[c] = "ok" /\ push[c] = "ok" THEN "ok" ELSE "failed"

Judge(c) ==
  /\ ph[c] = "pushed" /\ claim[c] = "held"
  /\ ph' = [ph EXCEPT ![c] = "done"]
  /\ fin' = [fin EXCEPT ![c] = JudgeOf(c)]
  /\ rep' = [rep EXCEPT ![c] = JudgeOf(c)]
  /\ UNCHANGED <<att, prev, from, commit, shape, verdict, push, claim>>

\* The claim moves under the launch at any time before it is judged.
Move(c) ==
  /\ claim[c] = "held" /\ ph[c] \in {"running", "ended", "pushed"}
  /\ claim' = [claim EXCEPT ![c] = "moved"]
  /\ UNCHANGED <<ph, att, prev, from, commit, shape, verdict, push, fin, rep>>

\* REAPED is its own finish (internal/member FinishReaped): the child has
\* ended, its claim is gone, and nothing is reported.
Reap(c) ==
  /\ claim[c] = "moved" /\ ph[c] \in {"ended", "pushed"}
  /\ ph' = [ph EXCEPT ![c] = "done"]
  /\ fin' = [fin EXCEPT ![c] = "reaped"]
  /\ rep' = IF Broken = "reapreported" THEN [rep EXCEPT ![c] = "failed"] ELSE rep
  /\ UNCHANGED <<att, prev, from, commit, shape, verdict, push, claim>>

Next == \E c \in Cards : Stage(c) \/ ChildEnds(c) \/ Push(c) \/ Judge(c) \/ Move(c) \/ Reap(c)

Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

TypeOK ==
  /\ ph \in [Cards -> {"new", "running", "ended", "pushed", "done"}]
  /\ from \in [Cards -> {"none", "base", "prevhead", "prevbranch"}]
  /\ push \in [Cards -> {"none", "ok", "refused", "nocommit"}]
  /\ fin \in [Cards -> {"none", "ok", "failed", "reaped"}]
  /\ rep \in [Cards -> {"none", "ok", "failed"}]

\* Ok => PushedHead /\ Shape: an ok finish is a commit the child made, on
\* origin by the member's push, with the result's shape and verdict ok.
OkIsPushedWork ==
  \A c \in Cards : fin[c] = "ok" => commit[c] /\ push[c] = "ok" /\ shape[c] /\ verdict[c] = "ok"

\* A reaped launch tells the sprint nothing.
ReapedIsNotReported == \A c \in Cards : fin[c] = "reaped" => rep[c] = "none"

\* What the sprint is told is the judgment, never a reaped launch's.
ReportedIsTheJudgment == \A c \in Cards : rep[c] # "none" => rep[c] = fin[c]

\* A launch is staged from a commit origin holds: the base, or the previous
\* attempt's head only when it was pushed.
StagedFromOrigin == \A c \in Cards : from[c] \in {"prevhead", "prevbranch"} => prev[c]

\* Every launch ends judged or reaped.
EveryLaunchEnds == <>(\A c \in Cards : ph[c] = "done")
=============================================================================
