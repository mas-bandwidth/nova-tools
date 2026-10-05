----------------------------- MODULE ReadsByRoom -----------------------------
\* The ask of nova-sprint (docs/SPEC-SPRINT.md section 6, the reads, sequential
\* and by room; internal/sprint readers.go ReadsWanted, finderFirst, askPicks;
\* steps_review.go Ask; steps_tick.go TickAsk): two rules of 2026-10-03 and
\* 2026-10-04 in one machine.
\*   - Reads by room: a reader reader-<m> runs at its machine m's width, and a
\*     read is asked only of a reader with free room (asked and reading under
\*     the width); a read no reader has room for waits (the night of
\*     2026-10-03: reads levelled by count queued seven deep on the two narrow
\*     machines while 31 reader slots sat idle).
\*   - Reads one at a time: a card's first read is asked alone, nothing more
\*     while it is outstanding, the rest it needs only once every read that
\*     stands came back ok; a broken read goes to its rework with no second
\*     read (the night of 2026-10-03: 4.2 reads a landing against a design of
\*     two). The rework's next attempt asks its first read of the reader who
\*     found the attempt before broken, when that reader has room.
\* Which reader with room is asked (the engine's greatest share of room, ties
\* round the readers) is a free choice here: every property holds for every
\* choice among the readers with room.
\*
\* THE STATE.
\*   rd     each card's reads at its current attempt, by reader: none, asked,
\*          ok or broken (one read card per reader per attempt)
\*   att    each card's attempt; st its state: review, accepted (the accept:
\*          the reads it needs came back ok) or out (broken at its last attempt)
\*   fnd    the reader whose broken read sent the attempt before back (NoR on
\*          the first attempt)
\*   reads  the reads asked of the card over all its attempts; brk the attempts
\*          found broken (ghosts: the measure the owner reads per landing)
\*   ff     FALSE once an attempt's first read was asked of another reader
\*          while the finder was free with room (a ghost for FinderFirst)
\*   fb     FALSE once an attempt found broken had an ok read (a ghost for
\*          ReadsPerLanding: TRUE while the first read found each broken)
\*
\* BROKEN names the reversed witnesses: "pair" asks every read a card needs at
\* once (the ask before 2026-10-04: OneAtATime and ReadsPerLanding fail);
\* "count" asks readers whatever their room (the ask before 2026-10-03:
\* WidthRespected fails); "nofinder" asks the rework's first read of any
\* reader with room (FinderFirst fails).
EXTENDS Integers, FiniteSets

CONSTANTS Readers, Width, Cards, Need, MaxAttempts, Broken

NoR == "none"
ASSUME NoR \notin Readers

VARIABLES rd, att, st, fnd, reads, brk, ff, fb
vars == <<rd, att, st, fnd, reads, brk, ff, fb>>

Load(r) == Cardinality({c \in Cards : rd[c][r] = "asked"})
Room(r) == Width[r] - Load(r)
Has(c, v) == {r \in Readers : rd[c][r] = v}

\* ReadsWanted: one while no read of the attempt stands, none while one is
\* outstanding or found it broken, else the rest it needs.
Wanted(c) ==
  IF Has(c, "asked") # {} \/ Has(c, "broken") # {} THEN 0
  ELSE IF Broken = "pair" \/ Has(c, "ok") # {} THEN Need[c] - Cardinality(Has(c, "ok"))
  ELSE 1

\* The readers the ask may ask: no card at the attempt, with free room.
Free(c) == {r \in Readers : rd[c][r] = "none" /\ (Broken = "count" \/ Room(r) > 0)}
First(c) == \A r \in Readers : rd[c][r] = "none"
FinderOwed(c) == First(c) /\ fnd[c] \in Free(c)

Init ==
  /\ rd = [c \in Cards |-> [r \in Readers |-> "none"]]
  /\ att = [c \in Cards |-> 1]
  /\ st = [c \in Cards |-> "review"]
  /\ fnd = [c \in Cards |-> NoR]
  /\ reads = [c \in Cards |-> 0]
  /\ brk = [c \in Cards |-> 0]
  /\ ff = [c \in Cards |-> TRUE]
  /\ fb = [c \in Cards |-> TRUE]

\* askPicks: the reads wanted now, each of a different free reader with room,
\* the finder first when it is owed.
Ask(c) ==
  /\ st[c] = "review"
  /\ Wanted(c) > 0
  /\ \E S \in SUBSET Free(c) :
       /\ Cardinality(S) = Wanted(c)
       /\ (Broken # "nofinder" /\ FinderOwed(c)) => fnd[c] \in S
       /\ rd' = [rd EXCEPT ![c] = [r \in Readers |-> IF r \in S THEN "asked" ELSE @[r]]]
       /\ reads' = [reads EXCEPT ![c] = @ + Cardinality(S)]
       /\ ff' = [ff EXCEPT ![c] = @ /\ (FinderOwed(c) => fnd[c] \in S)]
  /\ UNCHANGED <<att, st, fnd, brk, fb>>

Read(c, r) ==
  /\ rd[c][r] = "asked"
  /\ \E v \in {"ok", "broken"} : rd' = [rd EXCEPT ![c][r] = v]
  /\ UNCHANGED <<att, st, fnd, reads, brk, ff, fb>>

\* Rework: the attempt found broken goes to the next, its outstanding reads
\* retired; the broken read's reader is the finder. This model assumes no
\* order among readers: one read at a time (no ask --another here) leaves an
\* attempt at most one broken read (OneBroken), so the CHOOSE has one reader
\* to choose. Where ask --another leaves two, the engine (sprint.finderOf) and
\* the reference model (refmodel Rework) name the first in reader ROW order,
\* never name order (TestTheFinderIsTheFirstBrokenReadInReaderRowOrder,
\* TestReworkFinderIsTheFirstBrokenReadInReaderOrder).
Rework(c) ==
  /\ st[c] = "review"
  /\ Has(c, "broken") # {}
  /\ IF att[c] < MaxAttempts
     THEN /\ att' = [att EXCEPT ![c] = @ + 1]
          /\ fnd' = [fnd EXCEPT ![c] = CHOOSE r \in Has(c, "broken") : TRUE]
          /\ rd' = [rd EXCEPT ![c] = [r \in Readers |-> "none"]]
          /\ UNCHANGED st
     ELSE /\ st' = [st EXCEPT ![c] = "out"]
          /\ rd' = [rd EXCEPT ![c] = [r \in Readers |-> "none"]]
          /\ UNCHANGED <<att, fnd>>
  /\ brk' = [brk EXCEPT ![c] = @ + 1]
  /\ fb' = [fb EXCEPT ![c] = @ /\ Has(c, "ok") = {}]
  /\ UNCHANGED <<reads, ff>>

Accept(c) ==
  /\ st[c] = "review"
  /\ Cardinality(Has(c, "ok")) >= Need[c]
  /\ Has(c, "broken") = {}
  /\ st' = [st EXCEPT ![c] = "accepted"]
  /\ UNCHANGED <<rd, att, fnd, reads, brk, ff, fb>>

Next == \E c \in Cards : Ask(c) \/ Rework(c) \/ Accept(c) \/ \E r \in Readers : Read(c, r)

Fairness ==
  \A c \in Cards : /\ SF_vars(Ask(c)) /\ WF_vars(Rework(c)) /\ WF_vars(Accept(c))
                   /\ \A r \in Readers : WF_vars(Read(c, r))

Spec == Init /\ [][Next]_vars /\ Fairness

TypeOK ==
  /\ rd \in [Cards -> [Readers -> {"none", "asked", "ok", "broken"}]]
  /\ att \in [Cards -> 1..MaxAttempts]
  /\ st \in [Cards -> {"review", "accepted", "out"}]
  /\ fnd \in [Cards -> Readers \cup {NoR}]
  /\ reads \in [Cards -> Nat] /\ brk \in [Cards -> Nat] /\ ff \in [Cards -> BOOLEAN]
  /\ fb \in [Cards -> BOOLEAN]

\* Readers at their machine's width: no reader holds more reads than its width.
WidthRespected == \A r \in Readers : Load(r) <= Width[r]

\* One at a time: a card has at most one read outstanding.
OneAtATime == \A c \in Cards : Cardinality(Has(c, "asked")) <= 1

\* The finder is unique: an attempt has at most one broken read, so Rework's
\* finder needs no order among readers here.
OneBroken == \A c \in Cards : Cardinality(Has(c, "broken")) <= 1

\* The measure: a card whose attempts found broken had no ok read (fb: the
\* first read found each broken) cost, accepted, the reads it needs plus one
\* for each attempt found broken (a pro card broken once: three, where the pair
\* cost four), and out, one read a broken attempt.
ReadsPerLanding ==
  \A c \in Cards : /\ (fb[c] /\ st[c] = "accepted") => reads[c] = Need[c] + brk[c]
                   /\ (fb[c] /\ st[c] = "out") => reads[c] = brk[c]

\* The finder checks the fix: a rework's first read goes to the reader who found
\* the attempt before broken whenever that reader is free with room.
FinderFirst == \A c \in Cards : ff[c]

\* Every card is accepted or out in the end: a read waiting for room is asked
\* once a reader has room (the readers read, and the rework and accept run).
Settles == <>(\A c \in Cards : st[c] # "review")
=============================================================================
