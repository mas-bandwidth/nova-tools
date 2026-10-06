---- MODULE Lifecycle ----
\* Lifecycle is a primary card's life from its attempt to its end, held to the
\* one transition the paths rule adds (internal/sprint paths_proposed.go and
\* paths_hold.go; docs/SPEC-SPRINT.md section 8, the rules table's row paths):
\* an attempt that ends Verdict: HOLD with a PATHS-PROPOSED: line, its head
\* pushed, is answered by the tick with no judgment to the coordinator when the
\* proposal is inside the repository, names no protected or secrets path, and
\* the card is not itself a twin the rule cut. The card is dropped and its twin
\* placed (--replaces), and the rule is recorded on both. Any other proposal is
\* a judgment, which a mind answers by its own twin (a recut by hand) or a drop.
\*
\* Card ids are slots 1..MaxCards; the originals are 1..NOrigins and the rest
\* are free for twins. TLC chooses each attempt's end and its proposal.
\*
\* The rules the code keeps, as invariants:
\*   TwinnedOnceByRule       a card is twinned at most once by the rule, and a
\*                           twin the rule cut is never twinned by it again;
\*   OnlyInRepoByRule        the rule twins only an in-repository proposal that
\*                           names no protected path;
\*   RecordedOnBoth          a twin by rule names the card it replaces, and that
\*                           card names it (paths_twinned, paths_twin);
\*   FirstInRepoNeverJudged  a card the rule may twin never reaches a mind;
\*   OneLive                 each lineage has at most one card on the table.
\* And as a property: a held card is answered (HeldAnswered).
EXTENDS Naturals, FiniteSets

CONSTANTS
  NOrigins,        \* the cards added by the coordinator
  MaxCards,        \* the slots: originals and twins
  BrokenTwiceByRule, \* TRUE: the broken twin, which twins a rule twin again
  BrokenProtected    \* TRUE: the broken twin, which twins a protected proposal

Ids       == 1..MaxCards
Origins   == 1..NOrigins
None      == 0
States    == {"none", "working", "held", "judgment", "dropped", "landed"}
Proposals == {"none", "inrepo", "protected", "outside"}
Cuts      == {"origin", "rule", "hand"}

VARIABLES
  st,       \* each slot's state
  cut,      \* how the card came to be: added, twinned by rule, twinned by hand
  parent,   \* the card a twin replaces (None for an original)
  proposal, \* the PATHS-PROPOSED of its held attempt
  twin      \* the twin by rule recorded on a card it replaced (None for none)

vars == <<st, cut, parent, proposal, twin>>

TypeOK ==
  /\ st \in [Ids -> States]
  /\ cut \in [Ids -> Cuts]
  /\ parent \in [Ids -> Ids \cup {None}]
  /\ proposal \in [Ids -> Proposals]
  /\ twin \in [Ids -> Ids \cup {None}]

Init ==
  /\ st = [i \in Ids |-> IF i \in Origins THEN "working" ELSE "none"]
  /\ cut = [i \in Ids |-> "origin"]
  /\ parent = [i \in Ids |-> None]
  /\ proposal = [i \in Ids |-> "none"]
  /\ twin = [i \in Ids |-> None]

Free(j) == st[j] = "none" /\ j \notin Origins

\* The attempt ends: landed, or held with a proposal (none is a HOLD without
\* the line, which the failed rule answers and the model leaves to a mind).
Land(i) ==
  /\ st[i] = "working"
  /\ st' = [st EXCEPT ![i] = "landed"]
  /\ UNCHANGED <<cut, parent, proposal, twin>>

Hold(i, p) ==
  /\ st[i] = "working"
  /\ st' = [st EXCEPT ![i] = "held"]
  /\ proposal' = [proposal EXCEPT ![i] = p]
  /\ UNCHANGED <<cut, parent, twin>>

\* The rule's guard: an in-repository proposal naming no protected path, on a
\* card the rule did not cut (paths_hold.go pathsHoldLeft, badGlobs).
RuleTwins(i) ==
  /\ proposal[i] = "inrepo" \/ (BrokenProtected /\ proposal[i] = "protected")
  /\ cut[i] # "rule" \/ BrokenTwiceByRule

\* PathsProposed: the tick drops the held card and places its twin, PATHS
\* widened, the head carried, the rule recorded on both cards.
PathsProposed(i, j) ==
  /\ st[i] = "held"
  /\ RuleTwins(i)
  /\ Free(j)
  /\ st' = [st EXCEPT ![i] = "dropped", ![j] = "working"]
  /\ cut' = [cut EXCEPT ![j] = "rule"]
  /\ parent' = [parent EXCEPT ![j] = i]
  /\ twin' = [twin EXCEPT ![i] = j]
  /\ UNCHANGED proposal

\* Every other held card is a judgment, and so is one whose twin ids are all
\* taken (the code's "every twin id of <card> is taken; a mind's").
NoSlot == ~\E j \in Ids : Free(j)

Judge(i) ==
  /\ st[i] = "held"
  /\ ~RuleTwins(i) \/ NoSlot
  /\ st' = [st EXCEPT ![i] = "judgment"]
  /\ UNCHANGED <<cut, parent, proposal, twin>>

\* A mind answers the judgment: its own twin by hand, or a drop.
MindTwin(i, j) ==
  /\ st[i] = "judgment"
  /\ Free(j)
  /\ st' = [st EXCEPT ![i] = "dropped", ![j] = "working"]
  /\ cut' = [cut EXCEPT ![j] = "hand"]
  /\ parent' = [parent EXCEPT ![j] = i]
  /\ UNCHANGED <<proposal, twin>>

MindDrop(i) ==
  /\ st[i] = "judgment"
  /\ st' = [st EXCEPT ![i] = "dropped"]
  /\ UNCHANGED <<cut, parent, proposal, twin>>

Next ==
  \E i \in Ids :
    \/ Land(i)
    \/ \E p \in Proposals : Hold(i, p)
    \/ \E j \in Ids : PathsProposed(i, j)
    \/ Judge(i)
    \/ \E j \in Ids : MindTwin(i, j)
    \/ MindDrop(i)

\* The tick runs: a held card is answered, by the rule or as a judgment.
Spec == Init /\ [][Next]_vars /\ \A i \in Ids : WF_vars(Judge(i)) /\ WF_vars(\E j \in Ids : PathsProposed(i, j))

RuleTwinsOf(i) == {j \in Ids : parent[j] = i /\ cut[j] = "rule"}

TwinnedOnceByRule ==
  \A i \in Ids :
    /\ Cardinality(RuleTwinsOf(i)) <= 1
    /\ cut[i] = "rule" => RuleTwinsOf(i) = {}

OnlyInRepoByRule ==
  \A j \in Ids : cut[j] = "rule" => proposal[parent[j]] = "inrepo"

RecordedOnBoth ==
  \A j \in Ids : cut[j] = "rule" => parent[j] # None /\ twin[parent[j]] = j

\* Slots never come free again, so NoSlot read now holds at the judging too.
FirstInRepoNeverJudged ==
  \A i \in Ids : st[i] = "judgment" => (proposal[i] # "inrepo" \/ cut[i] = "rule" \/ NoSlot)

RECURSIVE RootOf(_)
RootOf(i) == IF parent[i] = None THEN i ELSE RootOf(parent[i])
OneLive ==
  \A i, j \in Ids :
    (i # j /\ st[i] \in {"working", "held", "judgment"} /\ st[j] \in {"working", "held", "judgment"}) => RootOf(i) # RootOf(j)

\* A held card is answered, by its twin or as a judgment: never dealt the same
\* brief again, never left held.
HeldAnswered ==
  \A i \in Ids : [](st[i] = "held" => <>(st[i] # "held"))
====
