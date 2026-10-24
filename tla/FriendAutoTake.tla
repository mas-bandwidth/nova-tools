--------------------------- MODULE FriendAutoTake ---------------------------
(***************************************************************************)
(* A friend's hook takes her ready cards on its own (cmd/nova-hook/lanes.go, *)
(* internal/friend/client.go; 2026-10-24: the friend daemons that took     *)
(* cards were retired on 10-17 and a friend hooked in the foreground took  *)
(* nothing; a friend sat on 9 ready cards with 3 working).               *)
(*                                                                         *)
(* Every Interval ticks (LaneEvery, 45 s) a round reads the server's own   *)
(* record of her working cards (friend cards) and asks for as many cards   *)
(* as her width leaves free; the server serves the ask atomically, held to *)
(* the row's width as it stands then, from her own row only, in deal       *)
(* order. The hook keeps no lane table: the free count is the server's.    *)
(* The hook takes and tells only; a card's work and finish are the         *)
(* model's (Finish is the environment). Rounds ask only while the model    *)
(* proves and her nova-config row reads (live).                            *)
(*                                                                         *)
(* Variables: the cards' column and row (state, dealt, holder: the ready   *)
(* and working sets), width, live, the take in flight, the clock and the   *)
(* next round, and since (when a free lane with a ready card began).       *)
(*                                                                         *)
(* Properties: TakesWithinWidth (no take leaves more than width working;   *)
(* a lowered width drains), NeverOthersCard, DealOrder, and                *)
(* TakenWithinInterval (a free lane with a ready card, while live, is      *)
(* taken within one interval of the round that answers). A refused ask    *)
(* counts the interval again from that round (the record stands, the next *)
(* round asks again).                                                      *)
(*                                                                         *)
(* Witness switches (each FALSE is a reversed witness, MCFriendAutoTake-   *)
(* Broken*.cfg): ServerHoldsWidth (the server serves the ask's count       *)
(* without its own width check), ServerChecksRow (it deals any ready card),*)
(* ClientReadsServer (the hook counts from a local table that never sees  *)
(* a finish), Periodic (one round at start, as a hook with no timer).      *)
(***************************************************************************)
EXTENDS Integers, FiniteSets

CONSTANTS Cards, Me, Other, Widths, Interval, MaxClock, AskFails,
          ServerHoldsWidth, ServerChecksRow, ClientReadsServer, Periodic

None == "none"
Rows == {Me, Other}

VARIABLES state, dealt, holder, width, live, inflight, clock, nextRound, since, local

vars == <<state, dealt, holder, width, live, inflight, clock, nextRound, since, local>>

Min(S) == CHOOSE x \in S : \A y \in S : x <= y
RECURSIVE Lowest(_, _)
Lowest(S, n) == IF n = 0 \/ S = {} THEN {} ELSE {Min(S)} \cup Lowest(S \ {Min(S)}, n - 1)
Pos(n) == IF n > 0 THEN n ELSE 0
Low(a, b) == IF a < b THEN a ELSE b

Working(r) == {c \in Cards : state[c] = "working" /\ holder[c] = r}
ReadyOf(r) == {c \in Cards : state[c] = "ready" /\ dealt[c] = r}
Free == Pos(width - Cardinality(Working(Me)))
FreeReady == live /\ Free > 0 /\ ReadyOf(Me) # {}

\* since follows FreeReady: set when it begins, kept while it holds, cleared when it ends
Since == since' = IF FreeReady' THEN (IF since = -1 THEN clock' ELSE since) ELSE -1

TypeOK ==
  /\ state \in [Cards -> {"pending", "ready", "working", "done"}]
  /\ dealt \in [Cards -> Rows \cup {None}]
  /\ holder \in [Cards -> Rows \cup {None}]
  /\ width \in Widths
  /\ live \in BOOLEAN
  /\ inflight \in -1..Cardinality(Cards) + 2
  /\ clock \in 0..MaxClock
  /\ since \in -1..MaxClock

Init ==
  /\ state = [c \in Cards |-> "pending"]
  /\ dealt = [c \in Cards |-> None]
  /\ holder = [c \in Cards |-> None]
  /\ width \in Widths
  /\ live \in BOOLEAN
  /\ inflight = -1
  /\ clock = 0
  /\ nextRound = 0
  /\ since = -1
  /\ local = 0

\* the sprint deals a card to a row's ready column
Deal(c, r) ==
  /\ state[c] = "pending"
  /\ state' = [state EXCEPT ![c] = "ready"]
  /\ dealt' = [dealt EXCEPT ![c] = r]
  /\ UNCHANGED <<holder, width, live, inflight, clock, nextRound, local>>
  /\ Since

\* another row takes a card dealt to it
OtherTakes(c) ==
  /\ state[c] = "ready" /\ dealt[c] = Other
  /\ state' = [state EXCEPT ![c] = "working"]
  /\ holder' = [holder EXCEPT ![c] = Other]
  /\ UNCHANGED <<dealt, width, live, inflight, clock, nextRound, local>>
  /\ Since

\* the model's child finishes a card (the hook never does)
Finish(c) ==
  /\ state[c] = "working"
  /\ state' = [state EXCEPT ![c] = "done"]
  /\ UNCHANGED <<dealt, holder, width, live, inflight, clock, nextRound, local>>
  /\ Since

\* nova-config sets her width
SetWidth(w) ==
  /\ width' = w
  /\ UNCHANGED <<state, dealt, holder, live, inflight, clock, nextRound, local>>
  /\ Since

\* the model proves or stops; her row reads or not
SetLive(b) ==
  /\ live' = b
  /\ UNCHANGED <<state, dealt, holder, width, inflight, clock, nextRound, local>>
  /\ Since

\* a round falls due: the hook reads the server's working cards and asks for the free count
Round ==
  /\ inflight = -1
  /\ clock >= nextRound
  /\ nextRound' = IF Periodic THEN clock + Interval ELSE MaxClock + 1
  /\ LET k == IF ClientReadsServer THEN Free ELSE Pos(width - local)
     IN inflight' = IF live /\ k > 0 THEN k ELSE -1
  /\ UNCHANGED <<state, dealt, holder, width, live, clock, local>>
  /\ Since

\* the server serves the ask in one step: held to the width as it stands, her row, deal order
Serve ==
  /\ inflight > 0
  /\ LET room == IF ServerHoldsWidth THEN Low(inflight, Free) ELSE inflight
         pool == IF ServerChecksRow THEN ReadyOf(Me) ELSE {c \in Cards : state[c] = "ready"}
         got  == Lowest(pool, room)
     IN /\ state' = [c \in Cards |-> IF c \in got THEN "working" ELSE state[c]]
        /\ holder' = [c \in Cards |-> IF c \in got THEN Me ELSE holder[c]]
        /\ local' = local + Cardinality(got)
  /\ inflight' = -1
  /\ UNCHANGED <<dealt, width, live, clock, nextRound>>
  \* the round answered: anything still free and ready became so at this instant
  /\ since' = IF FreeReady' THEN clock ELSE -1

\* the ask is refused or its answer lost: the server's record stands; asked again next round
AskFail ==
  /\ AskFails
  /\ inflight > 0
  /\ inflight' = -1
  /\ UNCHANGED <<state, dealt, holder, width, live, clock, nextRound, local>>
  /\ since' = IF FreeReady THEN clock ELSE -1

\* time passes only between rounds: an ask is answered inside its bound (laneAskWait < LaneEvery)
Tick ==
  /\ inflight = -1
  /\ clock < MaxClock
  /\ clock + 1 <= nextRound
  /\ clock' = clock + 1
  /\ UNCHANGED <<state, dealt, holder, width, live, inflight, nextRound, local>>
  /\ since' = since

Next ==
  \/ \E c \in Cards, r \in Rows : Deal(c, r)
  \/ \E c \in Cards : OtherTakes(c) \/ Finish(c)
  \/ \E w \in Widths : SetWidth(w)
  \/ \E b \in BOOLEAN : SetLive(b)
  \/ Round \/ Serve \/ AskFail \/ Tick

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------
\* never more than width working: no take leaves more of her cards working than the width
TakesWithinWidth ==
  [][(Working(Me)' \ Working(Me) # {}) => Cardinality(Working(Me)') <= width']_vars

\* never a card dealt to another row
NeverOthersCard == \A c \in Cards : holder[c] = Me => dealt[c] = Me

\* deal order: a take never passes over an earlier ready card of hers
DealOrder ==
  [][\A c \in Cards : (state[c] = "ready" /\ state'[c] = "working" /\ holder'[c] = Me)
       => \A d \in ReadyOf(Me) : d < c => state'[d] = "working"]_vars

\* a free lane with a ready card, while live, is taken within one interval
TakenWithinInterval == since = -1 \/ clock - since <= Interval
=============================================================================
