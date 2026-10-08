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
\*   frozen     the cards on the frozen tip of the open promotion (promo/<date>-<n>,
\*              cut at the sprint tip), or {} with none open
\*   promo      the last promotion's outcome: "none", "ok" or "failed"
\*   failed     the store's failed-promotion record stands (FailedPromotion)
\*
\* THE ACTIONS. Land a card on the sprint branch or a stream branch; merge a
\* stream branch into the sprint branch (its cards stay staged on the stream
\* branch); cut the promotion's frozen branch at the sprint tip (the lander keeps
\* landing on the live branch while the queue runs); merge the frozen tip into
\* dev, the record naming the cards it carried (--cards: the range's land
\* commits, the cards on the frozen tip not yet in dev), the branch and its tip
\* (--branch --tip: the cards staged on it whose staged commit is an ancestor
\* of the tip), or neither; a promotion that fails; install dev's content on a
\* target.
\*
\* THE BROKEN SWITCHES. VerifyAllLanded is the old count (every card landed
\* since the last promotion is called promoted): VerifiedInDev fails on a stream
\* branch push. ForgetFailure drops the failed promotion's record:
\* FailureVisible fails. IgnoreTip is the --branch fallback without its tip (the
\* cards staged on the branch, landed by now): a card landed after the cut is
\* verified though dev never got it, and VerifiedInDev fails.

EXTENDS FiniteSets, Naturals

CONSTANTS Cards, Targets, VerifyAllLanded, ForgetFailure, IgnoreTip

VARIABLES staged, sprintHas, streamHas, dev, target, verified, installed, frozen, promo, failed

vars == <<staged, sprintHas, streamHas, dev, target, verified, installed, frozen, promo, failed>>

Refs == {"none", "sprint", "stream"}

Landed == {c \in Cards : staged[c] # "none"}

TypeOK ==
    /\ staged \in [Cards -> Refs]
    /\ sprintHas \subseteq Cards /\ streamHas \subseteq Cards /\ dev \subseteq Cards
    /\ target \in [Targets -> SUBSET Cards]
    /\ verified \subseteq Cards
    /\ installed \in [Targets -> SUBSET Cards]
    /\ frozen \subseteq Cards
    /\ promo \in {"none", "ok", "failed"}
    /\ failed \in BOOLEAN

Init ==
    /\ staged = [c \in Cards |-> "none"]
    /\ sprintHas = {} /\ streamHas = {} /\ dev = {}
    /\ target = [t \in Targets |-> {}]
    /\ verified = {}
    /\ installed = [t \in Targets |-> {}]
    /\ frozen = {}
    /\ promo = "none"
    /\ failed = FALSE

\* the lander pushes c's batch to the sprint branch (merging -> landed, staged_ref)
LandSprint(c) ==
    /\ staged[c] = "none"
    /\ staged' = [staged EXCEPT ![c] = "sprint"]
    /\ sprintHas' = sprintHas \cup {c}
    /\ UNCHANGED <<streamHas, dev, target, verified, installed, frozen, promo, failed>>

\* the lander pushes c's batch to a stream branch: staged, and nothing more
LandStream(c) ==
    /\ staged[c] = "none"
    /\ staged' = [staged EXCEPT ![c] = "stream"]
    /\ streamHas' = streamHas \cup {c}
    /\ UNCHANGED <<sprintHas, dev, target, verified, installed, frozen, promo, failed>>

\* a stream branch is merged into the sprint branch; the records do not move
MergeStream ==
    /\ streamHas \ sprintHas # {}
    /\ sprintHas' = sprintHas \cup streamHas
    /\ UNCHANGED <<staged, streamHas, dev, target, verified, installed, frozen, promo, failed>>

\* promote cuts promo/<date>-<n> at the sprint tip: the frozen tip holds what
\* the sprint branch holds now, and later landings stay off it
Cut ==
    /\ frozen = {}
    /\ sprintHas \ dev # {}
    /\ frozen' = sprintHas
    /\ UNCHANGED <<staged, sprintHas, streamHas, dev, target, verified, installed, promo, failed>>

\* the cards a promotion record verifies (promotedCards), by what it names
Carried(mode) ==
    IF VerifyAllLanded THEN Landed \ verified
    ELSE CASE mode = "cards"  -> frozen \ dev
         []   mode = "branch" ->
                IF IgnoreTip THEN {c \in Landed \ verified : staged[c] = "sprint"}
                ELSE {c \in Landed \ verified : staged[c] = "sprint" /\ c \in frozen}
         []   OTHER           -> {}

\* the frozen tip merged: dev holds it, the record verifies what it names
PromoteOK(mode) ==
    /\ frozen # {}
    /\ dev' = dev \cup frozen
    /\ verified' = verified \cup Carried(mode)
    /\ frozen' = {}
    /\ promo' = "ok"
    /\ failed' = FALSE
    /\ UNCHANGED <<staged, sprintHas, streamHas, target, installed>>

\* the promotion's run failed: promoted --failed; the frozen branch is dropped
\* and the next pass cuts again
PromoteFail ==
    /\ frozen # {}
    /\ frozen' = {}
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
    /\ UNCHANGED <<staged, sprintHas, streamHas, dev, verified, frozen, promo, failed>>

Next ==
    \/ \E c \in Cards : LandSprint(c) \/ LandStream(c)
    \/ MergeStream
    \/ Cut
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
