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
\* THE MEMBER'S HARNESSES (fault 10, 2026-10-10: cards on heavy-opus-claude were
\* dealt to batman, space, superman and vision, which have no claude, and every
\* launch was refused). Unlaunch is the routes the dealing member cannot launch:
\* a route whose harness is headless (claude, codex, grok) that the member's
\* control card does not name (fleet up --harnesses). The work deal and the
\* redeal walk past such an entry as they walk past one that names no enabled
\* route: it is never taken, however the exclusion of a redeal lapses, and the
\* index moves past it; a card whose tier has no launchable entry is not dealt
\* to that member (it waits for one that can). A read is drawn by the ask, not
\* for a member, and is outside this rule.
\*
\* Broken: "none" is the design; "random" takes any entry of the array (the
\* weighted draw this replaces: RouteFair fails); "noadvance" is a redeal that
\* takes the entry at the place without moving past the excluded one
\* (ExcludedNeverDrawn fails); "readapart" is a read that takes the entry at
\* the place and leaves the index where it is, a read kept apart from the
\* tier's rotation (RouteIndexAdvancesOncePerCard fails); "harnessblind" is the
\* deal before fault 10, drawing the entry at the place whatever the member can
\* launch (NeverUnlaunchable fails).
\*
\* WHAT IS NOT MODELLED. Members, widths and the tick (DirtyTick.tla); an entry
\* that names no enabled route (the deal skips it as it skips an excluded one,
\* and the Go test pins it); the array changed between deals (config, read once
\* a tick); a plan's dropped unit (round.go's rewrite, the member rule's). The
\* cards of one tier are interchangeable (the instance's symmetry): which card
\* takes an entry does not change what the index does.
EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS Tiers, Arr, Cards, TierOf, Pinned, Reads, MaxRedeals, Broken, Unlaunch

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

\* The routes a card's deal walks past whatever else: those the member cannot
\* launch, for a work card (a read is the ask's).
Blind(c) == IF c \in Reads THEN {} ELSE Unlaunch

\* Whether tier t's array holds an entry outside ex.
Open(t, ex) == \E k \in 1..Len(Arr[t]) : Arr[t][k] \notin ex

\* A pick: the card, the tier, the route, what it left out, whether an entry was
\* not left out (the exclusion held), and the entries passed over by the rule.
Pick(c, r, ex, by) == [c |-> c, t |-> TierOf[c], r |-> r, ex |-> ex,
                       fresh |-> Open(TierOf[c], ex \cup Blind(c)),
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

\* The deal of a ready card that pins no model: the entry at the place, one step,
\* or the first from it the member can launch, the index moved past those walked.
Deal(c) ==
  LET t == TierOf[c]
      s == Steps(t, ridx[t], Blind(c))
  IN
  /\ c \notin Pinned
  /\ st[c] = "ready"
  /\ Broken = "harnessblind" \/ Open(t, Blind(c))
  /\ st' = [st EXCEPT ![c] = "dealt"]
  /\ UNCHANGED rdl
  /\ IF Broken = "random"
     THEN \E k \in 1..Len(Arr[t]) : Take(c, Arr[t][k], 1, 1)
     ELSE IF Broken = "readapart" /\ c \in Reads
     THEN Take(c, At(t, ridx[t]), 0, 1)
     ELSE IF Broken = "harnessblind"
     THEN Take(c, At(t, ridx[t]), 1, 1)
     ELSE Take(c, At(t, ridx[t] + s - 1), s, s)

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
\* moved past it and every entry skipped. The routes already taken are left out
\* while a launchable entry is not one of them (else that exclusion lapses); an
\* entry the member cannot launch is never taken.
Redeal(c) ==
  LET t == TierOf[c]
      ex == IF Open(t, drawn[c] \cup Blind(c)) THEN drawn[c] \cup Blind(c) ELSE Blind(c)
      s == Steps(t, ridx[t], ex)
  IN
  /\ st[c] = "withdrawn"
  /\ Broken = "harnessblind" \/ Open(t, Blind(c))
  /\ st' = [st EXCEPT ![c] = "dealt"]
  /\ rdl' = [rdl EXCEPT ![c] = @ + 1]
  /\ IF Broken = "noadvance"
     THEN Take(c, At(t, ridx[t]), 1, s)
     ELSE IF Broken = "harnessblind"
     THEN LET sb == Steps(t, ridx[t], drawn[c]) IN Take(c, At(t, ridx[t] + sb - 1), sb, sb)
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

\* A work card is never dealt on a route its member cannot launch (fault 10).
NeverUnlaunchable == last.c \notin Reads => last.r \notin Unlaunch

\* Reachability (a reversed witness, written to be false where the design must
\* reach): a redeal that passed over an entry it left out.
ReachSkip == \A t \in Tiers : skipped[t] = 0
=============================================================================
