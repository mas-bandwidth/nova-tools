------------------------------ MODULE ReadMemo ------------------------------
\* A read is kept by its head (docs/SPEC-SPRINT.md section 6, a read is kept by
\* its head; internal/sprint readers.go inheritedReads, keepVerdicts, and the
\* ask in steps_review.go). One primary's attempts, each finishing at a head on a
\* base (a rework, or a twin by add --replaces carrying the head: the same
\* machine here, a new attempt at a head the work chooses). Every verdict a
\* reader reports is kept by (head, base, reader); an attempt entering review
\* at a key that holds verdicts inherits them before any reader is asked: a
\* broken verdict alone is the finding, else the ok verdicts up to Need; a key
\* where readers disagreed (an ok and a broken) is read afresh.
\* The read tier is one tier here: a kept verdict stands only for its tier or a
\* weaker one, a filter on the key that this module does not vary.
EXTENDS Naturals, FiniteSets

CONSTANTS
    Heads,        \* the heads an attempt may finish at
    Bases,        \* the bases an attempt may finish on
    Readers,      \* the readers
    Need,         \* ok reads from different readers a landing needs
    MaxAttempts,  \* attempts of the card, a bound for the check
    Broken        \* "none"; "nomemo": the ask ignores the memo; "anybase": inheritance ignores the base

Verdicts == {"ok", "broken"}

VARIABLES
    memo,     \* memo[h][b][r]: the verdict r reported at head h on base b, or "none"
    head,     \* the attempt's head
    base,     \* the attempt's base
    attempt,  \* the attempt number
    phase,    \* "work", "review" or "done"
    reads,    \* reads[r]: "none", "asked", or the verdict, at this attempt
    from,     \* from[r]: "none", "fresh" (asked) or "kept" (inherited)
    twice     \* a reader was asked again at a key its verdict was kept by, the key not split

vars == <<memo, head, base, attempt, phase, reads, from, twice>>

NoReads == [r \in Readers |-> "none"]
NoFrom == [r \in Readers |-> "none"]

OKsAt(h, b) == {r \in Readers : memo[h][b][r] = "ok"}
BrokenAt(h, b) == {r \in Readers : memo[h][b][r] = "broken"}
Split(h, b) == OKsAt(h, b) # {} /\ BrokenAt(h, b) # {}

\* the key inheritance reads: the attempt's own, or (the broken variant) another base
InheritBase == IF Broken = "anybase" /\ \E b \in Bases : b # base
               THEN CHOOSE b \in Bases : b # base
               ELSE base

Placed == {r \in Readers : reads[r] # "none"}
OKs == {r \in Readers : reads[r] = "ok"}

Min(a, b) == IF a < b THEN a ELSE b

\* the readers whose kept verdicts the attempt inherits now (inheritedReads): while
\* every read placed stands ok and it needs more; nothing at a key where readers
\* disagreed; else the finding alone before any read stands; else the oks of
\* readers with no read placed, as many as it still needs
Inherit(h, b) ==
    LET oks == OKsAt(h, b)
        brs == BrokenAt(h, b)
        free == oks \ Placed
        k == Min(Need - Cardinality(OKs), Cardinality(free))
    IN  IF Placed # OKs \/ Cardinality(OKs) >= Need THEN {}
        ELSE IF brs # {} /\ oks = {} THEN (IF Placed = {} THEN {CHOOSE r \in brs : TRUE} ELSE {})
        ELSE IF brs # {} THEN {}
        ELSE CHOOSE s \in SUBSET free : Cardinality(s) = k

TypeOK ==
    /\ memo \in [Heads -> [Bases -> [Readers -> {"none"} \cup Verdicts]]]
    /\ head \in Heads /\ base \in Bases
    /\ attempt \in 0..MaxAttempts
    /\ phase \in {"work", "review", "done"}
    /\ reads \in [Readers -> {"none", "asked"} \cup Verdicts]
    /\ from \in [Readers -> {"none", "fresh", "kept"}]
    /\ twice \in BOOLEAN

Init ==
    /\ memo = [h \in Heads |-> [b \in Bases |-> [r \in Readers |-> "none"]]]
    /\ head \in Heads /\ base \in Bases
    /\ attempt = 0
    /\ phase = "work"
    /\ reads = NoReads
    /\ from = NoFrom
    /\ twice = FALSE

\* the work of the next attempt finishes at a head on a base: the card enters review
Finish(h, b) ==
    /\ phase = "work" /\ attempt < MaxAttempts
    /\ head' = h /\ base' = b
    /\ attempt' = attempt + 1
    /\ phase' = "review"
    /\ reads' = NoReads /\ from' = NoFrom
    /\ UNCHANGED <<memo, twice>>

\* the ask, first: what is kept at the attempt's key is inherited, not asked
Inherited ==
    /\ phase = "review" /\ Broken # "nomemo"
    /\ Inherit(head, InheritBase) # {}
    /\ LET i == Inherit(head, InheritBase)
       IN  /\ reads' = [r \in Readers |-> IF r \in i THEN memo[head][InheritBase][r] ELSE reads[r]]
           /\ from' = [r \in Readers |-> IF r \in i THEN "kept" ELSE from[r]]
    /\ UNCHANGED <<memo, head, base, attempt, phase, twice>>

\* the ask: one read at a time, the next once every read that stands came back ok;
\* never while the attempt could inherit (the ask inherits first)
Ask(r) ==
    /\ phase = "review" /\ reads[r] = "none"
    /\ (Inherit(head, InheritBase) = {} \/ Broken = "nomemo")
    /\ \A o \in Placed : reads[o] = "ok"
    /\ Cardinality(OKs) < Need
    /\ reads' = [reads EXCEPT ![r] = "asked"]
    /\ from' = [from EXCEPT ![r] = "fresh"]
    /\ twice' = (twice \/ (memo[head][base][r] # "none" /\ ~Split(head, base)))
    /\ UNCHANGED <<memo, head, base, attempt, phase>>

\* a reader reports: its verdict is kept by the attempt's head and base
Read(r, v) ==
    /\ phase = "review" /\ reads[r] = "asked"
    /\ reads' = [reads EXCEPT ![r] = v]
    /\ memo' = [memo EXCEPT ![head][base][r] = v]
    /\ UNCHANGED <<head, base, attempt, phase, from, twice>>

\* a broken read sends it back (a rework, or a twin): the next attempt's work
Rework ==
    /\ phase = "review" /\ \E r \in Readers : reads[r] = "broken"
    /\ \A r \in Readers : reads[r] # "asked"
    /\ phase' = "work"
    /\ UNCHANGED <<memo, head, base, attempt, reads, from, twice>>

\* the ok reads it needs: it lands, or is replaced by a twin that carries its head
Settle ==
    /\ phase = "review" /\ Cardinality(OKs) >= Need
    /\ phase' \in {"done", "work"}
    /\ UNCHANGED <<memo, head, base, attempt, reads, from, twice>>

Next ==
    \/ \E h \in Heads, b \in Bases : Finish(h, b)
    \/ Inherited
    \/ \E r \in Readers : Ask(r)
    \/ \E r \in Readers, v \in Verdicts : Read(r, v)
    \/ Rework
    \/ Settle

Spec == Init /\ [][Next]_vars

\* No reader is asked again at a head and base its verdict is kept by, unless the
\* readers disagreed there: the night of 79 twins read again at the carried head.
NoReadTwiceAtAKeptHead == ~twice

\* An inherited verdict is the one kept at the attempt's own head and base, by
\* that reader: never a verdict of another base.
InheritedAtItsKey ==
    \A r \in Readers : from[r] = "kept" => reads[r] = memo[head][base][r]

\* An attempt holds inherited verdicts only of one kind: the finding alone, or oks.
InheritedOneKind ==
    LET k == {r \in Readers : from[r] = "kept"}
    IN  \/ \A r \in k : reads[r] = "ok"
        \/ Cardinality(k) = 1
=============================================================================
