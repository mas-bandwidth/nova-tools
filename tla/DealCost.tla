------------------------------ MODULE DealCost ------------------------------
EXTENDS Naturals, FiniteSets, TLC
\* The deal by cost, whoever can work now first (the owner, 2026-10-09 and
\* 2026-10-10: "cards need to fill working slots FIRST across fleet and friends,
\* then go to ready overflow up to 2X"; "the deal is lowest cost first").
\* internal/sprint/steps_tick.go TickDeal and internal/sprint/rebalance.go
\* Rebalance are the code; docs/SPEC-SPRINT.md section 1, the deal by cost.
\*
\* Members are fleet machines and friends alike, each with a width (its working
\* slots), a cost rank (a subscription friend 0, a fleet machine 1, an API-rate
\* friend 2) and the tiers it may run. A card is in the pool, dealt to a member
\* (ready on its row, NOT working), started there (working: its slot taken), or
\* done. Dealt is not working: only a start takes a slot, and only the member's
\* own start. A member's free slots are its width less every card it holds,
\* started or dealt: a dealt card claims the slot it will start in. A member's
\* overflow is what it holds beyond its width: dealt cards no slot of its own
\* will take this tick.
\*
\* The tick is a run of steps (phase "tick") that ends when none is enabled:
\*   Return:  a dealt card of a member up over its cap (ready > 2 x width) goes
\*            back to the pool (a member down is no unit of the rebalance: its
\*            cards are the hold's and the stall ladder's, outside this module);
\*   WorkNow: a pool card, or a dealt card in its member's overflow, goes to a
\*            member up with a free slot that may run it, the cheapest first;
\*   Stack:   a pool card goes to a member up below its room (DealAhead, two,
\*            times its width, ready and started together) only when no member up
\*            that may run it has a free slot, the cheapest first.
\* Between ticks (phase "env") members start dealt cards into free slots, finish
\* started cards, and go up or down (Churn).
\*
\* Bad is the reversed witness: "none" is the code's rule;
\*   "friendsfirst": the old deal, friends stacked before any slot is filled
\*                   (Stack ignores free slots) and nothing moving a dealt card
\*                   after it: NoQueuedWhileFreeSlot breaks;
\*   "busyonly":     the old rebalance, which moved a dealt card only off a member
\*                   whose slots all worked (a friend row of 2026-10-10, 28 of 32 working and
\*                   50 ready): NoQueuedWhileFreeSlot breaks;
\*   "nocap":        no return over the cap: ReadyCap breaks;
\*   "costblind":    WorkNow to any member with a free slot: CheapestFirst breaks.
CONSTANT Bad, Churn

Fleet == {"m1", "m2"}
Friends == {"a", "b"}
Members == Fleet \cup Friends
Cards == {"c1", "c2", "c3", "c4"}
Width == [m \in Members |-> IF m \in {"m2", "a"} THEN 2 ELSE 1]
\* a: a subscription friend ($0); b: an API-rate friend, dearer than the fleet
Cost == [m \in Members |-> CASE m = "a" -> 0 [] m = "b" -> 2 [] OTHER -> 1]
\* a frontier card never goes to the fleet: only friend a runs frontier
MemberTiers == [m \in Members |-> IF m = "a" THEN {"flash", "frontier"} ELSE {"flash"}]
CardTier == [c \in Cards |-> IF c = "c1" THEN "frontier" ELSE "flash"]
Places == {"pool", "done"} \cup Members

\* settled: the tick has just ended and nothing has moved since (a ghost: what the
\* deal keeps holds of the state it leaves, before a start, finish or presence)
VARIABLES where, started, up, phase, skipped, settled
vars == <<where, started, up, phase, skipped, settled>>

Can(m, c) == CardTier[c] \in MemberTiers[m]
Held(m) == {c \in Cards : where[c] = m}
Working(m) == {c \in Held(m) : c \in started}
Ready(m) == {c \in Held(m) : c \notin started}
Free(m) == Width[m] - Cardinality(Held(m))
Overflow(m) == Cardinality(Held(m)) - Width[m]
Unstarted(c) == c \notin started /\ where[c] # "done"
\* a ready card the deal may place now: in the pool, or dealt into an overflow
Placeable(c) == Unstarted(c) /\ (where[c] = "pool" \/ (where[c] \in up /\ Overflow(where[c]) > 0))
FreeFor(c) == {m \in up : Can(m, c) /\ Free(m) > 0 /\ m # where[c]}
RoomFor(c) == {m \in up : Can(m, c) /\ Cardinality(Held(m)) < 2 * Width[m]}
Cheapest(S) == {m \in S : \A n \in S : Cost[m] <= Cost[n]}

\* any state at all: rows stacked past their slots and the cap as an older deal
\* left them (a friend row of 2026-10-10), started cards within the slots; a tick comes first
Init == /\ where \in [Cards -> {"pool"} \cup Members]
        /\ \A c \in Cards : where[c] \in Members => Can(where[c], c)
        /\ started \in SUBSET {c \in Cards : where[c] \in Members}
        /\ \A m \in Members : Cardinality(Working(m)) <= Width[m]
        /\ up \in (IF Churn THEN SUBSET Members ELSE {Members})
        /\ phase = "tick"
        /\ skipped = {}
        /\ settled = FALSE

\* --- the tick ---
OverCap(m) == Cardinality(Ready(m)) > 2 * Width[m]
Return(c) == /\ phase = "tick"
             /\ where[c] \in up /\ c \notin started
             /\ OverCap(where[c]) /\ Bad # "nocap"
             /\ where' = [where EXCEPT ![c] = "pool"]
             /\ UNCHANGED <<started, up, phase, skipped, settled>>

\* the rebalance's source: the overflow of any member (the code's rule), or only
\* of a member whose slots all work (the old rule, reversed witness "busyonly")
Movable(c) == /\ Placeable(c)
              /\ (Bad = "busyonly" /\ where[c] \in Members) =>
                    Cardinality(Working(where[c])) >= Width[where[c]]
              /\ Bad = "friendsfirst" => where[c] = "pool"

WorkNow(c, m) == /\ phase = "tick"
                 /\ Movable(c)
                 /\ m \in FreeFor(c)
                 /\ (m \in Cheapest(FreeFor(c)) \/ Bad = "costblind")
                 /\ where' = [where EXCEPT ![c] = m]
                 /\ skipped' = IF m \in Cheapest(FreeFor(c)) THEN skipped \ {c} ELSE skipped \cup {c}
                 /\ UNCHANGED <<started, up, phase, settled>>

Stack(c, m) == /\ phase = "tick"
               /\ where[c] = "pool" /\ Unstarted(c)
               /\ (FreeFor(c) = {} \/ Bad = "friendsfirst")
               /\ m \in RoomFor(c)
               /\ (m \in Cheapest(RoomFor(c)) \/ (Bad = "friendsfirst" /\ m \in Friends))
               /\ where' = [where EXCEPT ![c] = m]
               /\ UNCHANGED <<started, up, phase, skipped, settled>>

TickStep == \E c \in Cards : Return(c) \/ \E m \in Members : WorkNow(c, m) \/ Stack(c, m)
EndTick == /\ phase = "tick"
           /\ ~ENABLED TickStep
           /\ phase' = "env" /\ settled' = TRUE
           /\ UNCHANGED <<where, started, up, skipped>>

\* --- between ticks ---
Tick == /\ phase = "env" /\ phase' = "tick" /\ settled' = FALSE
        /\ UNCHANGED <<where, started, up, skipped>>
\* a member starts a card dealt to it in a free slot: only then is it working
Start(c) == /\ phase = "env"
            /\ where[c] \in up /\ c \notin started
            /\ Cardinality(Working(where[c])) < Width[where[c]]
            /\ started' = started \cup {c} /\ settled' = FALSE
            /\ UNCHANGED <<where, up, phase, skipped>>
Finish(c) == /\ phase = "env" /\ c \in started
             /\ started' = started \ {c}
             /\ where' = [where EXCEPT ![c] = "done"] /\ settled' = FALSE
             /\ UNCHANGED <<up, phase, skipped>>
Presence(m) == /\ Churn /\ phase = "env"
               /\ up' = (IF m \in up THEN up \ {m} ELSE up \cup {m})
               /\ settled' = FALSE
               /\ UNCHANGED <<where, started, phase, skipped>>

Next == \/ TickStep \/ EndTick \/ Tick
        \/ \E c \in Cards : Start(c) \/ Finish(c)
        \/ \E m \in Members : Presence(m)

Spec == Init /\ [][Next]_vars
Fair == /\ WF_vars(TickStep) /\ WF_vars(EndTick) /\ WF_vars(Tick)
        \* a start and a finish are enabled between ticks only: strong fairness
        /\ \A c \in Cards : SF_vars(Start(c)) /\ SF_vars(Finish(c))
LiveSpec == Spec /\ Fair

\* --- what the deal keeps, after every tick ---
TypeOK == /\ where \in [Cards -> Places]
          /\ started \subseteq Cards
          /\ \A c \in started : where[c] \in Members
          /\ up \subseteq Members
          /\ phase \in {"env", "tick"}
          /\ skipped \subseteq Cards
          /\ settled \in BOOLEAN
\* no card waits in the pool or in a member's overflow while a member up that may
\* run it has a free slot: every free slot is filled before any card is stacked
NoQueuedWhileFreeSlot == settled => \A c \in Cards : Placeable(c) => FreeFor(c) = {}
\* a member's ready (dealt, not started) is at most twice its width
ReadyCap == settled => \A m \in up : Cardinality(Ready(m)) <= 2 * Width[m]
\* no card is dealt into a slot while a cheaper member that may run it had one free
CheapestFirst == skipped = {}
\* a card is only ever on a member that may run its tier: a frontier card never
\* reaches the fleet
TierRespected == \A c \in Cards : where[c] \in Members => Can(where[c], c)
\* a member runs at most its width: only a start takes a slot
SlotsRespected == \A m \in Members : Cardinality(Working(m)) <= Width[m]
\* no started card moves, by any action
NoStartedCardMoves == [][\A c \in started : c \in started' => where'[c] = where[c]]_vars
\* a ready card with a free slot it may start in is eventually started
FreeSlotStarts == \A c \in Cards :
    (Unstarted(c) /\ phase = "env" /\ \E m \in up : Can(m, c) /\ Free(m) > 0) ~> (c \in started)
=============================================================================
