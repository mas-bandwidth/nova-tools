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
\*   rep       what the sprint was told: none, ok, failed, provider, retired
\*   prov      the child's run ended on the provider's failure: the harness's
\*             own log carries a provider error, or its transcript stops on a
\*             tool result after a clean exit (cmd/nova-swarm nativeprovider.go)
\*   rt        the route the take runs on; tried the routes a take of the card
\*             failed on (the card records the route that failed)
\*   rds       the card's redeals (internal/sprint steps_tick.go, MaxRedeals)
\*   fw        the failed-work judgments the sprint opened for the card
\*   bj        the bound judgments the sprint opened for the card
\*
\* THE PROVIDER FAILURE. A run that ended with no result and whose provider
\* failed it (prov) is finished `provider`, after a refused push (which is
\* said first) and before any failed-work reason: Judge. The sprint treats the
\* finish as an ended take (internal/sprint providerEnded; tla/DirtyTick.tla
\* RedealsAreEndedTakes): Redeal returns the card to the deal with the route
\* that failed recorded, and the route the redeal draws leaves it out when
\* another remains (route.go routeOf); it counts toward MaxRedeals, and the
\* take that ends with the bound reached is Retire: one judgment, never a
\* failed-work one.
\*
\* BROKEN names a reversed witness, a rule as it was or as it could be
\* written wrong, that TLC must break: "today" (ok unless the push was
\* refused: the finish of 2026-09-30 that sent a card with no commit to
\* review), "noshape" (ok without the result's shape), "pushignored" (ok on
\* the child's word with the push refused), "reapreported" (a reaped launch
\* reported failed), "branchname" (attempt 2 staged from the previous
\* attempt's branch by name, pushed or not), "providerfailed" (a provider
\* failure judged as failed work), "sameroute" (a redeal on the route that
\* failed though another remains), "unbounded" (a redeal past MaxRedeals).
\* "none" is the design.
\*
\* ONE CARD IS THE INSTANCE. Every action touches one card and no invariant relates
\* two, so the cards are independent: an invariant of the form "for every card"
\* holds on the cards when it holds on one, and two cards only square the states
\* (the redeals made two cards past the bench's budget at 7 million states).
\*
\* WHAT IS NOT MODELLED. The shims' parsing of each command (unit and
\* functional tests hold it), the pull request (it never changes the finish),
\* reads (a read's verdict is reported as the reader gives it), more than two
\* attempts, the store's own refusal of a stale finish (SprintEvents and
\* DirtyTick hold it).
EXTENDS FiniteSets, Naturals

CONSTANTS Cards, Broken, Routes, MaxRedeals

VARIABLES ph, att, prev, from, commit, shape, verdict, push, claim, fin, rep,
          prov, rt, tried, rds, fw, bj

vars == <<ph, att, prev, from, commit, shape, verdict, push, claim, fin, rep,
          prov, rt, tried, rds, fw, bj>>

\* The variables the provider failure adds, for the actions that leave them.
pvars == <<prov, rt, tried, rds, fw, bj>>

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
  /\ prov = [c \in Cards |-> FALSE]
  /\ rt \in [Cards -> Routes]
  /\ tried = [c \in Cards |-> {}]
  /\ rds = [c \in Cards |-> 0]
  /\ fw = [c \in Cards |-> 0]
  /\ bj = [c \in Cards |-> 0]

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
  /\ UNCHANGED <<att, prev, commit, shape, verdict, push, claim, fin, rep, pvars>>

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
  /\ UNCHANGED <<att, prev, from, push, claim, fin, rep, rt, tried, rds, fw, bj>>

\* The member's push (cmd/nova-swarm gitPusher.Push): a commit is pushed or
\* refused; no commit is nothing to push.
Push(c) ==
  /\ ph[c] = "ended" /\ claim[c] = "held"
  /\ ph' = [ph EXCEPT ![c] = "pushed"]
  /\ \E p \in IF commit[c] THEN {"ok", "refused"} ELSE {"nocommit"} :
       push' = [push EXCEPT ![c] = p]
  /\ UNCHANGED <<att, prev, from, commit, shape, verdict, claim, fin, rep, pvars>>

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
  /\ UNCHANGED <<att, prev, from, commit, shape, verdict, push, claim, prov, rt, rds, bj>>

\* The claim moves under the launch at any time before it is judged.
Move(c) ==
  /\ claim[c] = "held" /\ ph[c] \in {"running", "ended", "pushed"}
  /\ claim' = [claim EXCEPT ![c] = "moved"]
  /\ UNCHANGED <<ph, att, prev, from, commit, shape, verdict, push, fin, rep, pvars>>

\* REAPED is its own finish (internal/member FinishReaped): the child has
\* ended, its claim is gone, and nothing is reported.
Reap(c) ==
  /\ claim[c] = "moved" /\ ph[c] \in {"ended", "pushed"}
  /\ ph' = [ph EXCEPT ![c] = "done"]
  /\ fin' = [fin EXCEPT ![c] = "reaped"]
  /\ rep' = IF Broken = "reapreported" THEN [rep EXCEPT ![c] = "failed"] ELSE rep
  /\ UNCHANGED <<att, prev, from, commit, shape, verdict, push, claim, pvars>>

\* THE SPRINT'S SIDE OF A PROVIDER FINISH (internal/sprint providerEnded, then the
\* deal's redeal). The take ended: the card returns to the deal, the launch is
\* new again with the same attempt, the redeal is counted, and the route it draws
\* leaves out every route a take of it failed on while another remains.
RedealRoutes(c) ==
  IF Broken = "sameroute" THEN {rt[c]}
  ELSE IF tried[c] # Routes THEN Routes \ tried[c] ELSE Routes

Redeal(c) ==
  /\ fin[c] = "provider"
  /\ (rds[c] < MaxRedeals \/ Broken = "unbounded")
  /\ ph' = [ph EXCEPT ![c] = "new"]
  /\ from' = [from EXCEPT ![c] = "none"]
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
  /\ UNCHANGED <<att, prev, tried, fw, bj>>

\* The take that ends with the bound reached retires the card: one judgment, the
\* bound's, naming the provider and the last error; never a failed-work one.
Retire(c) ==
  /\ fin[c] = "provider" /\ rds[c] = MaxRedeals
  /\ fin' = [fin EXCEPT ![c] = "retired"]
  /\ rep' = [rep EXCEPT ![c] = "retired"]
  /\ bj' = [bj EXCEPT ![c] = @ + 1]
  /\ UNCHANGED <<ph, att, prev, from, commit, shape, verdict, push, claim, prov, rt, tried, rds, fw>>

Next == \E c \in Cards : Stage(c) \/ ChildEnds(c) \/ Push(c) \/ Judge(c) \/ Move(c) \/ Reap(c)
                          \/ Redeal(c) \/ Retire(c)

Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

TypeOK ==
  /\ ph \in [Cards -> {"new", "running", "ended", "pushed", "done"}]
  /\ from \in [Cards -> {"none", "base", "prevhead", "prevbranch"}]
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

\* A launch is staged from a commit origin holds: the base, or the previous
\* attempt's head only when it was pushed.
StagedFromOrigin == \A c \in Cards : from[c] \in {"prevhead", "prevbranch"} => prev[c]

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

\* Every launch ends judged, reaped or retired (a provider finish is dealt again, and
\* the bound ends the dealing).
EveryLaunchEnds == <>(\A c \in Cards : ph[c] = "done" /\ fin[c] \in {"ok", "failed", "reaped", "retired"})
=============================================================================
