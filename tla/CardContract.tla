---------------------------- MODULE CardContract ----------------------------
\* The card contract's finish (docs/SPEC-CARD-CONTRACT.md), written beside
\* internal/member's Judge and cmd/nova-swarm's frame: what a launch is
\* staged from, how its child ends, the member's push, and the one judgment
\* that turns them into the finish the sprint is told. The sprint's side of a
\* provider finish and a staging refusal -- the redeal, the bound's
\* retirement, the restage and the level -- is the sprint's own model
\* (tla/DirtyTick.tla, tla/SprintEvents.tla), never this one.
\*
\* THE STATE, per card.
\*   ph        the launch: new, running (staged, the child at work), ended
\*             (the child's end is written), pushed (the member's push is
\*             made), done (judged or reaped)
\*   att       the attempt: 1, 2 or 3 (a rework of the attempts before)
\*   earlier   the earlier attempts whose head reached origin (each finished
\*             ok, its push landed), whatever the attempts between them did
\*   from      what staging checked out: kind base (a first attempt), carry
\*             (a rework: the tip of the base branch with the pushed head of
\*             attempt n carried on top, a three-way merge that applied
\*             cleanly), tip (a rework: the bare tip of the base branch; n > 0
\*             is the attempt whose pushed work did not apply there, which
\*             JOB.md asks to be redone, and n = 0 is no work to carry), head
\*             (attempt n's pushed head itself, the rework's stage before
\*             nova-tools#5215) or branch (the previous attempt's branch by
\*             name)
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
\* the work pushed before it), "oldhead" (a rework staged at the last pushed
\* head itself, its base as old as the first attempt's: the stage before
\* nova-tools#5215, whose child could not start again from the tip),
\* "silentconflict" (a rework whose earlier work did not apply at the tip
\* staged at the bare tip with no word that the work must be redone: the
\* pushed work lost in silence). "none" is the design.
\*
\* ONE CARD IS THE INSTANCE. Every action touches one card and no invariant relates
\* two, so the cards are independent: an invariant of the form "for every card"
\* holds on the cards when it holds on one.
\*
\* WHAT IS NOT MODELLED. The shims' parsing of each command (unit and
\* functional tests hold it), the pull request (it never changes the finish),
\* reads (a read's verdict is reported as the reader gives it), more than three
\* attempts, the store's own refusal of a stale finish (SprintEvents and
\* DirtyTick hold it), whether a carry applies (git's merge decides it; both
\* outcomes are open to every rework here), a base that is a sha or a tag,
\* which never moves and whose rework is staged at the pushed head itself (the
\* base modelled is a branch, as every sprint stream's is), and the sprint's
\* own dealing -- a provider finish's redeal and the bound's retirement, a
\* staging refusal's restage and its bound, and the level -- which is the
\* sprint's model (tla/DirtyTick.tla, tla/SprintEvents.tla).
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

\* Staging (cmd/nova-swarm frameOf, internal/swarm StageCard and restageAtTip): a first
\* attempt is staged at the base; a rework at the tip of its base branch, carrying the work of
\* the last pushed head of any earlier attempt (sprint.BaseOf, the packet's base_head and
\* base_attempt), whatever the attempts between did, when it applies cleanly, and else the bare
\* tip with JOB.md asking that work be redone; with no earlier pushed head, the bare tip.
CarriedOf(c) ==
  IF Broken = "previousonly" THEN (IF (att[c] - 1) \in earlier[c] THEN att[c] - 1 ELSE 0)
  ELSE IF earlier[c] = {} THEN 0 ELSE MaxOf(earlier[c])

StageFroms(c) ==
  IF att[c] = 1 THEN {[k |-> "base", n |-> 0]}
  ELSE IF Broken = "branchname" THEN {[k |-> "branch", n |-> 0]}
  ELSE IF Broken = "oldhead" /\ earlier[c] # {} THEN {[k |-> "head", n |-> MaxOf(earlier[c])]}
  ELSE IF CarriedOf(c) = 0 THEN {[k |-> "tip", n |-> 0]}
  ELSE {[k |-> "carry", n |-> CarriedOf(c)],
        [k |-> "tip", n |-> IF Broken = "silentconflict" THEN 0 ELSE CarriedOf(c)]}

Stage(c) ==
  /\ ph[c] = "new"
  /\ ph' = [ph EXCEPT ![c] = "running"]
  /\ \E f \in StageFroms(c) : from' = [from EXCEPT ![c] = f]
  /\ UNCHANGED <<att, earlier, commit, shape, verdict, push, claim, fin, rep>>

\* The member's machine refuses the launch at staging (cmd/nova-swarm native's STAGE
\* FAIL): nothing is staged and no child runs, so there is no commit and no result.
StageRefused(c) ==
  /\ ph[c] = "new"
  /\ ph' = [ph EXCEPT ![c] = "ended"]
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim, fin, rep>>

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

\* THE ONE JUDGMENT (internal/member Judge): ok only when the result has the
\* shape, its verdict is ok, and the member's push landed; anything else is
\* failed, with its reason (a provider failure, a staging refusal, no result,
\* no shape, no commit, a refused push -- all a failed finish, the kind said in
\* the reason, never a separate finish).
JudgeOf(c) ==
  CASE Broken = "today" -> IF push[c] = "refused" THEN "failed" ELSE "ok"
    [] Broken = "noshape" -> IF push[c] = "ok" /\ verdict[c] = "ok" THEN "ok" ELSE "failed"
    [] Broken = "pushignored" -> IF shape[c] /\ verdict[c] = "ok" /\ commit[c] THEN "ok" ELSE "failed"
    [] OTHER -> IF shape[c] /\ verdict[c] = "ok" /\ push[c] = "ok" THEN "ok" ELSE "failed"

\* The one judgment names the finish and what the sprint is told.
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

Next == \E c \in Cards : Stage(c) \/ StageRefused(c) \/ ChildEnds(c) \/ Push(c) \/ Judge(c)
                          \/ Move(c) \/ Reap(c)

Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

TypeOK ==
  /\ ph \in [Cards -> {"new", "running", "ended", "pushed", "done"}]
  /\ from \in [Cards -> [k : {"none", "base", "head", "branch", "carry", "tip"}, n : 0..2]]
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

\* A launch is staged from a commit origin holds: the base, the base branch's tip, or an
\* earlier attempt's head (itself or carried) only when it was pushed (never a branch name).
StagedFromOrigin == \A c \in Cards :
  /\ from[c].k \in {"head", "carry"} => from[c].n \in earlier[c]
  /\ (from[c].k = "tip" /\ from[c].n > 0) => from[c].n \in earlier[c]
  /\ from[c].k = "branch" => (att[c] - 1) \in earlier[c]

\* No pushed work of an earlier attempt is lost to a later attempt: a rework with a pushed
\* earlier attempt carries a head no earlier pushed attempt is after (each attempt's head
\* holds the work it was staged with, so the last pushed head holds every pushed before it),
\* or, when that work did not apply at the tip, its JOB.md names that head's work to be redone.
NoPushedWorkUnreachable == \A c \in Cards :
  (from[c].k # "none" /\ earlier[c] # {}) =>
    (from[c].k \in {"carry", "tip"} /\ \A m \in earlier[c] : m <= from[c].n)

\* A rework is staged at the tip of its base branch, never at a commit as old as an earlier
\* attempt's base, so a child told to start again from the tip finishes from the staged commit
\* (nova-tools#5215).
ReworkStagedAtTheTip == \A c \in Cards :
  (from[c].k # "none" /\ att[c] > 1) => from[c].k \in {"carry", "tip"}

\* Every launch ends judged or reaped.
EveryLaunchEnds == <>(\A c \in Cards : ph[c] = "done" /\ fin[c] \in {"ok", "failed", "reaped"})
=============================================================================
