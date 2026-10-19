-------------------------- MODULE FriendLaneClient --------------------------
(***************************************************************************)
(* The friend lane is a client of the server with no local state (v1.3,    *)
(* pulled forward from v1.5 by the owner on 2026-10-14; the card           *)
(* the-friend-lane-is-a-client-with-no-local-state). Code: the sprint      *)
(* server's lane records (nova-sprint internal/sprint/lane_hold.go,        *)
(* steps_work.go takeOne) and the client inside the friend's supervised    *)
(* hook service (nova-tools internal/friend/client.go, cmd/nova-hook       *)
(* lanes.go). FriendLane.tla is the lane protocol itself; this module is   *)
(* who holds the state, and what a restart, a dead client and a dead       *)
(* model do to it.                                                         *)
(*                                                                         *)
(* The server owns every friend lane's record: the card it holds (srv),    *)
(* the card's attempt generation (gen), the lease (lease, renewed by an    *)
(* ask or a progress, counted down while nobody renews), and the finish.   *)
(* The client holds a cache (cache) it rebuilds from the server's answer   *)
(* and nothing else: a restart starts it empty. The model (the friend's    *)
(* session) works what one notice told it (told) and finishes through the *)
(* server's verb, which applies only when the server's record for that     *)
(* lane holds that card at that generation.                                *)
(*                                                                         *)
(* Liveness evidence is the hook record's alone: the client asks, and so   *)
(* renews, only while the model is proven (the per-minute challenge, only  *)
(* the model answers). A dead client renews nothing, and neither does a    *)
(* live client whose model stopped proving: either way the lease runs out  *)
(* and the server returns the card ready at its next generation.           *)
(*                                                                         *)
(* Time: a lease is Bound ticks; a renewing client renews at least every   *)
(* RenewTicks ticks (code: cmd/nova-hook LaneEvery plus one round's bound,  *)
(* against nova-sprint LaneGoneAfter). Age runs while nobody renews, and    *)
(* while a client renews only until RenewTicks ticks have passed since its *)
(* last renewal. The assumption RenewTicks < Bound is checked, not assumed: *)
(* LiveHoldNeverExpires fails without it (witness SlowRenew), and the Go    *)
(* test TestLaneRenewalIsWellInsideTheLease holds the code to it.           *)
(*                                                                         *)
(* Reversed witnesses (MCFriendLaneClientBroken*.cfg):                     *)
(*   TrustCache     the daemon before: lanes.json survives the restart and *)
(*                  decides a finish (the local file, not the server):     *)
(*                  FinishedOnce fails.                                    *)
(*   RenewUnproven  the client renews whether or not the model proves (a   *)
(*                  service that keeps the lease for a dead session):      *)
(*                  DeadModelReleases fails.                               *)
(*   SlowRenew      RenewTicks = Bound: a live, proving client renews no    *)
(*                  sooner than the lease runs (nova-tools#5798 at         *)
(*                  23aec479: renewal every 30m against a 10m lease):      *)
(*                  LiveHoldNeverExpires fails.                            *)
(*   TickUnstarts   the tick puts a card the friend took back ready while  *)
(*                  its lease is live (2026-10-14 20:30:21 EDT: a friend   *)
(*                  took a card; the tick's friendUnstartedWorking moved it*)
(*                  working -> ready the same second): TakenStaysTaken     *)
(*                  fails.                                                 *)
(*                                                                         *)
(* The width (the owner, 2026-10-16: "nova-config automatically owns lane *)
(* count; never unhook to resize"). The client applies the friend's        *)
(* canonical nova-config row about once a second (friend.Client.Apply):    *)
(* width is the width it last read, cfgOk whether the last read answered.  *)
(* SetWidth (the operator's nova-config friend set --width) and ConfigLost *)
(* / ConfigBack (a read that fails, then answers) are outside events. The  *)
(* client renews every lane the server says holds a card, whatever the     *)
(* width (Renew), and a free lane takes only while the row reads and her   *)
(* held cards are fewer than the width (Take). A lowered width drains; a   *)
(* raised one takes. Invariants NoLostCard, NoDuplicateHold,               *)
(* DrainNeverTakes; liveness GrowTakes. Witnesses:                         *)
(*   ShrinkStopsRenew  the client asks only lanes 1..width (the fixed-lane *)
(*                  client sized to the new width): a held lane above it   *)
(*                  goes unrenewed under a live client: NoLostCard fails.  *)
(*   ShrinkMoves    a lowered width moves a held card into a lane under it *)
(*                  without ending the old hold (a re-deal on resize):     *)
(*                  NoDuplicateHold fails.                                 *)
(*   TakeOverWidth  a free lane takes whatever the width or the read (the  *)
(*                  --lanes override): DrainNeverTakes fails.              *)
(*   NoGrow         the client takes to the width it started with (a      *)
(*                  width read once, at hook time): GrowTakes fails.       *)
(*                                                                         *)
(* The client's own view (nova-tools#5869 cold reads): Renew asks the     *)
(* lanes the client believes hold, never srv directly. While friend cards  *)
(* answers (cardsOk), that answer is the server's (the lanes srv says      *)
(* hold). When it fails (CardsReadFails, CardsReadBack) the round is       *)
(* blind: the client has only held, the lanes its own last round found     *)
(* holding (empty after a restart), and the lane take is the only lane ask *)
(* (no renew-only form: an ask of a lane that freed since is a take,       *)
(* BlindAsk). A blind round asks the lanes of held, and only while the row *)
(* reads and |held| is within the width (RenewBlind); else it asks nothing *)
(* and takes no free-lane turn (Take needs cardsOk). Witness:              *)
(*   BlindTakes     the round of 2a42a5d4a8: blind, every lane             *)
(*                  1..max(width, max held), ungated; a freed lane takes   *)
(*                  with the row unread or past a lowered width:           *)
(*                  DrainNeverTakes fails.                                 *)
(*   LostDrops      a free lane's take commits and its answer is lost      *)
(*                  (TakeAnswerLost); the view drops that lane, so a later *)
(*                  blind round counts too few and takes past a lowered    *)
(*                  width (before c2d44bcd5b): DrainNeverTakes fails.      *)
(* The bound, checked as a witness of the design, not excluded             *)
(* (MCFriendLaneClientBlindRestartLapses.cfg): a client that does not know *)
(* a held lane (restarted, held empty), or that drains past a lowered      *)
(* width or reads no row, renews nothing while friend cards keeps failing; *)
(* the lease returns the card: LiveHoldNeverExpires fails there.           *)
(* MCFriendLaneClientBlind.cfg (no restart) and                            *)
(* MCFriendLaneClientBlindWidth.cfg (resizes and a failed row read under a *)
(* failed cards read) hold everything else, and                          *)
(* MCFriendLaneClientBlindRestart.cfg holds every other invariant and      *)
(* property across a restart under a failed cards read.                    *)
(*                                                                         *)
(* The W2 batch abstraction (AnswerRound) attempts every held lane in one  *)
(* answered request; a reversed stop-at-first-failure leaves later lanes   *)
(* unasked (AllHeldRenewedEachAnsweredRound fails). A lane-0 tuple in told  *)
(* records a server-sent notice; NoticedOnce rejects also telling that same *)
(* card through the client's notice. MCFriendLaneClientBrokenDoubleNotice *)
(* reverses that suppression. The two witness selectors are combinations  *)
(* unique to those configs, so the existing config results stay unchanged. *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS Cards, Lanes, Bound, RenewTicks, MaxRestarts, MaxDeaths,
          TrustCache, RenewUnproven, TickUnstarts,
          Widths, InitWidth, MaxResizes, MaxLosses,
          ShrinkStopsRenew, ShrinkMoves, TakeOverWidth, NoGrow,
          MaxCardFails, BlindTakes, LostDrops

None == "none"
NoHold == <<None, 0>>  \* a cache entry naming no card
ASSUME None \notin Cards /\ Bound > 0 /\ RenewTicks \in 1..Bound
ASSUME TrustCache \in BOOLEAN /\ RenewUnproven \in BOOLEAN /\ TickUnstarts \in BOOLEAN
ASSUME Lanes = 1..Cardinality(Lanes) /\ Widths \subseteq Lanes /\ InitWidth \in Widths
ASSUME ShrinkStopsRenew \in BOOLEAN /\ ShrinkMoves \in BOOLEAN /\ TakeOverWidth \in BOOLEAN /\ NoGrow \in BOOLEAN
ASSUME MaxCardFails \in Nat /\ BlindTakes \in BOOLEAN /\ LostDrops \in BOOLEAN

(* W2 witness selectors use combinations absent from every existing config, so
   legacy TLC cases keep their prior state space without new required constants. *)
StopOnFirstFail == BlindTakes /\ LostDrops
BrokenDoubleNotice == BlindTakes /\ ShrinkStopsRenew
BatchRoundMode == StopOnFirstFail \/ BrokenDoubleNotice

VARIABLES srv,       \* server: the card each lane holds, or None
          col,       \* server: each card ready, held or done
          gen,       \* server: each card's generation (a returned card's next is one more)
          lease,     \* server: each lane's lease left, 0..Bound
          up,        \* the client process runs
          proven,    \* the model proves (the hook record reads proven)
          cache,     \* the client: each lane's copy of the server's answer, or NoHold
          told,      \* the model: the <<card, gen, lane>> one notice has told it
          finished,  \* ghost: finishes the server applied, per card
          returned,  \* ghost: a hold ended with its lease live, by neither finish nor expiry
          lostLive,  \* ghost: a lease ran out while its client was renewing it
          restarts,  \* client crashes so far
          deaths,    \* model deaths so far
          width,     \* the client: the width of the nova-config row it last read
          cfgOk,     \* the client: its last read of the row answered
          resizes,   \* operator width changes so far
          losses,    \* failed reads so far
          overTake,  \* ghost: a free lane took while the held cards were at the width or the row unread
          held,      \* the client: the lanes its own last answers found holding a card (c.held)
          cardsOk,   \* the client: its last friend cards read answered
          cardFails, \* failed friend cards reads so far
          seen,      \* ghost: the lanes this process asked since it started (a round's asks)
          roundMissed \* ghost: an answered batch round skipped a held lane

vars == <<srv, col, gen, lease, up, proven, cache, told, finished, returned, lostLive, restarts, deaths, width, cfgOk, resizes, losses, overTake, held, cardsOk, cardFails, seen, roundMissed>>
cfgVars == <<width, cfgOk, resizes, losses, overTake>>
viewVars == <<held, cardsOk, cardFails, seen, roundMissed>>

\* the lanes holding a card, as the server says
HeldN == Cardinality({l \in Lanes : srv[l] # None})

MaxGen == 2 + MaxRestarts + MaxDeaths
Gens == 1..MaxGen
Hold == (Cards \X Gens)

TypeOK ==
  /\ srv \in [Lanes -> Cards \cup {None}]
  /\ col \in [Cards -> {"ready", "held", "done"}]
  /\ gen \in [Cards -> Gens]
  /\ lease \in [Lanes -> 0..Bound]
  /\ up \in BOOLEAN /\ proven \in BOOLEAN
  /\ cache \in [Lanes -> Hold \cup {NoHold}]
  /\ told \subseteq (Cards \X Gens \X (Lanes \cup {0}))
  /\ roundMissed \in BOOLEAN
  /\ finished \in [Cards -> 0..2]
  /\ returned \in BOOLEAN /\ lostLive \in BOOLEAN
  /\ restarts \in 0..MaxRestarts /\ deaths \in 0..MaxDeaths
  /\ width \in Widths /\ cfgOk \in BOOLEAN
  /\ resizes \in 0..MaxResizes /\ losses \in 0..MaxLosses /\ overTake \in BOOLEAN
  /\ held \subseteq Lanes /\ cardsOk \in BOOLEAN /\ cardFails \in 0..MaxCardFails /\ seen \subseteq Lanes

Init ==
  /\ srv = [l \in Lanes |-> None]
  /\ col = [c \in Cards |-> "ready"]
  /\ gen = [c \in Cards |-> 1]
  /\ lease = [l \in Lanes |-> 0]
  /\ up = TRUE /\ proven = TRUE
  /\ cache = [l \in Lanes |-> NoHold]
  /\ told = {}
  /\ finished = [c \in Cards |-> 0]
  /\ returned = FALSE /\ lostLive = FALSE
  /\ restarts = 0 /\ deaths = 0
  /\ width = InitWidth /\ cfgOk = TRUE
  /\ resizes = 0 /\ losses = 0 /\ overTake = FALSE
  /\ held = {} /\ cardsOk = TRUE /\ cardFails = 0 /\ seen = {} /\ roundMissed = FALSE

\* the client renews (asks) only while the model proves; the witness renews regardless
Renewing == up /\ (proven \/ RenewUnproven)

\* The highest lane the client knows held, 0 when none.
MaxHeld == IF held = {} THEN 0 ELSE CHOOSE m \in held : \A k \in held : k <= m

\* RenewBlind: the lanes a client whose friend cards read failed asks, from its own view
\* alone: the lanes of held, while the row reads and they are within the width. The witness
\* BlindTakes asks every lane 1..max(width, MaxHeld) (width 0 while the row is unread),
\* ungated.
RenewBlind(l) ==
  IF BlindTakes
    THEN l <= (IF cfgOk /\ width > MaxHeld THEN width ELSE MaxHeld)
    ELSE l \in held /\ cfgOk /\ Cardinality(held) <= width

\* The lanes a renewing client asks as renewals, from its own view: while friend cards answers,
\* every lane that answer says holds a card (the server's own answer), whatever the width;
\* when it fails, RenewBlind. The witness ShrinkStopsRenew asks only 1..width.
ClientAsks(l) == IF cardsOk THEN srv[l] # None ELSE RenewBlind(l)
RenewsLane(l) == Renewing /\ ClientAsks(l) /\ (~ShrinkStopsRenew \/ l <= width)

\* The width a free lane takes to: the row's; the witness NoGrow keeps the width it started at.
TakeWidth == IF NoGrow THEN IF width < InitWidth THEN width ELSE InitWidth ELSE width

\* Renew(l): take --as friend.<name> --lane l on a lane the server says holds a card: the
\* server answers it, and the ask is the lane heard (its lease renewed). The client's cache
\* is whatever the server said, and the lane joins its own view of what is held.
Renew(l) ==
  /\ RenewsLane(l)
  /\ srv[l] # None
  /\ lease' = [lease EXCEPT ![l] = Bound]
  /\ cache' = [cache EXCEPT ![l] = <<srv[l], gen[srv[l]]>>]
  /\ held' = held \cup {l} /\ seen' = seen \cup {l}
  /\ UNCHANGED <<srv, col, gen, up, proven, told, finished, returned, lostLive, restarts, deaths>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED <<cardsOk, cardFails, roundMissed>>

\* Forget(l): an ask of a lane the client thought held answers it free (finished or expired
\* since): the lane leaves the client's view.
Forget(l) ==
  /\ up /\ l \in held /\ srv[l] = None
  /\ held' = held \ {l}
  /\ UNCHANGED <<srv, col, gen, lease, up, proven, cache, told, finished, returned, lostLive, restarts, deaths>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED <<cardsOk, cardFails, seen, roundMissed>>

\* BlindAsk(l): a blind round's ask of a lane it asks (RenewBlind) that the server says is
\* free: the lane take has no renew-only form, so the ask takes her next ready card, else
\* answers it free and the lane leaves the client's view.
BlindAsk(l) ==
  /\ Renewing /\ ~cardsOk /\ RenewBlind(l)
  /\ srv[l] = None
  /\ IF \E c \in Cards : col[c] = "ready"
       THEN \E c \in Cards :
              /\ col[c] = "ready"
              /\ srv' = [srv EXCEPT ![l] = c]
              /\ col' = [col EXCEPT ![c] = "held"]
              /\ lease' = [lease EXCEPT ![l] = Bound]
              /\ cache' = [cache EXCEPT ![l] = <<c, gen[c]>>]
              /\ overTake' = (overTake \/ ~cfgOk \/ HeldN >= width)
              /\ held' = held \cup {l}
       ELSE /\ cache' = [cache EXCEPT ![l] = NoHold]
            /\ held' = held \ {l}
            /\ UNCHANGED <<srv, col, lease, overTake>>
  /\ UNCHANGED <<gen, up, proven, told, finished, returned, lostLive, restarts, deaths>>
  /\ UNCHANGED <<width, cfgOk, resizes, losses>>
  /\ UNCHANGED <<cardsOk, cardFails, roundMissed>>
  /\ seen' = seen \cup {l}

\* CardsReadFails: friend cards does not answer (the client falls back to its own view);
\* CardsReadBack: it answers again. The read is a round's first step, so it fails between
\* rounds: before this process asked any lane (seen empty) or after a whole round, when it
\* has asked every lane the server holds. What held then names is the code's: c.held is a
\* round's holds, every lane asked kept in it, a failed ask's too (client.go Round), so held
\* covers the server's lanes; the witness LostDrops drops a take whose answer was lost
\* (TakeAnswerLost) and breaks DrainNeverTakes.
CardsReadFails ==
  /\ cardsOk /\ cardFails < MaxCardFails
  /\ seen = {} \/ \A l \in Lanes : srv[l] # None => l \in seen
  /\ cardsOk' = FALSE /\ cardFails' = cardFails + 1
  /\ UNCHANGED <<srv, col, gen, lease, up, proven, cache, told, finished, returned, lostLive, restarts, deaths, held, seen, roundMissed>>
  /\ UNCHANGED cfgVars
CardsReadBack ==
  /\ ~cardsOk
  /\ cardsOk' = TRUE
  /\ UNCHANGED <<srv, col, gen, lease, up, proven, cache, told, finished, returned, lostLive, restarts, deaths, held, cardFails, seen, roundMissed>>
  /\ UNCHANGED cfgVars

\* Take(l): the same verb on a free lane takes her next ready card, else nothing. The client
\* asks a free lane only while friend cards answers, the row reads and her held cards are
\* under the width (the witness TakeOverWidth asks regardless of the width and the read; a
\* blind round's asks are BlindAsk).
Take(l) ==
  /\ Renewing /\ cardsOk
  /\ srv[l] = None
  /\ TakeOverWidth \/ (cfgOk /\ HeldN < TakeWidth)
  /\ IF \E c \in Cards : col[c] = "ready"
       THEN \E c \in Cards :
              /\ col[c] = "ready"
              /\ srv' = [srv EXCEPT ![l] = c]
              /\ col' = [col EXCEPT ![c] = "held"]
              /\ lease' = [lease EXCEPT ![l] = Bound]
              /\ cache' = [cache EXCEPT ![l] = <<c, gen[c]>>]
              /\ overTake' = (overTake \/ ~cfgOk \/ HeldN >= width)
              /\ held' = held \cup {l}
       ELSE /\ cache' = [cache EXCEPT ![l] = NoHold]
            /\ held' = held \ {l}
            /\ UNCHANGED <<srv, col, lease, overTake>>
  /\ UNCHANGED <<gen, up, proven, told, finished, returned, lostLive, restarts, deaths>>
  /\ UNCHANGED <<width, cfgOk, resizes, losses>>
  /\ UNCHANGED <<cardsOk, cardFails, roundMissed>>
  /\ seen' = seen \cup {l}

\* TakeAnswerLost(l): Take's ask of a free lane commits on the server (her next ready card is
\* the lane's) and its answer is lost (a timeout, an unreadable answer, a brief cache write
\* that failed): the client's cache learns nothing, and the lane stays in its view (held),
\* as every failed ask's does. The witness LostDrops drops it from the view.
TakeAnswerLost(l) ==
  /\ Renewing /\ cardsOk
  /\ srv[l] = None
  /\ TakeOverWidth \/ (cfgOk /\ HeldN < TakeWidth)
  /\ \E c \in Cards :
       /\ col[c] = "ready"
       /\ srv' = [srv EXCEPT ![l] = c]
       /\ col' = [col EXCEPT ![c] = "held"]
       /\ lease' = [lease EXCEPT ![l] = Bound]
  /\ overTake' = (overTake \/ ~cfgOk \/ HeldN >= width)
  /\ held' = IF LostDrops THEN held ELSE held \cup {l}
  /\ seen' = seen \cup {l}
  /\ UNCHANGED <<cache, gen, up, proven, told, finished, returned, lostLive, restarts, deaths>>
  /\ UNCHANGED <<width, cfgOk, resizes, losses>>
  /\ UNCHANGED <<cardsOk, cardFails, roundMissed>>


\* AnswerRound is the bounded batch abstraction exercised by the two W2 witness configs.
\* An answered request attempts every lane the server says is held (or the client's last
\* held view when friend cards is blind) in one transition. StopOnFirstFail reverses that
\* rule: the first held lane fails and later held lanes are never asked. A server-announced
\* hold is recorded with lane 0 in told; the client notice remains the actual lane tuple.
AnswerRound ==
  /\ BatchRoundMode /\ Renewing
  /\ LET target == IF cardsOk THEN {l \in Lanes : srv[l] # None} ELSE held
         first == IF target = {} THEN 0 ELSE CHOOSE l \in target : \A k \in target : l <= k
         asked == IF StopOnFirstFail /\ target # {} THEN {first} ELSE target
         renewed == IF StopOnFirstFail /\ target # {} THEN {} ELSE target
         serverTold == IF BrokenDoubleNotice
                       THEN {<<srv[l], gen[srv[l]], 0>> : l \in target /\ srv[l] # None}
                       ELSE {}
     IN
       /\ lease' = [l \in Lanes |-> IF l \in renewed /\ srv[l] # None THEN Bound ELSE lease[l]]
       /\ cache' = [l \in Lanes |-> IF l \in renewed /\ srv[l] # None THEN <<srv[l], gen[srv[l]]>> ELSE cache[l]]
       /\ told' = told \cup serverTold
       /\ seen' = seen \cup asked
       /\ roundMissed' = roundMissed \/ ~(target \subseteq asked)
       /\ UNCHANGED <<srv, col, gen, up, proven, finished, returned, lostLive, restarts, deaths,
                      width, cfgOk, resizes, losses, overTake, held, cardsOk, cardFails>>

\* SetWidth(w): the operator sets her nova-config row's width; the client applies it on its
\* next read (outside event; the read's own timing is below the grain). No hold changes:
\* the witness ShrinkMoves re-deals a held card above the new width into a free lane under
\* it, the old hold kept.
SetWidth(w) ==
  /\ resizes < MaxResizes /\ w # width
  /\ width' = w /\ resizes' = resizes + 1
  /\ IF ShrinkMoves /\ w < width /\ \E h \in Lanes, k \in Lanes : h > w /\ k <= w /\ srv[h] # None /\ srv[k] = None
       THEN \E h \in Lanes, k \in Lanes :
              /\ h > w /\ k <= w /\ srv[h] # None /\ srv[k] = None
              /\ srv' = [srv EXCEPT ![k] = srv[h]]
              /\ lease' = [lease EXCEPT ![k] = Bound]
       ELSE UNCHANGED <<srv, lease>>
  /\ UNCHANGED <<col, gen, up, proven, cache, told, finished, returned, lostLive, restarts, deaths, cfgOk, losses, overTake>>
  /\ UNCHANGED viewVars

\* ConfigLost: a read of the row fails (intake closes); ConfigBack: it answers again.
ConfigLost ==
  /\ cfgOk /\ losses < MaxLosses
  /\ cfgOk' = FALSE /\ losses' = losses + 1
  /\ UNCHANGED <<srv, col, gen, lease, up, proven, cache, told, finished, returned, lostLive, restarts, deaths, width, resizes, overTake>>
  /\ UNCHANGED viewVars
ConfigBack ==
  /\ ~cfgOk
  /\ cfgOk' = TRUE
  /\ UNCHANGED <<srv, col, gen, lease, up, proven, cache, told, finished, returned, lostLive, restarts, deaths, width, resizes, losses, overTake>>
  /\ UNCHANGED viewVars

\* Notify: the one notice ("N cards taken; briefs at ...") tells the model every hold the
\* client's cache names. A restarted client tells again what the server says it holds.
ClientNoticeNeeded(l) ==
  /\ cache[l] # NoHold
  /\ <<cache[l][1], cache[l][2], l>> \notin told
  /\ (BrokenDoubleNotice \/ <<cache[l][1], cache[l][2], 0>> \notin told)

Notify ==
  /\ up /\ proven
  /\ \E l \in Lanes : ClientNoticeNeeded(l)
  /\ told' = told \cup {<<cache[l][1], cache[l][2], l>> : l \in {k \in Lanes : ClientNoticeNeeded(k)}}
  /\ UNCHANGED <<srv, col, gen, lease, up, proven, cache, finished, returned, lostLive, restarts, deaths>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED viewVars

\* the server's record for lane l holds card c at generation g
ServerSays(c, g, l) == srv[l] = c /\ gen[c] = g

\* Finish: the model finishes a card it was told, through the server's verb. The design
\* applies it only on the server's record; the witness TrustCache lets the client's local
\* lanes.json decide.
Finish(c, g, l) ==
  /\ proven
  /\ <<c, g, l>> \in told
  /\ IF TrustCache THEN cache[l] = <<c, g>> ELSE ServerSays(c, g, l)
  /\ col' = [col EXCEPT ![c] = "done"]
  /\ srv' = [k \in Lanes |-> IF srv[k] = c THEN None ELSE srv[k]]
  /\ lease' = [k \in Lanes |-> IF srv[k] = c THEN 0 ELSE lease[k]]
  /\ finished' = [finished EXCEPT ![c] = IF @ < 2 THEN @ + 1 ELSE 2]
  /\ cache' = [cache EXCEPT ![l] = NoHold]
  /\ UNCHANGED <<gen, up, proven, told, returned, lostLive, restarts, deaths>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED viewVars

\* Progress: the model says the run is alive; applied only on the server's record
Progress(c, g, l) ==
  /\ proven
  /\ <<c, g, l>> \in told
  /\ ServerSays(c, g, l)
  /\ lease' = [lease EXCEPT ![l] = Bound]
  /\ UNCHANGED <<srv, col, gen, up, proven, cache, told, finished, returned, lostLive, restarts, deaths>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED viewVars

\* Age(l): time passes. A lease nobody renews runs down to 0; a renewing client renews each
\* lane it asks within RenewTicks ticks, so its lease runs down no further than Bound -
\* RenewTicks. A lease that runs out while the client is up and proving is a lost live hold.
Age(l) ==
  /\ srv[l] # None /\ lease[l] > 0
  /\ (~RenewsLane(l) \/ lease[l] > Bound - RenewTicks)
  /\ lease' = [lease EXCEPT ![l] = @ - 1]
  /\ lostLive' = (lostLive \/ (Renewing /\ lease[l] = 1))
  /\ UNCHANGED <<srv, col, gen, up, proven, cache, told, finished, returned, restarts, deaths>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED viewVars

\* Expire(l): the tick returns a card whose lease ran out, ready at its next generation
Expire(l) ==
  /\ srv[l] # None /\ lease[l] = 0
  /\ gen[srv[l]] < MaxGen
  /\ col' = [col EXCEPT ![srv[l]] = "ready"]
  /\ gen' = [gen EXCEPT ![srv[l]] = @ + 1]
  /\ srv' = [srv EXCEPT ![l] = None]
  /\ UNCHANGED <<lease, up, proven, cache, told, finished, returned, lostLive, restarts, deaths>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED viewVars

\* TickUnstart(l), the witness only: the tick puts a taken card back ready while its lease
\* is live (friendUnstartedWorking before the fix: a take that was no "start")
TickUnstart(l) ==
  /\ TickUnstarts
  /\ srv[l] # None /\ lease[l] > 0
  /\ col' = [col EXCEPT ![srv[l]] = "ready"]
  /\ srv' = [srv EXCEPT ![l] = None]
  /\ lease' = [lease EXCEPT ![l] = 0]
  /\ returned' = TRUE
  /\ UNCHANGED <<gen, up, proven, cache, told, finished, lostLive, restarts, deaths>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED viewVars

\* Crash: the client process dies. Restart: it starts again in an empty working directory,
\* holding nothing; the witness TrustCache keeps its lanes.json.
Crash ==
  /\ up /\ restarts < MaxRestarts
  /\ up' = FALSE /\ restarts' = restarts + 1
  /\ UNCHANGED <<srv, col, gen, lease, proven, cache, told, finished, returned, lostLive, deaths>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED viewVars
Restart ==
  /\ ~up
  /\ up' = TRUE
  /\ cache' = IF TrustCache THEN cache ELSE [l \in Lanes |-> NoHold]
  /\ held' = {} /\ seen' = {}
  /\ UNCHANGED <<srv, col, gen, lease, proven, told, finished, returned, lostLive, restarts, deaths>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED <<cardsOk, cardFails, roundMissed>>

\* the model stops proving (its session gone), and comes back
Die ==
  /\ proven /\ deaths < MaxDeaths
  /\ proven' = FALSE /\ deaths' = deaths + 1
  /\ UNCHANGED <<srv, col, gen, lease, up, cache, told, finished, returned, lostLive, restarts>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED viewVars
Revive ==
  /\ ~proven
  /\ proven' = TRUE
  /\ UNCHANGED <<srv, col, gen, lease, up, cache, told, finished, returned, lostLive, restarts, deaths>>
  /\ UNCHANGED cfgVars
  /\ UNCHANGED viewVars

Next ==
  \/ \E l \in Lanes : Renew(l) \/ Take(l) \/ TakeAnswerLost(l) \/ Age(l) \/ Expire(l) \/ TickUnstart(l)
  \/ \E w \in Widths : SetWidth(w)
  \/ ConfigLost \/ ConfigBack
  \/ CardsReadFails \/ CardsReadBack \/ \E l \in Lanes : Forget(l) \/ BlindAsk(l)
  \/ AnswerRound
  \/ Notify
  \/ \E c \in Cards, g \in Gens, l \in Lanes : Finish(c, g, l) \/ Progress(c, g, l)
  \/ Crash \/ Restart \/ Die \/ Revive

Spec == Init /\ [][Next]_vars
        /\ \A l \in Lanes : WF_vars(Renew(l)) /\ WF_vars(Take(l)) /\ WF_vars(Age(l)) /\ WF_vars(Expire(l))
        /\ WF_vars(Notify) /\ WF_vars(Restart) /\ WF_vars(ConfigBack) /\ WF_vars(CardsReadBack)
        /\ WF_vars(AnswerRound)

-----------------------------------------------------------------------------
\* No card is held by two lanes: the server's record is the one owner.
OneLanePerCard ==
  \A l1, l2 \in Lanes : (l1 # l2 /\ srv[l1] # None) => srv[l1] # srv[l2]

\* A card is held exactly when a lane's server record holds it.
HeldIsTheServersRecord ==
  \A c \in Cards : (col[c] = "held") <=> (\E l \in Lanes : srv[l] = c)

\* A card is finished at most once, whatever restarts and returns came between: a restarted
\* client never duplicates a card.
FinishedOnce == \A c \in Cards : finished[c] <= 1

\* A hold ends only by a finish or by its lease running out.
TakenStaysTaken == ~returned

\* A live client that proves never lets a lease run out under it: its renewal interval is
\* inside the lease (the timing assumption RenewTicks < Bound, checked here). A lease that
\* ran out while the client was down may still be returned after it restarts: that is the
\* lease doing its work (DeadClientReleases), not a live hold lost.
LiveHoldNeverExpires == ~lostLive

\* A restarted client never loses a card: whatever the server holds for a lane, a renewing
\* client learns it again from the server alone and the model is told it, or the hold ends.
RestartResumes ==
  \A l \in Lanes, c \in Cards :
    (srv[l] = c /\ up /\ proven) ~> (srv[l] # c \/ <<c, gen[c], l>> \in told)

\* The lease bounds a dead client and a dead model: when nobody renews for good, every hold
\* is returned.
DeadClientReleases == (<>[](~up)) => <>[](\A l \in Lanes : srv[l] = None)
DeadModelReleases  == (<>[](~proven)) => <>[](\A l \in Lanes : srv[l] = None)

\* No card is lost to a resize or a failed read: a hold ends only by a finish or by its lease
\* running out under no renewing client (a shrink drains, never drops or returns).
NoLostCard == ~lostLive /\ ~returned

\* No card is worked twice: one lane holds it, and the model is told it on one lane per
\* generation (a resize never re-deals a held card).
NoDuplicateHold ==
  /\ OneLanePerCard
  /\ \A c \in Cards, g \in Gens : Cardinality({l \in Lanes : <<c, g, l>> \in told}) <= 1

\* No free lane takes while her held cards are at the width, or while the row is unread.
DrainNeverTakes == ~overTake

\* Every server-answered batch round attempts every lane it set out to renew.
AllHeldRenewedEachAnsweredRound == ~roundMissed

\* A hold is told either by the server (lane 0 marker) or the client (its real lane), never both.
NoticedOnce ==
  \A c \in Cards, g \in Gens, l \in Lanes :
    ~(<<c, g, 0>> \in told /\ <<c, g, l>> \in told)

\* A raised width takes: while the row reads, a renewing client holds fewer cards than the
\* width and a ready card waits, a lane takes it (or that stops being so).
Starved == Renewing /\ cfgOk /\ HeldN < width /\ \E c \in Cards : col[c] = "ready"
GrowTakes == Starved ~> ~Starved
=============================================================================
