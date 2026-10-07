------------------------------ MODULE ReadCards ------------------------------
\* A read is a consumer card (docs/SPEC-SPRINT.md section 6, "A read is a consumer
\* card"; internal/sprint/read_cards.go). One primary in review, three readers, the
\* first of them its worker. The deal cuts every read the attempt still needs at
\* once (Need less the reads that stand), each a card on a different reader; a card
\* is ready, then working, then closed ok or broken by its reader, handed back
\* (returned), let pass its deadline (late), or taken back by the machine (away:
\* its reader down or held, a resting route, its primary moved). A reader that
\* closed or let pass a read of the attempt is never dealt it again (Spent); a read
\* the machine took back, or its reader returned with no verdict (the owner,
\* 2026-10-07: only a verdict spends a reader; a return is a fetch that failed),
\* spends nothing and its reader may be dealt it again under the next generation,
\* at most MaxGen times (ReturnedIsDealtAgain). The primary leaves review only by
\* the existing rules: accepted on Need different oks at the attempt, reworked on a
\* broken read (a new attempt, its open cards retired). Checked by MCReadCards.cfg
\* over MCReadCards.tla (the instance, which extends this module unchanged);
\* MCReadCardsBrokenIgnoreSpent.cfg (BugIgnoreSpent TRUE) breaks NeverTwiceAfterSpent,
\* and MCReadCardsBrokenReturnSpends.cfg (BugReturnSpends TRUE, the rule before
\* 2026-10-07) breaks ReturnedIsDealtAgain.
\*
\* A read is late only from its start (working): one never started is taken back by
\* the machine (TakeBack: the deal bound, a member held), spending no one.
\* The instance is the module, as ReaderTiers' is, run with a cfg setting
\* BugIgnoreSpent: FALSE holds every invariant; TRUE, the reversed witness (a deal that
\* forgets a spent reader), breaks NeverTwiceAfterSpent. BugReturnSpends TRUE counts a
\* return as spent, the rule before 2026-10-07. Its cases are in tla/CASES.tsv.

EXTENDS Naturals, FiniteSets

CONSTANTS BugIgnoreSpent, BugReturnSpends

Readers == {"w", "a", "b"}
Worker == "w"
Need == 2
MaxAttempt == 2
MaxGen == 1

Open == {"ready", "working"}
\* what spends a reader: a verdict, or a deadline let pass (Judged); a return only under
\* the old rule
Judged == {"ok", "broken", "late"}
Spent == IF BugReturnSpends THEN Judged \cup {"returned"} ELSE Judged

VARIABLES attempt, pstate, cards
vars == <<attempt, pstate, cards>>

\* cards: a set of records, one per card ever cut
Card(a, r, g, st) == [att |-> a, rd |-> r, gen |-> g, st |-> st]

TypeOK ==
  /\ attempt \in 1..MaxAttempt
  /\ pstate \in {"review", "merging"}
  /\ \A c \in cards : c.att \in 1..MaxAttempt /\ c.rd \in Readers /\ c.gen \in 0..MaxGen
                     /\ c.st \in Open \cup Spent \cup {"away", "returned", "rework"}

Init == attempt = 1 /\ pstate = "review" /\ cards = {}

At(a) == {c \in cards : c.att = a}
Mine(a, r) == {c \in At(a) : c.rd = r}
Standing(a) == {c \in At(a) : c.st \in Open \cup {"ok", "broken"}}
Broken(a) == \E c \in At(a) : c.st = "broken"
OkReaders(a) == {c.rd : c \in {x \in At(a) : x.st = "ok"}}
Wanted == IF Broken(attempt) THEN 0 ELSE Need - Cardinality(Standing(attempt))
SpentBy(a, r) == \E c \in Mine(a, r) : c.st \in Spent \cup Open
NextGen(a, r) == Cardinality(Mine(a, r))

Eligible ==
  {r \in Readers \ {Worker} :
     /\ (BugIgnoreSpent \/ ~SpentBy(attempt, r))
     /\ \A c \in Mine(attempt, r) : c.st \notin Open
     /\ NextGen(attempt, r) <= MaxGen}

\* the deal: some of the reads wanted, at once, each to a different eligible reader (the
\* room decides how many: any number up to the wanted)
Deal ==
  /\ pstate = "review"
  /\ Wanted > 0
  /\ \E S \in SUBSET Eligible :
       /\ S # {}
       /\ Cardinality(S) <= Wanted
       /\ cards' = cards \cup {Card(attempt, r, NextGen(attempt, r), "ready") : r \in S}
  /\ UNCHANGED <<attempt, pstate>>

Move(c, st) == cards' = (cards \ {c}) \cup {[c EXCEPT !.st = st]}

Take == \E c \in At(attempt) : c.st = "ready" /\ Move(c, "working") /\ UNCHANGED <<attempt, pstate>>
Close == \E c \in At(attempt), v \in {"ok", "broken"} : c.st = "working" /\ Move(c, v) /\ UNCHANGED <<attempt, pstate>>
Return == \E c \in At(attempt) : c.st \in Open /\ Move(c, "returned") /\ UNCHANGED <<attempt, pstate>>
Late == \E c \in At(attempt) : c.st = "working" /\ Move(c, "late") /\ UNCHANGED <<attempt, pstate>>
TakeBack == \E c \in At(attempt) : c.st \in Open /\ Move(c, "away") /\ UNCHANGED <<attempt, pstate>>

Accept ==
  /\ pstate = "review"
  /\ Cardinality(OkReaders(attempt)) >= Need
  /\ pstate' = "merging"
  /\ UNCHANGED <<attempt, cards>>

Rework ==
  /\ pstate = "review"
  /\ Broken(attempt)
  /\ attempt < MaxAttempt
  /\ attempt' = attempt + 1
  /\ cards' = {IF c.st \in Open THEN [c EXCEPT !.st = "rework"] ELSE c : c \in cards}
  /\ UNCHANGED pstate

Next == Deal \/ Take \/ Close \/ Return \/ Late \/ TakeBack \/ Accept \/ Rework

Spec == Init /\ [][Next]_vars

\* one open read card per (attempt, reader)
OneOpenPerReader == \A a \in 1..MaxAttempt, r \in Readers : Cardinality({c \in Mine(a, r) : c.st \in Open}) <= 1

\* at most Need reads stand at an attempt: the deal never asks more than it needs
AtMostNeedStanding == \A a \in 1..MaxAttempt : Cardinality(Standing(a)) <= Need

\* the worker never reads its own attempt
NeverTheWorker == \A c \in cards : c.rd # Worker

\* a reader that closed, returned or let pass a read of the attempt has no later card of it
NeverTwiceAfterSpent ==
  \A c, d \in cards : (c.att = d.att /\ c.rd = d.rd /\ c.st \in Spent /\ d.gen > c.gen) => FALSE

\* the primary leaves review only by the rules: merging on Need different oks
MergingOnlyOnOks == pstate = "merging" => Cardinality(OkReaders(attempt)) >= Need

\* the open cards of an attempt past are retired: no read of a reworked attempt stays open
NoOpenReadOfAnOldAttempt == \A c \in cards : c.att < attempt => c.st \notin Open

\* a reader that returned a read with no verdict is dealt it again: whenever the attempt
\* still wants a read, she holds no open card of it, judged none (Judged, not Spent: the
\* returned card itself must not excuse her) and has a generation left, she is eligible
\* (the deal may pick her); only a verdict or a lapse puts her out
ReturnedIsDealtAgain ==
  \A c \in cards :
    (/\ c.att = attempt /\ c.st = "returned" /\ pstate = "review" /\ Wanted > 0
     /\ ~\E d \in Mine(attempt, c.rd) : d.st \in Judged \cup Open
     /\ NextGen(attempt, c.rd) <= MaxGen)
    => c.rd \in Eligible

=============================================================================
