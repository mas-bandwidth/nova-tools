---------------------------- MODULE LandTwoLevel ----------------------------
\* The merge tree at its simplest (cmd/nova-sprint/land_tree_proof_functional_test.go):
\* two levels, a node a stream and then the root, no leases. A node is the
\* batch lander's build over its stream's ready cards on its own tip (the base
\* as read at its first round, then its staged branch): it stages the longest
\* green prefix (gate once, bisect: tla/LandBisect.tla) and holds the card that
\* turns its tip red alone, for a judgment; the cards after it go again. The
\* nodes run at once (their steps interleave). Once every node is done, the
\* root merges the staged nodes onto the base in stream order, gates once (the
\* first node that turns it red is blamed), and pushes without force: a base
\* that moved since the gate is the push refused, and the root re-gates once,
\* a second move a judgment. The landing is recorded card by card, by id and
\* head; a card whose head the base held already (a no-op) makes no commit and
\* is settled on the base, no landing. A node the root blamed is unpacked when
\* it holds more than one card (its cards back to its stream, run again on the
\* new base), its one card held when it holds one.
\*
\* THE STATE. st each card (ready, staged, held, landed, onbase, refused);
\* ready and staged each stream's cards at its leaf and in its staged tip, in
\* order; nb, ne the base cards and the base's moves its node's tip was cut
\* on; judg the open judgments; ev, evn the last event and how many there
\* were. base, bver, ext the base's cards, its version and the moves others
\* made (MaxMoves at most); badAt each card: 0 green, 1 red, 2 red once the
\* base moved; clash the pairs whose merges conflict; noop the cards whose
\* heads are on the base. rph, rG, rB, rver, rreg the root's phase, the kids it
\* gated green, the kid it blamed, the base version it gated on, its re-gates.
\* GHOSTS lands (each card's landings recorded), commits (cards with a commit
\* on the base), pushed (every tree pushed with the moves it met), clashed
\* (cards held for a conflict), regated.
\*
\* Broken: "none" is the design.
\*   "noregate" pushes onto a moved base without re-gating (OnlyGatedReachBase).
\*   "prefix" records a node's landing as the first n cards of its stream's
\*     queue, n its staged count, not by id (RecordedOnBase: the tracer's
\*     merge --batch n).
\*   "nooplanding" records a no-op as a landing (NoOpNoLanding).
EXTENDS Integers, Sequences, FiniteSets

CONSTANTS SOrder, Lane, NoopCands, ClashPairs, MaxMoves, Broken

Streams == {SOrder[i] : i \in 1..Len(SOrder)}
Range(q) == {q[i] : i \in 1..Len(q)}
Cards == UNION {Range(Lane[s]) : s \in Streams}
None == "none"
JudgeKinds == {"card-red", "sibling-clash", "base-moving"}
MechKinds == {"start", "staged", "unpacked", "landed", "base-moved-regate", "refused", "released"}
Settled == {"landed", "onbase", "refused"}

VARIABLES st, ready, staged, nb, ne, judg, ev, evn,
          base, bver, ext, badAt, clash, noop,
          rph, rG, rB, rver, rreg,
          lands, commits, pushed, clashed, regated

vars == <<st, ready, staged, nb, ne, judg, ev, evn, base, bver, ext, badAt, clash, noop,
          rph, rG, rB, rver, rreg, lands, commits, pushed, clashed, regated>>
nodeVars == <<st, ready, staged, nb, ne>>
outside == <<base, bver, ext, badAt, clash, noop>>
rootVars == <<rph, rG, rB, rver, rreg>>
ghosts == <<lands, commits, pushed, clashed, regated>>

Red(S, e) == \E c \in S : badAt[c] = 1 \/ (badAt[c] = 2 /\ e > 0)
Clashes(S) == \E p \in clash : p \subseteq S
Bad(S, e) == Red(S, e) \/ Clashes(S)
Ev(k) == [k |-> k, ans |-> k \in JudgeKinds]
Emit(k) == ev' = Ev(k) /\ evn' = evn + 1

\* ---- a node: its tip, its green prefix ----

Tip(s) == IF staged[s] = <<>> THEN base ELSE nb[s] \cup Range(staged[s])
TipExt(s) == IF staged[s] = <<>> THEN ext ELSE ne[s]
Pre(s, j) == Tip(s) \cup Range(SubSeq(ready[s], 1, j))
GreenLen(s) == CHOOSE k \in 0..Len(ready[s]) :
                 /\ \A j \in 1..k : ~Bad(Pre(s, j), TipExt(s))
                 /\ k < Len(ready[s]) => Bad(Pre(s, k + 1), TipExt(s))
JKind(S) == IF Clashes(S) THEN "sibling-clash" ELSE "card-red"
Hold(c, k) ==
  /\ judg' = judg \cup {[k |-> k, c |-> c]}
  /\ clashed' = IF k = "sibling-clash" THEN clashed \cup {c} ELSE clashed

TypeOK ==
  /\ st \in [Cards -> {"ready", "staged", "held"} \cup Settled]
  /\ base \subseteq Cards /\ badAt \in [Cards -> 0..2] /\ clash \subseteq ClashPairs
  /\ rph \in {"idle", "regate", "gated", "pushed"}
  /\ ev.k \in JudgeKinds \cup MechKinds

Init ==
  /\ badAt \in [Cards -> 0..2] /\ clash \in SUBSET ClashPairs /\ noop \in SUBSET NoopCands
  \* a no-op's head is on the base: green, in no conflict
  /\ \A c \in noop : badAt[c] = 0 /\ \A p \in clash : c \notin p
  /\ st = [c \in Cards |-> "ready"]
  /\ ready = Lane /\ staged = [s \in Streams |-> <<>>]
  /\ nb = [s \in Streams |-> {}] /\ ne = [s \in Streams |-> 0]
  /\ judg = {} /\ ev = Ev("start") /\ evn = 0
  /\ base = noop /\ bver = 0 /\ ext = 0
  /\ rph = "idle" /\ rG = <<>> /\ rB = None /\ rver = 0 /\ rreg = 0
  /\ lands = [c \in Cards |-> 0] /\ commits = {} /\ pushed = {} /\ clashed = {} /\ regated = FALSE

\* the node's round, its green prefix staged on a new branch: one event
NodeStage(s) ==
  /\ rph = "idle" /\ ready[s] # <<>> /\ GreenLen(s) > 0
  /\ LET k == GreenLen(s) IN
     /\ nb' = IF staged[s] = <<>> THEN [nb EXCEPT ![s] = base] ELSE nb
     /\ ne' = IF staged[s] = <<>> THEN [ne EXCEPT ![s] = ext] ELSE ne
     /\ staged' = [staged EXCEPT ![s] = @ \o SubSeq(ready[s], 1, k)]
     /\ ready' = [ready EXCEPT ![s] = SubSeq(@, k + 1, Len(@))]
     /\ st' = [c \in Cards |-> IF c \in Range(SubSeq(ready[s], 1, k)) THEN "staged" ELSE st[c]]
  /\ Emit("staged")
  /\ UNCHANGED <<judg, outside, rootVars, ghosts>>

\* the card that turns the node's tip red, held alone: one event, a judgment
NodeBlame(s) ==
  /\ rph = "idle" /\ ready[s] # <<>> /\ GreenLen(s) = 0
  /\ LET c == Head(ready[s]) k == JKind(Tip(s) \cup {c}) IN
     /\ st' = [st EXCEPT ![c] = "held"]
     /\ ready' = [ready EXCEPT ![s] = Tail(@)]
     /\ Hold(c, k) /\ Emit(k)
  /\ UNCHANGED <<staged, nb, ne, outside, rootVars, lands, commits, pushed, regated>>

\* ---- the root ----

Kids == SelectSeq(SOrder, LAMBDA s : staged[s] # <<>>)
KidCards(q) == UNION {Range(staged[q[i]]) : i \in 1..Len(q)}
RootBad(j) == Bad(base \cup KidCards(SubSeq(Kids, 1, j)), ext)

\* the merge of the staged nodes onto the base as read, gated once, the first
\* node that turns it red blamed: in the root's process, no event of its own
RootGate ==
  /\ rph \in {"idle", "regate"} /\ \A s \in Streams : ready[s] = <<>>
  /\ Kids # <<>> /\ ~\E j \in judg : j.k = "base-moving"
  /\ LET n == Len(Kids)
         reds == {j \in 1..n : RootBad(j)}
         f == IF reds = {} THEN 0 ELSE CHOOSE j \in reds : \A i \in reds : j <= i
         g == IF f = 0 THEN n ELSE f - 1
     IN /\ rG' = SubSeq(Kids, 1, g)
        /\ rB' = IF f = 0 THEN None ELSE Kids[f]
        /\ rph' = IF g = 0 THEN "pushed" ELSE "gated"
  /\ rver' = bver
  /\ UNCHANGED <<nodeVars, judg, ev, evn, outside, rreg, ghosts>>

\* what a landing records: by id and head, the staged cards; "prefix" the
\* first n cards of the stream's queue not yet settled
Queue(s) == SelectSeq(Lane[s], LAMBDA c : st[c] \notin Settled)
Recorded(s) == IF Broken = "prefix" THEN Range(SubSeq(Queue(s), 1, Len(staged[s])))
               ELSE Range(staged[s])
IsLanding(c) == c \notin noop \/ Broken = "nooplanding"

\* the non-forced push: refused on a moved base (the compare-and-swap), and
\* then a re-gate once, a second move a judgment; else the push and the
\* record, one event
RootPush ==
  /\ rph = "gated"
  /\ IF bver # rver /\ Broken # "noregate"
     THEN IF rreg = 0
          THEN /\ rph' = "regate" /\ rreg' = 1 /\ regated' = TRUE
               /\ Emit("base-moved-regate")
               /\ UNCHANGED <<nodeVars, judg, outside, rG, rB, rver, lands, commits, pushed, clashed>>
          ELSE /\ rph' = "idle" /\ rreg' = 0 /\ rG' = <<>> /\ rB' = None
               /\ judg' = judg \cup {[k |-> "base-moving", c |-> None]}
               /\ Emit("base-moving")
               /\ UNCHANGED <<nodeVars, outside, rver, ghosts>>
     ELSE LET G == KidCards(rG)
              R == UNION {Recorded(rG[i]) : i \in 1..Len(rG)}
          IN /\ base' = base \cup G /\ bver' = bver + 1
             /\ pushed' = pushed \cup {[tree |-> base \cup G, e |-> ext]}
             /\ commits' = commits \cup (G \ base)
             /\ lands' = [c \in Cards |-> IF c \in R /\ IsLanding(c) THEN lands[c] + 1 ELSE lands[c]]
             /\ st' = [c \in Cards |-> IF c \in R THEN (IF IsLanding(c) THEN "landed" ELSE "onbase")
                                       ELSE IF c \in G THEN "onbase"
                                       ELSE st[c]]
             /\ staged' = [s \in Streams |-> IF s \in Range(rG) THEN <<>> ELSE staged[s]]
             /\ rph' = IF rB = None THEN "idle" ELSE "pushed"
             /\ rreg' = 0 /\ rG' = <<>>
             /\ Emit("landed")
             /\ UNCHANGED <<ready, nb, ne, judg, ext, badAt, clash, noop, rB, rver, clashed, regated>>

\* the node the root blamed: unpacked (more than one card) or its card held
RootUnpack ==
  /\ rph = "pushed" /\ rB # None
  /\ LET s == rB IN
     IF Len(staged[s]) = 1
     THEN LET c == staged[s][1] k == JKind(base \cup {c}) IN
          /\ st' = [st EXCEPT ![c] = "held"]
          /\ Hold(c, k) /\ Emit(k)
          /\ ready' = ready
     ELSE /\ st' = [c \in Cards |-> IF c \in Range(staged[s]) THEN "ready" ELSE st[c]]
          /\ ready' = [ready EXCEPT ![s] = staged[s] \o @]
          /\ Emit("unpacked")
          /\ UNCHANGED <<judg, clashed>>
  /\ staged' = [staged EXCEPT ![rB] = <<>>]
  /\ rph' = "idle" /\ rB' = None /\ rG' = <<>> /\ rreg' = 0
  /\ UNCHANGED <<nb, ne, outside, rver, lands, commits, pushed, regated>>

\* another writer's commit on the base, gated by its author: no event of ours
MoveBase ==
  /\ ext < MaxMoves /\ ~Bad(base, ext + 1)
  /\ ext' = ext + 1 /\ bver' = bver + 1
  /\ UNCHANGED <<nodeVars, judg, ev, evn, base, badAt, clash, noop, rootVars, ghosts>>

\* the coordinator: a held card refused with its reason, a moving base released
Answer(j) ==
  /\ judg' = judg \ {j}
  /\ IF j.k = "base-moving"
     THEN Emit("released") /\ UNCHANGED st
     ELSE Emit("refused") /\ st' = [st EXCEPT ![j.c] = "refused"]
  /\ UNCHANGED <<ready, staged, nb, ne, outside, rootVars, ghosts>>

Next ==
  \/ \E s \in Streams : NodeStage(s) \/ NodeBlame(s)
  \/ RootGate \/ RootPush \/ RootUnpack
  \/ MoveBase
  \/ \E j \in judg : Answer(j)

Spec == Init /\ [][Next]_vars

Fairness ==
  /\ \A s \in Streams : WF_vars(NodeStage(s)) /\ WF_vars(NodeBlame(s))
  /\ WF_vars(RootGate) /\ WF_vars(RootPush) /\ WF_vars(RootUnpack)
  /\ WF_vars(\E j \in judg : Answer(j))

FairSpec == Spec /\ Fairness

\* ---- the rules ----

\* only gated trees reach the base: every tree pushed is green on the base it
\* was pushed onto
OnlyGatedReachBase == \A p \in pushed : ~Bad(p.tree, p.e)

NoCardTwice == \A c \in Cards : lands[c] <= 1

\* a landing recorded is the card's commit on the base, recorded once
RecordedOnBase == \A c \in Cards : st[c] = "landed" => c \in base /\ c \in commits /\ lands[c] = 1

\* a no-op makes no commit and is no landing
NoOpNoLanding == \A c \in noop : c \notin commits /\ lands[c] = 0 /\ st[c] # "landed"

\* every card is in exactly one place: its leaf, its node's staged tip, held
\* by a judgment, or settled
NoLostCard == \A c \in Cards :
  /\ Cardinality({s \in Streams : c \in Range(ready[s])}) + Cardinality({s \in Streams : c \in Range(staged[s])}) <= 1
  /\ st[c] = "ready" <=> \E s \in Streams : c \in Range(ready[s])
  /\ st[c] = "staged" <=> \E s \in Streams : c \in Range(staged[s])
  /\ st[c] = "held" <=> \E j \in judg : j.c = c

\* a node's staged tip passed its gate
StagedGreen == \A s \in Streams : staged[s] # <<>> => ~Bad(nb[s] \cup Range(staged[s]), ne[s])

\* a card is held only when it is red or in a conflict: blamed alone, rightly
HeldIsGuilty == \A c \in Cards : st[c] = "held" =>
  badAt[c] = 1 \/ (badAt[c] = 2 /\ ext > 0) \/ \E p \in clash : c \in p

\* a conflict makes one judgment: of a pair, at most one card is held for it
OneJudgmentAConflict == \A p \in clash : Cardinality(p \cap clashed) <= 1

\* an event needs an answer exactly when it is a judgment
EventsHonest ==
  /\ ev.ans <=> ev.k \in JudgeKinds
  /\ \A j \in judg : j.k \in JudgeKinds

\* every step of ours says one event; the outside's and the root's in-process
\* gate say none
OneEventAStep == [][ (MoveBase \/ RootGate) \/ evn' = evn + 1 ]_vars

\* every card lands, is settled on the base or is refused with its reason
EveryCardSettles == \A c \in Cards : <>(st[c] \in Settled)

\* a card that is green and in no conflict lands (or is on the base): a red
\* sibling never holds it back
GreenCardsLand == \A c \in Cards :
  (badAt[c] = 0 /\ ~\E p \in clash : c \in p) => <>(st[c] \in {"landed", "onbase"})

JudgmentsAnswered == [](judg # {} => <>(judg = {}))

\* ---- reversed witnesses (each must be violated) ----

\* a red card is held while a sibling of its stream lands
ReachPartial == ~\E s \in Streams : \E c, d \in Range(Lane[s]) :
                  c # d /\ st[c] = "held" /\ st[d] = "landed"
\* the root re-gates a moved base and every card not a no-op lands
ReachRegateLands == ~(regated /\ \A c \in Cards : st[c] \in {"landed", "onbase"})
\* the root unpacks a node
ReachUnpack == ev.k # "unpacked"
\* a conflict across streams is held, and the rest land
ReachClashRestLand == ~(clashed # {} /\ \A c \in Cards \ clashed : st[c] \in {"landed", "onbase"})
=============================================================================
