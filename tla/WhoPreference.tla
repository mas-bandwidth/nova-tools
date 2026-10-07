-------------------------- MODULE WhoPreference --------------------------
EXTENDS Naturals, FiniteSets, TLC
\* docs/SPEC-SPRINT.md: WHO preference and unpin, and the rebalance (section 1).
\* One ready card of each kind, two subscription friends, one fleet worker,
\* one lane each and room for DealAhead (two) cards each. A deal consumes room
\* atomically; up/down changes eligibility. A card is started in its worker's
\* free lane. The rebalance moves a card dealt and not started, queued on a
\* worker whose lane works, to a worker up with an idle lane (no card held)
\* whose tier set admits the card's tier and that the card has not left.
\* The model checks selection, not transport, route rotation or retry bounds.
\* BadRebalance is the reversed witness: "started" lets the rebalance move a
\* started card (NoStartedCardMoves breaks), "tiers" lets it ignore the tier
\* set (RebalancedAdmitted breaks); "none" is the code's rule.
CONSTANT BadOnly, BadRebalance
Cards == {"plain", "preferred", "only"}
Friends == {"a", "b"}
Workers == Friends \cup {"fleet"}
Tiers == {"flash", "pro"}
CardTier == [c \in Cards |-> IF c = "preferred" THEN "pro" ELSE "flash"]
FriendTiers == [f \in Friends |-> IF f = "a" THEN {"flash", "pro"} ELSE {"pro"}]
VARIABLES owner, pin, up, started, fleetTiers, left, rebalanced
vars == <<owner, pin, up, started, fleetTiers, left, rebalanced>>
Init == /\ owner = [c \in Cards |-> "none"]
        /\ pin = [c \in Cards |-> IF c = "plain" THEN "none" ELSE c]
        /\ up \in SUBSET Workers
        /\ started = {}
        /\ fleetTiers \in SUBSET Tiers
        /\ left = [c \in Cards |-> {}]
        /\ rebalanced = {}
Held(w) == {c \in Cards : owner[c] = w}
Room(w) == w \in up /\ Cardinality(Held(w)) < 2
Admits(w, t) == IF w = "fleet" THEN t \in fleetTiers ELSE t \in FriendTiers[w]
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
             /\ rebalanced' = rebalanced \ {c}
             /\ UNCHANGED <<pin, up, started, fleetTiers, left>>
LaneBusy(w) == \E d \in started : owner[d] = w
Start(c) == /\ owner[c] \in Workers /\ c \notin started /\ ~LaneBusy(owner[c])
            /\ started' = started \cup {c}
            /\ UNCHANGED <<owner,pin,up,fleetTiers,left,rebalanced>>
\* friend take returns a dealt but unstarted card; the same identity can be
\* unpinned and offered again. Started work cannot take this transition.
TakeBack(c) == /\ owner[c] \in Friends /\ c \notin started
               /\ owner' = [owner EXCEPT ![c] = "none"]
               /\ rebalanced' = rebalanced \ {c}
               /\ UNCHANGED <<pin,up,started,fleetTiers,left>>
Unpin(c) == /\ owner[c] = "none" /\ c \notin started /\ pin[c] # "none"
            /\ pin' = [pin EXCEPT ![c] = "none"]
            /\ UNCHANGED <<owner,up,started,fleetTiers,left,rebalanced>>
\* the rebalance (internal/sprint/rebalance.go Rebalance): a queued card on a
\* worker whose lane works goes to a worker up with an idle lane that may take
\* it; never a started card, never a hard pin, never a worker it left. The code
\* also keeps a preference on the friend it names; the model lets it move, a
\* superset of the code's moves, so what holds here holds of the code.
Rebalance(c, w) ==
  LET g == owner[c] IN
  /\ g \in Workers /\ w \in up /\ w # g
  /\ (c \notin started \/ BadRebalance = "started")
  /\ pin[c] # "only"
  /\ LaneBusy(g)
  /\ Held(w) = {}
  /\ (Admits(w, CardTier[c]) \/ BadRebalance = "tiers")
  /\ w \notin left[c]
  /\ owner' = [owner EXCEPT ![c] = w]
  /\ left' = [left EXCEPT ![c] = @ \cup {g}]
  /\ rebalanced' = rebalanced \cup {c}
  /\ UNCHANGED <<pin, up, started, fleetTiers>>
Presence == /\ up' \in SUBSET Workers /\ UNCHANGED <<owner,pin,started,fleetTiers,left,rebalanced>>
Next == (\E c \in Cards, w \in Workers : Deal(c,w) \/ Rebalance(c,w))
        \/ (\E c \in Cards : Start(c) \/ TakeBack(c) \/ Unpin(c)) \/ Presence
Spec == Init /\ [][Next]_vars
OnlyToItsFriend == \A c \in Cards : pin[c] = "only" => owner[c] \in {"none","a"}
\* room: DealAhead (two) times a width of one
WidthRespected == \A w \in Workers : Cardinality(Held(w)) <= 2
LanesRespected == \A w \in Workers : Cardinality({c \in started : owner[c] = w}) <= 1
\* every card the rebalance placed sits on a worker whose tier set admits it
RebalancedAdmitted == \A c \in rebalanced : owner[c] \in Workers => Admits(owner[c], CardTier[c])
\* no started card moves, by any action
NoStartedCardMoves == [][\A c \in started : owner'[c] = owner[c]]_vars
TypeOK == /\ owner \in [Cards -> Workers \cup {"none"}]
          /\ pin \in [Cards -> {"none","preferred","only"}]
          /\ up \subseteq Workers /\ started \subseteq Cards
          /\ fleetTiers \subseteq Tiers
          /\ left \in [Cards -> SUBSET Workers]
          /\ rebalanced \subseteq Cards
=============================================================================
