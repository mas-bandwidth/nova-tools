------------------------------ MODULE DirtyTick ------------------------------
\* The sprint machine's tick as the owner shaped it on 2026-09-30, written
\* before it is built (TLA+ for every state machine, model first).
\*
\* THE SHAPE (the owner's words, as the model reads them).
\*   "Each table gets one update in turn per-tick. 1. work streams, 2.
\*   readers, 3. merge, 4. fleet." The first pass: PumpWork, then Pass(t) for
\*   readers, merge and fleet in that order.
\*   "the dirty bit is just the number of entries in each table's queue."
\*   Each table has a queue. A step that changes a table appends an entry to
\*   that table's queue; the table's update drains its whole queue in one
\*   plan over every row that needs it (batch).
\*   "dirty bits are acted on IMMEDIATELY"; "the tick doesn't end until all
\*   dirty bits are cleared." Readers, merge and fleet drain whenever their
\*   queue is not empty (Drain(t), in any order, interleaved with the first
\*   pass); TickEnd only when those three queues are empty.
\*   "the work table is only pumped once per-tick"; "nothing advances the
\*   work stream table EXCEPT on the next tick"; "the previous tick does
\*   queue up all the changes for the work stream table, to process start of
\*   next tick". Only PumpWork writes the work table. Readers, merge and
\*   fleet write their own tables and append to the work queue (a finish, a
\*   read done, a landing, a card returned, room freed); the next tick's
\*   PumpWork applies the whole queue, then lands sentinels, releases,
\*   and deals from ready. "no new work moves from waiting -> ready ->
\*   working except on the FIRST PASS on the work stream table, once
\*   per-tick."
\*   The rules ruled the same day: a placement on a machine, a reader or a
\*   stream takes a uint64 counter modulo the count of the candidates
\*   (persisting across ticks; clear resets it, and clear is not modelled);
\*   a machine has a width; the coordinator is woken once, at the tick's end,
\*   with the count of what the tick addressed to it, and not at all if
\*   nothing.
\*
\* THE STATE.
\*   col, att, bnd       the work table: a card's column, its attempt, at its bound
\*   rd, askw            the readers table: the reader holding a card's read;
\*                       a card waiting for a reader (askwait)
\*   mq                  the merge table: cards queued to merge
\*   stat, mc, mr        the fleet table: a machine's status as the fleet
\*                       knows it, the cards dealt to it, the reads its
\*                       readers hold; its load is |mc| + |mr| (a read takes
\*                       room on the reader's host: "the readers dealing work
\*                       might dirty the fleet table")
\*   noUp                the judgment "no fleet member is up" (open or not)
\*   Q                   the four queues, one per table
\*   mctr, rctr, sctr    the placement counters of machines, readers and
\*                       streams (uint64, modelled mod CtrMod, which divides
\*                       2^64 and every count a small instance has)
\*   live, miss, acts, ext
\*                       outside: a machine beating; the beat windows it has
\*                       missed in a row; outside actions used; an outside
\*                       entry since the last tick began
\*   phase, pumps, sub   the tick: where it is, work pumps this tick, steps
\*                       this tick (capped at MaxSub + 1)
\*   addr, notes, wake   what this tick addressed to the coordinator; notes
\*                       written this tick; the count the last note carried
\*   act, plc            the last action's name; the placements it made
\*   brk, dealt, always  ghosts: broken reports applied per card; deals per
\*                       stream (capped at 3); a stream with a dealable card
\*                       left after every pump so far
\*
\* THE ACCEPT AND THE REDEALS (2026-09-30, the laws the event-driven rules
\* held and the first dirty tick lost; docs/SPEC-SPRINT.md sections 2 and 6).
\*   The pump accepts a card in review whose read said ok (R9), unless its
\*   CI is red at its head or the coordinator returned it at its attempt:
\*   those are the coordinator's, whose own accept still takes them
\*   (AcceptHolds). A card's redeals count only takes that ended without a
\*   finish: a machine lost while the card was dealt and not taken returns
\*   it with its count kept; a take that ended marks it (ended), and the deal
\*   that places it again counts that take; a card whose take ends with its
\*   count at MaxRedeals is not dealt again (RedealsAreEndedTakes,
\*   RedealBoundHolds). The coordinator's verbs (ci, return, accept) and
\*   take run only where the instance turns them on (Coord, Takes).
\*
\* THE ROUTE (2026-10-01, the owner: "the card should determine the model
\*   used"). A card is dealt only when a route serves its tier or it pins a
\*   model (Served, the scenario's: internal/sprint/route.go resolves it from
\*   the store's routes and the card's brief); a card no route serves stays
\*   ready, its tier's judgment the Go tick's (NNoRoute, one per tier, tested
\*   on the twin), and is never dealt (RouteGuard; the witness "noroute"
\*   deals it). Which route is drawn is not modelled: the draw only fills the
\*   work card's fields.
\*
\* THE READERS' PRESENCE (2026-10-01, the owner: a readers row with no reader
\*   process was asked every card's second read, and sat). A reader has a state
\*   as a machine has: up or away (stat, live: a reader is a name of the same
\*   two functions; the readers update applies it). The outside's
\*   ReaderAway(r) and ReaderBack(r) are a reader that stops asking for its
\*   queue or is held away, and one that asks again: an entry to the readers
\*   queue, as Lapse and Beat are to the fleet's. The readers update applies
\*   "raway": the reader is not up, the reads it holds are taken back and
\*   asked again in the same update, at the card's attempt (askw, as a lapse's
\*   unread), each with its readoff to the fleet; "raback": the reader is up.
\*   THE ASK GUARD: with "readerup" in Fixes a read is placed only on a reader
\*   up (AbleReaders); the witness without it places a read on a reader away
\*   (ReadsStandOnReadersUp), and the witness "keepaway" leaves the reads of a
\*   reader that went away where they are. The scenario turns the events on
\*   (Scn.away).
\*
\* THE MISSED BEATS (2026-10-01, the owner: one store timeout on a member's
\*   beat downed a working member, took its card back and lost the child's
\*   finish; internal/sprint/presence.go MissedBeatsDown). A machine that
\*   beats is down only after Misses beat windows in a row with no beat. Miss(m)
\*   is one such window (miss[m], derived from the last beat in the code,
\*   never stored); a beat between misses resets the count (BeatReset); Lapse(m),
\*   the machine going down, needs miss[m] >= Misses, and the action property
\*   LapseNeedsMisses says no machine stops beating before. The witness
\*   "onemiss" lets a machine lapse on the first miss (what the code did). A
\*   scenario that turns Scn.misses on models the windows; one that does not
\*   takes a lapse to come after them, as it always did.
\*
\* THE RETURN (2026-10-01, the owner's ask: a reader whose launch was refused
\*   held the read for the whole two-hour deadline, and took the next). The
\*   outside's ReadReturn(c) is the reader that holds c's read handing it back
\*   with no verdict (read --return): an entry to the readers queue. The
\*   readers update applies "handback" as "raway" applies a reader's going
\*   away, for the one read: off the reader, its readoff to the fleet, and
\*   asked again in the same update (askw), no broken report counted; a return
\*   of a read the reader no longer holds changes nothing. The witness
\*   "handkeeps" asks it again and keeps the hold (W24, NothingLost). The scenario
\*   turns the event on (Scn.hand).
\*
\* WHAT IS NOT MODELLED. Clear and epochs (the counters' reset); two reads
\* per attempt (one read each); rework but by a broken read; take is
\* modelled only for the redeals (a finish needs no take); verbs other than
\* add, take, finish, report, merge, ci, return, accept, beat and lapse (and a
\* reader's away and back, and a read returned);
\* outside actions during a tick (they run between ticks); byte and step
\* budgets; two sentinels in one stream (the pump lands at most one per
\* stream per tick); the log itself (a queue entry is its line).
EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS
  Streams, Cards, StreamOf, Pos, Sents, COrder,
  Machines, Width, MOrder, Readers, ROrder, Host, SOrder,
  MaxAttempts, MaxActs, MaxSub, CtrMod,
  Addable, Scn, Fixes, Broken,
  MaxRedeals, Takes, Coord

Tables == {"work", "readers", "merge", "fleet"}
\* The cards a route serves, or that pin a model: the scenario's (THE ROUTE).
Served == Scn.served
Three == {"readers", "merge", "fleet"}
Cols == {"none", "waiting", "ready", "working", "review", "merging", "landed"}
NoR == "none"
Min(a, b) == IF a < b THEN a ELSE b
\* The beat windows a machine misses in a row before it is down
\* (internal/sprint/presence.go MissedBeatsDown).
Misses == 3

VARIABLES col, att, bnd, rd, askw, mq, stat, mc, mr, noUp, Q,
          mctr, rctr, sctr, live, miss, acts, ext,
          phase, pumps, sub, addr, notes, wake, act, plc,
          brk, dealt, always,
          okd, ci, ret, tk, rdl, ended, ends, hred

\* okd, ci, ret   the work table: a card's read said ok and stands; its CI
\*                ("none" or "red" at its head); returned at its attempt
\* tk             the fleet table: the card dealt to a machine is taken
\* rdl, ended     the work card: its redeals; a take of it ended and the
\*                deal that places it again has not counted it
\* ends           ghost: takes of the card that ended, this attempt
\* hred           ghost: the last CI result reported for the card's current
\*                head is red (what ci must say; a result for an older head
\*                leaves both as they are)

vars == <<col, att, bnd, rd, askw, mq, stat, mc, mr, noUp, Q,
          mctr, rctr, sctr, live, miss, acts, ext,
          phase, pumps, sub, addr, notes, wake, act, plc,
          brk, dealt, always,
          okd, ci, ret, tk, rdl, ended, ends, hred>>
CardVars == <<okd, ci, ret, tk, rdl, ended, ends, hred>>

-----------------------------------------------------------------------------
\* Entries. k is the kind, c the card (or "-"), x the machine, the reader
\* and verdict, or "-".
E(k, c, x) == [k |-> k, c |-> c, x |-> x]
Count(seq, P(_)) == Cardinality({i \in 1..Len(seq) : P(seq[i])})
Pend(t, k, c) == Count(Q[t], LAMBDA e : e.k = k /\ e.c = c)

\* Order. The index of x in an order sequence; the rank of x in a set.
Idx(ord, x) == CHOOSE i \in 1..Len(ord) : ord[i] = x
Rank(ord, S, x) == Cardinality({y \in S : Idx(ord, y) < Idx(ord, x)})
\* THE PLACEMENT RULE: the candidate whose rank is the counter modulo the
\* count. The witness "name" takes the first by name instead.
Pick(ord, S, ctr) ==
  IF Broken = "name" THEN CHOOSE x \in S : Rank(ord, S, x) = 0
  ELSE CHOOSE x \in S : Rank(ord, S, x) = ctr % Cardinality(S)

Before(c, d) == StreamOf[c] = StreamOf[d] /\ Pos[d] < Pos[c]
Lowest(S) == CHOOSE x \in S : \A y \in S : Idx(COrder, x) <= Idx(COrder, y)
\* The i-th card of a set, in card order.
Nth(cs, i) == CHOOSE c \in cs : Cardinality({d \in cs : Idx(COrder, d) < Idx(COrder, c)}) = i - 1

-----------------------------------------------------------------------------
\* A plan works on a record of the tables, S, and commits it at its end.
Cur == [col |-> col, att |-> att, bnd |-> bnd, rd |-> rd, askw |-> askw,
        mq |-> mq, stat |-> stat, mc |-> mc, mr |-> mr, noUp |-> noUp,
        q |-> Q, mctr |-> mctr, rctr |-> rctr, sctr |-> sctr, addr |-> addr,
        brk |-> brk, dealt |-> dealt, plc |-> <<>>, notes |-> notes,
        f0 |-> Len(Q["fleet"]),
        okd |-> okd, ci |-> ci, ret |-> ret, tk |-> tk, rdl |-> rdl, ended |-> ended, ends |-> ends,
        hred |-> hred]

Put(S, t, e) == [S EXCEPT !.q[t] = Append(@, e)]

\* Room on m as a plan sees it: the fleet table's load, the placements this
\* plan made (entries past f0 in the fleet queue), and, with "pendingroom",
\* the placements already queued to the fleet and not yet applied.
PendRoom(seq, lo, hi, m) ==
  Cardinality({i \in lo..hi : seq[i].k \in {"dealt", "readon"} /\ seq[i].x = m})
Room(S, m) ==
  Width[m] - Cardinality(S.mc[m]) - Cardinality(S.mr[m])
  - PendRoom(S.q["fleet"], S.f0 + 1, Len(S.q["fleet"]), m)
  - (IF "pendingroom" \in Fixes THEN PendRoom(S.q["fleet"], 1, S.f0, m) ELSE 0)

\* A coordinator-addressed event: counted; the witness "percard" writes a
\* note for each at once instead of one at the tick's end.
Address(S) ==
  [S EXCEPT !.addr = Min(@ + 1, 3),
            !.notes = IF Broken = "percard" THEN Min(@ + 1, 2) ELSE @]

-----------------------------------------------------------------------------
\* THE ACCEPT (R9). A card whose read said ok moves to merging and into the
\* merge queue; the pump holds one whose CI is red at its head or that the
\* coordinator returned at its attempt (the witness "acceptheld" holds
\* neither).
AcceptOne(S, c) == Put([S EXCEPT !.col[c] = "merging", !.okd[c] = FALSE], "merge", E("queue", c, "-"))
Held(S, c) == Broken # "acceptheld" /\ (S.ci[c] = "red" \/ S.ret[c])
Acceptable(S) == {c \in Cards : S.col[c] = "review" /\ S.okd[c] /\ ~Held(S, c)}
AcceptAll(S) ==
  LET A == Acceptable(S) IN
  [S EXCEPT !.col = [c \in Cards |-> IF c \in A THEN "merging" ELSE @[c]],
            !.okd = [c \in Cards |-> IF c \in A THEN FALSE ELSE @[c]],
            !.q["merge"] = @ \o [i \in 1..Cardinality(A) |->
               E("queue", CHOOSE x \in A : Cardinality({d \in A : Idx(COrder, d) < Idx(COrder, x)}) = i - 1, "-")]]

\* THE REDEAL BOUND. A card whose take ended with its count at MaxRedeals is
\* not dealt again (the witness "redealpast" deals it).
AtRB(S, c) == Broken # "redealpast" /\ S.ended[c] /\ S.rdl[c] >= MaxRedeals

\* A new attempt: a new card, a new head, nothing returned at it.
NewAttempt(S, c) ==
  [S EXCEPT !.okd[c] = FALSE, !.ci[c] = "none", !.ret[c] = FALSE,
            !.rdl[c] = 0, !.ended[c] = FALSE, !.ends[c] = 0, !.hred[c] = FALSE]

-----------------------------------------------------------------------------
\* THE WORK PUMP (1.). Applies its whole queue, lands sentinels, releases,
\* deals, accepts. The only writer of col, att and bnd.
ApplyW(S, e) ==
  LET c == e.c IN
  CASE e.k = "add" ->
         IF S.col[c] = "none" THEN [S EXCEPT !.col[c] = "waiting"] ELSE S
    [] e.k = "finished" ->
         IF S.col[c] = "working"
         THEN Put([S EXCEPT !.col[c] = "review"], "readers", E("ask", c, "-"))
         ELSE S
    [] e.k = "readok" ->    \* the read stands; the pump's accept moves it
         IF S.col[c] = "review" THEN [S EXCEPT !.okd[c] = TRUE] ELSE S
    [] e.k = "broken" ->
         IF S.col[c] # "review" THEN S
         ELSE IF Broken = "twice"
         THEN NewAttempt([S EXCEPT !.col[c] = "ready", !.att[c] = Min(@ + 2, MaxAttempts)], c)
         ELSE IF S.att[c] < MaxAttempts
         THEN NewAttempt([S EXCEPT !.col[c] = "ready", !.att[c] = @ + 1], c)
         ELSE Address([S EXCEPT !.bnd[c] = TRUE])
    [] e.k = "landed" ->
         IF S.col[c] = "merging" THEN [S EXCEPT !.col[c] = "landed"] ELSE S
    [] e.k = "returned" ->  \* x: "taken" when a take of it ended
         IF S.col[c] # "working" THEN S
         ELSE LET S1 == [S EXCEPT !.col[c] = "ready", !.ended[c] = @ \/ e.x = "taken"]
              IN IF e.x = "taken" /\ S.rdl[c] >= MaxRedeals THEN Address(S1) ELSE S1
    [] e.k = "ci" -> [S EXCEPT !.ci[c] = e.x, !.hred[c] = (e.x = "red")]
    [] e.k = "ciold" ->     \* a result for an older head: labelled, ci kept
         IF Broken = "ciold" THEN [S EXCEPT !.ci[c] = e.x] ELSE S
    [] e.k = "back" ->      \* returned to review: its read stands
         IF S.col[c] = "merging" THEN [S EXCEPT !.col[c] = "review", !.ret[c] = TRUE, !.okd[c] = TRUE] ELSE S
    [] e.k = "accept" ->    \* the coordinator's accept, held or not
         IF S.col[c] = "review" /\ S.okd[c] THEN AcceptOne(S, c) ELSE S
    [] OTHER -> S    \* room: a reason to tick, nothing to apply

\* A sentinel lands when every card before it in its stream has landed; a
\* card is released when no unlanded sentinel is before it. Both are planned
\* on the state the queue left (the witness "onread" plans them on the state
\* as read, before the queue was applied).
Landable(C) == {g \in Sents : C[g] = "waiting" /\ \A d \in Cards : Before(g, d) => C[d] = "landed"}
LandSents(S, C) ==
  LET L == Landable(C) IN
  IF L = {} THEN S
  ELSE LET S1 == [S EXCEPT !.col = [c \in Cards |-> IF c \in L THEN "landed" ELSE @[c]]]
       IN IF Broken = "percard" /\ Cardinality(L) > 1
          THEN Address(Address(S1))
          ELSE IF Cardinality(L) > 1
          THEN [S1 EXCEPT !.addr = Min(@ + Cardinality(L), 3)]
          ELSE Address(S1)
Releasable(C) == {c \in Cards \ Sents : C[c] = "waiting" /\
                    ~\E g \in Sents : Before(c, g) /\ C[g] # "landed"}
Release(S, C) ==
  [S EXCEPT !.col = [c \in Cards |-> IF c \in Releasable(C) THEN "ready" ELSE @[c]]]

\* The deal: the streams take turns by the stream counter over the streams
\* with a dealable card; within a stream the lowest position; the machine by
\* the machine counter over the up machines with room.
Dealable(S, cand) == {c \in cand : S.col[c] = "ready" /\ ~AtRB(S, c) /\ (c \in Served \/ Broken = "noroute")}
UpRoom(S) == {m \in Machines : S.stat[m] = "up" /\
                (Broken = "nowidth" \/ Room(S, m) > 0)}
DealOne(S, cand) ==
  LET D == Dealable(S, cand)
      ss == {StreamOf[c] : c \in D}
      st == Pick(SOrder, ss, S.sctr)
      c == CHOOSE x \in D : StreamOf[x] = st /\
             \A y \in D : StreamOf[y] = st => Pos[x] <= Pos[y]
      ms == UpRoom(S)
      m == Pick(MOrder, ms, S.mctr)
  IN Put([S EXCEPT !.col[c] = "working",
                   !.rdl[c] = IF S.ended[c] THEN Min(@ + 1, MaxRedeals + 1) ELSE @,
                   !.ended[c] = FALSE,
                   !.sctr = (@ + 1) % CtrMod, !.mctr = (@ + 1) % CtrMod,
                   !.dealt[st] = Min(@ + 1, 3),
                   !.plc = @ \o <<[k |-> "s", ctr |-> S.sctr, el |-> ss, pick |-> st],
                                  [k |-> "m", ctr |-> S.mctr, el |-> ms, pick |-> m]>>],
         "fleet", E("dealt", c, m))
RECURSIVE Deal(_, _)
Deal(S, cand) ==
  IF Dealable(S, cand) = {} \/ UpRoom(S) = {} THEN S
  ELSE IF Broken = "perrow" THEN DealOne(S, cand)
  ELSE Deal(DealOne(S, cand), cand)

NoUpJudge(S, cand) ==
  IF Dealable(S, cand) # {} /\ ~\E m \in Machines : S.stat[m] = "up" /\ ~S.noUp
  THEN Address([S EXCEPT !.noUp = TRUE]) ELSE S

-----------------------------------------------------------------------------
\* THE READERS UPDATE (2.). Applies its queue, then places every card in
\* askwait on a reader, by the reader counter over the readers whose host
\* has room (and, with "seefleet", is up as the fleet table says).
ApplyR(S, e) ==
  LET c == e.c IN
  CASE e.k = "ask" ->
         IF S.rd[c] = NoR THEN [S EXCEPT !.askw[c] = TRUE] ELSE S
    [] e.k = "rep" ->
         IF S.rd[c] # e.x[1] THEN S   \* a report of a read since taken back
         ELSE LET S1 == Put([S EXCEPT !.rd[c] = NoR], "fleet", E("readoff", c, Host[e.x[1]]))
              IN IF e.x[2] = "ok" THEN Put(S1, "work", E("readok", c, "-"))
                 ELSE IF Broken = "r10"
                 THEN \* R10 as v2.1 writes it: the next attempt dealt at once
                      [S1 EXCEPT !.col[c] = "ready", !.att[c] = Min(@ + 1, MaxAttempts),
                                 !.brk[c] = Min(@ + 1, MaxAttempts)]
                 ELSE Put([S1 EXCEPT !.brk[c] = Min(@ + 1, MaxAttempts)], "work", E("broken", c, "-"))
    [] e.k = "handback" ->  \* the reader returns the read it holds: asked again
         IF S.rd[c] # e.x THEN S
         ELSE IF Broken = "handkeeps" THEN [S EXCEPT !.askw[c] = TRUE]   \* the witness keeps the hold
         ELSE Put([S EXCEPT !.rd[c] = NoR, !.askw[c] = TRUE], "fleet", E("readoff", c, Host[e.x]))
    [] e.k = "unread" ->
         IF S.rd[c] # NoR THEN [S EXCEPT !.rd[c] = NoR, !.askw[c] = TRUE] ELSE S
    [] e.k = "raway" ->     \* the reader is not up: its reads are taken back and asked again
         IF S.stat[e.x] = "down" THEN S
         ELSE IF Broken = "keepaway" THEN [S EXCEPT !.stat[e.x] = "down"]    \* the witness keeps them
         ELSE LET H == {d \in Cards : S.rd[d] = e.x}
              IN [S EXCEPT !.stat[e.x] = "down",
                           !.rd = [d \in Cards |-> IF d \in H THEN NoR ELSE @[d]],
                           !.askw = [d \in Cards |-> IF d \in H THEN TRUE ELSE @[d]],
                           !.q["fleet"] = @ \o [i \in 1..Cardinality(H) |-> E("readoff", Nth(H, i), Host[e.x])]]
    [] e.k = "raback" -> [S EXCEPT !.stat[e.x] = "up"]
    [] OTHER -> S    \* room, echo

AbleReaders(S) == {r \in Readers : ("seefleet" \in Fixes => S.stat[Host[r]] = "up") /\
                                    ("readerup" \in Fixes => S.stat[r] = "up") /\
                                    Room(S, Host[r]) > 0}
RECURSIVE PlaceReads(_)
PlaceReads(S) ==
  LET W == {c \in Cards : S.askw[c]} IN
  IF W = {} \/ AbleReaders(S) = {} THEN S
  ELSE LET c == Lowest(W)
           rs == AbleReaders(S)
           r == Pick(ROrder, rs, S.rctr)
       IN PlaceReads(Put([S EXCEPT !.askw[c] = FALSE, !.rd[c] = r,
                                   !.rctr = (@ + 1) % CtrMod,
                                   !.plc = Append(@, [k |-> "r", ctr |-> S.rctr, el |-> rs, pick |-> r])],
                         "fleet", E("readon", c, Host[r])))

-----------------------------------------------------------------------------
\* THE MERGE UPDATE (3.). A merge recorded lands the card: an entry to the
\* work queue, and one to the fleet ("the merge doing work ... dirty the
\* fleet table"). The witness "mergewrites" writes landed into the work table.
ApplyM(S, e) ==
  LET c == e.c IN
  CASE e.k = "queue" -> [S EXCEPT !.mq = @ \cup {c}]
    [] e.k = "return" ->    \* the coordinator's return: off the queue, back to review
         IF c \notin S.mq THEN S ELSE Put([S EXCEPT !.mq = @ \ {c}], "work", E("back", c, "-"))
    [] e.k = "merged" ->
         IF c \notin S.mq THEN S
         ELSE IF Broken = "mergewrites"
         THEN Put([S EXCEPT !.mq = @ \ {c}, !.col[c] = "landed"], "fleet", E("landed", c, "-"))
         ELSE Put(Put([S EXCEPT !.mq = @ \ {c}], "work", E("landed", c, "-")),
                  "fleet", E("landed", c, "-"))
    [] OTHER -> S

-----------------------------------------------------------------------------
\* THE FLEET UPDATE (4.). Beats and lapses set the status; a lapse returns
\* the machine's cards to the work queue and takes back its reads; a card
\* dealt to a machine now down is returned; a finish frees room and goes to
\* the work queue; room freed or a machine up is a "room" entry to the work
\* queue (a reason for the next pump) and, when a card waits for a reader,
\* to the readers queue. The witness "fleetdeals" deals ready cards to a
\* machine that came up.
RoomNews(S, m) ==
  LET S1 == Put(S, "work", E("room", "-", m)) IN
  IF \E c \in Cards : S1.askw[c] THEN Put(S1, "readers", E("room", "-", m)) ELSE S1
\* x is "taken" when the card was taken (the witness "countall" marks every
\* card so).
TakenMark(S, c) == IF S.tk[c] \/ Broken = "countall" THEN "taken" ELSE "-"
ReturnAll(S, cs) ==
  [S EXCEPT !.q["work"] = @ \o [i \in 1..Cardinality(cs) |->
     E("returned", Nth(cs, i), TakenMark(S, Nth(cs, i)))],
            !.tk = [c \in Cards |-> IF c \in cs THEN FALSE ELSE @[c]],
            !.ends = [c \in Cards |-> IF c \in cs /\ S.tk[c] THEN Min(S.ends[c] + 1, MaxRedeals + 1) ELSE @[c]]]
UnreadAll(S, cs) ==
  [S EXCEPT !.q["readers"] = @ \o [i \in 1..Cardinality(cs) |->
     E("unread", CHOOSE c \in cs : Cardinality({d \in cs : Idx(COrder, d) < Idx(COrder, c)}) = i - 1, "-")]]
FleetDeals(S, m) ==
  IF Broken # "fleetdeals" THEN S
  ELSE LET D == {c \in Cards : S.col[c] = "ready"} IN
       IF D = {} THEN S
       ELSE LET c == Lowest(D) IN [S EXCEPT !.col[c] = "working", !.mc[m] = @ \cup {c}]

ApplyF(S, e) ==
  LET c == e.c
      m == e.x
  IN
  CASE e.k = "beat" ->
         IF S.stat[m] = "up" THEN S
         ELSE FleetDeals(RoomNews([S EXCEPT !.stat[m] = "up", !.noUp = FALSE], m), m)
    [] e.k = "lapse" ->
         IF S.stat[m] = "down" THEN S
         ELSE LET S1 == UnreadAll(ReturnAll([S EXCEPT !.stat[m] = "down", !.mc[m] = {}, !.mr[m] = {}],
                                            S.mc[m]), S.mr[m])
              IN IF S.mc[m] \cup S.mr[m] # {} THEN Address(S1) ELSE S1
    [] e.k = "dealt" ->
         IF S.stat[m] = "up" THEN [S EXCEPT !.mc[m] = @ \cup {c}]
         ELSE Put(S, "work", E("returned", c, "-"))
    [] e.k = "take" ->
         IF c \in S.mc[m] THEN [S EXCEPT !.tk[c] = TRUE] ELSE S
    [] e.k = "fin" ->
         IF c \notin S.mc[m] THEN S    \* a finish of a card since returned
         ELSE IF Broken = "drop" THEN RoomNews([S EXCEPT !.mc[m] = @ \ {c}, !.tk[c] = FALSE], m)
         ELSE RoomNews(Put([S EXCEPT !.mc[m] = @ \ {c}, !.tk[c] = FALSE], "work", E("finished", c, "-")), m)
    [] e.k = "readon" ->
         IF S.stat[m] = "up" THEN [S EXCEPT !.mr[m] = @ \cup {c}]
         ELSE Put(S, "readers", E("unread", c, "-"))
    [] e.k = "readoff" ->
         IF c \in S.mr[m] THEN RoomNews([S EXCEPT !.mr[m] = @ \ {c}], m) ELSE S
    [] OTHER -> S    \* landed, echo

-----------------------------------------------------------------------------
\* Every update applies its queue in order, entry by entry, in one plan.
RECURSIVE Fold(_, _, _)
Apply(t, S, e) ==
  CASE t = "work" -> ApplyW(S, e)
    [] t = "readers" -> ApplyR(S, e)
    [] t = "merge" -> ApplyM(S, e)
    [] t = "fleet" -> ApplyF(S, e)
Fold(t, S, seq) == IF seq = <<>> THEN S ELSE Fold(t, Apply(t, S, Head(seq)), Tail(seq))

\* The pump's plan: the queue applied, then sentinels, release, the deal.
Pump(S0) ==
  LET S1 == Fold("work", [S0 EXCEPT !.q["work"] = <<>>], Q["work"])
      C == IF Broken = "onread" THEN S0.col ELSE S1.col
      S2 == LandSents(S1, C)
      C2 == IF Broken = "onread" THEN S0.col ELSE S2.col
      S3 == Release(S2, C2)
      cand == IF Broken = "onread" THEN {c \in Cards : S0.col[c] = "ready"} ELSE Cards
      S4 == Deal(S3, cand)
  IN AcceptAll(NoUpJudge(S4, cand))

-----------------------------------------------------------------------------
\* An update of one of the three: drain the whole queue in one plan.
Update(t) ==
  LET S0 == [Cur EXCEPT !.q[t] = <<>>, !.f0 = IF t = "fleet" THEN 0 ELSE @]
      S1 == Fold(t, S0, Q[t])
      S2 == IF t = "readers" THEN PlaceReads(S1) ELSE S1
      \* the witness "echo": readers and fleet each tell the other they ran
      S3 == IF Broken = "echo" /\ t = "readers" THEN Put(S2, "fleet", E("echo", "-", "-"))
            ELSE IF Broken = "echo" /\ t = "fleet" THEN Put(S2, "readers", E("echo", "-", "-"))
            ELSE S2
  IN S3

Commit(S) ==
  /\ okd' = S.okd /\ ci' = S.ci /\ ret' = S.ret /\ tk' = S.tk /\ rdl' = S.rdl
  /\ ended' = S.ended /\ ends' = S.ends /\ hred' = S.hred
  /\ col' = S.col /\ att' = S.att /\ bnd' = S.bnd /\ rd' = S.rd /\ askw' = S.askw
  /\ mq' = S.mq /\ stat' = S.stat /\ mc' = S.mc /\ mr' = S.mr /\ noUp' = S.noUp
  /\ Q' = S.q /\ mctr' = S.mctr /\ rctr' = S.rctr /\ sctr' = S.sctr
  /\ addr' = S.addr /\ brk' = S.brk /\ dealt' = S.dealt /\ plc' = S.plc
  /\ notes' = S.notes

Step == sub' = Min(sub + 1, MaxSub + 1)

-----------------------------------------------------------------------------
\* THE TICK.
TickStart ==
  /\ phase = "idle"
  /\ \E t \in Tables : Q[t] # <<>>
  /\ Broken = "outsideonly" => ext
  /\ phase' = "work" /\ pumps' = 0 /\ sub' = 0 /\ addr' = 0 /\ notes' = 0
  /\ ext' = FALSE /\ act' = "TickStart" /\ plc' = <<>>
  /\ IF Broken = "reset" THEN mctr' = 0 /\ rctr' = 0 /\ sctr' = 0
     ELSE UNCHANGED <<mctr, rctr, sctr>>
  /\ UNCHANGED <<col, att, bnd, rd, askw, mq, stat, mc, mr, noUp, Q,
                 live, miss, acts, wake, brk, dealt, always, CardVars>>

\* The first pass's first update: the one pump of the tick.
PumpWork ==
  /\ phase = "work"
  /\ LET S == Pump(Cur) IN
       /\ Commit(S)
       /\ always' = [s \in Streams |-> always[s] /\ \E c \in Dealable(S, Cards) : StreamOf[c] = s]
  /\ phase' = "readers" /\ pumps' = pumps + 1 /\ act' = "PumpWork" /\ Step
  /\ UNCHANGED <<live, miss, acts, ext, wake>>

NextPhase(t) == CASE t = "readers" -> "merge" [] t = "merge" -> "fleet" [] t = "fleet" -> "drain"

\* The first pass's update of readers, merge, fleet, in turn.
Pass(t) ==
  /\ phase = t
  /\ Commit(Update(t))
  /\ phase' = NextPhase(t) /\ act' = "Pass" /\ Step
  /\ UNCHANGED <<live, miss, acts, ext, pumps, wake, always>>

\* A queue not empty is acted on at once, at any point after the pump.
Drain(t) ==
  /\ phase \in Three \cup {"drain"}
  /\ Q[t] # <<>>
  /\ Commit(Update(t))
  /\ act' = "Drain" /\ Step
  /\ UNCHANGED <<live, miss, acts, ext, phase, pumps, wake, always>>

\* The witness "twopumps": the work queue drained again inside the tick.
DrainWork ==
  /\ Broken = "twopumps"
  /\ phase \in Three \cup {"drain"}
  /\ Q["work"] # <<>>
  /\ LET S == Pump(Cur) IN Commit(S)
  /\ pumps' = Min(pumps + 1, 2) /\ act' = "DrainWork" /\ Step
  /\ UNCHANGED <<live, miss, acts, ext, phase, wake, always>>

TickEnd ==
  /\ phase = "drain"
  /\ Broken = "defer" \/ \A t \in Three : Q[t] = <<>>
  /\ IF addr > 0 \/ Broken = "always"
     THEN notes' = Min(notes + 1, 2) /\ wake' = addr
     ELSE UNCHANGED <<notes, wake>>
  /\ phase' = "idle" /\ act' = "TickEnd" /\ plc' = <<>>
  /\ UNCHANGED <<col, att, bnd, rd, askw, mq, stat, mc, mr, noUp, Q,
                 mctr, rctr, sctr, live, miss, acts, ext, pumps, sub, addr,
                 brk, dealt, always, CardVars>>

-----------------------------------------------------------------------------
\* THE OUTSIDE, between ticks: each appends to one queue and writes no table.
Outside(t, e) ==
  /\ phase = "idle"
  /\ Q' = [Q EXCEPT ![t] = Append(@, e)]
  /\ ext' = TRUE /\ act' = "Outside" /\ plc' = <<>>
  /\ UNCHANGED <<col, att, bnd, rd, askw, mq, stat, mc, mr, noUp,
                 mctr, rctr, sctr, phase, pumps, sub, addr, notes, wake,
                 brk, dealt, always, CardVars>>

Add(c) ==
  /\ c \in Addable /\ col[c] = "none" /\ Pend("work", "add", c) = 0
  /\ Outside("work", E("add", c, "-")) /\ UNCHANGED <<live, miss, acts>>
Finish(c, m) ==
  /\ c \in mc[m] /\ live[m] /\ Pend("fleet", "fin", c) = 0
  /\ Outside("fleet", E("fin", c, m)) /\ UNCHANGED <<live, miss, acts>>
Report(c, v) ==
  /\ rd[c] # NoR /\ live[Host[rd[c]]] /\ Pend("readers", "rep", c) = 0
  /\ Outside("readers", E("rep", c, <<rd[c], v>>)) /\ UNCHANGED <<live, miss, acts>>
Merge(c) ==
  /\ c \in mq /\ Pend("merge", "merged", c) = 0
  /\ Outside("merge", E("merged", c, "-")) /\ UNCHANGED <<live, miss, acts>>
\* A reader stops asking for its queue (or is held away), and asks again.
ReaderAway(r) ==
  /\ Scn.away /\ live[r] /\ acts < MaxActs
  /\ live' = [live EXCEPT ![r] = FALSE] /\ acts' = acts + 1 /\ UNCHANGED miss
  /\ Outside("readers", E("raway", "-", r))
\* The reader that holds a read returns it with no verdict (read --return).
ReadReturn(c) ==
  /\ Scn.hand /\ rd[c] # NoR /\ live[rd[c]] /\ acts < MaxActs /\ Pend("readers", "handback", c) = 0
  /\ acts' = acts + 1 /\ Outside("readers", E("handback", c, rd[c])) /\ UNCHANGED <<live, miss>>
ReaderBack(r) ==
  /\ Scn.away /\ ~live[r] /\ acts < MaxActs
  /\ live' = [live EXCEPT ![r] = TRUE] /\ acts' = acts + 1 /\ UNCHANGED miss
  /\ Outside("readers", E("raback", "-", r))
Beat(m) ==
  /\ ~live[m] /\ acts < MaxActs
  /\ live' = [live EXCEPT ![m] = TRUE] /\ acts' = acts + 1
  /\ miss' = [miss EXCEPT ![m] = 0]
  /\ Outside("fleet", E("beat", "-", m))
\* A machine that beats is down only after Misses windows with no beat (the
\* witness "onemiss" lets it lapse on any); a scenario without the windows
\* takes them to have passed.
Lapse(m) ==
  /\ live[m] /\ acts < MaxActs
  /\ Broken = "onemiss" \/ ~Scn.misses \/ miss[m] >= Misses
  /\ live' = [live EXCEPT ![m] = FALSE] /\ acts' = acts + 1 /\ UNCHANGED miss
  /\ Outside("fleet", E("lapse", "-", m))
\* One beat window passes with no beat from a machine that still beats; it
\* writes nothing and queues nothing (the member is seen late, not down).
Miss(m) ==
  /\ Scn.misses /\ phase = "idle" /\ live[m] /\ miss[m] < Misses /\ acts < MaxActs
  /\ miss' = [miss EXCEPT ![m] = miss[m] + 1]
  /\ act' = "Miss" /\ plc' = <<>>
  /\ UNCHANGED <<col, att, bnd, rd, askw, mq, stat, mc, mr, noUp, Q,
                 mctr, rctr, sctr, live, acts, ext, phase, pumps, sub, addr,
                 notes, wake, brk, dealt, always, CardVars>>
\* A beat between misses resets the count; the machine was up and stays up, so
\* nothing is queued.
BeatReset(m) ==
  /\ Scn.misses /\ phase = "idle" /\ live[m] /\ miss[m] > 0 /\ acts < MaxActs
  /\ miss' = [miss EXCEPT ![m] = 0]
  /\ act' = "BeatReset" /\ plc' = <<>>
  /\ UNCHANGED <<col, att, bnd, rd, askw, mq, stat, mc, mr, noUp, Q,
                 mctr, rctr, sctr, live, acts, ext, phase, pumps, sub, addr,
                 notes, wake, brk, dealt, always, CardVars>>
\* The worker takes a card dealt to its machine.
Take(c, m) ==
  /\ Takes /\ c \in mc[m] /\ live[m] /\ ~tk[c] /\ Pend("fleet", "take", c) = 0
  /\ Outside("fleet", E("take", c, m)) /\ UNCHANGED <<live, miss, acts>>
\* The coordinator's verbs: a CI result on a card in review (red at its head,
\* or green), a return of a card queued to merge, an accept of a card the
\* pump holds.
CIRed(c) ==
  /\ Coord /\ col[c] = "review" /\ ci[c] # "red" /\ Pend("work", "ci", c) = 0 /\ acts < MaxActs
  /\ acts' = acts + 1 /\ Outside("work", E("ci", c, "red")) /\ UNCHANGED <<live, miss>>
\* A late result, green, for a head the card has moved past.
CIOld(c) ==
  /\ Coord /\ col[c] = "review" /\ Pend("work", "ciold", c) = 0 /\ acts < MaxActs
  /\ acts' = acts + 1 /\ Outside("work", E("ciold", c, "none")) /\ UNCHANGED <<live, miss>>
CIGreen(c) ==
  /\ Coord /\ ci[c] = "red" /\ Pend("work", "ci", c) = 0
  /\ Outside("work", E("ci", c, "none")) /\ UNCHANGED <<live, miss, acts>>
Return(c) ==
  /\ Coord /\ c \in mq /\ Pend("merge", "return", c) = 0 /\ Pend("merge", "merged", c) = 0 /\ acts < MaxActs
  /\ acts' = acts + 1 /\ Outside("merge", E("return", c, "-")) /\ UNCHANGED <<live, miss>>
CoordAccept(c) ==
  /\ Coord /\ col[c] = "review" /\ okd[c] /\ (ci[c] = "red" \/ ret[c]) /\ Pend("work", "accept", c) = 0
  /\ Outside("work", E("accept", c, "-")) /\ UNCHANGED <<live, miss, acts>>

-----------------------------------------------------------------------------
Init ==
  /\ col = Scn.col /\ att = [c \in Cards |-> 1] /\ bnd = [c \in Cards |-> FALSE]
  /\ rd = Scn.rd /\ askw = [c \in Cards |-> FALSE] /\ mq = Scn.mq
  /\ stat = [m \in Machines \cup Readers |-> IF m \in Scn.up THEN "up" ELSE "down"]
  /\ mc = Scn.mc /\ mr = Scn.mr /\ noUp = FALSE /\ Q = Scn.q
  /\ mctr = 0 /\ rctr = 0 /\ sctr = 0
  /\ live = [m \in Machines \cup Readers |-> m \in Scn.live] /\ acts = 0 /\ ext = TRUE
  /\ miss = [m \in Machines |-> 0]
  /\ phase = "idle" /\ pumps = 0 /\ sub = 0 /\ addr = 0 /\ notes = 0 /\ wake = -1
  /\ act = "Init" /\ plc = <<>>
  /\ brk = [c \in Cards |-> 0] /\ dealt = [s \in Streams |-> 0]
  /\ always = [s \in Streams |-> TRUE]
  /\ okd = [c \in Cards |-> FALSE] /\ ci = [c \in Cards |-> "none"] /\ ret = [c \in Cards |-> FALSE]
  /\ tk = [c \in Cards |-> FALSE] /\ rdl = [c \in Cards |-> 0] /\ ended = [c \in Cards |-> FALSE]
  /\ ends = [c \in Cards |-> 0] /\ hred = [c \in Cards |-> FALSE]

TickNext == TickStart \/ PumpWork \/ DrainWork \/ TickEnd \/
            \E t \in Three : Pass(t) \/ Drain(t)
OutsideNext ==
  \/ \E c \in Cards : Add(c) \/ Merge(c) \/ \E v \in {"ok", "broken"} : Report(c, v)
  \/ \E c \in Cards, m \in Machines : Finish(c, m) \/ Take(c, m)
  \/ \E c \in Cards : CIRed(c) \/ CIGreen(c) \/ CIOld(c) \/ Return(c) \/ CoordAccept(c) \/ ReadReturn(c)
  \/ \E m \in Machines : Beat(m) \/ Lapse(m) \/ Miss(m) \/ BeatReset(m)
  \/ \E r \in Readers : ReaderAway(r) \/ ReaderBack(r)
Next == TickNext \/ OutsideNext
Spec == Init /\ [][Next]_vars /\ WF_vars(TickNext)

-----------------------------------------------------------------------------
\* PROPERTIES.
TypeOK ==
  /\ col \in [Cards -> Cols] /\ att \in [Cards -> 1..MaxAttempts] /\ bnd \in [Cards -> BOOLEAN]
  /\ rd \in [Cards -> Readers \cup {NoR}] /\ askw \in [Cards -> BOOLEAN] /\ mq \subseteq Cards
  /\ miss \in [Machines -> 0..Misses]
  /\ stat \in [Machines \cup Readers -> {"up", "down"}] /\ mc \in [Machines -> SUBSET Cards]
  /\ mr \in [Machines -> SUBSET Cards] /\ noUp \in BOOLEAN
  /\ phase \in {"idle", "work", "drain"} \cup Three
  /\ pumps \in 0..2 /\ sub \in 0..(MaxSub + 1) /\ addr \in 0..3 /\ notes \in 0..2
  /\ okd \in [Cards -> BOOLEAN] /\ ci \in [Cards -> {"none", "red"}] /\ ret \in [Cards -> BOOLEAN]
  /\ tk \in [Cards -> BOOLEAN] /\ rdl \in [Cards -> 0..(MaxRedeals + 1)]
  /\ ended \in [Cards -> BOOLEAN] /\ ends \in [Cards -> 0..(MaxRedeals + 1)] /\ hred \in [Cards -> BOOLEAN]

\* THE CENTRAL PROPERTY (the owner: "nothing advances the work stream table
\* EXCEPT on the next tick"). Only the tick's one pump writes the work table.
WorkChangesOnlyInPump == [][act' # "PumpWork" => UNCHANGED <<col, att, bnd, okd, ci, ret>>]_vars
\* No card leaves waiting or ready, and none enters working, but in the pump.
WorkAdvancesOnlyInThePump ==
  [][act' # "PumpWork" =>
       \A c \in Cards : /\ col[c] \in {"waiting", "ready"} => col'[c] = col[c]
                        /\ col'[c] = "working" => col[c] = "working"]_vars
\* One pump a tick, and every tick has it.
WorkPumpedOnce == pumps <= 1
WorkPumpedEveryTick == [][act' = "TickEnd" => pumps = 1]_vars
\* The pump leaves the work queue empty; nothing else takes from it.
QueueDrainedByPump == [][act' = "PumpWork" => Q'["work"] = <<>>]_vars
WorkQueueDrainedOnlyByPump ==
  [][act' # "PumpWork" => /\ Len(Q'["work"]) >= Len(Q["work"])
                          /\ SubSeq(Q'["work"], 1, Len(Q["work"])) = Q["work"]]_vars
\* The tick ends only with the three queues empty.
ThreeQueuesEmptyAtTickEnd == [][act' = "TickEnd" => \A t \in Three : Q'[t] = <<>>]_vars

\* NOTHING LOST: every entry is applied exactly once. Stated as conservation:
\* each card's token is in exactly one place, a table row or an entry on
\* its way, and the attempts equal the broken reports applied.
Holds(c) == Cardinality({m \in Machines : c \in mc[m]}) + Pend("fleet", "dealt", c)
            + Pend("work", "finished", c) + Pend("work", "returned", c)
ReadTok(c) == Pend("readers", "ask", c) + (IF askw[c] THEN 1 ELSE 0) + (IF rd[c] # NoR THEN 1 ELSE 0)
              + Pend("work", "readok", c) + Pend("work", "broken", c) + (IF okd[c] THEN 1 ELSE 0)
MergeTok(c) == Pend("merge", "queue", c) + (IF c \in mq THEN 1 ELSE 0) + Pend("work", "landed", c)
               + Pend("work", "back", c)
\* The host of the reader that holds the read holds it, or its readon is on the
\* way (or the read is on its way back); a host that held it before holds it
\* only until its readoff (a reader that went away: its read is on another).
ReadHome(c) ==
  /\ rd[c] # NoR => (IF c \in mr[Host[rd[c]]] THEN 1 ELSE 0) + Pend("fleet", "readon", c)
                    + Pend("readers", "unread", c) = 1
  /\ \A m \in Machines : c \in mr[m] =>
       (rd[c] # NoR /\ Host[rd[c]] = m) \/ Pend("fleet", "readoff", c) = 1
NothingLost ==
  \A c \in Cards :
    /\ Holds(c) = IF col[c] = "working" THEN 1 ELSE 0
    /\ ReadTok(c) = IF col[c] = "review" /\ ~bnd[c] THEN 1 ELSE 0
    /\ MergeTok(c) = IF col[c] = "merging" THEN 1 ELSE 0
    /\ ReadHome(c)
    /\ att[c] + Pend("work", "broken", c) + (IF bnd[c] THEN 1 ELSE 0) = 1 + brk[c]

\* EVERY ROW WITH WORK MOVES. After the pump nothing it could move is left
\* (the queue applied, sentinels landed, cards released, ready cards dealt
\* while a machine has room); at the tick's end no card waits for a reader
\* that could take it.
RoomNow(m) == Width[m] - Cardinality(mc[m]) - Cardinality(mr[m])
              - Count(Q["fleet"], LAMBDA e : e.k \in {"dealt", "readon"} /\ e.x = m)
PumpDone ==
  /\ Q["work"] = <<>>
  /\ Landable(col) = {} /\ Releasable(col) = {}
  /\ ~(/\ \E c \in Served : col[c] = "ready" /\ ~(ended[c] /\ rdl[c] >= MaxRedeals)
       /\ \E m \in Machines : stat[m] = "up" /\ RoomNow(m) > 0)
  /\ ~\E c \in Cards : col[c] = "review" /\ okd[c] /\ ci[c] # "red" /\ ~ret[c]
ReadersDone ==
  ~(/\ \E c \in Cards : askw[c]
    /\ \E r \in Readers : stat[Host[r]] = "up" /\ RoomNow(Host[r]) > 0
                          /\ ("readerup" \in Fixes => stat[r] = "up"))

\* THE ASK GUARD: a read is held only by a reader up, at every state (a reader
\* away has its reads taken back by the update that applies it, and is placed
\* none; the witness without "readerup" places one).
ReadsStandOnReadersUp == \A c \in Cards : rd[c] # NoR => stat[rd[c]] = "up"
EveryRowWithWorkMoves ==
  [][/\ act' = "PumpWork" => PumpDone'
     /\ act' = "TickEnd" => ReadersDone]_vars

\* THE WAKE: once, at the tick's end, with the count, and never for nothing.
OneWakePerTick == notes <= 1
NoWakeIfNothing ==
  [][act' = "TickEnd" => /\ (notes' > notes) <=> (addr > 0)
                         /\ addr > 0 => wake' = addr]_vars

\* PLACEMENTS ROUND: each placement takes the candidate at the counter modulo
\* their count, in the candidates' order (stated with SelectSeq, not with the
\* plan's Rank); each placement moves its counter by one, and nothing else
\* moves a counter.
OrdOf(k) == CASE k = "m" -> MOrder [] k = "r" -> ROrder [] k = "s" -> SOrder
RoundPick(p) == LET s == SelectSeq(OrdOf(p.k), LAMBDA x : x \in p.el)
                IN s[(p.ctr % Len(s)) + 1]
CtrOf(k) == CASE k = "m" -> mctr [] k = "r" -> rctr [] k = "s" -> sctr
CtrOfP(k) == CASE k = "m" -> mctr' [] k = "r" -> rctr' [] k = "s" -> sctr'
PlacementsRound ==
  [][/\ \A i \in 1..Len(plc') : plc'[i].pick = RoundPick(plc'[i])
     /\ \A k \in {"m", "r", "s"} :
          LET ks == SelectSeq(plc', LAMBDA p : p.k = k) IN
          /\ \A j \in 1..Len(ks) : ks[j].ctr = (CtrOf(k) + j - 1) % CtrMod
          /\ CtrOfP(k) = (CtrOf(k) + Len(ks)) % CtrMod]_vars

\* THE ROUTE GUARD: a card no route serves, and that pins no model, is never
\* dealt (it stays ready for the coordinator).
RouteGuard == \A c \in Cards \ Served : col[c] \in {"none", "waiting", "ready"}

\* WIDTH: a machine never holds more cards and reads than its width.
WidthRespected == \A m \in Machines : Cardinality(mc[m]) + Cardinality(mr[m]) <= Width[m]

\* STREAM FAIRNESS: two streams that had a dealable card left after every
\* pump so far were dealt within one of each other.
StreamFairness ==
  [][\A s, t \in Streams : always[s] /\ always[t] => dealt'[s] - dealt'[t] \in -1..1]_vars

\* NO LAPSE BEFORE THE MISSES: a machine stops beating (and so is down, and
\* its cards are taken back) only after Misses beat windows in a row with no
\* beat. A scenario that does not turn the windows on takes them to have passed.
LapseNeedsMisses ==
  [][\A m \in Machines : (live[m] /\ ~live'[m] /\ Scn.misses) => miss[m] >= Misses]_vars

\* THE ACCEPT HOLDS: a card in review whose last CI result for its head is
\* red (hred: a late result for an older head changes nothing), or that the
\* coordinator returned at its attempt, moves to merging only by the
\* coordinator's accept.
AcceptHolds ==
  [][\A c \in Cards : col[c] = "review" /\ col'[c] = "merging" /\ (ci'[c] = "red" \/ hred'[c] \/ ret'[c])
                      => Pend("work", "accept", c) > 0]_vars

\* REDEALS ARE ENDED TAKES: a card's redeals, with the take that ended and
\* is not yet counted (ended, or on its way to the work queue), are the takes
\* of it that ended, this attempt; a card returned untaken keeps its count.
EndedOnTheWay(c) == Count(Q["work"], LAMBDA e : e.k = "returned" /\ e.c = c /\ e.x = "taken")
RedealsAreEndedTakes ==
  \A c \in Cards : rdl[c] + (IF ended[c] THEN 1 ELSE 0) + EndedOnTheWay(c) = ends[c]
RedealsBounded == \A c \in Cards : rdl[c] <= MaxRedeals
\* THE CI IS ITS HEAD'S: the record says red exactly when the last result
\* reported for the card's current head is red.
CIIsItsHeads == \A c \in Cards : (ci[c] = "red") = hred[c]
\* THE REDEAL BOUND HOLDS: a card whose take ended at the bound is never
\* dealt again.
RedealBoundHolds ==
  [][\A c \in Cards : col[c] = "ready" /\ ended[c] /\ rdl[c] >= MaxRedeals => col'[c] # "working"]_vars

\* TERMINATION: a tick's steps are bounded (safety form, MaxSub) and every
\* tick ends (liveness form).
TickBounded == sub <= MaxSub
Terminates == [](phase # "idle" => <>(phase = "idle"))
\* Work queued is pumped: the next tick comes.
WorkNotStranded == (Q["work"] # <<>>) ~> (Q["work"] = <<>>)
=============================================================================
