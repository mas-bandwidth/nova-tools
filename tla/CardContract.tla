---------------------------- MODULE CardContract ----------------------------
\* The card contract's finish (docs/SPEC-CARD-CONTRACT.md), written beside
\* pkg/member's Judge and pkg/cardcontract's frame: what a launch is
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
\*   rep       what the sprint was told: none, ok, failed, retired
\*   cls       a failed finish's class, its reason prefix (pkg/member Judge):
\*             failed (failed work, the coordinator's to judge), provider or
\*             noresult (a redeal class, the sprint deals the card again) or
\*             staging (a restage class, the sprint deals it to another member);
\*             none before any judgment and for ok and reaped
\*   prov      the child's run ended on the provider's failure: the harness's
\*             own log carries a provider error, or its transcript stops on a
\*             tool result after a clean exit (cmd/nova-swarm nativeprovider.go)
\*   prv       the provider named a reason (its Provider is not empty); FALSE
\*             when the launch is one the provider never accepted
\*   unv       the run ended on a budget native could no longer see
\*             (EndUnverifiable, cmd/nova-swarm native): with no result it is a
\*             staging refusal, the member's failure, never the card's
\*   rt        the route the take runs on; tried the routes a take of the card
\*             failed on (the card records the route that failed)
\*   rds       the work card's redeals (nova-sprint steps_tick.go, MaxRedeals)
\*   fw        the failed-work judgments the sprint opened for the card
\*   bj        the bound judgments the sprint opened for the card
\*   stg       the launch was refused at staging, before any child ran
\*             (cmd/nova-swarm native's STAGE FAIL: no bench mirror, the pushed
\*             head missing, the stage's timeout)
\*   mem       the member the take is dealt to (the up members are Members)
\*   refused   the members that refused the card at staging
\*   sj        the staging-bound judgments the sprint opened for the card
\*   lvd       the card was levelled onto another member since it was last dealt
\*
\* THE CLASSES OF A FAILED FINISH. Judge's finish is ok, failed or reaped;
\* provider, noresult and staging are a failed finish's classes, its reason
\* prefix, never finishes. A run whose provider failed it with a reason (prov
\* and prv) and that left no result, its push not refused, is the `provider`
\* class, and a run that ended by itself, within its budget and deadline,
\* having written no result, is the `noresult` class: both are redeal classes,
\* the sprint deals the card again and judges nothing (nova-sprint
\* providerEnded, then the deal's redeal; tla/DirtyTick.tla
\* RedealsAreEndedTakes): Redeal returns the work card to the deal, the same
\* attempt, with the route that failed recorded, and the route the redeal draws
\* leaves it out when another remains (route.go routeOf); it counts toward
\* MaxRedeals, and the take that ends with the bound reached is Retire: one
\* judgment, never a failed-work one. A provider failure with no reason (a
\* launch the provider never accepted) is failed work, as is a run its budget
\* or deadline ended with no result. A redeal is the same attempt: att and
\* earlier do not move, so the next launch is staged exactly as the first was
\* (StageFrom); redeals count per work card, and a rework (the next attempt, up
\* to three) is a new work card with a count of its own, outside this model.
\*
\* THE STAGING REFUSAL. A launch its member refused at staging ran no child: no
\* commit, no result, nothing pushed (StageRefused). Judge finishes it failed with
\* the `staging` class, never failed work (pkg/member Judge, EndStaging); a
\* budget-unverifiable end that left no result is the same class (Judge, its
\* EndUnverifiable case). The sprint withdraws the card without an ended take
\* (nova-sprint stagingRefused), so Restage deals it to a member that has not
\* refused it, the same attempt, and spends none of MaxRedeals: the member's
\* failure, never the card's. Its own bound is the fleet: each member refuses the
\* card at most once (the deal never places it on one that did, nor does a
\* provider Redeal), and when every member has refused it, StagingAll is the one
\* judgment the coordinator sees (nova-sprint AtStagingBound; the bound's
\* judgment). The refusal is a separate count from rds, not the same: the redeal
\* bound measures what a card's takes cost (each ended take ran a child), while a
\* refusal costs nothing and is bounded by the fleet already, so counting it in rds
\* would retire a sound card for its members' machines (the fleet store,
\* 2026-10-01: three refusals spent the bound).
\* Level (nova-sprint level, round.levelTo) moves a dealt card that is not yet
\* taken to another member to even the queues; it too skips the card's refusers
\* (the reader's probe on #5000: the level moved a card back onto its refuser every
\* tick, without end). Levelling is bounded here to once per deal: the code moves a
\* card only while queues differ by more than one, which one card cannot express.
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
\* pushed work lost in silence), "providerfailed" (a provider failure judged as
\* failed work), "sameroute" (a redeal on the route that failed though another
\* remains), "unbounded" (a redeal past MaxRedeals), "stagingfailed" (a staging
\* refusal judged as failed work), "noresultfailed" (a no-result end judged as
\* failed work), "levelrefuser" (the level moves a card onto a
\* member that refused it). "none" is the design.
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
\* DirtyTick hold it), whether a carry applies (git's merge decides it; both
\* outcomes are open to every rework here), and a base that is a sha or a tag,
\* which never moves and whose rework is staged at the pushed head itself (the
\* base modelled is a branch, as every sprint stream's is).
EXTENDS Integers, FiniteSets

CONSTANTS Cards, Broken, Routes, MaxRedeals, Members

VARIABLES ph, att, earlier, from, commit, shape, verdict, push, claim, fin, rep, cls,
          prov, prv, unv, rt, tried, rds, fw, bj, stg, mem, refused, sj, lvd

vars == <<ph, att, earlier, from, commit, shape, verdict, push, claim, fin, rep, cls,
          prov, prv, unv, rt, tried, rds, fw, bj, stg, mem, refused, sj, lvd>>

\* The variables the staging refusal adds, for the actions that leave them.
svars == <<stg, mem, refused, sj, lvd>>

\* The variables the provider failure and the staging refusal add, for the actions that
\* leave them.
pvars == <<prov, prv, unv, rt, tried, rds, fw, bj, svars>>

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
  /\ cls = [c \in Cards |-> "none"]
  /\ prov = [c \in Cards |-> FALSE]
  /\ prv = [c \in Cards |-> FALSE]
  /\ unv = [c \in Cards |-> FALSE]
  /\ rt \in [Cards -> Routes]
  /\ tried = [c \in Cards |-> {}]
  /\ rds = [c \in Cards |-> 0]
  /\ fw = [c \in Cards |-> 0]
  /\ bj = [c \in Cards |-> 0]
  /\ stg = [c \in Cards |-> FALSE]
  /\ mem \in [Cards -> Members]
  /\ refused = [c \in Cards |-> {}]
  /\ sj = [c \in Cards |-> 0]
  /\ lvd = [c \in Cards |-> FALSE]

MaxOf(S) == CHOOSE m \in S : \A n \in S : n <= m

\* Staging (cmd/nova-swarm frameOf, pkg/swarm StageCard and restageAtTip): a first
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
  /\ UNCHANGED <<att, earlier, commit, shape, verdict, push, claim, fin, rep, cls, pvars>>

\* The member's machine refuses the launch at staging (cmd/nova-swarm native's STAGE
\* FAIL): nothing is staged and no child runs, so there is no commit and no result.
StageRefused(c) ==
  /\ ph[c] = "new"
  /\ ph' = [ph EXCEPT ![c] = "ended"]
  /\ stg' = [stg EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim, fin, rep, cls,
                 prov, prv, unv, rt, tried, rds, fw, bj, mem, refused, sj, lvd>>

\* The child ends: any commit, any result, any verdict, and the provider's
\* failure or not.
ChildEnds(c) ==
  /\ ph[c] = "running"
  /\ ph' = [ph EXCEPT ![c] = "ended"]
  /\ \E k \in BOOLEAN, s \in BOOLEAN, v \in {"ok", "notdone"}, f \in BOOLEAN, p \in BOOLEAN, u \in BOOLEAN :
       /\ commit' = [commit EXCEPT ![c] = k]
       /\ shape' = [shape EXCEPT ![c] = s]
       /\ verdict' = [verdict EXCEPT ![c] = v]
       /\ prov' = [prov EXCEPT ![c] = f]
       /\ prv' = [prv EXCEPT ![c] = p]
       /\ unv' = [unv EXCEPT ![c] = u]
  /\ UNCHANGED <<att, earlier, from, push, claim, fin, rep, cls, rt, tried, rds, fw, bj, svars>>

\* The member's push (cmd/nova-swarm gitPusher.Push): a commit is pushed or
\* refused; no commit is nothing to push.
Push(c) ==
  /\ ph[c] = "ended" /\ claim[c] = "held"
  /\ ph' = [ph EXCEPT ![c] = "pushed"]
  /\ \E p \in IF commit[c] THEN {"ok", "refused"} ELSE {"nocommit"} :
       push' = [push EXCEPT ![c] = p]
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, claim, fin, rep, cls, pvars>>

\* THE ONE JUDGMENT (pkg/member Judge). The finish is ok, failed or reaped;
\* provider, noresult and staging are a failed finish's classes (its reason
\* prefix, member.go), never finishes: only the `failed` class is failed work,
\* the coordinator's to judge; `provider` and `noresult` are redeal classes (the
\* sprint deals the card again); `staging` is a restage class (the sprint deals
\* it to another member).
JudgeOf(c) ==
  CASE Broken = "today" -> IF push[c] = "refused" THEN "failed" ELSE "ok"
    [] Broken = "noshape" -> IF push[c] = "ok" /\ verdict[c] = "ok" THEN "ok" ELSE "failed"
    [] Broken = "pushignored" -> IF shape[c] /\ verdict[c] = "ok" /\ commit[c] THEN "ok" ELSE "failed"
    [] OTHER -> IF shape[c] /\ verdict[c] = "ok" /\ push[c] = "ok" THEN "ok" ELSE "failed"

ClassOf(c) ==
  IF stg[c] THEN IF Broken = "stagingfailed" THEN "failed" ELSE "staging"
  ELSE IF unv[c] /\ ~shape[c] THEN IF Broken = "stagingfailed" THEN "failed" ELSE "staging"
  ELSE IF push[c] = "refused" THEN "failed"
  ELSE IF prov[c] /\ prv[c] /\ ~shape[c] THEN IF Broken = "providerfailed" THEN "failed" ELSE "provider"
  ELSE IF ~prov[c] /\ ~unv[c] /\ ~shape[c] THEN IF Broken = "noresultfailed" THEN "failed" ELSE "noresult"
  ELSE "failed"

JudgedClass(c) == IF JudgeOf(c) = "ok" THEN "none" ELSE ClassOf(c)

\* A finish `ok` is reported ok; a failed finish is reported failed with its
\* class's reason; only the `failed` class opens the failed-work judgment and
\* only the `provider` class records the route that failed.
Judge(c) ==
  /\ ph[c] = "pushed" /\ claim[c] = "held"
  /\ ph' = [ph EXCEPT ![c] = "done"]
  /\ fin' = [fin EXCEPT ![c] = JudgeOf(c)]
  /\ rep' = [rep EXCEPT ![c] = JudgeOf(c)]
  /\ cls' = [cls EXCEPT ![c] = JudgedClass(c)]
  /\ fw' = [fw EXCEPT ![c] = IF JudgedClass(c) = "failed" THEN @ + 1 ELSE @]
  /\ tried' = [tried EXCEPT ![c] = IF JudgedClass(c) = "provider" THEN @ \cup {rt[c]} ELSE @]
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim, prov, prv, unv, rt, rds, bj, svars>>

\* The claim moves under the launch at any time before it is judged.
Move(c) ==
  /\ claim[c] = "held" /\ ph[c] \in {"running", "ended", "pushed"}
  /\ claim' = [claim EXCEPT ![c] = "moved"]
  /\ UNCHANGED <<ph, att, earlier, from, commit, shape, verdict, push, fin, rep, cls, pvars>>

\* REAPED is its own finish (pkg/member FinishReaped): the child has
\* ended, its claim is gone, and nothing is reported.
Reap(c) ==
  /\ claim[c] = "moved" /\ ph[c] \in {"ended", "pushed"}
  /\ ph' = [ph EXCEPT ![c] = "done"]
  /\ fin' = [fin EXCEPT ![c] = "reaped"]
  /\ rep' = IF Broken = "reapreported" THEN [rep EXCEPT ![c] = "failed"] ELSE rep
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim, cls, pvars>>

\* THE SPRINT'S SIDE OF A PROVIDER FINISH (nova-sprint providerEnded, then the
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
  /\ \E m \in Members \ refused[c] : mem' = [mem EXCEPT ![c] = m]
  /\ lvd' = [lvd EXCEPT ![c] = FALSE]
  /\ UNCHANGED <<att, earlier, tried, fw, bj, stg, refused, sj, cls, prv, unv>>

\* The take that ends with the bound reached retires the card: one judgment, the
\* bound's, naming the provider and the last error; never a failed-work one.
Retire(c) ==
  /\ fin[c] = "provider" /\ rds[c] = MaxRedeals
  /\ fin' = [fin EXCEPT ![c] = "retired"]
  /\ rep' = [rep EXCEPT ![c] = "retired"]
  /\ bj' = [bj EXCEPT ![c] = @ + 1]
  /\ UNCHANGED <<ph, att, earlier, from, commit, shape, verdict, push, claim, prov, prv, unv, rt, tried, rds, fw, cls, svars>>

\* THE SPRINT'S SIDE OF A STAGING FINISH (nova-sprint stagingRefused, then the deal's
\* redeal). The member is recorded as having refused the card; the deal places it on a
\* member that has not, the same attempt, on the same route, and the redeal bound is not
\* spent (rds does not move).
Restage(c) ==
  /\ fin[c] = "staging"
  /\ Members \ (refused[c] \cup {mem[c]}) # {}
  /\ ph' = [ph EXCEPT ![c] = "new"]
  /\ from' = [from EXCEPT ![c] = [k |-> "none", n |-> 0]]
  /\ push' = [push EXCEPT ![c] = "none"]
  /\ claim' = [claim EXCEPT ![c] = "held"]
  /\ fin' = [fin EXCEPT ![c] = "none"]
  /\ rep' = [rep EXCEPT ![c] = "none"]
  /\ stg' = [stg EXCEPT ![c] = FALSE]
  /\ refused' = [refused EXCEPT ![c] = @ \cup {mem[c]}]
  /\ \E m \in Members \ (refused[c] \cup {mem[c]}) : mem' = [mem EXCEPT ![c] = m]
  /\ lvd' = [lvd EXCEPT ![c] = FALSE]
  /\ UNCHANGED <<att, earlier, commit, shape, verdict, prov, prv, unv, rt, tried, rds, fw, bj, sj, cls>>

\* Every member has refused the card at staging: one judgment, the bound's, naming the
\* members and the reason; the card is not dealt again (nova-sprint AtStagingBound).
StagingAll(c) ==
  /\ fin[c] = "staging" /\ Members \subseteq refused[c] \cup {mem[c]}
  /\ fin' = [fin EXCEPT ![c] = "stagingall"]
  /\ rep' = [rep EXCEPT ![c] = "stagingall"]
  /\ refused' = [refused EXCEPT ![c] = @ \cup {mem[c]}]
  /\ sj' = [sj EXCEPT ![c] = @ + 1]
  /\ UNCHANGED <<ph, att, earlier, from, commit, shape, verdict, push, claim, prov, prv, unv, rt, tried, rds, fw, bj, stg, mem, lvd, cls>>

\* THE LEVEL (nova-sprint level): a card dealt and not yet taken moves to another
\* member, never one that refused it at staging; once per deal here.
LevelTargets(c) ==
  IF Broken = "levelrefuser" THEN Members \ {mem[c]}
  ELSE Members \ (refused[c] \cup {mem[c]})

Level(c) ==
  /\ ph[c] = "new" /\ claim[c] = "held" /\ ~lvd[c]
  /\ \E m \in LevelTargets(c) : mem' = [mem EXCEPT ![c] = m]
  /\ lvd' = [lvd EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<ph, att, earlier, from, commit, shape, verdict, push, claim, fin, rep, cls,
                 prov, prv, unv, rt, tried, rds, fw, bj, stg, refused, sj>>

Next == \E c \in Cards : Stage(c) \/ ChildEnds(c) \/ Push(c) \/ Judge(c) \/ Move(c) \/ Reap(c)
                          \/ Redeal(c) \/ Retire(c) \/ StageRefused(c) \/ Restage(c) \/ StagingAll(c) \/ Level(c)

Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

TypeOK ==
  /\ ph \in [Cards -> {"new", "running", "ended", "pushed", "done"}]
  /\ from \in [Cards -> [k : {"none", "base", "head", "branch", "carry", "tip"}, n : 0..2]]
  /\ push \in [Cards -> {"none", "ok", "refused", "nocommit"}]
  /\ fin \in [Cards -> {"none", "ok", "failed", "reaped", "retired", "stagingall"}]
  /\ rep \in [Cards -> {"none", "ok", "failed", "retired", "stagingall"}]
  /\ cls \in [Cards -> {"none", "failed", "provider", "noresult", "staging"}]
  /\ prov \in [Cards -> BOOLEAN]
  /\ prv \in [Cards -> BOOLEAN]
  /\ unv \in [Cards -> BOOLEAN]
  /\ rt \in [Cards -> Routes]
  /\ tried \in [Cards -> SUBSET Routes]
  /\ rds \in [Cards -> 0..(MaxRedeals + 1)]
  /\ fw \in [Cards -> 0..1]
  /\ bj \in [Cards -> 0..1]
  /\ stg \in [Cards -> BOOLEAN]
  /\ mem \in [Cards -> Members]
  /\ refused \in [Cards -> SUBSET Members]
  /\ sj \in [Cards -> 0..1]
  /\ lvd \in [Cards -> BOOLEAN]

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

\* A provider failure with a reason never opens a failed-work judgment: a run the provider
\* failed that left no result, its push not refused, is a redeal class, never the failed
\* class (failed work). A provider failure with no reason (a launch the provider never
\* accepted, member.go) is failed work.
ProviderIsNotFailedWork ==
  \A c \in Cards : cls[c] = "failed" => ~(prov[c] /\ prv[c] /\ ~shape[c] /\ push[c] # "refused")

\* A staging refusal never opens a failed-work judgment: a launch refused at staging ran no
\* child, and a budget-unverifiable end that left no result is the member's failure too; both
\* are the staging class, never failed work.
StagingIsNotFailedWork ==
  \A c \in Cards : cls[c] = "failed" => ~(stg[c] \/ (unv[c] /\ ~shape[c]))

\* A no-result end never opens a failed-work judgment: a run that ended by itself, within
\* its budget and deadline, having written no result, is a redeal class, never failed work.
NoResultIsNotFailedWork ==
  \A c \in Cards : cls[c] = "failed" => ~(push[c] # "refused" /\ ~stg[c] /\ ~prov[c] /\ ~unv[c] /\ ~shape[c])

\* A take is never on a member that refused the card at staging.
NeverOnARefuser == \A c \in Cards : ph[c] \in {"new", "running"} => mem[c] \notin refused[c]

\* Every member refusing the card is one judgment, only then, and the card is not judged
\* failed work.
StagingBoundJudgedOnce ==
  \A c \in Cards :
    /\ sj[c] <= 1
    /\ (fin[c] = "stagingall") <=> (sj[c] = 1)
    /\ fin[c] = "stagingall" => refused[c] = Members

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

\* A redeal is the same attempt: the earlier attempts' heads stay the ones the launch carries,
\* so a redealt launch carries the same head as the first, onto the tip as it is then (the
\* record of what staging checked out is the unstaged one until it is staged again).
RedealRestagesTheSameBase ==
  \A c \in Cards : (ph[c] = "running" /\ rds[c] > 0 /\ earlier[c] # {}) =>
    (from[c].k \in {"carry", "tip"} /\ from[c].n = MaxOf(earlier[c]))

\* Every launch ends judged, reaped or retired (a provider finish is dealt again, and
\* the bound ends the dealing; a staging finish is dealt to another member, and the fleet
\* ends the dealing).
EveryLaunchEnds == <>(\A c \in Cards : ph[c] = "done" /\ fin[c] \in {"ok", "failed", "reaped", "retired", "stagingall"})
=============================================================================
