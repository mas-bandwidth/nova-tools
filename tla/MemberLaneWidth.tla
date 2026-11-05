--------------------------- MODULE MemberLaneWidth ---------------------------
(***************************************************************************)
(* A nova-swarm member's lanes held to its width (pkg/member/member.go     *)
(* Tick, full, halves, take, recoverWorking, start; cmd/nova-swarm         *)
(* member.go widthRefusal; docs/SPEC-SWARM.md, the width). The rules came  *)
(* from FriendLane.tla's width part (nova-tools#5726, its Member case),    *)
(* which went with the friend daemon; this module holds the member alone.  *)
(*                                                                         *)
(* The server is the one source of the width: W is nova-config's machine   *)
(* row (a reader's, its machine's), changed there at any step              *)
(* (ChangeWidth), the member not told. The member holds only the copy its  *)
(* last queue answer gave it, cw (m.width: 0 until one answers; an answer  *)
(* naming none is 0, max(q.Width, 0)), read every pass (Read) with no      *)
(* --width flag (widthRefusal refuses one) and no floor at 1. A queue that *)
(* fails ends the pass before the take: the copy stays and nothing starts  *)
(* (a stutter here). Every start in the code follows that pass's read; the *)
(* model lets a start come at any step under the last copy, a superset.   *)
(*                                                                         *)
(* A launch holds a lane from its start until its card is reported (a      *)
(* child that exited still holds it: Running). The member has no lane      *)
(* numbers; lane[1..N] is the model's bookkeeping of its launches, N the   *)
(* most there can be. The gate is full(): a member counts half slots, a    *)
(* work card two and a read card one, against 2 * width (halves); a reader *)
(* (Reader) counts whole lanes, Running() >= width. With no reads that is  *)
(* the count alone: no start while Running() >= width. A lowered width     *)
(* kills no launch: nothing new starts until the held cost is under it.    *)
(*   LanesStartNeverPastWidth  no start whose cost would pass 2 * cw.      *)
(*   NothingStartsUnanswered   no start while cw = 0 (none yet, or 0).     *)
(*   NoLaneKilledOnLower       a lowered cw leaves every live lane live.   *)
(*   WidthIsFilled             a width that stays >= k with a ready card   *)
(*               has k lanes live, or nothing ready (liveness).            *)
(* Reversed witnesses (Broken, MCMemberLaneWidthBroken*.cfg):              *)
(*   slotgate    a lane held by its own number alone (l <= cw, the friend  *)
(*               daemon's ln.n > limit before #5726): after a lower lane 1 *)
(*               refills while lane 2 still runs: LanesStartNeverPastWidth.*)
(*   floorone    the width floored at 1 (row() before #5726): width 0 or   *)
(*               no answer opens a lane: NothingStartsUnanswered.          *)
(*   killonlower a lower cuts the lanes past it: NoLaneKilledOnLower.      *)
(*   nofollow    the width given at start and never read again (the        *)
(*               retired --width flag and Config.Width): a width raised    *)
(*               from 0 is never taken up: WidthIsFilled.                  *)
(* Below the module's grain: the take's ask count (room; the start's full  *)
(* is the gate), the server's own cut to the width (nova-sprint takeOne),  *)
(* the recovery of a working card with no child (gated by the same full),  *)
(* the stop, the room and load gates, and a child's exit before its report.*)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS Cards,   \* the cards dealt to this member
          Reads,   \* the read cards among them (a half slot each for a member)
          N,       \* the most launches the model keeps (lane slots 1..N)
          Widths,  \* the widths nova-config may give, 0 among them
          Reader,  \* a reader's loop: every launch a whole lane
          Broken   \* "none" | "slotgate" | "floorone" | "killonlower" | "nofollow"

None == "none"
Lanes == 1..N

VARIABLES
    W,          \* the server: the width nova-config gives (0: no row names one)
    cw,         \* the member: m.width, its last queue answer's (0 until one answers)
    col,        \* the server: card -> "ready" | "working" | "done"
    lane,       \* the member: slot -> the card its launch holds, or None
    pastWidth,  \* ghost: a start whose cost passed 2 * cw
    unanswered  \* ghost: a start while cw = 0

vars == <<W, cw, col, lane, pastWidth, unanswered>>

Init ==
    /\ W \in Widths
    \* m.width is 0 until a queue answers; nofollow: the width given at start
    /\ cw = IF Broken = "nofollow" THEN W ELSE 0
    /\ col = [c \in Cards |-> "ready"]
    /\ lane = [l \in Lanes |-> None]
    /\ pastWidth = FALSE /\ unanswered = FALSE

\* The launches held now (Running(): every launch from its start until its report).
Live == {l \in Lanes : lane[l] # None}
ReadyN == Cardinality({c \in Cards : col[c] = "ready"})

\* A launch's cost in half slots: a member's read card one, every other two (halves; a
\* reader's every launch a whole lane, Running).
Cost(c) == IF ~Reader /\ c \in Reads THEN 1 ELSE 2
Halves == 2 * Cardinality({l \in Live : Cost(lane[l]) = 2})
          + Cardinality({l \in Live : Cost(lane[l]) = 1})

\* The width the member acts on: its copy (floorone: floored at 1).
Eff == IF Broken = "floorone" /\ cw < 1 THEN 1 ELSE cw

\* full(p) is false: the card's cost fits the width (slotgate: the lane's number alone).
Fits(l, c) ==
    IF Broken = "slotgate" THEN l <= Eff
    ELSE Halves + Cost(c) <= 2 * Eff

-----------------------------------------------------------------------------
\* A start: a ready card taken and launched into a free slot (take, then start; the
\* launch holds its lane from m.running's entry on).
Start(l, c) ==
    /\ lane[l] = None
    /\ col[c] = "ready"
    /\ Fits(l, c)
    /\ col' = [col EXCEPT ![c] = "working"]
    /\ lane' = [lane EXCEPT ![l] = c]
    /\ pastWidth' = (pastWidth \/ Halves + Cost(c) > 2 * cw)
    /\ unanswered' = (unanswered \/ cw = 0)
    /\ UNCHANGED <<W, cw>>

\* The card is reported (its child ended, the report applied): the lane is free.
Finish(l) ==
    /\ lane[l] # None
    /\ col' = [col EXCEPT ![lane[l]] = "done"]
    /\ lane' = [lane EXCEPT ![l] = None]
    /\ UNCHANGED <<W, cw, pastWidth, unanswered>>

\* nova-config changes the width (machine set --width, an apply), the member not told.
ChangeWidth ==
    /\ \E w \in Widths \ {W} : W' = w
    /\ UNCHANGED <<cw, col, lane, pastWidth, unanswered>>

\* The pass's queue answer: m.width = max(q.Width, 0), raised and lowered alike. A lower
\* kills nothing (killonlower: the lanes numbered past it are cut, their cards ready again).
\* nofollow: never read after the start.
Read ==
    /\ Broken # "nofollow"
    /\ cw' = W
    /\ IF Broken = "killonlower" /\ W < cw
       THEN /\ lane' = [l \in Lanes |-> IF l > W THEN None ELSE lane[l]]
            /\ col' = [c \in Cards |->
                         IF \E l \in Lanes : l > W /\ lane[l] = c THEN "ready" ELSE col[c]]
       ELSE UNCHANGED <<lane, col>>
    /\ UNCHANGED <<W, pastWidth, unanswered>>

Next ==
    \/ \E l \in Lanes, c \in Cards : Start(l, c)
    \/ \E l \in Lanes : Finish(l)
    \/ ChangeWidth
    \/ Read

\* the member reads the width every pass, takes into the room it has, and reports what ended
Fairness ==
    /\ WF_vars(Read)
    /\ WF_vars(\E l \in Lanes, c \in Cards : Start(l, c))
    /\ \A l \in Lanes : WF_vars(Finish(l))

Spec == Init /\ [][Next]_vars /\ Fairness

-----------------------------------------------------------------------------
TypeOK ==
    /\ W \in Widths
    /\ cw \in Widths \cup {0}
    /\ col \in [Cards -> {"ready", "working", "done"}]
    /\ lane \in [Lanes -> Cards \cup {None}]
    /\ pastWidth \in BOOLEAN /\ unanswered \in BOOLEAN

\* A card is working exactly while one launch holds it.
OneLaunchPerCard ==
    /\ \A c \in Cards : col[c] = "working" <=> \E l \in Lanes : lane[l] = c
    /\ \A k, l \in Live : k # l => lane[k] # lane[l]

\* No start whose cost would pass the width the member holds (a lowered width: nothing new
\* until the held cost is under it). With no reads: no start while Running() >= cw.
LanesStartNeverPastWidth == ~pastWidth

\* No start while the width is unanswered or 0.
NothingStartsUnanswered == ~unanswered

\* A lowered width kills no lane: a lane live before the read is live, with its card, after.
NoLaneKilledOnLower ==
    [][cw' < cw => \A l \in Live : lane'[l] = lane[l]]_vars

\* A width the server holds at k or more is taken up to: with a card ready and fewer than
\* k lanes live, k lanes come to be live, or nothing is ready, or the width fell.
Ks == {k \in Widths : k > 0 /\ k <= N}
WidthIsFilled ==
    \A k \in Ks :
        (W >= k /\ ReadyN > 0 /\ Cardinality(Live) < k)
            ~> (Cardinality(Live) >= k \/ ReadyN = 0 \/ W < k)
=============================================================================
