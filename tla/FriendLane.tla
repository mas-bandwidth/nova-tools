----------------------------- MODULE FriendLane -----------------------------
(***************************************************************************)
(* A friend's lanes as thin clients of the nova-sprint server (v1.2.6; the *)
(* owner, 2026-10-10: "always client/server, not distributed programming"). *)
(*                                                                         *)
(* The server (nova-sprint, Redis, keyed by epoch) is the one truth for    *)
(* what each lane holds. It keeps one record per (friend, lane, epoch):    *)
(* srv[l] is the card that record holds and the epoch it was granted at.   *)
(* A lane keeps nothing that decides what it holds: it asks.               *)
(*                                                                         *)
(* Every exchange is one request and its answer (an RPC): the lane sends,  *)
(* the server applies and answers, the lane does what the answer says. A   *)
(* lane that dies after the server applied and before it read the answer  *)
(* is Crash after the RPC. The verbs (nova-sprint `friend lane ...`):      *)
(*   Take(l)     take --lane l: names the friend and the lane, nothing    *)
(*               else; the server picks the epoch and the card (work or   *)
(*               read) and answers TAKEN card epoch, or the card the lane *)
(*               already holds (HOLDS), or NONE, or a refusal.            *)
(*   Held(l)     what do I hold: a lane after a crash, restart or clear.  *)
(*   Begin(l)    a read's begin (the run begins); a work card runs with   *)
(*               no begin (Run(l), taken to running with no request).     *)
(*   Progress(l) the run is alive.                                        *)
(*   Finish(l)   the run ended: done; TakenFinish(l), a card it holds and  *)
(*               cannot run (a report of a run before) finished failed.   *)
(*   Return(l)   the lane gives the card back (it drops it).              *)
(*   RowDrop(l)  a row read that disagrees drops the card locally, no     *)
(*               request; the lane asks again.                             *)
(* Begin, Progress, Finish and Return carry the card and the epoch the    *)
(* server gave; the server applies them only when its record for this    *)
(* (friend, lane) holds that card at that epoch and the epoch is current. *)
(* Any refusal: the lane drops the job and asks again, save one: an answer *)
(* the lane cannot read (below, the parked lane).                          *)
(*                                                                         *)
(* The parked lane (nova-tools#5724, v1.2.26; docs/SPEC-FRIEND.md, the    *)
(* LANE line). An answer naming a card in Garbled is a LANE line for the  *)
(* asking lane that it cannot read (a field it needs, gen, kind or job,   *)
(* missing or malformed: ParseLaneHold's Unparseable, a refusal). Asking  *)
(* again gets the same line from the same server, so the lane says the    *)
(* refusal once, runs nothing, and parks: it asks no more until its       *)
(* daemon restarts (laneAsk: askAt = now + neverAgain; only a new lane    *)
(* struct, a daemon start, clears it: a width change appends lanes and a  *)
(* clear's freshLanes keeps askAt). Restart unparks. The server's record  *)
(* still holds the card the parked lane refused: the lane is silent, so   *)
(* the gone bound bounces it (fair for a parked lane as for a dead one).  *)
(*   ParkedRunsNothing    a parked lane holds no card.                    *)
(*   RefusedOnceNotALoop  an unparseable line is refused once per daemon  *)
(*               life (refusals <= 1); a parked lane was refused once.    *)
(*   ParkedNeverAsks      a lane refused an unparseable line sends no     *)
(*               ask until its daemon restarts.                           *)
(*   ParkedRecordIsGone   the record a parked lane refused is dropped     *)
(*               (liveness: gone, a clear, or the lane restarted).        *)
(*   ResumesAfterRestart  a restarted lane asks again (liveness).         *)
(* Reversed witnesses (MCFriendLaneParkedBroken*.cfg):                    *)
(*   AskAgain    the code before #5724: the refusal set askAt = TakeRetry *)
(*               and the lane asked again every 5s, refused the same line *)
(*               every time (a reader-only friend's lanes, all night from *)
(*               2026-10-14 03:51Z; laneSay logged the line once, the     *)
(*               asks went on): RefusedOnceNotALoop, ParkedNeverAsks      *)
(*               fail, and, the asking lane never gone, WorkingResolves.  *)
(*   StickyPark  a park that outlives the restart (askAt persisted):      *)
(*               ResumesAfterRestart fails.                                *)
(*                                                                         *)
(* The server on its own: Gone(l), a lane that has not asked within the   *)
(* bound is gone, its card bounced back ready (the record dropped);       *)
(* Clear, tabula rasa: a new epoch, a new deal, every record dropped.     *)
(*                                                                         *)
(* Reversed witnesses (MCFriendLaneBroken*.cfg), each the code before:    *)
(*   RunOnNone   a take answered OK that moved nothing is logged "took"   *)
(*               and the lane runs a card still ready (2026-10-10 21:42Z, *)
(*               a friend host): WorkingOnlyOnTheServersSay fails.         *)
(*   LocalEpoch  the lane names the epoch its directory name says, 0 for  *)
(*               a job with none (progress at epoch 0): every progress is *)
(*               refused, the server's record still holds the card:      *)
(*               OneRequestApart fails.                                    *)
(*   KeepOnRefusal  a refused verb is logged and the lane runs on:        *)
(*               OneRequestApart fails.                                    *)
(*   CardOnly    the server applies a worker verb when the card is        *)
(*               working, whatever the epoch or lane (the server before): *)
(*               a stale lane's finish lands in the new epoch:            *)
(*               NoStaleStep fails.                                        *)
(*   NoGone      the server never bounces a silent lane's card back: a    *)
(*               lane that died stays holding it, WorkingResolves fails.  *)
(*   KeepOnMismatch  a lane keeps running its card after a row read       *)
(*               disagrees: WorkingOnlyOnTheServersSay fails.              *)
(*                                                                         *)
(* The width (nova-tools#5726, v1.2.26; docs/SPEC-SWARM.md, the width).    *)
(* The server is the one source of the width: W is nova-config's, read by  *)
(* the server and changed there at any step (ChangeWidth). The client     *)
(* holds only the copy its last answer gave it, cw (0 until one answers),  *)
(* and acts on nothing else; RowWidth is its read (the friend daemon's     *)
(* row(), the beat answer's row_width, every step; a member's Tick, the    *)
(* queue answer's Width, every pass). Lanes are numbered 1..N (the code's *)
(* ln.n). A lane starts a card (takes, or is told it holds one, and runs  *)
(* it in the same step: laneStep takes and starts the turn in one pass)   *)
(* only while its number is within cw and fewer than cw lanes are live    *)
(* (laneStep: ln.n > limit, then s.live() >= limit). Member is the        *)
(* member's lanes: no number, the count alone (member.go full, room:      *)
(* Running() >= m.width), the same invariants. A lower width kills no     *)
(* lane: running lanes finish their cards; a raised one is taken up to.   *)
(*   LanesStartNeverPastWidth  no start while |Live| >= cw.               *)
(*   NothingStartsUnanswered   no start while cw = 0 (no answer, or 0).   *)
(*   NoLaneKilledOnLower       a lowered cw leaves every live lane live.  *)
(*   WidthIsFilled             a width that stays >= k with a ready card  *)
(*               has k lanes live, or nothing ready (liveness).           *)
(* Reversed witnesses (MCFriendLaneWidthBroken*.cfg):                     *)
(*   SlotGate    the code before #5726: a lane is held by its own number  *)
(*               alone (ln.n > limit), so after a lower lane 1 refills    *)
(*               while lanes 2 and 3 still run: LanesStartNeverPastWidth. *)
(*   FloorOne    the code before #5726: row() floored the width at 1, so  *)
(*               width 0 or no answer opened a lane:                      *)
(*               NothingStartsUnanswered fails.                            *)
(*   KillOnLower a lower cuts the lanes past it: NoLaneKilledOnLower.     *)
(*   NoFollow    the width given at start and never read again (the       *)
(*               retired --width flag and Config.Width override): a       *)
(*               width raised from 0 is never taken up: WidthIsFilled.    *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

CONSTANTS Cards, Lanes, Reads, Steady, MaxClears, MaxCrashes, MaxGones, MaxReturns,
          RunOnNone, LocalEpoch, KeepOnRefusal, CardOnly, NoGone, KeepOnMismatch,
          Widths, Member, SlotGate, FloorOne, KillOnLower, NoFollow,
          Garbled, AskAgain, StickyPark

None == "none"
Nobody == [card |-> None, ep |-> 0]

VARIABLES
    epoch,     \* the server: the current epoch
    col,       \* the server: card -> "ready" | "working" | "done" (this epoch's deal)
    srv,       \* the server: lane -> [card, ep], its (friend, lane, epoch) record
    alive,     \* the lane process is up
    lc,        \* the lane: [st, card, ep]; st "idle" | "restart" | "taken" | "running" | "down"
    stale,     \* ghost: the server changed this lane's record (or the lane lost its own) since its last RPC
    granted,   \* ghost: lane -> the <<card, ep>> the server's answers gave it
    staleStep, \* ghost: a verb naming an epoch not current changed state
    clears, crashes, gones, returns,
    W,         \* the server: the width nova-config gives (the one definition of a width)
    cw,        \* the client: the width its last answer gave it (0: none yet, or 0)
    pastWidth, \* ghost: a lane started while |Live| >= cw
    unanswered, \* ghost: a lane started while cw = 0
    parked,    \* the lane: refused an unparseable LANE line, it asks no more until restarted
    refusals,  \* ghost: lane -> asks answered with a line it cannot read, since its daemon started (capped at 2)
    askedAfter \* ghost: a lane sent an ask after it was refused an unparseable line, same daemon life

wvars == <<W, cw, pastWidth, unanswered>>
pvars == <<parked, refusals, askedAfter>>
vars == <<epoch, col, srv, alive, lc, stale, granted, staleStep, clears, crashes, gones, returns, W, cw, pastWidth, unanswered,
          parked, refusals, askedAfter>>

Idle == [st |-> "idle", card |-> None, ep |-> 0]

Init ==
    /\ W \in Widths
    \* unanswered until the first read; NoFollow: the width given at start, never read again
    /\ cw = IF NoFollow THEN W ELSE 0
    /\ pastWidth = FALSE /\ unanswered = FALSE
    /\ epoch = 1
    /\ col = [c \in Cards |-> "ready"]
    /\ srv = [l \in Lanes |-> Nobody]
    /\ alive = [l \in Lanes |-> TRUE]
    /\ lc = [l \in Lanes |-> Idle]
    /\ stale = [l \in Lanes |-> FALSE]
    /\ granted = [l \in Lanes |-> {}]
    /\ staleStep = FALSE
    /\ clears = 0 /\ crashes = 0 /\ gones = 0 /\ returns = 0
    /\ parked = [l \in Lanes |-> FALSE]
    /\ refusals = [l \in Lanes |-> 0]
    /\ askedAfter = FALSE

Holding(l) == lc[l].st \in {"taken", "running"}
LaneSays(l) == IF Holding(l) THEN <<lc[l].card, lc[l].ep>> ELSE <<None, 0>>
ServerSays(l) == <<srv[l].card, srv[l].ep>>
HeldBySome == {srv[l].card : l \in Lanes} \ {None}

\* The lanes live now (the code's laneSet.live(), a member's Running()): a lane holding a
\* card, taken and running being one step apart in the code.
Live == {l \in Lanes : Holding(l)}
ReadyN == Cardinality({c \in Cards : col[c] = "ready"})

\* The width the client acts on: its last answer's alone (FloorOne: floored at 1, before).
Eff == IF FloorOne /\ cw < 1 THEN 1 ELSE cw

\* A lane may start a card: within the width by its number and by the live count
\* (laneStep); a member's lanes by the count alone (full, room); SlotGate by the number alone.
WidthGate(l) ==
    IF SlotGate THEN l <= Eff
    ELSE IF Member THEN Cardinality(Live) < Eff
    ELSE l <= Eff /\ Cardinality(Live) < Eff

\* The ghost of a start: what the client's own copy said when it started.
Started ==
    /\ pastWidth' = (pastWidth \/ Cardinality(Live) >= cw)
    /\ unanswered' = (unanswered \/ cw = 0)
    /\ UNCHANGED <<W, cw>>

\* The epoch the lane names: the server's, or (the code's Card.Epoch, lanes.go) the job
\* directory's epoch (0 for a job with none) when the server named none -- the LocalEpoch
\* witness's behaviour, still live wherever the server gave no epoch.
Named(l) == IF LocalEpoch THEN 0 ELSE lc[l].ep

\* The server's check of a worker verb from lane l naming card c at epoch e.
Owns(l, c, e) ==
    IF CardOnly THEN col[c] = "working"
    ELSE /\ e = epoch
         /\ srv[l].card = c
         /\ srv[l].ep = e

\* every RPC clears the lane's staleness: after it, lane and server agree
Exchanged(l) == stale' = [stale EXCEPT ![l] = FALSE]

-----------------------------------------------------------------------------
\* take --lane l: the lane names itself only; the server picks.
\* The width gate stands before the ask (laneStep: the live check, then laneAsk).
\* A parked lane does not ask (laneAsk: now.Before(ln.askAt), askAt = now + neverAgain).
TakeAsk(l) == alive[l] /\ lc[l].st = "idle" /\ ~parked[l] /\ WidthGate(l)
TakeRest == UNCHANGED <<epoch, alive, staleStep, clears, crashes, gones, returns>>

\* The ghost of an ask: a lane that was refused an unparseable line since its daemon started.
Asked(l) == askedAfter' = (askedAfter \/ refusals[l] >= 1)

\* The answer names card c on a LANE line the lane cannot read: ParseLaneHold's Unparseable.
Unreadable(c) == c \in Garbled

\* An Unparseable answer, a refusal (counted): the lane runs nothing and holds nothing. The fix
\* (#5724): it says so once and parks, asking no more until its daemon restarts. AskAgain, the
\* code before: it asks again in TakeRetry, and is answered the same line.
Refuse(l) ==
    /\ lc' = [lc EXCEPT ![l] = Idle]
    /\ refusals' = [refusals EXCEPT ![l] = IF @ < 2 THEN @ + 1 ELSE 2]
    /\ parked' = [parked EXCEPT ![l] = ~AskAgain]
    /\ UNCHANGED <<granted, wvars>>

\* HOLDS: the server's record holds a card: the lane is told it holds it.
TakeHolds(l) ==
    /\ TakeAsk(l)
    /\ srv[l].card # None
    /\ IF Unreadable(srv[l].card)
       THEN Refuse(l)
       ELSE /\ lc' = [lc EXCEPT ![l] = [st |-> "taken", card |-> srv[l].card, ep |-> srv[l].ep]]
            /\ granted' = [granted EXCEPT ![l] = @ \cup {ServerSays(l)}]
            /\ Started
            /\ UNCHANGED <<parked, refusals>>
    /\ UNCHANGED <<col, srv>>
    /\ Asked(l) /\ Exchanged(l) /\ TakeRest

\* TAKEN: the server picks a card ready on her row, at its epoch. Its record holds the card
\* whether or not the lane can read the line that says so.
TakeGrant(l, c) ==
    /\ TakeAsk(l)
    /\ srv[l].card = None
    /\ col[c] = "ready"
    /\ col' = [col EXCEPT ![c] = "working"]
    /\ srv' = [srv EXCEPT ![l] = [card |-> c, ep |-> epoch]]
    /\ IF Unreadable(c)
       THEN Refuse(l)
       ELSE /\ lc' = [lc EXCEPT ![l] = [st |-> "taken", card |-> c, ep |-> epoch]]
            /\ granted' = [granted EXCEPT ![l] = @ \cup {<<c, epoch>>}]
            /\ Started
            /\ UNCHANGED <<parked, refusals>>
    /\ Asked(l) /\ Exchanged(l) /\ TakeRest

\* NONE (nothing ready for her) or a refusal (STOPPED, her width full): nothing moves, and
\* the lane runs nothing (RunOnNone: it runs a card still ready, "took" on an OK that moved
\* nothing).
TakeNone(l) ==
    /\ TakeAsk(l)
    /\ srv[l].card = None
    /\ UNCHANGED <<col, srv, granted>>
    /\ IF RunOnNone /\ \E c \in Cards : col[c] = "ready"
       THEN /\ \E c \in Cards : col[c] = "ready" /\
                 lc' = [lc EXCEPT ![l] = [st |-> "taken", card |-> c, ep |-> epoch]]
            /\ Started
       ELSE UNCHANGED <<lc, wvars>>
    /\ UNCHANGED <<parked, refusals>>
    /\ Asked(l) /\ Exchanged(l) /\ TakeRest

Take(l) == TakeHolds(l) \/ (\E c \in Cards : TakeGrant(l, c)) \/ TakeNone(l)

\* what do I hold: the lane after a crash, a restart or a clear asks before anything else (the
\* code's first laneAsk after a start, never held back). A line it cannot read parks it again.
Held(l) ==
    /\ alive[l] /\ lc[l].st = "restart" /\ ~parked[l]
    /\ IF srv[l].card # None /\ srv[l].ep = epoch
       THEN IF Unreadable(srv[l].card)
            THEN Refuse(l)
            ELSE /\ lc' = [lc EXCEPT ![l] = [st |-> "taken", card |-> srv[l].card, ep |-> srv[l].ep]]
                 /\ granted' = [granted EXCEPT ![l] = @ \cup {ServerSays(l)}]
                 /\ UNCHANGED <<parked, refusals>>
       ELSE /\ lc' = [lc EXCEPT ![l] = Idle]
            /\ UNCHANGED <<granted, parked, refusals>>
    /\ Asked(l) /\ Exchanged(l)
    /\ UNCHANGED <<epoch, col, srv, alive, staleStep, clears, crashes, gones, returns, W, cw, pastWidth, unanswered>>

\* A refused verb: the lane drops the job (KeepOnRefusal: it runs on regardless).
Drop(l) == lc' = [lc EXCEPT ![l] = IF KeepOnRefusal THEN lc[l] ELSE Idle]

\* The step a verb takes on the server, with the ghost of a stale epoch.
Applied(l, e) == staleStep' = (staleStep \/ e # epoch)

\* A read's begin: the run begins with a request the server checks (read_lanes.go
\* ReadBeginArgv). A work card has no begin.
Begin(l) ==
    /\ alive[l] /\ lc[l].st = "taken" /\ lc[l].card \in Reads
    /\ LET c == lc[l].card IN
       IF Owns(l, c, Named(l))
       THEN /\ lc' = [lc EXCEPT ![l].st = "running"]
            /\ UNCHANGED staleStep
       ELSE /\ Drop(l) /\ UNCHANGED staleStep
    /\ Exchanged(l)
    /\ UNCHANGED <<epoch, col, srv, alive, granted, clears, crashes, gones, returns, W, cw, pastWidth, unanswered, parked, refusals, askedAfter>>

\* A work card's start: taken to running with no request (lane_take.go laneAsk runs exactly
\* what the take's answer named; no begin is sent).
Run(l) ==
    /\ alive[l] /\ lc[l].st = "taken" /\ lc[l].card \notin Reads
    /\ lc' = [lc EXCEPT ![l].st = "running"]
    /\ UNCHANGED <<epoch, col, srv, alive, stale, granted, staleStep, clears, crashes, gones, returns, W, cw, pastWidth, unanswered, parked, refusals, askedAfter>>

Progress(l) ==
    /\ alive[l] /\ lc[l].st = "running"
    /\ IF Owns(l, lc[l].card, Named(l))
       THEN UNCHANGED lc
       ELSE Drop(l)
    /\ Exchanged(l)
    /\ UNCHANGED <<epoch, col, srv, alive, granted, staleStep, clears, crashes, gones, returns, W, cw, pastWidth, unanswered, parked, refusals, askedAfter>>

Finish(l) ==
    /\ alive[l] /\ lc[l].st = "running"
    /\ LET c == lc[l].card IN
       IF Owns(l, c, Named(l))
       THEN /\ col' = [col EXCEPT ![c] = "done"]
            /\ srv' = [k \in Lanes |-> IF srv[k].card = c THEN Nobody ELSE srv[k]]
            /\ lc' = [lc EXCEPT ![l] = Idle]
            /\ Applied(l, lc[l].ep)
       ELSE /\ Drop(l) /\ UNCHANGED <<col, srv, staleStep>>
    /\ Exchanged(l)
    /\ UNCHANGED <<epoch, alive, granted, clears, crashes, gones, returns, W, cw, pastWidth, unanswered, parked, refusals, askedAfter>>

\* The lane finishes, failed, a card it was told it holds and cannot run: its outbox holds
\* the report of a run before that no step finished (lane_take.go finishStale).
TakenFinish(l) ==
    /\ alive[l] /\ lc[l].st = "taken"
    /\ LET c == lc[l].card IN
       IF Owns(l, c, Named(l))
       THEN /\ col' = [col EXCEPT ![c] = "done"]
            /\ srv' = [k \in Lanes |-> IF srv[k].card = c THEN Nobody ELSE srv[k]]
            /\ lc' = [lc EXCEPT ![l] = Idle]
            /\ Applied(l, lc[l].ep)
       ELSE /\ Drop(l) /\ UNCHANGED <<col, srv, staleStep>>
    /\ Exchanged(l)
    /\ UNCHANGED <<epoch, alive, granted, clears, crashes, gones, returns, W, cw, pastWidth, unanswered, parked, refusals, askedAfter>>

\* The lane gives its card back: it was told it holds a card it will not run (after a
\* restart, the job's tree gone), or its run was cut (the machine's stop, a cap).
Return(l) ==
    /\ alive[l] /\ Holding(l) /\ returns < MaxReturns
    /\ returns' = returns + 1
    /\ LET c == lc[l].card IN
       IF Owns(l, c, Named(l))
       THEN /\ col' = [col EXCEPT ![c] = "ready"]
            /\ srv' = [k \in Lanes |-> IF srv[k].card = c THEN Nobody ELSE srv[k]]
            /\ Applied(l, lc[l].ep)
       ELSE UNCHANGED <<col, srv, staleStep>>
    /\ lc' = [lc EXCEPT ![l] = Idle]
    /\ Exchanged(l)
    /\ UNCHANGED <<epoch, alive, granted, clears, crashes, gones, W, cw, pastWidth, unanswered, parked, refusals, askedAfter>>

\* A row read that disagrees with the lane (the card left her row, is not working there, or
\* another lane holds it: lane_take.go laneCheck) revokes the grant and the lane drops the
\* card locally and asks again: no request is sent, the lane's next ask re-syncs it. Broken
\* "keeponmismatch" lets the lane keep running its card after the grant is revoked.
RowDrop(l) ==
    /\ alive[l] /\ Holding(l)
    /\ lc[l].card # srv[l].card
    /\ granted' = [granted EXCEPT ![l] = @ \ {LaneSays(l)}]
    /\ IF KeepOnMismatch
       THEN UNCHANGED lc
       ELSE lc' = [lc EXCEPT ![l] = Idle]
    /\ UNCHANGED <<epoch, col, srv, alive, stale, staleStep, clears, crashes, gones, returns, W, cw, pastWidth, unanswered, parked, refusals, askedAfter>>

-----------------------------------------------------------------------------
\* The lane's process dies (a crash, the daemon stopped); what it held is the server's.
Crash(l) ==
    /\ l \notin Steady
    /\ alive[l] /\ crashes < MaxCrashes
    /\ alive' = [alive EXCEPT ![l] = FALSE]
    /\ lc' = [lc EXCEPT ![l] = [st |-> "down", card |-> None, ep |-> 0]]
    /\ stale' = [stale EXCEPT ![l] = TRUE]
    /\ crashes' = crashes + 1
    /\ UNCHANGED <<epoch, col, srv, granted, staleStep, clears, gones, returns, W, cw, pastWidth, unanswered, parked, refusals, askedAfter>>

\* It comes back with nothing: its first word is Held. A daemon start makes each lane anew
\* (lanes.go: &lane{n, session}, askAt zero): a parked lane is unparked, its count of refusals 0.
\* StickyPark: the park outlives the restart (an askAt carried across it).
Restart(l) ==
    /\ ~alive[l]
    /\ alive' = [alive EXCEPT ![l] = TRUE]
    /\ lc' = [lc EXCEPT ![l] = [st |-> "restart", card |-> None, ep |-> 0]]
    /\ IF StickyPark
       THEN UNCHANGED <<parked, refusals>>
       ELSE /\ parked' = [parked EXCEPT ![l] = FALSE]
            /\ refusals' = [refusals EXCEPT ![l] = 0]
    /\ UNCHANGED <<epoch, col, srv, stale, granted, staleStep, clears, crashes, gones, returns, W, cw, pastWidth, unanswered, askedAfter>>

\* The server: a lane that has not asked within the bound is gone; its card bounces back.
\* A dead lane never asks, nor does a parked one; a live one may be slow past the bound too
\* (bounded).
Gone(l) ==
    /\ ~NoGone
    /\ srv[l].card # None
    /\ \/ ~alive[l]
       \/ parked[l]
       \/ gones < MaxGones
    /\ col' = [col EXCEPT ![srv[l].card] = "ready"]
    /\ srv' = [srv EXCEPT ![l] = Nobody]
    /\ stale' = [stale EXCEPT ![l] = TRUE]
    /\ gones' = IF alive[l] /\ ~parked[l] THEN gones + 1 ELSE gones
    /\ UNCHANGED <<epoch, alive, lc, granted, staleStep, clears, crashes, returns, W, cw, pastWidth, unanswered, parked, refusals, askedAfter>>

\* clear: tabula rasa, a new epoch and a new deal; every record of the old epoch dropped.
Clear ==
    /\ clears < MaxClears
    /\ epoch' = epoch + 1
    /\ col' = [c \in Cards |-> "ready"]
    /\ srv' = [l \in Lanes |-> Nobody]
    /\ stale' = [l \in Lanes |-> TRUE]
    /\ clears' = clears + 1
    /\ UNCHANGED <<alive, lc, granted, staleStep, crashes, gones, returns, W, cw, pastWidth, unanswered, parked, refusals, askedAfter>>

-----------------------------------------------------------------------------
\* The width. The server's changes on its own (nova-config friend/machine set --width, an
\* apply): any width at any step, the client not told.
ChangeWidth ==
    /\ \E w \in Widths \ {W} : W' = w
    /\ UNCHANGED <<epoch, col, srv, alive, lc, stale, granted, staleStep, clears, crashes, gones, returns,
                   cw, pastWidth, unanswered, parked, refusals, askedAfter>>

\* The client's read (RowWidth): the beat answer's row_width every daemon step (row), the
\* queue answer's Width every member pass (Tick); no answer naming one is 0 (max(w, 0)).
\* A lower kills nothing: the lanes past it finish their cards (KillOnLower: they are cut).
\* NoFollow: never read after the start.
RowWidth ==
    /\ ~NoFollow
    /\ cw' = W
    /\ IF KillOnLower /\ W < cw
       THEN lc' = [l \in Lanes |-> IF Holding(l) /\ l > W THEN Idle ELSE lc[l]]
       ELSE UNCHANGED lc
    /\ UNCHANGED <<epoch, col, srv, alive, stale, granted, staleStep, clears, crashes, gones, returns,
                   W, pastWidth, unanswered, parked, refusals, askedAfter>>

Next ==
    \/ \E l \in Lanes :
          Take(l) \/ Held(l) \/ Begin(l) \/ Run(l) \/ Progress(l) \/ Finish(l) \/ TakenFinish(l) \/ Return(l)
          \/ RowDrop(l) \/ Crash(l) \/ Restart(l) \/ Gone(l)
    \/ Clear
    \/ ChangeWidth
    \/ RowWidth

Fairness ==
    /\ \A l \in Lanes : WF_vars(TakeHolds(l)) /\ WF_vars(\E c \in Cards : TakeGrant(l, c))
    /\ \A l \in Lanes : WF_vars(Held(l)) /\ WF_vars(Begin(l)) /\ WF_vars(Run(l)) /\ WF_vars(Finish(l))
    \* the bound passes for a lane that is down or parked (silent): its card bounces back
    /\ \A l \in Lanes : WF_vars((~alive[l] \/ parked[l]) /\ Gone(l))
    \* the client reads its width again on its next step
    /\ WF_vars(RowWidth)

Spec == Init /\ [][Next]_vars /\ Fairness

-----------------------------------------------------------------------------
TypeOK ==
    /\ epoch \in 1..(MaxClears + 1)
    /\ col \in [Cards -> {"ready", "working", "done"}]
    /\ srv \in [Lanes -> [card : Cards \cup {None}, ep : 0..(MaxClears + 1)]]
    /\ alive \in [Lanes -> BOOLEAN]
    /\ \A l \in Lanes : lc[l].st \in {"idle", "restart", "taken", "running", "down"}
    /\ W \in Widths
    /\ cw \in Widths \cup {0}
    /\ pastWidth \in BOOLEAN /\ unanswered \in BOOLEAN
    /\ parked \in [Lanes -> BOOLEAN]
    /\ refusals \in [Lanes -> 0..2]
    /\ askedAfter \in BOOLEAN

\* A card is working only on the server's say-so: a lane holds only what an answer gave
\* it, and every working card is held by one lane's record.
WorkingOnlyOnTheServersSay ==
    /\ \A l \in Lanes : Holding(l) => LaneSays(l) \in granted[l]
    /\ \A c \in Cards : col[c] = "working" <=> c \in HeldBySome
    /\ \A k, l \in Lanes : (k # l /\ srv[k].card # None) => srv[k].card # srv[l].card

\* The lane and the server never disagree on what a lane holds beyond one request: they
\* differ only when the server changed the record (gone, clear) or the lane lost its own
\* (crash) since its last exchange, and its next exchange makes them agree. A parked lane is
\* the one exception: it refused the card its record holds and sends no next exchange; the
\* record goes by the gone bound (ParkedRecordIsGone).
OneRequestApart ==
    \A l \in Lanes : (alive[l] /\ lc[l].st # "restart" /\ ~stale[l] /\ ~parked[l]) => LaneSays(l) = ServerSays(l)

\* No step naming a stale epoch changes state.
NoStaleStep == ~staleStep

\* Every card dealt to a live lane is taken or returned: nothing stays working forever.
WorkingResolves == \A c \in Cards : col[c] = "working" ~> col[c] # "working"

\* With a steady lane, every card of the last deal is eventually done.
AllDone == <>[](\A c \in Cards : col[c] = "done")

\* The width. No lane starts a card while as many lanes are live as the client's width
\* (a lowered width: nothing new until the live count is under it).
LanesStartNeverPastWidth == ~pastWidth

\* No lane starts a card while the width is unanswered or 0.
NothingStartsUnanswered == ~unanswered

\* A lowered width kills no lane: a lane live before the read is live after it.
NoLaneKilledOnLower ==
    [][cw' < cw => \A l \in Live : lc'[l].st \in {"taken", "running"}]_vars

\* A width the server holds at k or more is taken up to: with a card ready and fewer
\* than k lanes live, k lanes come to be live, or nothing is ready, or the width fell.
Ks == {k \in Widths : k > 0}
WidthIsFilled ==
    \A k \in Ks :
        (W >= k /\ ReadyN > 0 /\ Cardinality(Live) < k)
            ~> (Cardinality(Live) >= k \/ ReadyN = 0 \/ W < k)
\* The parked lane (#5724). A parked lane holds no card: it runs nothing.
ParkedRunsNothing == \A l \in Lanes : parked[l] => ~Holding(l)

\* An unparseable line is refused once per daemon life, never in a loop; a parked lane has
\* been refused once.
RefusedOnceNotALoop == \A l \in Lanes : refusals[l] <= 1 /\ (parked[l] => refusals[l] = 1)

\* A lane refused an unparseable line sends no ask (take, held) until its daemon restarts.
ParkedNeverAsks == ~askedAfter

\* The record a parked lane refused is dropped: the lane is silent, so the gone bound passes
\* (or a clear drops it, or the lane is restarted and asks again).
ParkedRecordIsGone ==
    \A l \in Lanes : (parked[l] /\ srv[l].card # None) ~> (srv[l].card = None \/ ~parked[l])

\* A parked lane resumes after a restart: a restarted lane asks again (Held, its first word),
\* unless it goes down again first.
ResumesAfterRestart ==
    \A l \in Lanes : (alive[l] /\ lc[l].st = "restart") ~> (~alive[l] \/ lc[l].st # "restart")
=============================================================================
