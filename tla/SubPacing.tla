----------------------------- MODULE SubPacing -----------------------------
\* A subscription friend paced to her plan's reset (docs/SPEC-SPRINT.md section 1,
\* sub-pacingb.w1; internal/sprint/sub_pacing.go: subPace, FriendPace, subCovers,
\* subHeadroom; the owner, 2026-10-04: "PACING and offloading", and "always make
\* sure that subs are 100% utilized before spending on fleet").
\*
\* The state the code owns: owner, where each card is (a friend, the fleet's
\* paid width, or none); burn and allowance, each friend's tokens this window
\* and what the window may spend (the burn of the last full window); pwidth,
\* her paced width, the sprint's, never config; down, until when she is at her
\* limit (0: not limited); started, the cards their holder has begun. The clock
\* is now, one unit a tick, WinLen units a window. The outside: a card's run
\* ending with its tokens (Burn), the friend's daemon or a usage reader saying
\* she hit her limit (Limit), and a holder starting a card (Start). Paid is the
\* paid_width switch.
\*
\* The tick (Tick): the clock moves; a window that ends makes its burn the next
\* allowance; a limit whose reset has come ends (she comes back, her window
\* restarts); and each friend's paced width moves one toward her pace: down
\* while her burn is past the burn her allowance spreads evenly to the reset,
\* up while it is behind, never under 0 or over Width. A deal gives a card to a
\* friend only up (not at her limit) and under her paced width (Deal); the
\* fleet takes a card only with Paid and no subscription friend with headroom
\* (DealFleet); a friend over her paced width has an unstarted card moved to
\* one with headroom (Offload), and a limit takes every unstarted card back at
\* once (Limit), what she started staying with her.
\*
\* Broken = "none" is the design. Every other value is a reversed witness,
\* each caught by one property below:
\*   "pastpace"      the deal ignores pace: a friend is dealt up to her
\*                   configured width, and the fleet whenever Paid: NoDealPastPace
\*   "dealslimited"  the deal ignores down-until: a friend at her limit is
\*                   dealt: LimitedNeverDealt
\* LimitedComesBack (liveness): a friend at her limit comes back at her reset.

EXTENDS Naturals, FiniteSets

CONSTANTS Friends, Cards, Width, WinLen, MaxTime, MaxBurn, Paid, Broken

VARIABLES now, owner, burn, allowance, pwidth, down, started, pastPace
vars == <<now, owner, burn, allowance, pwidth, down, started, pastPace>>

Holders == Friends \cup {"fleet", "none"}

TypeOK ==
  /\ now \in 0..MaxTime
  /\ owner \in [Cards -> Holders]
  /\ burn \in [Friends -> 0..MaxBurn]
  /\ allowance \in [Friends -> 0..MaxBurn]
  /\ pwidth \in [Friends -> 0..Width]
  /\ down \in [Friends -> 0..MaxTime]
  /\ started \subseteq Cards
  /\ pastPace \in BOOLEAN

Load(f) == Cardinality({c \in Cards : owner[c] = f})
Up(f) == down[f] = 0
\* Her room: under her paced width (sub_pacing.go friendRoom); headroom is room while up (subHeadroom).
Room(f) == Load(f) < pwidth[f]
Headroom(f) == Up(f) /\ Room(f)
\* The witness deals to her configured width, ignoring the pace.
DealRoom(f) == IF Broken = "pastpace" THEN Load(f) < Width ELSE Room(f)
\* The paced burn at now: the allowance spread evenly over the window (PaceWindow.Paced).
Paced(f) == (allowance[f] * (now % WinLen)) \div WinLen
Ahead(f) == burn[f] > Paced(f)

Init ==
  /\ now = 0
  /\ owner = [c \in Cards |-> "none"]
  /\ burn = [f \in Friends |-> 0]
  /\ allowance = [f \in Friends |-> 0]
  /\ pwidth = [f \in Friends |-> Width]
  /\ down = [f \in Friends |-> 0]
  /\ started = {}
  /\ pastPace = FALSE

\* One tick: the window rolls at its end (FriendPace.roll), a reset that has
\* come brings her back and restarts her window, and the paced width moves one
\* toward the pace (subPace).
Tick ==
  /\ now < MaxTime
  /\ now' = now + 1
  /\ LET rolls == (now' % WinLen = 0)
         back(f) == down[f] # 0 /\ now' >= down[f]
     IN /\ allowance' = [f \in Friends |-> IF rolls \/ back(f) THEN burn[f] ELSE allowance[f]]
        /\ burn' = [f \in Friends |-> IF rolls \/ back(f) THEN 0 ELSE burn[f]]
        /\ down' = [f \in Friends |-> IF back(f) THEN 0 ELSE down[f]]
        /\ pwidth' = [f \in Friends |-> IF Ahead(f) THEN (IF pwidth[f] > 0 THEN pwidth[f] - 1 ELSE 0)
                                        ELSE (IF pwidth[f] < Width THEN pwidth[f] + 1 ELSE Width)]
  /\ UNCHANGED <<owner, started, pastPace>>

\* A card's run on a friend ends with its tokens: her burn (paceBurns.burn).
Burn(c, f) ==
  /\ owner[c] = f /\ c \in started /\ burn[f] < MaxBurn
  /\ burn' = [burn EXCEPT ![f] = @ + 1]
  /\ owner' = [owner EXCEPT ![c] = "none"]
  /\ started' = started \ {c}
  /\ UNCHANGED <<now, allowance, pwidth, down, pastPace>>

\* The friends' deal (friendDeal with the paced seats): a friend up with room
\* under her paced width. The witness lets a limited friend be dealt, and a
\* friend be dealt past her pace while another has headroom.
Deal(c, f) ==
  /\ owner[c] = "none"
  /\ (Broken = "dealslimited" \/ Up(f))
  /\ DealRoom(f)
  /\ owner' = [owner EXCEPT ![c] = f]
  /\ pastPace' = (pastPace \/ (~Room(f) /\ \E g \in Friends : Headroom(g)))
  /\ UNCHANGED <<now, burn, allowance, pwidth, down, started>>

\* The fleet's paid width (TickDeal, subCovers, subHeadroom): only with the
\* switch on and no subscription friend with headroom. The witness lets the
\* fleet take a card whenever the switch is on.
DealFleet(c) ==
  /\ owner[c] = "none" /\ Paid
  /\ (Broken = "pastpace" \/ ~\E f \in Friends : Headroom(f))
  /\ owner' = [owner EXCEPT ![c] = "fleet"]
  /\ pastPace' = (pastPace \/ \E f \in Friends : Headroom(f))
  /\ UNCHANGED <<now, burn, allowance, pwidth, down, started>>

\* The level (friendLevel with the paced seats): an unstarted card of a friend
\* over her paced width goes to a subscription friend with headroom.
Offload(c, f, g) ==
  /\ owner[c] = f /\ c \notin started /\ Load(f) > pwidth[f]
  /\ g # f /\ Headroom(g)
  /\ owner' = [owner EXCEPT ![c] = g]
  /\ UNCHANGED <<now, burn, allowance, pwidth, down, started, pastPace>>

\* A limit seen (her daemon's down with its until, or a usage reader's): she
\* is down until the reset, and her unstarted cards are taken back at once
\* (FriendTake, Limit); what she started stays (subPace).
Limit(f, until) ==
  /\ Up(f) /\ until > now /\ until <= MaxTime
  /\ down' = [down EXCEPT ![f] = until]
  /\ owner' = [c \in Cards |-> IF owner[c] = f /\ c \notin started THEN "none" ELSE owner[c]]
  /\ UNCHANGED <<now, burn, allowance, pwidth, started, pastPace>>

Start(c) ==
  /\ owner[c] \in Friends \cup {"fleet"} /\ c \notin started
  /\ started' = started \cup {c}
  /\ UNCHANGED <<now, owner, burn, allowance, pwidth, down, pastPace>>

Next ==
  \/ Tick
  \/ \E c \in Cards, f \in Friends : Burn(c, f) \/ Deal(c, f)
  \/ \E c \in Cards : DealFleet(c) \/ Start(c)
  \/ \E c \in Cards, f, g \in Friends : Offload(c, f, g)
  \/ \E f \in Friends, until \in 1..MaxTime : Limit(f, until)

Spec == Init /\ [][Next]_vars /\ WF_vars(Tick)

\* No friend is dealt past her pace while another subscription friend has
\* headroom for the card, and the fleet takes none while one has.
NoDealPastPace == ~pastPace

\* A friend at her limit, down until her reset, holds nothing she has not
\* started: she is never dealt, and what she had not started was taken back.
LimitedNeverDealt == \A f \in Friends : ~Up(f) => \A c \in Cards : owner[c] = f => c \in started

\* A friend at her limit comes back at her reset.
LimitedComesBack == \A f \in Friends : (~Up(f)) ~> Up(f)

=============================================================================
