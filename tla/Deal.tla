------------------------------ MODULE Deal ------------------------------
EXTENDS Naturals, FiniteSets, TLC
\* docs/SPEC-SPRINT.md section 1, a friend's card; the card
\* the-dealer-honors-who.w1 (2026-10-06: a card whose WHO line named one friend was
\* dealt to another friend twice while she was up with room). The tick's friend
\* deal (internal/sprint/friend_deal.go, friendDeal), the machines' deal after
\* it (dealPlan, TickDeal), friend take of a card onto a friend's row
\* (friend_take.go, friendTakeOnto) and friend take of an unstarted card back
\* to ready (FriendTake). Each card's who is "none" (no WHO line), "any"
\* (WHO: friend) or a friend's name (WHO: friend <name>, or only friend <name>,
\* which the deal treats alike: PinnedFriend). A card is ready (owner "none"),
\* dealt to a worker and not taken, or taken. Room is one card a worker; up
\* changes freely. The model checks where a card may sit, not tiers, routes,
\* generations or retry bounds (WhoPreference.tla's selection among unnamed
\* cards still holds: an unnamed card goes to a friend first).
CONSTANTS BadFallback, BadTakeOnto
Cards == {"c1", "c2"}
Friends == {"a", "b"}
Workers == Friends \cup {"fleet"}
VARIABLES who, owner, taken, up
vars == <<who, owner, taken, up>>

Init == /\ who \in [Cards -> {"none", "any"} \cup Friends]
        /\ owner = [c \in Cards |-> "none"]
        /\ taken = {}
        /\ up \in SUBSET Workers

Room(w) == w \in up /\ Cardinality({c \in Cards : owner[c] = w}) < 1

\* where the deal may place a ready card: a named card on her row alone (with
\* BadFallback, the old preference: any friend up with room, then the fleet)
Candidates(c) ==
  IF who[c] \in Friends /\ ~BadFallback
  THEN {f \in {who[c]} : Room(f)}
  ELSE IF who[c] \in Friends /\ Room(who[c]) THEN {who[c]}
  ELSE LET fs == {f \in Friends : Room(f)}
       IN IF fs # {} THEN fs ELSE {w \in {"fleet"} : Room(w)}

\* the tick's deal of one ready card (friendDeal, then dealPlan)
Deal(c, w) == /\ owner[c] = "none"
              /\ w \in Candidates(c)
              /\ owner' = [owner EXCEPT ![c] = w]
              /\ UNCHANGED <<who, taken, up>>

\* a worker takes a card dealt to it (a friend's deal into working is this at once)
Take(c) == /\ owner[c] \in Workers /\ c \notin taken
           /\ taken' = taken \cup {c}
           /\ UNCHANGED <<who, owner, up>>

\* friend take <f> <id> of a card not on her row: ready, or dealt and not taken,
\* wherever it sits; refused when taken, or when its WHO line names another
\* friend (with BadTakeOnto, that refusal is missing)
TakeOnto(c, f) == /\ owner[c] # f /\ c \notin taken
                  /\ (who[c] \in Friends /\ who[c] # f) => BadTakeOnto
                  /\ owner' = [owner EXCEPT ![c] = f]
                  /\ UNCHANGED <<who, taken, up>>

\* friend take <f> <id> of an unstarted card on her row: back to ready
TakeBack(c) == /\ owner[c] \in Friends /\ c \notin taken
               /\ owner' = [owner EXCEPT ![c] = "none"]
               /\ UNCHANGED <<who, taken, up>>

\* a taken card finishes and leaves the rows
Finish(c) == /\ c \in taken
             /\ owner' = [owner EXCEPT ![c] = "none"]
             /\ taken' = taken \ {c}
             /\ who' = [who EXCEPT ![c] = "done"]
             /\ UNCHANGED up

\* unpin drops the WHO line of a ready card
Unpin(c) == /\ owner[c] = "none" /\ who[c] \in Friends
            /\ who' = [who EXCEPT ![c] = "none"]
            /\ UNCHANGED <<owner, taken, up>>

Presence == /\ up' \in SUBSET Workers
            /\ UNCHANGED <<who, owner, taken>>

Next == \/ \E c \in Cards, w \in Workers : Deal(c, w)
        \/ \E c \in Cards, f \in Friends : TakeOnto(c, f)
        \/ \E c \in Cards : Take(c) \/ TakeBack(c) \/ Finish(c) \/ Unpin(c)
        \/ Presence
Spec == Init /\ [][Next]_vars

\* a card whose WHO line names a friend is never on another worker's row
WhoIsHonored == \A c \in Cards : who[c] \in Friends /\ owner[c] # "none" => owner[c] = who[c]
\* a card naming a friend is never a machine's (WHO: friend, any friend, still is
\* the fleet's when no friend takes it)
NeverTheFleets == \A c \in Cards : who[c] \in Friends => owner[c] # "fleet"
TakenIsPlaced == \A c \in taken : owner[c] \in Workers
TypeOK == /\ who \in [Cards -> {"none", "any", "done"} \cup Friends]
          /\ owner \in [Cards -> Workers \cup {"none"}]
          /\ taken \subseteq Cards
          /\ up \subseteq Workers
=============================================================================
