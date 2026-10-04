----------------------------- MODULE RouteIndex -----------------------------
\* The deal's route choice (internal/sprint/route.go), written before it is
\* built: the owner, 2026-10-01, "I want model routing to use the same uint64
\* modulo -- all providers/models per-tier, should be an array with this same
\* index approach". It extends the deal of DirtyTick.tla, whose member rule
\* takes a uint64 counter modulo the fleet (deal_index), with one counter per
\* tier over the tier's route array (route_index_flash, route_index_pro on the
\* fleet table); DirtyTick.tla holds which route is taken out of its state, and
\* this module is that choice alone.
\*
\* THE STATE.
\*   ridx      the tiers' route indexes: uint64 counters, never reduced (the
\*             place is the counter modulo the array's length)
\*   st        a card's state: ready, dealt (on a route), or withdrawn (its
\*             take ended: a member lost, a provider failure)
\*   route     the route a card was dealt on last ("none" before its first deal,
\*             "pin" for a card that pins a model)
\*   drawn     the routes taken for a card: what a redeal leaves out
\*   rdl       a card's redeals, bounded by MaxRedeals
\*   deals, skipped, hist, last   ghosts: the tier's cards dealt (not pinned);
\*             the array entries the deals passed over, by the rule; the tier's
\*             picks in order, each with whether it passed over an entry; the
\*             last pick
\*
\* THE RULE (RouteIndexAdvancesOncePerCard, RouteFair, PinNeverAdvances,
\* ExcludedNeverDrawn). A card of tier t that pins no model takes Arr[t] at the
\* place ridx[t] mod Len(Arr[t]); its deal moves ridx[t] by one. A redeal leaves
\* out the routes already taken for the card while an entry of the array is not
\* one of them: it takes the first entry from the place that is not left out,
\* and the index moves past every entry it skipped and the one it took. When
\* every entry is left out the exclusion lapses and the entry at the place is
\* taken. A pinned card bypasses the array and moves no index.
\*
\* THE READS (2026-10-01, the owner: a read card carried no route and every
\* reader loop was started by hand with its model). A read card (Reads) is
\* drawn as a work card is, from the reader tier's array (TierOf names it: the
\* sprint row's reader_tier in nova-config, pro by default) at the same index:
\* the ask that creates it takes the entry at the place and moves the index by
\* one, so the work deal and the reads of a tier share one rotation
\* (RouteFair over every pick of the tier). A read is never withdrawn and dealt
\* again: a read handed back is asked again on its own card or on a new one,
\* each a deal of its own (internal/sprint Ask).
\*
\* Broken: "none" is the design; "random" takes any entry of the array (the
\* weighted draw this replaces: RouteFair fails); "noadvance" is a redeal that
\* takes the entry at the place without moving past the excluded one
\* (ExcludedNeverDrawn fails); "readapart" is a read that takes the entry at
\* the place and leaves the index where it is, a read kept apart from the
\* tier's rotation (RouteIndexAdvancesOncePerCard fails).
\*
\* WHAT IS NOT MODELLED. Members, widths and the tick (DirtyTick.tla); an entry
\* that names no enabled route (the deal skips it as it skips an excluded one,
\* and the Go test pins it); the array changed between deals (config, read once
\* a tick); a plan's dropped unit (round.go's rewrite, the member rule's). The
\* cards of one tier are interchangeable (the instance's symmetry): which card
\* takes an entry does not change what the index does.
EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS Tiers, Arr, Cards, TierOf, Pinned, Reads, MaxRedeals, Broken

VARIABLES ridx, st, route, drawn, rdl, deals, skipped, hist, last

vars == <<ridx, st, route, drawn, rdl, deals, skipped, hist, last>>

Routes == UNION {{Arr[t][k] : k \in 1..Len(Arr[t])} : t \in Tiers}

\* The entry at the place of counter i in tier t's array.
At(t, i) == Arr[t][(i % Len(Arr[t])) + 1]

\* The steps a deal from counter i takes, leaving out ex: one more than the
\* entries it passes over; 1 when every entry is left out (the exclusion lapses).
Steps(t, i, ex) ==
  IF \A k \in 1..Len(Arr[t]) : Arr[t][k] \in ex
  THEN 1
  ELSE CHOOSE s \in 1..Len(Arr[t]) :
         /\ At(t, i + s - 1) \notin ex
         /\ \A u \in 1..(s - 1) : At(t, i + u - 1) \in ex

\* A pick: the card, the tier, the route, what it left out, whether an entry was
\* not left out (the exclusion held), and the entries passed over by the rule.
Pick(c, r, ex, by) == [c |-> c, t |-> TierOf[c], r |-> r, ex |-> ex,
                       fresh |-> \E k \in 1..Len(Arr[TierOf[c]]) : Arr[TierOf[c]][k] \notin ex,
                       sk |-> by - 1]

TypeOK ==
  /\ ridx \in [Tiers -> Nat]
  /\ st \in [Cards -> {"ready", "dealt", "withdrawn"}]
  /\ route \in [Cards -> Routes \cup {"none", "pin"}]
  /\ drawn \in [Cards -> SUBSET Routes]
  /\ rdl \in [Cards -> 0..MaxRedeals]
  /\ deals \in [Tiers -> Nat]
  /\ skipped \in [Tiers -> Nat]

Init ==
  /\ ridx = [t \in Tiers |-> 0]
  /\ st = [c \in Cards |-> "ready"]
  /\ route = [c \in Cards |-> "none"]
  /\ drawn = [c \in Cards |-> {}]
  /\ rdl = [c \in Cards |-> 0]
  /\ deals = [t \in Tiers |-> 0]
  /\ skipped = [t \in Tiers |-> 0]
  /\ hist = [t \in Tiers |-> <<>>]
  /\ last = [c |-> "none", t |-> "none", r |-> "none", ex |-> {}, fresh |-> FALSE, sk |-> 0]

\* Take route r for card c, the index moved by adv; by is the steps the rule says.
Take(c, r, adv, by) ==
  LET t == TierOf[c] IN
  /\ ridx' = [ridx EXCEPT ![t] = @ + adv]
  /\ route' = [route EXCEPT ![c] = r]
  /\ drawn' = [drawn EXCEPT ![c] = @ \cup {r}]
  /\ deals' = [deals EXCEPT ![t] = @ + 1]
  /\ skipped' = [skipped EXCEPT ![t] = @ + (by - 1)]
  /\ hist' = [hist EXCEPT ![t] = Append(@, [r |-> r, sk |-> by > 1])]
  /\ last' = Pick(c, r, drawn[c], by)

\* The deal of a ready card that pins no model: the entry at the place, one step.
Deal(c) ==
  /\ c \notin Pinned
  /\ st[c] = "ready"
  /\ st' = [st EXCEPT ![c] = "dealt"]
  /\ UNCHANGED rdl
  /\ IF Broken = "random"
     THEN \E k \in 1..Len(Arr[TierOf[c]]) : Take(c, Arr[TierOf[c]][k], 1, 1)
     ELSE IF Broken = "readapart" /\ c \in Reads
     THEN Take(c, At(TierOf[c], ridx[TierOf[c]]), 0, 1)
     ELSE Take(c, At(TierOf[c], ridx[TierOf[c]]), 1, 1)

\* A pinned card is dealt on its pin: the array and the index untouched.
Pin(c) ==
  /\ c \in Pinned
  /\ st[c] = "ready"
  /\ st' = [st EXCEPT ![c] = "dealt"]
  /\ route' = [route EXCEPT ![c] = "pin"]
  /\ UNCHANGED <<ridx, drawn, rdl, deals, skipped, hist, last>>

\* The card's take ends without a finish (its member lost, the provider failed):
\* withdrawn, to be dealt again, while its redeals are under the bound.
Withdraw(c) ==
  /\ c \notin Pinned \cup Reads
  /\ st[c] = "dealt"
  /\ rdl[c] < MaxRedeals
  /\ st' = [st EXCEPT ![c] = "withdrawn"]
  /\ UNCHANGED <<ridx, route, drawn, rdl, deals, skipped, hist, last>>

\* The redeal: the next entry from the place that is not left out, the index
\* moved past it and every entry skipped.
Redeal(c) ==
  LET t == TierOf[c]
      s == Steps(t, ridx[t], drawn[c])
  IN
  /\ st[c] = "withdrawn"
  /\ st' = [st EXCEPT ![c] = "dealt"]
  /\ rdl' = [rdl EXCEPT ![c] = @ + 1]
  /\ IF Broken = "noadvance"
     THEN Take(c, At(t, ridx[t]), 1, s)
     ELSE Take(c, At(t, ridx[t] + s - 1), s, s)

\* A card that finishes is one that is not withdrawn again: no action of its own.
Next == \E c \in Cards : Deal(c) \/ Pin(c) \/ Withdraw(c) \/ Redeal(c)

Spec == Init /\ [][Next]_vars

\* After N cards of a tier dealt, the index is N, and one more for every entry
\* the rule passed over (none when no redeal left a route out): the place is
\* N mod Len(Arr[t]).
RouteIndexAdvancesOncePerCard == \A t \in Tiers : ridx[t] = deals[t] + skipped[t]

\* Over any Len(Arr[t]) consecutive picks of a tier that passed over no entry,
\* every entry of the array is taken exactly once: each route as many times as
\* the array names it.
Count(s, r) == Cardinality({j \in 1..Len(s) : s[j] = r})
RouteFair ==
  \A t \in Tiers :
    LET n == Len(Arr[t]) IN
    \A i \in 1..(Len(hist[t]) - n + 1) :
      (\A j \in i..(i + n - 1) : ~hist[t][j].sk) =>
        \A r \in Routes :
          Cardinality({j \in i..(i + n - 1) : hist[t][j].r = r}) = Count(Arr[t], r)

\* A pinned card's deal moves no index.
PinNeverAdvances == [][\A c \in Pinned : (st[c] = "ready" /\ st'[c] = "dealt") => ridx' = ridx]_vars

\* A redeal never takes a route left out while an entry of the array is not.
ExcludedNeverDrawn == last.fresh => last.r \notin last.ex

\* Reachability (a reversed witness, written to be false where the design must
\* reach): a redeal that passed over an entry it left out.
ReachSkip == \A t \in Tiers : skipped[t] = 0
=============================================================================
