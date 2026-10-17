-------------------------- MODULE WhoPreference --------------------------
EXTENDS Naturals, FiniteSets, TLC
\* docs/SPEC-SPRINT.md: WHO preference and unpin. One ready card of each
\* kind, two subscription friends, one fleet worker, one lane each. A deal
\* consumes room atomically; up/down changes eligibility.
\* The model checks selection, not transport, route rotation or retry bounds.
CONSTANT BadOnly
Cards == {"plain", "preferred", "only"}
Friends == {"a", "b"}
Workers == Friends \cup {"fleet"}
VARIABLES owner, pin, up, started
vars == <<owner, pin, up, started>>
Init == /\ owner = [c \in Cards |-> "none"]
        /\ pin = [c \in Cards |-> IF c = "plain" THEN "none" ELSE c]
        /\ up \in SUBSET Workers
        /\ started = {}
Room(w) == w \in up /\ Cardinality({c \in Cards : owner[c] = w}) < 1
\* Both friends cover the example pro tier; fleet is the final fallback.
Candidate(c) ==
 IF pin[c] # "none" /\ Room("a") THEN {"a"}
 ELSE IF pin[c] = "only" /\ ~BadOnly THEN {}
 ELSE IF Room("a") THEN {"a"}
 ELSE IF Room("b") THEN {"b"}
 ELSE IF Room("fleet") THEN {"fleet"}
 ELSE {}
Deal(c,w) == /\ owner[c] = "none" /\ w \in Candidate(c)
             /\ owner' = [owner EXCEPT ![c] = w]
             /\ UNCHANGED <<pin, up, started>>
Start(c) == /\ owner[c] \in Workers /\ c \notin started
            /\ started' = started \cup {c}
            /\ UNCHANGED <<owner,pin,up>>
\* friend take returns a dealt but unstarted card; the same identity can be
\* unpinned and offered again. Started work cannot take this transition.
TakeBack(c) == /\ owner[c] \in Friends /\ c \notin started
               /\ owner' = [owner EXCEPT ![c] = "none"]
               /\ UNCHANGED <<pin,up,started>>
Unpin(c) == /\ owner[c] = "none" /\ c \notin started /\ pin[c] # "none"
            /\ pin' = [pin EXCEPT ![c] = "none"]
            /\ UNCHANGED <<owner,up,started>>
Presence == /\ up' \in SUBSET Workers /\ UNCHANGED <<owner,pin,started>>
Next == (\E c \in Cards, w \in Workers : Deal(c,w))
        \/ (\E c \in Cards : Start(c) \/ TakeBack(c) \/ Unpin(c)) \/ Presence
Spec == Init /\ [][Next]_vars
OnlyToItsFriend == \A c \in Cards : pin[c] = "only" => owner[c] \in {"none","a"}
WidthRespected == \A w \in Workers : Cardinality({c \in Cards : owner[c] = w}) <= 1
TypeOK == /\ owner \in [Cards -> Workers \cup {"none"}]
          /\ pin \in [Cards -> {"none","preferred","only"}]
          /\ up \subseteq Workers /\ started \subseteq Cards
=============================================================================
