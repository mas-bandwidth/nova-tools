---------------------------- MODULE Lifecycle ----------------------------
\* A PATHS hold answered by a twin (internal/sprint/paths_hold.go, TickPathsHold).
\* One card proposes paths. The first proposal twins when it is admitted (every
\* path inside the repository, none protected). A later proposal stays a
\* judgment and does not twin again. Broken = {"twice"} twins on every
\* proposal, which breaks TwinnedAtMostOnce.
EXTENDS Naturals

CONSTANTS MaxProposals, Broken

VARIABLES proposed, twins, judgment
vars == <<proposed, twins, judgment>>

TypeOK ==
    /\ proposed \in 0..MaxProposals
    /\ twins \in 0..MaxProposals
    /\ twins <= proposed
    /\ judgment \in BOOLEAN

Init ==
    /\ proposed = 0
    /\ twins = 0
    /\ judgment = FALSE

\* admit is the first proposal's shape: TRUE twins, FALSE is protected or
\* outside the repository and stays a judgment. A later proposal twins only
\* when Broken names "twice".
PathsProposed ==
    /\ proposed < MaxProposals
    /\ \E admit \in BOOLEAN :
        /\ proposed' = proposed + 1
        /\ twins' = IF ("twice" \in Broken) \/ (proposed = 0 /\ admit)
                    THEN twins + 1
                    ELSE twins
        /\ judgment' = IF ("twice" \in Broken) \/ (proposed = 0 /\ admit)
                       THEN judgment
                       ELSE TRUE

Next == PathsProposed

Spec == Init /\ [][Next]_vars /\ WF_vars(PathsProposed)

\* a card is twinned at most once by this rule
TwinnedAtMostOnce == twins <= 1

=============================================================================
