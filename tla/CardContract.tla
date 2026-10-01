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
\*   rep       what the sprint was told: none, ok, failed, provider, retired
\*   prov      the child's run ended on the provider's failure: the harness's
\*             own log carries a provider error, or its transcript stops on a
\*             tool result after a clean exit (cmd/nova-swarm nativeprovider.go)
\*   rt        the route the take runs on; tried the routes a take of the card
\*             failed on (the card records the route that failed)
\*   rds       the work card's redeals (internal/sprint steps_tick.go, MaxRedeals)
\*   fw        the failed-work judgments the sprint opened for the card
\*   bj        the bound judgments the sprint opened for the card
\*
\* THE PROVIDER FAILURE. A run that ended with no result and whose provider
\* failed it (prov) is finished `provider`, after a refused push (which is
\* said first) and before any failed-work reason: Judge. The sprint treats the
\* finish as an ended take (internal/sprint providerEnded; tla/DirtyTick.tla
\* RedealsAreEndedTakes): Redeal returns the work card to the deal, the same
\* attempt, with the route that failed recorded, and the route the redeal draws
\* leaves it out when another remains (route.go routeOf); it counts toward
\* MaxRedeals, and the take that ends with the bound reached is Retire: one
\* judgment, never a failed-work one. A redeal is the same attempt: att and
\* earlier do not move, so the next launch is staged exactly as the first was
\* (StageFrom); redeals count per work card, and a rework (the next attempt, up
\* to three) is a new work card with a count of its own, outside this model.
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
\* the work pushed before it), "providerfailed" (a provider failure judged as
\* failed work), "sameroute" (a redeal on the route that failed though another
\* remains), "unbounded" (a redeal past MaxRedeals). "none" is the design.
\*
\* ONE CARD IS THE INSTANCE. Every action touches one card and no invariant relates
\* two, so the cards are independent: an invariant of the form "for every card"
\* holds on the cards when it holds on one, and two cards only square the states
\* (the redeals made two cards past the bench's budget).
\*
\* WHAT IS NOT MODELLED. The shims' parsing of each command (unit and
\* functional tests hold it), the pull request (it never changes the finish),
\* reads (a read's verdict is reported as the reader gives it), more than three
\* attempts, the store's own refusal of a stale finish (SprintEvents and
\* DirtyTick hold it).
EXTENDS Integers, FiniteSets

CONSTANTS Cards, Broken, Routes, MaxRedeals

VARIABLES ph, att, earlier, from, commit, shape, verdict, push, claim, fin, rep,
          prov, rt, tried, rds, fw, bj

vars == <<ph, att, earlier, from, commit, shape, verdict, push, claim, fin, rep,
          prov, rt, tried, rds, fw, bj>>

\* The variables the provider failure adds, for the actions that leave them.
pvars == <<prov, rt, tried, rds, fw, bj>>

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
  /\ prov = [c \in Cards |-> FALSE]
  /\ rt \in [Cards -> Routes]
  /\ tried = [c \in Cards |-> {}]
  /\ rds = [c \in Cards |-> 0]
  /\ fw = [c \in Cards |-> 0]
  /\ bj = [c \in Cards |-> 0]

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
  /\ UNCHANGED <<att, earlier, commit, shape, verdict, push, claim, fin, rep, pvars>>

\* The child ends: any commit, any result, any verdict, and the provider's
\* failure or not.
ChildEnds(c) ==
  /\ ph[c] = "running"
  /\ ph' = [ph EXCEPT ![c] = "ended"]
  /\ \E k \in BOOLEAN, s \in BOOLEAN, v \in {"ok", "notdone"}, f \in BOOLEAN :
       /\ commit' = [commit EXCEPT ![c] = k]
       /\ shape' = [shape EXCEPT ![c] = s]
       /\ verdict' = [verdict EXCEPT ![c] = v]
       /\ prov' = [prov EXCEPT ![c] = f]
  /\ UNCHANGED <<att, earlier, from, push, claim, fin, rep, rt, tried, rds, fw, bj>>

\* The member's push (cmd/nova-swarm gitPusher.Push): a commit is pushed or
\* refused; no commit is nothing to push.
Push(c) ==
  /\ ph[c] = "ended" /\ claim[c] = "held"
  /\ ph' = [ph EXCEPT ![c] = "pushed"]
  /\ \E p \in IF commit[c] THEN {"ok", "refused"} ELSE {"nocommit"} :
       push' = [push EXCEPT ![c] = p]
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, claim, fin, rep, pvars>>

\* THE ONE JUDGMENT (internal/member Judge).
JudgeOf(c) ==
  CASE Broken = "today" -> IF push[c] = "refused" THEN "failed" ELSE "ok"
    [] Broken = "noshape" -> IF push[c] = "ok" /\ verdict[c] = "ok" THEN "ok" ELSE "failed"
    [] Broken = "pushignored" -> IF shape[c] /\ verdict[c] = "ok" /\ commit[c] THEN "ok" ELSE "failed"
    [] Broken = "providerfailed" ->
         IF shape[c] /\ verdict[c] = "ok" /\ push[c] = "ok" THEN "ok" ELSE "failed"
    [] OTHER ->
         IF shape[c] /\ verdict[c] = "ok" /\ push[c] = "ok" THEN "ok"
         ELSE IF push[c] # "refused" /\ prov[c] /\ ~shape[c] THEN "provider"
         ELSE "failed"

\* A finish `failed` opens the failed-work judgment; a finish `provider` opens
\* none, and records the route that failed.
Judge(c) ==
  /\ ph[c] = "pushed" /\ claim[c] = "held"
  /\ ph' = [ph EXCEPT ![c] = "done"]
  /\ fin' = [fin EXCEPT ![c] = JudgeOf(c)]
  /\ rep' = [rep EXCEPT ![c] = JudgeOf(c)]
  /\ fw' = [fw EXCEPT ![c] = IF JudgeOf(c) = "failed" THEN @ + 1 ELSE @]
  /\ tried' = [tried EXCEPT ![c] = IF JudgeOf(c) = "provider" THEN @ \cup {rt[c]} ELSE @]
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim, prov, rt, rds, bj>>

\* The claim moves under the launch at any time before it is judged.
Move(c) ==
  /\ claim[c] = "held" /\ ph[c] \in {"running", "ended", "pushed"}
  /\ claim' = [claim EXCEPT ![c] = "moved"]
  /\ UNCHANGED <<ph, att, earlier, from, commit, shape, verdict, push, fin, rep, pvars>>

\* REAPED is its own finish (internal/member FinishReaped): the child has
\* ended, its claim is gone, and nothing is reported.
Reap(c) ==
  /\ claim[c] = "moved" /\ ph[c] \in {"ended", "pushed"}
  /\ ph' = [ph EXCEPT ![c] = "done"]
  /\ fin' = [fin EXCEPT ![c] = "reaped"]
  /\ rep' = IF Broken = "reapreported" THEN [rep EXCEPT ![c] = "failed"] ELSE rep
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim, pvars>>

\* THE SPRINT'S SIDE OF A PROVIDER FINISH (internal/sprint providerEnded, then the
\* deal's redeal). The take ended: the work card returns to the deal and the launch
\* is new again, the same attempt (att and earlier stay; the launch is staged from
\* them again, as the first was, so `from` is the unstaged record), the redeal is
\* counted, and the route it draws leaves out every route a take of the card failed
\* on while another remains.
RedealRoutes(c) ==
  IF Broken = "sameroute" THEN {rt[c]}
  ELSE IF tried[c] # Routes THEN Routes \ tried[c] ELSE Routes

Redeal(c) ==
  /\ fin[c] = "provider"
  /\ (rds[c] < MaxRedeals \/ Broken = "unbounded")
  /\ ph' = [ph EXCEPT ![c] = "new"]
  /\ from' = [from EXCEPT ![c] = [k |-> "none", n |-> 0]]
  /\ commit' = [commit EXCEPT ![c] = FALSE]
  /\ shape' = [shape EXCEPT ![c] = FALSE]
  /\ verdict' = [verdict EXCEPT ![c] = "none"]
  /\ push' = [push EXCEPT ![c] = "none"]
  /\ claim' = [claim EXCEPT ![c] = "held"]
  /\ fin' = [fin EXCEPT ![c] = "none"]
  /\ rep' = [rep EXCEPT ![c] = "none"]
  /\ prov' = [prov EXCEPT ![c] = FALSE]
  /\ rds' = [rds EXCEPT ![c] = @ + 1]
  /\ \E r \in RedealRoutes(c) : rt' = [rt EXCEPT ![c] = r]
  /\ UNCHANGED <<att, earlier, tried, fw, bj>>

\* The take that ends with the bound reached retires the card: one judgment, the
\* bound's, naming the provider and the last error; never a failed-work one.
Retire(c) ==
  /\ fin[c] = "provider" /\ rds[c] = MaxRedeals
  /\ fin' = [fin EXCEPT ![c] = "retired"]
  /\ rep' = [rep EXCEPT ![c] = "retired"]
  /\ bj' = [bj EXCEPT ![c] = @ + 1]
  /\ UNCHANGED <<ph, att, earlier, from, commit, shape, verdict, push, claim, prov, rt, tried, rds, fw>>

Next == \E c \in Cards : Stage(c) \/ ChildEnds(c) \/ Push(c) \/ Judge(c) \/ Move(c) \/ Reap(c)
                          \/ Redeal(c) \/ Retire(c)

Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

TypeOK ==
  /\ ph \in [Cards -> {"new", "running", "ended", "pushed", "done"}]
  /\ from \in [Cards -> [k : {"none", "base", "head", "branch"}, n : 0..2]]
  /\ push \in [Cards -> {"none", "ok", "refused", "nocommit"}]
  /\ fin \in [Cards -> {"none", "ok", "failed", "reaped", "provider", "retired"}]
  /\ rep \in [Cards -> {"none", "ok", "failed", "provider", "retired"}]
  /\ rt \in [Cards -> Routes]
  /\ tried \in [Cards -> SUBSET Routes]
  /\ rds \in [Cards -> 0..(MaxRedeals + 1)]
  /\ fw \in [Cards -> 0..1]
  /\ bj \in [Cards -> 0..1]

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

\* A provider failure never opens a failed-work judgment: a run the provider failed
\* that left no result, its push not refused, is never finished `failed`.
ProviderIsNotFailedWork ==
  \A c \in Cards : fin[c] = "failed" => ~(prov[c] /\ ~shape[c] /\ push[c] # "refused")

\* The redeals the bound allows, and no more.
RedealBound == \A c \in Cards : rds[c] <= MaxRedeals

\* The bound retires a card with one judgment, only at the bound, and the card is not
\* judged failed work: a retired card was never finished `failed`.
BoundJudgedOnce ==
  \A c \in Cards :
    /\ bj[c] <= 1
    /\ (fin[c] = "retired") <=> (bj[c] = 1)
    /\ fin[c] = "retired" => rds[c] = MaxRedeals

\* A redeal leaves out the routes a take of the card failed on while another remains.
RedealAvoidsFailedRoute ==
  \A c \in Cards : (ph[c] = "new" /\ rds[c] > 0 /\ tried[c] # Routes) => rt[c] \notin tried[c]

\* A redeal is the same attempt: the earlier attempts' heads stay the ones the launch is
\* staged from, so a redealt launch is staged from the same base as the first (the
\* record of what staging checked out is the unstaged one until it is staged again).
RedealRestagesTheSameBase ==
  \A c \in Cards : (ph[c] = "running" /\ rds[c] > 0 /\ earlier[c] # {}) => from[c].k = "head"

\* Every launch ends judged, reaped or retired (a provider finish is dealt again, and
\* the bound ends the dealing).
EveryLaunchEnds == <>(\A c \in Cards : ph[c] = "done" /\ fin[c] \in {"ok", "failed", "reaped", "retired"})
=============================================================================
