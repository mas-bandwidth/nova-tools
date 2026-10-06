--------------------------- MODULE PromoteDelivery ---------------------------
\* The delivery milestones of a card (internal/sprint/delivery.go, promotion.go
\* Promoted, cmd/nova-sprint/land.go and promote.go; docs/SPEC-SPRINT.md section
\* 7, delivery milestones). A card is staged when the lander pushes its batch to
\* the branch its stream lands on: the sprint branch, or a stream branch. It is
\* verified in dev when a promotion merges a branch holding it into dev, and
\* installed on a target when an install receipt names a dev commit a promotion
\* recorded. A review of nova-sprint (item 3) found the record and the truth
\* apart: a promotion called every card landed since the last one promoted, so a
\* push to a stream branch counted as delivered to dev.
\*
\* THE STATE.
\*   staged     the ref each card was staged on: "none", "sprint" or "stream"
\*   sprintHas  the cards whose work is on the sprint branch (git, not the record)
\*   streamHas  the cards whose work is on a stream branch
\*   dev        the cards whose work is on dev (git)
\*   target     each target's installed content (what it really runs)
\*   verified   the cards the store records verified in dev
\*   installed  the cards the store records installed, per target
\*   promo      the last promotion's outcome: "none", "ok" or "failed"
\*   failed     the store's failed-promotion record stands (FailedPromotion)
\*
\* THE ACTIONS. Land a card on the sprint branch or a stream branch; merge a
\* stream branch into the sprint branch (its cards stay staged on the stream
\* branch); promote the sprint branch into dev, the record naming the cards it
\* carried (--cards: the range's land commits, the cards on the sprint branch not
\* yet in dev), the branch (--branch: the cards staged on it), or neither; a
\* promotion that fails; install dev's content on a target.
\*
\* THE BROKEN SWITCHES. VerifyAllLanded is the old count (every card landed
\* since the last promotion is called promoted): VerifiedInDev fails on a stream
\* branch push. ForgetFailure drops the failed promotion's record:
\* FailureVisible fails.

EXTENDS FiniteSets, Naturals

CONSTANTS Cards, Targets, VerifyAllLanded, ForgetFailure

VARIABLES staged, sprintHas, streamHas, dev, target, verified, installed, promo, failed

vars == <<staged, sprintHas, streamHas, dev, target, verified, installed, promo, failed>>

Refs == {"none", "sprint", "stream"}

Landed == {c \in Cards : staged[c] # "none"}

TypeOK ==
    /\ staged \in [Cards -> Refs]
    /\ sprintHas \subseteq Cards /\ streamHas \subseteq Cards /\ dev \subseteq Cards
    /\ target \in [Targets -> SUBSET Cards]
    /\ verified \subseteq Cards
    /\ installed \in [Targets -> SUBSET Cards]
    /\ promo \in {"none", "ok", "failed"}
    /\ failed \in BOOLEAN

Init ==
    /\ staged = [c \in Cards |-> "none"]
    /\ sprintHas = {} /\ streamHas = {} /\ dev = {}
    /\ target = [t \in Targets |-> {}]
    /\ verified = {}
    /\ installed = [t \in Targets |-> {}]
    /\ promo = "none"
    /\ failed = FALSE

\* the lander pushes c's batch to the sprint branch (merging -> landed, staged_ref)
LandSprint(c) ==
    /\ staged[c] = "none"
    /\ staged' = [staged EXCEPT ![c] = "sprint"]
    /\ sprintHas' = sprintHas \cup {c}
    /\ UNCHANGED <<streamHas, dev, target, verified, installed, promo, failed>>

\* the lander pushes c's batch to a stream branch: staged, and nothing more
LandStream(c) ==
    /\ staged[c] = "none"
    /\ staged' = [staged EXCEPT ![c] = "stream"]
    /\ streamHas' = streamHas \cup {c}
    /\ UNCHANGED <<sprintHas, dev, target, verified, installed, promo, failed>>

\* a stream branch is merged into the sprint branch; the records do not move
MergeStream ==
    /\ streamHas \ sprintHas # {}
    /\ sprintHas' = sprintHas \cup streamHas
    /\ UNCHANGED <<staged, streamHas, dev, target, verified, installed, promo, failed>>

\* the cards a promotion record verifies (promotedCards), by what it names
Carried(mode) ==
    IF VerifyAllLanded THEN Landed \ verified
    ELSE CASE mode = "cards"  -> sprintHas \ dev
         []   mode = "branch" -> {c \in Landed \ verified : staged[c] = "sprint"}
         []   OTHER           -> {}

\* the promotion merged: dev holds the sprint branch, the record verifies what it names
PromoteOK(mode) ==
    /\ sprintHas \ dev # {}
    /\ dev' = dev \cup sprintHas
    /\ verified' = verified \cup Carried(mode)
    /\ promo' = "ok"
    /\ failed' = FALSE
    /\ UNCHANGED <<staged, sprintHas, streamHas, target, installed>>

\* the promotion's merge-group run failed: promoted --failed
PromoteFail ==
    /\ sprintHas \ dev # {}
    /\ promo' = "failed"
    /\ failed' = ~ForgetFailure
    /\ UNCHANGED <<staged, sprintHas, streamHas, dev, target, verified, installed>>

\* a target installs dev's commit a promotion recorded; the receipt marks the
\* cards verified at or before it
Install(t) ==
    /\ promo # "none"
    /\ verified \ installed[t] # {} \/ dev # target[t]
    /\ target' = [target EXCEPT ![t] = dev]
    /\ installed' = [installed EXCEPT ![t] = installed[t] \cup verified]
    /\ UNCHANGED <<staged, sprintHas, streamHas, dev, verified, promo, failed>>

Next ==
    \/ \E c \in Cards : LandSprint(c) \/ LandStream(c)
    \/ MergeStream
    \/ \E m \in {"cards", "branch", "none"} : PromoteOK(m)
    \/ PromoteFail
    \/ \E t \in Targets : Install(t)

Spec == Init /\ [][Next]_vars

\* verified in dev is never recorded for a card whose work is not on dev: a
\* stream-branch push cannot count as dev-delivered
VerifiedInDev == verified \subseteq dev

\* every verified card was staged first
VerifiedStaged == verified \subseteq Landed

\* installed is recorded only for cards verified in dev, and on what the target runs
InstalledVerified == \A t \in Targets : installed[t] \subseteq verified
InstalledOnTarget == \A t \in Targets : installed[t] \subseteq target[t]

\* a failed promotion stays on the record until a promotion merges after it
FailureVisible == promo = "failed" => failed

\* the counts apart: installed <= verified <= staged
CountsApart ==
    /\ Cardinality(verified) <= Cardinality(Landed)
    /\ \A t \in Targets : Cardinality(installed[t]) <= Cardinality(verified)

=============================================================================
