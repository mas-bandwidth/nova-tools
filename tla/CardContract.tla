---------------------------- MODULE CardContract ----------------------------
\* The card contract's finish (docs/SPEC-CARD-CONTRACT.md), written beside
\* pkg/member's Judge and pkg/cardcontract's frame (contract.go, gate.go):
\* what a launch is staged from, how its child ends, the member's push, and
\* the one judgment that turns them into the finish the sprint is told.

\* THE STATE, per card.
\*   ph        the launch: new, running (staged, the child at work), ended
\*             (the child's end is written), pushed (the member's push is
\*             made), reported (the report is sent, its result not yet
\*             collected), done (judged or reaped)
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
\*   fin       the finish: none, or one of the code's three, ok, failed,
\*             reaped (member.go Finish; Judge names ok and failed, and
\*             FinishReaped is the moved claim). The values provider and
\*             staging are the failed finish whose reason carries that redeal
\*             or staging prefix (below), named apart because the sprint acts
\*             on the prefix; they are not finishes of their own
\*   rep       what the sprint was told: none, ok, failed, provider, staging
\*   prov      the child's run ended on the provider's failure and the record
\*             names the provider's reason (EndProvider with a Provider): the
\*             harness's own log carries a provider error, or its transcript
\*             stops on a tool result after a clean exit (pkg/swarm's provider
\*             cause). A provider end with no Provider is not prov: it is
\*             failed work ("no RESULT.md shape")
\*   nr        the child ended by itself, within its budget and deadline, and
\*             left no result (EndNoResult): no RESULT.md shape, or only its
\*             first RESULT.md (verdict not-done, nothing committed, no gate,
\*             inside FirstResultWall); a redeal class like a provider
\*             failure, never failed work (member.go Judge)
\*   stg       a staging refusal: the launch was refused at staging, before any
\*             child ran (native's STAGE FAIL: no bench mirror, the pushed head
\*             missing, the stage's timeout), or the child's end was
\*             budget-unverifiable and left no result (EndUnverifiable)
\*   lf        a run with no result whose assistant turn was cut off at the
\*             harness output limit; a harness fault, the staging class
\*   answer    the store's answer to the report (the finish verb): none (no
\*             report collected yet), accepted (code 0, the store took it:
\*             the finish was the card's claim when it landed, so a move seen
\*             after is the store acting on it, a failed finish redealt at
\*             gen+1), refused (the store did not take it: the result is
\*             nobody's). An unknown outcome (no answer, changed=unknown,
\*             keepUncertain) keeps the run as failed and is below this
\*             model's grain
\*   stageAnchor   the immutable commit recorded by native when this launch is
\*                 staged; update-ref can move checkout refs but not this record
\*   headDescends  whether the result's current head descends from stageAnchor
\*   originUpdated  whether this launch updated its target branch
\*   originDescends whether the pushed origin head descends from stageAnchor

\* THE REASON PREFIXES (ProviderFailure, the name pkg/member/member.go,
\* cmd/nova-swarm/nativeprovider.go, their tests and SPEC-CARD-CONTRACT.md
\* cite). Judge names one of three finishes, ok, failed or reaped
\* (pkg/member/member.go, Finish), and the failed finish's reason carries the
\* prefix that classes it. A run that ended with no result and whose provider
\* failed it with a reason (prov) is failed `provider failure`, after a refused
\* push (which is said first) and before any failed-work reason. A run the child
\* ended by itself with no result (nr: no shape at all, or only its first
\* RESULT.md, not-done with nothing committed) is failed `no result`. Both are
\* redeal classes: the sprint treats the finish as an ended take (nova-sprint's
\* internal/sprint/steps_work.go IsProviderFailure and IsNoResult;
\* tla/DirtyTick.tla RedealsAreEndedTakes) and deals the card
\* again without judging it: the redeal and the retire at the bound are the
\* sprint's side, outside this model. A provider end with no reason
\* (EndProvider, empty Provider), and a run its budget or deadline ended with no
\* result, are failed work, the end said first, never a redeal.

\* THE STAGING REFUSAL. A launch its member refused at staging ran no child: no
\* commit, no result, nothing pushed (StageRefused), and a run the member's
\* usage source ended budget-unverifiable with no result is the same refusal.
\* Judge finishes both failed `staging refused`, its own kind and never failed
\* work (pkg/member/member.go, EndStaging); the sprint withdraws the card
\* without an ended take (nova-sprint's stagingRefused): the restage to a member
\* that has not refused it is the sprint's side, outside this model.

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
\* failed work), "noresultfailed" (a no-result end judged as failed work),
\* "stagingfailed" (a staging refusal judged as failed work), "lengthnoresult"
\* (a cutoff judged as a no-result redeal instead of a no-cost staging fault), "applymoved"
\* (a report applied on a moved claim), "reapaccepted" (an accepted report
\* reaped), and "rewritepushed" (the update-ref bypass rewrites the checkout
\* below the recorded stage and the member pushes that unrelated head anyway).
\* "none" is the design.

\* ONE CARD IS THE INSTANCE. Every action touches one card and no invariant relates
\* two, so the cards are independent: an invariant of the form "for every card"
\* holds on the cards when it holds on one, and two cards only square the states.

\* WHAT IS NOT MODELLED. The shims' parsing of each command (unit and
\* functional tests hold it), the pull request (it never changes the finish),
\* reads (a read's verdict is reported as the reader gives it), more than three
\* attempts, the store's own refusal of a stale finish (SprintEvents and
\* DirtyTick hold it), whether a carry applies (git's merge decides it; both
\* outcomes are open to every rework here), and a base that is a sha or a tag,
\* which never moves and whose rework is staged at the pushed head itself (the
\* base modelled is a branch, as every sprint stream's is).
EXTENDS Integers, FiniteSets

CONSTANTS Cards, Broken

VARIABLES ph, att, earlier, from, commit, shape, verdict, push, claim, fin, rep,
          prov, nr, stg, lf, answer, stageAnchor, headDescends, originUpdated, originDescends

vars == <<ph, att, earlier, from, commit, shape, verdict, push, claim, fin, rep,
          prov, nr, stg, lf, answer, stageAnchor, headDescends, originUpdated, originDescends>>

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
  /\ nr = [c \in Cards |-> FALSE]
  /\ stg = [c \in Cards |-> FALSE]
  /\ lf = [c \in Cards |-> FALSE]
  /\ answer = [c \in Cards |-> "none"]
  /\ stageAnchor = [c \in Cards |-> FALSE]
  /\ headDescends = [c \in Cards |-> FALSE]
  /\ originUpdated = [c \in Cards |-> FALSE]
  /\ originDescends = [c \in Cards |-> FALSE]

MaxOf(S) == CHOOSE m \in S : \A n \in S : n <= m

\* Staging (pkg/swarm StageCard and restageAtTip): a first
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
  /\ stageAnchor' = [stageAnchor EXCEPT ![c] = TRUE]
  /\ headDescends' = [headDescends EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<att, earlier, commit, shape, verdict, push, claim, fin, rep, prov, nr, stg, lf, answer, originUpdated, originDescends>>

\* The member's machine refuses the launch at staging (native's STAGE
\* FAIL): nothing is staged and no child runs, so there is no commit and no result.
StageRefused(c) ==
  /\ ph[c] = "new"
  /\ ph' = [ph EXCEPT ![c] = "ended"]
  /\ stg' = [stg EXCEPT ![c] = TRUE]
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim, fin, rep, prov, nr, lf, answer, stageAnchor, headDescends, originUpdated, originDescends>>

\* The child ends: any commit, any result, any verdict, and the run's end class:
\* a provider failure with a reason (prov), a no-result end (nr), or a
\* budget-unverifiable staging refusal (stg), or output-limit harness fault (lf); at most one. A budget-unverifiable
\* end in the class leaves no shape, and a no-result end leaves none or only its
\* first RESULT.md: verdict not-done and no commit (member.go Judge, its
\* first-RESULT case: pu.Sha empty, so nothing the member could push).
ChildEnds(c) ==
  /\ ph[c] = "running"
  /\ ph' = [ph EXCEPT ![c] = "ended"]
  /\ \E k \in BOOLEAN, s \in BOOLEAN, v \in {"ok", "notdone"},
         f \in BOOLEAN, n \in BOOLEAN, u \in BOOLEAN, l \in BOOLEAN :
       /\ commit' = [commit EXCEPT ![c] = k]
       /\ shape' = [shape EXCEPT ![c] = s]
       /\ verdict' = [verdict EXCEPT ![c] = v]
       /\ prov' = [prov EXCEPT ![c] = f]
       /\ nr' = [nr EXCEPT ![c] = n]
       /\ stg' = [stg EXCEPT ![c] = u]
       /\ lf' = [lf EXCEPT ![c] = l]
       /\ headDescends' = [headDescends EXCEPT ![c] = TRUE]
       /\ (IF f THEN 1 ELSE 0) + (IF n THEN 1 ELSE 0) + (IF u THEN 1 ELSE 0) + (IF l THEN 1 ELSE 0) <= 1
       /\ n => (~s \/ (v = "notdone" /\ ~k))
       /\ u => ~s
       /\ l => ~s
  /\ UNCHANGED <<att, earlier, from, push, claim, fin, rep, answer, stageAnchor, originUpdated, originDescends>>

\* update-ref bypasses the launch's command shim: it can move the checkout's head
\* to work that does not descend from the immutable stage record. The member checks
\* that relation again in gitPusher.Push (memberpush.go, merge-base --is-ancestor).
\* The slot's recorded staged commit is unchanged by this checkout rewrite.
RewriteBelowStage(c) ==
  /\ ph[c] = "ended" /\ commit[c] /\ stageAnchor[c] /\ headDescends[c]
  /\ headDescends' = [headDescends EXCEPT ![c] = FALSE]
  /\ UNCHANGED <<ph, att, earlier, from, commit, shape, verdict, push, claim, fin, rep, prov, nr, stg, lf, answer, stageAnchor, originUpdated, originDescends>>

\* The member's push (gitPusher.Push): a commit is pushed or
\* refused; no commit is nothing to push.
PushOutcomes(c) ==
  IF ~commit[c] THEN {"nocommit"}
  ELSE IF headDescends[c] THEN {"ok", "refused"}
  ELSE IF Broken = "rewritepushed" THEN {"ok", "refused"}
  ELSE {"refused"}

Push(c) ==
  /\ ph[c] = "ended" /\ claim[c] = "held"
  /\ ph' = [ph EXCEPT ![c] = "pushed"]
  /\ \E p \in PushOutcomes(c) :
       /\ push' = [push EXCEPT ![c] = p]
       /\ IF p = "ok"
            THEN /\ originUpdated' = [originUpdated EXCEPT ![c] = TRUE]
                 /\ originDescends' = [originDescends EXCEPT ![c] = headDescends[c]]
            ELSE /\ originUpdated' = originUpdated
                 /\ originDescends' = originDescends
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, claim, fin, rep, prov, nr, stg, lf, answer, stageAnchor, headDescends>>

\* A retry after a refusal has no new checkout or origin effect: it is a stutter.
RetryRefusedPush(c) ==
  /\ ph[c] = "pushed" /\ push[c] = "refused"
  /\ UNCHANGED vars

\* The ok rule: a commit the child made, pushed, with the result's shape and
\* verdict ok. "today" drops the commit (ok unless the push was refused, which
\* was said first above), "noshape" drops the shape.
OkOf(c) ==
  IF Broken = "today" THEN push[c] # "refused"
  ELSE IF Broken = "noshape" THEN verdict[c] = "ok" /\ push[c] = "ok"
  ELSE shape[c] /\ verdict[c] = "ok" /\ push[c] = "ok"

\* THE ONE JUDGMENT (pkg/member/member.go, Judge): three finishes, ok, failed
\* and reaped, with a refused push said first and a staging refusal, a provider
\* failure with a reason, and a no-result end as reason prefixes on failed.
\* Each reversed witness (Broken) differs from the design in its own rule only,
\* never a replacement judgment: "stagingfailed" skips the staging prefix,
\* "pushignored" judges ok on the child's word when the push was refused,
\* "providerfailed" judges a provider failure as failed work, "noresultfailed"
\* judges a no-result end as failed work, and "today" and "noshape" loosen the
\* ok rule (OkOf).
JudgeOf(c) ==
  IF lf[c] THEN (IF Broken = "lengthnoresult" THEN "provider" ELSE "staging")
  ELSE IF stg[c] /\ Broken # "stagingfailed" THEN "staging"
  ELSE IF push[c] = "refused" THEN
         IF Broken = "pushignored" /\ shape[c] /\ verdict[c] = "ok" /\ commit[c]
           THEN "ok"
           ELSE "failed"
  ELSE IF prov[c] /\ ~shape[c] THEN
         IF Broken = "providerfailed" THEN "failed" ELSE "provider"
  ELSE IF nr[c] /\ Broken # "noresultfailed" THEN "provider"
  ELSE IF OkOf(c) THEN "ok"
  ELSE "failed"

\* A finish `failed` with no redeal or staging reason opens the failed-work
\* judgment; a `provider` finish (a provider failure or a no-result end) opens
\* none; a `staging` finish opens none. JudgeOf names the finish.

\* THE SPLIT (pkg/member/member.go, reportOne/reportRun and collectReports/
\* applyReport): the member sends the report (the finish verb) and collects its
\* result in two steps, and the claim can move between them. SendReport sends
\* the report for a pushed launch whose claim is held; CollectReport applies
\* the result against the fresh queue read. A result applies (fin and rep are
\* set to the judgment) only while the claim is still held, or when the store
\* accepted the report (code 0): an accepted finish was the card's claim when
\* it landed, so a move seen after is the store acting on it (a failed finish
\* redealt at gen+1). A refused report whose claim moved is reaped: the result
\* is nobody's.
SendReport(c) ==
  /\ ph[c] = "pushed" /\ claim[c] = "held"
  /\ ph' = [ph EXCEPT ![c] = "reported"]
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim, fin, rep, prov, nr, stg, lf, answer, stageAnchor, headDescends, originUpdated, originDescends>>

\* What a collected report comes to: applied (the judgment, said to the
\* sprint) when the claim is still held, or when the store accepted it; else
\* reaped (the moved claim's result is nobody's). Broken names the reversed
\* witnesses: "applymoved" applies on a moved claim whatever the answer,
\* "reapaccepted" reaps an accepted report.
CollectOf(c, a) ==
  IF Broken = "applymoved" THEN "apply"
  ELSE IF Broken = "reapaccepted" THEN (IF claim[c] = "held" THEN "apply" ELSE "reap")
  ELSE IF claim[c] = "held" \/ a = "accepted" THEN "apply"
  ELSE "reap"

CollectReport(c) ==
  /\ ph[c] = "reported"
  /\ \E a \in {"accepted", "refused"} :
       /\ answer' = [answer EXCEPT ![c] = a]
       /\ IF CollectOf(c, a) = "apply"
            THEN /\ fin' = [fin EXCEPT ![c] = JudgeOf(c)]
                 /\ rep' = [rep EXCEPT ![c] = JudgeOf(c)]
          ELSE /\ fin' = [fin EXCEPT ![c] = "reaped"]
               /\ rep' = [rep EXCEPT ![c] = "none"]
  /\ ph' = [ph EXCEPT ![c] = "done"]
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim, prov, nr, stg, lf, stageAnchor, headDescends, originUpdated, originDescends>>

\* The claim moves under the launch at any time before its report is collected.
Move(c) ==
  /\ claim[c] = "held" /\ ph[c] \in {"running", "ended", "pushed", "reported"}
  /\ claim' = [claim EXCEPT ![c] = "moved"]
  /\ UNCHANGED <<ph, att, earlier, from, commit, shape, verdict, push, fin, rep, prov, nr, stg, lf, answer, stageAnchor, headDescends, originUpdated, originDescends>>

\* REAPED is its own finish (pkg/member/member.go, FinishReaped): the child has
\* ended, its claim is gone, and nothing is reported.
Reap(c) ==
  /\ claim[c] = "moved" /\ ph[c] \in {"ended", "pushed"}
  /\ ph' = [ph EXCEPT ![c] = "done"]
  /\ fin' = [fin EXCEPT ![c] = "reaped"]
  /\ rep' = IF Broken = "reapreported" THEN [rep EXCEPT ![c] = "failed"] ELSE rep
  /\ UNCHANGED <<att, earlier, from, commit, shape, verdict, push, claim, prov, nr, stg, lf, answer, stageAnchor, headDescends, originUpdated, originDescends>>

Next == \E c \in Cards : Stage(c) \/ ChildEnds(c) \/ RewriteBelowStage(c) \/ Push(c) \/ RetryRefusedPush(c) \/ SendReport(c) \/ CollectReport(c) \/ Move(c) \/ Reap(c)
                          \/ StageRefused(c)

Spec == Init /\ [][Next]_vars /\ WF_vars(Next)

TypeOK ==
  /\ ph \in [Cards -> {"new", "running", "ended", "pushed", "reported", "done"}]
  /\ from \in [Cards -> [k : {"none", "base", "head", "branch", "carry", "tip"}, n : 0..2]]
  /\ push \in [Cards -> {"none", "ok", "refused", "nocommit"}]
  /\ fin \in [Cards -> {"none", "ok", "failed", "reaped", "provider", "staging"}]
  /\ rep \in [Cards -> {"none", "ok", "failed", "provider", "staging"}]
  /\ prov \in [Cards -> BOOLEAN]
  /\ nr \in [Cards -> BOOLEAN]
  /\ stg \in [Cards -> BOOLEAN]
  /\ lf \in [Cards -> BOOLEAN]
  /\ answer \in [Cards -> {"none", "accepted", "refused"}]
  /\ stageAnchor \in [Cards -> BOOLEAN]
  /\ headDescends \in [Cards -> BOOLEAN]
  /\ originUpdated \in [Cards -> BOOLEAN]
  /\ originDescends \in [Cards -> BOOLEAN]

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

\* A provider failure never opens a failed-work judgment: a run the provider failed
\* that left no result, its push not refused, is never finished `failed`.
ProviderIsNotFailedWork ==
  \A c \in Cards : fin[c] = "failed" => ~(prov[c] /\ ~shape[c] /\ push[c] # "refused")

\* A no-result end never opens a failed-work judgment: a run the child ended by
\* itself with no result, its push not refused, is never finished `failed`.
NoResultIsNotFailedWork ==
  \A c \in Cards : fin[c] = "failed" => ~(nr[c] /\ push[c] # "refused")

\* A staging refusal never opens a failed-work judgment: a launch refused at staging ran no
\* child, and is never finished `failed`.
StagingIsNotFailedWork == \A c \in Cards : fin[c] = "failed" => ~stg[c]

\* A cutoff belongs to the harness, not to the card: staging never spends
\* an attempt, a redeal or a card cost (the sprint staging-refused path).
LengthIsAHarnessFault == \A c \in Cards :
  (ph[c] = "done" /\ fin[c] # "reaped" /\ lf[c]) => fin[c] = "staging"

\* A collected result applies only if the claim is still held at the fresh
\* queue read, otherwise the card is reaped: what the sprint is told is set
\* only when the claim was held, or when the store accepted the report (the
\* move seen after an acceptance is the store acting on it, a failed finish
\* redealt at gen+1, never an independent move).
CollectedAppliesOnlyIfHeld ==
  \A c \in Cards : rep[c] # "none" => claim[c] = "held" \/ answer[c] = "accepted"

\* A collected result whose report the store accepted (code 0) is never
\* reaped, even when that acceptance moved the card to gen+1: an accepted
\* finish was the card's claim when it landed.
AcceptedIsNeverReaped ==
  \A c \in Cards : answer[c] = "accepted" => fin[c] # "reaped"

\* A pushed origin head keeps the immutable stage commit in its ancestry. The update-ref
\* adversary moves only the child's checkout head; Push refuses that non-descendant and
\* leaves the target branch unchanged. `rewritepushed` bypasses the check as the reversed witness.
RewriteKeptIsPushed == \A c \in Cards : ~originUpdated[c] \/ originDescends[c]

\* A refusal, including a repeated refusal, preserves both the recorded stage and origin.
RefusedPushPreservesOrigin ==
  \A c \in Cards : push[c] = "refused" => stageAnchor[c] /\ ~originUpdated[c]

\* Every launch ends judged, reaped, or finished provider (a run the provider failed, the
\* sprint's redeal outside this model) or staging (a launch refused at staging, the sprint's
\* restage outside this model).
EveryLaunchEnds == <>(\A c \in Cards : ph[c] = "done" /\ fin[c] \in {"ok", "failed", "reaped", "provider", "staging"})
=============================================================================
