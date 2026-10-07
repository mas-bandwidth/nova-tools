-------------------------- MODULE Deal --------------------------
EXTENDS Naturals, FiniteSets
\* SPEC-SPRINT WHO ownership and explicit friend take, on two cards and friends.
CONSTANT BreakWho
Cards == {"named", "plain"}
Friends == {"a", "b"}
Workers == Friends \cup {"fleet"}
VARIABLES who, owner, phase, up
vars == <<who, owner, phase, up>>
Init == /\ who = [c \in Cards |-> IF c = "named" THEN "a" ELSE "none"]
        /\ owner = [c \in Cards |-> "none"]
        /\ phase = [c \in Cards |-> "ready"]
        /\ up \in SUBSET Workers
Room(w) == w \in up /\ Cardinality({c \in Cards : owner[c] = w}) < 1
Deal(c,w) == /\ phase[c] = "ready" /\ Room(w)
             /\ (who[c] = "none" \/ who[c] = w \/ BreakWho)
             /\ owner' = [owner EXCEPT ![c] = w]
             /\ phase' = [phase EXCEPT ![c] = "dealt"]
             /\ UNCHANGED <<who,up>>
Take(c) == /\ phase[c] = "dealt"
           /\ phase' = [phase EXCEPT ![c] = "taken"]
           /\ UNCHANGED <<who,owner,up>>
\* The coordinator explicitly transfers ownership only before a lane takes it.
FriendTake(c,f) == /\ phase[c] \in {"ready","dealt"} /\ f \in Friends
                  /\ who' = [who EXCEPT ![c] = f]
                  /\ owner' = [owner EXCEPT ![c] = f]
                  /\ phase' = [phase EXCEPT ![c] = "dealt"]
                  /\ UNCHANGED up
Presence == /\ up' \in SUBSET Workers /\ UNCHANGED <<who,owner,phase>>
Next == (\E c \in Cards, w \in Workers : Deal(c,w))
        \/ (\E c \in Cards : Take(c))
        \/ (\E c \in Cards, f \in Friends : FriendTake(c,f)) \/ Presence
Spec == Init /\ [][Next]_vars
WhoIsHonored == \A c \in Cards : who[c] # "none" => owner[c] \in {"none",who[c]}
TakenKeepsItsLane == [][\A c \in Cards : phase[c] = "taken" => owner'[c] = owner[c]]_vars
TypeOK == /\ who \in [Cards -> Friends \cup {"none"}]
          /\ owner \in [Cards -> Workers \cup {"none"}]
          /\ phase \in [Cards -> {"ready","dealt","taken"}] /\ up \subseteq Workers
=================================================================
