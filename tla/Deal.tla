------------------------------- MODULE Deal -------------------------------
EXTENDS Naturals, FiniteSets, TLC
\* docs/SPEC-SPRINT.md section 5, the deal: priority (internal/sprint/priority.go,
\* dealOrder over streamTurns, used by TickDeal, friendDeal and the deal verb).
\* A priority is a stream's level, never a card's. The deal takes a ready card
\* of the highest level that some worker eligible for it has room for; inside a
\* level any stream may be next (the stream turns, left free here); within a
\* stream the cards go in their rank order. Workers are machines and friends
\* alike: each takes the tiers it holds, within its room. Outside events: a
\* worker comes up or goes down, a dealt card finishes and frees its room, and
\* the coordinator sets a stream's level (priority set).
\* BadIgnoreLevel is the deal before this card: stream turns alone, the level
\* not read (MCDealBrokenLevelIgnored.cfg).
CONSTANT BadIgnoreLevel
Streams == {"s1", "s2"}
Cards == {"a1", "a2", "b1"}
StreamOf == [c \in Cards |-> IF c = "b1" THEN "s2" ELSE "s1"]
Rank == [c \in Cards |-> IF c = "a2" THEN 2 ELSE 1]
Tier == [c \in Cards |-> IF c = "a1" THEN "flash" ELSE "pro"]
Workers == {"m", "f"}
Tiers == [w \in Workers |-> IF w = "m" THEN {"flash"} ELSE {"flash", "pro"}]
Levels == 0..2
VARIABLES owner, done, up, level
vars == <<owner, done, up, level>>

Init == /\ owner = [c \in Cards |-> "none"]
        /\ done = {}
        /\ up \in SUBSET Workers
        /\ level \in [Streams -> Levels]

Ready(c) == owner[c] = "none" /\ c \notin done
Room(w) == w \in up /\ Cardinality({c \in Cards : owner[c] = w /\ c \notin done}) < 1
Eligible(c, w) == Tier[c] \in Tiers[w]
Dealable(c) == Ready(c) /\ \E w \in Workers : Eligible(c, w) /\ Room(w)
Lv(c) == level[StreamOf[c]]
\* d comes before c in the deal's order: a higher level, or the same stream and
\* an earlier rank (the stream's own order); streams of one level take turns,
\* and the model leaves the turn free.
Ahead(d, c) == \/ (~BadIgnoreLevel /\ Lv(d) > Lv(c))
               \/ (StreamOf[d] = StreamOf[c] /\ Rank[d] < Rank[c])
\* the deal offers cards in its order: the first one some eligible worker has
\* room for is placed; a card no worker may take costs no turn
Deal(c, w) == /\ Ready(c) /\ Eligible(c, w) /\ Room(w)
              /\ ~\E d \in Cards : d # c /\ Ahead(d, c) /\ Dealable(d)
              /\ owner' = [owner EXCEPT ![c] = w]
              /\ UNCHANGED <<done, up, level>>
Finish(c) == /\ owner[c] # "none" /\ c \notin done
             /\ done' = done \cup {c}
             /\ UNCHANGED <<owner, up, level>>
Presence == /\ up' \in SUBSET Workers
            /\ UNCHANGED <<owner, done, level>>
SetLevel(s, n) == /\ level' = [level EXCEPT ![s] = n]
                  /\ UNCHANGED <<owner, done, up>>
Next == \/ \E c \in Cards, w \in Workers : Deal(c, w)
        \/ \E c \in Cards : Finish(c)
        \/ Presence
        \/ \E s \in Streams, n \in Levels : SetLevel(s, n)
Spec == Init /\ [][Next]_vars

TypeOK == /\ owner \in [Cards -> Workers \cup {"none"}]
          /\ done \subseteq Cards
          /\ up \subseteq Workers
          /\ level \in [Streams -> Levels]
WidthRespected == \A w \in Workers : Cardinality({c \in Cards : owner[c] = w /\ c \notin done}) <= 1
\* the card's rule: a card of a higher level is never passed over for a lower
\* one while a worker eligible for it has room
DealtNow(c) == owner[c] = "none" /\ owner'[c] # "none"
LevelFirst == [][\A c \in Cards : DealtNow(c) =>
                   ~\E d \in Cards : Ready(d) /\ Lv(d) > Lv(c) /\ \E w \in Workers : Eligible(d, w) /\ Room(w)]_vars
\* a priority never reorders a stream: a card is never dealt while an earlier
\* card of its stream is ready and some worker eligible for it has room
StreamOrder == [][\A c \in Cards : DealtNow(c) =>
                   ~\E d \in Cards : StreamOf[d] = StreamOf[c] /\ Rank[d] < Rank[c] /\ Dealable(d)]_vars
=============================================================================
