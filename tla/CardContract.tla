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
\*   att       the attempt: 1, 2 or 3 (a rework of the attempts before)
\*   earlier   the earlier attempts whose head reached origin (each finished
\*             ok, its push landed), whatever the attempts between them did
\*   from      what staging checked out: kind base, head (with n, the attempt
\*             whose pushed head it is) or branch (the previous attempt's
\*             branch by name)
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
\* reported failed), "branchname" (a rework staged from the previous
\* attempt's branch by name, pushed or not), "previousonly" (a rework staged
\* from the immediately previous attempt's head only: an attempt between
\* that failed with no commit sends the next one back to the base, losing
\* the work pushed before it). "none" is the design.
\*
\* WHAT IS NOT MODELLED. The shims' parsing of each command (unit and
\* functional tests hold it), the pull request (it never changes the finish),
\* reads (a read's verdict is reported as the reader gives it), more than three
\* attempts, the store's own refusal of a stale finish (SprintEvents and
\* DirtyTick hold it).
EXTENDS Integers, FiniteSets

CONSTANTS Cards, Broken

VARIABLES ph, att, earlier, from, commit, shape, verdict, push, claim, fin, rep

vars == <<ph, att, earlier, from, commit, shape, verdict, push, claim, fin, rep>>

Init ==
  /\ att \in [Cards -> {1, 2, 3}]
  /\ earlier \in [Cards -> SUBSET {1, 2}]
  /\ \A c \in Cards : earlier[c] \subseteq 1..(att[c] - 1)
  /\ ph = [c \in Cards |-> "new"]
  /\ from = [c \in Cards |-> [k |-> "none", n |-> 0]]
  /\ commit = [c \in Cards |-> FALSE]
  /\ shape = [c \in Cards |-> FALSE]
  /\ verdict = [c \in Cards |-> "none"]
  /\ push = [c \in Cards |-> "none"]
  /\ claim = [c \in Cards |-> "held"]
  /\ fin = [c \in Cards |-> "none"]
  /\ rep = [c \in Cards |-> "none"]

MaxOf(S) == CHOOSE m \in S : \A n \in S : n <= m

\* Staging (cmd/nova-swarm frameOf, internal/swarm StageCard): a rework starts
\* from the last pushed head of any earlier attempt (sprint.BaseOf, the
\* packet's base_head and base_attempt), whatever the attempts between did,
\* else from the base.
StageFrom(c) ==
  IF att[c] = 1 THEN [k |-> "base", n |-> 0]
  ELSE IF Broken = "branchname" THEN [k |-> "branch", n |-> 0]
  ELSE IF Broken = "previousonly" THEN
    IF (att[c] - 1) \in earlier[c] THEN [k |-> "head", n |-> att[c] - 1] ELSE [k |-> "base", n |-> 0]
  ELSE IF earlier[c] = {} THEN [k |-> "base", n |-> 0]
  ELSE [k |-> "head", n |-> MaxOf(earlier[c])]

Stage(c) ==
  /\ ph[c] = "new"
  /\ ph' = [ph EXCEPT ![c] = "running"]
  /\ from' = [from EXCEPT ![c] = StageFrom(c)]
  /\ UNCHANGED <<att, earlier, commit, shape, verdict, push, claim, fin, rep>>

\* The child ends: any commit, any result, any verdict.
ChildEnds(c) ==
  /\ ph[c] = "running"
  /\ ph' = [ph EXCEPT ![c] = "ended"]
  /\ \E k \in BOOLEAN, s \in BOOLEAN, v \in {"ok", "notdone"} :
       /\ commit' = [commit EXCEPT ![c] = k]
       /\ shape' = [shape EXCEPT ![c] = s]
       /\ verdict' = [verdict EXCEPT ![c] = v]
  /\ UNCHANGED <<att, earlier, from, push, claim, fin, rep>>

\* The member's push (cmd/nova-swarm gitPusher.Push): a commit is pushed or
\* refused; no commit is nothing to push.
Push(c) ==
  /\ ph[c] = "ended" /\ claim[c] = "held"
  /\ ph' = [ph EXCEPT ![c] = "pushed"]
  /\ \E p \in IF commit[c] THEN {"ok", "refused"} ELSE {"nocommit"} :
       push' = [push EXCEPT ![c] = p]
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, claim, fin, rep>>

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
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim>>

\* The claim moves under the launch at any time before it is judged.
Move(c) ==
  /\ claim[c] = "held" /\ ph[c] \in {"running", "ended", "pushed"}
  /\ claim' = [claim EXCEPT ![c] = "moved"]
  /\ UNCHANGED <<ph, att, earlier, from, commit, shape, verdict, push, fin, rep>>

\* REAPED is its own finish (internal/member FinishReaped): the child has
\* ended, its claim is gone, and nothing is reported.
Reap(c) ==
  /\ claim[c] = "moved" /\ ph[c] \in {"ended", "pushed"}
  /\ ph' = [ph EXCEPT ![c] = "done"]
  /\ fin' = [fin EXCEPT ![c] = "reaped"]
  /\ rep' = IF Broken = "reapreported" THEN [rep EXCEPT ![c] = "failed"] ELSE rep
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim>>

Next == \E c \in Cards : Stage(c) \/ ChildEnds(c) \/ Push(c) \/ Judge(c) \/ Move(c) \/ Reap(c)

Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

TypeOK ==
  /\ ph \in [Cards -> {"new", "running", "ended", "pushed", "done"}]
  /\ from \in [Cards -> [k : {"none", "base", "head", "branch"}, n : 0..2]]
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

\* A launch is staged from a commit origin holds: the base, or an earlier
\* attempt's head only when it was pushed (never a branch name).
StagedFromOrigin == \A c \in Cards :
  /\ from[c].k = "head" => from[c].n \in earlier[c]
  /\ from[c].k = "branch" => (att[c] - 1) \in earlier[c]

\* No pushed work of an earlier attempt is unreachable from a later attempt's
\* staged base: a rework with a pushed earlier attempt is staged at a head, and
\* at one no earlier pushed attempt is after (each attempt's head holds the
\* base it was staged at, so the last pushed head holds every pushed before it).
NoPushedWorkUnreachable == \A c \in Cards :
  (from[c].k # "none" /\ earlier[c] # {}) => (from[c].k = "head" /\ \A m \in earlier[c] : m <= from[c].n)

\* Every launch ends judged or reaped.
EveryLaunchEnds == <>(\A c \in Cards : ph[c] = "done")
=============================================================================
